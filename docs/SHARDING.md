# Horizontal Sharding Guide

## Overview

Helios now includes **horizontal sharding** capabilities for distributing data across multiple nodes to handle large datasets that don't fit on a single machine. This guide explains how to set up, configure, and manage a sharded Helios cluster.

## Features

- **Consistent Hashing**: Automatic, balanced distribution of keys across nodes
- **Virtual Nodes**: Enhanced distribution uniformity (configurable 150 virtual nodes per physical node)
- **Automatic Rebalancing**: Optional automatic rebalancing when nodes are added/removed
- **Migration Support**: Controlled data migration between nodes with rate limiting
- **Replication**: Configurable replication factor for fault tolerance
- **Dynamic Topology**: Add/remove nodes without cluster restart
- **Monitoring**: Comprehensive statistics and health monitoring

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Helios Sharded Cluster                   │
│                                                              │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │  Shard 1    │  │  Shard 2    │  │  Shard 3    │        │
│  │  (keys A-G) │  │  (keys H-N) │  │  (keys O-Z) │        │
│  │             │  │             │  │             │        │
│  │ ┌─────────┐ │  │ ┌─────────┐ │  │ ┌─────────┐ │        │
│  │ │  ATLAS  │ │  │ │  ATLAS  │ │  │ │  ATLAS  │ │        │
│  │ │  Store  │ │  │ │  Store  │ │  │ │  Store  │ │        │
│  │ └─────────┘ │  │ └─────────┘ │  │ └─────────┘ │        │
│  └─────────────┘  └─────────────┘  └─────────────┘        │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐  │
│  │         Consistent Hash Ring (Virtual Nodes)          │  │
│  │  Automatic routing to correct shard based on key     │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

## Configuration

### Basic Configuration

Add the following to your `configs/config.yaml`:

```yaml
sharding:
  # Enable horizontal sharding
  enabled: true

  # Number of virtual nodes per physical node (higher = better distribution)
  virtual_nodes: 150

  # Number of replicas for each key (1 = no replication)
  replication_factor: 3

  # Migration rate in keys per second
  migration_rate: 1000

  # Enable automatic rebalancing
  auto_rebalance: true

  # Rebalancing check interval
  rebalance_interval: "1h"
```

### Default Values

If not specified, the following defaults are used:

```yaml
sharding:
  enabled: false
  virtual_nodes: 150
  replication_factor: 1
  migration_rate: 1000
  auto_rebalance: false
  rebalance_interval: "1h"
```

## Cluster Setup

### 1. Start Initial Nodes

Start multiple Helios nodes with sharding enabled:

**Node 1:**
```bash
./bin/helios-atlasd \
  --node-id=shard-1 \
  --data-dir=/var/lib/helios/shard1 \
  --listen=:6379 \
  --config=/etc/helios/config.yaml
```

**Node 2:**
```bash
./bin/helios-atlasd \
  --node-id=shard-2 \
  --data-dir=/var/lib/helios/shard2 \
  --listen=:6380 \
  --config=/etc/helios/config.yaml
```

**Node 3:**
```bash
./bin/helios-atlasd \
  --node-id=shard-3 \
  --data-dir=/var/lib/helios/shard3 \
  --listen=:6381 \
  --config=/etc/helios/config.yaml
```

### 2. Register Nodes

Use the CLI to add nodes to the cluster:

```bash
# Add nodes to the cluster
./bin/helios-cli addnode shard-1 localhost:6379
./bin/helios-cli addnode shard-2 localhost:6380
./bin/helios-cli addnode shard-3 localhost:6381

# Verify nodes are registered
./bin/helios-cli listnodes
```

### 3. Verify Setup

Check cluster statistics:

```bash
./bin/helios-cli clusterstats
```

Expected output:
```json
{
  "total_nodes": 3,
  "online_nodes": 3,
  "total_keys": 0,
  "sharding_enabled": true,
  "replication_factor": 3,
  "active_migrations": 0
}
```

## API Usage

### REST API Endpoints

#### Add Node
```bash
curl -X POST http://localhost:8443/api/v1/shards/nodes \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "node_id": "shard-4",
    "address": "localhost:6382",
    "raft_enabled": true
  }'
```

