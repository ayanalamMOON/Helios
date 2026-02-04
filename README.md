# HELIOS

A self-contained backend framework combining durable storage, authentication, rate limiting, job orchestration, reverse proxy, and **distributed consensus** capabilities.

![HELIOS Documentation](assets/Screenshot%202026-01-29%20191717.png)

## Overview

HELIOS integrates **ATLAS** (a Redis-like key-value store) with essential backend services into a cohesive framework. It provides production-grade features with strong durability guarantees, **multi-node replication via Raft consensus**, and comprehensive observability.

## Features

- **ATLAS KV Store**: Durable key-value storage with AOF and snapshots
- **Raft Consensus**: Production-ready distributed consensus for multi-node replication
- **Horizontal Sharding**: Distribute data across multiple nodes for massive scale
- **GraphQL API**: Full-featured GraphQL interface with queries, mutations, and subscriptions
- **WebSocket Support**: Real-time bidirectional communication for live updates
- **Authentication**: JWT-based auth with bcrypt password hashing
- **RBAC**: Role-based access control with flexible permissions
- **Rate Limiting**: Token bucket algorithm with per-client limits
- **Job Queue**: Reliable job orchestration with retries and DLQ
- **Reverse Proxy**: Load balancing with health checks and circuit breakers
- **Observability**: Prometheus metrics, structured logging, OpenTelemetry tracing

## Architecture

```
┌───────────────────────────────────────────────────────────┐
│                    Helios Cluster                         │
│                                                            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │   Node 1     │  │   Node 2     │  │   Node 3     │   │
│  │  (Leader)    │  │  (Follower)  │  │  (Follower)  │   │
│  │              │  │              │  │              │   │
│  │ ┌──────────┐ │  │ ┌──────────┐ │  │ ┌──────────┐ │   │
│  │ │   Raft   │◄┼──┼►│   Raft   │◄┼──┼►│   Raft   │ │   │
│  │ └────┬─────┘ │  │ └────┬─────┘ │  │ └────┬─────┘ │   │
│  │      │       │  │      │       │  │      │       │   │
│  │ ┌────▼─────┐ │  │ ┌────▼─────┐ │  │ ┌────▼─────┐ │   │
│  │ │  ATLAS   │ │  │ │  ATLAS   │ │  │ │  ATLAS   │ │   │
│  │ │  Store   │ │  │ │  Store   │ │  │ │  Store   │ │   │
│  │ └──────────┘ │  │ └──────────┘ │  │ └──────────┘ │   │
│  └──────────────┘  └──────────────┘  └──────────────┘   │
│                                                            │
│  ┌──────────────────────────────────────────────────┐   │
│  │      Consistent Replicated State                  │   │
│  └──────────────────────────────────────────────────┘   │
└───────────────────────────────────────────────────────────┘

┌───────────────┐
│ Reverse Proxy │ → Load balancing & health checks
└──────┬────────┘
       │
┌──────▼───────┐
│ API Gateway  │ → Auth, RBAC, Rate Limiting
└──────┬───────┘
       │
┌──────▼───────┐
│   Workers    │ → Job processing
└──────┬───────┘
       │
┌──────▼───────┐
│ Raft Cluster │ → Distributed consensus & replication
└──────────────┘
```

## Quick Start

### Prerequisites

- Go 1.21 or later
- Linux/macOS/Windows

### Installation

```bash
# Clone the repository
git clone https://github.com/helios/helios.git
cd helios

# Build all binaries
go build -o bin/helios-atlasd ./cmd/helios-atlasd
go build -o bin/helios-gateway ./cmd/helios-gateway
go build -o bin/helios-proxy ./cmd/helios-proxy
go build -o bin/helios-worker ./cmd/helios-worker
go build -o bin/helios-cli ./cmd/helios-cli

# Or use make
make build

# Create data directory
mkdir -p /var/lib/helios
```

### Running Components

#### ATLAS Daemon (Standalone)

```bash
./bin/helios-atlasd \
  --data-dir=/var/lib/helios \
  --listen=:6379 \
  --sync-mode=every
```

#### ATLAS Cluster (3-Node with Raft)

