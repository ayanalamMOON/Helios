# Rebalancing Implementation Guide

This document describes the automatic rebalancing implementation for the Helios sharding system.

## Overview

The rebalancing system automatically redistributes data across nodes when load imbalances are detected. It uses a sophisticated algorithm that:
- Monitors node loads continuously
- Identifies overloaded and underloaded nodes
- Creates migration tasks to balance the load
- Prevents duplicate migrations
- Limits concurrent migrations to avoid cluster overload

## Key Components

### 1. Automatic Rebalancing

The `checkRebalance()` method runs periodically (configurable via `rebalance_interval`) and:

1. **Filters Online Nodes**: Only considers nodes with "online" status
2. **Calculates Statistics**:
   - Average keys per node
   - Standard deviation of key distribution
   - Threshold for rebalancing (30% of average)

3. **Identifies Imbalanced Nodes**:
   - **Overloaded**: Nodes with keys > (average + threshold)
   - **Underloaded**: Nodes with keys < (average - threshold)

4. **Triggers Rebalancing**: If imbalance detected, calls `executeRebalancing()`

### 2. Rebalancing Execution

The `executeRebalancing()` method implements the core rebalancing logic:

```go
func (sm *ShardManager) executeRebalancing(overloaded, underloaded []*NodeInfo, avgKeys int64)
```

**Algorithm**:

1. **Sort Nodes**:
   - Overloaded nodes: Descending by key count (most loaded first)
   - Underloaded nodes: Ascending by key count (least loaded first)

2. **Pair Nodes**: Match overloaded with underloaded nodes

3. **Calculate Migration Size**: For each pair:
   ```go
   keysToMove = (overloadedKeys - avgKeys) / 2
   ```

4. **Check Significance**: Only migrate if difference is significant (> 10% of average)

5. **Prevent Duplicates**: Skip if active migration already exists between the pair

6. **Create Migration Task**: Generate migration with calculated key count

7. **Concurrency Limit**: Stop after 5 concurrent migrations

### 3. Migration Lifecycle Management

#### GetActiveMigrations
Returns migrations with status "pending" or "in-progress":
```go
func (sm *ShardManager) GetActiveMigrations() []*MigrationTask
```

#### GetAllMigrations
Returns all migrations regardless of status:
```go
func (sm *ShardManager) GetAllMigrations() []*MigrationTask
```

#### CancelMigration
Cancels an active migration:
```go
func (sm *ShardManager) CancelMigration(taskID string) error
```
- Can only cancel "pending" or "in-progress" migrations
- Sets status to "cancelled"
- Records end time

#### CleanupCompletedMigrations
Removes old completed/failed migrations:
```go
func (sm *ShardManager) CleanupCompletedMigrations(olderThan time.Duration) int
```
- Removes migrations completed/failed before the specified duration
- Returns count of cleaned migrations
- Useful for housekeeping and preventing memory bloat

#### TriggerManualRebalance
Manually trigger rebalancing:
```go
func (sm *ShardManager) TriggerManualRebalance() error
```
- Performs immediate rebalancing check
- Useful for manual cluster optimization
- Returns error if rebalancing is disabled

## Configuration

```yaml
sharding:
  enabled: true
  auto_rebalance: true           # Enable automatic rebalancing
  rebalance_interval: "1h"       # Check for rebalancing every hour
  virtual_nodes: 150             # Virtual nodes per physical node
  replication_factor: 3          # Number of replicas
  migration_rate: 1000           # Keys per second migration rate
```

## API Endpoints

### Trigger Manual Rebalance
```
POST /sharding/rebalance
```
Response:
```json
{
  "status": "success",
  "message": "Rebalancing triggered successfully"
}
```

### Get Active Migrations
```
GET /sharding/migrations/active
```
Response:
```json
{
  "migrations": [
    {
      "id": "mig-1770220797664391700-1",
      "source_node": "node-1",
      "target_node": "node-2",
      "key_pattern": "*",
      "status": "in-progress",
      "keys_moved": 1500,
      "total_keys": 3000,
      "start_time": "2026-02-04T21:30:00Z"
    }
  ]
}
```

### Get All Migrations
```
GET /sharding/migrations
```
Returns all migrations including completed and failed ones.

### Cancel Migration
```
POST /sharding/migrations/:taskID/cancel
```
Response:
```json
{
  "status": "success",
  "message": "Migration cancelled successfully"
}
```

### Cleanup Old Migrations
```
POST /sharding/migrations/cleanup
```
Body:
```json
{
  "older_than_hours": 24
}
```
Response:
```json
{
  "status": "success",
  "cleaned": 5
}
```

## CLI Commands

### Trigger Rebalancing
```bash
helios-cli REBALANCE
```

### View Active Migrations
```bash
helios-cli ACTIVEMIGRATIONS
```

### View All Migrations
```bash
helios-cli ALLMIGRATIONS
```

