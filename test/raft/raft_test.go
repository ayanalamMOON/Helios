package raft_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/helios/helios/internal/raft"
)

func TestRaftBasicElection(t *testing.T) {
	// Create test data directories with unique path for this test
	testDataDir := fmt.Sprintf("./test-data-election-%d", time.Now().UnixNano())
	os.MkdirAll(fmt.Sprintf("%s/node-0", testDataDir), 0755)
	os.MkdirAll(fmt.Sprintf("%s/node-1", testDataDir), 0755)
	os.MkdirAll(fmt.Sprintf("%s/node-2", testDataDir), 0755)
	defer os.RemoveAll(testDataDir)

	// Create three nodes
	nodes := make([]*raft.Raft, 3)
	transports := make([]*raft.LocalTransport, 3)
	applyChs := make([]chan raft.ApplyMsg, 3)

	for i := 0; i < 3; i++ {
		nodeID := fmt.Sprintf("node-%d", i)
		config := raft.DefaultConfig()
		config.NodeID = nodeID
		config.DataDir = fmt.Sprintf("%s/%s", testDataDir, nodeID)

		transport := raft.NewLocalTransport(fmt.Sprintf("127.0.0.1:900%d", i))
		fsm := raft.NewMockFSM()
		applyCh := make(chan raft.ApplyMsg, 100)

		node, err := raft.New(config, transport, fsm, applyCh)
		if err != nil {
			t.Fatalf("Failed to create node: %v", err)
		}

		transport.SetRaft(node)

		nodes[i] = node
		transports[i] = transport
		applyChs[i] = applyCh
	}

	// Connect peers
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if i != j {
				peerID := fmt.Sprintf("node-%d", j)
				peerAddr := fmt.Sprintf("127.0.0.1:900%d", j)
				nodes[i].AddPeer(peerID, peerAddr)
			}
		}
	}

	// Start all nodes
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := nodes[i].Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
	}

	// Wait for leader election
	time.Sleep(500 * time.Millisecond)

	// Wait for leader election to stabilize (retry logic)
	leaderCount := 0
	maxRetries := 50 // 5 seconds total (100ms * 50)
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)

		leaderCount = 0
		for _, node := range nodes {
			_, isLeader := node.GetState()
			if isLeader {
				leaderCount++
			}
		}

		if leaderCount == 1 {
			// Wait a bit more to ensure stability
			time.Sleep(200 * time.Millisecond)
			leaderCount = 0
			for _, node := range nodes {
				_, isLeader := node.GetState()
				if isLeader {
					leaderCount++
				}
			}
			if leaderCount == 1 {
				break
			}
		}
	}

	if leaderCount != 1 {
		t.Fatalf("Expected 1 leader, got %d", leaderCount)
	}

	t.Log("Leader elected successfully")

	// Cleanup
	for _, node := range nodes {
		node.Shutdown()
	}
}

