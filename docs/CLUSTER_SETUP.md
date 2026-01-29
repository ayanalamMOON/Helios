# Helios Multi-Node Cluster Configuration

This directory contains example configurations for running a 3-node Helios cluster with Raft consensus.

## Quick Start

### Starting a 3-Node Cluster

**Terminal 1 - Node 1:**
```bash
./bin/helios-atlasd \
  -data-dir=./data/node1 \
  -listen=:6379 \
  -raft=true \
  -raft-node-id=node-1 \
  -raft-addr=127.0.0.1:7000 \
  -raft-data-dir=./data/node1/raft
```

**Terminal 2 - Node 2:**
```bash
./bin/helios-atlasd \
  -data-dir=./data/node2 \
  -listen=:6380 \
  -raft=true \
  -raft-node-id=node-2 \
  -raft-addr=127.0.0.1:7001 \
  -raft-data-dir=./data/node2/raft
```

**Terminal 3 - Node 3:**
```bash
./bin/helios-atlasd \
  -data-dir=./data/node3 \
  -listen=:6381 \
  -raft=true \
  -raft-node-id=node-3 \
  -raft-addr=127.0.0.1:7002 \
  -raft-data-dir=./data/node3/raft
```

### Configuring Peers

After starting the nodes, you need to configure peers. This can be done via:

1. **Environment Variables** (before starting):
```bash
export HELIOS_RAFT_PEERS="node-2:127.0.0.1:7001,node-3:127.0.0.1:7002"
```

2. **Configuration File** (see `cluster-node1.yaml` example below)

3. **Runtime API** (future enhancement)

## Configuration Files

### Node 1 Configuration (cluster-node1.yaml)

```yaml
atlas:
  data_dir: "./data/node1"
  aof_fsync: "every"
  snapshot_interval_cmds: 10000
  snapshot_interval_time: "5m"

  raft:
    enabled: true
    node_id: "node-1"
    bind_addr: "127.0.0.1:7000"
    data_dir: "./data/node1/raft"
    heartbeat_timeout: "100ms"
    election_timeout: "500ms"
    snapshot_threshold: 10000

    peers:
      - id: "node-2"
        address: "127.0.0.1:7001"
      - id: "node-3"
        address: "127.0.0.1:7002"

gateway:
  listen: ":8443"

observability:
  log_level: "info"
  metrics_enabled: true
  metrics_port: ":9090"
```

### Node 2 Configuration (cluster-node2.yaml)

```yaml
atlas:
  data_dir: "./data/node2"
  aof_fsync: "every"
  snapshot_interval_cmds: 10000
  snapshot_interval_time: "5m"

  raft:
    enabled: true
    node_id: "node-2"
    bind_addr: "127.0.0.1:7001"
    data_dir: "./data/node2/raft"
    heartbeat_timeout: "100ms"
    election_timeout: "500ms"
    snapshot_threshold: 10000

    peers:
      - id: "node-1"
        address: "127.0.0.1:7000"
      - id: "node-3"
        address: "127.0.0.1:7002"

gateway:
  listen: ":8444"

observability:
  log_level: "info"
  metrics_enabled: true
  metrics_port: ":9091"
```

### Node 3 Configuration (cluster-node3.yaml)

```yaml
atlas:
  data_dir: "./data/node3"
  aof_fsync: "every"
  snapshot_interval_cmds: 10000
  snapshot_interval_time: "5m"

  raft:
    enabled: true
    node_id: "node-3"
    bind_addr: "127.0.0.1:7002"
    data_dir: "./data/node3/raft"
    heartbeat_timeout: "100ms"
    election_timeout: "500ms"
    snapshot_threshold: 10000

    peers:
      - id: "node-1"
        address: "127.0.0.1:7000"
      - id: "node-2"
        address: "127.0.0.1:7001"

gateway:
  listen: ":8445"

observability:
  log_level: "info"
  metrics_enabled: true
  metrics_port: ":9092"
```

## Testing the Cluster

### 1. Check Cluster Status

Connect to any node and check the status:
```bash
redis-cli -p 6379 INFO
```

Look for:
- `raft_enabled: true`
- `raft_state: Leader/Follower/Candidate`
- `raft_leader: node-X`

### 2. Write to Leader

Data written to the leader will be replicated to followers:
```bash
redis-cli -p 6379 SET mykey "hello cluster"
```

### 3. Read from Followers

Verify replication by reading from a follower:
```bash
redis-cli -p 6380 GET mykey
# Should return: "hello cluster"
```

