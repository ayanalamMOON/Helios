# GraphQL API Quickstart Guide

## Getting Started

This guide will help you quickly start using the Helios GraphQL API.

## Prerequisites

- Helios Gateway running (default: `http://localhost:8443`)
- `helios-cli` installed (optional for CLI access)
- GraphQL client or web browser

## Quick Start Steps

### 1. Start the Gateway

```bash
# Start with GraphQL enabled
helios-gateway --listen :8443 --data-dir ./data
```

The GraphQL API will be available at:
- GraphQL endpoint: `http://localhost:8443/graphql`
- GraphQL Playground: `http://localhost:8443/graphql/playground`

### 2. Access GraphQL Playground

Open your browser and navigate to:
```
http://localhost:8443/graphql/playground
```

This provides an interactive interface for exploring and testing queries.

### 3. Run Your First Query

Try a simple health check:

```graphql
query {
  health {
    status
    timestamp
    checks
  }
}
```

## Basic Operations

### User Registration

Register a new user account:

```graphql
mutation {
  register(input: {
    username: "alice"
    password: "mypassword123"
  }) {
    token
    expiresAt
    user {
      id
      username
      createdAt
    }
  }
}
```

**Response:**
```json
{
  "data": {
    "register": {
      "token": "eyJhbGc...",
      "expiresAt": "2024-01-15T10:30:00Z",
      "user": {
        "id": "user_123",
        "username": "alice",
        "createdAt": "2024-01-14T10:30:00Z"
      }
    }
  }
}
```

Save the `token` value for authenticated requests!

### User Login

Login with existing credentials:

```graphql
mutation {
  login(input: {
    username: "alice"
    password: "mypassword123"
  }) {
    token
    expiresAt
    user {
      username
    }
  }
}
```

### Authenticated Requests

Add the token to your request headers:

**HTTP Header:**
```
Authorization: Bearer eyJhbGc...
```

**Playground:** Click "HTTP HEADERS" at the bottom and add:
```json
{
  "Authorization": "Bearer eyJhbGc..."
}
```

### Get Current User

Verify authentication:

```graphql
query {
  me {
    id
    username
    createdAt
  }
}
```

## Key-Value Operations

### Set a Key

```graphql
mutation {
  set(input: {
    key: "user:alice:profile"
    value: "{\"name\":\"Alice\",\"email\":\"alice@example.com\"}"
  }) {
    key
    value
    ttl
  }
}
```

### Set a Key with TTL

```graphql
mutation {
  set(input: {
    key: "session:abc123"
    value: "user_data"
    ttl: 3600
  }) {
    key
    value
    ttl
  }
}
```

### Get a Key

```graphql
query {
  get(key: "user:alice:profile") {
    key
    value
    ttl
  }
}
```

### List Keys

```graphql
query {
  keys(prefix: "user:") {
    key
    value
  }
}
```

### Check Key Existence

```graphql
query {
  exists(key: "user:alice:profile")
}
```

### Delete a Key

```graphql
mutation {
  delete(key: "user:alice:profile")
}
```

### Set Expiration

```graphql
mutation {
  expire(key: "user:alice:profile", ttl: 7200)
}
```

## Queue Operations

### Enqueue a Job

```graphql
mutation {
  enqueueJob(input: {
    type: "email"
    payload: "{\"to\":\"alice@example.com\",\"subject\":\"Welcome!\",\"body\":\"Hello Alice!\"}"
    priority: 5
  }) {
    id
    type
    status
    createdAt
  }
}
```

### Get Job Status

```graphql
query {
  job(id: "job_12345") {
    id
    type
    status
    payload
    priority
    retries
    createdAt
    updatedAt
  }
}
```

### List Jobs

```graphql
query {
  jobs(status: PENDING) {
    id
    type
    status
    priority
    createdAt
  }
}
```

### Cancel a Job

```graphql
mutation {
  cancelJob(id: "job_12345")
}
```

### Retry a Failed Job

```graphql
mutation {
  retryJob(id: "job_12345")
}
```

## Cluster Operations

### View Shard Nodes

```graphql
query {
  shardNodes {
    id
    address
    state
    keyCount
    capacity
  }
}
```

### Add Shard Node

```graphql
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
```

### Get Cluster Statistics

```graphql
query {
  shardStats {
    totalKeys
    totalNodes
    rebalancing
    distribution
  }
}
```

### Find Node for Key

```graphql
query {
  nodeForKey(key: "user:alice:profile")
}
```

