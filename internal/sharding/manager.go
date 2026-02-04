package sharding

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// ShardManager manages the distribution of data across multiple nodes
type ShardManager struct {
	mu                sync.RWMutex
	consistentHash    *ConsistentHash
	nodes             map[string]*NodeInfo
	config            *ShardConfig
	migrations        map[string]*MigrationTask
	rebalanceInterval time.Duration
	stopCh            chan struct{}
	wg                sync.WaitGroup
	migrationCounter  int64
}

// NewShardManager creates a new shard manager
func NewShardManager(config *ShardConfig) (*ShardManager, error) {
	if config == nil {
		config = DefaultShardConfig()
	}

	interval, err := time.ParseDuration(config.RebalanceInterval)
	if err != nil {
		interval = time.Hour
	}

	sm := &ShardManager{
		consistentHash:    NewConsistentHash(config.VirtualNodes),
		nodes:             make(map[string]*NodeInfo),
		config:            config,
		migrations:        make(map[string]*MigrationTask),
		rebalanceInterval: interval,
		stopCh:            make(chan struct{}),
	}

	return sm, nil
}

// Start starts the shard manager
func (sm *ShardManager) Start(ctx context.Context) error {
	if sm.config.AutoRebalance {
		sm.wg.Add(1)
		go sm.rebalanceLoop(ctx)
	}

	sm.wg.Add(1)
	go sm.healthCheckLoop(ctx)

	return nil
}

// Stop stops the shard manager
func (sm *ShardManager) Stop() {
	close(sm.stopCh)
	sm.wg.Wait()
}

// AddNode adds a node to the shard cluster
func (sm *ShardManager) AddNode(nodeID, address string, raftEnabled bool) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.nodes[nodeID]; exists {
		return fmt.Errorf("node %s already exists", nodeID)
	}

	// Add to consistent hash
	if err := sm.consistentHash.AddNode(nodeID); err != nil {
		return fmt.Errorf("failed to add node to hash ring: %w", err)
	}

	// Store node info
	sm.nodes[nodeID] = &NodeInfo{
		NodeID:      nodeID,
		Address:     address,
		Status:      "online",
		KeyCount:    0,
		LastSeen:    time.Now(),
		IsLeader:    false,
		RaftEnabled: raftEnabled,
	}

	log.Printf("[ShardManager] Added node %s at %s", nodeID, address)
	return nil
}

// RemoveNode removes a node from the shard cluster
func (sm *ShardManager) RemoveNode(nodeID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.nodes[nodeID]; !exists {
		return fmt.Errorf("node %s does not exist", nodeID)
	}

	// Remove from consistent hash
	if err := sm.consistentHash.RemoveNode(nodeID); err != nil {
		return fmt.Errorf("failed to remove node from hash ring: %w", err)
	}

	// Remove node info
	delete(sm.nodes, nodeID)

	log.Printf("[ShardManager] Removed node %s", nodeID)
	return nil
}

// GetNodeForKey returns the node responsible for a given key
func (sm *ShardManager) GetNodeForKey(key string) (string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if !sm.config.Enabled {
		// If sharding is disabled, return the first available node (sorted for consistency)
		if len(sm.nodes) == 0 {
			return "", fmt.Errorf("no nodes available")
		}
		// Get nodes in sorted order for consistency
		nodes := make([]string, 0, len(sm.nodes))
		for nodeID := range sm.nodes {
			nodes = append(nodes, nodeID)
		}
		// Sort to ensure consistent ordering
		sort.Strings(nodes)
		return nodes[0], nil
	}

	return sm.consistentHash.GetNode(key)
}

// GetNodesForKey returns multiple nodes for a key (for replication)
func (sm *ShardManager) GetNodesForKey(key string) ([]string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if !sm.config.Enabled {
		// If sharding is disabled, return all nodes
		nodes := make([]string, 0, len(sm.nodes))
		for nodeID := range sm.nodes {
			nodes = append(nodes, nodeID)
		}
		return nodes, nil
	}

	return sm.consistentHash.GetNodes(key, sm.config.ReplicationFactor)
}

