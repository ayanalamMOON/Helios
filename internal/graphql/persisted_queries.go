package graphql

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// PersistedQueryConfig configures the persisted query store
type PersistedQueryConfig struct {
	// Enabled controls whether persisted queries are active
	Enabled bool `json:"enabled" yaml:"enabled"`

	// CacheSize is the maximum number of queries to cache (0 = unlimited)
	CacheSize int `json:"cacheSize" yaml:"cache_size"`

	// AllowAutoRegister enables Automatic Persisted Queries (APQ) protocol,
	// where clients can register queries by sending both hash and query text
	AllowAutoRegister bool `json:"allowAutoRegister" yaml:"allow_auto_register"`

	// RejectUnpersistedQueries when true, only persisted queries are allowed;
	// raw query strings are rejected. Use in production for security.
	RejectUnpersistedQueries bool `json:"rejectUnpersistedQueries" yaml:"reject_unpersisted_queries"`

	// PersistencePath is the file path for persisting queries to disk (empty = in-memory only)
	PersistencePath string `json:"persistencePath" yaml:"persistence_path"`

	// TTL is the time-to-live for auto-registered queries (0 = no expiry)
	TTL time.Duration `json:"ttl" yaml:"ttl"`

	// AllowManagementAPI enables the register/unregister mutations
	AllowManagementAPI bool `json:"allowManagementAPI" yaml:"allow_management_api"`

	// LogOperations logs persisted query operations for debugging
	LogOperations bool `json:"logOperations" yaml:"log_operations"`
}

// DefaultPersistedQueryConfig returns sensible defaults
func DefaultPersistedQueryConfig() *PersistedQueryConfig {
	return &PersistedQueryConfig{
		Enabled:                  true,
		CacheSize:                1000,
		AllowAutoRegister:        true,
		RejectUnpersistedQueries: false,
		PersistencePath:          "",
		TTL:                      0, // No expiry
		AllowManagementAPI:       true,
		LogOperations:            false,
	}
}

