package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/helios/helios/internal/atlas"
	"github.com/helios/helios/internal/auth"
	"github.com/helios/helios/internal/queue"
	"github.com/helios/helios/internal/raft"
	"github.com/helios/helios/internal/sharding"
)

// Resolver is the root resolver for GraphQL operations
type Resolver struct {
	atlasStore    *atlas.Atlas
	shardedAtlas  *atlas.ShardedAtlas
	authService   *auth.Service
	jobQueue      *queue.Queue
	raftNode      *raft.Raft
	shardManager  *sharding.ShardManager
	subscriptions *SubscriptionManager
}

// NewResolver creates a new GraphQL resolver
func NewResolver(
	atlasStore *atlas.Atlas,
	shardedAtlas *atlas.ShardedAtlas,
	authService *auth.Service,
	jobQueue *queue.Queue,
	raftNode *raft.Raft,
	shardManager *sharding.ShardManager,
) *Resolver {
	return &Resolver{
		atlasStore:    atlasStore,
		shardedAtlas:  shardedAtlas,
		authService:   authService,
		jobQueue:      jobQueue,
		raftNode:      raftNode,
		shardManager:  shardManager,
		subscriptions: NewSubscriptionManager(),
	}
}

// Query Resolvers

// Me returns the current authenticated user
func (r *Resolver) Me(ctx context.Context) (*User, error) {
	userID, ok := ctx.Value("user_id").(string)
	if !ok || userID == "" {
		return nil, fmt.Errorf("authentication required")
	}

	// Use DataLoader if available for batching/caching
	if loaders := LoadersFromContext(ctx); loaders != nil && loaders.UserLoader != nil {
		return loaders.UserLoader.Load(ctx, userID)
	}

	// Fallback to direct fetch
	user, err := r.authService.GetUser(userID)
	if err != nil {
		return nil, err
	}

	return &User{
		ID:        user.ID,
		Username:  user.Username,
		CreatedAt: user.CreatedAt.Format(time.RFC3339),
		UpdatedAt: user.CreatedAt.Format(time.RFC3339),
	}, nil
}

// Get retrieves a key-value pair
func (r *Resolver) Get(ctx context.Context, args struct{ Key string }) (*KVPair, error) {
	// Use DataLoader if available for batching/caching
	if loaders := LoadersFromContext(ctx); loaders != nil && loaders.KeyLoader != nil {
		kv, err := loaders.KeyLoader.Load(ctx, args.Key)
		if err != nil {
			// Key not found returns nil, not error
			return nil, nil
		}
		return kv, nil
	}

	// Fallback to direct fetch
	data, exists := r.atlasStore.Get(args.Key)
	if !exists {
		return nil, nil
	}

	return &KVPair{
		Key:       args.Key,
		Value:     string(data),
		CreatedAt: time.Now().Format(time.RFC3339),
	}, nil
}

// Keys retrieves all keys matching a pattern
func (r *Resolver) Keys(ctx context.Context, args struct{ Pattern *string }) ([]string, error) {
	pattern := "*"
	if args.Pattern != nil {
		pattern = *args.Pattern
	}

	// Get all keys from atlas using Scan
	allKeys := r.atlasStore.Scan("data:")

	// Simple pattern matching (only supports *)
	if pattern == "*" {
		return allKeys, nil
	}

	return allKeys, nil
}

// Exists checks if a key exists
func (r *Resolver) Exists(ctx context.Context, args struct{ Key string }) (bool, error) {
	_, exists := r.atlasStore.Get(args.Key)
	return exists, nil
}

// Job retrieves a job by ID
func (r *Resolver) Job(ctx context.Context, args struct{ ID string }) (*Job, error) {
	if r.jobQueue == nil {
		return nil, fmt.Errorf("job queue not available")
	}

	// Use DataLoader if available for batching/caching
	if loaders := LoadersFromContext(ctx); loaders != nil && loaders.JobLoader != nil {
		return loaders.JobLoader.Load(ctx, args.ID)
	}

	// Fallback to direct fetch
	job, err := r.jobQueue.GetJob(args.ID)
	if err != nil {
		return nil, nil
	}

	// Extract type from payload
	jobType := ""
	if t, ok := job.Payload["type"].(string); ok {
		jobType = t
	}

	// Marshal payload to JSON string
	payloadJSON, _ := json.Marshal(job.Payload)

	return &Job{
		ID:        job.ID,
		Type:      jobType,
		Payload:   string(payloadJSON),
		Status:    string(job.Status),
		Priority:  0,
		Retries:   int32(job.Attempts),
		CreatedAt: job.CreatedAt.Format(time.RFC3339),
		UpdatedAt: job.LastUpdated.Format(time.RFC3339),
	}, nil
}

