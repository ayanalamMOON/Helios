package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// HistoryEntry represents a single configuration change in history
type HistoryEntry struct {
	ID          string                 `json:"id" yaml:"id"`                             // Unique identifier (hash-based)
	Version     int                    `json:"version" yaml:"version"`                   // Sequential version number
	Timestamp   time.Time              `json:"timestamp" yaml:"timestamp"`               // When the change was made
	ChangeType  HistoryChangeType      `json:"change_type" yaml:"change_type"`           // Type of change
	Source      string                 `json:"source" yaml:"source"`                     // How the change was made
	User        string                 `json:"user,omitempty" yaml:"user"`               // Who made the change (if known)
	Description string                 `json:"description,omitempty" yaml:"description"` // Optional description
	ConfigHash  string                 `json:"config_hash" yaml:"config_hash"`           // Hash of the configuration
	Changes     []FieldChange          `json:"changes,omitempty" yaml:"changes"`         // What fields changed
	Snapshot    map[string]interface{} `json:"snapshot,omitempty" yaml:"-"`              // Full config snapshot (not persisted in index)
	Tags        []string               `json:"tags,omitempty" yaml:"tags"`               // Optional tags for organization
}

// HistoryChangeType indicates the type of configuration change
type HistoryChangeType string

const (
	ChangeTypeReload    HistoryChangeType = "reload"     // Configuration reloaded
	ChangeTypeMigration HistoryChangeType = "migration"  // Migration applied
	ChangeTypeRollback  HistoryChangeType = "rollback"   // Rollback performed
	ChangeTypeRestore   HistoryChangeType = "restore"    // Backup restored
	ChangeTypeManual    HistoryChangeType = "manual"     // Manual change via API
	ChangeTypeInitial   HistoryChangeType = "initial"    // Initial configuration load
	ChangeTypeFileWatch HistoryChangeType = "file_watch" // Automatic file watch reload
)

// HistorySource indicates how the change was triggered
const (
	SourceAPI       = "api"        // Changed via HTTP API
	SourceCLI       = "cli"        // Changed via CLI tool
	SourceFileWatch = "file_watch" // Automatic file watching
	SourceSignal    = "signal"     // SIGHUP signal
	SourceStartup   = "startup"    // Initial startup
	SourceMigration = "migration"  // Migration system
)

// HistoryQuery allows filtering history entries
type HistoryQuery struct {
	Limit      int               `json:"limit,omitempty"`       // Max entries to return
	Offset     int               `json:"offset,omitempty"`      // Offset for pagination
	StartTime  *time.Time        `json:"start_time,omitempty"`  // Filter by start time
	EndTime    *time.Time        `json:"end_time,omitempty"`    // Filter by end time
	ChangeType HistoryChangeType `json:"change_type,omitempty"` // Filter by change type
	Source     string            `json:"source,omitempty"`      // Filter by source
	User       string            `json:"user,omitempty"`        // Filter by user
	Tags       []string          `json:"tags,omitempty"`        // Filter by tags
}

// HistoryList contains paginated history results
type HistoryList struct {
	Entries []HistoryEntry `json:"entries"`
	Total   int            `json:"total"`
	Offset  int            `json:"offset"`
	Limit   int            `json:"limit"`
	HasMore bool           `json:"has_more"`
}

// HistoryComparison compares two history versions
type HistoryComparison struct {
	VersionA       int           `json:"version_a"`
	VersionB       int           `json:"version_b"`
	TimestampA     time.Time     `json:"timestamp_a"`
	TimestampB     time.Time     `json:"timestamp_b"`
	Changes        []FieldChange `json:"changes"`
	TotalChanges   int           `json:"total_changes"`
	AddedFields    []string      `json:"added_fields,omitempty"`
	RemovedFields  []string      `json:"removed_fields,omitempty"`
	ModifiedFields []string      `json:"modified_fields,omitempty"`
}

