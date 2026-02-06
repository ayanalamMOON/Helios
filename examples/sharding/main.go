package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/helios/helios/internal/atlas"
	"github.com/helios/helios/internal/atlas/aof"
	"github.com/helios/helios/internal/atlas/protocol"
	"github.com/helios/helios/internal/sharding"
)

func main() {
	// Example: Creating and using a sharded ATLAS cluster
	example1_BasicSharding()

	// Example: Adding and removing nodes dynamically
	example2_DynamicTopology()

	// Example: Data migration
	example3_DataMigration()

	// Example: Monitoring and statistics
	example4_Monitoring()
}

// Example 1: Basic sharding setup
func example1_BasicSharding() {
	fmt.Println("=== Example 1: Basic Sharding ===")

	// Configure sharding
	shardConfig := &sharding.ShardConfig{
		Enabled:           true,
		VirtualNodes:      150,
		ReplicationFactor: 3,
		MigrationRate:     1000,
		AutoRebalance:     false,
	}

	// Configure ATLAS
	atlasConfig := &atlas.Config{
		DataDir:          "./data/shard1",
		AOFSyncMode:      aof.SyncEvery,
		SnapshotInterval: 5 * time.Minute,
	}

	// Create sharded ATLAS config
	config := &atlas.ShardedConfig{
		ShardConfig:   shardConfig,
		AtlasConfig:   atlasConfig,
		LocalNodeID:   "shard-1",
		StatsInterval: 10 * time.Second,
	}

	// Forward function for routing requests to remote nodes
	forwardFunc := func(nodeID string, cmd *protocol.Command) (*protocol.Response, error) {
		// In a real implementation, this would send the command over the network
		// to the appropriate node. For this example, we'll just log it.
		log.Printf("Forwarding command %s to node %s", cmd.Type, nodeID)
		return protocol.NewSuccessResponse(), nil
	}

	// Create sharded ATLAS
	shardedAtlas, err := atlas.NewShardedAtlas(config, forwardFunc)
	if err != nil {
		log.Fatalf("Failed to create sharded ATLAS: %v", err)
	}
	defer shardedAtlas.Stop()

	// Start the sharded ATLAS
	ctx := context.Background()
	if err := shardedAtlas.Start(ctx); err != nil {
		log.Fatalf("Failed to start sharded ATLAS: %v", err)
	}

	// Add nodes to the cluster
	shardedAtlas.AddNode("shard-1", "localhost:6379", false)
	shardedAtlas.AddNode("shard-2", "localhost:6380", false)
	shardedAtlas.AddNode("shard-3", "localhost:6381", false)

	// Store some data
	shardedAtlas.Set("user:alice", []byte("Alice Data"), 0)
	shardedAtlas.Set("user:bob", []byte("Bob Data"), 0)
	shardedAtlas.Set("user:carol", []byte("Carol Data"), 0)

	// Retrieve data (automatically routed to correct shard)
	if value, ok := shardedAtlas.Get("user:alice"); ok {
		fmt.Printf("Retrieved: %s\n", value)
	}

	fmt.Println("Basic sharding example complete")
}

// Example 2: Adding and removing nodes
func example2_DynamicTopology() {
	fmt.Println("=== Example 2: Dynamic Topology ===")

	shardConfig := sharding.DefaultShardConfig()
	shardConfig.Enabled = true

	manager, _ := sharding.NewShardManager(shardConfig)

	// Add initial nodes
	manager.AddNode("shard-1", "localhost:6379", false)
	manager.AddNode("shard-2", "localhost:6380", false)
	manager.AddNode("shard-3", "localhost:6381", false)

	fmt.Printf("Initial cluster: %d nodes\n", len(manager.GetAllNodes()))

	// Add a new node (for scaling out)
	manager.AddNode("shard-4", "localhost:6382", false)
	fmt.Printf("After adding node: %d nodes\n", len(manager.GetAllNodes()))

	// Test key distribution
	testKeys := []string{"user:1", "user:2", "user:3", "user:4", "user:5"}
	fmt.Println("Key distribution:")
	for _, key := range testKeys {
		node, _ := manager.GetNodeForKey(key)
		fmt.Printf("  %s -> %s\n", key, node)
	}

	// Remove a node (for scaling in or maintenance)
	manager.RemoveNode("shard-4")
	fmt.Printf("After removing node: %d nodes\n", len(manager.GetAllNodes()))

	fmt.Println("Dynamic topology example complete")
}

