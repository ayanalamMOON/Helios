package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/helios/helios/internal/atlas/store"
	"github.com/helios/helios/internal/raft"
)

// AtlasFSM implements the Raft FSM interface for ATLAS store
type AtlasFSM struct {
	store *store.Store
}

func NewAtlasFSM() *AtlasFSM {
	return &AtlasFSM{
		store: store.New(),
	}
}

func (a *AtlasFSM) Apply(command interface{}) interface{} {
	// Parse command from bytes
	var cmd map[string]interface{}
	if err := json.Unmarshal(command.([]byte), &cmd); err != nil {
		return err
	}

	op := cmd["op"].(string)
	key := cmd["key"].(string)

	switch op {
	case "set":
		value := []byte(cmd["value"].(string))
		ttl := time.Duration(0)
		if ttlMs, ok := cmd["ttl"].(float64); ok {
			ttl = time.Duration(ttlMs) * time.Millisecond
		}
		err := a.store.Set(key, value, ttl)
		if err != nil {
			return err
		}
		return "OK"

	case "delete":
		a.store.Delete(key)
		return "OK"

	default:
		return fmt.Errorf("unknown operation: %s", op)
	}
}

func (a *AtlasFSM) Snapshot() ([]byte, error) {
	// In production, serialize the entire store state
	// For now, return empty snapshot
	return []byte{}, nil
}

func (a *AtlasFSM) Restore(snapshot []byte) error {
	// In production, deserialize and restore store state
	return nil
}

// RaftAtlas combines Raft consensus with ATLAS store
type RaftAtlas struct {
	raft    *raft.Raft
	fsm     *AtlasFSM
	applyCh chan raft.ApplyMsg
}

func NewRaftAtlas(nodeID, listenAddr, dataDir string, peers map[string]string) (*RaftAtlas, error) {
	// Create configuration
	config := raft.DefaultConfig()
	config.NodeID = nodeID
	config.ListenAddr = listenAddr
	config.DataDir = dataDir

	// Create transport
	transport := raft.NewLocalTransport(listenAddr)

	// Create FSM
	fsm := NewAtlasFSM()

	// Create apply channel
	applyCh := make(chan raft.ApplyMsg, 1000)

	// Create Raft node
	node, err := raft.New(config, transport, fsm, applyCh)
	if err != nil {
		return nil, fmt.Errorf("failed to create raft node: %w", err)
	}

	// Set raft on transport (for local transport)
	transport.SetRaft(node)

	// Add peers
	for peerID, peerAddr := range peers {
		if err := node.AddPeer(peerID, peerAddr); err != nil {
			return nil, fmt.Errorf("failed to add peer %s: %w", peerID, err)
		}
	}

	return &RaftAtlas{
		raft:    node,
		fsm:     fsm,
		applyCh: applyCh,
	}, nil
}

func (ra *RaftAtlas) Start(ctx context.Context) error {
	// Start Raft
	if err := ra.raft.Start(ctx); err != nil {
		return err
	}

	// Process applied commands
	go ra.processApplied(ctx)

	return nil
}

func (ra *RaftAtlas) processApplied(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ra.applyCh:
			if msg.CommandValid {
				log.Printf("Command applied at index %d", msg.CommandIndex)
			}
			if msg.SnapshotValid {
				log.Printf("Snapshot restored at index %d", msg.SnapshotIndex)
			}
		}
	}
}

func (ra *RaftAtlas) Set(key, value string, ttl time.Duration) error {
	// Check if we're the leader
	_, isLeader := ra.raft.GetState()
	if !isLeader {
		return fmt.Errorf("not the leader - please redirect to leader")
	}

	// Create command
	cmd := map[string]interface{}{
		"op":    "set",
		"key":   key,
		"value": value,
	}
	if ttl > 0 {
		cmd["ttl"] = ttl.Milliseconds()
	}

	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	// Apply command through Raft
	_, _, err = ra.raft.Apply(cmdBytes, 5*time.Second)
	return err
}

func (ra *RaftAtlas) Get(key string) ([]byte, error) {
	// Reads can be served from local store
	// Raft ensures consistency through log replication
	return ra.fsm.store.Get(key)
}

