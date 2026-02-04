package graphql

// GraphQL schema definition for Helios
const Schema = `
# Root Query type
type Query {
	# Authentication
	me: User

	# Key-Value Store
	get(key: String!): KVPair
	keys(pattern: String, limit: Int): [String!]!
	exists(key: String!): Boolean!

	# Job Queue
	job(id: ID!): Job
	jobs(status: JobStatus, limit: Int, offset: Int): JobConnection!

	# Sharding
	shardNodes: [ShardNode!]!
	shardStats: ShardStats!
	nodeForKey(key: String!): ShardNode
	activeMigrations: [Migration!]!
	allMigrations: [Migration!]!

	# Raft Cluster
	raftStatus: RaftStatus
	raftPeers: [RaftPeer!]!
	clusterStatus: ClusterStatus!

	# Users & RBAC
	user(id: ID!): User
	users(limit: Int, offset: Int): [User!]!
	role(name: String!): Role
	roles: [Role!]!

	# System
	health: HealthStatus!
	metrics: SystemMetrics!
}

# Root Mutation type
type Mutation {
	# Authentication
	register(input: RegisterInput!): AuthPayload!
	login(input: LoginInput!): AuthPayload!
	logout: Boolean!
	refreshToken(refreshToken: String!): AuthPayload!

	# Key-Value Store
	set(input: SetInput!): KVPair!
	delete(key: String!): Boolean!
	expire(key: String!, ttl: Int!): Boolean!

	# Job Queue
	enqueueJob(input: EnqueueJobInput!): Job!
	cancelJob(id: ID!): Job
	retryJob(id: ID!): Job

	# Sharding
	addShardNode(input: AddShardNodeInput!): ShardNode!
	removeShardNode(nodeId: String!): Boolean!
	triggerRebalance: RebalanceResult!
	cancelMigration(taskId: String!): Boolean!
	cleanupMigrations(olderThanHours: Int!): CleanupResult!

	# Raft Cluster
	addRaftPeer(input: AddRaftPeerInput!): RaftPeer!
	removeRaftPeer(nodeId: String!): Boolean!

	# Users & RBAC
	createUser(input: CreateUserInput!): User!
	updateUser(id: ID!, input: UpdateUserInput!): User!
	deleteUser(id: ID!): Boolean!
	assignRole(userId: ID!, roleName: String!): User!
	revokeRole(userId: ID!, roleName: String!): User!
	createRole(input: CreateRoleInput!): Role!
	updateRole(name: String!, input: UpdateRoleInput!): Role!
	deleteRole(name: String!): Boolean!
}

# Subscription type for real-time updates
type Subscription {
	# Job status updates
	jobUpdated(id: ID!): Job!

	# Migration progress
	migrationProgress(taskId: String!): Migration!

	# Cluster events
	clusterEvent: ClusterEvent!

	# Key changes (pub/sub)
	keyChanged(pattern: String!): KeyChangeEvent!
}

# Authentication Types
type User {
	id: ID!
	username: String!
	email: String
	roles: [String!]!
	createdAt: String!
	updatedAt: String!
}

type AuthPayload {
	token: String!
	refreshToken: String
	user: User!
	expiresIn: Int!
}

input RegisterInput {
	username: String!
	password: String!
	email: String
}

input LoginInput {
	username: String!
	password: String!
}

# Key-Value Store Types
type KVPair {
	key: String!
	value: String!
	ttl: Int
	createdAt: String
	updatedAt: String
}

input SetInput {
	key: String!
	value: String!
	ttl: Int
}

# Job Queue Types
enum JobStatus {
	PENDING
	RUNNING
	COMPLETED
	FAILED
	CANCELLED
	DEAD_LETTER
}

type Job {
	id: ID!
	payload: String!
	status: JobStatus!
	attempts: Int!
	maxAttempts: Int!
	error: String
	createdAt: String!
	updatedAt: String!
	completedAt: String
}

type JobConnection {
	jobs: [Job!]!
	totalCount: Int!
	hasMore: Boolean!
}

input EnqueueJobInput {
	payload: String!
	maxAttempts: Int
	priority: Int
}

# Sharding Types
type ShardNode {
	nodeId: String!
	address: String!
	keyCount: Int!
	status: String!
	lastHeartbeat: String
}

type ShardStats {
	totalNodes: Int!
	onlineNodes: Int!
	totalKeys: Int!
	averageKeysPerNode: Float!
	activeMigrations: Int!
}

type Migration {
	id: String!
	sourceNode: String!
	targetNode: String!
	keyPattern: String!
	status: String!
	keysMoved: Int!
	totalKeys: Int!
	startTime: String!
	endTime: String
	error: String
}

type RebalanceResult {
	triggered: Boolean!
	migrationsCreated: Int!
	message: String!
}

type CleanupResult {
	cleaned: Int!
	message: String!
}

input AddShardNodeInput {
	nodeId: String!
	address: String!
	isVirtual: Boolean
}

# Raft Cluster Types
type RaftStatus {
	nodeId: String!
	state: String!
	term: Int!
	votedFor: String
	commitIndex: Int!
	lastApplied: Int!
	leader: String
}

type RaftPeer {
	nodeId: String!
	address: String!
	state: String!
	nextIndex: Int!
	matchIndex: Int!
}

type ClusterStatus {
	healthy: Boolean!
	leader: String
	nodes: [RaftPeer!]!
	raftEnabled: Boolean!
}

input AddRaftPeerInput {
	nodeId: String!
	address: String!
}

# RBAC Types
type Role {
	name: String!
	permissions: [String!]!
	description: String
	createdAt: String!
}

input CreateRoleInput {
	name: String!
	permissions: [String!]!
	description: String
}

input UpdateRoleInput {
	permissions: [String!]
	description: String
}

input CreateUserInput {
	username: String!
	password: String!
	email: String
	roles: [String!]
}

input UpdateUserInput {
	email: String
	password: String
	roles: [String!]
}

# System Types
type HealthStatus {
	status: String!
	version: String!
	uptime: Int!
	components: [ComponentHealth!]!
}

type ComponentHealth {
	name: String!
	status: String!
	message: String
}

type SystemMetrics {
	requestsTotal: Int!
	requestsPerSecond: Float!
	averageLatency: Float!
	keysCount: Int!
	jobQueueDepth: Int!
	rateLimitDenials: Int!
	memoryUsage: Int!
}

# Event Types
type ClusterEvent {
	type: String!
	timestamp: String!
	nodeId: String!
	details: String
}

type KeyChangeEvent {
	key: String!
	operation: String!
	value: String
	timestamp: String!
}
`