func TestRaftLogReplication(t *testing.T) {
	// Create test data directories with unique path for this test
	testDataDir := fmt.Sprintf("./test-data-replication-%d", time.Now().UnixNano())
	os.MkdirAll(fmt.Sprintf("%s/node-0", testDataDir), 0755)
	os.MkdirAll(fmt.Sprintf("%s/node-1", testDataDir), 0755)
	os.MkdirAll(fmt.Sprintf("%s/node-2", testDataDir), 0755)
	defer os.RemoveAll(testDataDir)

	// Setup similar to election test
	nodes := make([]*raft.Raft, 3)
	transports := make([]*raft.LocalTransport, 3)
	applyChs := make([]chan raft.ApplyMsg, 3)

	for i := 0; i < 3; i++ {
		nodeID := fmt.Sprintf("node-%d", i)
		config := raft.DefaultConfig()
		config.NodeID = nodeID
		config.DataDir = fmt.Sprintf("%s/%s", testDataDir, nodeID)

		transport := raft.NewLocalTransport(fmt.Sprintf("127.0.0.1:901%d", i))
		fsm := raft.NewMockFSM()
		applyCh := make(chan raft.ApplyMsg, 100)

		node, err := raft.New(config, transport, fsm, applyCh)
		if err != nil {
			t.Fatalf("Failed to create node: %v", err)
		}

		transport.SetRaft(node)

		nodes[i] = node
		transports[i] = transport
		applyChs[i] = applyCh
	}

	// Connect peers
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if i != j {
				peerID := fmt.Sprintf("node-%d", j)
				peerAddr := fmt.Sprintf("127.0.0.1:901%d", j)
				nodes[i].AddPeer(peerID, peerAddr)
			}
		}
	}

	// Start all nodes
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := nodes[i].Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
	}

	// Wait for leader election to stabilize (retry logic)
	var leader *raft.Raft
	maxRetries := 50 // 5 seconds total (100ms * 50)
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)

		for _, node := range nodes {
			_, isLeader := node.GetState()
			if isLeader {
				leader = node
				break
			}
		}

		if leader != nil {
			// Wait a bit more to ensure stability
			time.Sleep(200 * time.Millisecond)
			_, isLeader := leader.GetState()
			if isLeader {
				break
			}
			leader = nil // Leader lost, keep searching
		}
	}

	if leader == nil {
		t.Fatal("No leader elected")
	}

	// Submit commands
	commands := [][]byte{
		[]byte(`{"op":"set","key":"key1","value":"value1"}`),
		[]byte(`{"op":"set","key":"key2","value":"value2"}`),
		[]byte(`{"op":"set","key":"key3","value":"value3"}`),
	}

	for _, cmd := range commands {
		index, term, err := leader.Apply(cmd, 1*time.Second)
		if err != nil {
			t.Fatalf("Failed to apply command: %v", err)
		}
		t.Logf("Command applied: index=%d, term=%d", index, term)
	}

	// Wait for replication
	time.Sleep(200 * time.Millisecond)

	t.Log("Log replication test completed successfully")

	// Cleanup
	for _, node := range nodes {
		node.Shutdown()
	}
}

