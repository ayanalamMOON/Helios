package graphql

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SubscriptionManager manages real-time subscriptions
type SubscriptionManager struct {
	mu sync.RWMutex

	// Job subscriptions
	jobSubscribers map[string][]chan *Job

	// Migration subscriptions
	migrationSubscribers map[string][]chan *Migration

	// Cluster event subscriptions
	clusterSubscribers []chan *ClusterEvent

	// Key change subscriptions
	keySubscribers map[string][]chan *KeyChangeEvent
}

// NewSubscriptionManager creates a new subscription manager
func NewSubscriptionManager() *SubscriptionManager {
	return &SubscriptionManager{
		jobSubscribers:       make(map[string][]chan *Job),
		migrationSubscribers: make(map[string][]chan *Migration),
		clusterSubscribers:   make([]chan *ClusterEvent, 0),
		keySubscribers:       make(map[string][]chan *KeyChangeEvent),
	}
}

// Subscription Resolvers

// JobUpdated subscribes to job status updates
func (r *Resolver) JobUpdated(ctx context.Context, args struct{ ID string }) (<-chan *Job, error) {
	ch := make(chan *Job, 10)

	r.subscriptions.mu.Lock()
	if _, exists := r.subscriptions.jobSubscribers[args.ID]; !exists {
		r.subscriptions.jobSubscribers[args.ID] = make([]chan *Job, 0)
	}
	r.subscriptions.jobSubscribers[args.ID] = append(r.subscriptions.jobSubscribers[args.ID], ch)
	r.subscriptions.mu.Unlock()

	// Cleanup on context cancellation
	go func() {
		<-ctx.Done()
		r.subscriptions.mu.Lock()
		defer r.subscriptions.mu.Unlock()

		subscribers := r.subscriptions.jobSubscribers[args.ID]
		for i, sub := range subscribers {
			if sub == ch {
				r.subscriptions.jobSubscribers[args.ID] = append(subscribers[:i], subscribers[i+1:]...)
				close(ch)
				break
			}
		}
	}()

	return ch, nil
}

// MigrationProgress subscribes to migration progress updates
func (r *Resolver) MigrationProgress(ctx context.Context, args struct{ TaskID string }) (<-chan *Migration, error) {
	ch := make(chan *Migration, 10)

	r.subscriptions.mu.Lock()
	if _, exists := r.subscriptions.migrationSubscribers[args.TaskID]; !exists {
		r.subscriptions.migrationSubscribers[args.TaskID] = make([]chan *Migration, 0)
	}
	r.subscriptions.migrationSubscribers[args.TaskID] = append(r.subscriptions.migrationSubscribers[args.TaskID], ch)
	r.subscriptions.mu.Unlock()

	// Cleanup on context cancellation
	go func() {
		<-ctx.Done()
		r.subscriptions.mu.Lock()
		defer r.subscriptions.mu.Unlock()

		subscribers := r.subscriptions.migrationSubscribers[args.TaskID]
		for i, sub := range subscribers {
			if sub == ch {
				r.subscriptions.migrationSubscribers[args.TaskID] = append(subscribers[:i], subscribers[i+1:]...)
				close(ch)
				break
			}
		}
	}()

	return ch, nil
}

// ClusterEvent subscribes to cluster events
func (r *Resolver) ClusterEvent(ctx context.Context) (<-chan *ClusterEvent, error) {
	ch := make(chan *ClusterEvent, 10)

	r.subscriptions.mu.Lock()
	r.subscriptions.clusterSubscribers = append(r.subscriptions.clusterSubscribers, ch)
	r.subscriptions.mu.Unlock()

	// Cleanup on context cancellation
	go func() {
		<-ctx.Done()
		r.subscriptions.mu.Lock()
		defer r.subscriptions.mu.Unlock()

		for i, sub := range r.subscriptions.clusterSubscribers {
			if sub == ch {
				r.subscriptions.clusterSubscribers = append(r.subscriptions.clusterSubscribers[:i], r.subscriptions.clusterSubscribers[i+1:]...)
				close(ch)
				break
			}
		}
	}()

	return ch, nil
}

