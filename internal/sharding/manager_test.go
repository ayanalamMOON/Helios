package sharding

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestShardManager_AddNode(t *testing.T) {
	config := &ShardConfig{
		Enabled:           true,
		VirtualNodes:      10,
		ReplicationFactor: 1,
		MigrationRate:     1000,
		AutoRebalance:     false,
	}

	manager, err := NewShardManager(config)
	if err != nil {
		t.Fatalf("Failed to create shard manager: %v", err)
	}

	err = manager.AddNode("node-1", "localhost:6379", false)
	if err != nil {
		t.Fatalf("Failed to add node: %v", err)
	}

	nodes := manager.GetAllNodes()
	if len(nodes) != 1 {
		t.Errorf("Expected 1 node, got %d", len(nodes))
	}

	if nodes[0].NodeID != "node-1" {
		t.Errorf("Expected node-1, got %s", nodes[0].NodeID)
	}
}

func TestShardManager_RemoveNode(t *testing.T) {
	config := DefaultShardConfig()
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)

	err := manager.RemoveNode("node-1")
	if err != nil {
		t.Fatalf("Failed to remove node: %v", err)
	}

	nodes := manager.GetAllNodes()
	if len(nodes) != 1 {
		t.Errorf("Expected 1 node after removal, got %d", len(nodes))
	}
}

func TestShardManager_GetNodeForKey(t *testing.T) {
	config := DefaultShardConfig()
	config.Enabled = true
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)
	manager.AddNode("node-3", "localhost:6381", false)

	// Test that same key always maps to same node
	node1, err := manager.GetNodeForKey("test-key")
	if err != nil {
		t.Fatalf("Failed to get node for key: %v", err)
	}

	node2, err := manager.GetNodeForKey("test-key")
	if err != nil {
		t.Fatalf("Failed to get node for key: %v", err)
	}

	if node1 != node2 {
		t.Errorf("Key mapped to different nodes: %s vs %s", node1, node2)
	}
}

func TestShardManager_GetNodesForKey(t *testing.T) {
	config := &ShardConfig{
		Enabled:           true,
		VirtualNodes:      10,
		ReplicationFactor: 3,
		MigrationRate:     1000,
	}

	manager, _ := NewShardManager(config)
	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)
	manager.AddNode("node-3", "localhost:6381", false)

	nodes, err := manager.GetNodesForKey("test-key")
	if err != nil {
		t.Fatalf("Failed to get nodes for key: %v", err)
	}

	if len(nodes) != 3 {
		t.Errorf("Expected 3 replicas, got %d", len(nodes))
	}

	// Check all nodes are unique
	seen := make(map[string]bool)
	for _, node := range nodes {
		if seen[node] {
			t.Errorf("Duplicate node in replica set: %s", node)
		}
		seen[node] = true
	}
}

func TestShardManager_UpdateNodeStats(t *testing.T) {
	config := DefaultShardConfig()
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)

	err := manager.UpdateNodeStats("node-1", 1000, true)
	if err != nil {
		t.Fatalf("Failed to update node stats: %v", err)
	}

	nodes := manager.GetAllNodes()
	if nodes[0].KeyCount != 1000 {
		t.Errorf("Expected key count 1000, got %d", nodes[0].KeyCount)
	}

	if !nodes[0].IsLeader {
		t.Error("Expected node to be marked as leader")
	}
}

func TestShardManager_CreateMigrationTask(t *testing.T) {
	config := DefaultShardConfig()
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)

	task, err := manager.CreateMigrationTask("node-1", "node-2", "user:*")
	if err != nil {
		t.Fatalf("Failed to create migration task: %v", err)
	}

	if task.SourceNode != "node-1" {
		t.Errorf("Expected source node-1, got %s", task.SourceNode)
	}

	if task.TargetNode != "node-2" {
		t.Errorf("Expected target node-2, got %s", task.TargetNode)
	}

	if task.Status != "pending" {
		t.Errorf("Expected status pending, got %s", task.Status)
	}
}