func (ra *RaftAtlas) Delete(key string) error {
	// Check if we're the leader
	_, isLeader := ra.raft.GetState()
	if !isLeader {
		return fmt.Errorf("not the leader - please redirect to leader")
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

	// Apply command through Raft
	_, _, err = ra.raft.Apply(cmdBytes, 5*time.Second)
	return err
}

func (ra *RaftAtlas) IsLeader() bool {
	_, isLeader := ra.raft.GetState()
	return isLeader
}

func (ra *RaftAtlas) Shutdown() error {
	return ra.raft.Shutdown()
}

func main() {
	// Parse command line arguments
	if len(os.Args) < 2 {
		fmt.Println("Usage: raft-example <node-id>")
		fmt.Println("Example: raft-example node-1")
		os.Exit(1)
	}

	nodeID := os.Args[1]

	// Define cluster configuration
	clusterConfig := map[string]struct {
		Addr  string
		Peers map[string]string
	}{
		"node-1": {
			Addr: "127.0.0.1:9001",
			Peers: map[string]string{
				"node-2": "127.0.0.1:9002",
				"node-3": "127.0.0.1:9003",
			},
		},
		"node-2": {
			Addr: "127.0.0.1:9002",
			Peers: map[string]string{
				"node-1": "127.0.0.1:9001",
				"node-3": "127.0.0.1:9003",
			},
		},
		"node-3": {
			Addr: "127.0.0.1:9003",
			Peers: map[string]string{
				"node-1": "127.0.0.1:9001",
				"node-2": "127.0.0.1:9002",
			},
		},
	}

	nodeConfig, exists := clusterConfig[nodeID]
	if !exists {
		log.Fatalf("Unknown node ID: %s", nodeID)
	}

	// Create RaftAtlas instance
	dataDir := fmt.Sprintf("./data/%s", nodeID)
	ra, err := NewRaftAtlas(nodeID, nodeConfig.Addr, dataDir, nodeConfig.Peers)
	if err != nil {
		log.Fatalf("Failed to create RaftAtlas: %v", err)
	}

	// Start the node
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ra.Start(ctx); err != nil {
		log.Fatalf("Failed to start RaftAtlas: %v", err)
	}

	log.Printf("Node %s started on %s", nodeID, nodeConfig.Addr)

	// Wait for leader election
	time.Sleep(1 * time.Second)

	// Check if we're the leader
	if ra.IsLeader() {
		log.Printf("Node %s is the LEADER", nodeID)

		// Perform some operations as leader
		time.Sleep(500 * time.Millisecond)

		log.Println("Writing test data...")

		// Write some test data
		if err := ra.Set("user:1:name", "Alice", 0); err != nil {
			log.Printf("Failed to set key: %v", err)
		} else {
			log.Println("✓ Set user:1:name = Alice")
		}

		if err := ra.Set("user:2:name", "Bob", 5*time.Minute); err != nil {
			log.Printf("Failed to set key: %v", err)
		} else {
			log.Println("✓ Set user:2:name = Bob (TTL: 5m)")
		}

		if err := ra.Set("counter", "1", 0); err != nil {
			log.Printf("Failed to set key: %v", err)
		} else {
			log.Println("✓ Set counter = 1")
		}

		time.Sleep(500 * time.Millisecond)

		// Read data
		log.Println("\nReading data...")
		if val, err := ra.Get("user:1:name"); err != nil {
			log.Printf("Failed to get key: %v", err)
		} else {
			log.Printf("✓ Get user:1:name = %s", string(val))
		}

		if val, err := ra.Get("counter"); err != nil {
			log.Printf("Failed to get key: %v", err)
		} else {
			log.Printf("✓ Get counter = %s", string(val))
		}

		// Delete a key
		log.Println("\nDeleting counter...")
		if err := ra.Delete("counter"); err != nil {
			log.Printf("Failed to delete key: %v", err)
		} else {
			log.Println("✓ Deleted counter")
		}

		// Try to read deleted key
		if val, err := ra.Get("counter"); err != nil {
			log.Printf("✓ Get counter = <not found> (expected)")
		} else {
			log.Printf("Unexpected: got value %s", string(val))
		}
	} else {
		log.Printf("Node %s is a FOLLOWER", nodeID)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	log.Println("\nNode running. Press Ctrl+C to shutdown...")
	<-sigCh

	log.Println("\nShutting down...")
	if err := ra.Shutdown(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	log.Println("Shutdown complete")
}