// KeyChanged subscribes to key change events
func (r *Resolver) KeyChanged(ctx context.Context, args struct{ Pattern string }) (<-chan *KeyChangeEvent, error) {
	ch := make(chan *KeyChangeEvent, 10)

	r.subscriptions.mu.Lock()
	if _, exists := r.subscriptions.keySubscribers[args.Pattern]; !exists {
		r.subscriptions.keySubscribers[args.Pattern] = make([]chan *KeyChangeEvent, 0)
	}
	r.subscriptions.keySubscribers[args.Pattern] = append(r.subscriptions.keySubscribers[args.Pattern], ch)
	r.subscriptions.mu.Unlock()

	// Cleanup on context cancellation
	go func() {
		<-ctx.Done()
		r.subscriptions.mu.Lock()
		defer r.subscriptions.mu.Unlock()

		subscribers := r.subscriptions.keySubscribers[args.Pattern]
		for i, sub := range subscribers {
			if sub == ch {
				r.subscriptions.keySubscribers[args.Pattern] = append(subscribers[:i], subscribers[i+1:]...)
				close(ch)
				break
			}
		}
	}()

	return ch, nil
}

// Publishing methods

// PublishJobUpdate publishes a job update to subscribers
func (sm *SubscriptionManager) PublishJobUpdate(job *Job) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if subscribers, exists := sm.jobSubscribers[job.ID]; exists {
		for _, ch := range subscribers {
			select {
			case ch <- job:
			default:
				// Channel full, skip
			}
		}
	}
}

// PublishMigrationProgress publishes migration progress to subscribers
func (sm *SubscriptionManager) PublishMigrationProgress(migration *Migration) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if subscribers, exists := sm.migrationSubscribers[migration.ID]; exists {
		for _, ch := range subscribers {
			select {
			case ch <- migration:
			default:
				// Channel full, skip
			}
		}
	}
}

// PublishClusterEvent publishes a cluster event to all subscribers
func (sm *SubscriptionManager) PublishClusterEvent(eventType, nodeID, details string) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	event := &ClusterEvent{
		Type:      eventType,
		Timestamp: time.Now().Format(time.RFC3339),
		NodeID:    nodeID,
		Details:   &details,
	}

	for _, ch := range sm.clusterSubscribers {
		select {
		case ch <- event:
		default:
			// Channel full, skip
		}
	}
}

// PublishKeyChange publishes a key change event to pattern subscribers
func (sm *SubscriptionManager) PublishKeyChange(key, operation, value string) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	event := &KeyChangeEvent{
		Key:       key,
		Operation: operation,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if value != "" {
		event.Value = &value
	}

	// Match against all patterns
	for pattern, subscribers := range sm.keySubscribers {
		if matchPattern(key, pattern) {
			for _, ch := range subscribers {
				select {
				case ch <- event:
				default:
					// Channel full, skip
				}
			}
		}
	}
}

// matchPattern checks if a key matches a pattern (simple * wildcard)
func matchPattern(key, pattern string) bool {
	if pattern == "*" {
		return true
	}

	// Simple prefix matching for patterns like "user:*"
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(key) >= len(prefix) && key[:len(prefix)] == prefix
	}

	return key == pattern
}

// Close closes all subscription channels
func (sm *SubscriptionManager) Close() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Close job subscribers
	for _, subscribers := range sm.jobSubscribers {
		for _, ch := range subscribers {
			close(ch)
		}
	}
	sm.jobSubscribers = make(map[string][]chan *Job)

	// Close migration subscribers
	for _, subscribers := range sm.migrationSubscribers {
		for _, ch := range subscribers {
			close(ch)
		}
	}
	sm.migrationSubscribers = make(map[string][]chan *Migration)

	// Close cluster subscribers
	for _, ch := range sm.clusterSubscribers {
		close(ch)
	}
	sm.clusterSubscribers = make([]chan *ClusterEvent, 0)

	// Close key subscribers
	for _, subscribers := range sm.keySubscribers {
		for _, ch := range subscribers {
			close(ch)
		}
	}
	sm.keySubscribers = make(map[string][]chan *KeyChangeEvent)
}

// Example usage for background workers

// StartMigrationMonitor monitors migrations and publishes updates
func (r *Resolver) StartMigrationMonitor(ctx context.Context) {
	if r.shardManager == nil {
		return
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Migration monitoring would need proper API from ShardManager
			// For now, skip publishing - needs GetActiveMigrations() method
		}
	}
}

// StartClusterMonitor monitors cluster and publishes events
func (r *Resolver) StartClusterMonitor(ctx context.Context) {
	if r.raftNode == nil {
		return
	}

	var lastLeader string
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Get current leader
			currentLeader, _ := r.raftNode.GetLeader()

			if currentLeader != lastLeader {
				if currentLeader != "" {
					r.subscriptions.PublishClusterEvent(
						"LEADER_CHANGED",
						currentLeader,
						fmt.Sprintf("New leader elected: %s", currentLeader),
					)
				} else {
					r.subscriptions.PublishClusterEvent(
						"LEADER_LOST",
						r.raftNode.GetNodeID(),
						"Leader election in progress",
					)
				}
				lastLeader = currentLeader
			}
		}
	}
}