func TestShardManager_GetClusterStats(t *testing.T) {
	config := DefaultShardConfig()
	config.Enabled = true
	config.ReplicationFactor = 3
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)
	manager.AddNode("node-3", "localhost:6381", false)

	manager.UpdateNodeStats("node-1", 1000, true)
	manager.UpdateNodeStats("node-2", 2000, false)
	manager.UpdateNodeStats("node-3", 1500, false)

	stats := manager.GetClusterStats()

	if stats["total_nodes"] != 3 {
		t.Errorf("Expected 3 total nodes, got %v", stats["total_nodes"])
	}

	if stats["online_nodes"] != 3 {
		t.Errorf("Expected 3 online nodes, got %v", stats["online_nodes"])
	}

	if stats["total_keys"] != int64(4500) {
		t.Errorf("Expected 4500 total keys, got %v", stats["total_keys"])
	}

	if stats["sharding_enabled"] != true {
		t.Error("Expected sharding to be enabled")
	}

	if stats["replication_factor"] != 3 {
		t.Errorf("Expected replication factor 3, got %v", stats["replication_factor"])
	}
}

func TestShardManager_NodeHealthCheck(t *testing.T) {
	config := DefaultShardConfig()
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.UpdateNodeStats("node-1", 100, false)

	// Initially node should be online
	nodes := manager.GetAllNodes()
	if nodes[0].Status != "online" {
		t.Errorf("Expected node to be online, got %s", nodes[0].Status)
	}

	// Manually set last seen to long ago
	manager.mu.Lock()
	manager.nodes["node-1"].LastSeen = time.Now().Add(-2 * time.Minute)
	manager.mu.Unlock()

	// Run health check
	manager.checkNodeHealth()

	// Node should now be offline
	nodes = manager.GetAllNodes()
	if nodes[0].Status != "offline" {
		t.Errorf("Expected node to be offline, got %s", nodes[0].Status)
	}
}

func TestShardManager_DisabledSharding(t *testing.T) {
	config := DefaultShardConfig()
	config.Enabled = false // Disable sharding
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)

	// With sharding disabled, should return any available node
	// But it should be consistent for the same key
	results := make(map[string]int)

	for i := 0; i < 10; i++ {
		node, err := manager.GetNodeForKey("test-key")
		if err != nil {
			t.Fatalf("Failed to get node: %v", err)
		}
		results[node]++
	}

	// Should return one of the available nodes consistently
	if len(results) != 1 {
		t.Errorf("Expected consistent node with sharding disabled, got %d different nodes", len(results))
	}
}

func TestShardManager_ExportImportState(t *testing.T) {
	config := DefaultShardConfig()
	config.Enabled = true
	manager, _ := NewShardManager(config)

	// Add some nodes
	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)
	manager.UpdateNodeStats("node-1", 1000, true)

	// Export state
	data, err := manager.ExportState()
	if err != nil {
		t.Fatalf("Failed to export state: %v", err)
	}

	// Create new manager and import state
	manager2, _ := NewShardManager(config)
	err = manager2.ImportState(data)
	if err != nil {
		t.Fatalf("Failed to import state: %v", err)
	}

	// Verify imported state
	nodes := manager2.GetAllNodes()
	if len(nodes) != 2 {
		t.Errorf("Expected 2 nodes after import, got %d", len(nodes))
	}
}

func TestShardManager_AutoRebalancing(t *testing.T) {
	config := &ShardConfig{
		Enabled:           true,
		VirtualNodes:      10,
		ReplicationFactor: 1,
		AutoRebalance:     false, // We'll trigger manually
		MigrationRate:     1000,
	}

	manager, _ := NewShardManager(config)

	// Add 3 nodes
	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)
	manager.AddNode("node-3", "localhost:6381", false)

	// Set up unbalanced load
	manager.UpdateNodeStats("node-1", 10000, false) // Heavily loaded
	manager.UpdateNodeStats("node-2", 1000, false)  // Lightly loaded
	manager.UpdateNodeStats("node-3", 1000, false)  // Lightly loaded

	// Trigger rebalancing
	manager.checkRebalance()

	// Check that migration tasks were created
	migrations := manager.GetActiveMigrations()
	if len(migrations) == 0 {
		t.Error("Expected migration tasks to be created during rebalancing")
	}

	// Verify migration is from overloaded to underloaded node
	for _, migration := range migrations {
		if migration.SourceNode != "node-1" {
			t.Errorf("Expected migration from node-1, got %s", migration.SourceNode)
		}
		if migration.Status != "pending" {
			t.Errorf("Expected pending status, got %s", migration.Status)
		}
	}
}

