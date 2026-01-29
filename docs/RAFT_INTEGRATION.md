# Raft Consensus Integration

## Overview

Helios now includes production-ready Raft consensus for distributed replication across multiple nodes. This enables high availability, fault tolerance, and strong consistency guarantees for your data.

## Key Features

- **Leader Election**: Automatic leader election with configurable timeouts
- **Log Replication**: Reliable replication of all write operations across the cluster
- **Fault Tolerance**: Cluster tolerates minority node failures (3 nodes = 1 failure, 5 nodes = 2 failures)
- **Persistence**: Durable log storage with snapshot support
- **Single-Node Optimization**: Automatically detects single-node clusters and bypasses election delays
- **Production Ready**: Comprehensive test coverage with all tests passing

## Architecture

```
┌─────────────────────────────────────────────────┐
│              Helios Cluster                      │
│                                                   │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   │
│  │  Node 1  │   │  Node 2  │   │  Node 3  │   │
│  │ (Leader) │   │(Follower)│   │(Follower)│   │
│  │          │   │          │   │          │   │
│  │  Raft ◄──┼───┼──► Raft ◄┼───┼──► Raft  │   │
│  │    ▼     │   │    ▼     │   │    ▼     │   │
│  │  Atlas   │   │  Atlas   │   │  Atlas   │   │
│  └──────────┘   └──────────┘   └──────────┘   │
│                                                   │
│  All writes go through Raft consensus            │
│  Reads can be served from any node              │
└─────────────────────────────────────────────────┘
```

## Quick Start

### Standalone Mode (Default)

```bash
./bin/helios-atlasd \
  --data-dir=/var/lib/helios \
  --listen=:6379
```

### Cluster Mode (3 Nodes)

**Node 1:**
```bash
./bin/helios-atlasd \
  --data-dir=./data/node1 \
  --listen=:6379 \
  --raft=true \
  --raft-node-id=node-1 \
  --raft-addr=127.0.0.1:7000 \
  --raft-data-dir=./data/node1/raft
```

**Node 2:**
```bash
./bin/helios-atlasd \
  --data-dir=./data/node2 \
  --listen=:6380 \
  --raft=true \
  --raft-node-id=node-2 \
  --raft-addr=127.0.0.1:7001 \
  --raft-data-dir=./data/node2/raft
```

**Node 3:**
```bash
./bin/helios-atlasd \
  --data-dir=./data/node3 \
  --listen=:6381 \
  --raft=true \
  --raft-node-id=node-3 \
  --raft-addr=127.0.0.1:7002 \
  --raft-data-dir=./data/node3/raft
```

### Using Helper Scripts

**Windows:**
```cmd
scripts\start-cluster.bat
```

**Linux/macOS:**
```bash
chmod +x scripts/start-cluster.sh
./scripts/start-cluster.sh
```

## Configuration

Add to `configs/default.yaml`:

```yaml
atlas:
  data_dir: "/var/lib/helios"

  raft:
    enabled: true
    node_id: "node-1"
    bind_addr: "127.0.0.1:7000"
    data_dir: "/var/lib/helios/raft"
    heartbeat_timeout: "100ms"
    election_timeout: "500ms"
    snapshot_threshold: 10000

    peers:
      - id: "node-2"
        address: "127.0.0.1:7001"
      - id: "node-3"
        address: "127.0.0.1:7002"
```

## Testing the Cluster

### 1. Check Cluster Status

```bash
redis-cli -p 6379 INFO
```

Look for:
- `raft_enabled: true`
- `raft_state: Leader/Follower/Candidate`
- `raft_leader: node-X`
- `raft_term: N`

### 2. Write Data

Write to any node (will be routed to leader):
```bash
redis-cli -p 6379 SET mykey "distributed-value"
```

### 3. Verify Replication

Read from different nodes to verify data is replicated:
```bash
redis-cli -p 6379 GET mykey
redis-cli -p 6380 GET mykey
redis-cli -p 6381 GET mykey
```

All should return: `"distributed-value"`

### 4. Test Leader Failover

1. Find the current leader:
   ```bash
   redis-cli -p 6379 INFO | grep raft_state
   ```

2. Stop the leader node (Ctrl+C or kill process)

3. Wait 1-2 seconds for election

4. Check new leader:
   ```bash
   redis-cli -p 6380 INFO | grep raft_state
   ```

5. Verify writes still work:
   ```bash
   redis-cli -p 6380 SET failover-test "still-working"
   ```

## Production Deployment

### Recommended Configuration

- **3 nodes**: Tolerates 1 failure, good for most use cases
- **5 nodes**: Tolerates 2 failures, better availability
- **Odd numbers**: Always use odd number of nodes (3, 5, 7)

### Network Requirements

- Low latency between Raft peers (<10ms recommended)
- Dedicated network for Raft traffic (separate from client traffic)
- Firewall rules allowing Raft ports between nodes only

### Storage

- Use SSDs for Raft log storage
- Separate volumes for data and Raft logs
- Enable fsync for durability