#### Remove Node
```bash
curl -X DELETE http://localhost:8443/api/v1/shards/nodes \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"node_id": "shard-4"}'
```

#### List Nodes
```bash
curl -X GET http://localhost:8443/api/v1/shards/nodes \
  -H "Authorization: Bearer <token>"
```

#### Find Node for Key
```bash
curl -X GET "http://localhost:8443/api/v1/shards/node?key=user:12345" \
  -H "Authorization: Bearer <token>"
```

Response:
```json
{
  "key": "user:12345",
  "node_id": "shard-2",
  "address": "localhost:6380"
}
```

#### Get Cluster Statistics
```bash
curl -X GET http://localhost:8443/api/v1/shards/stats \
  -H "Authorization: Bearer <token>"
```

#### Initiate Migration
```bash
curl -X POST http://localhost:8443/api/v1/shards/migrate \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "source_node": "shard-1",
    "target_node": "shard-4",
    "key_pattern": "user:*"
  }'
```

#### Check Migration Status
```bash
curl -X GET "http://localhost:8443/api/v1/shards/migration?task_id=mig-1234567890" \
  -H "Authorization: Bearer <token>"
```

Response:
```json
{
  "id": "mig-1234567890",
  "source_node": "shard-1",
  "target_node": "shard-4",
  "key_pattern": "user:*",
  "status": "in_progress",
  "keys_moved": 5000,
  "total_keys": 10000,
  "start_time": "2026-02-04T10:00:00Z",
  "end_time": null
}
```

### CLI Commands

#### Node Management
```bash
# Add a node
./bin/helios-cli addnode shard-4 localhost:6382

# Remove a node
./bin/helios-cli removenode shard-4

# List all nodes
./bin/helios-cli listnodes
```

#### Key Routing
```bash
# Find which node handles a specific key
./bin/helios-cli nodeforkey user:12345
```

#### Data Migration
```bash
# Migrate all keys from shard-1 to shard-4
./bin/helios-cli migrate shard-1 shard-4

# Migrate only keys matching pattern
./bin/helios-cli migrate shard-1 shard-4 "user:*"
```

#### Monitoring
```bash
# Display cluster statistics
./bin/helios-cli clusterstats
```

## Data Operations

### Automatic Routing

All standard operations automatically route to the correct shard:

```bash
# Set operations route to the appropriate shard
./bin/helios-cli set user:alice "Alice Data"
./bin/helios-cli set user:bob "Bob Data"

# Get operations automatically find the correct shard
./bin/helios-cli get user:alice
./bin/helios-cli get user:bob

# Delete operations route correctly
./bin/helios-cli del user:alice
```

The client doesn't need to know which shard contains the data—the system handles routing automatically using consistent hashing.

## Scaling Operations

### Adding a New Node

When you add a new node to the cluster:

1. **Start the new node:**
   ```bash
   ./bin/helios-atlasd \
     --node-id=shard-4 \
     --data-dir=/var/lib/helios/shard4 \
     --listen=:6382
   ```

2. **Register the node:**
   ```bash
   ./bin/helios-cli addnode shard-4 localhost:6382
   ```

3. **Trigger rebalancing (if auto-rebalance is disabled):**
   ```bash
   # Manually migrate keys
   ./bin/helios-cli migrate shard-1 shard-4
   ```

4. **Monitor migration progress:**
   ```bash
   ./bin/helios-cli clusterstats
   ```

### Removing a Node

When removing a node from the cluster:

1. **Migrate data away from the node:**
   ```bash
   # Migrate data to remaining nodes
   ./bin/helios-cli migrate shard-4 shard-1
   ./bin/helios-cli migrate shard-4 shard-2
   ```

2. **Wait for migration to complete:**
   ```bash
   # Check migration status
   ./bin/helios-cli clusterstats
   ```

3. **Remove the node:**
   ```bash
   ./bin/helios-cli removenode shard-4
   ```

4. **Stop the node process:**
   ```bash
   # Stop the helios-atlasd process for shard-4
   ```

## Rebalancing

