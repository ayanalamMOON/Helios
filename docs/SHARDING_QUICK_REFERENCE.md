# Helios Sharding Quick Reference

## Configuration

```yaml
sharding:
  enabled: true              # Enable sharding
  virtual_nodes: 150         # Virtual nodes per physical node
  replication_factor: 3      # Replicas per key
  migration_rate: 1000       # Keys/sec during migration
  auto_rebalance: true       # Auto-rebalance on topology changes
  rebalance_interval: "1h"   # Rebalancing check interval
```

## CLI Commands

```bash
# Node Management
helios-cli ADDNODE <id> <address>       # Add node
helios-cli REMOVENODE <id>              # Remove node
helios-cli LISTNODES                    # List all nodes

# Key Operations
helios-cli SET <key> <value>            # Set (auto-routed)
helios-cli GET <key>                    # Get (auto-routed)
helios-cli NODEFORKEY <key>             # Find node for key

# Migration
helios-cli MIGRATE <src> <dst> [pat]    # Migrate keys

# Rebalancing (NEW)
helios-cli REBALANCE                    # Trigger manual rebalance
helios-cli ACTIVEMIGRATIONS             # List active migrations
helios-cli ALLMIGRATIONS                # List all migrations
helios-cli CANCELMIGRATION <task_id>    # Cancel specific migration

# Monitoring
helios-cli CLUSTERSTATS                 # Cluster statistics
```

## REST API

```bash
# Add Node
POST /api/v1/shards/nodes
{"node_id":"shard-4", "address":"localhost:6382"}

# Remove Node
DELETE /api/v1/shards/nodes
{"node_id":"shard-4"}

# List Nodes
GET /api/v1/shards/nodes

# Find Node for Key
GET /api/v1/shards/node?key=user:123

# Cluster Stats
GET /api/v1/shards/stats

# Start Migration
POST /api/v1/shards/migrate
{"source_node":"shard-1", "target_node":"shard-2", "key_pattern":"*"}

# Check Migration
GET /api/v1/shards/migration?task_id=mig-123

# Rebalancing (NEW)
POST /api/v1/shards/rebalance                    # Trigger rebalance
GET /api/v1/shards/migrations/active             # Get active migrations
GET /api/v1/shards/migrations                    # Get all migrations
POST /api/v1/shards/migrations/{taskID}/cancel   # Cancel migration
POST /api/v1/shards/migrations/cleanup           # Cleanup old migrations
{"older_than_hours": 24}
```

## Common Operations

### Setup 3-Node Cluster
```bash
# Terminal 1
helios-atlasd --node-id=shard-1 --listen=:6379

# Terminal 2
helios-atlasd --node-id=shard-2 --listen=:6380

# Terminal 3
helios-atlasd --node-id=shard-3 --listen=:6381

# Register nodes
helios-cli addnode shard-1 localhost:6379
helios-cli addnode shard-2 localhost:6380
helios-cli addnode shard-3 localhost:6381
```

### Scale Out (Add Node)
```bash
# Start new node
helios-atlasd --node-id=shard-4 --listen=:6382 &

# Add to cluster
helios-cli ADDNODE shard-4 localhost:6382

# Option 1: Automatic rebalancing (if auto_rebalance: true)
# Cluster will rebalance automatically within rebalance_interval

# Option 2: Manual rebalancing
helios-cli REBALANCE

# Option 3: Manual migration
helios-cli MIGRATE shard-1 shard-4

# Monitor migrations
helios-cli ACTIVEMIGRATIONS
```

### Scale In (Remove Node)
```bash
# Migrate data away
helios-cli MIGRATE shard-4 shard-1

# Wait for completion
helios-cli CLUSTERSTATS

# Remove node
helios-cli REMOVENODE shard-4
```

### Monitor Cluster
```bash
# Overall stats
helios-cli CLUSTERSTATS

# Node list with details
helios-cli LISTNODES

# Check key distribution
for key in user:1 user:2 user:3; do
  helios-cli NODEFORKEY $key
done

# View active migrations
helios-cli ACTIVEMIGRATIONS

# View all migrations (including completed/failed)
helios-cli ALLMIGRATIONS
```

### Manage Rebalancing
```bash
# Trigger manual rebalance
helios-cli REBALANCE

# Check active migrations
helios-cli ACTIVEMIGRATIONS

# Cancel stuck migration
helios-cli CANCELMIGRATION mig-1770220797664391700-1

# Via API: Cleanup old migrations
curl -X POST http://localhost:8443/api/v1/shards/migrations/cleanup \
  -H "Content-Type: application/json" \
  -d '{"older_than_hours": 24}'
```

## Troubleshooting

### Uneven Distribution
```bash
# Check stats
helios-cli CLUSTERSTATS

# Trigger automatic rebalancing
helios-cli REBALANCE

# Or manually migrate
helios-cli MIGRATE overloaded-node underloaded-node
```

### Node Offline
```bash
# Check node status
helios-cli LISTNODES

# Remove offline node
helios-cli REMOVENODE offline-node-id
```

### Migration Stuck
```bash
# Check migration status
helios-cli ACTIVEMIGRATIONS

# Cancel stuck migration
helios-cli CANCELMIGRATION mig-1770220797664391700-1

# Or via API
curl http://localhost:8443/api/v1/shards/migration?task_id=mig-XXX

# Trigger new rebalancing if needed
helios-cli REBALANCE
```

### Too Many Active Migrations
```bash
# System limits to 5 concurrent migrations automatically

# If cleanup needed, cancel old migrations
helios-cli CANCELMIGRATION <task_id>

# Cleanup completed migrations (via API)
curl -X POST http://localhost:8443/api/v1/shards/migrations/cleanup \
  -H "Content-Type: application/json" \
  -d '{"older_than_hours": 1}'
```

## Performance Tips

- **Small clusters (3-5 nodes)**: Use defaults
- **Medium clusters (10-20 nodes)**: Increase virtual_nodes to 300
- **Large clusters (50+ nodes)**: Reduce migration_rate to 500
- **High write load**: Use aof_fsync: "interval"
- **With Raft**: Set replication_factor: 1 (Raft handles replication)

## Combining with Raft

```yaml
# Enable both for horizontal scaling + fault tolerance
immutable:
  raft_enabled: true

sharding:
  enabled: true
  replication_factor: 1  # Raft handles replication within shard
```

Architecture:
- Each shard = 3-node Raft cluster
- Sharding provides horizontal scaling
- Raft provides fault tolerance within each shard

## Metrics to Monitor

- `total_nodes` - Number of registered nodes
- `online_nodes` - Number of responsive nodes
- `total_keys` - Keys across all shards
- `active_migrations` - Ongoing migrations

Monitor per-node:
- `key_count` - Keys on each node
- `status` - online/offline/degraded
- `is_leader` - Raft leader status

## Best Practices

1. **Start with 3 nodes** minimum
2. **Enable auto-rebalance** for dynamic workloads
3. **Monitor distribution** regularly
4. **Plan migrations** during off-peak hours
5. **Use consistent key naming** for better locality
6. **Test failover** before production
7. **Document node mappings** for disaster recovery
8. **Set up alerts** for node failures
