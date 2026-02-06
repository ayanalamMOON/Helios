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
- [ ] Persisted queries
- [ ] Automatic schema documentation
- [ ] GraphQL Federation support
- [ ] Custom directives
- [ ] File upload support

## References

- [GraphQL Specification](https://spec.graphql.org/)
- [GraphQL Best Practices](https://graphql.org/learn/best-practices/)
- [WebSocket Protocol](https://tools.ietf.org/html/rfc6455)