### Automatic Rebalancing

When enabled, the cluster automatically rebalances when:
- A new node is added
- A node is removed
- Key distribution becomes uneven (>30% variance)

The rebalancing algorithm:
- Monitors node loads continuously
- Identifies overloaded and underloaded nodes
- Creates migration tasks to balance the load
- Prevents duplicate migrations
- Limits concurrent migrations to 5 (prevents cluster overload)

For detailed information, see [REBALANCING_IMPLEMENTATION.md](REBALANCING_IMPLEMENTATION.md).

Configure in `config.yaml`:
```yaml
sharding:
  auto_rebalance: true
  rebalance_interval: "1h"  # Check for rebalancing every hour
```

#### Rebalancing API Endpoints

**Trigger Manual Rebalancing:**
```bash
curl -X POST http://localhost:8443/api/v1/shards/rebalance \
  -H "Authorization: Bearer <token>"
```

**Get Active Migrations:**
```bash
curl -X GET http://localhost:8443/api/v1/shards/migrations/active \
  -H "Authorization: Bearer <token>"
```

**Get All Migrations:**
```bash
curl -X GET http://localhost:8443/api/v1/shards/migrations \
  -H "Authorization: Bearer <token>"
```

**Cancel Migration:**
```bash
curl -X POST http://localhost:8443/api/v1/shards/migrations/{taskID}/cancel \
  -H "Authorization: Bearer <token>"
```

**Cleanup Old Migrations:**
```bash
curl -X POST http://localhost:8443/api/v1/shards/migrations/cleanup \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"older_than_hours": 24}'
```

### Manual Rebalancing

For controlled rebalancing:

1. **Disable automatic rebalancing:**
   ```yaml
   sharding:
     auto_rebalance: false
   ```

2. **Plan migrations:**
   ```bash
   # Check current distribution
   ./bin/helios-cli CLUSTERSTATS

   # View active migrations
   ./bin/helios-cli ACTIVEMIGRATIONS
   ```

3. **Trigger manual rebalance:**
   ```bash
   ./bin/helios-cli REBALANCE
   ```

4. **Monitor migrations:**
   ```bash
   # View all migrations
   ./bin/helios-cli ALLMIGRATIONS

   # Cancel stuck migration if needed
   ./bin/helios-cli CANCELMIGRATION mig-1770220797664391700-1
   ```

## Monitoring

### Key Metrics

Monitor these metrics for cluster health:

- **Total Nodes**: Number of nodes in the cluster
- **Online Nodes**: Number of responsive nodes
- **Total Keys**: Total keys across all shards
- **Keys per Node**: Distribution of keys across nodes
- **Active Migrations**: Number of ongoing migrations

### Health Checks

Nodes are automatically marked offline if they:
- Don't respond to health checks (30-second timeout)
- Fail to update statistics

### Distribution Analysis

Check if keys are evenly distributed:

```bash
./bin/helios-cli listnodes
```

Look for:
- Similar key counts across nodes
- All nodes in "online" status
- No nodes with 0 keys (unless newly added)

## Performance Tuning

### Virtual Nodes

More virtual nodes = better distribution but higher overhead:

```yaml
sharding:
  virtual_nodes: 150  # Default, good balance
  # virtual_nodes: 300  # Better distribution, more memory
  # virtual_nodes: 50   # Lower overhead, less uniform
```

### Migration Rate

Control migration speed to avoid overwhelming the cluster:

```yaml
sharding:
  migration_rate: 1000  # Keys per second
  # migration_rate: 5000  # Faster migration
  # migration_rate: 100   # Slower, less impact
```

### Replication Factor

Balance durability vs. storage:

```yaml
sharding:
  replication_factor: 3  # Each key stored on 3 nodes
  # replication_factor: 1  # No replication, maximum capacity
  # replication_factor: 5  # High durability, more storage
```

## Best Practices

### 1. Plan Capacity

- Start with 3-5 nodes for small clusters
- Add nodes when average load exceeds 70%
- Keep replication factor at 3 for production

### 2. Monitor Distribution