### Trigger Rebalancing

```graphql
mutation {
  triggerRebalance
}
```

### View Active Migrations

```graphql
query {
  activeMigrations {
    taskId
    sourceNode
    targetNode
    progress
    status
    keysTransferred
    totalKeys
  }
}
```

## Raft Operations

### Get Raft Status

```graphql
query {
  raftStatus {
    term
    isLeader
    leader
    state
  }
}
```

### List Raft Peers

```graphql
query {
  raftPeers {
    id
    address
  }
}
```

### Add Raft Peer

```graphql
mutation {
  addRaftPeer(input: {
    id: "node2"
    address: "localhost:8002"
  })
}
```

### Remove Raft Peer

```graphql
mutation {
  removeRaftPeer(id: "node2")
}
```

### Get Cluster Status

```graphql
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

## Real-time Subscriptions

### Subscribe to Job Updates

```graphql
subscription {
  jobUpdated(jobId: "job_12345") {
    id
    status
    progress
    updatedAt
  }
}
```

### Subscribe to Migration Progress

```graphql
subscription {
  migrationProgress(taskId: "migration_123") {
    taskId
    progress
    keysTransferred
    totalKeys
    status
  }
}
```

### Subscribe to Cluster Events

```graphql
subscription {
  clusterEvent {
    type
    nodeId
    timestamp
    data
  }
}
```

### Subscribe to Key Changes

```graphql
subscription {
  keyChanged(pattern: "user:*") {
    key
    operation
    value
    timestamp
  }
}
```

## Using the CLI

### Query Operations

```bash
# Health check
helios-cli graphql query "{ health { status } }"

# Get current user (with authentication)
helios-cli graphql query "{ me { username } }" --token <your-token>

# Get a key
helios-cli graphql query "{ get(key: \"user:alice\") { value } }"

# List keys
helios-cli graphql query "{ keys(prefix: \"user:\") { key value } }"
```

### Mutation Operations

```bash
# Register
helios-cli graphql mutate "mutation { register(input: { username: \"alice\", password: \"pass123\" }) { token user { username } } }"

# Login
helios-cli graphql mutate "mutation { login(input: { username: \"alice\", password: \"pass123\" }) { token } }"

# Set a key
helios-cli graphql mutate "mutation { set(input: { key: \"mykey\", value: \"myvalue\" }) { key value } }"

# Enqueue a job
helios-cli graphql mutate "mutation { enqueueJob(input: { type: \"task\", payload: \"{\\\"data\\\":\\\"value\\\"}\" }) { id status } }"
```

### With Variables

```bash
# Using variables for cleaner syntax
helios-cli graphql query "query GetKey(\$key: String!) { get(key: \$key) { value } }" \
  --vars '{"key":"user:alice"}'

# Mutation with variables
helios-cli graphql mutate "mutation SetKey(\$input: SetInput!) { set(input: \$input) { key } }" \
  --vars '{"input":{"key":"test","value":"data"}}'
```

### Introspection

```bash
# Get full schema
helios-cli graphql introspect
```

## Using with cURL

### Query

```bash
curl -X POST http://localhost:8443/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{ health { status } }"}'
```

### Authenticated Query

```bash
curl -X POST http://localhost:8443/graphql \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-token>" \
  -d '{"query":"{ me { username } }"}'
```

### Mutation

```bash
curl -X POST http://localhost:8443/graphql \
  -H "Content-Type: application/json" \
  -d '{
    "query": "mutation { register(input: { username: \"alice\", password: \"pass123\" }) { token user { username } } }"
  }'
```

### With Variables

```bash
curl -X POST http://localhost:8443/graphql \
  -H "Content-Type: application/json" \
  -d '{
    "query": "mutation SetKey($input: SetInput!) { set(input: $input) { key value } }",
    "variables": {
      "input": {
        "key": "mykey",
        "value": "myvalue"
      }
    }
  }'
```

## Using with JavaScript

### Fetch API

```javascript
async function graphqlQuery(query, variables = {}) {
  const response = await fetch('http://localhost:8443/graphql', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      // Add token if needed
      // 'Authorization': 'Bearer <token>'
    },
    body: JSON.stringify({ query, variables })
  });

  return response.json();
}

// Usage
const result = await graphqlQuery(`
  query {
    health {
      status
      timestamp
    }
  }
`);

