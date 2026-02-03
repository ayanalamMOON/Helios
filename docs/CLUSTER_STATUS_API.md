# Cluster Status API Implementation

## Overview

Implemented a comprehensive cluster status API endpoint that provides detailed information about the Raft cluster's health, state, and replication status. This feature is now production-ready and fully integrated with the Helios framework.

## Implementation Summary

### New Components

#### 1. Raft Accessor Methods (`internal/raft/raft.go`)

Added the following public methods for gathering cluster state:

- **`GetNodeID() string`**: Returns the node's unique identifier
- **`GetCommitIndex() uint64`**: Returns the highest log index known to be committed
- **`GetLastApplied() uint64`**: Returns the highest log index applied to the state machine
- **`GetLastLogInfo() (uint64, uint64)`**: Returns the last log index and term
- **`GetTLSConfig() *TLSConfig`**: Returns TLS configuration if enabled
- **`GetUptime() time.Duration`**: Returns time since node started
- **`GetMatchIndex(peerID string) uint64`**: Returns match index for a specific peer
- **`GetNextIndex(peerID string) uint64`**: Returns next index for a specific peer

#### 2. Log LastTerm Method (`internal/raft/log.go`)

- **`LastTerm() uint64`**: Returns the term of the last log entry

#### 3. Cluster Status Types (`internal/atlas/raftatlas.go` & `internal/api/gateway.go`)

Comprehensive status structures:

```go
type ClusterStatus struct {
    Node     NodeStatus    // Current node info (ID, state, term)
    Leader   LeaderInfo    // Leader info (ID, address, is_leader flag)
    Indices  IndicesInfo   // Log indices (last_log, commit, applied)
    Peers    []PeerStatus  // Peer status with replication indices
    Sessions SessionsInfo  // Active session count and IDs
    TLS      TLSStatus     // TLS configuration status
    Uptime   string        // Node uptime as formatted duration
}
```

#### 4. RaftAtlas Integration (`internal/atlas/raftatlas.go`)

- **`GetClusterStatus() (interface{}, error)`**: Orchestrates gathering of all cluster status information

#### 5. API Gateway Handler (`internal/api/gateway.go`)

- **`handleClusterStatus(w, r)`**: HTTP handler at `/admin/cluster/status`
- Requires admin authentication
- Returns JSON response with comprehensive cluster information

### Features

1. **Node Information**
   - Node ID, current state (Leader/Follower/Candidate), current term

2. **Leader Information**
   - Leader ID and address
   - Boolean flag indicating if this node is the leader

3. **Log Indices**
   - Last log index and term
   - Commit index (highest known committed entry)
   - Applied index (highest applied to state machine)

4. **Peer Replication Status** (only when node is leader)
   - Match index: Highest log entry replicated on each peer
   - Next index: Next log entry to send to each peer

5. **Session Statistics**
   - Active session count for read-your-writes consistency
   - Session IDs (included only if count ≤ 10 to avoid verbose output)

6. **TLS Status**
   - Enabled/disabled flag
   - Peer verification status

7. **Uptime**
   - Formatted duration since node started

### API Endpoint

**Endpoint:** `GET /admin/cluster/status`

**Authentication:** Required (admin role)

