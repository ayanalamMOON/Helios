package sharding

import (
	"time"
)

// ShardInfo represents information about a shard
type ShardInfo struct {
	ID          string    `json:"id"`
	NodeID      string    `json:"node_id"`
	StartRange  uint32    `json:"start_range"`
	EndRange    uint32    `json:"end_range"`
	KeyCount    int64     `json:"key_count"`
	Status      string    `json:"status"` // active, migrating, offline
	LastUpdated time.Time `json:"last_updated"`
}

// MigrationTask represents a data migration between shards
type MigrationTask struct {
	ID         string    `json:"id"`
	SourceNode string    `json:"source_node"`
	TargetNode string    `json:"target_node"`
	KeyPattern string    `json:"key_pattern"`
	Status     string    `json:"status"` // pending, in_progress, completed, failed
	KeysMoved  int64     `json:"keys_moved"`
	TotalKeys  int64     `json:"total_keys"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	Error      string    `json:"error,omitempty"`
}

// ShardConfig represents sharding configuration
type ShardConfig struct {
	Enabled           bool   `yaml:"enabled" json:"enabled"`
	VirtualNodes      int    `yaml:"virtual_nodes" json:"virtual_nodes"`
	ReplicationFactor int    `yaml:"replication_factor" json:"replication_factor"`
	MigrationRate     int    `yaml:"migration_rate" json:"migration_rate"` // Keys per second
	AutoRebalance     bool   `yaml:"auto_rebalance" json:"auto_rebalance"`
	RebalanceInterval string `yaml:"rebalance_interval" json:"rebalance_interval"`
}

// DefaultShardConfig returns default sharding configuration
func DefaultShardConfig() *ShardConfig {
	return &ShardConfig{
		Enabled:           false,
		VirtualNodes:      150,
		ReplicationFactor: 1,
		MigrationRate:     1000,
		AutoRebalance:     false,
		RebalanceInterval: "1h",
	}
}

// NodeInfo represents a shard node's information
type NodeInfo struct {
	NodeID      string    `json:"node_id"`
	Address     string    `json:"address"`
	Status      string    `json:"status"` // online, offline, degraded
	KeyCount    int64     `json:"key_count"`
	LastSeen    time.Time `json:"last_seen"`
	IsLeader    bool      `json:"is_leader"`
	RaftEnabled bool      `json:"raft_enabled"`
}

// RebalanceStrategy defines how to rebalance shards
type RebalanceStrategy int

const (
	// RebalanceStrategyMinimize minimizes data movement
	RebalanceStrategyMinimize RebalanceStrategy = iota
	// RebalanceStrategyUniform aims for uniform distribution
	RebalanceStrategyUniform
)
