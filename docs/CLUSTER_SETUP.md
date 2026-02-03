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

    # TLS configuration (optional, but recommended for production)
    tls:
      enabled: false
      cert_file: "./certs/node-1.crt"
      key_file: "./certs/node-1.key"
      ca_file: "./certs/ca.crt"
      verify_peer: true

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

- [x] Implement dynamic peer addition/removal
- [x] Add TLS support for Raft transport
- [x] Implement read-your-writes consistency
- [x] Add cluster status API endpoint
- [x] Support configuration reloading

## Configuration Reloading

Helios supports hot-reloading of configuration without service restart. This allows you to dynamically adjust operational parameters like log levels, rate limits, and timeouts while the system is running.

**Key Features:**
- **Automatic File Watching**: Monitors config file and reloads automatically on changes
- **Manual Reload**: API endpoint and SIGHUP signal support
- **Hot-reload without downtime**: Zero-downtime configuration updates
- **Immutable field protection**: node_id, addresses, etc. cannot change
- **Validation before applying**: Prevents invalid configs from being loaded
- **Listener pattern**: Components can react to configuration changes
- **Debouncing**: Handles rapid successive changes intelligently

**Example: Automatic Reload with File Watching**
```go
// Enable automatic file watching
if err := configManager.StartWatching(); err != nil {
    log.Fatal(err)
}
defer configManager.StopWatching()

// Now just edit config.yaml - changes apply automatically!
```

**Example: Manual Reload via API**
```bash
# Get current configuration
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config

# Manually trigger reload
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/reload
```

For complete documentation on configuration reloading, including automatic file watching, examples, best practices, and troubleshooting, see [CONFIG_RELOAD.md](CONFIG_RELOAD.md).

## Dynamic Peer Management

### Overview

Helios now supports dynamic addition and removal of peers without requiring cluster restart. This feature allows you to scale your cluster up or down, replace failed nodes, or perform maintenance operations seamlessly.

### Using the CLI

The `helios-cli` tool provides commands for managing cluster peers:

**Add a peer:**
```bash
./bin/helios-cli --host=localhost --port=6379 addpeer node-4 127.0.0.1:7003
```

**Remove a peer:**
```bash
./bin/helios-cli --host=localhost --port=6379 removepeer node-4
```

**List all peers:**
```bash
./bin/helios-cli --host=localhost --port=6379 listpeers
```

### Using the HTTP API

You can also manage peers via the HTTP API endpoints (requires admin privileges):

**List peers:**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/cluster/peers
```

**Add a peer:**
```bash
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"node-4","address":"127.0.0.1:7003"}' \
  http://localhost:8443/admin/cluster/peers
```

**Remove a peer:**
```bash
curl -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"node-4"}' \
  http://localhost:8443/admin/cluster/peers
```

**Get comprehensive cluster status:**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/cluster/status
```

**Example cluster status response:**
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

**Cluster Status Fields:**
- **node**: Current node information (ID, state, current term)
- **leader**: Leader information (ID, address, whether this node is leader)
- **indices**: Log replication indices
  - `last_log`: Index of the last log entry
  - `last_log_term`: Term of the last log entry
  - `commit_index`: Highest log entry known to be committed
  - `applied_index`: Highest log entry applied to state machine
- **peers**: List of peer nodes with replication status (only shown if this node is leader)
  - `match_index`: Highest log entry known to be replicated on peer
  - `next_index`: Index of next log entry to send to peer
- **sessions**: Read-your-writes consistency session information
  - `active_count`: Number of active client sessions
  - `session_ids`: List of session IDs (only included if count ≤ 10)
- **tls**: TLS configuration status
- **uptime**: Duration since node started

### Best Practices

1. **Always add/remove peers through the leader**: Peer management operations must be performed on the current leader node. If you try to add/remove on a follower, you'll receive a redirect response with the current leader's address.

2. **Add nodes one at a time**: When scaling up, add peers sequentially to ensure each new node has time to catch up with the leader before adding the next one.

3. **Maintain quorum**: Never remove so many peers that the cluster loses quorum (majority). For a 5-node cluster, you must always have at least 3 nodes online.

4. **Start new nodes before adding**: When adding a peer, ensure the new ATLAS node is already running and listening on the specified address before executing the add command.

5. **Monitor replication**: After adding a peer, monitor its replication lag to ensure it's catching up properly. Check the cluster status endpoint regularly.

### Example: Scaling from 3 to 5 Nodes