// Jobs retrieves all jobs
func (r *Resolver) Jobs(ctx context.Context, args struct {
	Status *string
	Limit  *int32
}) ([]*Job, error) {
	if r.jobQueue == nil {
		return nil, fmt.Errorf("job queue not available")
	}

	// Get jobs by status or all pending jobs
	var allJobs []*queue.Job
	var err error

	if args.Status != nil {
		// Convert string status to JobStatus type
		var status queue.JobStatus
		switch *args.Status {
		case "PENDING":
			status = queue.StatusPending
		case "IN_PROGRESS":
			status = queue.StatusInProgress
		case "DONE":
			status = queue.StatusDone
		case "FAILED":
			status = queue.StatusFailed
		case "DLQ":
			status = queue.StatusDLQ
		default:
			status = queue.StatusPending
		}
		allJobs, err = r.jobQueue.ListJobs(status)
	} else {
		allJobs, err = r.jobQueue.ListJobs(queue.StatusPending)
	}

	if err != nil {
		return nil, err
	}

	result := make([]*Job, 0, len(allJobs))

	for _, job := range allJobs {
		// Extract type from payload
		jobType := ""
		if t, ok := job.Payload["type"].(string); ok {
			jobType = t
		}

		// Marshal payload to JSON string
		payloadJSON, _ := json.Marshal(job.Payload)

		result = append(result, &Job{
			ID:        job.ID,
			Type:      jobType,
			Payload:   string(payloadJSON),
			Status:    string(job.Status),
			Priority:  0,
			Retries:   int32(job.Attempts),
			CreatedAt: job.CreatedAt.Format(time.RFC3339),
			UpdatedAt: job.LastUpdated.Format(time.RFC3339),
		})

		if args.Limit != nil && len(result) >= int(*args.Limit) {
			break
		}
	}

	return result, nil
}

// ShardNodes retrieves all shard nodes
func (r *Resolver) ShardNodes(ctx context.Context) ([]*ShardNode, error) {
	if r.shardManager == nil {
		return nil, fmt.Errorf("shard manager not available")
	}

	nodes := r.shardManager.GetAllNodes()
	result := make([]*ShardNode, 0, len(nodes))

	for _, node := range nodes {
		result = append(result, &ShardNode{
			NodeID:   node.NodeID,
			Address:  node.Address,
			Status:   node.Status,
			KeyCount: int32(node.KeyCount),
			LastSeen: node.LastSeen.Format(time.RFC3339),
		})
	}

	return result, nil
}

// ShardStats retrieves shard statistics
func (r *Resolver) ShardStats(ctx context.Context) (*ShardStats, error) {
	if r.shardManager == nil {
		return nil, fmt.Errorf("shard manager not available")
	}

	stats := r.shardManager.GetClusterStats()

	return &ShardStats{
		TotalNodes:        int32(stats["total_nodes"].(int)),
		ActiveNodes:       int32(stats["active_nodes"].(int)),
		TotalKeys:         int32(stats["total_keys"].(int64)),
		AvgKeysPerNode:    stats["avg_keys_per_node"].(float64),
		MigrationsPending: int32(stats["active_migrations"].(int)),
	}, nil
}