**Example Request:**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/cluster/status
```

**Example Response:**
```json
{
  "node": {
    "id": "node-1",
    "state": "Leader",
    "term": 5
  },
  "leader": {
    "id": "node-1",
    "address": "127.0.0.1:7000",
    "is_leader": true
  },
  "indices": {
    "last_log": 125,
    "last_log_term": 5,
    "commit_index": 125,
    "applied_index": 125
  },
  "peers": [
    {
      "id": "node-2",
      "address": "127.0.0.1:7001",
      "match_index": 125,
      "next_index": 126
    },
    {
      "id": "node-3",
      "address": "127.0.0.1:7002",
      "match_index": 125,
      "next_index": 126
    }
  ],
  "sessions": {
    "active_count": 3,
    "session_ids": ["session-abc123", "session-def456", "session-ghi789"]
  },
  "tls": {
    "enabled": true,
    "verify_peer": true
  },
  "uptime": "2h15m30s"
}
```

## Testing

### Unit Tests

1. **Accessor Methods Test** (`internal/raft/accessor_test.go`)
   - Tests all new Raft accessor methods
   - Validates correct retrieval of node state, indices, and peer information
   - Tests uptime calculation and TLS configuration access

2. **Log LastTerm Test** (`internal/raft/accessor_test.go`)
   - Tests LastTerm() method with empty and populated logs
   - Validates correct term retrieval

3. **API Gateway Test** (`internal/api/gateway_cluster_test.go`)
   - Updated TestHandleClusterStatus to validate comprehensive response format
   - Tests authentication and authorization requirements
   - Validates JSON response structure

### Test Results

All tests pass successfully:
```
✓ internal/api tests (5 tests, all passing)
✓ internal/raft tests (8 tests, all passing)
✓ internal/atlas tests (all passing)
✓ Overall test suite (all passing)
```

## Files Modified

1. **internal/raft/raft.go**
   - Added startTime field to Raft struct for uptime tracking
   - Added 9 new public accessor methods
   - Modified New() to initialize startTime

2. **internal/raft/log.go**
   - Added LastTerm() method

3. **internal/atlas/raftatlas.go**
   - Added ClusterStatus and supporting types
   - Implemented GetClusterStatus() method

4. **internal/api/gateway.go**
   - Updated RaftManager interface with GetClusterStatus()
   - Added ClusterStatus types for API layer
   - Modified handleClusterStatus() to call comprehensive status method

5. **internal/api/gateway_cluster_test.go**
   - Updated mockRaftManager with GetClusterStatus() implementation
   - Updated TestHandleClusterStatus to validate new response format

6. **internal/raft/accessor_test.go** (new)
   - Comprehensive test suite for all accessor methods

7. **docs/CLUSTER_SETUP.md**
   - Added detailed cluster status endpoint documentation
   - Included example response and field descriptions
   - Marked cluster status API as complete in TODO list

## Integration

The cluster status API is fully integrated with:

- **Authentication System**: Requires valid bearer token
- **RBAC**: Requires admin role
- **Raft Layer**: Direct access to cluster state via accessor methods
- **Session Management**: Reports active read-your-writes sessions
- **TLS Configuration**: Reports TLS status

## Production Readiness

✅ **Complete Implementation**: All planned features implemented
✅ **Comprehensive Testing**: Unit tests cover all new code paths
✅ **Documentation**: Fully documented in CLUSTER_SETUP.md
✅ **No Breaking Changes**: Backward compatible with existing code
✅ **Clean Build**: No compilation errors or warnings
✅ **Thread-Safe**: All methods use appropriate locking

## Use Cases

1. **Cluster Monitoring**: Monitor cluster health and replication lag
2. **Operations Dashboard**: Build status dashboards for cluster management
3. **Debugging**: Diagnose replication issues and leader election problems
4. **Capacity Planning**: Track session counts and log growth
5. **Health Checks**: Automated health checking and alerting
6. **Troubleshooting**: Identify which peers are behind in replication

## Future Enhancements

Possible future additions (not implemented in this iteration):

- [x] Peer latency metrics ✓ (Implemented)
- [x] Historical uptime tracking ✓ (Implemented)
- [x] Snapshot metadata in status ✓ (Implemented)
- [ ] Custom metrics for monitoring systems
- [ ] WebSocket support for real-time status updates

---

## Historical Uptime Tracking

### Overview

The historical uptime tracking feature provides comprehensive monitoring of Raft node availability, session history, state transitions, and reliability metrics. This enables tracking of node uptime percentages, crash detection, MTBF (Mean Time Between Failures) calculations, and leadership statistics.

### Components

#### 1. Uptime Tracker (`internal/raft/uptime.go`)

The core uptime tracking module provides:

- **Session Management**: Tracks node sessions (start → stop) with persistence
- **Event History**: Circular buffer of uptime events (max 10,000 events, 1,000 sessions)
- **State Time Tracking**: Time spent in each Raft state (Leader, Follower, Candidate)
- **Crash Detection**: Automatic detection of unclean shutdowns on restart
- **Uptime Percentages**: Rolling uptime for 24h, 7d, 30d, and all-time
- **MTBF Calculation**: Mean Time Between Failures calculation
- **Leadership Metrics**: Leader time ratio, terms as leader, elections participated

#### 2. Prometheus Metrics (`internal/observability/metrics.go`)

New Prometheus metrics for uptime tracking:

```go
// Current session uptime
raft_node_uptime_seconds