**Node 1:**
```bash
./bin/helios-atlasd \
  --data-dir=/var/lib/helios/node1 \
  --listen=:6379 \
  --raft=true \
  --raft-node-id=node-1 \
  --raft-addr=10.0.0.1:7000 \
  --raft-data-dir=/var/lib/helios/node1/raft
```

**Node 2:**
```bash
./bin/helios-atlasd \
  --data-dir=/var/lib/helios/node2 \
  --listen=:6379 \
  --raft=true \
  --raft-node-id=node-2 \
  --raft-addr=10.0.0.2:7000 \
  --raft-data-dir=/var/lib/helios/node2/raft
```

**Node 3:**
```bash
./bin/helios-atlasd \
  --data-dir=/var/lib/helios/node3 \
  --listen=:6379 \
  --raft=true \
  --raft-node-id=node-3 \
  --raft-addr=10.0.0.3:7000 \
  --raft-data-dir=/var/lib/helios/node3/raft
```

See [Cluster Setup Guide](docs/CLUSTER_SETUP.md) for detailed multi-node configuration.

**Quick Local Cluster:**
```bash
# On Windows
.\scripts\start-cluster.bat

# On Linux/macOS
./scripts/start-cluster.sh
```

#### API Gateway

```bash
./bin/helios-gateway \
  --listen=:8443 \
  --data-dir=/var/lib/helios
```

#### Reverse Proxy

```bash
./bin/helios-proxy \
  --listen=:8080
```

#### Worker

```bash
./bin/helios-worker \
  --worker-id=worker-1 \
  --poll-interval=5s
```

#### CLI Tool

The `helios-cli` tool provides a command-line interface for interacting with ATLAS:

```bash
# Basic KV operations
./bin/helios-cli --host=localhost --port=6379 set mykey "hello world"
./bin/helios-cli --host=localhost --port=6379 get mykey
./bin/helios-cli --host=localhost --port=6379 del mykey

# Cluster management (requires Raft-enabled cluster)
./bin/helios-cli --host=localhost --port=6379 addpeer node-4 127.0.0.1:7003
./bin/helios-cli --host=localhost --port=6379 removepeer node-4
./bin/helios-cli --host=localhost --port=6379 listpeers

# GraphQL operations
./bin/helios-cli graphql \
  --endpoint=http://localhost:8443/graphql \
  --token=<jwt_token> \
  --query='query { me { id username email } }'
```

For detailed cluster management instructions, see [CLUSTER_SETUP.md](docs/CLUSTER_SETUP.md).

## Configuration

Create `configs/config.yaml`:

```yaml
atlas:
  data_dir: "/var/lib/helios"
  aof_fsync: "every"
  snapshot_interval_time: "5m"

gateway:
  listen: ":8443"
  rate_limit:
    default_capacity: 100
    default_rate_num: 1
    default_rate_den: 1

proxy:
  listen: ":8080"
  health_check_interval: "5s"

worker:
  poll_interval: "5s"
  max_attempts: 3
```

## API Examples

### Authentication

```bash
# Register user
curl -X POST http://localhost:8443/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"secret123"}'

# Login
curl -X POST http://localhost:8443/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"secret123"}'

# Response: {"token":"...", "user_id":"..."}
```

### Key-Value Operations

```bash
# Set key
curl -X POST http://localhost:8443/api/v1/kv/mykey \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"value":"myvalue","ttl":3600}'

# Get key
curl -X GET http://localhost:8443/api/v1/kv/mykey \
  -H "Authorization: Bearer <token>"

# Delete key
curl -X DELETE http://localhost:8443/api/v1/kv/mykey \
  -H "Authorization: Bearer <token>"
```

### GraphQL API

```bash
# GraphQL endpoint
curl -X POST http://localhost:8443/graphql \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "query { me { id username email roles { name permissions } } }"
  }'

# Create a role (mutation)
curl -X POST http://localhost:8443/graphql \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "mutation { createRole(input: {name: \"editor\", permissions: [\"read\", \"write\"]}) { id name permissions } }"
  }'

# Subscribe to user updates (WebSocket)
# Connect to ws://localhost:8443/graphql with:
{
  "type": "subscribe",
  "id": "1",
  "payload": {
    "query": "subscription { userUpdated { id username email } }"
  }
}
```

