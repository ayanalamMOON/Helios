package atlas

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/helios/helios/internal/atlas/protocol"
	"github.com/helios/helios/internal/atlas/store"
	"github.com/helios/helios/internal/observability"
	"github.com/helios/helios/internal/raft"
)

// RaftAtlas wraps Atlas with Raft consensus for distributed replication
type RaftAtlas struct {
	atlas   *Atlas
	raft    *raft.Raft
	applyCh chan raft.ApplyMsg
	logger  *observability.Logger
	ctx     context.Context
	cancel  context.CancelFunc
}

// RaftConfig holds Raft-specific configuration
type RaftConfig struct {
	Enabled           bool
	NodeID            string
	BindAddr          string
	DataDir           string
	HeartbeatTimeout  time.Duration
	ElectionTimeout   time.Duration
	SnapshotThreshold int
	Peers             []RaftPeer
}

// RaftPeer represents a peer node in the cluster
type RaftPeer struct {
	ID      string
	Address string
}

// raftFSM implements the raft.FSM interface for Atlas
type raftFSM struct {
	atlas *Atlas
}

func (f *raftFSM) Apply(command interface{}) interface{} {
	data, ok := command.([]byte)
	if !ok {
		return fmt.Errorf("invalid command type")
	}

	var cmd protocol.Command
	if err := json.Unmarshal(data, &cmd); err != nil {
		return err
	}
	return f.atlas.ApplyCommand(&cmd)
}

func (f *raftFSM) Snapshot() ([]byte, error) {
	data := f.atlas.store.GetAll()
	return json.Marshal(data)
}

func (f *raftFSM) Restore(data []byte) error {
	var storeData map[string][]byte
	if err := json.Unmarshal(data, &storeData); err != nil {
		return err
	}
	// Convert to proper Entry format
	entryData := make(map[string]store.Entry)
	for k, v := range storeData {
		entryData[k] = store.Entry{Value: v} // ExpireAt defaults to zero (no expiry)
	}
	f.atlas.store.LoadAll(entryData)
	return nil
}

// NewRaftAtlas creates a new RaftAtlas instance with distributed consensus
func NewRaftAtlas(cfg *Config, raftCfg *RaftConfig) (*RaftAtlas, error) {
	// Create base Atlas instance
	atlasInstance, err := New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Atlas: %w", err)
	}

	logger := observability.NewLogger("raftatlas")

	ctx, cancel := context.WithCancel(context.Background())

	// If Raft is disabled, return a simple wrapper
	if !raftCfg.Enabled {
		logger.Info("Raft disabled, running in standalone mode")
		return &RaftAtlas{
			atlas:   atlasInstance,
			raft:    nil,
			applyCh: nil,
			logger:  logger,
			ctx:     ctx,
			cancel:  cancel,
		}, nil
	}

	logger.Info("Initializing Raft consensus", map[string]interface{}{
		"node_id":   raftCfg.NodeID,
		"bind_addr": raftCfg.BindAddr,
		"peers":     len(raftCfg.Peers),
	})

	// Create Raft configuration
	raftConfig := raft.DefaultConfig()
	raftConfig.NodeID = raftCfg.NodeID
	raftConfig.HeartbeatTimeout = raftCfg.HeartbeatTimeout
	raftConfig.ElectionTimeout = raftCfg.ElectionTimeout
	raftConfig.SnapshotThreshold = uint64(raftCfg.SnapshotThreshold)
	raftConfig.DataDir = raftCfg.DataDir

	// Create Raft transport
	transport := raft.NewLocalTransport(raftCfg.BindAddr)

	// Create FSM
	fsm := &raftFSM{atlas: atlasInstance}

	// Create apply channel
	applyCh := make(chan raft.ApplyMsg, 256)

	// Create Raft instance
	raftNode, err := raft.New(raftConfig, transport, fsm, applyCh)
	if err != nil {
		cancel()
		atlasInstance.Close()
		return nil, fmt.Errorf("failed to create Raft node: %w", err)
	}

	// Add peers
	for _, peer := range raftCfg.Peers {
		if err := raftNode.AddPeer(peer.ID, peer.Address); err != nil {
			cancel()
			atlasInstance.Close()
			return nil, fmt.Errorf("failed to add peer %s: %w", peer.ID, err)
		}
	}

	ra := &RaftAtlas{
		atlas:   atlasInstance,
		raft:    raftNode,
		applyCh: applyCh,
		logger:  logger,
		ctx:     ctx,
		cancel:  cancel,
	}

	// Start Raft node
	if err := raftNode.Start(ctx); err != nil {
		cancel()
		atlasInstance.Close()
		return nil, fmt.Errorf("failed to start Raft: %w", err)
	}

	// Start applying committed entries
	go ra.applyLoop()

	logger.Info("Raft consensus initialized successfully")

	return ra, nil
}

