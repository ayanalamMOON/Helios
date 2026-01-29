# Raft Consensus Implementation for Helios

A production-ready Raft consensus algorithm implementation for enabling distributed, fault-tolerant replication across multiple Helios nodes.

## Overview

This implementation provides:

- **Leader Election**: Automatic leader election with configurable timeouts
- **Log Replication**: Consistent log replication across all nodes
- **Safety**: Guarantees consistency even with network partitions
- **Snapshotting**: Automatic log compaction to manage disk usage
- **Persistence**: Durable state storage for crash recovery
- **Production-Ready**: Optimized for real-world deployment

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Raft Cluster                             │
│                                                              │
│  ┌─────────┐         ┌─────────┐         ┌─────────┐      │
│  │ Node 1  │         │ Node 2  │         │ Node 3  │      │
│  │(Leader) │◄───────►│(Follower)│◄───────►│(Follower)│     │
│  └────┬────┘         └─────────┘         └─────────┘      │
│       │                                                      │
│       │ Log Replication                                     │
│       ▼                                                      │
│  ┌─────────────┐                                           │
│  │ ATLAS Store │                                           │
│  │  (FSM)      │                                           │
│  └─────────────┘                                           │
└─────────────────────────────────────────────────────────────┘
```

## Core Components

### 1. Raft Node (`raft.go`)
Main Raft implementation managing:
- State machine (Follower/Candidate/Leader)
- Term management
- Vote handling
- Log management
- Peer communication

### 2. Log Management (`log.go`)
Replicated log with:
- Persistent storage
- Index-based retrieval
- Log compaction
- Consistency checks

### 3. Leader Election (`election.go`)
Handles:
- RequestVote RPCs
- Vote counting
- Election timeouts
- Term transitions

### 4. Persistence Layer (`persistence.go`)
Manages:
- State persistence (currentTerm, votedFor)
- Snapshot creation/restoration
- Log compaction
- Crash recovery

### 5. Transport Layer (`transport.go`)
RPC communication:
- AppendEntries
- RequestVote
- InstallSnapshot

### 6. State Machine (`fsm.go`)
Application interface:
- Command application
- Snapshot creation
- State restoration

## Usage Example

### Basic 3-Node Cluster

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/helios/helios/internal/raft"
)

func main() {
	// Configure node
	config := raft.DefaultConfig()
	config.NodeID = "node-1"
	config.ListenAddr = "127.0.0.1:9001"
	config.DataDir = "./data/node-1"

	// Create transport
	transport := raft.NewLocalTransport(config.ListenAddr)

	// Create state machine
	fsm := raft.NewMockFSM()

	// Create apply channel
	applyCh := make(chan raft.ApplyMsg, 100)

	// Create Raft node
	node, err := raft.New(config, transport, fsm, applyCh)
	if err != nil {
		panic(err)
	}

	// Add peers
	node.AddPeer("node-2", "127.0.0.1:9002")
	node.AddPeer("node-3", "127.0.0.1:9003")

	// Start node
	ctx := context.Background()
	if err := node.Start(ctx); err != nil {
		panic(err)
	}

	// Wait for leader election
	time.Sleep(500 * time.Millisecond)

	// Check if leader
	term, isLeader := node.GetState()
	fmt.Printf("Node state: term=%d, isLeader=%v\n", term, isLeader)

	// If leader, apply command
	if isLeader {
		command := []byte(`{"op":"set","key":"foo","value":"bar"}`)
		index, term, err := node.Apply(command, 5*time.Second)
		if err != nil {
			fmt.Printf("Failed to apply command: %v\n", err)
		} else {
			fmt.Printf("Command applied: index=%d, term=%d\n", index, term)
		}
	}

	// Listen for applied commands
	go func() {
		for msg := range applyCh {
			if msg.CommandValid {
				fmt.Printf("Applied command at index %d: %s\n",
					msg.CommandIndex, string(msg.Command))
			}
		}
	}()

	// Run forever
	select {}
}
```

### Integration with ATLAS

