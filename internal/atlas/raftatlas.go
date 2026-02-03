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
	TLS               *raft.TLSConfig // TLS configuration
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
		"tls":       raftCfg.TLS != nil && raftCfg.TLS.Enabled,
	})

	// Create Raft configuration
	raftConfig := raft.DefaultConfig()
	raftConfig.NodeID = raftCfg.NodeID
	raftConfig.HeartbeatTimeout = raftCfg.HeartbeatTimeout
	raftConfig.ElectionTimeout = raftCfg.ElectionTimeout
	raftConfig.SnapshotThreshold = uint64(raftCfg.SnapshotThreshold)
	raftConfig.DataDir = raftCfg.DataDir
	raftConfig.TLS = raftCfg.TLS

	// Create Raft transport
	var transport raft.Transport
	if raftCfg.TLS != nil && raftCfg.TLS.Enabled {
		// Use TLS-enabled network transport
		netTransport, err := raft.NewNetworkTransport(raftCfg.BindAddr, raftCfg.TLS)
		if err != nil {
			cancel()
			atlasInstance.Close()
			return nil, fmt.Errorf("failed to create network transport: %w", err)
		}
		transport = netTransport
		logger.Info("Using TLS transport", map[string]interface{}{
			"cert_file": raftCfg.TLS.CertFile,
			"ca_file":   raftCfg.TLS.CAFile,
		})
	} else {
		// Use local in-memory transport (for testing)
		transport = raft.NewLocalTransport(raftCfg.BindAddr)
		logger.Warn("Using local transport (not suitable for production)")
	}

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
	// Handle peer management commands directly (not through Raft log)
	switch cmd.Type {
	case "ADDPEER":
		if !ra.IsLeader() {
			leader, _ := ra.GetLeader()
			return protocol.NewErrorResponse(fmt.Errorf("not the leader, redirect to: %s", leader))
		}
		if err := ra.AddPeer(cmd.Key, cmd.Value); err != nil {
			return protocol.NewErrorResponse(err)
		}
		return protocol.NewSuccessResponse()

	case "REMOVEPEER":
		if !ra.IsLeader() {
			leader, _ := ra.GetLeader()
			return protocol.NewErrorResponse(fmt.Errorf("not the leader, redirect to: %s", leader))
		}
		if err := ra.RemovePeer(cmd.Key); err != nil {
			return protocol.NewErrorResponse(err)
		}
		return protocol.NewSuccessResponse()

	case "LISTPEERS":
		peers, err := ra.GetPeers()
		if err != nil {
			return protocol.NewErrorResponse(err)
		}
		// Convert peers to map for JSON response
		peerMap := make(map[string]interface{})
		for _, p := range peers {
			peerMap[p.ID] = map[string]string{
				"address": p.Address,
				"state":   p.State,
			}
		}
		resp := protocol.NewSuccessResponse()
		resp.Extra = map[string]interface{}{
			"peers": peerMap,
		}
		return resp
	}

	// For read commands, execute directly if we're the leader or allow stale reads
	if cmd.Type == "GET" || cmd.Type == "TTL" {
		// If session ID is provided, ensure read-your-writes consistency
		if cmd.SessionID != "" && ra.raft != nil {
			minIndex := ra.raft.GetSessionIndex(cmd.SessionID)
			if minIndex > 0 {
				// Wait for replication to catch up
				if err := ra.raft.ReadConsistent(cmd.SessionID, minIndex); err != nil {
					ra.logger.Warn("Read consistency check failed", map[string]interface{}{
						"error":     err.Error(),
						"sessionID": cmd.SessionID,
					})
					// Continue with stale read rather than failing
				}
			}
		}
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
	index, _, err := ra.raft.Apply(data, 5*time.Second)
	if err != nil {
		return protocol.NewErrorResponse(fmt.Errorf("raft apply failed: %w", err))
	}

	// Update session index after write for read-your-writes consistency
	if cmd.SessionID != "" {
		ra.raft.UpdateSessionAfterWrite(cmd.SessionID, index)
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

// GetConsistent retrieves a value with read-your-writes consistency guarantee.
func (ra *RaftAtlas) GetConsistent(key string, sessionID string) ([]byte, bool, error) {
	// If session ID is provided, ensure read-your-writes consistency
	if sessionID != "" && ra.raft != nil {
		minIndex := ra.raft.GetSessionIndex(sessionID)
		if minIndex > 0 {
			// Wait for replication to catch up
			if err := ra.raft.ReadConsistent(sessionID, minIndex); err != nil {
				return nil, false, fmt.Errorf("read consistency failed: %w", err)
			}
		}
	}

	value, exists := ra.atlas.Get(key)
	return value, exists, nil
}

// Set stores a key-value pair through Raft consensus
func (ra *RaftAtlas) Set(key string, value []byte, ttl int64) {
	cmd := protocol.NewSetCommand(key, value, ttl)
	ra.Handle(cmd)
}

// SetWithSession stores a key-value pair and tracks the session for consistency.
func (ra *RaftAtlas) SetWithSession(key string, value []byte, ttl int64, sessionID string) error {
	cmd := protocol.NewSetCommand(key, value, ttl).WithSession(sessionID)
	resp := ra.Handle(cmd)
	if !resp.OK {
		return fmt.Errorf("set failed: %v", resp.Error)
	}
	return nil
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

// AddPeer adds a peer to the Raft cluster
func (ra *RaftAtlas) AddPeer(id, address string) error {
	if ra.raft == nil {
		return fmt.Errorf("raft not enabled")
	}

	ra.logger.Info("Adding peer to cluster", map[string]interface{}{
		"peer_id": id,
		"address": address,
	})

	if err := ra.raft.AddPeer(id, address); err != nil {
		ra.logger.Error("Failed to add peer", map[string]interface{}{
			"peer_id": id,
			"error":   err.Error(),
		})
		return err
	}

	ra.logger.Info("Peer added successfully", map[string]interface{}{
		"peer_id": id,
	})
	return nil
}

// RemovePeer removes a peer from the Raft cluster
func (ra *RaftAtlas) RemovePeer(id string) error {
	if ra.raft == nil {
		return fmt.Errorf("raft not enabled")
	}

	ra.logger.Info("Removing peer from cluster", map[string]interface{}{
		"peer_id": id,
	})

	if err := ra.raft.RemovePeer(id); err != nil {
		ra.logger.Error("Failed to remove peer", map[string]interface{}{
			"peer_id": id,
			"error":   err.Error(),
		})
		return err
	}

	ra.logger.Info("Peer removed successfully", map[string]interface{}{
		"peer_id": id,
	})
	return nil
}

// GetPeers returns information about all peers in the cluster
func (ra *RaftAtlas) GetPeers() ([]PeerInfo, error) {
	if ra.raft == nil {
		return nil, fmt.Errorf("raft not enabled")
	}

	peers := ra.raft.GetPeers()
	peerInfos := make([]PeerInfo, 0, len(peers))

	for id, peer := range peers {
		peerInfos = append(peerInfos, PeerInfo{
			ID:      id,
			Address: peer.Address,
			State:   "Active", // Could be enhanced with actual peer state
		})
	}

	return peerInfos, nil
}

// GetLeader returns the current leader's ID
func (ra *RaftAtlas) GetLeader() (string, error) {
	if ra.raft == nil {
		return "", fmt.Errorf("raft not enabled")
	}

	leaderID, _ := ra.raft.GetLeader()
	if leaderID == "" {
		return "", fmt.Errorf("no leader elected")
	}

	return leaderID, nil
}

// ClusterStatus represents comprehensive cluster status information
type ClusterStatus struct {
	Node          NodeStatus          `json:"node"`
	Leader        LeaderInfo          `json:"leader"`
	Indices       IndicesInfo         `json:"indices"`
	Peers         []PeerStatus        `json:"peers"`
	Sessions      SessionsInfo        `json:"sessions"`
	TLS           TLSStatus           `json:"tls"`
	Uptime        string              `json:"uptime"`
	UptimeStats   *UptimeStatusInfo   `json:"uptime_stats,omitempty"`
	ClusterHealth *ClusterHealthInfo  `json:"cluster_health,omitempty"`
	Snapshot      *SnapshotStatusInfo `json:"snapshot,omitempty"`
}

// UptimeStatusInfo represents uptime statistics in cluster status
type UptimeStatusInfo struct {
	CurrentUptimeMs     int64   `json:"current_uptime_ms"`
	CurrentUptimeStr    string  `json:"current_uptime_str"`
	TotalUptimeMs       int64   `json:"total_uptime_ms"`
	TotalUptimeStr      string  `json:"total_uptime_str"`
	TotalSessions       int     `json:"total_sessions"`
	UptimePercentage24h float64 `json:"uptime_percentage_24h"`
	UptimePercentage7d  float64 `json:"uptime_percentage_7d"`
	UptimePercentageAll float64 `json:"uptime_percentage_all"`
	TotalRestarts       int     `json:"total_restarts"`
	TotalCrashes        int     `json:"total_crashes"`
	LeadershipRatio     float64 `json:"leadership_ratio"`
	TotalElections      int     `json:"total_elections"`
	FirstTrackedTime    string  `json:"first_tracked_time,omitempty"`
}

// SnapshotStatusInfo represents snapshot statistics in cluster status
type SnapshotStatusInfo struct {
	HasSnapshot            bool    `json:"has_snapshot"`
	LatestSnapshotID       string  `json:"latest_snapshot_id,omitempty"`
	LatestSnapshotIndex    uint64  `json:"latest_snapshot_index"`
	LatestSnapshotTerm     uint64  `json:"latest_snapshot_term"`
	LatestSnapshotSize     int64   `json:"latest_snapshot_size"`
	LatestSnapshotTime     string  `json:"latest_snapshot_time,omitempty"`
	LatestSnapshotAgeMs    int64   `json:"latest_snapshot_age_ms"`
	LatestSnapshotAge      string  `json:"latest_snapshot_age"`
	TotalSnapshots         int     `json:"total_snapshots"`
	TotalSnapshotsCreated  int     `json:"total_snapshots_created"`
	TotalSnapshotsReceived int     `json:"total_snapshots_received"`
	TotalRestores          int     `json:"total_restores"`
	TotalBytesWritten      int64   `json:"total_bytes_written"`
	FailedCreations        int     `json:"failed_creations"`
	FailedRestores         int     `json:"failed_restores"`
	AvgCreationTimeMs      float64 `json:"avg_creation_time_ms"`
	SnapshotInterval       string  `json:"snapshot_interval"`
	SnapshotThreshold      uint64  `json:"snapshot_threshold"`
}

// ClusterHealthInfo represents cluster-wide health metrics
type ClusterHealthInfo struct {
	HealthyPeers        int     `json:"healthy_peers"`
	UnhealthyPeers      int     `json:"unhealthy_peers"`
	TotalPeers          int     `json:"total_peers"`
	AvgClusterLatencyMs float64 `json:"avg_cluster_latency_ms"`
	MaxClusterLatencyMs float64 `json:"max_cluster_latency_ms"`
	ClusterErrorRate    float64 `json:"cluster_error_rate"`
}

// NodeStatus represents the current node's status
type NodeStatus struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Term  uint64 `json:"term"`
}

// LeaderInfo represents leader information
type LeaderInfo struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	IsLeader bool   `json:"is_leader"`
}

// IndicesInfo represents log indices
type IndicesInfo struct {
	LastLog      uint64 `json:"last_log"`
	LastLogTerm  uint64 `json:"last_log_term"`
	CommitIndex  uint64 `json:"commit_index"`
	AppliedIndex uint64 `json:"applied_index"`
}

// PeerStatus represents a peer's status (includes replication info if leader)
type PeerStatus struct {
	ID         string             `json:"id"`
	Address    string             `json:"address"`
	MatchIndex *uint64            `json:"match_index,omitempty"`
	NextIndex  *uint64            `json:"next_index,omitempty"`
	Latency    *PeerLatencyStatus `json:"latency,omitempty"`
}

// PeerLatencyStatus represents peer latency metrics
type PeerLatencyStatus struct {
	AvgLatencyMs      float64 `json:"avg_latency_ms"`
	MinLatencyMs      float64 `json:"min_latency_ms"`
	MaxLatencyMs      float64 `json:"max_latency_ms"`
	P50LatencyMs      float64 `json:"p50_latency_ms"`
	P90LatencyMs      float64 `json:"p90_latency_ms"`
	P99LatencyMs      float64 `json:"p99_latency_ms"`
	LastLatencyMs     float64 `json:"last_latency_ms"`
	SampleCount       int64   `json:"sample_count"`
	ErrorCount        int64   `json:"error_count"`
	ErrorRate         float64 `json:"error_rate"`
	LastContactUnix   int64   `json:"last_contact_unix,omitempty"`
	Reachable         bool    `json:"reachable"`
	ConnectionHealthy bool    `json:"connection_healthy"`
	ConsecutiveErrors int     `json:"consecutive_errors"`
}

// SessionsInfo represents session statistics
type SessionsInfo struct {
	ActiveCount int      `json:"active_count"`
	SessionIDs  []string `json:"session_ids,omitempty"`
}

// TLSStatus represents TLS configuration status
type TLSStatus struct {
	Enabled    bool `json:"enabled"`
	VerifyPeer bool `json:"verify_peer"`
}

// GetClusterStatus returns comprehensive cluster status
func (ra *RaftAtlas) GetClusterStatus() (interface{}, error) {
	if ra.raft == nil {
		return nil, fmt.Errorf("raft not enabled")
	}

	// Get node information
	nodeID := ra.raft.GetNodeID()
	nodeState := ra.raft.GetNodeState()
	term := ra.raft.GetCurrentTerm()
	_, isLeader := ra.raft.GetState()

	// Get leader information
	leaderID, leaderAddr := ra.raft.GetLeader()

	// Get log indices
	lastLogIndex, lastLogTerm := ra.raft.GetLastLogInfo()
	commitIndex := ra.raft.GetCommitIndex()
	appliedIndex := ra.raft.GetLastApplied()

	// Build peer status list with latency info
	peers := ra.raft.GetPeers()
	peerStatuses := make([]PeerStatus, 0, len(peers))
	allLatencyStats := ra.raft.GetAllPeerLatencyStats()

	for id, peer := range peers {
		ps := PeerStatus{
			ID:      id,
			Address: peer.Address,
		}

		// If we're leader, include replication indices
		if isLeader {
			matchIdx := ra.raft.GetMatchIndex(id)
			if matchIdx > 0 {
				ps.MatchIndex = &matchIdx
			}
			nextIdx := ra.raft.GetNextIndex(id)
			if nextIdx > 0 {
				ps.NextIndex = &nextIdx
			}
		}

		// Add latency information if available
		if latencyInfo, exists := allLatencyStats[id]; exists {
			aggStats := latencyInfo.AggregatedLatency
			var errorRate float64
			if aggStats.Count > 0 {
				errorRate = float64(aggStats.ErrorCount) / float64(aggStats.Count+aggStats.ErrorCount)
			}
			ps.Latency = &PeerLatencyStatus{
				AvgLatencyMs:      aggStats.AvgValueMs,
				MinLatencyMs:      aggStats.MinValueMs,
				MaxLatencyMs:      aggStats.MaxValueMs,
				P50LatencyMs:      aggStats.P50ValueMs,
				P90LatencyMs:      aggStats.P90ValueMs,
				P99LatencyMs:      aggStats.P99ValueMs,
				LastLatencyMs:     aggStats.LastValueMs,
				SampleCount:       aggStats.Count,
				ErrorCount:        aggStats.ErrorCount,
				ErrorRate:         errorRate,
				LastContactUnix:   latencyInfo.LastContactUnix,
				Reachable:         latencyInfo.Reachable,
				ConnectionHealthy: latencyInfo.ConnectionHealthy,
				ConsecutiveErrors: latencyInfo.ConsecutiveErrors,
			}
		}

		peerStatuses = append(peerStatuses, ps)
	}

	// Get session information
	sessionMgr := ra.raft.GetSessionManager()
	allSessions := sessionMgr.GetAllSessions()
	sessionsInfo := SessionsInfo{
		ActiveCount: len(allSessions),
	}
	// Only include session IDs if there aren't too many
	if len(allSessions) <= 10 {
		sessionsInfo.SessionIDs = make([]string, 0, len(allSessions))
		for _, s := range allSessions {
			sessionsInfo.SessionIDs = append(sessionsInfo.SessionIDs, s.ID)
		}
	}

	// Get TLS status
	tlsConfig := ra.raft.GetTLSConfig()
	tlsStatus := TLSStatus{
		Enabled:    tlsConfig != nil && tlsConfig.CertFile != "",
		VerifyPeer: tlsConfig != nil && tlsConfig.VerifyPeer,
	}

	// Get uptime
	uptime := ra.raft.GetUptime()

	// Convert NodeState to string
	var stateStr string
	switch nodeState {
	case raft.Follower:
		stateStr = "Follower"
	case raft.Candidate:
		stateStr = "Candidate"
	case raft.Leader:
		stateStr = "Leader"
	default:
		stateStr = "Unknown"
	}

	// Get cluster health info from latency stats
	var clusterHealth *ClusterHealthInfo
	healthy, unhealthy, total := ra.raft.GetPeerHealthSummary()
	if total > 0 {
		aggStats := ra.raft.GetAggregatedLatencyStats()
		var clusterErrorRate float64
		if aggStats.Count > 0 {
			clusterErrorRate = float64(aggStats.ErrorCount) / float64(aggStats.Count+aggStats.ErrorCount)
		}
		clusterHealth = &ClusterHealthInfo{
			HealthyPeers:        healthy,
			UnhealthyPeers:      unhealthy,
			TotalPeers:          total,
			AvgClusterLatencyMs: aggStats.AvgValueMs,
			MaxClusterLatencyMs: aggStats.MaxValueMs,
			ClusterErrorRate:    clusterErrorRate,
		}
	}

	// Get uptime stats
	var uptimeStats *UptimeStatusInfo
	uptimeStatsJSON := ra.raft.GetUptimeStatsJSON()
	if uptimeStatsJSON.TotalSessions > 0 || uptimeStatsJSON.CurrentUptimeMs > 0 {
		uptimeStats = &UptimeStatusInfo{
			CurrentUptimeMs:     uptimeStatsJSON.CurrentUptimeMs,
			CurrentUptimeStr:    uptimeStatsJSON.CurrentUptimeStr,
			TotalUptimeMs:       uptimeStatsJSON.TotalUptimeMs,
			TotalUptimeStr:      uptimeStatsJSON.TotalUptimeStr,
			TotalSessions:       uptimeStatsJSON.TotalSessions,
			UptimePercentage24h: uptimeStatsJSON.UptimePercentage24h,
			UptimePercentage7d:  uptimeStatsJSON.UptimePercentage7d,
			UptimePercentageAll: uptimeStatsJSON.UptimePercentageAll,
			TotalRestarts:       uptimeStatsJSON.TotalRestarts,
			TotalCrashes:        uptimeStatsJSON.TotalCrashes,
			LeadershipRatio:     uptimeStatsJSON.LeadershipRatio,
			TotalElections:      uptimeStatsJSON.TotalElections,
			FirstTrackedTime:    uptimeStatsJSON.FirstTrackedTime,
		}
	}

	// Get snapshot stats
	var snapshotStats *SnapshotStatusInfo
	snapshotStatsJSON := ra.raft.GetSnapshotStatsJSON()
	if snapshotStatsJSON != nil {
		snapshotStats = &SnapshotStatusInfo{
			HasSnapshot:            snapshotStatsJSON.HasSnapshot,
			LatestSnapshotID:       snapshotStatsJSON.LatestSnapshotID,
			LatestSnapshotIndex:    snapshotStatsJSON.LatestSnapshotIndex,
			LatestSnapshotTerm:     snapshotStatsJSON.LatestSnapshotTerm,
			LatestSnapshotSize:     snapshotStatsJSON.LatestSnapshotSize,
			LatestSnapshotTime:     snapshotStatsJSON.LatestSnapshotTime,
			LatestSnapshotAgeMs:    snapshotStatsJSON.LatestSnapshotAgeMs,
			LatestSnapshotAge:      snapshotStatsJSON.LatestSnapshotAge,
			TotalSnapshots:         snapshotStatsJSON.TotalSnapshots,
			TotalSnapshotsCreated:  snapshotStatsJSON.TotalSnapshotsCreated,
			TotalSnapshotsReceived: snapshotStatsJSON.TotalSnapshotsReceived,
			TotalRestores:          snapshotStatsJSON.TotalRestores,
			TotalBytesWritten:      snapshotStatsJSON.TotalBytesWritten,
			FailedCreations:        snapshotStatsJSON.FailedCreations,
			FailedRestores:         snapshotStatsJSON.FailedRestores,
			AvgCreationTimeMs:      snapshotStatsJSON.AvgCreationTimeMs,
			SnapshotInterval:       snapshotStatsJSON.SnapshotInterval,
			SnapshotThreshold:      snapshotStatsJSON.SnapshotThreshold,
		}
	}

	return &ClusterStatus{
		Node: NodeStatus{
			ID:    nodeID,
			State: stateStr,
			Term:  term,
		},
		Leader: LeaderInfo{
			ID:       leaderID,
			Address:  leaderAddr,
			IsLeader: isLeader,
		},
		Indices: IndicesInfo{
			LastLog:      lastLogIndex,
			LastLogTerm:  lastLogTerm,
			CommitIndex:  commitIndex,
			AppliedIndex: appliedIndex,
		},
		Peers:         peerStatuses,
		Sessions:      sessionsInfo,
		TLS:           tlsStatus,
		Uptime:        uptime.String(),
		UptimeStats:   uptimeStats,
		ClusterHealth: clusterHealth,
		Snapshot:      snapshotStats,
	}, nil
}

// PeerInfo represents information about a peer node
type PeerInfo struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	State   string `json:"state"`
}