See [GraphQL Quick Start](docs/GRAPHQL_QUICKSTART.md) for detailed GraphQL API documentation.

### WebSocket API

```javascript
// Connect to WebSocket endpoint
const ws = new WebSocket('ws://localhost:8443/ws');

// Send message
ws.send(JSON.stringify({
  type: 'message',
  payload: { text: 'Hello, HELIOS!' }
}));

// Receive message
ws.onmessage = (event) => {
  const data = JSON.parse(event.data);
  console.log('Received:', data);
};
```

See [WebSocket Implementation](docs/WEBSOCKET_IMPLEMENTATION.md) for detailed WebSocket API documentation.
```

### GraphQL API

Helios includes a complete GraphQL API for type-safe, flexible queries.

**Access GraphQL Playground**: Navigate to `http://localhost:8443/graphql/playground`

**Example Query:**
```graphql
query {
  me {
    username
  }
  health {
    status
  }
}
```

**Example Mutation:**
```graphql
mutation {
  set(input: {
    key: "user:123"
    value: "{\"name\":\"Alice\"}"
    ttl: 3600
  }) {
    key
    value
  }
}
```

**Using CLI:**
```bash
# Execute a query
./bin/helios-cli graphql query "{ health { status } }"

# Execute a mutation
./bin/helios-cli graphql mutate "mutation { login(input: { username: \"alice\", password: \"pass\" }) { token } }"

# With authentication
./bin/helios-cli graphql query "{ me { username } }" --token <token>
```

**Features:**
- Type-safe queries and mutations
- Real-time subscriptions (WebSocket)
- Interactive playground UI
- Introspection support
- Authentication integration
- Cluster management operations
- Job queue operations

For complete GraphQL documentation, see:
- [GraphQL Implementation Guide](docs/GRAPHQL_IMPLEMENTATION.md)
- [GraphQL Quickstart](docs/GRAPHQL_QUICKSTART.md)
```

### Job Queue

```bash
# Enqueue job
curl -X POST http://localhost:8443/api/v1/jobs \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"payload":{"task":"process_data","data":"..."}}'

# List jobs
curl -X GET http://localhost:8443/api/v1/jobs?status=PENDING \
  -H "Authorization: Bearer <token>"

# Get job details
curl -X GET http://localhost:8443/api/v1/jobs/{job_id} \
  -H "Authorization: Bearer <token>"
```

## Distributed Consensus with Raft

### Overview

HELIOS now includes a **production-ready Raft consensus implementation** for distributed, fault-tolerant replication across multiple nodes.

### Features

- **Leader Election**: Automatic leader election with randomized timeouts
- **Log Replication**: Consistent replication across all nodes
- **Safety Guarantees**: Strong consistency even with network partitions
- **Snapshotting**: Automatic log compaction
- **Fault Tolerance**: Tolerates minority node failures
- **Persistence**: Durable state for crash recovery
- **TLS Support**: Encrypted Raft communication with mutual authentication
- **Dynamic Membership**: Add/remove cluster members without restart

### Quick Start

Build and run a 3-node cluster:

```bash
# Build the Raft example
go build -o bin/raft-example cmd/raft-example/main.go

# Terminal 1: Start Node 1
./bin/raft-example node-1

# Terminal 2: Start Node 2
./bin/raft-example node-2

# Terminal 3: Start Node 3
./bin/raft-example node-3
```

### Cluster Configuration

Configure nodes for production deployment:

```go
config := raft.DefaultConfig()
config.NodeID = "node-1"
config.ListenAddr = "10.0.1.10:9001"
config.DataDir = "/var/lib/helios/raft"
config.HeartbeatTimeout = 50 * time.Millisecond
config.ElectionTimeout = 150 * time.Millisecond
```

### Integration with ATLAS

```go
// Create Raft-backed ATLAS store
fsm := NewAtlasFSM(store)
applyCh := make(chan raft.ApplyMsg, 1000)
node, _ := raft.New(config, transport, fsm, applyCh)