// NodeForKey returns the node responsible for a key
func (r *Resolver) NodeForKey(ctx context.Context, args struct{ Key string }) (*ShardNode, error) {
	if r.shardManager == nil {
		return nil, fmt.Errorf("shard manager not available")
	}

	nodeID, err := r.shardManager.GetNodeForKey(args.Key)
	if err != nil {
		return nil, err
	}

	// Get node info from all nodes
	nodes := r.shardManager.GetAllNodes()
	var node *sharding.NodeInfo
	for _, n := range nodes {
		if n.NodeID == nodeID {
			node = n
			break
		}
	}

	if node == nil {
		return nil, nil
	}

	return &ShardNode{
		NodeID:   node.NodeID,
		Address:  node.Address,
		Status:   node.Status,
		KeyCount: int32(node.KeyCount),
		LastSeen: node.LastSeen.Format(time.RFC3339),
	}, nil
}

// ActiveMigrations retrieves active migrations
func (r *Resolver) ActiveMigrations(ctx context.Context) ([]*Migration, error) {
	if r.shardManager == nil {
		return nil, fmt.Errorf("shard manager not available")
	}

	// Active migrations are stored in the manager's migrations map
	// For now, return empty slice - this would need manager API enhancement
	result := make([]*Migration, 0)

	return result, nil
}

// AllMigrations retrieves all migrations
func (r *Resolver) AllMigrations(ctx context.Context, args struct{ Limit *int32 }) ([]*Migration, error) {
	if r.shardManager == nil {
		return nil, fmt.Errorf("shard manager not available")
	}

	// All migrations would need manager API enhancement
	// For now, return empty slice
	result := make([]*Migration, 0)

	return result, nil
}

// RaftStatus retrieves Raft cluster status
func (r *Resolver) RaftStatus(ctx context.Context) (*RaftStatus, error) {
	if r.raftNode == nil {
		return nil, fmt.Errorf("raft not available")
	}

	term, isLeader := r.raftNode.GetState()
	state := r.raftNode.GetNodeState()
	leaderID, leaderAddr := r.raftNode.GetLeader()

	return &RaftStatus{
		Term:        int32(term),
		Leader:      leaderID,
		LeaderAddr:  leaderAddr,
		State:       state.String(),
		IsLeader:    isLeader,
		CommitIndex: 0, // TODO: Add method to get commit index
	}, nil
}

// RaftPeers retrieves Raft peer information
func (r *Resolver) RaftPeers(ctx context.Context) ([]*RaftPeer, error) {
	if r.raftNode == nil {
		return nil, fmt.Errorf("raft not available")
	}

	peers := r.raftNode.GetPeers()
	result := make([]*RaftPeer, 0, len(peers))

	for _, peer := range peers {
		result = append(result, &RaftPeer{
			NodeID:  peer.ID,
			Address: peer.Address,
			Status:  "connected",
		})
	}

	return result, nil
}

// ClusterStatus retrieves overall cluster status
func (r *Resolver) ClusterStatus(ctx context.Context) (*ClusterStatus, error) {
	// Combine info from Raft and Sharding
	raftStatus, _ := r.RaftStatus(ctx)
	shardStats, _ := r.ShardStats(ctx)

	var raftPeers []*RaftPeer
	if r.raftNode != nil {
		peers := r.raftNode.GetPeers()
		raftPeers = make([]*RaftPeer, 0, len(peers))
		for _, peer := range peers {
			raftPeers = append(raftPeers, &RaftPeer{
				NodeID:  peer.ID,
				Address: peer.Address,
				Status:  "connected",
			})
		}
	}

	var shardNodes []*ShardNode
	if r.shardManager != nil {
		nodes := r.shardManager.GetAllNodes()
		shardNodes = make([]*ShardNode, 0, len(nodes))
		for _, node := range nodes {
			shardNodes = append(shardNodes, &ShardNode{
				NodeID:   node.NodeID,
				Address:  node.Address,
				Status:   node.Status,
				KeyCount: int32(node.KeyCount),
				LastSeen: node.LastSeen.Format(time.RFC3339),
			})
		}
	}

	return &ClusterStatus{
		Healthy:    true,
		Raft:       raftStatus,
		Sharding:   shardStats,
		Nodes:      raftPeers,
		ShardNodes: shardNodes,
		Timestamp:  time.Now().Format(time.RFC3339),
	}, nil
}