// HistoryStats provides statistics about configuration history
type HistoryStats struct {
	TotalEntries    int                       `json:"total_entries"`
	OldestEntry     *time.Time                `json:"oldest_entry,omitempty"`
	NewestEntry     *time.Time                `json:"newest_entry,omitempty"`
	CurrentVersion  int                       `json:"current_version"`
	ChangesByType   map[HistoryChangeType]int `json:"changes_by_type"`
	ChangesBySource map[string]int            `json:"changes_by_source"`
	AverageInterval string                    `json:"average_interval,omitempty"`
	StorageSize     int64                     `json:"storage_size_bytes"`
}

// HistoryManager manages configuration history
type HistoryManager struct {
	mu           sync.RWMutex
	historyDir   string
	indexPath    string
	entries      []HistoryEntry
	maxEntries   int
	autoSnapshot bool
}

// HistoryConfig configures the history manager
type HistoryConfig struct {
	HistoryDir   string // Directory to store history files
	MaxEntries   int    // Maximum history entries to keep (0 = unlimited)
	AutoSnapshot bool   // Whether to save full snapshots
}

// DefaultHistoryConfig returns default history configuration
func DefaultHistoryConfig() *HistoryConfig {
	return &HistoryConfig{
		HistoryDir:   ".helios/history",
		MaxEntries:   1000,
		AutoSnapshot: true,
	}
}

// NewHistoryManager creates a new history manager
func NewHistoryManager(configPath string, cfg *HistoryConfig) (*HistoryManager, error) {
	if cfg == nil {
		cfg = DefaultHistoryConfig()
	}

	// Determine history directory relative to config file
	configDir := filepath.Dir(configPath)
	historyDir := filepath.Join(configDir, cfg.HistoryDir)

	// Create history directory if it doesn't exist
	if err := os.MkdirAll(historyDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create history directory: %w", err)
	}

	hm := &HistoryManager{
		historyDir:   historyDir,
		indexPath:    filepath.Join(historyDir, "index.yaml"),
		entries:      make([]HistoryEntry, 0),
		maxEntries:   cfg.MaxEntries,
		autoSnapshot: cfg.AutoSnapshot,
	}

	// Load existing history index
	if err := hm.loadIndex(); err != nil {
		// If no history exists, that's fine
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load history index: %w", err)
		}
	}

	return hm, nil
}

// RecordChange records a configuration change in history
func (hm *HistoryManager) RecordChange(
	oldConfig, newConfig *Config,
	changeType HistoryChangeType,
	source string,
	user string,
	description string,
	tags []string,
) (*HistoryEntry, error) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Calculate config hash
	configHash := hm.calculateConfigHash(newConfig)

	// Compute changes between old and new config
	var changes []FieldChange
	if oldConfig != nil {
		changes = hm.computeChanges(oldConfig, newConfig)
	}

	// Determine version number
	version := 1
	if len(hm.entries) > 0 {
		version = hm.entries[len(hm.entries)-1].Version + 1
	}

	// Create entry
	entry := HistoryEntry{
		ID:          hm.generateEntryID(version, time.Now()),
		Version:     version,
		Timestamp:   time.Now(),
		ChangeType:  changeType,
		Source:      source,
		User:        user,
		Description: description,
		ConfigHash:  configHash,
		Changes:     changes,
		Tags:        tags,
	}

	// Save snapshot if enabled
	if hm.autoSnapshot {
		snapshot, err := hm.configToMap(newConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create snapshot: %w", err)
		}
		entry.Snapshot = snapshot

		// Save snapshot to file
		if err := hm.saveSnapshot(&entry); err != nil {
			return nil, fmt.Errorf("failed to save snapshot: %w", err)
		}
	}

	// Add to entries
	hm.entries = append(hm.entries, entry)

	// Enforce max entries limit
	if hm.maxEntries > 0 && len(hm.entries) > hm.maxEntries {
		// Remove oldest entries
		toRemove := len(hm.entries) - hm.maxEntries
		for i := 0; i < toRemove; i++ {
			// Delete snapshot file
			hm.deleteSnapshot(hm.entries[i].ID)
		}
		hm.entries = hm.entries[toRemove:]
	}

	// Save index
	if err := hm.saveIndex(); err != nil {
		return nil, fmt.Errorf("failed to save history index: %w", err)
	}

	return &entry, nil
}

