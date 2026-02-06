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
	costAnalyzer  *CostAnalyzer
	rateLimiter   *ResolverRateLimiter
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
		costAnalyzer:  NewCostAnalyzer(DefaultCostConfig()),
	}
}

// SetCostAnalyzer sets the cost analyzer for the resolver
func (r *Resolver) SetCostAnalyzer(analyzer *CostAnalyzer) {
	r.costAnalyzer = analyzer
}

// SetRateLimiter sets the rate limiter for the resolver
func (r *Resolver) SetRateLimiter(limiter *ResolverRateLimiter) {
	r.rateLimiter = limiter
}

// GetRateLimiter returns the rate limiter
func (r *Resolver) GetRateLimiter() *ResolverRateLimiter {
	return r.rateLimiter
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

// QueryCostConfig returns the current cost analysis configuration
func (r *Resolver) QueryCostConfig(ctx context.Context) (*QueryCostConfigResponse, error) {
	if r.costAnalyzer == nil {
		return &QueryCostConfigResponse{
			Enabled:               false,
			MaxComplexity:         1000,
			MaxDepth:              10,
			DefaultFieldCost:      1,
			RejectOnExceed:        true,
			IncludeCostInResponse: true,
		}, nil
	}

	config := r.costAnalyzer.GetConfig()
	return &QueryCostConfigResponse{
		Enabled:               config.Enabled,
		MaxComplexity:         int32(config.MaxComplexity),
		MaxDepth:              int32(config.MaxDepth),
		DefaultFieldCost:      int32(config.DefaultFieldCost),
		RejectOnExceed:        config.RejectOnExceed,
		IncludeCostInResponse: config.IncludeCostInResponse,
	}, nil
}

// EstimateQueryCost estimates the cost of a given query
func (r *Resolver) EstimateQueryCost(ctx context.Context, args struct{ Query string }) (*QueryCostEstimateResponse, error) {
	if r.costAnalyzer == nil {
		return &QueryCostEstimateResponse{
			TotalCost:  0,
			MaxDepth:   0,
			Exceeded:   false,
			FieldCosts: []*FieldCostEntry{},
			Warnings:   []string{"Cost analysis not enabled"},
		}, nil
	}

	result, err := r.costAnalyzer.AnalyzeQuery(ctx, args.Query, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze query: %w", err)
	}

	// Convert field costs to response format
	fieldCosts := make([]*FieldCostEntry, 0, len(result.FieldCosts))
	for path, cost := range result.FieldCosts {
		fieldCosts = append(fieldCosts, &FieldCostEntry{
			Path: path,
			Cost: int32(cost),
		})
	}

	var exceededReason *string
	if result.ExceededReason != "" {
		exceededReason = &result.ExceededReason
	}

	return &QueryCostEstimateResponse{
		TotalCost:      int32(result.TotalCost),
		MaxDepth:       int32(result.MaxDepth),
		Exceeded:       result.Exceeded,
		ExceededReason: exceededReason,
		FieldCosts:     fieldCosts,
		Warnings:       result.Warnings,
	}, nil
}

// RateLimitConfigResponse represents the rate limit configuration response
type RateLimitConfigResponse struct {
	Enabled             bool    `json:"enabled"`
	DefaultLimit        int32   `json:"defaultLimit"`
	DefaultWindow       int32   `json:"defaultWindow"`
	AnonymousMultiplier float64 `json:"anonymousMultiplier"`
	RejectOnExceed      bool    `json:"rejectOnExceed"`
	FieldLimitCount     int32   `json:"fieldLimitCount"`
}

// RateLimitStatusResponse represents the rate limit status for a client
type RateLimitStatusResponse struct {
	Field      string  `json:"field"`
	ClientID   string  `json:"clientId"`
	Limit      int32   `json:"limit"`
	Remaining  int32   `json:"remaining"`
	ResetAt    string  `json:"resetAt"`
	Allowed    bool    `json:"allowed"`
	RetryAfter *int32  `json:"retryAfter"`
}

// FieldRateLimitInfo represents rate limit info for a specific field
type FieldRateLimitInfo struct {
	Field              string `json:"field"`
	Limit              int32  `json:"limit"`
	Window             int32  `json:"window"`
	AuthenticatedLimit int32  `json:"authenticatedLimit"`
	BurstLimit         int32  `json:"burstLimit"`
	SkipAuthenticated  bool   `json:"skipAuthenticated"`
	Disabled           bool   `json:"disabled"`
}

// RateLimitConfig returns the current rate limit configuration
func (r *Resolver) RateLimitConfig(ctx context.Context) (*RateLimitConfigResponse, error) {
	if r.rateLimiter == nil {
		return &RateLimitConfigResponse{
			Enabled:             false,
			DefaultLimit:        60,
			DefaultWindow:       60,
			AnonymousMultiplier: 0.5,
			RejectOnExceed:      true,
			FieldLimitCount:     0,
		}, nil
	}

	config := r.rateLimiter.GetConfig()
	return &RateLimitConfigResponse{
		Enabled:             config.Enabled,
		DefaultLimit:        int32(config.DefaultLimit),
		DefaultWindow:       int32(config.DefaultWindow),
		AnonymousMultiplier: config.AnonymousMultiplier,
		RejectOnExceed:      config.RejectOnExceed,
		FieldLimitCount:     int32(len(config.FieldLimits)),
	}, nil
}

// RateLimitStatus returns the current rate limit status for a field and client
func (r *Resolver) RateLimitStatus(ctx context.Context, args struct {
	Field    string
	ClientID *string
}) (*RateLimitStatusResponse, error) {
	if r.rateLimiter == nil {
		return nil, fmt.Errorf("rate limiting not enabled")
	}

	// Get client ID from context if not provided
	clientID := "anonymous"
	if args.ClientID != nil && *args.ClientID != "" {
		clientID = *args.ClientID
	} else if userID, ok := ctx.Value("user_id").(string); ok && userID != "" {
		clientID = "user:" + userID
	}

	// Get rate limit status (without incrementing counter)
	fieldLimit := r.rateLimiter.GetFieldLimit(args.Field)
	isAuthenticated := ctx.Value("user_id") != nil

	// Determine effective limit
	limit := fieldLimit.Limit
	if isAuthenticated && fieldLimit.AuthenticatedLimit > 0 {
		limit = fieldLimit.AuthenticatedLimit
	} else if !isAuthenticated {
		limit = int(float64(limit) * r.rateLimiter.config.AnonymousMultiplier)
		if limit < 1 {
			limit = 1
		}
	}

	return &RateLimitStatusResponse{
		Field:     args.Field,
		ClientID:  clientID,
		Limit:     int32(limit),
		Remaining: int32(limit), // For status check, show full limit
		ResetAt:   time.Now().Add(time.Duration(fieldLimit.Window) * time.Second).Format(time.RFC3339),
		Allowed:   true,
	}, nil
}

// FieldRateLimits returns rate limit configuration for specific fields
func (r *Resolver) FieldRateLimits(ctx context.Context, args struct {
	Fields *[]string
}) ([]*FieldRateLimitInfo, error) {
	if r.rateLimiter == nil {
		return nil, fmt.Errorf("rate limiting not enabled")
	}

	config := r.rateLimiter.GetConfig()
	results := make([]*FieldRateLimitInfo, 0)

	if args.Fields != nil && len(*args.Fields) > 0 {
		// Return specific fields
		for _, field := range *args.Fields {
			fieldLimit := r.rateLimiter.GetFieldLimit(field)
			results = append(results, &FieldRateLimitInfo{
				Field:              field,
				Limit:              int32(fieldLimit.Limit),
				Window:             int32(fieldLimit.Window),
				AuthenticatedLimit: int32(fieldLimit.AuthenticatedLimit),
				BurstLimit:         int32(fieldLimit.BurstLimit),
				SkipAuthenticated:  fieldLimit.SkipAuthenticated,
				Disabled:           fieldLimit.Disabled,
			})
		}
	} else {
		// Return all configured fields
		for field, fieldLimit := range config.FieldLimits {
			results = append(results, &FieldRateLimitInfo{
				Field:              field,
				Limit:              int32(fieldLimit.Limit),
				Window:             int32(fieldLimit.Window),
				AuthenticatedLimit: int32(fieldLimit.AuthenticatedLimit),
				BurstLimit:         int32(fieldLimit.BurstLimit),
				SkipAuthenticated:  fieldLimit.SkipAuthenticated,
				Disabled:           fieldLimit.Disabled,
			})
		}
	}

	return results, nil
}
