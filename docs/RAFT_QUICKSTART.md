# Raft Cluster Quick Start Guide

This guide shows how to quickly set up and run a 3-node Raft cluster with Helios ATLAS.

## Prerequisites

- Go 1.21 or later
- Git
- 3 terminal windows (or tmux/screen)

## Setup

### 1. Clone and Build

```bash
cd ~/Projects/Helios
go mod tidy
go build -o bin/raft-example cmd/raft-example/main.go
```

### 2. Create Data Directories

```bash
mkdir -p data/node-1 data/node-2 data/node-3
```

### 3. Start Node 1 (Terminal 1)

```bash
./bin/raft-example node-1
```

Expected output:
```
2024/01/15 10:00:00 [INFO] raft: Raft node initialized nodeID=node-1 term=0 lastLogIndex=0
2024/01/15 10:00:00 [INFO] raft: Starting Raft node nodeID=node-1
2024/01/15 10:00:00 Node node-1 started on 127.0.0.1:9001
2024/01/15 10:00:01 Node node-1 is the LEADER
2024/01/15 10:00:01 Writing test data...
2024/01/15 10:00:01 ✓ Set user:1:name = Alice
2024/01/15 10:00:01 ✓ Set user:2:name = Bob (TTL: 5m)
2024/01/15 10:00:01 ✓ Set counter = 1
```

### 4. Start Node 2 (Terminal 2)

```bash
./bin/raft-example node-2
```

Expected output:
```
2024/01/15 10:00:05 [INFO] raft: Raft node initialized nodeID=node-2 term=0 lastLogIndex=0
2024/01/15 10:00:05 [INFO] raft: Starting Raft node nodeID=node-2
2024/01/15 10:00:05 Node node-2 started on 127.0.0.1:9002
2024/01/15 10:00:06 Node node-2 is a FOLLOWER
```

### 5. Start Node 3 (Terminal 3)

```bash
./bin/raft-example node-3
```

Expected output:
```
2024/01/15 10:00:10 [INFO] raft: Raft node initialized nodeID=node-3 term=0 lastLogIndex=0
2024/01/15 10:00:10 [INFO] raft: Starting Raft node nodeID=node-3
2024/01/15 10:00:10 Node node-3 started on 127.0.0.1:9003
2024/01/15 10:00:11 Node node-3 is a FOLLOWER
```

## Verify Cluster

### Check Leader

Look for the node that prints "is the LEADER" - this is the elected leader.

### Check Replication

All nodes should replicate the data. The leader will apply commands first, then replicate to followers.

### Monitor Logs

Watch the log output to see:
- Leader election
- Log replication
- Heartbeats
- Command application

## Test Failover

### 1. Stop the Leader

In the terminal running the leader, press `Ctrl+C`:

```
^C
2024/01/15 10:05:00 Shutting down...
2024/01/15 10:05:00 [INFO] raft: Shutting down Raft node
2024/01/15 10:05:00 Shutdown complete
```

### 2. Observe New Election

Within 150-300ms, one of the remaining nodes will become the new leader:

```
2024/01/15 10:05:00 [INFO] raft: Starting election term=2
2024/01/15 10:05:00 [INFO] raft: Vote granted candidate=node-2 term=2
2024/01/15 10:05:00 [INFO] raft: Became leader term=2
```

### 3. Restart the Old Leader

```bash
./bin/raft-example node-1
```

It will rejoin as a follower and sync with the current leader.

## Production Deployment

For production deployment, you'll want to:

### 1. Use Separate Machines

Update the cluster configuration in `main.go`:

```go
clusterConfig := map[string]struct {
    Addr string
    Peers map[string]string
}{
    "node-1": {
        Addr: "10.0.1.10:9001",  // Real IP
        Peers: map[string]string{
            "node-2": "10.0.1.11:9001",
            "node-3": "10.0.1.12:9001",
        },
    },
    // ... more nodes
}
```

### 2. Configure Storage

Ensure adequate disk space for logs and snapshots:

```bash
# Create dedicated partition
sudo mkdir -p /var/lib/helios/raft
sudo chown helios:helios /var/lib/helios/raft

# Update config
config.DataDir = "/var/lib/helios/raft"
```

### 3. Add Systemd Service

Create `/etc/systemd/system/helios-raft@.service`:

