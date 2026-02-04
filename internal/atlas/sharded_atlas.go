package atlas

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/helios/helios/internal/atlas/protocol"
	"github.com/helios/helios/internal/sharding"
)

// ShardedAtlas provides horizontal sharding across multiple Atlas instances
type ShardedAtlas struct {
	mu           sync.RWMutex
	shards       map[string]*Atlas // nodeID -> Atlas instance
	shardManager *sharding.ShardManager
	config       *ShardedConfig
	localNodeID  string // ID of the local node
	forwardFunc  ForwardFunc
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

// ShardedConfig holds configuration for sharded Atlas
type ShardedConfig struct {
	ShardConfig   *sharding.ShardConfig
	AtlasConfig   *Config
	LocalNodeID   string
	StatsInterval time.Duration
}

// ForwardFunc is used to forward requests to remote nodes
type ForwardFunc func(nodeID string, cmd *protocol.Command) (*protocol.Response, error)

// NewShardedAtlas creates a new sharded Atlas instance
func NewShardedAtlas(config *ShardedConfig, forwardFunc ForwardFunc) (*ShardedAtlas, error) {
	manager, err := sharding.NewShardManager(config.ShardConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create shard manager: %w", err)
	}

	sa := &ShardedAtlas{
		shards:       make(map[string]*Atlas),
		shardManager: manager,
		config:       config,
		localNodeID:  config.LocalNodeID,
		forwardFunc:  forwardFunc,
		stopCh:       make(chan struct{}),
	}

	// Create local Atlas instance
	localAtlas, err := New(config.AtlasConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create local Atlas: %w", err)
	}

	sa.shards[config.LocalNodeID] = localAtlas

	return sa, nil
}

// Start starts the sharded Atlas
func (sa *ShardedAtlas) Start(ctx context.Context) error {
	// Start shard manager
	if err := sa.shardManager.Start(ctx); err != nil {
		return fmt.Errorf("failed to start shard manager: %w", err)
	}

	// Start stats updater
	sa.wg.Add(1)
	go sa.statsUpdater(ctx)

	return nil
}

// Stop stops the sharded Atlas
func (sa *ShardedAtlas) Stop() error {
	close(sa.stopCh)
	sa.wg.Wait()

	sa.shardManager.Stop()

	sa.mu.Lock()
	defer sa.mu.Unlock()

	// Close all local shards
	for _, atlas := range sa.shards {
		if err := atlas.Close(); err != nil {
			return err
		}
	}

	return nil
}

// Handle processes a command (routes to appropriate shard)
func (sa *ShardedAtlas) Handle(cmd *protocol.Command) *protocol.Response {
	// Determine which node should handle this key
	nodeID, err := sa.shardManager.GetNodeForKey(cmd.Key)
	if err != nil {
		return protocol.NewErrorResponse(fmt.Errorf("failed to find node for key: %w", err))
	}

	// If it's the local node, handle locally
	if nodeID == sa.localNodeID {
		return sa.handleLocal(cmd)
	}

	// Otherwise, forward to remote node
	return sa.handleRemote(nodeID, cmd)
}

// handleLocal handles a command locally
func (sa *ShardedAtlas) handleLocal(cmd *protocol.Command) *protocol.Response {
	sa.mu.RLock()
	localAtlas, exists := sa.shards[sa.localNodeID]
	sa.mu.RUnlock()

	if !exists {
		return protocol.NewErrorResponse(fmt.Errorf("local shard not found"))
	}

	return localAtlas.Handle(cmd)
}

// handleRemote forwards a command to a remote node
func (sa *ShardedAtlas) handleRemote(nodeID string, cmd *protocol.Command) *protocol.Response {
	if sa.forwardFunc == nil {
		return protocol.NewErrorResponse(fmt.Errorf("forwarding not configured"))
	}

	resp, err := sa.forwardFunc(nodeID, cmd)
	if err != nil {
		return protocol.NewErrorResponse(fmt.Errorf("forward failed: %w", err))
	}

	return resp
}

// Get retrieves a value (routes to appropriate shard)
func (sa *ShardedAtlas) Get(key string) ([]byte, bool) {
	cmd := &protocol.Command{
		Type: "GET",
		Key:  key,
	}

	resp := sa.Handle(cmd)
	if !resp.OK {
		return nil, false
	}

	return []byte(resp.Value), true
}

// Set stores a key-value pair (routes to appropriate shard)
func (sa *ShardedAtlas) Set(key string, value []byte, ttl int64) {
	cmd := protocol.NewSetCommand(key, value, ttl)
	sa.Handle(cmd)
}

// Delete removes a key (routes to appropriate shard)
func (sa *ShardedAtlas) Delete(key string) bool {
	cmd := protocol.NewDelCommand(key)
	resp := sa.Handle(cmd)
	return resp.OK
}

// Scan scans for keys matching a prefix (queries all shards)
func (sa *ShardedAtlas) Scan(prefix string) []string {
	sa.mu.RLock()
	defer sa.mu.RUnlock()

	var allKeys []string
	for _, atlas := range sa.shards {
		keys := atlas.Scan(prefix)
		allKeys = append(allKeys, keys...)
	}

	return allKeys
}

// AddNode adds a new node to the cluster
func (sa *ShardedAtlas) AddNode(nodeID, address string, raftEnabled bool) error {
	return sa.shardManager.AddNode(nodeID, address, raftEnabled)
}

// RemoveNode removes a node from the cluster
func (sa *ShardedAtlas) RemoveNode(nodeID string) error {
	sa.mu.Lock()
	defer sa.mu.Unlock()

	// Remove from shard manager
	if err := sa.shardManager.RemoveNode(nodeID); err != nil {
		return err
	}

	// Close and remove local shard if exists
	if atlas, exists := sa.shards[nodeID]; exists {
		if err := atlas.Close(); err != nil {
			return err
		}
		delete(sa.shards, nodeID)
	}

	return nil
}

// MigrateKeys migrates keys from one node to another
func (sa *ShardedAtlas) MigrateKeys(sourceNode, targetNode, keyPattern string, rateLimit int) error {
	// Create migration task
	task, err := sa.shardManager.CreateMigrationTask(sourceNode, targetNode, keyPattern)
	if err != nil {
		return err
	}

	// Start migration in background
	sa.wg.Add(1)
	go sa.executeMigration(task, keyPattern, rateLimit)

	return nil
}

// executeMigration performs the actual key migration
func (sa *ShardedAtlas) executeMigration(task *sharding.MigrationTask, keyPattern string, rateLimit int) {
	defer sa.wg.Done()

	sa.mu.RLock()
	sourceAtlas, sourceExists := sa.shards[task.SourceNode]
	sa.mu.RUnlock()

	if !sourceExists {
		sa.shardManager.UpdateMigrationTask(task.ID, 0, 0, "failed",
			fmt.Errorf("source node not found"))
		return
	}

	// Update status to in_progress
	sa.shardManager.UpdateMigrationTask(task.ID, 0, 0, "in_progress", nil)

	// Get all keys from source
	keys := sourceAtlas.Scan(keyPattern)
	totalKeys := int64(len(keys))
	keysMoved := int64(0)

	// Rate limiter
	ticker := time.NewTicker(time.Second / time.Duration(rateLimit))
	defer ticker.Stop()

	// Migrate keys
	for _, key := range keys {
		select {
		case <-sa.stopCh:
			sa.shardManager.UpdateMigrationTask(task.ID, keysMoved, totalKeys, "cancelled", nil)
			return
		case <-ticker.C:
			// Get value from source
			value, ok := sourceAtlas.Get(key)
			if !ok {
				continue
			}

			// Set value on target (via forwarding)
			cmd := protocol.NewSetCommand(key, value, 0)
			resp, err := sa.forwardFunc(task.TargetNode, cmd)
			if err != nil || !resp.OK {
				continue
			}

			// Delete from source
			sourceAtlas.Delete(key)
			keysMoved++

			// Update progress periodically
			if keysMoved%100 == 0 {
				sa.shardManager.UpdateMigrationTask(task.ID, keysMoved, totalKeys, "in_progress", nil)
			}
		}
	}

	// Mark as completed
	sa.shardManager.UpdateMigrationTask(task.ID, keysMoved, totalKeys, "completed", nil)
}

// GetShardManager returns the shard manager
func (sa *ShardedAtlas) GetShardManager() *sharding.ShardManager {
	return sa.shardManager
}

// Stats returns statistics including sharding info
func (sa *ShardedAtlas) Stats() map[string]interface{} {
	sa.mu.RLock()
	localAtlas := sa.shards[sa.localNodeID]
	sa.mu.RUnlock()

	localStats := make(map[string]interface{})
	if localAtlas != nil {
		localStats = localAtlas.Stats()
	}

	clusterStats := sa.shardManager.GetClusterStats()

	return map[string]interface{}{
		"local":   localStats,
		"cluster": clusterStats,
	}
}

// statsUpdater periodically updates node statistics
func (sa *ShardedAtlas) statsUpdater(ctx context.Context) {
	defer sa.wg.Done()

	interval := sa.config.StatsInterval
	if interval == 0 {
		interval = 10 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sa.stopCh:
			return
		case <-ticker.C:
			sa.updateStats()
		}
	}
}

// updateStats updates statistics in the shard manager
func (sa *ShardedAtlas) updateStats() {
	sa.mu.RLock()
	localAtlas := sa.shards[sa.localNodeID]
	sa.mu.RUnlock()

	if localAtlas == nil {
		return
	}

	stats := localAtlas.Stats()
	keyCount := int64(0)
	if keys, ok := stats["keys"].(int); ok {
		keyCount = int64(keys)
	}

	sa.shardManager.UpdateNodeStats(sa.localNodeID, keyCount, false)
}

// GetAllNodes returns all nodes in the cluster
func (sa *ShardedAtlas) GetAllNodes() []*sharding.NodeInfo {
	return sa.shardManager.GetAllNodes()
}

// GetNodeForKey returns the node responsible for a given key
func (sa *ShardedAtlas) GetNodeForKey(key string) (string, error) {
	return sa.shardManager.GetNodeForKey(key)
}