// GetPeerLatencyStats returns latency statistics for a specific peer
func (ra *RaftAtlas) GetPeerLatencyStats(peerID string) (interface{}, bool) {
	if ra.raft == nil {
		return nil, false
	}
	return ra.raft.GetPeerLatencyStats(peerID)
}

// GetAllPeerLatencyStats returns latency statistics for all peers
func (ra *RaftAtlas) GetAllPeerLatencyStats() interface{} {
	if ra.raft == nil {
		return nil
	}
	return ra.raft.GetAllPeerLatencyStats()
}

// GetAggregatedLatencyStats returns aggregated latency statistics across all peers
func (ra *RaftAtlas) GetAggregatedLatencyStats() interface{} {
	if ra.raft == nil {
		return nil
	}
	return ra.raft.GetAggregatedLatencyStats()
}

// GetPeerHealthSummary returns a summary of healthy vs unhealthy peers
func (ra *RaftAtlas) GetPeerHealthSummary() (healthy int, unhealthy int, total int) {
	if ra.raft == nil {
		return 0, 0, 0
	}
	return ra.raft.GetPeerHealthSummary()
}

// ResetPeerLatencyStats resets all peer latency statistics
func (ra *RaftAtlas) ResetPeerLatencyStats() {
	if ra.raft != nil {
		ra.raft.ResetPeerLatencyStats()
	}
}