func TestShardManager_RebalancingThreshold(t *testing.T) {
	config := DefaultShardConfig()
	config.Enabled = true
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)
	manager.AddNode("node-3", "localhost:6381", false)

	// Set up balanced load (within 30% threshold)
	manager.UpdateNodeStats("node-1", 1000, false)
	manager.UpdateNodeStats("node-2", 1100, false)
	manager.UpdateNodeStats("node-3", 1200, false)

	// Trigger rebalancing
	manager.checkRebalance()

	// Should NOT create migrations (within threshold)
	migrations := manager.GetActiveMigrations()
	if len(migrations) != 0 {
		t.Errorf("Expected no migrations for balanced load, got %d", len(migrations))
	}
}

func TestShardManager_TriggerManualRebalance(t *testing.T) {
	config := DefaultShardConfig()
	config.Enabled = true
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)

	// Set up unbalanced load
	manager.UpdateNodeStats("node-1", 5000, false)
	manager.UpdateNodeStats("node-2", 500, false)

	// Manually trigger rebalancing
	err := manager.TriggerManualRebalance()
	if err != nil {
		t.Fatalf("Failed to trigger manual rebalance: %v", err)
	}

	// Check migrations were created
	migrations := manager.GetActiveMigrations()
	if len(migrations) == 0 {
		t.Error("Expected migrations after manual rebalance trigger")
	}
}

func TestShardManager_HasActiveMigration(t *testing.T) {
	config := DefaultShardConfig()
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)

	// Create a migration task
	task, _ := manager.CreateMigrationTask("node-1", "node-2", "*")

	// Check for active migration
	manager.mu.Lock()
	hasActive := manager.hasActiveMigration("node-1", "node-2")
	manager.mu.Unlock()

	if !hasActive {
		t.Error("Expected active migration to be detected")
	}

	// Complete the migration
	manager.UpdateMigrationTask(task.ID, 1000, 1000, "completed", nil)

	// Should no longer be active
	manager.mu.Lock()
	hasActive = manager.hasActiveMigration("node-1", "node-2")
	manager.mu.Unlock()

	if hasActive {
		t.Error("Expected no active migration after completion")
	}
}

func TestShardManager_GetActiveMigrations(t *testing.T) {
	config := DefaultShardConfig()
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)
	manager.AddNode("node-3", "localhost:6381", false)

	// Create multiple migrations
	task1, _ := manager.CreateMigrationTask("node-1", "node-2", "*")
	task2, _ := manager.CreateMigrationTask("node-2", "node-3", "*")
	task3, _ := manager.CreateMigrationTask("node-3", "node-1", "*")

	// All should be active initially
	active := manager.GetActiveMigrations()
	if len(active) != 3 {
		t.Errorf("Expected 3 active migrations, got %d", len(active))
	}

	// Complete one
	manager.UpdateMigrationTask(task1.ID, 100, 100, "completed", nil)

	// Should have 2 active
	active = manager.GetActiveMigrations()
	if len(active) != 2 {
		t.Errorf("Expected 2 active migrations, got %d", len(active))
	}

	// Fail one
	manager.UpdateMigrationTask(task2.ID, 50, 100, "failed", fmt.Errorf("test error"))

	// Should have 1 active
	active = manager.GetActiveMigrations()
	if len(active) != 1 {
		t.Errorf("Expected 1 active migration, got %d", len(active))
	}

	// Verify it's the right one
	if active[0].ID != task3.ID {
		t.Errorf("Expected task3 to be active, got %s", active[0].ID)
	}
}

func TestShardManager_CancelMigration(t *testing.T) {
	config := DefaultShardConfig()
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)

	// Create migration
	task, _ := manager.CreateMigrationTask("node-1", "node-2", "*")

	// Cancel it
	err := manager.CancelMigration(task.ID)
	if err != nil {
		t.Fatalf("Failed to cancel migration: %v", err)
	}

	// Verify it's cancelled
	cancelledTask, _ := manager.GetMigrationTask(task.ID)
	if cancelledTask.Status != "cancelled" {
		t.Errorf("Expected cancelled status, got %s", cancelledTask.Status)
	}

	// Try to cancel again (should fail)
	err = manager.CancelMigration(task.ID)
	if err == nil {
		t.Error("Expected error when cancelling already cancelled migration")
	}
}

