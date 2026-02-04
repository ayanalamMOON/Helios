# Horizontal Sharding Example

This example demonstrates how to use horizontal sharding in Helios for distributing data across multiple nodes.

## Overview

Horizontal sharding allows you to scale beyond a single machine's capacity by distributing data across multiple nodes based on key hashing.

## Features Demonstrated

1. **Basic Sharding Setup**: Creating a sharded cluster
2. **Dynamic Topology**: Adding/removing nodes
3. **Data Migration**: Moving data between shards
4. **Monitoring**: Tracking cluster statistics
5. **Combining with Raft**: Sharding + consensus for maximum fault tolerance

## Running the Example

```bash
# Build the example
go build -o sharding-example main.go

# Run the example
./sharding-example
```

## What It Does

The example shows:
- How to configure sharding
- Adding nodes to a cluster
- Storing and retrieving sharded data
- Checking which node handles which keys
- Creating migration tasks
- Monitoring cluster health and statistics

## Key Concepts

### Consistent Hashing
Keys are distributed using consistent hashing with virtual nodes for uniform distribution.

### Replication Factor
Each key can be replicated to multiple nodes for fault tolerance (configurable).

### Auto-Rebalancing
The cluster can automatically rebalance when nodes are added or removed.

## Real-World Usage

In production, you would:
1. Run multiple Helios nodes across different machines
2. Configure proper network addresses instead of localhost
3. Implement proper forwarding logic for remote nodes
4. Set up monitoring and alerting
5. Enable TLS for secure communication

## Related Documentation

- [SHARDING.md](../../docs/SHARDING.md) - Complete sharding guide
- [CLUSTER_SETUP.md](../../docs/CLUSTER_SETUP.md) - Multi-node setup
- [README.md](../../README.md) - Project overview
