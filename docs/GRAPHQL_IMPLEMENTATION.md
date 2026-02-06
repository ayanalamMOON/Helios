# GraphQL API Implementation

## Overview

The Helios GraphQL API provides a modern, flexible interface for interacting with the distributed key-value store and its cluster management features. It offers type-safe queries, mutations, and real-time subscriptions.

## Architecture

### Components

1. **Schema Definition** (`schema.go`)
   - Complete GraphQL schema with type definitions
   - 15 query operations
   - 17 mutation operations
   - 4 subscription types
   - 20+ type definitions

2. **Query Resolvers** (`resolver.go`)
   - Implements all read operations
   - Authentication context handling
   - Error handling and validation
   - Integration with ATLAS, Raft, Sharding, Queue, and Auth services

3. **Mutation Resolvers** (`mutations.go`)
   - Implements all write operations
   - User registration and authentication
   - Key-value operations (set, delete, expire)
   - Queue job management
   - Cluster operations (shard and raft management)

4. **Subscriptions** (`subscriptions.go`)
   - Real-time updates via WebSocket
   - Job status updates
   - Migration progress tracking
   - Cluster events
   - Key change notifications

5. **HTTP Handler** (`handler.go`)
   - GraphQL request processing
   - Authentication middleware
   - CORS support
   - GraphQL Playground UI
   - Introspection support

### Dependencies

The GraphQL API integrates with:

- **ATLAS Store**: Core key-value operations
- **Sharded ATLAS**: Distributed key-value operations (optional)
- **Auth Service**: User authentication and token management
- **Job Queue**: Asynchronous job processing
- **Raft Node**: Consensus and cluster state
- **Shard Manager**: Horizontal sharding and rebalancing

## API Operations

### Queries

#### Authentication

```graphql
# Get current authenticated user
query {
  me {
    id
    username
    createdAt
  }
}
```

#### Key-Value Operations

```graphql
# Get a single key
query {
  get(key: "user:123") {
    key
    value
    ttl
  }
}

# List keys with prefix
query {
  keys(prefix: "user:") {
    key
    value
    ttl
  }
}

# Check if key exists
query {
  exists(key: "user:123")
}
```

#### Queue Operations

```graphql
# Get a specific job
query {
  job(id: "job123") {
    id
    type
    status
    payload
    priority
    retries
    createdAt
  }
}

# List jobs by status
query {
  jobs(status: PENDING) {
    id
    type
    status
    createdAt
  }
}
```

#### Cluster Operations

```graphql
# Get shard nodes
query {
  shardNodes {
    id
    address
    state
    keyCount
  }
}

# Get cluster statistics
query {
  shardStats {
    totalKeys
    totalNodes
    rebalancing
  }
}

# Find node for a key
query {
  nodeForKey(key: "user:123")
}

# Get active migrations
query {
  activeMigrations {
    taskId
    sourceNode
    targetNode
    progress
    status
  }
}
```

#### Raft Operations

```graphql
# Get Raft status
query {
  raftStatus {
    term
    isLeader
    leader
    state
  }
}

# List Raft peers
query {
  raftPeers {
    id
    address
  }
}

# Get cluster status
query {
  clusterStatus {
    nodes {
      id
      address
      isLeader
      isHealthy
    }
  }
}
```

#### System Operations

```graphql
# Health check
query {
  health {
    status
    timestamp
    checks
  }
}

# System metrics
query {
  metrics {
    timestamp
    data
  }
}
```

### Mutations

#### User Management

```graphql
# Register new user
mutation {
  register(input: {
    username: "alice"
    password: "secret123"
  }) {
    token
    expiresAt
    user {
      id
      username
    }
  }
}

# Login
mutation {
  login(input: {
    username: "alice"
    password: "secret123"
  }) {
    token
    expiresAt
    user {
      id
      username
    }
  }
}

# Logout
mutation {
  logout
}

# Refresh token
mutation {
  refreshToken {
    token
    expiresAt
  }
}
```

#### Key-Value Operations

```graphql
# Set a key
mutation {
  set(input: {
    key: "user:123"
    value: "{\"name\":\"Alice\"}"
    ttl: 3600
  }) {
    key
    value
    ttl
  }
}

# Delete a key
mutation {
  delete(key: "user:123")
}

# Set expiration
mutation {
  expire(key: "user:123", ttl: 7200)
}
```