// GetHistory returns history entries based on query
func (hm *HistoryManager) GetHistory(query *HistoryQuery) (*HistoryList, error) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if query == nil {
		query = &HistoryQuery{Limit: 50}
	}

	// Apply default limit
	if query.Limit <= 0 {
		query.Limit = 50
	}

	// Filter entries
	filtered := make([]HistoryEntry, 0)
	for i := len(hm.entries) - 1; i >= 0; i-- { // Newest first
		entry := hm.entries[i]

		// Apply filters
		if query.StartTime != nil && entry.Timestamp.Before(*query.StartTime) {
			continue
		}
		if query.EndTime != nil && entry.Timestamp.After(*query.EndTime) {
			continue
		}
		if query.ChangeType != "" && entry.ChangeType != query.ChangeType {
			continue
		}
		if query.Source != "" && entry.Source != query.Source {
			continue
		}
		if query.User != "" && entry.User != query.User {
			continue
		}
		if len(query.Tags) > 0 && !hm.hasAnyTag(entry.Tags, query.Tags) {
			continue
		}

		filtered = append(filtered, entry)
	}

	total := len(filtered)

	// Apply pagination
	start := query.Offset
	if start > len(filtered) {
		start = len(filtered)
	}

	end := start + query.Limit
	if end > len(filtered) {
		end = len(filtered)
	}

	result := filtered[start:end]

	return &HistoryList{
		Entries: result,
		Total:   total,
		Offset:  query.Offset,
		Limit:   query.Limit,
		HasMore: end < total,
	}, nil
}

// GetEntry returns a specific history entry by ID or version
func (hm *HistoryManager) GetEntry(idOrVersion string) (*HistoryEntry, error) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	for i := range hm.entries {
		entry := &hm.entries[i]
		if entry.ID == idOrVersion || fmt.Sprintf("%d", entry.Version) == idOrVersion {
			// Load snapshot if needed
			if entry.Snapshot == nil && hm.autoSnapshot {
				snapshot, err := hm.loadSnapshot(entry.ID)
				if err == nil {
					entry.Snapshot = snapshot
				}
			}
			return entry, nil
		}
	}

	return nil, fmt.Errorf("history entry not found: %s", idOrVersion)
}

// GetLatestEntry returns the most recent history entry
func (hm *HistoryManager) GetLatestEntry() (*HistoryEntry, error) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if len(hm.entries) == 0 {
		return nil, fmt.Errorf("no history entries")
	}

	entry := &hm.entries[len(hm.entries)-1]

	// Load snapshot if needed
	if entry.Snapshot == nil && hm.autoSnapshot {
		snapshot, err := hm.loadSnapshot(entry.ID)
		if err == nil {
			entry.Snapshot = snapshot
		}
	}

	return entry, nil
}

// GetEntryByVersion returns a history entry by version number
func (hm *HistoryManager) GetEntryByVersion(version int) (*HistoryEntry, error) {
	return hm.GetEntry(fmt.Sprintf("%d", version))
}