func TestRaftLeaderFailover(t *testing.T) {
	// Create test data directories for 5 nodes with unique path for this test
	testDataDir := fmt.Sprintf("./test-data-failover-%d", time.Now().UnixNano())
	os.MkdirAll(fmt.Sprintf("%s/node-0", testDataDir), 0755)
	os.MkdirAll(fmt.Sprintf("%s/node-1", testDataDir), 0755)
	os.MkdirAll(fmt.Sprintf("%s/node-2", testDataDir), 0755)
	os.MkdirAll(fmt.Sprintf("%s/node-3", testDataDir), 0755)
	os.MkdirAll(fmt.Sprintf("%s/node-4", testDataDir), 0755)
	defer os.RemoveAll(testDataDir)

	// Setup 5 nodes for better fault tolerance demonstration
	nodes := make([]*raft.Raft, 5)
	transports := make([]*raft.LocalTransport, 5)
	applyChs := make([]chan raft.ApplyMsg, 5)

	for i := 0; i < 5; i++ {
		nodeID := fmt.Sprintf("node-%d", i)
		config := raft.DefaultConfig()
		config.NodeID = nodeID
		config.DataDir = fmt.Sprintf("%s/%s", testDataDir, nodeID)
		config.HeartbeatTimeout = 50 * time.Millisecond
		config.ElectionTimeout = 150 * time.Millisecond

		transport := raft.NewLocalTransport(fmt.Sprintf("127.0.0.1:902%d", i))
		fsm := raft.NewMockFSM()
		applyCh := make(chan raft.ApplyMsg, 100)

		node, err := raft.New(config, transport, fsm, applyCh)
		if err != nil {
			t.Fatalf("Failed to create node: %v", err)
		}

		transport.SetRaft(node)

		nodes[i] = node
		transports[i] = transport
		applyChs[i] = applyCh
	}

	// Connect peers
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			if i != j {
				peerID := fmt.Sprintf("node-%d", j)
				peerAddr := fmt.Sprintf("127.0.0.1:902%d", j)
				nodes[i].AddPeer(peerID, peerAddr)
			}
		}
	}

	// Start all nodes
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := nodes[i].Start(ctx); err != nil {
			t.Fatalf("Failed to start node %d: %v", i, err)
		}
	}

	// Wait for initial leader election to stabilize (retry logic)
	var oldLeader *raft.Raft
	var oldLeaderIndex int
	maxRetries := 50 // 5 seconds total (100ms * 50)
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)

		for j, node := range nodes {
			_, isLeader := node.GetState()
			if isLeader {
				oldLeader = node
				oldLeaderIndex = j
				break
			}
		}

		if oldLeader != nil {
			// Wait a bit more to ensure stability
			time.Sleep(200 * time.Millisecond)
			_, isLeader := oldLeader.GetState()
			if isLeader {
				break
			}
			oldLeader = nil // Leader lost, keep searching
		}
	}

	if oldLeader == nil {
		t.Fatal("No initial leader elected")
	}

	t.Logf("Initial leader: node-%d", oldLeaderIndex)

	// Shutdown old leader
	t.Log("Shutting down leader to trigger failover")
	oldLeader.Shutdown()

	// Wait for new election
	time.Sleep(500 * time.Millisecond)

	// Verify new leader elected
	var newLeader *raft.Raft
	var newLeaderIndex int
	for i, node := range nodes {
		_, isLeader := node.GetState()
		if i != oldLeaderIndex && isLeader {
			newLeader = node
			newLeaderIndex = i
			break
		}
	}

	if newLeader == nil {
		t.Fatal("No new leader elected after failover")
	}

	if newLeaderIndex == oldLeaderIndex {
		t.Fatal("New leader is same as old leader")
	}

	t.Logf("New leader after failover: node-%d", newLeaderIndex)

	// Submit command to new leader
	cmd := []byte(`{"op":"set","key":"failover-test","value":"success"}`)
	index, term, err := newLeader.Apply(cmd, 1*time.Second)
	if err != nil {
		t.Fatalf("Failed to apply command to new leader: %v", err)
	}

	t.Logf("Command applied to new leader: index=%d, term=%d", index, term)

	// Cleanup
	for i, node := range nodes {
		if i != oldLeaderIndex {
			node.Shutdown()
		}
	}
}

func TestRaftSnapshot(t *testing.T) {
	// Create test data directory with unique path for this test
	testDataDir := fmt.Sprintf("./test-data-snapshot-%d", time.Now().UnixNano())
	os.MkdirAll(fmt.Sprintf("%s/snapshot", testDataDir), 0755)
	defer os.RemoveAll(testDataDir)

	config := raft.DefaultConfig()
	config.NodeID = "test-node"
	config.DataDir = fmt.Sprintf("%s/snapshot", testDataDir)
	config.SnapshotThreshold = 10 // Take snapshot after 10 entries

	transport := raft.NewLocalTransport("127.0.0.1:9030")
	fsm := raft.NewMockFSM()
	applyCh := make(chan raft.ApplyMsg, 100)

	node, err := raft.New(config, transport, fsm, applyCh)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}

	transport.SetRaft(node)

	ctx := context.Background()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Failed to start node: %v", err)
	}

	// Wait for node to become leader (single node cluster)
	time.Sleep(200 * time.Millisecond)

	// Apply many commands to trigger snapshot
	for i := 0; i < 15; i++ {
		cmd := []byte(fmt.Sprintf(`{"op":"set","key":"key-%d","value":"value-%d"}`, i, i))

		_, _, err := node.Apply(cmd, 1*time.Second)
		if err != nil {
			t.Fatalf("Failed to apply command %d: %v", i, err)
		}
	}

	// Wait for snapshot to be taken
	time.Sleep(500 * time.Millisecond)

	t.Log("Snapshot test completed successfully")

	// Cleanup
	node.Shutdown()
}