func TestShardManager_CleanupCompletedMigrations(t *testing.T) {
	config := DefaultShardConfig()
	manager, _ := NewShardManager(config)

	manager.AddNode("node-1", "localhost:6379", false)
	manager.AddNode("node-2", "localhost:6380", false)

	// Create migrations
	task1, _ := manager.CreateMigrationTask("node-1", "node-2", "*")
	task2, _ := manager.CreateMigrationTask("node-2", "node-1", "*")
	task3, _ := manager.CreateMigrationTask("node-1", "node-2", "user:*")

	// Complete some migrations
	manager.UpdateMigrationTask(task1.ID, 100, 100, "completed", nil)
	manager.UpdateMigrationTask(task2.ID, 50, 100, "failed", fmt.Errorf("test error"))

	// Manually set end times to the past for cleanup testing
	manager.mu.Lock()
	manager.migrations[task1.ID].EndTime = time.Now().Add(-2 * time.Hour)
	manager.migrations[task2.ID].EndTime = time.Now().Add(-2 * time.Hour)
	manager.mu.Unlock()

	// Cleanup migrations older than 1 hour
	cleaned := manager.CleanupCompletedMigrations(1 * time.Hour)

	if cleaned != 2 {
		t.Errorf("Expected 2 migrations cleaned, got %d", cleaned)
	}

	// Verify task3 still exists (it's pending)
	allMigrations := manager.GetAllMigrations()
	if len(allMigrations) != 1 {
		t.Errorf("Expected 1 migration remaining, got %d", len(allMigrations))
	}

	if allMigrations[0].ID != task3.ID {
		t.Error("Expected task3 to remain after cleanup")
	}
}

func TestShardManager_RebalancingConcurrency(t *testing.T) {
	config := &ShardConfig{
		Enabled:           true,
		VirtualNodes:      10,
		ReplicationFactor: 1,
		AutoRebalance:     false,
		MigrationRate:     1000,
	}

	manager, _ := NewShardManager(config)

	// Add nodes
	for i := 1; i <= 5; i++ {
		manager.AddNode(fmt.Sprintf("node-%d", i), fmt.Sprintf("localhost:637%d", i), false)
	}

	// Set up varied loads
	manager.UpdateNodeStats("node-1", 10000, false)
	manager.UpdateNodeStats("node-2", 8000, false)
	manager.UpdateNodeStats("node-3", 2000, false)
	manager.UpdateNodeStats("node-4", 1000, false)
	manager.UpdateNodeStats("node-5", 500, false)

	// Trigger rebalancing concurrently
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager.TriggerManualRebalance()
		}()
	}

	wg.Wait()

	// Verify migrations were created without race conditions
	migrations := manager.GetAllMigrations()
	if len(migrations) == 0 {
		t.Error("Expected migrations to be created")
	}

	// Verify all migrations have valid source and target
	for _, mig := range migrations {
		if mig.SourceNode == "" || mig.TargetNode == "" {
			t.Error("Migration has empty source or target node")
		}
	}
}

func TestShardManager_ExecuteRebalancing(t *testing.T) {
	config := DefaultShardConfig()
	config.Enabled = true
	manager, _ := NewShardManager(config)

	// Create nodes with specific loads
	nodes := []*NodeInfo{
		{NodeID: "node-1", KeyCount: 10000, Status: "online"},
		{NodeID: "node-2", KeyCount: 1000, Status: "online"},
	}

	for _, node := range nodes {
		manager.AddNode(node.NodeID, "localhost:6379", false)
		manager.UpdateNodeStats(node.NodeID, node.KeyCount, false)
	}

	// Execute rebalancing
	overloaded := []*NodeInfo{nodes[0]}
	underloaded := []*NodeInfo{nodes[1]}
	avgKeys := int64(5500)

	manager.mu.Lock()
	manager.executeRebalancing(overloaded, underloaded, avgKeys)
	manager.mu.Unlock()

	// Verify migration was created
	migrations := manager.GetActiveMigrations()
	if len(migrations) == 0 {
		t.Error("Expected migration to be created")
	}

	// Verify migration details
	mig := migrations[0]
	if mig.SourceNode != "node-1" {
		t.Errorf("Expected source node-1, got %s", mig.SourceNode)
	}
	if mig.TargetNode != "node-2" {
		t.Errorf("Expected target node-2, got %s", mig.TargetNode)
	}
	if mig.TotalKeys <= 0 {
		t.Error("Expected positive total keys")
	}
}