### Monitoring

Monitor these metrics:
- `raft_state`: Should be stable (not constantly changing)
- `raft_term`: Slowly increasing is normal
- `raft_leader`: Should remain stable
- Leader election time: Should be <2 seconds
- Commit index lag: Followers should stay within 100 entries of leader

## How It Works

1. **Write Request**: Client sends write (SET/DEL) to any node
2. **Forward to Leader**: Non-leader nodes reject or forward to leader
3. **Log Append**: Leader appends to its local log
4. **Replicate**: Leader sends log entry to all followers
5. **Majority Ack**: Leader waits for majority to acknowledge
6. **Commit**: Leader marks entry as committed
7. **Apply**: All nodes apply committed entry to their state machine (Atlas store)
8. **Response**: Success returned to client

### Consistency Guarantees

- **Linearizability**: All operations appear to execute atomically at some point between invocation and response
- **Strong Consistency**: Reads reflect all completed writes
- **No Split-Brain**: Only one leader per term, guaranteed by quorum

## Implementation Details

### Components

- **`internal/raft`**: Complete Raft implementation
  - Leader election with randomized timeouts
  - Log replication with batching
  - Snapshot and log compaction
  - Persistent state management

- **`internal/atlas/raftatlas.go`**: Integration layer
  - Wraps Atlas with Raft consensus
  - FSM (Finite State Machine) implementation
  - Transparent failover handling

### FSM (Finite State Machine)

The Raft FSM translates between Raft log entries and Atlas operations:

```go
type raftFSM struct {
    atlas *Atlas
}

func (f *raftFSM) Apply(command interface{}) interface{} {
    // Deserialize command
    // Apply to Atlas store
    // Return result
}

func (f *raftFSM) Snapshot() ([]byte, error) {
    // Create point-in-time snapshot
}

func (f *raftFSM) Restore([]byte) error {
    // Restore from snapshot
}
```

### Single-Node Optimization

Raft automatically detects single-node clusters and bypasses election:

```go
if len(r.peers) == 0 {
    // Immediately become leader
    r.currentTerm = 1
    r.state = Leader
    // Skip election delays
}
```

## Troubleshooting

### Split Brain

**Symptoms**: Multiple leaders in cluster

**Solution**:
1. Check network connectivity between nodes
2. Verify all nodes have same peer configuration
3. Ensure clocks are synchronized (NTP)
4. Check firewall rules

### Election Failures

**Symptoms**: No leader elected, constant elections

**Solution**:
1. Increase election timeout: `--election-timeout=1s`
2. Check network latency between nodes
3. Verify Raft ports are accessible
4. Check system load (high CPU can delay elections)

### Replication Lag

**Symptoms**: Followers far behind leader in commit index

**Solution**:
1. Check network bandwidth between nodes
2. Reduce write rate temporarily
3. Increase snapshot frequency
4. Check disk I/O performance

## Performance Tuning

### Timeouts

```yaml
raft:
  heartbeat_timeout: "100ms"  # Lower = faster failure detection, higher network load
  election_timeout: "500ms"    # Lower = faster elections, more likely split votes
```

### Snapshots

```yaml
raft:
  snapshot_threshold: 10000  # Lower = smaller logs, more CPU; Higher = larger logs, less CPU
```

### Batching

Raft automatically batches log entries for replication. No configuration needed.

## API Usage

### Programmatic Access

```go
import "github.com/helios/helios/internal/atlas"

// Create cluster-aware Atlas
cfg := &atlas.Config{
    DataDir: "/var/lib/helios",
}

raftCfg := &atlas.RaftConfig{
    Enabled:          true,
    NodeID:           "node-1",
    BindAddr:         "127.0.0.1:7000",
    DataDir:          "/var/lib/helios/raft",
    Peers: []atlas.RaftPeer{
        {ID: "node-2", Address: "127.0.0.1:7001"},
        {ID: "node-3", Address: "127.0.0.1:7002"},
    },
}

ra, err := atlas.NewRaftAtlas(cfg, raftCfg)
if err != nil {
    log.Fatal(err)
}
defer ra.Close()

// Check leadership
if ra.IsLeader() {
    fmt.Println("I am the leader!")
}

// Get cluster stats
stats := ra.Stats()
fmt.Printf("Raft State: %v\n", stats["raft_state"])
fmt.Printf("Raft Term: %v\n", stats["raft_term"])
fmt.Printf("Raft Leader: %v\n", stats["raft_leader"])
```

## Further Reading

- [Raft Paper](https://raft.github.io/raft.pdf) - Original Raft consensus algorithm
- [Cluster Setup Guide](CLUSTER_SETUP.md) - Detailed multi-node setup
- [Architecture Overview](architecture.md) - Helios system architecture

## Test Coverage

All Raft tests passing:
- TestRaftBasicElection - Leader election
- TestRaftLogReplication - Log replication
- TestRaftLeaderFailover - Leader failover
- TestRaftSnapshot - Snapshotting

Run tests:
```bash
go test ./test/raft/... -v
```
