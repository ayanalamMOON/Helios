package sharding

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
)

// ConsistentHash implements consistent hashing with virtual nodes
type ConsistentHash struct {
	mu           sync.RWMutex
	ring         []uint32          // Sorted hash ring
	ringMap      map[uint32]string // Hash -> Node mapping
	nodes        map[string]bool   // Active nodes
	virtualNodes int               // Number of virtual nodes per physical node
}

// NewConsistentHash creates a new consistent hash ring
func NewConsistentHash(virtualNodes int) *ConsistentHash {
	if virtualNodes <= 0 {
		virtualNodes = 150 // Default virtual nodes for better distribution
	}
	return &ConsistentHash{
		ring:         []uint32{},
		ringMap:      make(map[uint32]string),
		nodes:        make(map[string]bool),
		virtualNodes: virtualNodes,
	}
}

// AddNode adds a physical node to the hash ring
func (ch *ConsistentHash) AddNode(nodeID string) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.nodes[nodeID] {
		return fmt.Errorf("node %s already exists", nodeID)
	}

	// Add virtual nodes for this physical node
	for i := 0; i < ch.virtualNodes; i++ {
		virtualKey := fmt.Sprintf("%s#%d", nodeID, i)
		hash := ch.hash(virtualKey)
		ch.ring = append(ch.ring, hash)
		ch.ringMap[hash] = nodeID
	}

	// Sort the ring
	sort.Slice(ch.ring, func(i, j int) bool {
		return ch.ring[i] < ch.ring[j]
	})

	ch.nodes[nodeID] = true
	return nil
}

// RemoveNode removes a physical node from the hash ring
func (ch *ConsistentHash) RemoveNode(nodeID string) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if !ch.nodes[nodeID] {
		return fmt.Errorf("node %s does not exist", nodeID)
	}

	// Remove all virtual nodes for this physical node
	newRing := make([]uint32, 0, len(ch.ring))
	for _, hash := range ch.ring {
		if ch.ringMap[hash] != nodeID {
			newRing = append(newRing, hash)
		} else {
			delete(ch.ringMap, hash)
		}
	}

	ch.ring = newRing
	delete(ch.nodes, nodeID)
	return nil
}

// GetNode returns the node responsible for a given key
func (ch *ConsistentHash) GetNode(key string) (string, error) {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	if len(ch.ring) == 0 {
		return "", fmt.Errorf("no nodes available")
	}

	hash := ch.hash(key)
	idx := ch.search(hash)
	return ch.ringMap[ch.ring[idx]], nil
}

// GetNodes returns N nodes responsible for a given key (for replication)
func (ch *ConsistentHash) GetNodes(key string, count int) ([]string, error) {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	if len(ch.ring) == 0 {
		return nil, fmt.Errorf("no nodes available")
	}

	if count <= 0 {
		count = 1
	}

	hash := ch.hash(key)
	idx := ch.search(hash)

	nodes := make([]string, 0, count)
	seen := make(map[string]bool)

	// Walk the ring to find unique physical nodes
	for i := 0; i < len(ch.ring) && len(nodes) < count; i++ {
		ringIdx := (idx + i) % len(ch.ring)
		node := ch.ringMap[ch.ring[ringIdx]]
		if !seen[node] {
			nodes = append(nodes, node)
			seen[node] = true
		}
	}

	return nodes, nil
}

// GetAllNodes returns all active nodes
func (ch *ConsistentHash) GetAllNodes() []string {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	nodes := make([]string, 0, len(ch.nodes))
	for node := range ch.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// NodeCount returns the number of physical nodes
func (ch *ConsistentHash) NodeCount() int {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return len(ch.nodes)
}

// hash generates a hash value for a string
func (ch *ConsistentHash) hash(key string) uint32 {
	h := md5.Sum([]byte(key))
	return binary.BigEndian.Uint32(h[:4])
}

// search performs binary search to find the appropriate node
func (ch *ConsistentHash) search(hash uint32) int {
	idx := sort.Search(len(ch.ring), func(i int) bool {
		return ch.ring[i] >= hash
	})

	// Wrap around if we've gone past the end
	if idx >= len(ch.ring) {
		idx = 0
	}

	return idx
}

// GetDistribution returns the distribution of keys across nodes (for testing/monitoring)
func (ch *ConsistentHash) GetDistribution(keys []string) map[string]int {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	distribution := make(map[string]int)
	for _, key := range keys {
		if node, err := ch.GetNode(key); err == nil {
			distribution[node]++
		}
	}
	return distribution
}