// Example 3: Data migration
func example3_DataMigration() {
	fmt.Println("=== Example 3: Data Migration ===")

	shardConfig := &sharding.ShardConfig{
		Enabled:           true,
		VirtualNodes:      150,
		ReplicationFactor: 1,
		MigrationRate:     100, // Slow rate for demonstration
	}

	manager, _ := sharding.NewShardManager(shardConfig)

	manager.AddNode("shard-1", "localhost:6379", false)
	manager.AddNode("shard-2", "localhost:6380", false)

	// Simulate nodes with different loads
	manager.UpdateNodeStats("shard-1", 10000, false) // Overloaded
	manager.UpdateNodeStats("shard-2", 1000, false)  // Underloaded

	// Create migration task
	task, err := manager.CreateMigrationTask("shard-1", "shard-2", "*")
	if err != nil {
		log.Fatalf("Failed to create migration task: %v", err)
	}

	fmt.Printf("Migration task created: %s\n", task.ID)
	fmt.Printf("  Source: %s (10000 keys)\n", task.SourceNode)
	fmt.Printf("  Target: %s (1000 keys)\n", task.TargetNode)
	fmt.Printf("  Status: %s\n", task.Status)

	// In a real scenario, the migration would be executed asynchronously
	// and you would poll for status updates

	fmt.Println("Data migration example complete")
}

// Example 4: Monitoring and statistics
func example4_Monitoring() {
	fmt.Println("=== Example 4: Monitoring ===")

	shardConfig := &sharding.ShardConfig{
		Enabled:           true,
		VirtualNodes:      150,
		ReplicationFactor: 3,
	}

	manager, _ := sharding.NewShardManager(shardConfig)

	// Add nodes
	manager.AddNode("shard-1", "localhost:6379", false)
	manager.AddNode("shard-2", "localhost:6380", false)
	manager.AddNode("shard-3", "localhost:6381", false)

	// Update stats
	manager.UpdateNodeStats("shard-1", 5000, true) // Leader
	manager.UpdateNodeStats("shard-2", 4800, false)
	manager.UpdateNodeStats("shard-3", 5200, false)

	// Get cluster statistics
	stats := manager.GetClusterStats()
	fmt.Println("Cluster Statistics:")
	fmt.Printf("  Total Nodes: %v\n", stats["total_nodes"])
	fmt.Printf("  Online Nodes: %v\n", stats["online_nodes"])
	fmt.Printf("  Total Keys: %v\n", stats["total_keys"])
	fmt.Printf("  Sharding Enabled: %v\n", stats["sharding_enabled"])
	fmt.Printf("  Replication Factor: %v\n", stats["replication_factor"])

	// Get per-node statistics
	fmt.Println("\nPer-Node Statistics:")
	for _, node := range manager.GetAllNodes() {
		fmt.Printf("  %s:\n", node.NodeID)
		fmt.Printf("    Address: %s\n", node.Address)
		fmt.Printf("    Status: %s\n", node.Status)
		fmt.Printf("    Keys: %d\n", node.KeyCount)
		fmt.Printf("    Leader: %v\n", node.IsLeader)
	}

	// Test replication
	fmt.Println("\nReplication Example:")
	testKey := "important:data"
	nodes, _ := manager.GetNodesForKey(testKey)
	fmt.Printf("Key '%s' replicated to:\n", testKey)
	for i, node := range nodes {
		fmt.Printf("  Replica %d: %s\n", i+1, node)
	}

	fmt.Println("Monitoring example complete")
}

// Example 5: Using sharding with Raft (conceptual)
func example5_ShardingWithRaft() {
	fmt.Println("=== Example 5: Sharding + Raft (Conceptual) ===")

	fmt.Println(`
Architecture: Sharding + Raft
------------------------------

For maximum fault tolerance and horizontal scaling, combine both:

1. Horizontal Sharding: Distribute data across shards
   - Shard 1: keys A-G
   - Shard 2: keys H-N
   - Shard 3: keys O-Z

2. Raft Consensus: Replicate data within each shard
   - Shard 1: 3-node Raft cluster (shard-1a, shard-1b, shard-1c)
   - Shard 2: 3-node Raft cluster (shard-2a, shard-2b, shard-2c)
   - Shard 3: 3-node Raft cluster (shard-3a, shard-3b, shard-3c)

Benefits:
- Horizontal scaling: Add more shards for capacity
- Fault tolerance: Raft ensures durability within each shard
- Load distribution: Requests distributed across shards
- High availability: Each shard can tolerate 1 node failure

Configuration:
  sharding:
    enabled: true
    replication_factor: 1  # Raft handles replication

  atlas:
    raft:
      enabled: true
      node_id: "shard-1a"
      peers:
        - shard-1b
        - shard-1c
	`)

	fmt.Println("Conceptual example complete")
}