#### Queue Operations

```graphql
# Enqueue a job
mutation {
  enqueueJob(input: {
    type: "email"
    payload: "{\"to\":\"user@example.com\",\"subject\":\"Hello\"}"
    priority: 5
    dedupId: "email-123"
  }) {
    id
    type
    status
    createdAt
  }
}

# Cancel a job
mutation {
  cancelJob(id: "job123")
}

# Retry a failed job
mutation {
  retryJob(id: "job123")
}
```

#### Shard Management

```graphql
# Add shard node
mutation {
  addShardNode(input: {
    id: "node2"
    address: "localhost:8002"
  }) {
    id
    address
    state
  }
}

# Remove shard node
mutation {
  removeShardNode(id: "node2")
}

# Trigger rebalancing
mutation {
  triggerRebalance
}

# Cancel migration
mutation {
  cancelMigration(taskId: "migration123")
}

# Cleanup completed migrations
mutation {
  cleanupMigrations(olderThan: "24h")
}
```

#### Raft Management

```graphql
# Add Raft peer
mutation {
  addRaftPeer(input: {
    id: "node2"
    address: "localhost:8002"
  })
}

# Remove Raft peer
mutation {
  removeRaftPeer(id: "node2")
}
```

### Subscriptions

```graphql
# Subscribe to job updates
subscription {
  jobUpdated(jobId: "job123") {
    id
    status
    progress
    updatedAt
  }
}

# Subscribe to migration progress
subscription {
  migrationProgress(taskId: "migration123") {
    taskId
    progress
    keysTransferred
    totalKeys
    status
  }
}

# Subscribe to cluster events
subscription {
  clusterEvent {
    type
    nodeId
    timestamp
    data
  }
}

# Subscribe to key changes
subscription {
  keyChanged(pattern: "user:*") {
    key
    operation
    value
    timestamp
  }
}
```

## Configuration

Add to `configs/default.yaml`:

```yaml
graphql:
  enabled: true
  endpoint: "/graphql"
  playground_enabled: true
  playground_endpoint: "/graphql/playground"
  introspection_enabled: true
  max_query_depth: 10
  max_complexity: 1000
  allowed_origins:
    - "*"
```

### Configuration Options

- `enabled`: Enable/disable GraphQL API
- `endpoint`: GraphQL API endpoint path
- `playground_enabled`: Enable GraphQL Playground UI
- `playground_endpoint`: Playground UI endpoint path
- `introspection_enabled`: Allow schema introspection
- `max_query_depth`: Maximum nested query depth
- `max_complexity`: Maximum query complexity score
- `allowed_origins`: CORS allowed origins

## Security

### Authentication

GraphQL operations requiring authentication:
- `me` query
- `logout` mutation
- `refreshToken` mutation
- Key-value operations (context-dependent)
- Cluster management operations
- Queue operations (context-dependent)

### Authorization

The API uses Bearer token authentication:

```
Authorization: Bearer <token>
```

Tokens are obtained through:
1. `register` mutation
2. `login` mutation
3. `refreshToken` mutation

### Best Practices

1. **Production Settings**:
   - Disable introspection: `introspection_enabled: false`
   - Limit query complexity: `max_complexity: 1000`
   - Restrict CORS: `allowed_origins: ["https://yourdomain.com"]`
   - Disable playground: `playground_enabled: false`

2. **Query Optimization**:
   - Request only needed fields
   - Use pagination for large result sets
   - Avoid deeply nested queries
   - Use aliases for multiple similar queries

3. **Error Handling**:
   - Check `errors` array in responses
   - Implement retry logic for transient errors
   - Log errors for debugging

## Implementation Details

### Resolver Context

Each request carries context containing:
- Authenticated user (if logged in)
- Request metadata
- Service dependencies

### Error Handling

Errors are returned in GraphQL format:

```json
{
  "errors": [
    {
      "message": "User not authenticated",
      "path": ["me"]
    }
  ]
}
```

### Type System

All types are strongly typed:
- Scalars: String, Int, Boolean
- Custom scalars: Time (RFC3339 formatted)
- Objects: User, KVPair, Job, etc.
- Enums: JobStatus, JobType, MigrationStatus

### Real-time Updates

Subscriptions use WebSocket protocol:
1. Client connects to WebSocket endpoint
2. Sends subscription query
3. Receives updates as they occur
4. Server pushes updates automatically