// Add peers
node.AddPeer("node-2", "10.0.1.11:9001")
node.AddPeer("node-3", "10.0.1.12:9001")

// Start consensus
node.Start(ctx)

// All writes go through Raft
if _, isLeader := node.GetState(); isLeader {
    cmd := []byte(`{"op":"set","key":"foo","value":"bar"}`)
    node.Apply(cmd, 5*time.Second)
}
```

### Documentation

- **[Raft Implementation Guide](docs/RAFT_IMPLEMENTATION.md)** - Complete technical documentation
- **[Raft Quick Start](docs/RAFT_QUICKSTART.md)** - Deployment guide
- **[Raft README](internal/raft/README.md)** - API reference

### Architecture

```
┌─────────────────────────────────────────────────────────┐
│                  Helios Cluster                         │
│                                                          │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐ │
│  │   Node 1     │  │   Node 2     │  │   Node 3     │ │
│  │  (Leader)    │◄─┼─►(Follower)  │◄─┼─►(Follower)  │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘ │
│         │                 │                 │          │
│    ┌────▼─────────────────▼─────────────────▼────┐    │
│    │      Consistent Replicated State            │    │
│    └─────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────┘
```

### Cluster Sizes

- **3 nodes**: Tolerates 1 failure (recommended minimum)
- **5 nodes**: Tolerates 2 failures (recommended for production)
- **7 nodes**: Tolerates 3 failures (high availability)

## Horizontal Sharding

### Overview

HELIOS includes **horizontal sharding** for distributing data across multiple nodes to handle datasets larger than a single machine's capacity.

### Features

- **Consistent Hashing**: Automatic, balanced distribution using virtual nodes
- **Dynamic Topology**: Add/remove nodes without cluster restart
- **Automatic Rebalancing**: Optional auto-rebalancing when topology changes
- **Data Migration**: Controlled migration with rate limiting
- **Replication Support**: Configurable replication factor across shards
- **Monitoring**: Real-time statistics and health monitoring

### Quick Start

**Enable sharding in configuration:**

```yaml
sharding:
  enabled: true
  virtual_nodes: 150
  replication_factor: 3
  auto_rebalance: true
  migration_rate: 1000
  rebalance_interval: "1h"
```

**Add nodes to the cluster:**

```bash
# Start multiple nodes
./bin/helios-atlasd --node-id=shard-1 --listen=:6379 &
./bin/helios-atlasd --node-id=shard-2 --listen=:6380 &
./bin/helios-atlasd --node-id=shard-3 --listen=:6381 &

# Register nodes
./bin/helios-cli addnode shard-1 localhost:6379
./bin/helios-cli addnode shard-2 localhost:6380
./bin/helios-cli addnode shard-3 localhost:6381

# Verify setup
./bin/helios-cli clusterstats
```

**Operations automatically route to correct shard:**

```bash
# Keys automatically distributed based on consistent hashing
./bin/helios-cli set user:alice "Alice Data"  # -> shard-2
./bin/helios-cli set user:bob "Bob Data"      # -> shard-1
./bin/helios-cli set user:carol "Carol Data"  # -> shard-3

# Retrievals route to correct shard automatically
./bin/helios-cli get user:alice  # -> queries shard-2
```

### Sharding CLI Commands

```bash
# Node management
./bin/helios-cli addnode <node-id> <address>
./bin/helios-cli removenode <node-id>
./bin/helios-cli listnodes

# Key routing
./bin/helios-cli nodeforkey <key>

# Data migration
./bin/helios-cli migrate <source-node> <target-node> [key-pattern]

# Monitoring
./bin/helios-cli clusterstats
```

### REST API

```bash
# Add node
curl -X POST http://localhost:8443/api/v1/shards/nodes \
  -H "Authorization: Bearer <token>" \
  -d '{"node_id":"shard-4","address":"localhost:6382"}'

# Get cluster stats
curl http://localhost:8443/api/v1/shards/stats \
  -H "Authorization: Bearer <token>"

# Find node for key
curl "http://localhost:8443/api/v1/shards/node?key=user:alice" \
  -H "Authorization: Bearer <token>"