**Step 1:** Start two new ATLAS nodes:
```bash
# Terminal 4
./bin/helios-atlasd \
  -data-dir=./data/node4 \
  -listen=:6382 \
  -raft=true \
  -raft-node-id=node-4 \
  -raft-addr=127.0.0.1:7003 \
  -raft-data-dir=./data/node4/raft

# Terminal 5
./bin/helios-atlasd \
  -data-dir=./data/node5 \
  -listen=:6383 \
  -raft=true \
  -raft-node-id=node-5 \
  -raft-addr=127.0.0.1:7004 \
  -raft-data-dir=./data/node5/raft
```

**Step 2:** Add them to the cluster (connect to current leader):
```bash
# Add node-4
./bin/helios-cli --host=localhost --port=6379 addpeer node-4 127.0.0.1:7003

# Wait for node-4 to catch up, then add node-5
./bin/helios-cli --host=localhost --port=6379 addpeer node-5 127.0.0.1:7004
```

**Step 3:** Verify cluster status:
```bash
./bin/helios-cli --host=localhost --port=6379 listpeers
```

### Example: Replacing a Failed Node

**Step 1:** Remove the failed node:
```bash
./bin/helios-cli --host=localhost --port=6379 removepeer node-3
```

**Step 2:** Start a replacement node:
```bash
./bin/helios-atlasd \
  -data-dir=./data/node3-new \
  -listen=:6381 \
  -raft=true \
  -raft-node-id=node-3-new \
  -raft-addr=127.0.0.1:7002 \
  -raft-data-dir=./data/node3-new/raft
```

**Step 3:** Add the replacement:
```bash
./bin/helios-cli --host=localhost --port=6379 addpeer node-3-new 127.0.0.1:7002
```

### Troubleshooting

**"Not the leader" error:**
- This means you're trying to add/remove peers on a follower node
- Connect to the current leader instead (check cluster status or logs)

**"Peer already exists" error:**
- The peer ID is already in the cluster
- Use a different ID or remove the old peer first

**"Peer not found" error:**
- The peer ID doesn't exist in the cluster
- Check the peer list with `listpeers` command

**Connection timeouts:**
- Ensure the new node is running and accessible
- Check firewall rules and network connectivity
- Verify the Raft address is correct

## TLS Configuration for Raft Transport

### Overview

Helios supports TLS encryption for Raft cluster communication, providing secure peer-to-peer communication with mutual authentication. This ensures that all Raft messages (leader elections, log replication, snapshots) are encrypted and authenticated.

### Features

- **Mutual TLS Authentication**: Both client and server certificates are verified
- **ECDSA P-256 Keys**: Efficient elliptic curve cryptography
- **Certificate Generation Tool**: Automated certificate creation for clusters
- **Flexible Configuration**: Support for custom CA, per-node certificates, and optional verification

### Generating Certificates

Use the `helios-gencerts` tool to generate certificates for your cluster:

```bash
# Generate certificates for a 3-node cluster
./bin/helios-gencerts --output=./certs --nodes=node-1,node-2,node-3

# Generate with additional hostnames/IPs
./bin/helios-gencerts \
  --output=./certs \
  --nodes=node-1,node-2,node-3 \
  --hosts=example.com,192.168.1.100
```

This creates:
- `ca.crt` - Certificate Authority certificate (distribute to all nodes)
- `ca.key` - CA private key (keep secure, only needed for generating new certificates)
- `node-X.crt` - Node certificate for each node
- `node-X.key` - Private key for each node

### Configuration

#### Command-Line Flags

```bash
./bin/helios-atlasd \
  -raft=true \
  -raft-node-id=node-1 \
  -raft-addr=127.0.0.1:7000 \
  -raft-tls-enabled=true \
  -raft-tls-cert=./certs/node-1.crt \
  -raft-tls-key=./certs/node-1.key \
  -raft-tls-ca=./certs/ca.crt \
  -raft-tls-verify-peer=true
```

#### YAML Configuration

```yaml
raft:
  enabled: true
  node_id: "node-1"
  address: "127.0.0.1:7000"
  data_dir: "./data/node1/raft"

  # TLS Configuration
  tls:
    enabled: true
    cert_file: "./certs/node-1.crt"
    key_file: "./certs/node-1.key"
    ca_file: "./certs/ca.crt"
    verify_peer: true
    server_name: ""  # Optional: override server name verification
    insecure_skip_verify: false  # DO NOT use in production
```

### Deploying a TLS-Enabled Cluster

**Step 1:** Generate certificates
```bash
./bin/helios-gencerts --output=./certs --nodes=node-1,node-2,node-3
```

**Step 2:** Distribute certificates
```bash
# Copy to each node (example for remote deployment)
scp certs/ca.crt node1:/etc/helios/certs/
scp certs/node-1.crt certs/node-1.key node1:/etc/helios/certs/

scp certs/ca.crt node2:/etc/helios/certs/
scp certs/node-2.crt certs/node-2.key node2:/etc/helios/certs/

scp certs/ca.crt node3:/etc/helios/certs/
scp certs/node-3.crt certs/node-3.key node3:/etc/helios/certs/
```