// Total accumulated uptime
raft_node_total_uptime_seconds

// Total accumulated downtime
raft_node_total_downtime_seconds

// Uptime percentage by period (24h, 7d, 30d, all)
raft_node_uptime_percentage{period}

// Session counts
raft_node_sessions_total
raft_node_restarts_total
raft_node_crashes_total

// State duration tracking
raft_node_leader_time_seconds
raft_node_follower_time_seconds
raft_node_candidate_time_seconds

// Leadership metrics
raft_node_elections_total
raft_node_terms_as_leader_total
raft_node_leadership_ratio

// Reliability metric
raft_node_mtbf_seconds
```

### API Endpoints

#### GET /admin/cluster/uptime

Returns current uptime statistics for the node.

**Request:**
```bash
curl -H "Authorization: Bearer <token>" \
     http://localhost:8080/admin/cluster/uptime
```

**Response:**
```json
{
  "node_id": "node-1",
  "current_session": {
    "start_time": "2024-01-15T08:00:00Z",
    "uptime_seconds": 36000,
    "uptime_formatted": "10h0m0s",
    "current_state": "Leader",
    "state_since": "2024-01-15T08:05:00Z"
  },
  "historical": {
    "total_sessions": 15,
    "total_uptime_seconds": 864000,
    "total_downtime_seconds": 3600,
    "uptime_percentage_24h": 99.8,
    "uptime_percentage_7d": 99.5,
    "uptime_percentage_30d": 99.2,
    "uptime_percentage_all": 99.6,
    "restarts": 14,
    "crashes": 2,
    "mtbf_seconds": 432000
  },
  "state_time": {
    "leader_seconds": 500000,
    "follower_seconds": 360000,
    "candidate_seconds": 4000,
    "leadership_ratio": 0.58
  },
  "leadership": {
    "elections_participated": 20,
    "terms_as_leader": 8,
    "current_term": 42
  },
  "first_seen": "2024-01-01T00:00:00Z",
  "last_event": "2024-01-15T08:05:00Z"
}
```

#### GET /admin/cluster/uptime/history

Returns historical uptime events and sessions.

**Request:**
```bash
curl -H "Authorization: Bearer <token>" \
     "http://localhost:8080/admin/cluster/uptime/history?events=20&sessions=5"