```

### Combining Sharding with Raft

Use sharding for horizontal scaling and Raft for fault tolerance:

```yaml
# Each shard is a Raft cluster
immutable:
  raft_enabled: true

sharding:
  enabled: true
  replication_factor: 1  # Raft handles replication within shard
```

**Architecture:**
```
Shard 1 (keys A-G):  3-node Raft cluster (shard-1a, 1b, 1c)
Shard 2 (keys H-N):  3-node Raft cluster (shard-2a, 2b, 2c)
Shard 3 (keys O-Z):  3-node Raft cluster (shard-3a, 3b, 3c)
```

See [SHARDING.md](docs/SHARDING.md) for complete documentation.

## Persistence & Durability

### AOF (Append-Only File)

All write operations are logged before being applied:

```json
{"cmd":"SET","key":"foo","value":"bar","ttl":0}
{"cmd":"DEL","key":"foo"}
```

**Fsync Modes:**
- `every` - Fsync after every write (strongest durability)
- `interval` - Periodic fsync (balanced)
- `none` - OS-managed (weakest)

### Snapshots

Periodic snapshots for faster recovery:
- Atomic writes (tmp → rename)
- Configurable interval
- Background snapshot with online writes

### Recovery

On startup:
1. Load latest snapshot
2. Replay AOF commands
3. Restore full state

## Monitoring

### Metrics Endpoint

```bash
curl http://localhost:9090/metrics
```

### Key Metrics

- `helios_requests_total` - Request count
- `helios_request_duration_seconds` - Latency
- `atlas_store_keys` - Key count
- `worker_job_queue_depth` - Queue depth
- `rate_limiter_denied_total` - Rate limit denials
- `proxy_backend_healthy` - Backend health

### Logging

Structured JSON logs:

```json
{
  "timestamp": "2026-01-29T10:00:00Z",
  "level": "INFO",
  "component": "gateway",
  "message": "Request processed",
  "extra": {"endpoint": "/api/v1/kv/foo", "duration_ms": 5}
}
```

## Testing

### Run Tests

```bash
go test ./...
```

### Benchmark

```bash
./scripts/benchmark.sh
```

### AOF Validation

```bash
node scripts/aof-check.js /var/lib/helios/appendonly.aof
```

## Security

- **TLS**: Enable for production (`tls_enabled: true`)
- **Passwords**: Bcrypt with configurable cost
- **Tokens**: Short-lived JWTs with refresh mechanism
- **RBAC**: Permission-based access control
- **Rate Limiting**: DoS protection

## Performance

### Single-Node Capacity

- **Writes**: ~10K-50K ops/sec (fsync-dependent)
- **Reads**: ~100K+ ops/sec
- **Memory**: Depends on dataset size
- **Disk**: AOF write throughput critical

### Tuning

- Adjust `aof_fsync` for durability vs. performance trade-off
- Configure snapshot interval based on dataset size
- Scale workers horizontally for job processing
- Use multiple gateway instances behind load balancer

## Deployment

### Docker

```dockerfile
FROM golang:1.21 AS builder
WORKDIR /app
COPY . .
RUN go build -o helios-gateway ./cmd/helios-gateway

FROM debian:bookworm-slim
COPY --from=builder /app/helios-gateway /usr/local/bin/
VOLUME /var/lib/helios
EXPOSE 8443
CMD ["helios-gateway"]
```

### Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: helios-gateway
spec:
  replicas: 3
  template:
    spec:
      containers:
      - name: gateway
        image: helios/gateway:latest
        ports:
        - containerPort: 8443
        volumeMounts:
        - name: data
          mountPath: /var/lib/helios
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: helios-data
```

## Roadmap

- [x] Raft consensus for multi-node replication
- [x] Horizontal sharding for large datasets
- [x] WebSocket support
- [x] GraphQL API
- [ ] Admin UI dashboard
- [ ] Built-in backup/restore tools
- [ ] Plugin system

## Contributing

Contributions welcome! Please:

1. Fork the repository
2. Create a feature branch
3. Add tests for new functionality
4. Ensure all tests pass
5. Submit a pull request