## Integration

### Gateway Integration

```go
import "github.com/helios/helios/internal/graphql"

// Initialize GraphQL handler
handler := graphql.InitializeGraphQL(
    atlasStore,
    shardedAtlas,
    authService,
    jobQueue,
    raftNode,
    shardManager,
)

// Register with gateway
gateway.SetGraphQLHandler(handler)
```

### Standalone Usage

```go
// Create resolver
resolver := graphql.NewResolver(
    atlasStore,
    shardedAtlas,
    authService,
    jobQueue,
    raftNode,
    shardManager,
)

// Create handler
handler := graphql.NewHandler(resolver, authService)

// Serve HTTP
http.Handle("/graphql", handler)
http.Handle("/graphql/playground", graphql.PlaygroundHandler())
http.ListenAndServe(":8080", nil)
```

## Testing

### Unit Tests

```bash
go test ./internal/graphql/... -v
```

### Integration Tests

```bash
go test ./internal/graphql/... -tags=integration -v
```

### Manual Testing

Use GraphQL Playground at `http://localhost:8443/graphql/playground`

### CLI Testing

```bash
# Query
helios-cli graphql query "{ health { status } }"

# Mutation
helios-cli graphql mutate "mutation { register(input: { username: \"test\", password: \"test123\" }) { token } }"

# With authentication
helios-cli graphql query "{ me { username } }" --token <token>
```

## Performance

### Optimization Tips

1. **Batching**: Use DataLoader pattern for N+1 query problems
2. **Caching**: Cache frequently accessed data
3. **Pagination**: Use limit/offset for large result sets
4. **Field Selection**: Only request needed fields
5. **Complexity Limits**: Configure appropriate limits

### Monitoring

Monitor these metrics:
- Query execution time
- Error rates
- Request volume
- Subscription connections
- Memory usage

## Query Cost Analysis

Query Cost Analysis prevents expensive queries from overloading the server by analyzing query complexity before execution.

### Configuration

```yaml
graphql:
  cost_analysis:
    enabled: true
    max_complexity: 1000
    max_depth: 10
    default_field_cost: 1
    reject_on_exceed: true
    include_cost_in_response: true
```

### Programmatic Configuration

```go
import "github.com/helios/helios/internal/graphql"

// Create handler with custom cost config
costConfig := &graphql.CostConfig{
    MaxComplexity:         500,
    MaxDepth:              5,
    DefaultFieldCost:      1,
    Enabled:               true,
    RejectOnExceed:        true,
    IncludeCostInResponse: true,
}

handler := graphql.NewHandler(resolver, authService,
    graphql.WithCostConfig(costConfig))

// Or update at runtime
handler.SetCostConfig(costConfig)
handler.EnableCostAnalysis(true)
```

### Field Cost Definitions

Fields have predefined costs based on their computational expense:

| Field Type       | Cost Range | Examples                                         |
| ---------------- | ---------- | ------------------------------------------------ |
| Scalar fields    | 0-1        | `User.id`, `User.username`                       |
| Simple queries   | 1-5        | `health`, `me`, `get`                            |
| List queries     | 5-10       | `keys`, `jobs`, `users`                          |
| Complex queries  | 10-50      | `clusterStatus`, `allMigrations`                 |
| Mutations        | 3-50       | `set: 5`, `register: 10`, `triggerRebalance: 50` |
| Admin operations | 20-50      | `addRaftPeer: 30`, `removeShardNode: 20`         |

### Multipliers

List fields automatically apply multipliers based on the `limit` argument:

```graphql
# Cost = base(10) + children(2) * limit(100) = 210
query {
  jobs(limit: 100) {
    id
    status
  }
}
```

### Custom Field Costs

```go
// Set custom cost for a field
analyzer := handler.GetCostAnalyzer()
analyzer.SetFieldCost("Query.customExpensiveField", &graphql.FieldCost{
    Cost:             100,
    Multiplier:       10,
    UseMultiplierArg: "limit",
    RequiresAuth:     true,
})
```

### Query Cost Introspection

Query the current cost configuration:

```graphql
query {
  queryCostConfig {
    enabled
    maxComplexity
    maxDepth
    rejectOnExceed
    includeCostInResponse
  }
}
```

Estimate cost before executing:

```graphql
query EstimateCost($query: String!) {
  estimateQueryCost(query: $query) {
    totalCost
    maxDepth
    exceeded
    exceededReason
    fieldCosts {
      path
      cost
    }
    warnings
  }
}
```

### Response Extensions

When `include_cost_in_response` is enabled, responses include cost information:

```json
{
  "data": { ... },
  "extensions": {
    "cost": {
      "totalCost": 15,
      "maxComplexity": 1000,
      "maxDepth": 10,
      "currentDepth": 3
    }
  }
}
```

### Error Response

When a query exceeds limits:

```json
{
  "errors": [
    {
      "message": "Query complexity 1500 exceeds maximum allowed 1000",
      "extensions": {
        "code": "QUERY_COMPLEXITY_EXCEEDED",
        "totalCost": 1500,
        "maxComplexity": 1000,
        "maxDepth": 10,
        "currentDepth": 5
      }
    }
  ],
  "extensions": {
    "cost": {
      "totalCost": 1500,
      "maxComplexity": 1000,
      "exceeded": true,
      "exceededReason": "Query complexity 1500 exceeds maximum allowed 1000"
    }
  }
}
```

### Best Practices

1. **Start with high limits**: Begin with generous limits and adjust based on monitoring
2. **Monitor cost distribution**: Track the cost of actual queries to find optimal limits
3. **Use depth limits**: Prevent deeply nested queries that can cause performance issues
4. **Adjust field costs**: Tune costs based on actual query performance
5. **Enable cost in response**: Helps clients understand and optimize their queries

## Rate Limiting per Resolver

Rate limiting prevents abuse by limiting the number of requests a client can make to specific GraphQL resolvers within a time window.

### Implementation

The rate limiter is implemented in `internal/graphql/rate_limiter.go` with the following features:

- **Per-field rate limits**: Different limits for each GraphQL field (query, mutation, subscription)
- **Sliding window algorithm**: Smooth rate limiting without burst spikes at window boundaries
- **User-based identification**: Different limits for authenticated vs anonymous users
- **Burst support**: Allow temporary bursts above normal limits
- **HTTP headers**: Standard rate limit headers in responses
- **GraphQL extensions**: Rate limit info in response extensions

### Configuration

```go
config := &RateLimitConfig{
    Enabled:                  true,
    DefaultLimit:             60,       // 60 requests per window
    DefaultWindow:            60,       // 60 second window
    AnonymousMultiplier:      0.5,      // 50% limit for anonymous users
    IncludeHeadersInResponse: true,
    RejectOnExceed:           true,
    IPBasedForAnonymous:      true,
    FieldLimits: map[string]*FieldRateLimit{
        "Mutation.login": {
            Limit:      10,
            Window:     60,
            BurstLimit: 20,
        },
        "Mutation.register": {
            Limit:      5,
            Window:     60,
            BurstLimit: 10,
        },
        "Query.health": {
            Limit:             120,
            Window:            60,
            SkipAuthenticated: true,
        },
    },
}
```

### Field Rate Limit Options

| Option               | Type | Description                                     |
| -------------------- | ---- | ----------------------------------------------- |
| `Limit`              | int  | Number of requests allowed per window           |
| `Window`             | int  | Time window in seconds                          |
| `AuthenticatedLimit` | int  | Higher limit for authenticated users            |
| `BurstLimit`         | int  | Additional requests allowed as burst            |
| `SkipAuthenticated`  | bool | Skip rate limiting for authenticated users      |
| `Disabled`           | bool | Completely disable rate limiting for this field |

### Default Field Limits

The following default limits are configured:

**Queries**:
| Field        | Limit   | Window | Notes                        |
| ------------ | ------- | ------ | ---------------------------- |
| Query.health | 120/min | 60s    | Skip for authenticated users |
| Query.get    | 100/min | 60s    | 200/min for authenticated    |
| Query.keys   | 30/min  | 60s    | 60/min for authenticated     |
| Query.jobs   | 20/min  | 60s    | 40/min for authenticated     |

**Mutations**:
| Field                     | Limit  | Window | Notes                     |
| ------------------------- | ------ | ------ | ------------------------- |
| Mutation.register         | 5/min  | 60s    | +10 burst                 |
| Mutation.login            | 10/min | 60s    | +20 burst                 |
| Mutation.set              | 60/min | 60s    | 120/min for authenticated |
| Mutation.triggerRebalance | 2/min  | 60s    | Very strict               |