// persistedQueryEntry is a single cached query
type persistedQueryEntry struct {
	Query        string    `json:"query"`
	Hash         string    `json:"hash"`
	Name         string    `json:"name,omitempty"`
	RegisteredAt time.Time `json:"registeredAt"`
	LastUsedAt   time.Time `json:"lastUsedAt"`
	UseCount     int64     `json:"useCount"`
	AutoReg      bool      `json:"autoRegistered"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
}

// isExpired returns true if the entry has a TTL set and is past its expiration
func (e *persistedQueryEntry) isExpired() bool {
	if e.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(e.ExpiresAt)
}

// PersistedQueryStore is a thread-safe store for persisted queries
type PersistedQueryStore struct {
	mu      sync.RWMutex
	config  *PersistedQueryConfig
	queries map[string]*persistedQueryEntry // hash -> entry
	lruList []string                        // hashes in LRU order (most recent last)

	// Stats
	hits   int64
	misses int64
}

// NewPersistedQueryStore creates a new persisted query store
func NewPersistedQueryStore(config *PersistedQueryConfig) *PersistedQueryStore {
	if config == nil {
		config = DefaultPersistedQueryConfig()
	}
	s := &PersistedQueryStore{
		config:  config,
		queries: make(map[string]*persistedQueryEntry),
		lruList: make([]string, 0),
	}

	// Load persisted queries from disk if configured
	if config.PersistencePath != "" {
		_ = s.loadFromDisk()
	}

	return s
}

// --- Hash computation ---

// ComputeHash computes the SHA-256 hash of a query string (Apollo APQ compatible)
func ComputeHash(query string) string {
	h := sha256.Sum256([]byte(query))
	return hex.EncodeToString(h[:])
}

// --- Core operations ---

// Register adds a query to the store with an optional name.
// If the query already exists, it updates the entry.
func (s *PersistedQueryStore) Register(query string, name string) (string, error) {
	if query == "" {
		return "", fmt.Errorf("query cannot be empty")
	}

	hash := ComputeHash(query)

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.queries[hash]; ok {
		// Update name if provided and not already set
		if name != "" && existing.Name == "" {
			existing.Name = name
		}
		return hash, nil
	}

	entry := &persistedQueryEntry{
		Query:        query,
		Hash:         hash,
		Name:         name,
		RegisteredAt: time.Now(),
		LastUsedAt:   time.Now(),
		UseCount:     0,
		AutoReg:      false,
	}

	s.queries[hash] = entry
	s.lruList = append(s.lruList, hash)

	// Evict if cache is full
	s.evictIfNeeded()

	// Persist to disk if configured
	if s.config.PersistencePath != "" {
		_ = s.saveToDiskLocked()
	}

	return hash, nil
}

// RegisterWithHash registers a query with a pre-computed hash (for APQ protocol)
func (s *PersistedQueryStore) RegisterWithHash(hash string, query string) error {
	if hash == "" {
		return fmt.Errorf("hash cannot be empty")
	}
	if query == "" {
		return fmt.Errorf("query cannot be empty")
	}

	// Verify hash matches
	computed := ComputeHash(query)
	if computed != hash {
		return fmt.Errorf("provided hash does not match computed hash of query")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.queries[hash]; ok {
		return nil // Already registered
	}

	entry := &persistedQueryEntry{
		Query:        query,
		Hash:         hash,
		RegisteredAt: time.Now(),
		LastUsedAt:   time.Now(),
		UseCount:     0,
		AutoReg:      true,
	}

	if s.config.TTL > 0 {
		entry.ExpiresAt = time.Now().Add(s.config.TTL)
	}

	s.queries[hash] = entry
	s.lruList = append(s.lruList, hash)

	// Evict if cache is full
	s.evictIfNeeded()

	// Persist to disk if configured
	if s.config.PersistencePath != "" {
		_ = s.saveToDiskLocked()
	}

	return nil
}

// Lookup retrieves a query by its hash
func (s *PersistedQueryStore) Lookup(hash string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.queries[hash]
	if !ok {
		s.misses++
		return "", false
	}

	// Check expiration
	if entry.isExpired() {
		s.removeEntryLocked(hash)
		s.misses++
		return "", false
	}

	// Update LRU and usage stats
	entry.LastUsedAt = time.Now()
	entry.UseCount++
	s.touchLRU(hash)
	s.hits++

	return entry.Query, true
}

// Unregister removes a query by its hash
func (s *PersistedQueryStore) Unregister(hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.queries[hash]; !ok {
		return false
	}

	s.removeEntryLocked(hash)

	// Persist to disk if configured
	if s.config.PersistencePath != "" {
		_ = s.saveToDiskLocked()
	}

	return true
}

// Exists checks if a query hash is registered
func (s *PersistedQueryStore) Exists(hash string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.queries[hash]
	if !ok {
		return false
	}
	return !entry.isExpired()
}

// GetEntry returns the full entry for a hash
func (s *PersistedQueryStore) GetEntry(hash string) (*persistedQueryEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.queries[hash]
	if !ok || entry.isExpired() {
		return nil, false
	}

	// Return a copy to avoid race conditions
	copy := *entry
	return &copy, true
}

// Count returns the number of registered queries
func (s *PersistedQueryStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.queries)
}

// Clear removes all queries
func (s *PersistedQueryStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.queries = make(map[string]*persistedQueryEntry)
	s.lruList = make([]string, 0)
	s.hits = 0
	s.misses = 0

	if s.config.PersistencePath != "" {
		_ = s.saveToDiskLocked()
	}
}

// Stats returns usage statistics
func (s *PersistedQueryStore) Stats() PersistedQueryStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	autoRegCount := 0
	manualCount := 0
	expiredCount := 0

	for _, entry := range s.queries {
		if entry.isExpired() {
			expiredCount++
			continue
		}
		if entry.AutoReg {
			autoRegCount++
		} else {
			manualCount++
		}
	}

	return PersistedQueryStats{
		TotalQueries:       len(s.queries) - expiredCount,
		AutoRegistered:     autoRegCount,
		ManuallyRegistered: manualCount,
		CacheSize:          s.config.CacheSize,
		Hits:               s.hits,
		Misses:             s.misses,
		HitRate:            calculateHitRate(s.hits, s.misses),
	}
}

// PersistedQueryStats contains store statistics
type PersistedQueryStats struct {
	TotalQueries       int     `json:"totalQueries"`
	AutoRegistered     int     `json:"autoRegistered"`
	ManuallyRegistered int     `json:"manuallyRegistered"`
	CacheSize          int     `json:"cacheSize"`
	Hits               int64   `json:"hits"`
	Misses             int64   `json:"misses"`
	HitRate            float64 `json:"hitRate"`
}

func calculateHitRate(hits, misses int64) float64 {
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total) * 100.0
}

// ListQueries returns all registered (non-expired) queries
func (s *PersistedQueryStore) ListQueries(limit, offset int) []*PersistedQueryInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Collect non-expired entries
	entries := make([]*PersistedQueryInfo, 0, len(s.queries))
	for _, entry := range s.queries {
		if entry.isExpired() {
			continue
		}
		entries = append(entries, &PersistedQueryInfo{
			Hash:         entry.Hash,
			Name:         entry.Name,
			Query:        entry.Query,
			RegisteredAt: entry.RegisteredAt.Format(time.RFC3339),
			LastUsedAt:   entry.LastUsedAt.Format(time.RFC3339),
			UseCount:     entry.UseCount,
			AutoReg:      entry.AutoReg,
		})
	}

	// Sort by registration time (newest first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].RegisteredAt > entries[j].RegisteredAt
	})

	// Apply offset and limit
	if offset >= len(entries) {
		return []*PersistedQueryInfo{}
	}
	entries = entries[offset:]
	if limit > 0 && limit < len(entries) {
		entries = entries[:limit]
	}

	return entries
}

// PersistedQueryInfo is the public-facing query info
type PersistedQueryInfo struct {
	Hash         string `json:"hash"`
	Name         string `json:"name"`
	Query        string `json:"query"`
	RegisteredAt string `json:"registeredAt"`
	LastUsedAt   string `json:"lastUsedAt"`
	UseCount     int64  `json:"useCount"`
	AutoReg      bool   `json:"autoRegistered"`
}

// --- LRU management ---

func (s *PersistedQueryStore) touchLRU(hash string) {
	// Move hash to the end (most recently used)
	for i, h := range s.lruList {
		if h == hash {
			s.lruList = append(s.lruList[:i], s.lruList[i+1:]...)
			s.lruList = append(s.lruList, hash)
			return
		}
	}
}

func (s *PersistedQueryStore) evictIfNeeded() {
	if s.config.CacheSize <= 0 {
		return // Unlimited
	}

	for len(s.queries) > s.config.CacheSize && len(s.lruList) > 0 {
		// Evict the least recently used (first in list)
		oldest := s.lruList[0]
		s.removeEntryLocked(oldest)
	}
}

func (s *PersistedQueryStore) removeEntryLocked(hash string) {
	delete(s.queries, hash)
	for i, h := range s.lruList {
		if h == hash {
			s.lruList = append(s.lruList[:i], s.lruList[i+1:]...)
			return
		}
	}
}

// CleanupExpired removes all expired entries
func (s *PersistedQueryStore) CleanupExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for hash, entry := range s.queries {
		if entry.isExpired() {
			s.removeEntryLocked(hash)
			count++
		}
	}

	if count > 0 && s.config.PersistencePath != "" {
		_ = s.saveToDiskLocked()
	}

	return count
}

// --- Disk persistence ---

type persistedQueryDiskFormat struct {
	Version int                    `json:"version"`
	Queries []*persistedQueryEntry `json:"queries"`
}

func (s *PersistedQueryStore) saveToDiskLocked() error {
	if s.config.PersistencePath == "" {
		return nil
	}

	entries := make([]*persistedQueryEntry, 0, len(s.queries))
	for _, entry := range s.queries {
		if !entry.isExpired() {
			entries = append(entries, entry)
		}
	}

	data := &persistedQueryDiskFormat{
		Version: 1,
		Queries: entries,
	}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal persisted queries: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(s.config.PersistencePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write atomically
	tmpPath := s.config.PersistencePath + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.config.PersistencePath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

func (s *PersistedQueryStore) loadFromDisk() error {
	if s.config.PersistencePath == "" {
		return nil
	}

	raw, err := os.ReadFile(s.config.PersistencePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist yet, nothing to load
		}
		return fmt.Errorf("failed to read persisted queries: %w", err)
	}

	var data persistedQueryDiskFormat
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("failed to unmarshal persisted queries: %w", err)
	}

	for _, entry := range data.Queries {
		if !entry.isExpired() {
			s.queries[entry.Hash] = entry
			s.lruList = append(s.lruList, entry.Hash)
		}
	}

	return nil
}

// SaveToDisk persists the store to disk (thread-safe)
func (s *PersistedQueryStore) SaveToDisk() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveToDiskLocked()
}

// --- APQ (Automatic Persisted Queries) Protocol ---

// PersistedQueryExtension is the Apollo APQ extension format
type PersistedQueryExtension struct {
	Version    int    `json:"version"`
	Sha256Hash string `json:"sha256Hash"`
}

// APQRequest represents a request with APQ extensions
type APQRequest struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName,omitempty"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
	Extensions    map[string]interface{} `json:"extensions,omitempty"`
}

// APQResult is the result of processing an APQ request
type APQResult struct {
	Query         string // The resolved query string
	Hash          string // The query hash
	WasRegistered bool   // Whether a new query was registered in this request
	WasCacheHit   bool   // Whether the query came from cache
	Error         error  // Error if any
}

// ProcessAPQ processes an Automatic Persisted Query request.
// It follows the Apollo APQ protocol:
//  1. Client sends hash only → server looks up query
//  2. If not found, server responds with PERSISTED_QUERY_NOT_FOUND
//  3. Client sends hash + query → server registers and executes
func (s *PersistedQueryStore) ProcessAPQ(req *GraphQLRequest) *APQResult {
	// Check for persisted query extension
	ext := s.extractAPQExtension(req)
	if ext == nil {
		// No APQ extension — this is a normal request
		if s.config.RejectUnpersistedQueries && req.Query != "" {
			return &APQResult{
				Error: fmt.Errorf("unpersisted queries are not allowed; use the persisted query protocol"),
			}
		}
		return nil // Not an APQ request, continue normal flow
	}

	hash := ext.Sha256Hash
	if hash == "" {
		return &APQResult{
			Error: fmt.Errorf("persisted query extension missing sha256Hash"),
		}
	}

	// Case 1: Client sends hash only (no query text)
	if req.Query == "" {
		query, found := s.Lookup(hash)
		if !found {
			return &APQResult{
				Hash: hash,
				Error: &PersistedQueryNotFoundError{
					Hash: hash,
				},
			}
		}
		return &APQResult{
			Query:       query,
			Hash:        hash,
			WasCacheHit: true,
		}
	}

	// Case 2: Client sends hash + query (registration)
	if s.config.AllowAutoRegister {
		if err := s.RegisterWithHash(hash, req.Query); err != nil {
			return &APQResult{
				Hash:  hash,
				Error: fmt.Errorf("failed to register persisted query: %w", err),
			}
		}
		return &APQResult{
			Query:         req.Query,
			Hash:          hash,
			WasRegistered: true,
		}
	}

	// Auto-register disabled but query is provided — just look up and use if found
	if query, found := s.Lookup(hash); found {
		return &APQResult{
			Query:       query,
			Hash:        hash,
			WasCacheHit: true,
		}
	}

	// Not found and auto-register disabled
	return &APQResult{
		Hash:  hash,
		Error: fmt.Errorf("persisted query not found and auto-registration is disabled"),
	}
}

// extractAPQExtension extracts the persisted query extension from the request
func (s *PersistedQueryStore) extractAPQExtension(req *GraphQLRequest) *PersistedQueryExtension {
	if req == nil {
		return nil
	}

	// Check if extensions were parsed from the request
	// The extensions field is added to GraphQLRequest for this purpose
	extensions := getRequestExtensions(req)
	if extensions == nil {
		return nil
	}

	pqExt, ok := extensions["persistedQuery"]
	if !ok {
		return nil
	}

	// Handle already-typed or raw map
	switch v := pqExt.(type) {
	case map[string]interface{}:
		ext := &PersistedQueryExtension{}
		if version, ok := v["version"].(float64); ok {
			ext.Version = int(version)
		}
		if hash, ok := v["sha256Hash"].(string); ok {
			ext.Sha256Hash = hash
		}
		if ext.Version != 1 {
			return nil // Only version 1 is supported
		}
		return ext
	default:
		return nil
	}
}

// getRequestExtensions retrieves extensions from the request
func getRequestExtensions(req *GraphQLRequest) map[string]interface{} {
	if req.Extensions == nil {
		return nil
	}
	return req.Extensions
}

// PersistedQueryNotFoundError is returned when a hash has no registered query
type PersistedQueryNotFoundError struct {
	Hash string
}

func (e *PersistedQueryNotFoundError) Error() string {
	return "PersistedQueryNotFound"
}

// IsPersistedQueryNotFound checks if an error is a PersistedQueryNotFoundError
func IsPersistedQueryNotFound(err error) bool {
	_, ok := err.(*PersistedQueryNotFoundError)
	return ok
}

// --- Batch Registration ---

// RegisterBatch registers multiple queries at once
func (s *PersistedQueryStore) RegisterBatch(queries map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(queries))

	for name, query := range queries {
		hash, err := s.Register(query, name)
		if err != nil {
			return result, fmt.Errorf("failed to register query %q: %w", name, err)
		}
		result[name] = hash
	}

	return result, nil
}

// RegisterFromFile loads and registers queries from a JSON file
func (s *PersistedQueryStore) RegisterFromFile(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("failed to read queries file: %w", err)
	}

	var queries map[string]string
	if err := json.Unmarshal(raw, &queries); err != nil {
		return 0, fmt.Errorf("failed to parse queries file: %w", err)
	}

	count := 0
	for name, query := range queries {
		if _, err := s.Register(query, name); err != nil {
			return count, fmt.Errorf("failed to register query %q: %w", name, err)
		}
		count++
	}

	return count, nil
}

// --- Config management ---

// GetConfig returns the current configuration
func (s *PersistedQueryStore) GetConfig() *PersistedQueryConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return a copy
	c := *s.config
	return &c
}

// UpdateConfig updates the configuration
func (s *PersistedQueryStore) UpdateConfig(config *PersistedQueryConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = config
}

// --- Predefined common queries ---

// RegisterCommonQueries registers commonly used queries for Helios
func (s *PersistedQueryStore) RegisterCommonQueries() {
	commonQueries := map[string]string{
		"HealthCheck": `query HealthCheck {
  health {
    status
    version
    uptime
    components {
      name
      status
      message
    }
  }
}`,
		"GetKey": `query GetKey($key: String!) {
  get(key: $key) {
    key
    value
    ttl
  }
}`,
		"ListKeys": `query ListKeys($pattern: String, $limit: Int) {
  keys(pattern: $pattern, limit: $limit)
}`,
		"KeyExists": `query KeyExists($key: String!) {
  exists(key: $key)
}`,
		"SetKey": `mutation SetKey($input: SetInput!) {
  set(input: $input) {
    key
    value
    ttl
  }
}`,
		"DeleteKey": `mutation DeleteKey($key: String!) {
  delete(key: $key)
}`,
		"Login": `mutation Login($input: LoginInput!) {
  login(input: $input) {
    token
    refreshToken
    user {
      id
      username
    }
    expiresIn
  }
}`,
		"Register": `mutation Register($input: RegisterInput!) {
  register(input: $input) {
    token
    refreshToken
    user {
      id
      username
    }
    expiresIn
  }
}`,
		"CurrentUser": `query CurrentUser {
  me {
    id
    username
    email
    roles
    createdAt
  }
}`,
		"ClusterStatus": `query ClusterStatus {
  clusterStatus {
    healthy
    leader
    nodes {
      nodeId
      address
      state
    }
    raftEnabled
  }
}`,
		"RaftStatus": `query RaftStatus {
  raftStatus {
    nodeId
    state
    term
    leader
  }
}`,
		"ShardNodes": `query ShardNodes {
  shardNodes {
    nodeId
    address
    keyCount
    status
  }
}`,
		"ShardStats": `query ShardStats {
  shardStats {
    totalNodes
    onlineNodes
    totalKeys
    averageKeysPerNode
    activeMigrations
  }
}`,
		"SystemMetrics": `query SystemMetrics {
  metrics {
    requestsTotal
    requestsPerSecond
    averageLatency
    keysCount
    jobQueueDepth
    memoryUsage
  }
}`,
		"ListJobs": `query ListJobs($status: JobStatus, $limit: Int, $offset: Int) {
  jobs(status: $status, limit: $limit, offset: $offset) {
    jobs {
      id
      payload
      status
      attempts
      createdAt
    }
    totalCount
    hasMore
  }
}`,
		"EnqueueJob": `mutation EnqueueJob($input: EnqueueJobInput!) {
  enqueueJob(input: $input) {
    id
    payload
    status
    createdAt
  }
}`,
	}

	for name, query := range commonQueries {
		// Normalize whitespace for consistent hashing
		normalized := normalizeQuery(query)
		_, _ = s.Register(normalized, name)
	}
}

// normalizeQuery normalizes whitespace in a query for consistent hashing
func normalizeQuery(query string) string {
	// Trim leading/trailing whitespace
	query = strings.TrimSpace(query)

	// Collapse multiple whitespace into single spaces while preserving newlines for readability
	lines := strings.Split(query, "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}

	return strings.Join(normalized, "\n")
}
