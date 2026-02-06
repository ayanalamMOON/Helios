package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/helios/helios/internal/atlas"
	"github.com/helios/helios/internal/auth"
	"github.com/helios/helios/internal/queue"
	"github.com/helios/helios/internal/sharding"
)

// DataLoader provides batched and cached data loading to solve N+1 query problems.
// It collects individual load requests over a short time window (wait duration),
// then executes a single batch fetch. Results are cached for the lifetime of the request.

// loaderKey is used as context key for DataLoaders
type loaderKey struct{}

// LoaderConfig configures DataLoader behavior
type LoaderConfig struct {
	// Wait is the amount of time to wait before triggering a batch
	Wait time.Duration
	// MaxBatch is the maximum number of keys to batch together (0 = unlimited)
	MaxBatch int
	// Cache enables result caching within request scope
	Cache bool
}

// DefaultLoaderConfig returns the default DataLoader configuration
func DefaultLoaderConfig() LoaderConfig {
	return LoaderConfig{
		Wait:     2 * time.Millisecond, // Wait 2ms to batch requests
		MaxBatch: 100,                  // Batch up to 100 keys
		Cache:    true,                 // Enable caching
	}
}

// Loaders contains all DataLoaders for a request
type Loaders struct {
	UserLoader     *UserLoader
	UserByName     *UserByNameLoader
	KeyLoader      *KeyLoader
	JobLoader      *JobLoader
	ShardNodeLoader *ShardNodeLoader
	
	// Dependencies for loading
	atlasStore   *atlas.Atlas
	shardedAtlas *atlas.ShardedAtlas
	authService  *auth.Service
	jobQueue     *queue.Queue
	shardManager *sharding.ShardManager
}

// NewLoaders creates a new set of DataLoaders for a request
func NewLoaders(
	atlasStore *atlas.Atlas,
	shardedAtlas *atlas.ShardedAtlas,
	authService *auth.Service,
	jobQueue *queue.Queue,
	shardManager *sharding.ShardManager,
) *Loaders {
	config := DefaultLoaderConfig()
	
	l := &Loaders{
		atlasStore:   atlasStore,
		shardedAtlas: shardedAtlas,
		authService:  authService,
		jobQueue:     jobQueue,
		shardManager: shardManager,
	}
	
	// Initialize all loaders
	l.UserLoader = NewUserLoader(config, l.batchLoadUsers)
	l.UserByName = NewUserByNameLoader(config, l.batchLoadUsersByName)
	l.KeyLoader = NewKeyLoader(config, l.batchLoadKeys)
	l.JobLoader = NewJobLoader(config, l.batchLoadJobs)
	l.ShardNodeLoader = NewShardNodeLoader(config, l.batchLoadShardNodes)
	
	return l
}

// ContextWithLoaders adds DataLoaders to context
func ContextWithLoaders(ctx context.Context, loaders *Loaders) context.Context {
	return context.WithValue(ctx, loaderKey{}, loaders)
}

// LoadersFromContext retrieves DataLoaders from context
func LoadersFromContext(ctx context.Context) *Loaders {
	loaders, _ := ctx.Value(loaderKey{}).(*Loaders)
	return loaders
}

// =============================================================================
// UserLoader - Loads users by ID
// =============================================================================

// UserResult represents a user load result
type UserResult struct {
	User  *User
	Error error
}

// UserLoader batches and caches user loads by ID
type UserLoader struct {
	config   LoaderConfig
	batchFn  func([]string) []*UserResult
	cache    map[string]*UserResult
	cacheMu  sync.RWMutex
	batch    []string
	batchMu  sync.Mutex
	resultCh map[string]chan *UserResult
	timer    *time.Timer
}

// NewUserLoader creates a new UserLoader
func NewUserLoader(config LoaderConfig, batchFn func([]string) []*UserResult) *UserLoader {
	return &UserLoader{
		config:   config,
		batchFn:  batchFn,
		cache:    make(map[string]*UserResult),
		resultCh: make(map[string]chan *UserResult),
	}
}