### HTTP Response Headers

Rate limit information is included in HTTP headers:

```http
X-RateLimit-Limit: 60
X-RateLimit-Remaining: 55
X-RateLimit-Reset: 1704067200
Retry-After: 45   # Only when rate limited
```

### GraphQL Response Extension

Rate limit info is included in the response extensions:

```json
{
  "data": { "health": { "status": "ok" } },
  "extensions": {
    "rateLimit": {
      "limit": 60,
      "remaining": 55,
      "resetAt": "2024-01-01T00:00:00Z",
      "field": "Query.health"
    }
  }
}
```

### Rate Limit Exceeded Response

When rate limit is exceeded, a 429 status code is returned:

```json
{
  "errors": [
    {
      "message": "Rate limit exceeded for Query.health. Try again in 45 seconds",
      "extensions": {
        "code": "RATE_LIMIT_EXCEEDED",
        "limit": 60,
        "remaining": 0,
        "retryAfter": 45,
        "resetAt": "2024-01-01T00:00:45Z",
        "field": "Query.health"
      }
    }
  ],
  "extensions": {
    "rateLimit": {
      "limit": 60,
      "remaining": 0,
      "resetAt": "2024-01-01T00:00:45Z",
      "field": "Query.health"
    }
  }
}
```

### GraphQL Queries

Query rate limit configuration:

```graphql
query {
  rateLimitConfig {
    enabled
    defaultLimit
    defaultWindow
    anonymousMultiplier
    rejectOnExceed
    fieldLimitCount
  }
}
```

Check rate limit status for a field:

```graphql
query {
  rateLimitStatus(field: "Query.health") {
    field
    limit
    remaining
    resetAt
    allowed
  }
}
```

Get rate limit configuration for specific fields:

```graphql
query {
  fieldRateLimits(fields: ["Query.health", "Mutation.login"]) {
    field
    limit
    window
    authenticatedLimit
    burstLimit
    skipAuthenticated
    disabled
  }
}
```

### Best Practices

1. **Start with generous limits**: Begin with higher limits and tighten based on monitoring
2. **Stricter limits for mutations**: Mutations typically need stricter limits than queries
3. **Use burst limits**: Allow temporary bursts for legitimate use cases
4. **Higher limits for authenticated users**: Reward logged-in users with higher limits
5. **Skip rate limiting for health checks**: Allow monitoring systems unrestricted access
6. **Monitor rate limit violations**: Track which users/IPs are being rate limited

## Persisted Queries

Persisted queries improve security and performance by allowing clients to send a short hash instead of the full query string. The server looks up the query by hash and executes it. This prevents arbitrary queries in production and reduces bandwidth.

### Implementation

The persisted query system is implemented in `internal/graphql/persisted_queries.go` with the following features:

- **Apollo APQ protocol**: Full compatibility with the Automatic Persisted Queries protocol
- **SHA-256 hashing**: Industry-standard query identification
- **LRU cache eviction**: Configurable cache size with least-recently-used eviction
- **TTL support**: Auto-registered queries can expire after a configured duration
- **Disk persistence**: Optionally persist queries across server restarts
- **Batch registration**: Register multiple queries at once or from a file
- **Pre-built common queries**: Register common Helios queries at startup
- **Management API**: GraphQL queries and mutations for managing persisted queries

### Configuration

```go
config := &PersistedQueryConfig{
    Enabled:                  true,
    CacheSize:                1000,     // Max queries in cache (0 = unlimited)
    AllowAutoRegister:        true,     // Allow APQ auto-registration
    RejectUnpersistedQueries: false,    // When true, only persisted queries allowed
    PersistencePath:          "",       // Path to persist queries to disk
    TTL:                      0,        // TTL for auto-registered queries (0 = no expiry)
    AllowManagementAPI:       true,     // Enable register/unregister mutations
    LogOperations:            false,    // Log persisted query operations
}
```

### Configuration Options