```

**Query Parameters:**
- `events`: Number of recent events to return (default: 100, max: 1000)
- `sessions`: Number of recent sessions to return (default: 10, max: 100)

**Response:**
```json
{
  "node_id": "node-1",
  "recent_events": [
    {
      "type": "state_change",
      "timestamp": "2024-01-15T08:05:00Z",
      "state": "Leader",
      "previous_state": "Candidate",
      "term": 42,
      "leader_id": "node-1",
      "uptime_at_event": 300
    },
    {
      "type": "state_change",
      "timestamp": "2024-01-15T08:04:50Z",
      "state": "Candidate",
      "previous_state": "Follower",
      "term": 42,
      "uptime_at_event": 290
    },
    {
      "type": "start",
      "timestamp": "2024-01-15T08:00:00Z",
      "state": "Follower",
      "term": 41,
      "reason": "node_startup"
    }
  ],
  "recent_sessions": [
    {
      "session_id": "session-abc123",
      "start_time": "2024-01-15T08:00:00Z",
      "end_time": null,
      "duration_seconds": 36000,
      "end_reason": null,
      "states_visited": ["Follower", "Candidate", "Leader"],
      "terms_seen": [41, 42],
      "is_current": true
    },
    {
      "session_id": "session-xyz789",
      "start_time": "2024-01-14T10:00:00Z",
      "end_time": "2024-01-14T22:00:00Z",
      "duration_seconds": 43200,
      "end_reason": "graceful_shutdown",
      "states_visited": ["Follower", "Leader"],
      "terms_seen": [38, 39, 40, 41],
      "is_current": false
    }
  ],
  "total_events": 1250,
  "total_sessions": 15
}
```

#### POST /admin/cluster/uptime/reset

Resets uptime history (for administrative purposes).

**Request:**
```bash
curl -X POST -H "Authorization: Bearer <token>" \
     http://localhost:8080/admin/cluster/uptime/reset
```

**Response:**
```json
{
  "message": "Uptime history reset successfully"
}
```

### Cluster Status Integration

Uptime statistics are included in the main cluster status endpoint:

**GET /admin/cluster/status** now includes:

```json
{
  "node": { ... },
  "leader": { ... },
  "indices": { ... },
  "peers": [ ... ],
  "uptime_stats": {
    "current_uptime_seconds": 36000,
    "current_uptime_formatted": "10h0m0s",
    "uptime_percentage_24h": 99.8,
    "uptime_percentage_7d": 99.5,
    "total_restarts": 14,
    "total_crashes": 2,
    "mtbf_seconds": 432000,
    "leader_time_seconds": 500000,
    "leadership_ratio": 0.58
  },
  "sessions": { ... },
  "tls": { ... },
  "uptime": "10h0m0s"
}
```

### Crash Detection

The uptime tracker automatically detects crashes by:
1. On startup, loading the persisted uptime history
2. Checking if the last session ended cleanly
3. If not (no `EndTime` recorded), marking it as a crash
4. Recording a crash event with "crash: unclean shutdown" reason

### Persistence

Uptime history is persisted to a JSON file (`uptime-history.json`) in the data directory:
- Automatic saving every 5 minutes
- Saved on graceful shutdown
- Loaded on startup for continuity across restarts

### Example Prometheus Queries

```promql
# Current node uptime
raft_node_uptime_seconds

# 7-day uptime percentage
raft_node_uptime_percentage{period="7d"}

# Crash rate (crashes per hour over last 24h)
rate(raft_node_crashes_total[24h]) * 3600

# Time spent as leader vs follower
raft_node_leader_time_seconds / (raft_node_leader_time_seconds + raft_node_follower_time_seconds)

# Mean Time Between Failures
raft_node_mtbf_seconds