// GetNodeAddress returns the address for a given node
func (sm *ShardManager) GetNodeAddress(nodeID string) (string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	node, exists := sm.nodes[nodeID]
	if !exists {
		return "", fmt.Errorf("node %s not found", nodeID)
	}

	return node.Address, nil
}

// GetAllNodes returns all nodes in the cluster
func (sm *ShardManager) GetAllNodes() []*NodeInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	nodes := make([]*NodeInfo, 0, len(sm.nodes))
	for _, node := range sm.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// UpdateNodeStats updates statistics for a node
func (sm *ShardManager) UpdateNodeStats(nodeID string, keyCount int64, isLeader bool) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	node, exists := sm.nodes[nodeID]
	if !exists {
		return fmt.Errorf("node %s not found", nodeID)
	}

	node.KeyCount = keyCount
	node.IsLeader = isLeader
	node.LastSeen = time.Now()
	node.Status = "online"

	return nil
}

// CreateMigrationTask creates a new data migration task
func (sm *ShardManager) CreateMigrationTask(sourceNode, targetNode, keyPattern string) (*MigrationTask, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.migrationCounter++
	taskID := fmt.Sprintf("mig-%d-%d", time.Now().UnixNano(), sm.migrationCounter)
	task := &MigrationTask{
		ID:         taskID,
		SourceNode: sourceNode,
		TargetNode: targetNode,
		KeyPattern: keyPattern,
		Status:     "pending",
		KeysMoved:  0,
		TotalKeys:  0,
		StartTime:  time.Now(),
	}

	sm.migrations[taskID] = task
	log.Printf("[ShardManager] Created migration task %s: %s -> %s", taskID, sourceNode, targetNode)
	return task, nil
}

// GetMigrationTask returns a migration task by ID
func (sm *ShardManager) GetMigrationTask(taskID string) (*MigrationTask, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	task, exists := sm.migrations[taskID]
	if !exists {
		return nil, fmt.Errorf("migration task %s not found", taskID)
	}

	return task, nil
}

// UpdateMigrationTask updates a migration task
func (sm *ShardManager) UpdateMigrationTask(taskID string, keysMoved, totalKeys int64, status string, err error) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	task, exists := sm.migrations[taskID]
	if !exists {
		return fmt.Errorf("migration task %s not found", taskID)
	}

	task.KeysMoved = keysMoved
	task.TotalKeys = totalKeys
	task.Status = status

	if status == "completed" || status == "failed" {
		task.EndTime = time.Now()
	}

	if err != nil {
		task.Error = err.Error()
	}

	return nil
}

// GetClusterStats returns cluster-wide statistics
func (sm *ShardManager) GetClusterStats() map[string]interface{} {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	totalKeys := int64(0)
	onlineNodes := 0

	for _, node := range sm.nodes {
		totalKeys += node.KeyCount
		if node.Status == "online" {
			onlineNodes++
		}
	}

	return map[string]interface{}{
		"total_nodes":        len(sm.nodes),
		"online_nodes":       onlineNodes,
		"total_keys":         totalKeys,
		"sharding_enabled":   sm.config.Enabled,
		"replication_factor": sm.config.ReplicationFactor,
		"active_migrations":  len(sm.migrations),
	}
}

// rebalanceLoop periodically checks if rebalancing is needed
func (sm *ShardManager) rebalanceLoop(ctx context.Context) {
	defer sm.wg.Done()

	ticker := time.NewTicker(sm.rebalanceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sm.stopCh:
			return
		case <-ticker.C:
			sm.checkRebalance()
		}
	}
}