console.log(result.data);
```

### Apollo Client

```javascript
import { ApolloClient, InMemoryCache, gql, createHttpLink } from '@apollo/client';
import { setContext } from '@apollo/client/link/context';

// HTTP link
const httpLink = createHttpLink({
  uri: 'http://localhost:8443/graphql',
});

// Auth link
const authLink = setContext((_, { headers }) => {
  const token = localStorage.getItem('token');
  return {
    headers: {
      ...headers,
      authorization: token ? `Bearer ${token}` : '',
    }
  };
});

// Client
const client = new ApolloClient({
  link: authLink.concat(httpLink),
  cache: new InMemoryCache(),
});

// Query
const { data } = await client.query({
  query: gql`
    query {
      me {
        username
      }
    }
  `,
});
```

## Using with Python

```python
import requests
import json

class GraphQLClient:
    def __init__(self, url, token=None):
        self.url = url
        self.token = token

    def execute(self, query, variables=None):
        headers = {'Content-Type': 'application/json'}
        if self.token:
            headers['Authorization'] = f'Bearer {self.token}'

        payload = {'query': query}
        if variables:
            payload['variables'] = variables

        response = requests.post(
            self.url,
            headers=headers,
            data=json.dumps(payload)
        )
        return response.json()

# Usage
client = GraphQLClient('http://localhost:8443/graphql')

# Register
result = client.execute('''
    mutation {
        register(input: {
            username: "alice"
            password: "pass123"
        }) {
            token
            user { username }
        }
    }
''')

token = result['data']['register']['token']

# Authenticated request
client = GraphQLClient('http://localhost:8443/graphql', token=token)
result = client.execute('query { me { username } }')
print(result)
```

## Common Patterns

### Pagination

```graphql
query {
  keys(prefix: "user:", limit: 10, offset: 0) {
    key
    value
  }
}
```

### Filtering

```graphql
query {
  jobs(status: PENDING) {
    id
    type
    priority
  }
}
```

### Nested Queries

```graphql
query {
  me {
    username
    createdAt
  }
  health {
    status
  }
  metrics {
    timestamp
    data
  }
}
```

### Aliases

```graphql
query {
  user1: get(key: "user:alice") { value }
  user2: get(key: "user:bob") { value }
}
```

### Fragments

```graphql
fragment JobFields on Job {
  id
  type
  status
  priority
  createdAt
}

query {
  pending: jobs(status: PENDING) {
    ...JobFields
  }
  running: jobs(status: RUNNING) {
    ...JobFields
  }
}
```

## Error Handling

GraphQL errors are returned in the response:

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

**Common error messages:**
- `"User not authenticated"` - Missing or invalid token
- `"Key not found"` - Requested key doesn't exist
- `"Invalid input"` - Validation error
- `"Internal server error"` - Server error

## Best Practices

1. **Request only needed fields**:
   ```graphql
   # Good
   query { me { username } }

   # Avoid
   query { me { id username email createdAt updatedAt roles } }
   ```

2. **Use variables for dynamic values**:
   ```graphql
   # Good
   query GetUser($key: String!) {
     get(key: $key) { value }
   }
   ```

3. **Handle errors properly**:
   ```javascript
   const result = await graphqlQuery(query);
   if (result.errors) {
     console.error('GraphQL errors:', result.errors);
   }
   ```

4. **Use subscriptions for real-time data**:
   ```graphql
   subscription {
     jobUpdated(jobId: $jobId) {
       status
       progress
     }
   }
   ```

## Troubleshooting

### CORS Issues

If you get CORS errors, check the configuration:

```yaml
graphql:
  allowed_origins:
    - "http://localhost:3000"
    - "https://yourdomain.com"
```

### Authentication Errors

Make sure you're sending the token in the header:

```
Authorization: Bearer <token>
```

### Query Complexity

If queries are rejected, simplify them or increase limits:

```yaml
graphql:
  max_query_depth: 15
  max_complexity: 2000
```

## Next Steps

- Read the [GraphQL Implementation Guide](./GRAPHQL_IMPLEMENTATION.md)
- Explore the [API Reference](./GRAPHQL_API_REFERENCE.md)
- Check [Security Best Practices](./GRAPHQL_IMPLEMENTATION.md#security)
- Learn about [Subscriptions](./GRAPHQL_IMPLEMENTATION.md#subscriptions)

## Support

For issues or questions:
- GitHub Issues: `https://github.com/helios/helios/issues`
- Documentation: `https://helios-docs.example.com`
- Community: `https://community.helios.example.com`