### Cancel Migration
```bash
helios-cli CANCELMIGRATION mig-1770220797664391700-1
```

## Algorithm Details

### Load Calculation

The algorithm uses standard deviation to determine if rebalancing is needed:

```go
// Calculate average
avgKeys := totalKeys / int64(len(onlineNodes))

// Calculate standard deviation
variance := 0.0
for _, node := range onlineNodes {
    diff := float64(node.KeyCount) - float64(avgKeys)
    variance += diff * diff
}
stdDev := math.Sqrt(variance / float64(len(onlineNodes)))

// Threshold is 30% of average
threshold := int64(float64(avgKeys) * 0.3)
```

### Significance Threshold

To avoid unnecessary migrations, only migrate if the difference is significant:

```go
minSignificance := avgKeys / 10  // 10% of average
if keysToMove < minSignificance {
    continue  // Skip insignificant migrations
}
```

### Concurrency Control

Limit concurrent migrations to prevent cluster overload:

```go
const maxConcurrentMigrations = 5
migrationCount := 0
for ... {
    if migrationCount >= maxConcurrentMigrations {
        break
    }
    // Create migration
    migrationCount++
}
```

## Best Practices

### 1. Tuning Rebalance Interval
- **Production**: 1-4 hours (reduces cluster churn)
- **Development**: 5-15 minutes (faster testing)
- **High-traffic**: 30-60 minutes (more responsive)

### 2. Migration Rate
- Start with 1000 keys/second
- Increase for faster rebalancing (more cluster load)
- Decrease for minimal impact (slower rebalancing)

### 3. Manual Rebalancing
- Use after adding/removing nodes
- Use when auto-rebalance is disabled
- Use for immediate optimization

### 4. Monitoring
- Monitor active migrations regularly
- Clean up old migrations weekly
- Cancel stuck migrations if needed

### 5. Concurrency Limit
- Default 5 concurrent migrations is optimal for most clusters
- Increase for larger clusters (10+ nodes)
- Decrease for resource-constrained environments

## Troubleshooting

### Rebalancing Not Triggering
1. Check `auto_rebalance: true` in config
2. Verify rebalance interval is appropriate
3. Ensure load imbalance exceeds 30% threshold
4. Check all nodes are marked "online"

### Stuck Migrations
1. Check node connectivity
2. Review migration rate (may be too slow)
3. Use `CancelMigration` to cancel stuck tasks
4. Investigate source/target node logs

### Too Many Migrations
1. Increase rebalance interval
2. Check for node flapping (online/offline)
3. Review significance threshold (may need tuning)
4. Consider disabling auto-rebalance temporarily

### Memory Usage from Old Migrations
1. Enable periodic cleanup
2. Use `CleanupCompletedMigrations` API/CLI
3. Set appropriate retention period (24-72 hours)

## Performance Considerations

### CPU Usage
- Rebalancing calculations are lightweight
- Most CPU used during key migration
- Limit concurrent migrations to control CPU

### Network Usage
- Migration rate directly impacts network usage
- Use rate limiting (migration_rate) to control bandwidth
- Schedule rebalancing during off-peak hours if possible

### Storage I/O
- Key reads from source node
- Key writes to target node
- Monitor disk I/O during migrations

## Testing

The implementation includes comprehensive tests:

- `TestShardManager_AutoRebalancing`: Tests automatic rebalancing trigger
- `TestShardManager_RebalancingThreshold`: Verifies threshold logic
- `TestShardManager_TriggerManualRebalance`: Tests manual trigger
- `TestShardManager_HasActiveMigration`: Tests duplicate prevention
- `TestShardManager_GetActiveMigrations`: Tests migration filtering
- `TestShardManager_CancelMigration`: Tests cancellation
- `TestShardManager_CleanupCompletedMigrations`: Tests cleanup
- `TestShardManager_RebalancingConcurrency`: Tests concurrent operations
- `TestShardManager_ExecuteRebalancing`: Tests core algorithm

Run tests:
```bash
go test ./internal/sharding/... -v
```

## Future Enhancements

Potential improvements:

1. **Adaptive Thresholds**: Adjust threshold based on cluster size
2. **Cost Functions**: Consider network topology in migration decisions
3. **Predictive Rebalancing**: Use historical data to anticipate needs
4. **Gradual Migrations**: Split large migrations into smaller chunks
5. **Priority Queues**: Prioritize migrations based on urgency
6. **Multi-step Migrations**: Chain migrations for complex rebalancing

## References

- [SHARDING.md](SHARDING.md) - Complete sharding documentation
- [SHARDING_QUICK_REFERENCE.md](SHARDING_QUICK_REFERENCE.md) - Quick command reference
- [SHARDING_IMPLEMENTATION_SUMMARY.md](SHARDING_IMPLEMENTATION_SUMMARY.md) - Implementation details