# Leadership ratio
raft_node_leadership_ratio
```

### Statistics Explained

| Metric                | Description                                        |
| --------------------- | -------------------------------------------------- |
| `uptime_percentage_*` | (Total Uptime / Total Time Window) × 100           |
| `mtbf_seconds`        | Total Uptime / Number of Crashes (0 if no crashes) |
| `leadership_ratio`    | Leader Time / (Leader Time + Follower Time)        |
| `restarts`            | Total graceful restarts                            |
| `crashes`             | Unclean shutdowns detected                         |

### Use Cases

1. **SLA Monitoring**: Track 99.9% uptime SLAs with rolling percentages
2. **Reliability Analysis**: Calculate MTBF for capacity planning
3. **Incident Review**: Examine session history to understand past outages
4. **Leadership Patterns**: Analyze leader election frequency and duration
5. **Operational Alerts**: Alert on crash counts or uptime drops
6. **Capacity Planning**: Use historical data to predict maintenance windows

---

## Peer Latency Metrics

### Overview

The peer latency metrics feature provides comprehensive tracking of RPC latency between Raft cluster peers. This enables monitoring of network health, identifying slow or unhealthy peers, and troubleshooting replication issues.

### Components

#### 1. Latency Tracker (`internal/raft/latency.go`)

The core latency tracking module provides:

- **Circular Buffer**: Efficient storage of recent latency samples (default: last 100 samples per RPC type)
- **Per-RPC Type Tracking**: Separate tracking for AppendEntries, RequestVote, and InstallSnapshot RPCs
- **Statistical Calculations**: Min, max, avg, P50, P90, P99, and standard deviation
- **Error Tracking**: Consecutive error counting and error rate calculation
- **Health Assessment**: Automatic peer health determination based on error patterns

#### 2. Prometheus Metrics (`internal/observability/metrics.go`)

New Prometheus metrics for peer latency:

```go
// Histogram for latency distribution
raft_peer_latency_seconds{peer_id, rpc_type}

// Counter for total RPCs
raft_peer_rpc_total{peer_id, rpc_type, result}

// Gauge for peer health
raft_peer_healthy{peer_id}

// Gauge for time since last contact
raft_peer_last_contact_seconds{peer_id}

// Gauge for consecutive errors
raft_peer_consecutive_errors{peer_id}

// Cluster-wide gauges
raft_cluster_healthy_peers
raft_cluster_unhealthy_peers
```

### API Endpoints

#### GET /admin/cluster/latency

Returns detailed latency metrics for all peers.

**Request:**
```bash
curl -H "Authorization: Bearer <token>" \
     http://localhost:8080/admin/cluster/latency
```

**Response:**
```json
{
  "peers": {
    "peer1": {
      "peer_id": "peer1",
      "address": "127.0.0.1:8001",
      "append_entries": {
        "count": 100,
        "last_value_ms": 2.5,
        "min_value_ms": 0.8,
        "max_value_ms": 15.2,
        "avg_value_ms": 2.1,
        "p50_value_ms": 1.8,
        "p90_value_ms": 4.5,
        "p99_value_ms": 12.3,
        "std_dev_ms": 1.2,
        "error_count": 2
      },
      "request_vote": { ... },
      "install_snapshot": { ... },
      "aggregated": { ... },
      "last_contact": "2024-01-15T10:30:00Z",
      "last_contact_unix": 1705315800,
      "reachable": true,
      "consecutive_errors": 0,
      "connection_healthy": true
    }
  },
  "aggregated": {
    "count": 500,
    "avg_value_ms": 2.3,
    "min_value_ms": 0.5,
    "max_value_ms": 20.1,
    "error_count": 5
  },
  "health_summary": {
    "healthy": 2,
    "unhealthy": 0,
    "total": 2
  }
}
```

#### GET /admin/cluster/latency?peer_id=<id>

Returns latency metrics for a specific peer.

**Request:**
```bash
curl -H "Authorization: Bearer <token>" \
     "http://localhost:8080/admin/cluster/latency?peer_id=peer1"
```

#### POST /admin/cluster/latency/reset

Resets all latency metrics.

**Request:**
```bash
curl -X POST -H "Authorization: Bearer <token>" \
     http://localhost:8080/admin/cluster/latency/reset
