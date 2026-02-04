package sharding

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Rebalancer handles automatic shard rebalancing
type Rebalancer struct {
	mu       sync.RWMutex
	manager  *ShardManager
	strategy RebalanceStrategy
	running  bool
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewRebalancer creates a new rebalancer
func NewRebalancer(manager *ShardManager, strategy RebalanceStrategy) *Rebalancer {
	return &Rebalancer{
		manager:  manager,
		strategy: strategy,
		running:  false,
		stopCh:   make(chan struct{}),
	}
}

// Start starts the rebalancer
func (r *Rebalancer) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return
	}

	r.running = true
	r.wg.Add(1)
	go r.monitorLoop(ctx)
}

// Stop stops the rebalancer
func (r *Rebalancer) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return
	}

	r.running = false
	close(r.stopCh)
	r.wg.Wait()
}

// monitorLoop continuously monitors cluster balance
func (r *Rebalancer) monitorLoop(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			if err := r.CheckAndRebalance(); err != nil {
				log.Printf("[Rebalancer] Rebalance check failed: %v", err)
			}
		}
	}
}

// CheckAndRebalance checks if rebalancing is needed and executes it
func (r *Rebalancer) CheckAndRebalance() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	nodes := r.manager.GetAllNodes()
	if len(nodes) < 2 {
		return nil // No rebalancing needed with less than 2 nodes
	}

	// Calculate current distribution
	totalKeys := int64(0)
	maxKeys := int64(0)
	minKeys := int64(1<<63 - 1)

	for _, node := range nodes {
		if node.Status != "online" {
			continue
		}
		totalKeys += node.KeyCount
		if node.KeyCount > maxKeys {
			maxKeys = node.KeyCount
		}
		if node.KeyCount < minKeys {
			minKeys = node.KeyCount
		}
	}

	if totalKeys == 0 {
		return nil // No keys to rebalance
	}

	avgKeys := totalKeys / int64(len(nodes))
	variance := float64(maxKeys-minKeys) / float64(avgKeys)

	// Trigger rebalancing if variance is high (>50%)
	if variance > 0.5 {
		log.Printf("[Rebalancer] High variance detected (%.2f), initiating rebalance", variance)
		return r.executeRebalance(nodes, avgKeys)
	}

	return nil
}

// executeRebalance performs the actual rebalancing
func (r *Rebalancer) executeRebalance(nodes []*NodeInfo, targetAvg int64) error {
	switch r.strategy {
	case RebalanceStrategyMinimize:
		return r.rebalanceMinimize(nodes, targetAvg)
	case RebalanceStrategyUniform:
		return r.rebalanceUniform(nodes, targetAvg)
	default:
		return fmt.Errorf("unknown rebalancing strategy: %d", r.strategy)
	}
}

// rebalanceMinimize minimizes data movement
func (r *Rebalancer) rebalanceMinimize(nodes []*NodeInfo, targetAvg int64) error {
	// Find overloaded and underloaded nodes
	var overloaded, underloaded []*NodeInfo

	for _, node := range nodes {
		if node.Status != "online" {
			continue
		}
		if node.KeyCount > targetAvg*11/10 { // 10% threshold
			overloaded = append(overloaded, node)
		} else if node.KeyCount < targetAvg*9/10 {
			underloaded = append(underloaded, node)
		}
	}

	// Create migration tasks
	for i := 0; i < len(overloaded) && i < len(underloaded); i++ {
		source := overloaded[i]
		target := underloaded[i]

		keysToMove := (source.KeyCount - targetAvg) / 2
		if keysToMove <= 0 {
			continue
		}

		task, err := r.manager.CreateMigrationTask(
			source.NodeID,
			target.NodeID,
			"*", // Move any keys
		)
		if err != nil {
			return fmt.Errorf("failed to create migration task: %w", err)
		}

		log.Printf("[Rebalancer] Created migration task %s: %s -> %s (%d keys estimated)",
			task.ID, source.NodeID, target.NodeID, keysToMove)
	}

	return nil
}

// rebalanceUniform aims for uniform distribution
func (r *Rebalancer) rebalanceUniform(nodes []*NodeInfo, targetAvg int64) error {
	// Sort nodes by key count
	sortedNodes := make([]*NodeInfo, len(nodes))
	copy(sortedNodes, nodes)

	// Simple bubble sort (fine for small number of nodes)
	for i := 0; i < len(sortedNodes)-1; i++ {
		for j := 0; j < len(sortedNodes)-i-1; j++ {
			if sortedNodes[j].KeyCount < sortedNodes[j+1].KeyCount {
				sortedNodes[j], sortedNodes[j+1] = sortedNodes[j+1], sortedNodes[j]
			}
		}
	}

	// Move keys from most loaded to least loaded nodes
	for i := 0; i < len(sortedNodes)/2; i++ {
		source := sortedNodes[i]
		target := sortedNodes[len(sortedNodes)-1-i]

		if source.KeyCount <= targetAvg {
			break
		}

		keysToMove := (source.KeyCount - targetAvg) / 2
		if keysToMove <= 0 {
			continue
		}

		task, err := r.manager.CreateMigrationTask(
			source.NodeID,
			target.NodeID,
			"*",
		)
		if err != nil {
			return fmt.Errorf("failed to create migration task: %w", err)
		}

		log.Printf("[Rebalancer] Created migration task %s: %s -> %s (%d keys estimated)",
			task.ID, source.NodeID, target.NodeID, keysToMove)
	}

	return nil
}

// GetStatus returns the current rebalancing status
func (r *Rebalancer) GetStatus() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return map[string]interface{}{
		"running":  r.running,
		"strategy": r.strategy,
	}
}