| Option                       | Type     | Default | Description                                      |
| ---------------------------- | -------- | ------- | ------------------------------------------------ |
| `Enabled`                    | bool     | true    | Enable/disable persisted queries                 |
| `CacheSize`                  | int      | 1000    | Max queries in cache (0 = unlimited)             |
| `AllowAutoRegister`          | bool     | true    | Allow APQ auto-registration                      |
| `RejectUnpersistedQueries`   | bool     | false   | Only allow persisted queries (security lockdown) |
| `PersistencePath`            | string   | ""      | File path for disk persistence                   |
| `TTL`                        | Duration | 0       | Auto-registration TTL (0 = no expiry)            |
| `AllowManagementAPI`         | bool     | true    | Enable register/unregister mutations             |
| `LogOperations`              | bool     | false   | Log persisted query operations                   |

### Programmatic Configuration

```go
import "github.com/helios/helios/internal/graphql"

// Create handler with custom persisted query config
config := &graphql.PersistedQueryConfig{
    Enabled:                  true,
    CacheSize:                500,
    AllowAutoRegister:        true,
    RejectUnpersistedQueries: false,
    PersistencePath:          "/data/persisted-queries.json",
}

handler := graphql.NewHandler(resolver, authService,
    graphql.WithPersistedQueryConfig(config))

// Or use a custom store with pre-registered queries
store := graphql.NewPersistedQueryStore(config)
store.RegisterCommonQueries()
store.Register("{ health { status version } }", "HealthCheck")

handler := graphql.NewHandler(resolver, authService,
    graphql.WithPersistedQueries(store))

// Runtime configuration
handler.EnablePersistedQueries(true)
handler.SetPersistedQueryConfig(newConfig)
```

### APQ Protocol (Apollo Compatible)

The Automatic Persisted Queries protocol works in three steps:

**Step 1**: Client sends hash only (no query text):

```json
{
  "extensions": {
    "persistedQuery": {
      "version": 1,
      "sha256Hash": "ecf4edb46db40b5132295c0291d62fb65d6759a9eedfa4d5d612dd5ec54a6b38"
    }
  }
}
```

**Step 2**: If the query is not found, the server responds:

```json
{
  "errors": [
    {
      "message": "PersistedQueryNotFound",
      "extensions": {
        "code": "PERSISTED_QUERY_NOT_FOUND",
        "persistedQueryNotFound": true
      }
    }
  ]
}
```

**Step 3**: Client resends with hash + full query text:

```json
{
  "query": "{ health { status version } }",
  "extensions": {
    "persistedQuery": {
      "version": 1,
      "sha256Hash": "ecf4edb46db40b5132295c0291d62fb65d6759a9eedfa4d5d612dd5ec54a6b38"
    }
  }
}
```

The server registers the query and subsequent requests with the hash only will work.

### Pre-registering Queries

Register queries at startup for immediate availability:

```go
// Register common Helios queries
store := handler.GetPersistedQueryStore()
store.RegisterCommonQueries()

// Register custom queries
store.Register(`query GetUser($id: ID!) {
  user(id: $id) { id username email roles }
}`, "GetUser")

// Batch register from a map
store.RegisterBatch(map[string]string{
    "GetKey":    `query GetKey($key: String!) { get(key: $key) { key value ttl } }`,
    "SetKey":    `mutation SetKey($input: SetInput!) { set(input: $input) { key value } }`,
    "DeleteKey": `mutation DeleteKey($key: String!) { delete(key: $key) }`,
})

// Register from a JSON file
count, err := store.RegisterFromFile("/path/to/queries.json")
```

The JSON file format:
```json
{
  "GetKey": "query GetKey($key: String!) { get(key: $key) { key value ttl } }",
  "SetKey": "mutation SetKey($input: SetInput!) { set(input: $input) { key value } }"
}
```

### Built-in Common Queries

Calling `RegisterCommonQueries()` registers these queries:

| Name           | Type     | Description                        |
| -------------- | -------- | ---------------------------------- |
| HealthCheck    | Query    | Health status with component info  |
| GetKey         | Query    | Get a key-value pair               |
| ListKeys       | Query    | List keys by pattern               |
| KeyExists      | Query    | Check if a key exists              |
| SetKey         | Mutation | Set a key-value pair               |
| DeleteKey      | Mutation | Delete a key                       |
| Login          | Mutation | User authentication                |
| Register       | Mutation | User registration                  |
| CurrentUser    | Query    | Get current authenticated user     |
| ClusterStatus  | Query    | Cluster health and nodes           |
| RaftStatus     | Query    | Raft consensus status              |
| ShardNodes     | Query    | Shard node listing                 |
| ShardStats     | Query    | Shard statistics                   |
| SystemMetrics  | Query    | System performance metrics         |
| ListJobs       | Query    | Job queue listing                  |
| EnqueueJob     | Mutation | Enqueue a new job                  |