## Project Structure

```
Helios/
├── bin/                    # Compiled binaries
├── cmd/                    # Application entry points
│   ├── helios-atlasd/      # ATLAS daemon
│   ├── helios-gateway/     # API gateway
│   ├── helios-proxy/       # Reverse proxy
│   ├── helios-worker/      # Job worker
│   └── helios-cli/         # CLI tool
├── configs/                # Configuration templates
├── docs/                   # Documentation
│   ├── architecture.md     # System architecture
│   ├── CLUSTER_SETUP.md    # Multi-node cluster setup
│   ├── CLUSTER_STATUS_API.md # Cluster status API reference
│   ├── CONFIG_HISTORY.md   # Configuration versioning
│   ├── GRAPHQL_IMPLEMENTATION.md # GraphQL API implementation guide
│   ├── GRAPHQL_QUICKSTART.md # GraphQL quick start guide
│   ├── WEBSOCKET_IMPLEMENTATION.md # WebSocket implementation guide
│   ├── SHARDING.md         # Sharding architecture and design
│   ├── SHARDING_IMPLEMENTATION_SUMMARY.md # Sharding implementation details
│   ├── RAFT_IMPLEMENTATION.md # Raft consensus implementation
│   ├── CONFIG_MIGRATION.md # Configuration migration guide
│   ├── CONFIG_RELOAD.md    # Hot reload documentation
│   ├── PEER_MANAGEMENT_IMPLEMENTATION.md
│   ├── QUICKSTART.md       # Quick start guide
│   ├── RAFT_IMPLEMENTATION.md # Raft consensus details
│   ├── RAFT_INTEGRATION.md # Integration guide
│   ├── RAFT_QUICKSTART.md  # Raft deployment guide
│   └── TEST_REPORT.md      # Test coverage report
├── examples/               # Example configurations
├── internal/               # Private application code
│   ├── api/                # API gateway implementation
│   ├── atlas/              # ATLAS KV store
│   ├── auth/               # Authentication & RBAC
│   ├── config/             # Configuration management
│   ├── observability/      # Metrics & logging
│   ├── proxy/              # Reverse proxy
│   ├── queue/              # Job queue
│   ├── raft/               # Raft consensus implementation
│   └── rate/               # Rate limiting
├── scripts/                # Build & utility scripts
├── test/                   # Integration tests
│   └── raft/               # Raft cluster integration tests
├── docker-compose.yml      # Docker development setup
├── Dockerfile              # Container build
├── Makefile                # Build automation
└── README.md               # This file
```

### Test Organization

- **Unit tests** (`*_test.go`) are located alongside source files in `internal/` following Go conventions
- **Integration tests** for multi-node scenarios are in `test/`
- Run all tests: `go test ./...`
- Run integration tests only: `go test ./test/...`

## Documentation

| Document                                           | Description                   |
| -------------------------------------------------- | ----------------------------- |
| [Architecture](docs/architecture.md)               | Detailed system design        |
| [Cluster Setup](docs/CLUSTER_SETUP.md)             | Multi-node cluster deployment |
| [Cluster Status API](docs/CLUSTER_STATUS_API.md)   | Status monitoring endpoints   |
| [Horizontal Sharding](docs/SHARDING.md)            | Sharding setup and management |
| [Raft Implementation](docs/RAFT_IMPLEMENTATION.md) | Consensus algorithm details   |
| [Raft Quick Start](docs/RAFT_QUICKSTART.md)        | Raft deployment guide         |
| [Config Migration](docs/CONFIG_MIGRATION.md)       | Configuration versioning      |
| [Config Reload](docs/CONFIG_RELOAD.md)             | Hot reload feature            |
| [Quick Start](docs/QUICKSTART.md)                  | Getting started guide         |

## License

MIT License - see LICENSE file for details.

## Support

- GitHub Issues: [https://github.com/helios/helios/issues](https://github.com/helios/helios/issues)
- Documentation: [https://helios.dev/docs](https://helios.dev/docs)

## Acknowledgments

HELIOS is inspired by Redis, etcd, and modern backend frameworks. Special thanks to the Go community and all contributors.