### 4. Test Leader Failover

1. Stop the current leader node
2. Wait 1-2 seconds for election
3. A new leader will be elected automatically
4. Write operations continue with the new leader

### 5. Monitor Logs

Watch for Raft consensus logs:
```
[INFO] raft: Starting election [term 2]
[INFO] raft: Won election [votes 2 term 2]
[INFO] raft: Entering leader state [term 2]
[INFO] raft: Log replication successful [peer node-2 matchIndex 42]
```

## Production Deployment

### Recommendations

1. **Odd Number of Nodes**: Use 3, 5, or 7 nodes for optimal fault tolerance
   - 3 nodes: tolerates 1 failure
   - 5 nodes: tolerates 2 failures
   - 7 nodes: tolerates 3 failures

2. **Network Configuration**:
   - Use separate network interfaces for client traffic (port 6379) and Raft traffic (port 7000)
   - Ensure low latency between Raft peers (< 10ms)
   - Use dedicated VLANs or private networks for Raft communication

3. **Storage**:
   - Use SSDs for Raft log storage
   - Mount data directories on dedicated volumes
   - Enable fsync for durability in production

4. **Monitoring**:
   - Monitor Raft metrics (term, commit index, leader changes)
   - Set up alerts for split-brain or election failures
   - Track replication lag between leader and followers

5. **Backup Strategy**:
   - Take snapshots regularly from followers (not leader)
   - Store snapshots in object storage (S3, GCS, etc.)
   - Test recovery procedures regularly

## Docker Compose Example

```yaml
version: '3.8'

services:
  helios-node1:
    build: .
    command: >
      /app/helios-atlasd
      -data-dir=/data
      -listen=:6379
      -raft=true
      -raft-node-id=node-1
      -raft-addr=helios-node1:7000
      -raft-data-dir=/data/raft
    ports:
      - "6379:6379"
      - "7000:7000"
    volumes:
      - node1-data:/data
    networks:
      - helios-cluster

  helios-node2:
    build: .
    command: >
      /app/helios-atlasd
      -data-dir=/data
      -listen=:6379
      -raft=true
      -raft-node-id=node-2
      -raft-addr=helios-node2:7000
      -raft-data-dir=/data/raft
    ports:
      - "6380:6379"
      - "7001:7000"
    volumes:
      - node2-data:/data
    networks:
      - helios-cluster
    environment:
      - HELIOS_RAFT_PEERS=node-1:helios-node1:7000,node-3:helios-node3:7000

  helios-node3:
    build: .
    command: >
      /app/helios-atlasd
      -data-dir=/data
      -listen=:6379
      -raft=true
      -raft-node-id=node-3
      -raft-addr=helios-node3:7000
      -raft-data-dir=/data/raft
    ports:
      - "6381:6379"
      - "7002:7000"
    volumes:
      - node3-data:/data
    networks:
      - helios-cluster
    environment:
      - HELIOS_RAFT_PEERS=node-1:helios-node1:7000,node-2:helios-node2:7000

volumes:
  node1-data:
  node2-data:
  node3-data:

networks:
  helios-cluster:
    driver: bridge
```

Start the cluster:
```bash
docker-compose up -d
```

## Troubleshooting

### Split Brain Detection

If you see multiple leaders:
```bash
# Check each node
redis-cli -p 6379 INFO | grep raft_state
redis-cli -p 6380 INFO | grep raft_state
redis-cli -p 6381 INFO | grep raft_state
```

All nodes should agree on the leader. If not:
1. Check network connectivity between nodes
2. Review Raft logs for election issues
3. Verify peer configuration

### Election Timeouts

If elections fail repeatedly:
1. Increase `election_timeout` (e.g., to 1s)
2. Check network latency between nodes
3. Ensure nodes can reach each other on Raft ports

### Replication Lag

Monitor commit index differences:
- Leader should have highest commit index
- Followers should be within 10-100 entries
- Large gaps indicate network or performance issues

## Security Considerations

1. **TLS for Raft**: Future enhancement to encrypt Raft communication
2. **Authentication**: Use Redis AUTH or implement custom auth
3. **Firewall Rules**: Restrict Raft ports to cluster members only
4. **Network Segmentation**: Isolate cluster traffic from public internet

## Next Steps

- [ ] Implement dynamic peer addition/removal
- [ ] Add TLS support for Raft transport
- [ ] Implement read-your-writes consistency
- [ ] Add cluster status API endpoint
- [ ] Support configuration reloading