// Load loads a user by ID, batching with other requests
func (l *UserLoader) Load(ctx context.Context, userID string) (*User, error) {
	// Check cache first
	if l.config.Cache {
		l.cacheMu.RLock()
		if result, ok := l.cache[userID]; ok {
			l.cacheMu.RUnlock()
			return result.User, result.Error
		}
		l.cacheMu.RUnlock()
	}
	
	// Create result channel
	ch := make(chan *UserResult, 1)
	
	l.batchMu.Lock()
	
	// Add to batch
	l.batch = append(l.batch, userID)
	l.resultCh[userID] = ch
	
	// Start timer if this is the first item
	if len(l.batch) == 1 {
		l.timer = time.AfterFunc(l.config.Wait, l.executeBatch)
	}
	
	// Execute immediately if batch is full
	if l.config.MaxBatch > 0 && len(l.batch) >= l.config.MaxBatch {
		l.timer.Stop()
		go l.executeBatch()
	}
	
	l.batchMu.Unlock()
	
	// Wait for result
	select {
	case result := <-ch:
		return result.User, result.Error
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// LoadMany loads multiple users by IDs
func (l *UserLoader) LoadMany(ctx context.Context, userIDs []string) ([]*User, []error) {
	users := make([]*User, len(userIDs))
	errors := make([]error, len(userIDs))
	
	var wg sync.WaitGroup
	for i, id := range userIDs {
		wg.Add(1)
		go func(idx int, userID string) {
			defer wg.Done()
			users[idx], errors[idx] = l.Load(ctx, userID)
		}(i, id)
	}
	wg.Wait()
	
	return users, errors
}

// Prime adds a user to the cache
func (l *UserLoader) Prime(userID string, user *User, err error) {
	if l.config.Cache {
		l.cacheMu.Lock()
		l.cache[userID] = &UserResult{User: user, Error: err}
		l.cacheMu.Unlock()
	}
}

// Clear removes a user from the cache
func (l *UserLoader) Clear(userID string) {
	if l.config.Cache {
		l.cacheMu.Lock()
		delete(l.cache, userID)
		l.cacheMu.Unlock()
	}
}

// ClearAll clears the entire cache
func (l *UserLoader) ClearAll() {
	if l.config.Cache {
		l.cacheMu.Lock()
		l.cache = make(map[string]*UserResult)
		l.cacheMu.Unlock()
	}
}

func (l *UserLoader) executeBatch() {
	l.batchMu.Lock()
	
	if len(l.batch) == 0 {
		l.batchMu.Unlock()
		return
	}
	
	// Get batch and channels
	keys := l.batch
	channels := l.resultCh
	
	// Reset for next batch
	l.batch = nil
	l.resultCh = make(map[string]chan *UserResult)
	
	l.batchMu.Unlock()
	
	// Execute batch load
	results := l.batchFn(keys)
	
	// Cache and distribute results
	for i, key := range keys {
		result := results[i]
		
		// Cache result
		if l.config.Cache {
			l.cacheMu.Lock()
			l.cache[key] = result
			l.cacheMu.Unlock()
		}
		
		// Send result to waiting goroutine
		if ch, ok := channels[key]; ok {
			ch <- result
			close(ch)
		}
	}
}

// =============================================================================
// UserByNameLoader - Loads users by username
// =============================================================================

// UserByNameLoader batches and caches user loads by username
type UserByNameLoader struct {
	config   LoaderConfig
	batchFn  func([]string) []*UserResult
	cache    map[string]*UserResult
	cacheMu  sync.RWMutex
	batch    []string
	batchMu  sync.Mutex
	resultCh map[string]chan *UserResult
	timer    *time.Timer
}

// NewUserByNameLoader creates a new UserByNameLoader
func NewUserByNameLoader(config LoaderConfig, batchFn func([]string) []*UserResult) *UserByNameLoader {
	return &UserByNameLoader{
		config:   config,
		batchFn:  batchFn,
		cache:    make(map[string]*UserResult),
		resultCh: make(map[string]chan *UserResult),
	}
}

// Load loads a user by username
func (l *UserByNameLoader) Load(ctx context.Context, username string) (*User, error) {
	// Check cache first
	if l.config.Cache {
		l.cacheMu.RLock()
		if result, ok := l.cache[username]; ok {
			l.cacheMu.RUnlock()
			return result.User, result.Error
		}
		l.cacheMu.RUnlock()
	}
	
	ch := make(chan *UserResult, 1)
	
	l.batchMu.Lock()
	l.batch = append(l.batch, username)
	l.resultCh[username] = ch
	
	if len(l.batch) == 1 {
		l.timer = time.AfterFunc(l.config.Wait, l.executeBatch)
	}
	
	if l.config.MaxBatch > 0 && len(l.batch) >= l.config.MaxBatch {
		l.timer.Stop()
		go l.executeBatch()
	}
	
	l.batchMu.Unlock()
	
	select {
	case result := <-ch:
		return result.User, result.Error
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Prime adds a user to the cache
func (l *UserByNameLoader) Prime(username string, user *User, err error) {
	if l.config.Cache {
		l.cacheMu.Lock()
		l.cache[username] = &UserResult{User: user, Error: err}
		l.cacheMu.Unlock()
	}
}

// Clear removes a user from the cache
func (l *UserByNameLoader) Clear(username string) {
	if l.config.Cache {
		l.cacheMu.Lock()
		delete(l.cache, username)
		l.cacheMu.Unlock()
	}
}

func (l *UserByNameLoader) executeBatch() {
	l.batchMu.Lock()
	
	if len(l.batch) == 0 {
		l.batchMu.Unlock()
		return
	}
	
	keys := l.batch
	channels := l.resultCh
	l.batch = nil
	l.resultCh = make(map[string]chan *UserResult)
	
	l.batchMu.Unlock()
	
	results := l.batchFn(keys)
	
	for i, key := range keys {
		result := results[i]
		
		if l.config.Cache {
			l.cacheMu.Lock()
			l.cache[key] = result
			l.cacheMu.Unlock()
		}
		
		if ch, ok := channels[key]; ok {
			ch <- result
			close(ch)
		}
	}
}

// =============================================================================
// KeyLoader - Loads key-value pairs by key
// =============================================================================

// KeyResult represents a key load result
type KeyResult struct {
	KVPair *KVPair
	Error  error
}

// KeyLoader batches and caches key-value loads
type KeyLoader struct {
	config   LoaderConfig
	batchFn  func([]string) []*KeyResult
	cache    map[string]*KeyResult
	cacheMu  sync.RWMutex
	batch    []string
	batchMu  sync.Mutex
	resultCh map[string]chan *KeyResult
	timer    *time.Timer
}

// NewKeyLoader creates a new KeyLoader
func NewKeyLoader(config LoaderConfig, batchFn func([]string) []*KeyResult) *KeyLoader {
	return &KeyLoader{
		config:   config,
		batchFn:  batchFn,
		cache:    make(map[string]*KeyResult),
		resultCh: make(map[string]chan *KeyResult),
	}
}

// Load loads a key-value pair by key
func (l *KeyLoader) Load(ctx context.Context, key string) (*KVPair, error) {
	if l.config.Cache {
		l.cacheMu.RLock()
		if result, ok := l.cache[key]; ok {
			l.cacheMu.RUnlock()
			return result.KVPair, result.Error
		}
		l.cacheMu.RUnlock()
	}
	
	ch := make(chan *KeyResult, 1)
	
	l.batchMu.Lock()
	l.batch = append(l.batch, key)
	l.resultCh[key] = ch
	
	if len(l.batch) == 1 {
		l.timer = time.AfterFunc(l.config.Wait, l.executeBatch)
	}
	
	if l.config.MaxBatch > 0 && len(l.batch) >= l.config.MaxBatch {
		l.timer.Stop()
		go l.executeBatch()
	}
	
	l.batchMu.Unlock()
	
	select {
	case result := <-ch:
		return result.KVPair, result.Error
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// LoadMany loads multiple key-value pairs
func (l *KeyLoader) LoadMany(ctx context.Context, keys []string) ([]*KVPair, []error) {
	pairs := make([]*KVPair, len(keys))
	errors := make([]error, len(keys))
	
	var wg sync.WaitGroup
	for i, key := range keys {
		wg.Add(1)
		go func(idx int, k string) {
			defer wg.Done()
			pairs[idx], errors[idx] = l.Load(ctx, k)
		}(i, key)
	}
	wg.Wait()
	
	return pairs, errors
}

// Prime adds a key-value pair to the cache
func (l *KeyLoader) Prime(key string, kv *KVPair, err error) {
	if l.config.Cache {
		l.cacheMu.Lock()
		l.cache[key] = &KeyResult{KVPair: kv, Error: err}
		l.cacheMu.Unlock()
	}
}

// Clear removes a key from the cache
func (l *KeyLoader) Clear(key string) {
	if l.config.Cache {
		l.cacheMu.Lock()
		delete(l.cache, key)
		l.cacheMu.Unlock()
	}
}

func (l *KeyLoader) executeBatch() {
	l.batchMu.Lock()
	
	if len(l.batch) == 0 {
		l.batchMu.Unlock()
		return
	}
	
	keys := l.batch
	channels := l.resultCh
	l.batch = nil
	l.resultCh = make(map[string]chan *KeyResult)
	
	l.batchMu.Unlock()
	
	results := l.batchFn(keys)
	
	for i, key := range keys {
		result := results[i]
		
		if l.config.Cache {
			l.cacheMu.Lock()
			l.cache[key] = result
			l.cacheMu.Unlock()
		}
		
		if ch, ok := channels[key]; ok {
			ch <- result
			close(ch)
		}
	}
}

// =============================================================================
// JobLoader - Loads jobs by ID
// =============================================================================

// JobResult represents a job load result
type JobResult struct {
	Job   *Job
	Error error
}

// JobLoader batches and caches job loads
type JobLoader struct {
	config   LoaderConfig
	batchFn  func([]string) []*JobResult
	cache    map[string]*JobResult
	cacheMu  sync.RWMutex
	batch    []string
	batchMu  sync.Mutex
	resultCh map[string]chan *JobResult
	timer    *time.Timer
}

// NewJobLoader creates a new JobLoader
func NewJobLoader(config LoaderConfig, batchFn func([]string) []*JobResult) *JobLoader {
	return &JobLoader{
		config:   config,
		batchFn:  batchFn,
		cache:    make(map[string]*JobResult),
		resultCh: make(map[string]chan *JobResult),
	}
}

// Load loads a job by ID
func (l *JobLoader) Load(ctx context.Context, jobID string) (*Job, error) {
	if l.config.Cache {
		l.cacheMu.RLock()
		if result, ok := l.cache[jobID]; ok {
			l.cacheMu.RUnlock()
			return result.Job, result.Error
		}
		l.cacheMu.RUnlock()
	}
	
	ch := make(chan *JobResult, 1)
	
	l.batchMu.Lock()
	l.batch = append(l.batch, jobID)
	l.resultCh[jobID] = ch
	
	if len(l.batch) == 1 {
		l.timer = time.AfterFunc(l.config.Wait, l.executeBatch)
	}
	
	if l.config.MaxBatch > 0 && len(l.batch) >= l.config.MaxBatch {
		l.timer.Stop()
		go l.executeBatch()
	}
	
	l.batchMu.Unlock()
	
	select {
	case result := <-ch:
		return result.Job, result.Error
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Prime adds a job to the cache
func (l *JobLoader) Prime(jobID string, job *Job, err error) {
	if l.config.Cache {
		l.cacheMu.Lock()
		l.cache[jobID] = &JobResult{Job: job, Error: err}
		l.cacheMu.Unlock()
	}
}

// Clear removes a job from the cache
func (l *JobLoader) Clear(jobID string) {
	if l.config.Cache {
		l.cacheMu.Lock()
		delete(l.cache, jobID)
		l.cacheMu.Unlock()
	}
}

func (l *JobLoader) executeBatch() {
	l.batchMu.Lock()
	
	if len(l.batch) == 0 {
		l.batchMu.Unlock()
		return
	}
	
	keys := l.batch
	channels := l.resultCh
	l.batch = nil
	l.resultCh = make(map[string]chan *JobResult)
	
	l.batchMu.Unlock()
	
	results := l.batchFn(keys)
	
	for i, key := range keys {
		result := results[i]
		
		if l.config.Cache {
			l.cacheMu.Lock()
			l.cache[key] = result
			l.cacheMu.Unlock()
		}
		
		if ch, ok := channels[key]; ok {
			ch <- result
			close(ch)
		}
	}
}

// =============================================================================
// ShardNodeLoader - Loads shard nodes by ID
// =============================================================================

// ShardNodeResult represents a shard node load result
type ShardNodeResult struct {
	Node  *ShardNode
	Error error
}

// ShardNodeLoader batches and caches shard node loads
type ShardNodeLoader struct {
	config   LoaderConfig
	batchFn  func([]string) []*ShardNodeResult
	cache    map[string]*ShardNodeResult
	cacheMu  sync.RWMutex
	batch    []string
	batchMu  sync.Mutex
	resultCh map[string]chan *ShardNodeResult
	timer    *time.Timer
}

// NewShardNodeLoader creates a new ShardNodeLoader
func NewShardNodeLoader(config LoaderConfig, batchFn func([]string) []*ShardNodeResult) *ShardNodeLoader {
	return &ShardNodeLoader{
		config:   config,
		batchFn:  batchFn,
		cache:    make(map[string]*ShardNodeResult),
		resultCh: make(map[string]chan *ShardNodeResult),
	}
}

// Load loads a shard node by ID
func (l *ShardNodeLoader) Load(ctx context.Context, nodeID string) (*ShardNode, error) {
	if l.config.Cache {
		l.cacheMu.RLock()
		if result, ok := l.cache[nodeID]; ok {
			l.cacheMu.RUnlock()
			return result.Node, result.Error
		}
		l.cacheMu.RUnlock()
	}
	
	ch := make(chan *ShardNodeResult, 1)
	
	l.batchMu.Lock()
	l.batch = append(l.batch, nodeID)
	l.resultCh[nodeID] = ch
	
	if len(l.batch) == 1 {
		l.timer = time.AfterFunc(l.config.Wait, l.executeBatch)
	}
	
	if l.config.MaxBatch > 0 && len(l.batch) >= l.config.MaxBatch {
		l.timer.Stop()
		go l.executeBatch()
	}
	
	l.batchMu.Unlock()
	
	select {
	case result := <-ch:
		return result.Node, result.Error
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *ShardNodeLoader) executeBatch() {
	l.batchMu.Lock()
	
	if len(l.batch) == 0 {
		l.batchMu.Unlock()
		return
	}
	
	keys := l.batch
	channels := l.resultCh
	l.batch = nil
	l.resultCh = make(map[string]chan *ShardNodeResult)
	
	l.batchMu.Unlock()
	
	results := l.batchFn(keys)
	
	for i, key := range keys {
		result := results[i]
		
		if l.config.Cache {
			l.cacheMu.Lock()
			l.cache[key] = result
			l.cacheMu.Unlock()
		}
		
		if ch, ok := channels[key]; ok {
			ch <- result
			close(ch)
		}
	}
}

// =============================================================================
// Batch Loading Functions - Implementations
// =============================================================================

// batchLoadUsers loads multiple users by ID in a single batch
func (l *Loaders) batchLoadUsers(userIDs []string) []*UserResult {
	results := make([]*UserResult, len(userIDs))
	
	if l.authService == nil {
		for i := range results {
			results[i] = &UserResult{Error: fmt.Errorf("auth service not available")}
		}
		return results
	}
	
	for i, userID := range userIDs {
		authUser, err := l.authService.GetUser(userID)
		if err != nil {
			results[i] = &UserResult{Error: err}
		} else {
			results[i] = &UserResult{
				User: &User{
					ID:        authUser.ID,
					Username:  authUser.Username,
					CreatedAt: authUser.CreatedAt.Format(time.RFC3339),
					UpdatedAt: authUser.CreatedAt.Format(time.RFC3339),
				},
			}
		}
	}
	
	return results
}

// batchLoadUsersByName loads multiple users by username in a single batch
func (l *Loaders) batchLoadUsersByName(usernames []string) []*UserResult {
	results := make([]*UserResult, len(usernames))
	
	if l.authService == nil {
		for i := range results {
			results[i] = &UserResult{Error: fmt.Errorf("auth service not available")}
		}
		return results
	}
	
	for i, username := range usernames {
		authUser, err := l.authService.GetUserByUsername(username)
		if err != nil {
			results[i] = &UserResult{Error: err}
		} else {
			results[i] = &UserResult{
				User: &User{
					ID:        authUser.ID,
					Username:  authUser.Username,
					CreatedAt: authUser.CreatedAt.Format(time.RFC3339),
					UpdatedAt: authUser.CreatedAt.Format(time.RFC3339),
				},
			}
		}
	}
	
	return results
}

// batchLoadKeys loads multiple key-value pairs in a single batch
func (l *Loaders) batchLoadKeys(keys []string) []*KeyResult {
	results := make([]*KeyResult, len(keys))
	
	// Prefer sharded atlas if available
	if l.shardedAtlas != nil {
		for i, key := range keys {
			value, ok := l.shardedAtlas.Get(key)
			if !ok {
				results[i] = &KeyResult{Error: fmt.Errorf("key not found: %s", key)}
			} else {
				results[i] = &KeyResult{
					KVPair: &KVPair{
						Key:       key,
						Value:     string(value),
						CreatedAt: time.Now().Format(time.RFC3339),
					},
				}
			}
		}
	} else if l.atlasStore != nil {
		for i, key := range keys {
			value, ok := l.atlasStore.Get(key)
			if !ok {
				results[i] = &KeyResult{Error: fmt.Errorf("key not found: %s", key)}
			} else {
				results[i] = &KeyResult{
					KVPair: &KVPair{
						Key:       key,
						Value:     string(value),
						CreatedAt: time.Now().Format(time.RFC3339),
					},
				}
			}
		}
	} else {
		for i := range results {
			results[i] = &KeyResult{Error: fmt.Errorf("no store available")}
		}
	}
	
	return results
}

// batchLoadJobs loads multiple jobs by ID in a single batch
func (l *Loaders) batchLoadJobs(jobIDs []string) []*JobResult {
	results := make([]*JobResult, len(jobIDs))
	
	if l.jobQueue == nil {
		for i := range results {
			results[i] = &JobResult{Error: fmt.Errorf("job queue not available")}
		}
		return results
	}
	
	for i, jobID := range jobIDs {
		job, err := l.jobQueue.GetJob(jobID)
		if err != nil {
			results[i] = &JobResult{Error: err}
		} else {
			// Extract type from payload
			jobType := "unknown"
			if t, ok := job.Payload["type"].(string); ok {
				jobType = t
			}
			
			// Convert payload to JSON
			payloadBytes, _ := json.Marshal(job.Payload)
			
			// Extract priority from payload if available
			priority := int32(0)
			if p, ok := job.Payload["priority"].(float64); ok {
				priority = int32(p)
			}
			
			// Extract error from payload if available
			var errStr *string
			if e, ok := job.Payload["error"].(string); ok && e != "" {
				errStr = &e
			}
			
			results[i] = &JobResult{
				Job: &Job{
					ID:        job.ID,
					Type:      jobType,
					Payload:   string(payloadBytes),
					Status:    string(job.Status),
					Priority:  priority,
					Retries:   int32(job.Attempts),
					Error:     errStr,
					CreatedAt: job.CreatedAt.Format(time.RFC3339),
					UpdatedAt: job.LastUpdated.Format(time.RFC3339),
				},
			}
		}
	}
	
	return results
}

// batchLoadShardNodes loads multiple shard nodes by ID in a single batch
func (l *Loaders) batchLoadShardNodes(nodeIDs []string) []*ShardNodeResult {
	results := make([]*ShardNodeResult, len(nodeIDs))
	
	if l.shardManager == nil {
		for i := range results {
			results[i] = &ShardNodeResult{Error: fmt.Errorf("shard manager not available")}
		}
		return results
	}
	
	// Get all nodes once
	allNodes := l.shardManager.GetAllNodes()
	nodeMap := make(map[string]*sharding.NodeInfo)
	for _, node := range allNodes {
		nodeMap[node.NodeID] = node
	}
	
	for i, nodeID := range nodeIDs {
		if node, ok := nodeMap[nodeID]; ok {
			results[i] = &ShardNodeResult{
				Node: &ShardNode{
					NodeID:   node.NodeID,
					Address:  node.Address,
					Status:   node.Status,
					KeyCount: int32(node.KeyCount),
					LastSeen: node.LastSeen.Format(time.RFC3339),
				},
			}
		} else {
			results[i] = &ShardNodeResult{Error: fmt.Errorf("node not found: %s", nodeID)}
		}
	}
	
	return results
}
