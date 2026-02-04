package graphql

import (
	"github.com/helios/helios/internal/atlas"
	"github.com/helios/helios/internal/auth"
	"github.com/helios/helios/internal/queue"
	"github.com/helios/helios/internal/raft"
	"github.com/helios/helios/internal/sharding"
)

// InitializeGraphQL creates and returns a configured GraphQL handler
func InitializeGraphQL(
	atlasStore *atlas.Atlas,
	shardedAtlas *atlas.ShardedAtlas,
	authService *auth.Service,
	jobQueue *queue.Queue,
	raftNode *raft.Raft,
	shardManager *sharding.ShardManager,
) *Handler {
	// Create resolver with all dependencies
	resolver := NewResolver(
		atlasStore,
		shardedAtlas,
		authService,
		jobQueue,
		raftNode,
		shardManager,
	)

	// Create and return handler
	return NewHandler(resolver, authService)
}
