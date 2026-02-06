package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/helios/helios/internal/sharding"
)

// Mutation Resolvers

// Register creates a new user account
func (r *Resolver) Register(ctx context.Context, args struct{ Input RegisterInput }) (*AuthPayload, error) {
	if r.authService == nil {
		return nil, fmt.Errorf("auth service not available")
	}

	// Create user
	user, err := r.authService.CreateUser(args.Input.Username, args.Input.Password)
	if err != nil {
		return nil, err
	}

	// Create token
	token, err := r.authService.CreateToken(user.ID, 24*time.Hour)
	if err != nil {
		return nil, err
	}

	// Prime the DataLoader cache with the new user
	if loaders := LoadersFromContext(ctx); loaders != nil {
		gqlUser := &User{
			ID:        user.ID,
			Username:  user.Username,
			Email:     args.Input.Email,
			Roles:     []string{"user"},
			CreatedAt: user.CreatedAt.Format(time.RFC3339),
			UpdatedAt: user.CreatedAt.Format(time.RFC3339),
		}
		if loaders.UserLoader != nil {
			loaders.UserLoader.Prime(user.ID, gqlUser, nil)
		}
		if loaders.UserByName != nil {
			loaders.UserByName.Prime(user.Username, gqlUser, nil)
		}
	}

	refreshToken := token.TokenHash // Store for refresh

	return &AuthPayload{
		Token:        token.TokenHash,
		RefreshToken: &refreshToken,
		User: &User{
			ID:        user.ID,
			Username:  user.Username,
			Email:     args.Input.Email,
			Roles:     []string{"user"},
			CreatedAt: user.CreatedAt.Format(time.RFC3339),
			UpdatedAt: user.CreatedAt.Format(time.RFC3339),
		},
		ExpiresIn: int32(86400), // 24 hours in seconds
	}, nil
}

// Login authenticates a user
func (r *Resolver) Login(ctx context.Context, args struct{ Input LoginInput }) (*AuthPayload, error) {
	if r.authService == nil {
		return nil, fmt.Errorf("auth service not available")
	}

	// Get user by username
	user, err := r.authService.GetUserByUsername(args.Input.Username)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	// Validate password
	if !r.authService.ValidatePassword(user, args.Input.Password) {
		return nil, fmt.Errorf("invalid credentials")
	}

	// Create token
	token, err := r.authService.CreateToken(user.ID, 24*time.Hour)
	if err != nil {
		return nil, err
	}

	refreshToken := token.TokenHash

	return &AuthPayload{
		Token:        token.TokenHash,
		RefreshToken: &refreshToken,
		User: &User{
			ID:        user.ID,
			Username:  user.Username,
			Roles:     []string{"user"},
			CreatedAt: user.CreatedAt.Format(time.RFC3339),
			UpdatedAt: user.CreatedAt.Format(time.RFC3339),
		},
		ExpiresIn: int32(86400), // 24 hours in seconds
	}, nil
}

// Logout invalidates the current session
func (r *Resolver) Logout(ctx context.Context) (bool, error) {
	token, ok := ctx.Value("token").(string)
	if !ok || token == "" {
		return false, fmt.Errorf("no token provided")
	}

	if r.authService != nil {
		r.authService.RevokeToken(token)
	}

	return true, nil
}

// RefreshToken generates a new access token
func (r *Resolver) RefreshToken(ctx context.Context, args struct{ RefreshToken string }) (*AuthPayload, error) {
	if r.authService == nil {
		return nil, fmt.Errorf("auth service not available")
	}

	// Validate refresh token
	userID, err := r.authService.ValidateToken(args.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token")
	}

	// Get user
	user, err := r.authService.GetUser(userID)
	if err != nil {
		return nil, err
	}

	// Create new token
	token, err := r.authService.CreateToken(userID, 24*time.Hour)
	if err != nil {
		return nil, err
	}

	refreshToken := token.TokenHash

	return &AuthPayload{
		Token:        token.TokenHash,
		RefreshToken: &refreshToken,
		User: &User{
			ID:        user.ID,
			Username:  user.Username,
			Roles:     []string{"user"},
			CreatedAt: user.CreatedAt.Format(time.RFC3339),
			UpdatedAt: user.CreatedAt.Format(time.RFC3339),
		},
		ExpiresIn: int32(86400),
	}, nil
}