```ini
[Unit]
Description=Helios Raft Node %i
After=network.target

[Service]
Type=simple
User=helios
Group=helios
ExecStart=/usr/local/bin/raft-example %i
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

Start the service:

```bash
sudo systemctl enable helios-raft@node-1
sudo systemctl start helios-raft@node-1
sudo systemctl status helios-raft@node-1
```

### 4. Monitor

Check logs:

```bash
# Follow logs
sudo journalctl -u helios-raft@node-1 -f

# View recent logs
sudo journalctl -u helios-raft@node-1 -n 100
```

Monitor metrics:
- Leader election count
- Log replication lag
- Snapshot frequency
- RPC error rate

### 5. Backup

Regular backups of the data directory:

```bash
# Snapshot the data directory
tar -czf raft-backup-$(date +%Y%m%d).tar.gz /var/lib/helios/raft
```

## Troubleshooting

### No Leader Elected

**Symptom**: All nodes stuck as followers

**Causes**:
- Network partition
- All nodes can't reach each other
- Clock skew too large

**Solution**:
```bash
# Check connectivity
ping 10.0.1.11  # From node-1 to node-2

# Check ports
nc -zv 10.0.1.11 9001

# Check firewall
sudo iptables -L | grep 9001
```

### Split Brain

**Symptom**: Multiple leaders

**Causes**:
- Network partition creating two majorities
- Bug in implementation (shouldn't happen)

**Solution**:
- This implementation uses majority voting - split brain is impossible with proper configuration
- Ensure odd number of nodes (3, 5, 7)

### Log Replication Slow

**Symptom**: High replication lag

**Causes**:
- Network latency
- Disk I/O bottleneck
- Too many entries per append

**Solution**:
```go
// Tune configuration
config.HeartbeatTimeout = 100 * time.Millisecond  // Increase for high latency
config.MaxEntriesPerAppend = 50  // Reduce batch size
```

### Snapshot Too Large

**Symptom**: Snapshots consuming too much disk

**Causes**:
- Large state machine
- Frequent snapshots
- Not cleaning old snapshots

**Solution**:
```go
// Increase threshold
config.SnapshotThreshold = 50000

// Increase interval
config.SnapshotInterval = 30 * time.Minute
```

### High CPU Usage

**Symptom**: Raft consuming too much CPU

**Causes**:
- Too many elections (unstable leader)
- Network issues causing retries
- Busy wait in event loop

**Solution**:
- Check network stability
- Increase heartbeat timeout
- Monitor leader stability

## Performance Tips

### 1. Tune Timeouts

```go
// Low latency network (same datacenter)
config.HeartbeatTimeout = 50 * time.Millisecond
config.ElectionTimeout = 150 * time.Millisecond

// High latency network (cross-region)
config.HeartbeatTimeout = 200 * time.Millisecond
config.ElectionTimeout = 1 * time.Second
```

### 2. Batch Operations

Batch multiple commands into a single Apply() call when possible:

```go
// Instead of multiple Apply() calls
commands := [][]byte{cmd1, cmd2, cmd3}
for _, cmd := range commands {
    raft.Apply(cmd, timeout)
}

// Batch into one
batchCmd := encodeBatch(commands)
raft.Apply(batchCmd, timeout)
```

### 3. Read Optimization

For read-heavy workloads:

```go
// Read from local follower (may be slightly stale)
value := ra.Get(key)

// Read from leader (linearizable, slower)
if !ra.IsLeader() {
    return forwardToLeader(key)
}
value := ra.Get(key)
```

### 4. Use SSDs

Raft performance is heavily dependent on disk I/O for persistence. Use SSDs for best performance.

## Next Steps

- **Add TLS**: Secure RPC communication
- **Add Authentication**: Protect cluster membership
- **Add Monitoring**: Integrate with Prometheus/Grafana
- **Load Test**: Test under production load
- **Disaster Recovery**: Document recovery procedures

## Support

For issues or questions:
- GitHub Issues: https://github.com/ayanalamMOON/Helios/issues
- Documentation: `/docs/RAFT_IMPLEMENTATION.md`
- Raft Paper: https://raft.github.io/raft.pdf

## Example Cluster Status

Here's what a healthy 3-node cluster looks like:

```
Node 1 (Leader):
- State: Leader
- Term: 5
- Commit Index: 1247
- Last Applied: 1247
- Peers: 2 (both healthy)

Node 2 (Follower):
- State: Follower
- Term: 5
- Commit Index: 1247
- Last Applied: 1247
- Leader: node-1

Node 3 (Follower):
- State: Follower
- Term: 5
- Commit Index: 1247
- Last Applied: 1247
- Leader: node-1
```

All commit indices match - cluster is consistent! 🎉