**Step 3:** Start nodes with TLS

**Terminal 1 - Node 1:**
```bash
./bin/helios-atlasd \
  -data-dir=./data/node1 \
  -listen=:6379 \
  -raft=true \
  -raft-node-id=node-1 \
  -raft-addr=127.0.0.1:7000 \
  -raft-data-dir=./data/node1/raft \
  -raft-tls-enabled=true \
  -raft-tls-cert=./certs/node-1.crt \
  -raft-tls-key=./certs/node-1.key \
  -raft-tls-ca=./certs/ca.crt \
  -raft-tls-verify-peer=true
```

**Terminal 2 - Node 2:**
```bash
./bin/helios-atlasd \
  -data-dir=./data/node2 \
  -listen=:6380 \
  -raft=true \
  -raft-node-id=node-2 \
  -raft-addr=127.0.0.1:7001 \
  -raft-data-dir=./data/node2/raft \
  -raft-tls-enabled=true \
  -raft-tls-cert=./certs/node-2.crt \
  -raft-tls-key=./certs/node-2.key \
  -raft-tls-ca=./certs/ca.crt \
  -raft-tls-verify-peer=true
```

**Terminal 3 - Node 3:**
```bash
./bin/helios-atlasd \
  -data-dir=./data/node3 \
  -listen=:6381 \
  -raft=true \
  -raft-node-id=node-3 \
  -raft-addr=127.0.0.1:7002 \
  -raft-data-dir=./data/node3/raft \
  -raft-tls-enabled=true \
  -raft-tls-cert=./certs/node-3.crt \
  -raft-tls-key=./certs/node-3.key \
  -raft-tls-ca=./certs/ca.crt \
  -raft-tls-verify-peer=true
```

### Security Best Practices

1. **Keep CA Key Secure**: Store `ca.key` in a secure location, preferably offline after generating node certificates
2. **Use Strong File Permissions**: Set key files to 0600 (owner read/write only)
3. **Enable Peer Verification**: Always use `verify_peer: true` in production
4. **Certificate Rotation**: Regenerate certificates before expiration (default 1 year)
5. **Network Isolation**: Even with TLS, restrict Raft ports with firewalls
6. **Never Use InsecureSkipVerify**: Only for testing; disables certificate validation

### Advanced Certificate Management

#### Generating New Node Certificates

To add a new node to an existing cluster with the same CA:

```bash
# Generate only the new node certificate
go run ./cmd/helios-gencerts \
  --ca-cert=./certs/ca.crt \
  --ca-key=./certs/ca.key \
  --output=./certs \
  --nodes=node-4
```

#### Certificate Expiration

Certificates generated by `helios-gencerts` are valid for 1 year by default. To check expiration:

```bash
openssl x509 -in certs/node-1.crt -noout -dates
```

#### Rotating Certificates

To rotate certificates without downtime:

1. Generate new certificates with the same CA
2. Deploy new certificates to nodes (but keep old ones)
3. Update configuration to use new certificates
4. Perform rolling restart of each node
5. Remove old certificates after all nodes are updated

### TLS Troubleshooting

**"x509: certificate signed by unknown authority"**
- Ensure all nodes have the correct `ca.crt` file
- Verify the CA file path is correct in configuration

**"x509: certificate is not valid for any names"**
- Certificate DNS names/IPs don't match the connection address
- Generate new certificates with correct `--hosts` parameter
- Or use `server_name` config to override verification

**"tls: bad certificate"**
- Node certificate or key file is incorrect or corrupted
- Ensure the certificate and key match (node-1.crt goes with node-1.key)
- Check file permissions (must be readable by Helios process)

**Connection timeouts with TLS enabled**
- Verify all nodes have TLS enabled consistently
- Check that all nodes can reach each other on Raft ports
- Review firewall rules for TLS connections

**"remote error: tls: bad certificate" in logs**
- Peer verification is failing
- Ensure the connecting node's certificate is signed by the correct CA
- Verify all nodes use certificates from the same CA

### Performance Considerations

TLS adds minimal overhead to Raft communication:
- **Latency**: ~1-2ms additional latency for handshake (cached for persistent connections)
- **Throughput**: ECDSA P-256 is optimized for performance
- **CPU**: <5% additional CPU usage for encryption/decryption

For high-throughput clusters, ensure:
- Use persistent connections (automatic in NetworkTransport)
- Enable CPU hardware acceleration for crypto (automatic on modern CPUs)
- Monitor TLS handshake failures and connection reuse metrics

## Read-Your-Writes Consistency

### Overview