// Compare compares two history versions
func (hm *HistoryManager) Compare(versionA, versionB int) (*HistoryComparison, error) {
	entryA, err := hm.GetEntryByVersion(versionA)
	if err != nil {
		return nil, fmt.Errorf("version %d not found: %w", versionA, err)
	}

	entryB, err := hm.GetEntryByVersion(versionB)
	if err != nil {
		return nil, fmt.Errorf("version %d not found: %w", versionB, err)
	}

	// Load snapshots if not present
	if entryA.Snapshot == nil {
		snapshot, err := hm.loadSnapshot(entryA.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to load snapshot for version %d: %w", versionA, err)
		}
		entryA.Snapshot = snapshot
	}

	if entryB.Snapshot == nil {
		snapshot, err := hm.loadSnapshot(entryB.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to load snapshot for version %d: %w", versionB, err)
		}
		entryB.Snapshot = snapshot
	}

	// Compare snapshots
	changes, added, removed, modified := hm.compareSnapshots(entryA.Snapshot, entryB.Snapshot)

	return &HistoryComparison{
		VersionA:       versionA,
		VersionB:       versionB,
		TimestampA:     entryA.Timestamp,
		TimestampB:     entryB.Timestamp,
		Changes:        changes,
		TotalChanges:   len(changes),
		AddedFields:    added,
		RemovedFields:  removed,
		ModifiedFields: modified,
	}, nil
}

// GetStats returns history statistics
func (hm *HistoryManager) GetStats() (*HistoryStats, error) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	stats := &HistoryStats{
		TotalEntries:    len(hm.entries),
		ChangesByType:   make(map[HistoryChangeType]int),
		ChangesBySource: make(map[string]int),
	}

	if len(hm.entries) == 0 {
		return stats, nil
	}

	// Calculate stats
	stats.OldestEntry = &hm.entries[0].Timestamp
	stats.NewestEntry = &hm.entries[len(hm.entries)-1].Timestamp
	stats.CurrentVersion = hm.entries[len(hm.entries)-1].Version

	var totalInterval time.Duration
	for i, entry := range hm.entries {
		stats.ChangesByType[entry.ChangeType]++
		stats.ChangesBySource[entry.Source]++

		if i > 0 {
			interval := entry.Timestamp.Sub(hm.entries[i-1].Timestamp)
			totalInterval += interval
		}
	}

	if len(hm.entries) > 1 {
		avgInterval := totalInterval / time.Duration(len(hm.entries)-1)
		stats.AverageInterval = avgInterval.Round(time.Second).String()
	}

	// Calculate storage size
	stats.StorageSize = hm.calculateStorageSize()

	return stats, nil
}

// GetSnapshot returns the configuration snapshot for a specific version
func (hm *HistoryManager) GetSnapshot(version int) (map[string]interface{}, error) {
	entry, err := hm.GetEntryByVersion(version)
	if err != nil {
		return nil, err
	}

	if entry.Snapshot != nil {
		return entry.Snapshot, nil
	}

	// Try to load from file
	return hm.loadSnapshot(entry.ID)
}

// RestoreVersion restores configuration to a specific version
func (hm *HistoryManager) RestoreVersion(version int, configPath string) error {
	snapshot, err := hm.GetSnapshot(version)
	if err != nil {
		return fmt.Errorf("failed to get snapshot: %w", err)
	}

	// Convert to YAML
	data, err := yaml.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %w", err)
	}

	// Write to temp file first
	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, configPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename config file: %w", err)
	}

	return nil
}

// PurgeOldEntries removes entries older than the specified duration
func (hm *HistoryManager) PurgeOldEntries(olderThan time.Duration) (int, error) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	cutoff := time.Now().Add(-olderThan)
	purged := 0

	newEntries := make([]HistoryEntry, 0, len(hm.entries))
	for _, entry := range hm.entries {
		if entry.Timestamp.Before(cutoff) {
			// Delete snapshot
			hm.deleteSnapshot(entry.ID)
			purged++
		} else {
			newEntries = append(newEntries, entry)
		}
	}

	hm.entries = newEntries

	// Save updated index
	if err := hm.saveIndex(); err != nil {
		return purged, fmt.Errorf("failed to save index after purge: %w", err)
	}

	return purged, nil
}