### GraphQL Management Queries

Query persisted query configuration:

```graphql
query {
  persistedQueryConfig {
    enabled
    cacheSize
    allowAutoRegister
    rejectUnpersistedQueries
    allowManagementAPI
    totalQueries
  }
}
```

Get cache statistics:

```graphql
query {
  persistedQueryStats {
    totalQueries
    autoRegistered
    manuallyRegistered
    cacheSize
    hits
    misses
    hitRate
  }
}
```

List registered queries:

```graphql
query {
  persistedQueries(limit: 10, offset: 0) {
    hash
    name
    query
    registeredAt
    lastUsedAt
    useCount
    autoRegistered
  }
}
```

Look up a specific query by hash:

```graphql
query {
  persistedQueryLookup(hash: "ecf4edb46db...") {
    hash
    name
    query
    useCount
  }
}
```

### GraphQL Management Mutations

Register a new query:

```graphql
mutation {
  registerPersistedQuery(
    query: "{ health { status version } }"
    name: "HealthCheck"
  ) {
    hash
    success
    message
  }
}
```

Remove a query:

```graphql
mutation {
  unregisterPersistedQuery(hash: "ecf4edb46db...")
}
```

Clear all queries:

```graphql
mutation {
  clearPersistedQueries
}
```

### Security: Production Lockdown

For maximum security in production, disable auto-registration and reject unpersisted queries:

```go
config := &PersistedQueryConfig{
    Enabled:                  true,
    AllowAutoRegister:        false,    // No runtime registration
    RejectUnpersistedQueries: true,     // Only persisted queries allowed
    AllowManagementAPI:       false,    // No management mutations
    PersistencePath:          "/data/persisted-queries.json",
}

store := graphql.NewPersistedQueryStore(config)
store.RegisterFromFile("/config/approved-queries.json")
handler := graphql.NewHandler(resolver, authService,
    graphql.WithPersistedQueries(store))
```

This ensures only pre-approved queries can be executed.

### Performance

Benchmarks on Intel i7-12700H:

| Operation          | Latency  | Allocations |
| ------------------ | -------- | ----------- |
| Hash computation   | ~193ns   | 3 allocs    |
| Cache lookup       | ~35ns    | 0 allocs    |
| APQ hit            | ~84ns    | 2 allocs    |
| Concurrent lookup  | ~57ns    | 0 allocs    |

### Best Practices

1. **Pre-register critical queries**: Register important queries at startup for instant availability
2. **Use disk persistence**: Enable `PersistencePath` to survive restarts without re-registration
3. **Enable APQ in development**: Allow auto-registration during development for convenience
4. **Lock down in production**: Disable auto-registration and reject unpersisted queries
5. **Monitor cache stats**: Track hit rate and adjust cache size as needed
6. **Use meaningful names**: Name queries when registering for easier management
7. **Batch register**: Use `RegisterBatch()` or `RegisterFromFile()` for bulk registration

## Troubleshooting

### Common Issues

1. **Authentication Errors**:
   - Check token validity
   - Verify Authorization header format
   - Ensure token hasn't expired

2. **CORS Errors**:
   - Check `allowed_origins` configuration
   - Verify request origin
   - Check browser console for errors

3. **Query Complexity**:
   - Reduce query depth
   - Simplify nested queries
   - Use pagination

4. **Subscription Issues**:
   - Verify WebSocket connection
   - Check firewall settings
   - Ensure proper protocol upgrade

## Future Enhancements

Planned features:
- [x] DataLoader for batch loading (implemented in `internal/graphql/dataloader.go`)
- [x] Query cost analysis (implemented in `internal/graphql/cost_analysis.go`)
- [x] Rate limiting per resolver (implemented in `internal/graphql/rate_limiter.go`)
- [x] Persisted queries (implemented in `internal/graphql/persisted_queries.go`)
- [ ] Automatic schema documentation
- [ ] GraphQL Federation support
- [ ] Custom directives
- [ ] File upload support

## References

- [GraphQL Specification](https://spec.graphql.org/)
- [GraphQL Best Practices](https://graphql.org/learn/best-practices/)
- [WebSocket Protocol](https://tools.ietf.org/html/rfc6455)