// checkRebalance checks if rebalancing is needed and initiates it if necessary
func (sm *ShardManager) checkRebalance() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.nodes) < 2 {
		return
	}

	// Calculate average keys per node
	totalKeys := int64(0)
	onlineNodes := []*NodeInfo{}

	for _, node := range sm.nodes {
		if node.Status == "online" {
			totalKeys += node.KeyCount
			onlineNodes = append(onlineNodes, node)
		}
	}

	if len(onlineNodes) < 2 || totalKeys == 0 {
		return
	}

	avgKeys := totalKeys / int64(len(onlineNodes))
	threshold := float64(avgKeys) * 0.3 // 30% variance threshold

	// Find overloaded and underloaded nodes
	var overloaded, underloaded []*NodeInfo

	for _, node := range onlineNodes {
		diff := float64(node.KeyCount - avgKeys)
		absDiff := diff
		if absDiff < 0 {
			absDiff = -absDiff
		}

		if absDiff > threshold {
			if node.KeyCount > avgKeys {
				overloaded = append(overloaded, node)
			} else {
				underloaded = append(underloaded, node)
			}
		}
	}

	if len(overloaded) == 0 || len(underloaded) == 0 {
		return
	}

	log.Printf("[ShardManager] Rebalancing needed: %d overloaded, %d underloaded nodes (avg: %d keys, threshold: %.0f)",
		len(overloaded), len(underloaded), avgKeys, threshold)

	// Sort overloaded nodes by key count (descending)
	sort.Slice(overloaded, func(i, j int) bool {
		return overloaded[i].KeyCount > overloaded[j].KeyCount
	})

	// Sort underloaded nodes by key count (ascending)
	sort.Slice(underloaded, func(i, j int) bool {
		return underloaded[i].KeyCount < underloaded[j].KeyCount
	})

	// Create migration tasks to balance the load
	sm.executeRebalancing(overloaded, underloaded, avgKeys)
}

// healthCheckLoop periodically checks node health
func (sm *ShardManager) healthCheckLoop(ctx context.Context) {
	defer sm.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sm.stopCh:
			return
		case <-ticker.C:
			sm.checkNodeHealth()
		}
	}
}

// checkNodeHealth marks nodes as offline if they haven't been seen recently
func (sm *ShardManager) checkNodeHealth() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	timeout := 30 * time.Second
	now := time.Now()

	for _, node := range sm.nodes {
		if now.Sub(node.LastSeen) > timeout {
			if node.Status != "offline" {
				node.Status = "offline"
				log.Printf("[ShardManager] Node %s marked as offline", node.NodeID)
			}
		}
	}
}

// ExportState exports the shard manager state for persistence
func (sm *ShardManager) ExportState() ([]byte, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	state := map[string]interface{}{
		"nodes":      sm.nodes,
		"config":     sm.config,
		"migrations": sm.migrations,
	}

	return json.Marshal(state)
}

// ImportState imports shard manager state from persistence
func (sm *ShardManager) ImportState(data []byte) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var state map[string]interface{}
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	// Reconstruct nodes
	if nodesData, ok := state["nodes"].(map[string]interface{}); ok {
		for nodeID, nodeData := range nodesData {
			nodeBytes, _ := json.Marshal(nodeData)
			var node NodeInfo
			if err := json.Unmarshal(nodeBytes, &node); err == nil {
				sm.nodes[nodeID] = &node
				sm.consistentHash.AddNode(nodeID)
			}
		}
	}

	log.Printf("[ShardManager] Imported state: %d nodes", len(sm.nodes))
	return nil
}