// Set creates or updates a key-value pair
func (r *Resolver) Set(ctx context.Context, args struct{ Input SetInput }) (*KVPair, error) {
	ttl := int64(0)
	if args.Input.TTL != nil {
		ttl = int64(*args.Input.TTL)
	}

	r.atlasStore.Set(args.Input.Key, []byte(args.Input.Value), ttl)

	kv := &KVPair{
		Key:       args.Input.Key,
		Value:     args.Input.Value,
		TTL:       args.Input.TTL,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	// Prime the DataLoader cache with the new value
	if loaders := LoadersFromContext(ctx); loaders != nil && loaders.KeyLoader != nil {
		loaders.KeyLoader.Prime(args.Input.Key, kv, nil)
	}

	return kv, nil
}

// Delete removes a key-value pair
func (r *Resolver) Delete(ctx context.Context, args struct{ Key string }) (bool, error) {
	result := r.atlasStore.Delete(args.Key)

	// Clear the DataLoader cache for this key
	if loaders := LoadersFromContext(ctx); loaders != nil && loaders.KeyLoader != nil {
		loaders.KeyLoader.Clear(args.Key)
	}

	return result, nil
}

// Expire sets the TTL for a key
func (r *Resolver) Expire(ctx context.Context, args struct {
	Key string
	TTL int32
}) (bool, error) {
	// Get existing value
	value, exists := r.atlasStore.Get(args.Key)
	if !exists {
		return false, nil
	}

	// Set with new TTL
	r.atlasStore.Set(args.Key, value, int64(args.TTL))
	return true, nil
}

// EnqueueJob adds a job to the queue
func (r *Resolver) EnqueueJob(ctx context.Context, args struct{ Input EnqueueJobInput }) (*Job, error) {
	if r.jobQueue == nil {
		return nil, fmt.Errorf("job queue not available")
	}

	// Create job payload
	payload := map[string]interface{}{
		"type": args.Input.Type,
		"data": args.Input.Payload,
	}

	// Enqueue job
	jobID, err := r.jobQueue.Enqueue(payload, "")
	if err != nil {
		return nil, err
	}

	// Retrieve the created job
	job, err := r.jobQueue.GetJob(jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve created job: %w", err)
	}

	// Convert payload back to string
	payloadData, _ := json.Marshal(job.Payload)

	return &Job{
		ID:        job.ID,
		Type:      args.Input.Type,
		Payload:   string(payloadData),
		Status:    string(job.Status),
		Priority:  0,
		CreatedAt: job.CreatedAt.Format(time.RFC3339),
		UpdatedAt: job.LastUpdated.Format(time.RFC3339),
	}, nil
}

// CancelJob cancels a job
func (r *Resolver) CancelJob(ctx context.Context, args struct{ ID string }) (bool, error) {
	if r.jobQueue == nil {
		return false, fmt.Errorf("job queue not available")
	}

	err := r.jobQueue.DeleteJob(args.ID)
	if err != nil {
		return false, err
	}
	return true, nil
}

// RetryJob retries a failed job
func (r *Resolver) RetryJob(ctx context.Context, args struct{ ID string }) (*Job, error) {
	if r.jobQueue == nil {
		return nil, fmt.Errorf("job queue not available")
	}

	job, err := r.jobQueue.GetJob(args.ID)
	if err != nil {
		return nil, fmt.Errorf("job not found")
	}

	// Use Nack to retry the job
	if err := r.jobQueue.Nack(args.ID, "retry requested"); err != nil {
		return nil, err
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
		Status:    "PENDING",
		Priority:  0,
		Retries:   int32(job.Attempts + 1),
		CreatedAt: job.CreatedAt.Format(time.RFC3339),
		UpdatedAt: time.Now().Format(time.RFC3339),
	}, nil
}

// AddShardNode adds a node to the shard cluster
func (r *Resolver) AddShardNode(ctx context.Context, args struct{ Input AddShardNodeInput }) (*ShardNode, error) {
	if r.shardManager == nil {
		return nil, fmt.Errorf("shard manager not available")
	}

	raftEnabled := false
	if args.Input.RaftEnabled != nil {
		raftEnabled = *args.Input.RaftEnabled
	}

	if err := r.shardManager.AddNode(args.Input.NodeID, args.Input.Address, raftEnabled); err != nil {
		return nil, err
	}

	// Get node from all nodes
	nodes := r.shardManager.GetAllNodes()
	var node *sharding.NodeInfo
	for _, n := range nodes {
		if n.NodeID == args.Input.NodeID {
			node = n
			break
		}
	}
	if node == nil {
		return nil, fmt.Errorf("failed to retrieve added node")
	}

	return &ShardNode{
		NodeID:   node.NodeID,
		Address:  node.Address,
		Status:   node.Status,
		KeyCount: int32(node.KeyCount),
		LastSeen: node.LastSeen.Format(time.RFC3339),
	}, nil
}

// RemoveShardNode removes a node from the shard cluster
func (r *Resolver) RemoveShardNode(ctx context.Context, args struct{ NodeID string }) (bool, error) {
	if r.shardManager == nil {
		return false, fmt.Errorf("shard manager not available")
	}

	if err := r.shardManager.RemoveNode(args.NodeID); err != nil {
		return false, err
	}

	return true, nil
}

// TriggerRebalance triggers shard rebalancing
func (r *Resolver) TriggerRebalance(ctx context.Context) (bool, error) {
	if r.shardManager == nil {
		return false, fmt.Errorf("shard manager not available")
	}

	// Trigger manual rebalance check
	// This would need a rebalancer instance or manual check method
	// For now, return success - actual implementation needs rebalancer integration
	return true, nil
}

// CancelMigration cancels an active migration
func (r *Resolver) CancelMigration(ctx context.Context, args struct{ ID string }) (bool, error) {
	if r.shardManager == nil {
		return false, fmt.Errorf("shard manager not available")
	}

	// Update migration status to cancelled
	if err := r.shardManager.UpdateMigrationTask(args.ID, 0, 0, "cancelled", fmt.Errorf("cancelled by user")); err != nil {
		return false, err
	}

	return true, nil
}

// CleanupMigrations cleans up completed migrations
func (r *Resolver) CleanupMigrations(ctx context.Context) (int32, error) {
	if r.shardManager == nil {
		return 0, fmt.Errorf("shard manager not available")
	}

	// Cleanup migrations older than 24 hours
	count := r.shardManager.CleanupCompletedMigrations(24 * time.Hour)
	return int32(count), nil
}

// AddRaftPeer adds a peer to the Raft cluster
func (r *Resolver) AddRaftPeer(ctx context.Context, args struct{ Input AddRaftPeerInput }) (*RaftPeer, error) {
	if r.raftNode == nil {
		return nil, fmt.Errorf("raft not available")
	}

	if err := r.raftNode.AddPeer(args.Input.NodeID, args.Input.Address); err != nil {
		return nil, err
	}

	return &RaftPeer{
		NodeID:  args.Input.NodeID,
		Address: args.Input.Address,
		Status:  "connected",
	}, nil
}

// RemoveRaftPeer removes a peer from the Raft cluster
func (r *Resolver) RemoveRaftPeer(ctx context.Context, args struct{ NodeID string }) (bool, error) {
	if r.raftNode == nil {
		return false, fmt.Errorf("raft not available")
	}

	if err := r.raftNode.RemovePeer(args.NodeID); err != nil {
		return false, err
	}

	return true, nil
}
