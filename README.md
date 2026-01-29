# HELIOS

A self-contained backend framework combining durable storage, authentication, rate limiting, job orchestration, reverse proxy, and **distributed consensus** capabilities.

![HELIOS Documentation](docs/images/helios-docs-hero.png)

## Overview

HELIOS integrates **ATLAS** (a Redis-like key-value store) with essential backend services into a cohesive framework. It provides production-grade features with strong durability guarantees, **multi-node replication via Raft consensus**, and comprehensive observability.

## Features

- **ATLAS KV Store**: Durable key-value storage with AOF and snapshots
- **🆕 Raft Consensus**: Production-ready distributed consensus for multi-node replication
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

- ✅ **Leader Election**: Automatic leader election with randomized timeouts
- ✅ **Log Replication**: Consistent replication across all nodes
- ✅ **Safety Guarantees**: Strong consistency even with network partitions
- ✅ **Snapshotting**: Automatic log compaction
- ✅ **Fault Tolerance**: Tolerates minority node failures
- ✅ **Persistence**: Durable state for crash recovery

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

- [ ] Raft consensus for multi-node replication
- [ ] Horizontal sharding for large datasets
- [ ] WebSocket support
- [ ] GraphQL API
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

## Documentation

- [Architecture](docs/architecture.md) - Detailed system design
- [API Reference](docs/api.md) - Complete API documentation (TBD)
- [Operations Guide](docs/operations.md) - Deployment and maintenance (TBD)

## License

MIT License - see LICENSE file for details.

## Support

- GitHub Issues: [https://github.com/helios/helios/issues](https://github.com/helios/helios/issues)
- Documentation: [https://helios.dev/docs](https://helios.dev/docs)

## Acknowledgments

HELIOS is inspired by Redis, etcd, and modern backend frameworks. Special thanks to the Go community and all contributors.
