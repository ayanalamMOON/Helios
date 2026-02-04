package graphql

// GraphQL type definitions

// User represents a user in the system
type User struct {
	ID        string
	Username  string
	Email     *string
	Roles     []string
	CreatedAt string
	UpdatedAt string
}

// AuthPayload contains authentication response data
type AuthPayload struct {
	Token        string
	RefreshToken *string
	User         *User
	ExpiresIn    int32
}

// RegisterInput contains user registration data
type RegisterInput struct {
	Username string
	Password string
	Email    *string
}

// LoginInput contains login credentials
type LoginInput struct {
	Username string
	Password string
}

// KVPair represents a key-value pair
type KVPair struct {
	Key       string
	Value     string
	TTL       *int32
	CreatedAt string
	UpdatedAt *string
}

// SetInput contains data for setting a key-value pair
type SetInput struct {
	Key   string
	Value string
	TTL   *int32
}

// Job represents a job in the queue
type Job struct {
	ID        string
	Type      string
	Payload   string
	Status    string
	Priority  int32
	Retries   int32
	Error     *string
	CreatedAt string
	UpdatedAt string
}

// EnqueueJobInput contains data for creating a job
type EnqueueJobInput struct {
	Type     string
	Payload  string
	Priority *int32
}

// ShardNode represents a node in the shard cluster
type ShardNode struct {
	NodeID   string
	Address  string
	KeyCount int32
	Status   string
	LastSeen string
}

// ShardStats contains shard cluster statistics
type ShardStats struct {
	TotalNodes        int32
	ActiveNodes       int32
	TotalKeys         int32
	AvgKeysPerNode    float64
	MigrationsPending int32
}

// Migration represents a data migration task
type Migration struct {
	ID         string
	SourceNode string
	TargetNode string
	Status     string
	KeysTotal  int32
	KeysMoved  int32
	StartedAt  string
}

// AddShardNodeInput contains data for adding a shard node
type AddShardNodeInput struct {
	NodeID      string
	Address     string
	RaftEnabled *bool
}

// RaftStatus represents the current Raft status
type RaftStatus struct {
	Term        int32
	Leader      string
	LeaderAddr  string
	State       string
	IsLeader    bool
	CommitIndex int32
}

// RaftPeer represents a Raft peer
type RaftPeer struct {
	NodeID  string
	Address string
	Status  string
}

// ClusterStatus represents overall cluster status
type ClusterStatus struct {
	Healthy    bool
	Raft       *RaftStatus
	Sharding   *ShardStats
	Nodes      []*RaftPeer
	ShardNodes []*ShardNode
	Timestamp  string
}

// AddRaftPeerInput contains data for adding a Raft peer
type AddRaftPeerInput struct {
	NodeID  string
	Address string
}

// Health represents health check results
type Health struct {
	Status    string
	Timestamp string
	Checks    []string
}

// Metrics represents system metrics
type Metrics struct {
	Timestamp string
	Data      string
}

// ClusterEvent represents a cluster event
type ClusterEvent struct {
	Type      string
	Timestamp string
	NodeID    string
	Details   *string
}

// KeyChangeEvent represents a key change event
type KeyChangeEvent struct {
	Key       string
	Operation string
	Value     *string
	Timestamp string
}