// applyLoop continuously applies committed Raft entries to Atlas
func (ra *RaftAtlas) applyLoop() {
	for {
		select {
		case <-ra.ctx.Done():
			return
		case msg := <-ra.applyCh:
			if msg.CommandValid {
				// Command is already applied by FSM, just log it
				ra.logger.Debug("Command applied", map[string]interface{}{
					"index": msg.CommandIndex,
				})
			} else if msg.SnapshotValid {
				// Snapshot restore
				ra.logger.Info("Snapshot restored", map[string]interface{}{
					"index": msg.SnapshotIndex,
					"term":  msg.SnapshotTerm,
				})
			}
		}
	}
}

// Handle processes a command through Raft consensus
func (ra *RaftAtlas) Handle(cmd *protocol.Command) *protocol.Response {
	// For read commands, execute directly if we're the leader or allow stale reads
	if cmd.Type == "GET" || cmd.Type == "TTL" {
		// In a production system, you might want to check leadership for consistency
		return ra.atlas.executeRead(cmd)
	}

	// If Raft is disabled, use Atlas directly
	if ra.raft == nil {
		return ra.atlas.Handle(cmd)
	}

	// For write commands, go through Raft
	// Serialize command
	data, err := json.Marshal(cmd)
	if err != nil {
		return protocol.NewErrorResponse(fmt.Errorf("failed to marshal command: %w", err))
	}

	// Apply through Raft
	_, _, err = ra.raft.Apply(data, 5*time.Second)
	if err != nil {
		return protocol.NewErrorResponse(fmt.Errorf("raft apply failed: %w", err))
	}

	return protocol.NewSuccessResponse()
}

// IsLeader returns whether this node is the Raft leader
func (ra *RaftAtlas) IsLeader() bool {
	if ra.raft == nil {
		return true // Standalone mode, always "leader"
	}
	return ra.raft.GetNodeState() == raft.Leader
}

// LeaderID returns the current leader's ID
func (ra *RaftAtlas) LeaderID() string {
	if ra.raft == nil {
		return "standalone" // Standalone mode
	}
	leader, _ := ra.raft.GetLeader()
	return leader
}

// Snapshot creates a snapshot of the current state
func (ra *RaftAtlas) Snapshot() error {
	return ra.atlas.Snapshot()
}

// Close shuts down RaftAtlas gracefully
func (ra *RaftAtlas) Close() error {
	ra.logger.Info("Shutting down RaftAtlas")

	// Cancel context
	ra.cancel()

	// Shutdown Raft
	if ra.raft != nil {
		if err := ra.raft.Shutdown(); err != nil {
			ra.logger.Error("Raft shutdown error", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

	// Close Atlas
	if err := ra.atlas.Close(); err != nil {
		return err
	}

	ra.logger.Info("RaftAtlas shutdown complete")
	return nil
}

// Stats returns combined Atlas and Raft statistics
func (ra *RaftAtlas) Stats() map[string]interface{} {
	stats := ra.atlas.Stats()

	if ra.raft != nil {
		stats["raft_enabled"] = true
		stats["raft_state"] = ra.raft.GetNodeState().String()
		stats["raft_term"] = ra.raft.GetCurrentTerm()
		leader, _ := ra.raft.GetLeader()
		stats["raft_leader"] = leader
	} else {
		stats["raft_enabled"] = false
		stats["mode"] = "standalone"
	}

	return stats
}

// Direct access methods (delegate to Atlas)

// Get retrieves a value from the store
func (ra *RaftAtlas) Get(key string) ([]byte, bool) {
	return ra.atlas.Get(key)
}

// Set stores a key-value pair through Raft consensus
func (ra *RaftAtlas) Set(key string, value []byte, ttl int64) {
	cmd := protocol.NewSetCommand(key, value, ttl)
	ra.Handle(cmd)
}

// Delete removes a key through Raft consensus
func (ra *RaftAtlas) Delete(key string) bool {
	cmd := protocol.NewDelCommand(key)
	resp := ra.Handle(cmd)
	return resp.OK
}

// Scan returns keys matching a prefix
func (ra *RaftAtlas) Scan(prefix string) []string {
	return ra.atlas.Scan(prefix)
}