```

**Response:**
```json
{
  "message": "Peer latency metrics reset successfully"
}
```

### Cluster Status Integration

Latency metrics are also included in the main cluster status endpoint:

**GET /admin/cluster/status** now includes:

```json
{
  "node": { ... },
  "leader": { ... },
  "indices": { ... },
  "peers": [
    {
      "id": "peer1",
      "address": "127.0.0.1:8001",
      "match_index": 1234,
      "next_index": 1235,
      "latency": {
        "avg_latency_ms": 2.1,
        "min_latency_ms": 0.8,
        "max_latency_ms": 15.2,
        "p50_latency_ms": 1.8,
        "p90_latency_ms": 4.5,
        "p99_latency_ms": 12.3,
        "last_latency_ms": 2.5,
        "sample_count": 100,
        "error_count": 2,
        "error_rate": 0.02,
        "last_contact_unix": 1705315800,
        "reachable": true,
        "connection_healthy": true,
        "consecutive_errors": 0
      }
    }
  ],
  "cluster_health": {
    "healthy_peers": 2,
    "unhealthy_peers": 0,
    "total_peers": 2,
    "avg_cluster_latency_ms": 2.3,
    "max_cluster_latency_ms": 20.1,
    "cluster_error_rate": 0.01
  },
  "sessions": { ... },
  "tls": { ... },
  "uptime": "24h30m15s"
}
```

### Health Detection

A peer is considered **unhealthy** when:
- It has 3 or more consecutive RPC errors
- OR it hasn't been contacted in over 30 seconds

### Use Cases

1. **Network Monitoring**: Track latency trends to identify network degradation
2. **Performance Tuning**: Use P90/P99 metrics to identify tail latency issues
3. **Alerting**: Set up alerts based on error rates or latency thresholds
4. **Troubleshooting**: Identify which peers are experiencing issues
5. **Capacity Planning**: Understand cluster communication patterns

### Example Prometheus Queries

```promql
# Average latency per peer
avg by (peer_id) (rate(raft_peer_latency_seconds_sum[5m]) / rate(raft_peer_latency_seconds_count[5m]))

# RPC error rate per peer
sum by (peer_id) (rate(raft_peer_rpc_total{result="error"}[5m])) / sum by (peer_id) (rate(raft_peer_rpc_total[5m]))

# Number of unhealthy peers
raft_cluster_unhealthy_peers

# P99 latency
histogram_quantile(0.99, rate(raft_peer_latency_seconds_bucket[5m]))
```

---

## Snapshot Metadata Tracking

### Overview

The snapshot metadata tracking feature provides comprehensive monitoring and historical data for Raft snapshots. It tracks snapshot creation, reception, restoration, deletion, and errors, providing detailed statistics and metrics for operational visibility.

### Components

#### 1. Snapshot Metadata Tracker (`internal/raft/snapshot_metadata.go`)

The core snapshot tracking module provides:

- **Snapshot Metadata**: Comprehensive information about each snapshot (ID, index, term, size, creation time, etc.)
- **Statistics Tracking**: Aggregate statistics including creation counts, bytes written, entries compacted
- **Event History**: Circular buffer of snapshot events (max 1,000 events, 100 snapshots tracked)
- **Timing Metrics**: Average, min, max creation and restore times
- **Error Tracking**: Failed operations with last error details
- **Persistence**: JSON-based persistence with automatic loading on restart

#### 2. Prometheus Metrics (`internal/observability/metrics.go`)

New Prometheus metrics for snapshot tracking:

```go
// Snapshot counts by type
raft_snapshot_total{node_id, type}  // type: created, received, restored

// Current snapshot info
raft_snapshot_size{node_id}           // Size of latest snapshot
raft_snapshot_index{node_id}          // Index of latest snapshot
raft_snapshot_term{node_id}           // Term of latest snapshot
raft_snapshot_age_seconds{node_id}    // Age of latest snapshot

// Timing metrics
raft_snapshot_duration_seconds{node_id, operation}  // Histogram: create, restore

// Error tracking
raft_snapshot_errors{node_id, operation}  // Error counts by operation

// Cumulative metrics
raft_snapshot_bytes_written{node_id}      // Total bytes written
raft_snapshot_entries_compacted{node_id}  // Total entries compacted
raft_snapshot_on_disk{node_id}            // Number of snapshots on disk
```

### API Endpoints

#### GET /admin/cluster/snapshot

Returns current snapshot statistics for the node.

**Example Request:**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/cluster/snapshot
```