Helios implements read-your-writes consistency to ensure that after a client writes data, any subsequent reads from that client will see the write or newer data. This is critical in distributed systems where reads might be served by followers that haven't caught up with the leader yet.

### How It Works

The implementation uses session-based tracking:

1. **Session Management**: Each client session is assigned a unique ID and tracks the log index of its last write
2. **Write Tracking**: After each write operation, the session's last applied index is updated
3. **Read Consistency**: Before serving reads, the system waits until the local commit index reaches the session's minimum required index (with a 5-second timeout)
4. **Automatic Cleanup**: Sessions are automatically cleaned up after 10 minutes of inactivity

### Using Session-Based Consistency

#### With RaftAtlas (Recommended)

The `RaftAtlas` integration provides automatic session tracking:

```go
sessionID := "client-session-123"

// Write with session tracking
err := raftAtlas.SetWithSession("key", []byte("value"), 0, sessionID)
if err != nil {
    return err
}

// Read with consistency guarantee - waits until replication catches up
value, err := raftAtlas.GetConsistent("key", sessionID)
if err != nil {
    return err
}
// value will always reflect the previous write or newer data
```

#### With Raw Raft API

For custom applications using the Raft layer directly:

```go
sessionID := "client-session-123"

// Apply a write
index, term, err := raft.Apply(command, 5*time.Second)
if err != nil {
    return err
}

// Track the write in the session
raft.UpdateSessionAfterWrite(sessionID, index)

// Later, ensure consistency before reading
minIndex := raft.GetSessionIndex(sessionID)
err = raft.ReadConsistent(sessionID, minIndex)
if err != nil {
    // Handle timeout or error - can proceed with stale read if acceptable
    log.Printf("Consistency check failed: %v", err)
}

// Perform the read operation
result := performRead()
```

### Protocol Integration

The ATLAS protocol supports session tracking via the `SessionID` field:

```go
// Create command with session
cmd := protocol.NewSetCommand("key", []byte("value"), 0).WithSession(sessionID)

// Serialize and send
data, _ := json.Marshal(cmd)
```

### Configuration

Session management parameters:

- **Session Timeout**: 10 minutes (configurable in `SessionManager`)
- **Read Timeout**: 5 seconds (maximum wait for replication in `ReadConsistent`)
- **Cleanup Interval**: 5 minutes (automatic stale session cleanup)

### Best Practices

1. **Use Stable Session IDs**: Generate UUIDs or use client connection IDs
2. **Handle Timeouts Gracefully**: If `ReadConsistent` times out, decide whether to:
   - Return an error to the client
   - Proceed with a potentially stale read (for availability)
   - Retry the consistency check

3. **Monitor Session Count**: Track active sessions via `GetSessionManager().SessionCount()`
4. **Session Lifetime**: Sessions are per-client, maintain them across multiple operations
5. **Follower Reads**: Reads from followers will wait for replication; leader reads are immediate if already committed

### Example: Web Application

```go
type Handler struct {
    atlas *atlas.RaftAtlas
}

func (h *Handler) HandleWrite(w http.ResponseWriter, r *http.Request) {
    // Use session cookie or auth token as session ID
    sessionID := r.Header.Get("X-Session-ID")

    key := r.FormValue("key")
    value := []byte(r.FormValue("value"))

    err := h.atlas.SetWithSession(key, value, 0, sessionID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.WriteHeader(http.StatusOK)
}

func (h *Handler) HandleRead(w http.ResponseWriter, r *http.Request) {
    sessionID := r.Header.Get("X-Session-ID")
    key := r.FormValue("key")

    // Guaranteed to see previous writes from this session
    value, err := h.atlas.GetConsistent(key, sessionID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.Write(value)
}
```

### Performance Impact

Read-your-writes consistency adds minimal overhead:

- **Leader Reads**: Immediate if commitIndex >= required index (most common case)
- **Follower Reads**: May wait up to 5 seconds for replication
- **Memory**: ~100 bytes per active session
- **Cleanup**: Background goroutine runs every 5 minutes

### Testing

The implementation includes comprehensive tests:

```bash
# Run session management tests
go test ./internal/raft/... -run TestSession

# Run integration tests
go test ./test/raft/... -run TestReadYourWrites
```

### Troubleshooting

**"Consistency timeout" errors:**
- Replication may be slow or a follower is lagging
- Check network latency and follower health
- Consider increasing read timeout if acceptable

**Memory usage growing:**
- Check for session leaks (sessions not being cleaned up)
- Monitor `SessionCount()` - should be roughly equal to active clients
- Verify automatic cleanup is running (check logs)

**Stale reads despite using sessions:**
- Ensure sessionID is consistent across requests
- Verify `UpdateSessionAfterWrite` is called after each write
- Check that `ReadConsistent` is called before reads