// Clear removes all history entries
func (hm *HistoryManager) Clear() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Delete all snapshot files
	for _, entry := range hm.entries {
		hm.deleteSnapshot(entry.ID)
	}

	hm.entries = make([]HistoryEntry, 0)

	// Save empty index
	return hm.saveIndex()
}

// Helper functions

func (hm *HistoryManager) loadIndex() error {
	data, err := os.ReadFile(hm.indexPath)
	if err != nil {
		return err
	}

	var entries []HistoryEntry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("failed to parse history index: %w", err)
	}

	hm.entries = entries
	return nil
}

func (hm *HistoryManager) saveIndex() error {
	// Create entries without snapshots for index
	indexEntries := make([]HistoryEntry, len(hm.entries))
	for i, entry := range hm.entries {
		indexEntries[i] = entry
		indexEntries[i].Snapshot = nil // Don't save snapshots in index
	}

	data, err := yaml.Marshal(indexEntries)
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}

	tmpPath := hm.indexPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}

	return os.Rename(tmpPath, hm.indexPath)
}

func (hm *HistoryManager) saveSnapshot(entry *HistoryEntry) error {
	if entry.Snapshot == nil {
		return nil
	}

	snapshotPath := filepath.Join(hm.historyDir, fmt.Sprintf("snapshot_%s.yaml", entry.ID))

	data, err := yaml.Marshal(entry.Snapshot)
	if err != nil {
		return err
	}

	return os.WriteFile(snapshotPath, data, 0600)
}

func (hm *HistoryManager) loadSnapshot(id string) (map[string]interface{}, error) {
	snapshotPath := filepath.Join(hm.historyDir, fmt.Sprintf("snapshot_%s.yaml", id))

	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, err
	}

	var snapshot map[string]interface{}
	if err := yaml.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}

	return snapshot, nil
}

func (hm *HistoryManager) deleteSnapshot(id string) {
	snapshotPath := filepath.Join(hm.historyDir, fmt.Sprintf("snapshot_%s.yaml", id))
	os.Remove(snapshotPath)
}

func (hm *HistoryManager) generateEntryID(version int, timestamp time.Time) string {
	data := fmt.Sprintf("%d-%s", version, timestamp.Format(time.RFC3339Nano))
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:8]) // First 8 bytes = 16 hex chars
}