**Example Response:**
```json
{
  "has_snapshot": true,
  "latest_snapshot_id": "5-100",
  "latest_snapshot_index": 100,
  "latest_snapshot_term": 5,
  "latest_snapshot_size": 2048576,
  "latest_snapshot_time": "2024-01-15T10:30:00Z",
  "latest_snapshot_age_ms": 3600000,
  "latest_snapshot_age": "1h0m0s",
  "total_snapshots": 5,
  "total_snapshots_created": 4,
  "total_snapshots_received": 1,
  "total_restores": 2,
  "failed_creations": 0,
  "failed_restores": 0,
  "total_bytes_written": 10240000,
  "total_entries_compacted": 5000,
  "avg_snapshot_size": 2048000,
  "largest_snapshot_size": 3072000,
  "avg_creation_time_ms": 250.5,
  "max_creation_time_ms": 500.0,
  "last_creation_time_ms": 200.0,
  "avg_restore_time_ms": 100.0,
  "last_restore_time_ms": 80.0,
  "snapshot_interval_ms": 300000,
  "snapshot_interval": "5m0s",
  "snapshot_threshold": 1000,
  "retention_count": 2,
  "time_since_last_snapshot_ms": 120000,
  "time_since_last_snapshot": "2m0s",
  "snapshots_on_disk": 2
}
```

#### GET /admin/cluster/snapshot/history

Returns snapshot event history and snapshot metadata list.

**Query Parameters:**
- `events` (optional): Maximum number of events to return (default: 100, max: 1000)
- `snapshots` (optional): Maximum number of snapshots to return (default: 10, max: 100)

**Example Request:**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8443/admin/cluster/snapshot/history?events=50&snapshots=10"
```

**Example Response:**
```json
{
  "node_id": "node-1",
  "events": [
    {
      "timestamp": "2024-01-15T10:30:00Z",
      "type": "created",
      "snapshot_id": "5-100",
      "index": 100,
      "term": 5,
      "size": 2048576,
      "duration_ms": 200.0,
      "success": true,
      "entries_compacted": 1000
    },
    {
      "timestamp": "2024-01-15T09:30:00Z",
      "type": "restored",
      "snapshot_id": "4-80",
      "index": 80,
      "term": 4,
      "duration_ms": 80.0,
      "success": true
    }
  ],
  "snapshots": [
    {
      "id": "5-100",
      "index": 100,
      "term": 5,
      "size": 2048576,
      "created_at": "2024-01-15T10:30:00Z",
      "creation_duration": 200000000,
      "node_id": "node-1",
      "leader_id": "node-1",
      "is_from_peer": false,
      "peer_count": 2,
      "entries_compacted": 1000,
      "version": 1
    }
  ],
  "total_events": 50,
  "total_snapshots": 5
}
```

#### POST /admin/cluster/snapshot/reset

Resets all snapshot statistics. Useful for clearing history after maintenance.

**Example Request:**
```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/cluster/snapshot/reset
```

**Example Response:**
```json
{
  "message": "Snapshot statistics reset successfully"
}
```

### Cluster Status Integration

Snapshot metadata is included in the main cluster status response:

```json
{
  "node": { ... },
  "leader": { ... },
  "indices": { ... },
  "peers": [ ... ],
  "snapshot": {
    "has_snapshot": true,
    "latest_snapshot_id": "5-100",
    "latest_snapshot_index": 100,
    "latest_snapshot_term": 5,
    "latest_snapshot_size": 2048576,
    "latest_snapshot_age": "1h0m0s",
    "total_snapshots_created": 4,
    "total_snapshots_received": 1,
    "total_restores": 2,
    "failed_creations": 0,
    "failed_restores": 0,
    "avg_creation_time_ms": 250.5,
    "last_creation_time_ms": 200.0,
    "snapshot_interval": "5m0s",
    "snapshot_threshold": 1000,
    "snapshots_on_disk": 2
  },
  "uptime": { ... }
}
```

### Data Types

#### SnapshotMetadata

Comprehensive information about a single snapshot:

| Field             | Type      | Description                              |
| ----------------- | --------- | ---------------------------------------- |
| id                | string    | Unique identifier (term-index format)    |
| index             | uint64    | Last included log index                  |
| term              | uint64    | Last included log term                   |
| size              | int64     | Snapshot data size in bytes              |
| created_at        | time.Time | When snapshot was created                |
| creation_duration | duration  | How long it took to create               |
| node_id           | string    | Node that created the snapshot           |
| leader_id         | string    | Leader at time of snapshot               |
| is_from_peer      | bool      | True if received via InstallSnapshot RPC |
| peer_count        | int       | Number of peers at snapshot time         |
| entries_compacted | uint64    | Number of log entries compacted          |
| previous_index    | uint64    | Previous snapshot's index                |
| version           | int       | Snapshot format version                  |

#### SnapshotHistoryEntry

Event entry for snapshot operations:

| Field             | Type      | Description                                             |
| ----------------- | --------- | ------------------------------------------------------- |
| timestamp         | time.Time | When the event occurred                                 |
| type              | string    | Event type: created, received, restored, deleted, error |
| snapshot_id       | string    | Associated snapshot ID                                  |
| index             | uint64    | Snapshot index                                          |
| term              | uint64    | Snapshot term                                           |
| size              | int64     | Snapshot size (for created/received)                    |
| duration_ms       | float64   | Operation duration in milliseconds                      |
| success           | bool      | Whether operation succeeded                             |
| error_message     | string    | Error details if failed                                 |
| from_peer         | string    | Peer ID for received snapshots                          |
| entries_compacted | uint64    | Entries compacted (for created)                         |

### Example Prometheus Queries

```promql
# Snapshot creation rate
rate(raft_snapshot_total{type="created"}[1h])