```go
package atlas

import (
	"context"
	"encoding/json"

	"github.com/helios/helios/internal/atlas/store"
	"github.com/helios/helios/internal/raft"
)

// AtlasFSM implements raft.FSM for ATLAS store
type AtlasFSM struct {
	store *store.Store
}

func NewAtlasFSM(store *store.Store) *AtlasFSM {
	return &AtlasFSM{store: store}
}

func (a *AtlasFSM) Apply(commandBytes interface{}) interface{} {
	// Parse command
	var cmd struct {
		Op    string `json:"op"`
		Key   string `json:"key"`
		Value []byte `json:"value"`
		TTL   int64  `json:"ttl"`
	}

	if err := json.Unmarshal(commandBytes.([]byte), &cmd); err != nil {
		return err
	}

	// Execute command
	switch cmd.Op {
	case "set":
		return a.store.Set(cmd.Key, cmd.Value, time.Duration(cmd.TTL))
	case "delete":
		return a.store.Delete(cmd.Key)
	default:
		return fmt.Errorf("unknown command: %s", cmd.Op)
	}
}

func (a *AtlasFSM) Snapshot() ([]byte, error) {
	// Create snapshot of ATLAS store
	return a.store.Snapshot()
}

func (a *AtlasFSM) Restore(snapshot []byte) error {
	// Restore ATLAS store from snapshot
	return a.store.Restore(snapshot)
}

// RaftAtlas wraps ATLAS with Raft consensus
type RaftAtlas struct {
	store     *store.Store
	raft      *raft.Raft
	applyCh   chan raft.ApplyMsg
	ctx       context.Context
	cancelCtx context.CancelFunc
}

func NewRaftAtlas(config *raft.Config, transport raft.Transport) (*RaftAtlas, error) {
	// Create ATLAS store
	store := store.New()

	// Create FSM
	fsm := NewAtlasFSM(store)

	// Create apply channel
	applyCh := make(chan raft.ApplyMsg, 1000)

	// Create Raft node
	node, err := raft.New(config, transport, fsm, applyCh)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	ra := &RaftAtlas{
		store:     store,
		raft:      node,
		applyCh:   applyCh,
		ctx:       ctx,
		cancelCtx: cancel,
	}

	return ra, nil
}

func (ra *RaftAtlas) Start() error {
	// Start Raft
	if err := ra.raft.Start(ra.ctx); err != nil {
		return err
	}

	// Process applied commands
	go ra.processApplied()

	return nil
}

func (ra *RaftAtlas) processApplied() {
	for {
		select {
		case <-ra.ctx.Done():
			return
		case msg := <-ra.applyCh:
			if msg.CommandValid {
				// Command already applied to FSM by Raft
				// Just log or update metrics here
			}
		}
	}
}

func (ra *RaftAtlas) Set(key string, value []byte, ttl time.Duration) error {
	// Check if leader
	_, isLeader := ra.raft.GetState()
	if !isLeader {
		return fmt.Errorf("not the leader")
	}

	// Create command
	cmd := map[string]interface{}{
		"op":    "set",
		"key":   key,
		"value": value,
		"ttl":   ttl.Milliseconds(),
	}

	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	// Apply through Raft
	_, _, err = ra.raft.Apply(cmdBytes, 5*time.Second)
	return err
}

func (ra *RaftAtlas) Get(key string) ([]byte, error) {
	// Reads can go directly to local store
	// (Raft ensures it's consistent)
	return ra.store.Get(key)
}

func (ra *RaftAtlas) Delete(key string) error {
	// Check if leader
	_, isLeader := ra.raft.GetState()
	if !isLeader {
		return fmt.Errorf("not the leader")
	}

	// Create command
	cmd := map[string]interface{}{
		"op":  "delete",
		"key": key,
	}

	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	// Apply through Raft
	_, _, err = ra.raft.Apply(cmdBytes, 5*time.Second)
	return err
}

func (ra *RaftAtlas) Shutdown() error {
	ra.cancelCtx()
	return ra.raft.Shutdown()
}
```

## Configuration