// executeRebalancing creates migration tasks to balance load across nodes
func (sm *ShardManager) executeRebalancing(overloaded, underloaded []*NodeInfo, targetAvg int64) {
	// Pair up overloaded and underloaded nodes to create migration tasks
	migrations := 0
	maxMigrations := 5 // Limit concurrent migrations to avoid overwhelming the cluster

	for i := 0; i < len(overloaded) && i < len(underloaded) && migrations < maxMigrations; i++ {
		source := overloaded[i]
		target := underloaded[i]

		// Calculate how many keys to move
		sourceExcess := source.KeyCount - targetAvg
		targetDeficit := targetAvg - target.KeyCount

		// Move the minimum of excess and deficit (don't overshoot)
		keysToMove := sourceExcess
		if targetDeficit < sourceExcess {
			keysToMove = targetDeficit
		}

		// Only create migration if it's significant (at least 10% of target average)
		minSignificant := targetAvg / 10
		if minSignificant < 100 {
			minSignificant = 100
		}

		if keysToMove < minSignificant {
			continue
		}

		// Check if there's already an active migration between these nodes
		if sm.hasActiveMigration(source.NodeID, target.NodeID) {
			log.Printf("[ShardManager] Skipping migration %s -> %s: active migration already exists",
				source.NodeID, target.NodeID)
			continue
		}

		// Create migration task with pattern based on approximate key count
		// In practice, this would use more sophisticated key selection
		taskID := fmt.Sprintf("mig-%d", time.Now().UnixNano())
		task := &MigrationTask{
			ID:         taskID,
			SourceNode: source.NodeID,
			TargetNode: target.NodeID,
			KeyPattern: "*", // Pattern for all keys; could be refined
			Status:     "pending",
			KeysMoved:  0,
			TotalKeys:  keysToMove,
			StartTime:  time.Now(),
		}

		sm.migrations[taskID] = task
		migrations++

		log.Printf("[ShardManager] Created rebalancing migration %s: %s -> %s (target: %d keys)",
			taskID, source.NodeID, target.NodeID, keysToMove)
	}

	if migrations > 0 {
		log.Printf("[ShardManager] Initiated %d rebalancing migrations", migrations)
	}
}

// hasActiveMigration checks if there's an active migration between two nodes
func (sm *ShardManager) hasActiveMigration(sourceNode, targetNode string) bool {
	for _, task := range sm.migrations {
		if task.SourceNode == sourceNode && task.TargetNode == targetNode {
			if task.Status == "pending" || task.Status == "in_progress" {
				return true
			}
		}
	}
	return false
}

// TriggerManualRebalance manually triggers a rebalancing check
func (sm *ShardManager) TriggerManualRebalance() error {
	sm.checkRebalance()
	return nil
}

// CleanupCompletedMigrations removes completed or failed migrations from memory
func (sm *ShardManager) CleanupCompletedMigrations(olderThan time.Duration) int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cutoff := time.Now().Add(-olderThan)
	cleaned := 0

	for id, task := range sm.migrations {
		if (task.Status == "completed" || task.Status == "failed") && task.EndTime.Before(cutoff) {
			delete(sm.migrations, id)
			cleaned++
		}
	}

	if cleaned > 0 {
		log.Printf("[ShardManager] Cleaned up %d completed migrations", cleaned)
	}

	return cleaned
}

// GetActiveMigrations returns all active (pending or in-progress) migrations
func (sm *ShardManager) GetActiveMigrations() []*MigrationTask {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	active := []*MigrationTask{}
	for _, task := range sm.migrations {
		if task.Status == "pending" || task.Status == "in_progress" {
			active = append(active, task)
		}
	}

	return active
}

// GetAllMigrations returns all migrations
func (sm *ShardManager) GetAllMigrations() []*MigrationTask {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	all := make([]*MigrationTask, 0, len(sm.migrations))
	for _, task := range sm.migrations {
		all = append(all, task)
	}

	return all
}

// CancelMigration cancels an active migration
func (sm *ShardManager) CancelMigration(taskID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	task, exists := sm.migrations[taskID]
	if !exists {
		return fmt.Errorf("migration task %s not found", taskID)
	}

	if task.Status != "pending" && task.Status != "in_progress" {
		return fmt.Errorf("cannot cancel migration in status: %s", task.Status)
	}

	task.Status = "cancelled"
	task.EndTime = time.Now()
	task.Error = "cancelled by user"

	log.Printf("[ShardManager] Cancelled migration %s", taskID)
	return nil
}