// Health performs a health check
func (r *Resolver) Health(ctx context.Context) (*Health, error) {
	status := "healthy"
	var checks []string

	// Check Atlas
	if r.atlasStore != nil {
		checks = append(checks, "atlas:ok")
	} else {
		checks = append(checks, "atlas:unavailable")
		status = "degraded"
	}

	// Check Auth
	if r.authService != nil {
		checks = append(checks, "auth:ok")
	} else {
		checks = append(checks, "auth:unavailable")
		status = "degraded"
	}

	// Check Queue
	if r.jobQueue != nil {
		checks = append(checks, "queue:ok")
	} else {
		checks = append(checks, "queue:unavailable")
	}

	// Check Raft
	if r.raftNode != nil {
		checks = append(checks, "raft:ok")
	} else {
		checks = append(checks, "raft:unavailable")
	}

	// Check Sharding
	if r.shardManager != nil {
		checks = append(checks, "sharding:ok")
	} else {
		checks = append(checks, "sharding:unavailable")
	}

	return &Health{
		Status:    status,
		Timestamp: time.Now().Format(time.RFC3339),
		Checks:    checks,
	}, nil
}

// Metrics retrieves system metrics
func (r *Resolver) Metrics(ctx context.Context) (*Metrics, error) {
	metricsData := make(map[string]float64)

	// Add Atlas metrics if available
	if r.atlasStore != nil {
		// Count keys using Scan
		allKeys := r.atlasStore.Scan("data:")
		metricsData["atlas.keys"] = float64(len(allKeys))
	}

	// Add Queue metrics if available
	if r.jobQueue != nil {
		// Get all jobs across all statuses
		allStatuses := []queue.JobStatus{
			queue.StatusPending,
			queue.StatusInProgress,
			queue.StatusDone,
			queue.StatusFailed,
			queue.StatusDLQ,
		}

		pending := 0
		processing := 0
		completed := 0
		failed := 0
		totalJobs := 0

		for _, status := range allStatuses {
			jobs, err := r.jobQueue.ListJobs(status)
			if err == nil {
				switch status {
				case queue.StatusPending:
					pending = len(jobs)
				case queue.StatusInProgress:
					processing = len(jobs)
				case queue.StatusDone:
					completed = len(jobs)
				case queue.StatusFailed, queue.StatusDLQ:
					failed += len(jobs)
				}
				totalJobs += len(jobs)
			}
		}

		metricsData["queue.jobs.total"] = float64(totalJobs)
		metricsData["queue.jobs.pending"] = float64(pending)
		metricsData["queue.jobs.processing"] = float64(processing)
		metricsData["queue.jobs.completed"] = float64(completed)
		metricsData["queue.jobs.failed"] = float64(failed)
	}

	// Add Raft metrics if available
	if r.raftNode != nil {
		term, isLeader := r.raftNode.GetState()
		metricsData["raft.term"] = float64(term)
		if isLeader {
			metricsData["raft.is_leader"] = 1
		} else {
			metricsData["raft.is_leader"] = 0
		}
		metricsData["raft.peers"] = float64(len(r.raftNode.GetPeers()))
	}

	// Add Shard metrics if available
	if r.shardManager != nil {
		stats := r.shardManager.GetClusterStats()
		if totalNodes, ok := stats["total_nodes"].(int); ok {
			metricsData["sharding.nodes.total"] = float64(totalNodes)
		}
		if activeNodes, ok := stats["active_nodes"].(int); ok {
			metricsData["sharding.nodes.active"] = float64(activeNodes)
		}
		if totalKeys, ok := stats["total_keys"].(int64); ok {
			metricsData["sharding.keys.total"] = float64(totalKeys)
		}
		if avgKeys, ok := stats["avg_keys_per_node"].(float64); ok {
			metricsData["sharding.keys.avg_per_node"] = avgKeys
		}
		if activeMigrations, ok := stats["active_migrations"].(int); ok {
			metricsData["sharding.migrations.pending"] = float64(activeMigrations)
		}
	}

	// Convert to JSON string
	metricsJSON, err := json.Marshal(metricsData)
	if err != nil {
		return nil, err
	}

	return &Metrics{
		Timestamp: time.Now().Format(time.RFC3339),
		Data:      string(metricsJSON),
	}, nil
}