# Average snapshot creation time
avg(raft_snapshot_duration_seconds{operation="create"})

# Total bytes written per hour
rate(raft_snapshot_bytes_written[1h])

# Snapshot error rate
rate(raft_snapshot_errors[5m])

# Time since last snapshot
time() - raft_snapshot_age_seconds

# Entries compacted per snapshot
rate(raft_snapshot_entries_compacted[1h]) / rate(raft_snapshot_total{type="created"}[1h])
```

### Use Cases

1. **Snapshot Monitoring**: Track snapshot frequency, size, and timing
2. **Performance Analysis**: Identify slow snapshot operations
3. **Capacity Planning**: Monitor storage growth from snapshots
4. **Debugging**: Diagnose snapshot failures and replication issues
5. **Alerting**: Set up alerts for snapshot failures or delays
6. **Compliance**: Track snapshot history for audit purposes

### Files Modified/Created

1. **internal/raft/snapshot_metadata.go** (new)
   - SnapshotMetadata, SnapshotStats, SnapshotStatsJSON types
   - SnapshotHistoryEntry, SnapshotHistory types
   - SnapshotMetadataTracker with all tracking methods
   - Persistence support (JSON file)

2. **internal/raft/raft.go**
   - Added snapshotMetaTracker field
   - Integrated tracker initialization
   - Added accessor methods

3. **internal/raft/persistence.go**
   - Integrated tracking in takeSnapshot()
   - Integrated tracking in restoreSnapshot()
   - Integrated tracking in handleInstallSnapshot()

4. **internal/observability/metrics.go**
   - Added 10 new Prometheus metrics for snapshots

5. **internal/atlas/raftatlas.go**
   - Added SnapshotStatusInfo type
   - Updated ClusterStatus with snapshot field
   - Added snapshot wrapper methods

6. **internal/api/gateway.go**
   - Extended RaftManager interface
   - Added 3 new API routes
   - Implemented handler methods

7. **internal/raft/snapshot_metadata_test.go** (new)
   - Comprehensive test suite for snapshot tracking