- Check cluster stats regularly
- Investigate nodes with significantly more/fewer keys
- Enable auto-rebalancing for dynamic workloads

### 3. Migration Strategy

- Schedule migrations during off-peak hours
- Migrate in small batches (adjust migration_rate)
- Monitor migration progress before adding/removing more nodes

### 4. Backup Strategy

- Snapshot each shard independently
- Test restore procedures for individual shards
- Document node-to-shard mappings

### 5. Key Design

- Use prefixes for related keys (e.g., `user:*`, `session:*`)
- Avoid sequential keys (they'll all hash to the same shard)
- Consider consistent naming for better locality

## Troubleshooting

### Uneven Distribution

**Symptom:** One node has many more keys than others

**Solutions:**
1. Increase virtual nodes: `virtual_nodes: 300`
2. Manually migrate keys from overloaded node
3. Enable auto-rebalancing

### Migration Failures

**Symptom:** Migration tasks stuck in "in_progress"

**Solutions:**
1. Check node connectivity
2. Verify source and target nodes are online
3. Restart migration with lower rate

### Node Offline

**Symptom:** Node shows as offline but is running

**Solutions:**
1. Check network connectivity
2. Verify node can communicate with cluster
3. Check firewall rules

### Performance Degradation

**Symptom:** Slow operations after adding sharding

**Solutions:**
1. Reduce replication factor
2. Optimize migration rate
3. Add more nodes to distribute load

## Combining with Raft

Sharding works alongside Raft consensus:

- **Raft**: Replicates data within a shard for durability
- **Sharding**: Distributes data across shards for capacity

Example configuration:
```yaml
# Enable both Raft and sharding
immutable:
  raft_enabled: true

sharding:
  enabled: true
  replication_factor: 1  # Raft handles replication
```

Each shard can be a Raft cluster:
- Shard 1: 3-node Raft cluster (shard-1a, shard-1b, shard-1c)
- Shard 2: 3-node Raft cluster (shard-2a, shard-2b, shard-2c)
- Shard 3: 3-node Raft cluster (shard-3a, shard-3b, shard-3c)

This provides both horizontal scaling (sharding) and fault tolerance (Raft).

## API Reference

### HTTP Endpoints

| Endpoint                   | Method | Description            |
| -------------------------- | ------ | ---------------------- |
| `/api/v1/shards/nodes`     | GET    | List all nodes         |
| `/api/v1/shards/nodes`     | POST   | Add a node             |
| `/api/v1/shards/nodes`     | DELETE | Remove a node          |
| `/api/v1/shards/node`      | GET    | Find node for key      |
| `/api/v1/shards/stats`     | GET    | Get cluster stats      |
| `/api/v1/shards/migrate`   | POST   | Start migration        |
| `/api/v1/shards/migration` | GET    | Check migration status |

### CLI Commands

| Command                         | Description             |
| ------------------------------- | ----------------------- |
| `addnode <id> <address>`        | Add a shard node        |
| `removenode <id>`               | Remove a shard node     |
| `listnodes`                     | List all shard nodes    |
| `nodeforkey <key>`              | Find node for key       |
| `migrate <src> <dst> [pattern]` | Migrate keys            |
| `clusterstats`                  | Show cluster statistics |

## FAQ

**Q: Does sharding work with single-node deployments?**
A: Yes, but it's unnecessary. Keep `sharding.enabled: false` for single-node setups.

**Q: Can I change the number of virtual nodes after startup?**
A: No, this requires cluster rebuild. Plan carefully.

**Q: How much overhead does sharding add?**
A: Minimal—routing adds ~0.1ms latency. Memory overhead is ~100KB per node.

**Q: Can I mix sharded and non-sharded nodes?**
A: No, either all nodes use sharding or none do.

**Q: What happens if a shard goes offline?**
A: Keys on that shard become unavailable. Use replication_factor > 1 for redundancy.

## Next Steps

- Read [CLUSTER_SETUP.md](CLUSTER_SETUP.md) for Raft cluster configuration
- See [RAFT_IMPLEMENTATION.md](RAFT_IMPLEMENTATION.md) for consensus details
- Check [architecture.md](architecture.md) for overall system design
