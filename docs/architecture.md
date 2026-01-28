# HELIOS Architecture

## Overview

HELIOS is a self-contained backend framework that combines a durable key-value store (ATLAS), authentication, rate limiting, job queue, and reverse proxy capabilities into a cohesive system.

## System Components

### 1. ATLAS - Core KV Store

ATLAS provides durable, atomic storage with:
- In-memory key-value store
- Append-Only File (AOF) for durability
- Periodic snapshots (RDB-style)
- TTL/expiration support
- Crash recovery with replay

**Key Features:**
- Durability guarantee: AOF-first ordering
- Single-threaded command execution for consistency
- JSON-lines command format
- Configurable fsync policies

### 2. API Gateway

The API Gateway handles:
- HTTP/HTTPS endpoints
- JWT authentication
- Rate limiting per client
- RBAC permission checks
- Request/response logging
- Metrics collection

**Endpoints:**
- `/api/v1/auth/*` - Authentication
- `/api/v1/kv/*` - Key-value operations
- `/api/v1/jobs/*` - Job queue management
- `/admin/*` - Admin operations

### 3. Authentication & RBAC

**Authentication:**
- User registration and login
- Password hashing with bcrypt
- JWT token generation
- Token validation and revocation

**RBAC:**
- Role-based permissions
- Default roles: admin, user, readonly
- Permission checking middleware
- Role assignment/revocation

### 4. Rate Limiter

Token bucket algorithm with:
- Per-client rate limiting
- Configurable capacity and refill rate
- Integer arithmetic for precision
- Durable state in ATLAS

### 5. Worker Queue

Job orchestration with:
- PENDING → IN_PROGRESS → DONE/FAILED states
- Visibility timeout and lease management
- Automatic retry with max attempts
- Dead Letter Queue (DLQ) for failed jobs
- Deduplication support

### 6. Reverse Proxy

Load balancing and routing with:
- Multiple backends per route
- Health checking
- Circuit breaker pattern
- Load balancing algorithms: round-robin, least-conn, weighted
- Dynamic backend configuration

### 7. Observability

**Metrics:**
- Prometheus-compatible metrics
- Request rates and latencies
- AOF and snapshot metrics
- Queue depth and job metrics
- Backend health metrics

**Logging:**
- Structured JSON logs
- Log levels: debug, info, warn, error, fatal
- Component-based logging

**Tracing:**
- OpenTelemetry integration
- Distributed tracing support
- Span context propagation

## Data Model

All data is stored in ATLAS with prefixed keys:

```
auth:user:{userId}          → User object
auth:username:{username}    → userId lookup
auth:token:{tokenHash}      → Token metadata
rbac:role:{role}            → Role definition
rbac:user_roles:{userId}    → User's roles
kv:{key}                    → Application data
rate:{clientId}             → Rate limiter bucket
job:{jobId}                 → Job definition
job:dedup:{dedupId}         → Deduplication tracking
proxy:backend:{id}          → Backend configuration
proxy:route:{id}            → Route definition
```

## Persistence Model

### AOF (Append-Only File)

All write operations are logged as JSON commands:

```json
{"cmd":"SET","key":"foo","value":"bar","ttl":0}
{"cmd":"DEL","key":"foo"}
{"cmd":"EXPIRE","key":"bar","ttl":60}
```

**Write Path:**
1. Append command to AOF
2. Fsync (based on policy)
3. Apply to in-memory state
4. Return success

### Snapshots

Periodic full snapshots using Go's gob encoding:
- Atomic file writes (tmp → rename)
- Background snapshot with online writes
- Snapshot + AOF = complete recovery

### Crash Recovery

1. Load latest snapshot (if exists)
2. Replay AOF commands after snapshot timestamp
3. Restore in-memory state
4. Resume operations

## Deployment

### Binary Commands

- `helios-atlasd` - ATLAS daemon (standalone KV store)
- `helios-gateway` - API Gateway with embedded ATLAS
- `helios-proxy` - Reverse proxy
- `helios-worker` - Job worker

### Configuration

Configuration via YAML file (`configs/default.yaml`):
- Data directories
- Network addresses
- Durability settings
- Rate limits
- Timeouts

### Docker Support

Each component can run as a container:
- Persistent volumes for data directories
- Health checks
- Graceful shutdown

## Scalability

**Current (Single-Node):**
- Single ATLAS instance
- All components share embedded ATLAS
- Suitable for moderate loads

**Future (Multi-Node):**
- Raft consensus for ATLAS replication
- Distributed workers
- Multiple gateway instances
- Sharded keyspace

## Security

- TLS for all external connections
- Password hashing with bcrypt
- JWT token authentication
- RBAC for authorization
- Rate limiting for DoS protection
- Audit logging

## Monitoring

### Key Metrics

- `helios_requests_total` - Request count by endpoint
- `helios_request_duration_seconds` - Request latency
- `atlas_aof_bytes_total` - AOF size
- `atlas_snapshot_duration_seconds` - Snapshot time
- `worker_job_queue_depth` - Queue depth
- `rate_limiter_denied_total` - Rate limit denials
- `proxy_backend_healthy` - Backend health

### Dashboards

Grafana dashboards for:
- Request rates and latencies
- Queue depths and job processing
- Backend health and circuit breakers
- Resource utilization

## Testing

### Unit Tests
- Store operations
- Command parsing
- Rate limiter arithmetic
- Job state machine

### Integration Tests
- End-to-end workflows
- Crash recovery
- AOF replay
- Multi-component interaction

### Chaos Testing
- Random process kills
- Network partitions
- Disk full scenarios
- Corrupt AOF handling

## Operational Guidelines

### Backup

1. Stop writes or use snapshot
2. Copy `dump.rdb` and `appendonly.aof`
3. Store securely off-site

### Restore

1. Place snapshot and AOF in data directory
2. Start ATLAS daemon
3. Verify data integrity

### AOF Maintenance

- Monitor AOF size
- Trigger manual snapshots
- Use `aof-check.js` for validation
- Truncate corrupt sections if needed

### Performance Tuning

- Adjust fsync policy based on durability needs
- Tune snapshot intervals
- Configure rate limits appropriately
- Scale workers based on job load

## Troubleshooting

### High Latency
- Check AOF fsync settings
- Monitor disk I/O
- Review rate limiter configuration

### Jobs Not Processing
- Check worker status
- Verify lease timeouts
- Review DLQ for failed jobs

### Memory Usage
- Monitor key count
- Check TTL settings
- Consider snapshot frequency

### Recovery Failures
- Use `aof-check.js` to validate AOF
- Restore from backup if needed
- Check disk space and permissions

## Contributing

See the main README for contribution guidelines.

## License

See LICENSE file for details.