// GetUptimeStats returns current uptime statistics
func (ra *RaftAtlas) GetUptimeStats() interface{} {
	if ra.raft == nil {
		return nil
	}
	return ra.raft.GetUptimeStatsJSON()
}

// GetUptimeHistory returns the complete uptime history
func (ra *RaftAtlas) GetUptimeHistory(maxEvents, maxSessions int) interface{} {
	if ra.raft == nil {
		return nil
	}
	return ra.raft.GetUptimeHistory(maxEvents, maxSessions)
}

// ResetUptimeHistory resets all uptime history
func (ra *RaftAtlas) ResetUptimeHistory() {
	if ra.raft != nil {
		ra.raft.ResetUptimeHistory()
	}
}

// GetSnapshotStats returns current snapshot statistics
func (ra *RaftAtlas) GetSnapshotStats() interface{} {
	if ra.raft == nil {
		return nil
	}
	return ra.raft.GetSnapshotStatsJSON()
}

// GetSnapshotHistory returns the complete snapshot history
func (ra *RaftAtlas) GetSnapshotHistory(maxEvents, maxSnapshots int) interface{} {
	if ra.raft == nil {
		return nil
	}
	return ra.raft.GetSnapshotHistory(maxEvents, maxSnapshots)
}

// GetLatestSnapshotMetadata returns metadata about the latest snapshot
func (ra *RaftAtlas) GetLatestSnapshotMetadata() interface{} {
	if ra.raft == nil {
		return nil
	}
	return ra.raft.GetLatestSnapshotMetadata()
}

// ResetSnapshotStats resets all snapshot statistics
func (ra *RaftAtlas) ResetSnapshotStats() {
	if ra.raft != nil {
		ra.raft.ResetSnapshotStats()
	}
}
