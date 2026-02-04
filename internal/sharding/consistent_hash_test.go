package sharding

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
)

func TestConsistentHash_AddNode(t *testing.T) {
	ch := NewConsistentHash(10)

	err := ch.AddNode("node-1")
	if err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}

	if ch.NodeCount() != 1 {
		t.Errorf("Expected 1 node, got %d", ch.NodeCount())
	}

	// Adding duplicate should fail
	err = ch.AddNode("node-1")
	if err == nil {
		t.Error("Expected error when adding duplicate node")
	}
}

func TestConsistentHash_RemoveNode(t *testing.T) {
	ch := NewConsistentHash(10)
	ch.AddNode("node-1")
	ch.AddNode("node-2")

	err := ch.RemoveNode("node-1")
	if err != nil {
		t.Fatalf("RemoveNode failed: %v", err)
	}

	if ch.NodeCount() != 1 {
		t.Errorf("Expected 1 node, got %d", ch.NodeCount())
	}

	// Removing non-existent node should fail
	err = ch.RemoveNode("node-3")
	if err == nil {
		t.Error("Expected error when removing non-existent node")
	}
}

func TestConsistentHash_GetNode(t *testing.T) {
	ch := NewConsistentHash(10)

	// No nodes should return error
	_, err := ch.GetNode("test-key")
	if err == nil {
		t.Error("Expected error when no nodes available")
	}

	ch.AddNode("node-1")
	ch.AddNode("node-2")
	ch.AddNode("node-3")

	// Same key should always map to same node
	node1, err := ch.GetNode("test-key")
	if err != nil {
		t.Fatalf("GetNode failed: %v", err)
	}

	node2, err := ch.GetNode("test-key")
	if err != nil {
		t.Fatalf("GetNode failed: %v", err)
	}

	if node1 != node2 {
		t.Errorf("Expected same node for same key, got %s and %s", node1, node2)
	}
}

func TestConsistentHash_GetNodes(t *testing.T) {
	ch := NewConsistentHash(10)
	ch.AddNode("node-1")
	ch.AddNode("node-2")
	ch.AddNode("node-3")

	nodes, err := ch.GetNodes("test-key", 2)
	if err != nil {
		t.Fatalf("GetNodes failed: %v", err)
	}

	if len(nodes) != 2 {
		t.Errorf("Expected 2 nodes, got %d", len(nodes))
	}

	// Nodes should be unique
	if nodes[0] == nodes[1] {
		t.Error("Expected unique nodes")
	}
}

func TestConsistentHash_Distribution(t *testing.T) {
	ch := NewConsistentHash(150)
	nodes := []string{"node-1", "node-2", "node-3"}

	for _, node := range nodes {
		ch.AddNode(node)
	}

	// Generate test keys
	keys := make([]string, 10000)
	for i := 0; i < 10000; i++ {
		keys[i] = fmt.Sprintf("key-%d", i)
	}

	distribution := ch.GetDistribution(keys)

	// Check that distribution is reasonably balanced
	// With 10000 keys and 3 nodes, each should get roughly 3333 keys
	// Allow 20% variance
	expectedPerNode := 10000 / len(nodes)
	variance := float64(expectedPerNode) * 0.20

	for node, count := range distribution {
		diff := float64(count - expectedPerNode)
		if diff < 0 {
			diff = -diff
		}
		if diff > variance {
			t.Errorf("Node %s has unbalanced distribution: %d keys (expected ~%d)",
				node, count, expectedPerNode)
		}
	}
}

func TestConsistentHash_Stability(t *testing.T) {
	ch := NewConsistentHash(150)
	ch.AddNode("node-1")
	ch.AddNode("node-2")
	ch.AddNode("node-3")

	// Map keys to nodes before adding a new node
	keys := make([]string, 1000)
	beforeMapping := make(map[string]string)
	for i := 0; i < 1000; i++ {
		keys[i] = fmt.Sprintf("key-%d", i)
		node, _ := ch.GetNode(keys[i])
		beforeMapping[keys[i]] = node
	}

	// Add new node
	ch.AddNode("node-4")

	// Check how many keys moved
	moved := 0
	for _, key := range keys {
		newNode, _ := ch.GetNode(key)
		if newNode != beforeMapping[key] {
			moved++
		}
	}

	// With consistent hashing, adding a node should move roughly 1/N keys
	// With 4 nodes, expect ~25% movement, allow 10% variance
	expectedMoved := 1000 / 4
	maxMove := float64(expectedMoved) * 1.5 // 50% variance
	minMove := float64(expectedMoved) * 0.5

	if float64(moved) > maxMove || float64(moved) < minMove {
		t.Errorf("Too many keys moved: %d (expected ~%d)", moved, expectedMoved)
	}
}

func TestConsistentHash_Concurrency(t *testing.T) {
	ch := NewConsistentHash(150)
	ch.AddNode("node-1")
	ch.AddNode("node-2")
	ch.AddNode("node-3")

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				select {
				case <-ctx.Done():
					return
				default:
					key := fmt.Sprintf("key-%d-%d", id, j)
					_, err := ch.GetNode(key)
					if err != nil {
						t.Errorf("GetNode failed: %v", err)
						return
					}
				}
			}
		}(i)
	}

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			nodeID := fmt.Sprintf("node-%d", id+10)
			ch.AddNode(nodeID)
		}(i)
	}

	wg.Wait()
}

func BenchmarkConsistentHash_GetNode(b *testing.B) {
	ch := NewConsistentHash(150)
	for i := 0; i < 10; i++ {
		ch.AddNode(fmt.Sprintf("node-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i%1000)
		ch.GetNode(key)
	}
}

func BenchmarkConsistentHash_AddNode(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch := NewConsistentHash(150)
		for j := 0; j < 10; j++ {
			ch.AddNode(fmt.Sprintf("node-%d", j))
		}
	}
}

func TestHash_Deterministic(t *testing.T) {
	ch := NewConsistentHash(10)

	key := "test-key"
	hash1 := ch.hash(key)
	hash2 := ch.hash(key)

	if hash1 != hash2 {
		t.Error("Hash function should be deterministic")
	}
}

func TestHash_Distribution(t *testing.T) {
	ch := NewConsistentHash(10)

	// Generate hashes for many keys
	hashes := make(map[uint32]bool)
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		hash := ch.hash(key)
		hashes[hash] = true
	}

	// Check that we have good distribution (most hashes should be unique)
	if len(hashes) < 9000 {
		t.Errorf("Poor hash distribution: only %d unique hashes out of 10000", len(hashes))
	}
}

func hash(key string) uint32 {
	h := md5.Sum([]byte(key))
	return binary.BigEndian.Uint32(h[:4])
}