```go
config := &raft.Config{
	NodeID:              "node-1",
	ListenAddr:          "127.0.0.1:9001",
	DataDir:             "./data/raft",
	HeartbeatTimeout:    50 * time.Millisecond,  // Leader heartbeat interval
	ElectionTimeout:     150 * time.Millisecond, // Base election timeout
	SnapshotInterval:    5 * time.Minute,        // Snapshot check interval
	SnapshotThreshold:   10000,                  // Log entries before snapshot
	MaxEntriesPerAppend: 100,                    // Max entries per AppendEntries RPC
}
```

## Key Features

### Leader Election
- Randomized election timeouts prevent split votes
- Automatic re-election on leader failure
- Majority vote required

### Log Replication
- Consistent ordering across all nodes
- Automatic retry on failure
- Conflict resolution for network partitions

### Safety Guarantees
- Log Matching Property: logs are consistent across nodes
- Leader Completeness: committed entries never lost
- State Machine Safety: all nodes apply same commands in same order

### Snapshotting
- Automatic log compaction
- Configurable thresholds
- Efficient state transfer for slow followers

### Fault Tolerance
- Tolerates minority node failures
- Automatic recovery from crashes
- Network partition handling

## Testing

Run the included tests:

```bash
go test -v ./internal/raft/...
```

Tests include:
- Basic leader election
- Log replication
- Leader failover
- Snapshot creation/restoration
- Network partition recovery

## Production Considerations

### Cluster Size
- **3 nodes**: Tolerates 1 failure (recommended minimum)
- **5 nodes**: Tolerates 2 failures (recommended for production)
- **7 nodes**: Tolerates 3 failures (high availability)

Odd numbers are preferred to avoid split-brain scenarios.

### Performance Tuning

```go
// Low latency (local network)
config.HeartbeatTimeout = 50 * time.Millisecond
config.ElectionTimeout = 150 * time.Millisecond

// High latency (WAN)
config.HeartbeatTimeout = 200 * time.Millisecond
config.ElectionTimeout = 1 * time.Second

// High throughput
config.MaxEntriesPerAppend = 500
config.SnapshotThreshold = 50000
```

### Monitoring

Monitor these metrics:
- Leader election count
- Log replication latency
- Snapshot size and frequency
- Node state transitions
- RPC failure rate

### Deployment

1. **Start all nodes simultaneously** for initial cluster formation
2. **Configure persistent storage** with sufficient disk space
3. **Use stable network addresses** for peer communication
4. **Monitor leader stability** and election frequency
5. **Plan for rolling upgrades** to maintain availability

## API Reference

### Raft Node

```go
// Create new Raft node
func New(config *Config, transport Transport, fsm FSM, applyCh chan ApplyMsg) (*Raft, error)

// Start the Raft consensus algorithm
func (r *Raft) Start(ctx context.Context) error

// Shutdown gracefully shuts down the node
func (r *Raft) Shutdown() error

// GetState returns current term and leader status
func (r *Raft) GetState() (uint64, bool)

// Apply submits a command to the cluster
func (r *Raft) Apply(command []byte, timeout time.Duration) (uint64, uint64, error)

// AddPeer adds a peer to the cluster
func (r *Raft) AddPeer(id, address string) error

// RemovePeer removes a peer from the cluster
func (r *Raft) RemovePeer(id string) error
```

### FSM Interface

```go
type FSM interface {
	// Apply a command to the state machine
	Apply(command interface{}) interface{}

	// Create a snapshot of current state
	Snapshot() ([]byte, error)

	// Restore state from a snapshot
	Restore(snapshot []byte) error
}
```

## License

Part of the Helios project. See main LICENSE file.

## References

- [Raft Paper](https://raft.github.io/raft.pdf) - In Search of an Understandable Consensus Algorithm
- [Raft Visualization](https://raft.github.io/) - Interactive visualization of Raft

## Contributing

Contributions welcome! Please ensure all tests pass and add tests for new features.

## Support

For issues or questions, please open an issue on the Helios GitHub repository.