func (hm *HistoryManager) calculateConfigHash(cfg *Config) string {
	data, _ := json.Marshal(cfg)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (hm *HistoryManager) configToMap(cfg *Config) (map[string]interface{}, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (hm *HistoryManager) computeChanges(oldCfg, newCfg *Config) []FieldChange {
	changes := make([]FieldChange, 0)

	// Compare observability
	if oldCfg.Observability.LogLevel != newCfg.Observability.LogLevel {
		changes = append(changes, FieldChange{
			Field:    "log_level",
			OldValue: oldCfg.Observability.LogLevel,
			NewValue: newCfg.Observability.LogLevel,
			Category: "observability",
		})
	}
	if oldCfg.Observability.MetricsEnabled != newCfg.Observability.MetricsEnabled {
		changes = append(changes, FieldChange{
			Field:    "metrics_enabled",
			OldValue: oldCfg.Observability.MetricsEnabled,
			NewValue: newCfg.Observability.MetricsEnabled,
			Category: "observability",
		})
	}
	if oldCfg.Observability.MetricsPort != newCfg.Observability.MetricsPort {
		changes = append(changes, FieldChange{
			Field:    "metrics_port",
			OldValue: oldCfg.Observability.MetricsPort,
			NewValue: newCfg.Observability.MetricsPort,
			Category: "observability",
		})
	}
	if oldCfg.Observability.TracingEnabled != newCfg.Observability.TracingEnabled {
		changes = append(changes, FieldChange{
			Field:    "tracing_enabled",
			OldValue: oldCfg.Observability.TracingEnabled,
			NewValue: newCfg.Observability.TracingEnabled,
			Category: "observability",
		})
	}
	if oldCfg.Observability.TracingEndpoint != newCfg.Observability.TracingEndpoint {
		changes = append(changes, FieldChange{
			Field:    "tracing_endpoint",
			OldValue: oldCfg.Observability.TracingEndpoint,
			NewValue: newCfg.Observability.TracingEndpoint,
			Category: "observability",
		})
	}

	// Compare rate limiting
	if oldCfg.RateLimiting.Enabled != newCfg.RateLimiting.Enabled {
		changes = append(changes, FieldChange{
			Field:    "rate_limiting_enabled",
			OldValue: oldCfg.RateLimiting.Enabled,
			NewValue: newCfg.RateLimiting.Enabled,
			Category: "rate_limiting",
		})
	}
	if oldCfg.RateLimiting.RequestsPerSecond != newCfg.RateLimiting.RequestsPerSecond {
		changes = append(changes, FieldChange{
			Field:    "requests_per_second",
			OldValue: oldCfg.RateLimiting.RequestsPerSecond,
			NewValue: newCfg.RateLimiting.RequestsPerSecond,
			Category: "rate_limiting",
		})
	}
	if oldCfg.RateLimiting.BurstSize != newCfg.RateLimiting.BurstSize {
		changes = append(changes, FieldChange{
			Field:    "burst_size",
			OldValue: oldCfg.RateLimiting.BurstSize,
			NewValue: newCfg.RateLimiting.BurstSize,
			Category: "rate_limiting",
		})
	}

	// Compare timeouts
	if oldCfg.Timeouts.ReadTimeout != newCfg.Timeouts.ReadTimeout {
		changes = append(changes, FieldChange{
			Field:    "read_timeout",
			OldValue: oldCfg.Timeouts.ReadTimeout.String(),
			NewValue: newCfg.Timeouts.ReadTimeout.String(),
			Category: "timeouts",
		})
	}
	if oldCfg.Timeouts.WriteTimeout != newCfg.Timeouts.WriteTimeout {
		changes = append(changes, FieldChange{
			Field:    "write_timeout",
			OldValue: oldCfg.Timeouts.WriteTimeout.String(),
			NewValue: newCfg.Timeouts.WriteTimeout.String(),
			Category: "timeouts",
		})
	}
	if oldCfg.Timeouts.IdleTimeout != newCfg.Timeouts.IdleTimeout {
		changes = append(changes, FieldChange{
			Field:    "idle_timeout",
			OldValue: oldCfg.Timeouts.IdleTimeout.String(),
			NewValue: newCfg.Timeouts.IdleTimeout.String(),
			Category: "timeouts",
		})
	}
	if oldCfg.Timeouts.ShutdownTimeout != newCfg.Timeouts.ShutdownTimeout {
		changes = append(changes, FieldChange{
			Field:    "shutdown_timeout",
			OldValue: oldCfg.Timeouts.ShutdownTimeout.String(),
			NewValue: newCfg.Timeouts.ShutdownTimeout.String(),
			Category: "timeouts",
		})
	}

	// Compare performance
	if oldCfg.Performance.MaxConnections != newCfg.Performance.MaxConnections {
		changes = append(changes, FieldChange{
			Field:    "max_connections",
			OldValue: oldCfg.Performance.MaxConnections,
			NewValue: newCfg.Performance.MaxConnections,
			Category: "performance",
		})
	}
	if oldCfg.Performance.WorkerPoolSize != newCfg.Performance.WorkerPoolSize {
		changes = append(changes, FieldChange{
			Field:    "worker_pool_size",
			OldValue: oldCfg.Performance.WorkerPoolSize,
			NewValue: newCfg.Performance.WorkerPoolSize,
			Category: "performance",
		})
	}
	if oldCfg.Performance.QueueSize != newCfg.Performance.QueueSize {
		changes = append(changes, FieldChange{
			Field:    "queue_size",
			OldValue: oldCfg.Performance.QueueSize,
			NewValue: newCfg.Performance.QueueSize,
			Category: "performance",
		})
	}
	if oldCfg.Performance.SnapshotInterval != newCfg.Performance.SnapshotInterval {
		changes = append(changes, FieldChange{
			Field:    "snapshot_interval",
			OldValue: oldCfg.Performance.SnapshotInterval.String(),
			NewValue: newCfg.Performance.SnapshotInterval.String(),
			Category: "performance",
		})
	}
	if oldCfg.Performance.AOFSyncMode != newCfg.Performance.AOFSyncMode {
		changes = append(changes, FieldChange{
			Field:    "aof_sync_mode",
			OldValue: oldCfg.Performance.AOFSyncMode,
			NewValue: newCfg.Performance.AOFSyncMode,
			Category: "performance",
		})
	}

	return changes
}

func (hm *HistoryManager) compareSnapshots(a, b map[string]interface{}) ([]FieldChange, []string, []string, []string) {
	changes := make([]FieldChange, 0)
	added := make([]string, 0)
	removed := make([]string, 0)
	modified := make([]string, 0)

	// Flatten both maps for comparison
	flatA := flattenMap(a, "")
	flatB := flattenMap(b, "")

	// Find added and modified fields
	for key, valB := range flatB {
		if valA, exists := flatA[key]; exists {
			if fmt.Sprintf("%v", valA) != fmt.Sprintf("%v", valB) {
				modified = append(modified, key)
				changes = append(changes, FieldChange{
					Field:    key,
					OldValue: valA,
					NewValue: valB,
					Category: getCategoryFromKey(key),
				})
			}
		} else {
			added = append(added, key)
			changes = append(changes, FieldChange{
				Field:    key,
				OldValue: nil,
				NewValue: valB,
				Category: getCategoryFromKey(key),
			})
		}
	}

	// Find removed fields
	for key, valA := range flatA {
		if _, exists := flatB[key]; !exists {
			removed = append(removed, key)
			changes = append(changes, FieldChange{
				Field:    key,
				OldValue: valA,
				NewValue: nil,
				Category: getCategoryFromKey(key),
			})
		}
	}

	// Sort for consistent output
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(modified)

	return changes, added, removed, modified
}

func flattenMap(m map[string]interface{}, prefix string) map[string]interface{} {
	result := make(map[string]interface{})

	for key, val := range m {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		switch v := val.(type) {
		case map[string]interface{}:
			for k, v := range flattenMap(v, fullKey) {
				result[k] = v
			}
		default:
			result[fullKey] = val
		}
	}

	return result
}

func getCategoryFromKey(key string) string {
	if len(key) > 0 {
		parts := splitKey(key)
		if len(parts) > 0 {
			return parts[0]
		}
	}
	return "unknown"
}

func splitKey(key string) []string {
	result := make([]string, 0)
	current := ""
	for _, c := range key {
		if c == '.' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func (hm *HistoryManager) hasAnyTag(entryTags, queryTags []string) bool {
	for _, qt := range queryTags {
		for _, et := range entryTags {
			if et == qt {
				return true
			}
		}
	}
	return false
}

func (hm *HistoryManager) calculateStorageSize() int64 {
	var size int64

	// Index file
	if info, err := os.Stat(hm.indexPath); err == nil {
		size += info.Size()
	}

	// Snapshot files
	pattern := filepath.Join(hm.historyDir, "snapshot_*.yaml")
	matches, _ := filepath.Glob(pattern)
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil {
			size += info.Size()
		}
	}

	return size
}

// GetHistoryDir returns the history directory path
func (hm *HistoryManager) GetHistoryDir() string {
	return hm.historyDir
}

// GetEntryCount returns the number of history entries
func (hm *HistoryManager) GetEntryCount() int {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return len(hm.entries)
}
