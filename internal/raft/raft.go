package raft

import (
	"context"
	crypto_rand "crypto/rand"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// Logger provides logging functionality
type Logger struct {
	prefix string
}

// NewLogger creates a new logger
func NewLogger(prefix string) *Logger {
	return &Logger{prefix: prefix}
}

// Info logs an informational message
func (l *Logger) Info(msg string, args ...interface{}) {
	log.Printf("[INFO] %s: %s %v\n", l.prefix, msg, args)
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, args ...interface{}) {
	log.Printf("[DEBUG] %s: %s %v\n", l.prefix, msg, args)
}

// Error logs an error message
func (l *Logger) Error(msg string, args ...interface{}) {
	log.Printf("[ERROR] %s: %s %v\n", l.prefix, msg, args)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, args ...interface{}) {
	log.Printf("[WARN] %s: %s %v\n", l.prefix, msg, args)
}

// Raft implements the Raft consensus algorithm
type Raft struct {
	// Configuration
	config *Config
	logger *Logger

	// Persistent state (updated on stable storage before responding to RPCs)
	currentTerm uint64 // Latest term server has seen
	votedFor    string // CandidateID that received vote in current term
	log         *Log   // Log entries

	// Volatile state (on all servers)
	commitIndex uint64    // Index of highest log entry known to be committed
	lastApplied uint64    // Index of highest log entry applied to state machine
	state       NodeState // Current node state
	stateMu     sync.RWMutex

	// Volatile state (on leaders, reinitialized after election)
	nextIndex  map[string]uint64 // For each server, index of next log entry to send
	matchIndex map[string]uint64 // For each server, index of highest log entry known to be replicated

	// Channels and control
	applyCh         chan ApplyMsg       // Channel to send committed entries to application
	rpcCh           chan RPC            // Channel for incoming RPCs
	shutdownCh      chan struct{}       // Signal shutdown
	electionTimer   *time.Timer         // Election timeout timer
	heartbeatTicker *time.Ticker        // Heartbeat ticker for leaders
	peers           map[string]*Peer    // Connected peers
	transport       Transport           // RPC transport layer
	fsm             FSM                 // Application state machine
	snapshotStore   *SnapshotStore      // Snapshot storage
	observationsCh  chan Observation    // Channel for state observations
	persistenceMgr  *PersistenceManager // Serializes all disk writes

	// Metrics
	lastContact         atomic.Value             // time.Time of last contact from leader
	startTime           time.Time                // Node start time for uptime tracking
	peerLatencyMgr      *PeerLatencyTracker      // Peer latency metrics tracker
	uptimeTracker       *UptimeTracker           // Historical uptime tracking
	snapshotMetaTracker *SnapshotMetadataTracker // Snapshot metadata tracking

	// Session management for read-your-writes consistency
	sessionMgr *SessionManager

	// Random number generator for this node
	random *rand.Rand

	// Synchronization
	mu         sync.RWMutex
	routinesWg sync.WaitGroup
}

// ApplyMsg is sent to application when log entries are committed
type ApplyMsg struct {
	CommandValid bool
	Command      []byte
	CommandIndex uint64

	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  uint64
	SnapshotIndex uint64
}

// RPC represents an incoming RPC request
type RPC struct {
	Command  interface{}
	RespChan chan RPCResponse
}

// RPCResponse wraps the response and any error
type RPCResponse struct {
	Response interface{}
	Error    error
}

// Observation is used for monitoring Raft state changes
type Observation struct {
	Raft  *Raft
	State NodeState
	Term  uint64
}

// New creates a new Raft node
func New(config *Config, transport Transport, fsm FSM, applyCh chan ApplyMsg) (*Raft, error) {
	if config == nil {
		config = DefaultConfig()
	}

	logger := NewLogger("raft")

	// Create a per-node random number generator with unique seed
	// Use crypto/rand to get truly random bytes for seeding
	var random *rand.Rand
	var seedBytes [8]byte
	_, err := crypto_rand.Read(seedBytes[:])
	if err != nil {
		// Fallback to time-based seed if crypto/rand fails
		seed := time.Now().UnixNano()
		h := uint32(0)
		for _, c := range config.NodeID {
			h = h*31 + uint32(c)
		}
		seed ^= int64(h) * 1000000000
		random = rand.New(rand.NewSource(seed))
	} else {
		// Use crypto random bytes as seed
		seed := int64(seedBytes[0]) | int64(seedBytes[1])<<8 | int64(seedBytes[2])<<16 |
			int64(seedBytes[3])<<24 | int64(seedBytes[4])<<32 | int64(seedBytes[5])<<40 |
			int64(seedBytes[6])<<48 | int64(seedBytes[7])<<56
		random = rand.New(rand.NewSource(seed))
	}

	// Initialize persistence manager (must be first for file operations)
	persistenceMgr := NewPersistenceManager()

	// Ensure data directory exists
	if err := persistenceMgr.EnsureDir(config.DataDir); err != nil {
		persistenceMgr.Shutdown()
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Initialize log with persistence manager
	log, err := NewLogWithPersistence(config.DataDir, persistenceMgr)
	if err != nil {
		persistenceMgr.Shutdown()
		return nil, fmt.Errorf("failed to create log: %w", err)
	}

	// Initialize snapshot store with persistence manager
	snapshotStore := NewSnapshotStoreWithPersistence(config.DataDir, persistenceMgr)

	r := &Raft{
		config:              config,
		logger:              logger,
		currentTerm:         0,
		votedFor:            "",
		log:                 log,
		commitIndex:         0,
		lastApplied:         0,
		state:               Follower,
		nextIndex:           make(map[string]uint64),
		matchIndex:          make(map[string]uint64),
		applyCh:             applyCh,
		rpcCh:               make(chan RPC, 256),
		shutdownCh:          make(chan struct{}),
		peers:               make(map[string]*Peer),
		transport:           transport,
		fsm:                 fsm,
		snapshotStore:       snapshotStore,
		observationsCh:      make(chan Observation, 16),
		persistenceMgr:      persistenceMgr,
		sessionMgr:          NewSessionManager(10 * time.Minute), // 10-minute session timeout
		random:              random,
		startTime:           time.Now(),
		peerLatencyMgr:      NewPeerLatencyTracker(100, logger), // Track last 100 samples per RPC type
		uptimeTracker:       NewUptimeTracker(config.NodeID, config.DataDir, persistenceMgr, logger),
		snapshotMetaTracker: NewSnapshotMetadataTracker(config.NodeID, config.DataDir, config.SnapshotInterval, config.SnapshotThreshold, logger),
	}

	// Load persistent state
	if err := r.loadState(); err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}

	// Restore from snapshot if exists
	if err := r.restoreSnapshot(); err != nil {
		return nil, fmt.Errorf("failed to restore snapshot: %w", err)
	}

	r.logger.Info("Raft node initialized",
		"nodeID", config.NodeID,
		"term", r.currentTerm,
		"lastLogIndex", r.log.LastIndex())

	return r, nil
}

// Start begins the Raft consensus algorithm
func (r *Raft) Start(ctx context.Context) error {
	r.logger.Info("Starting Raft node", "nodeID", r.config.NodeID)

	// Record start event for uptime tracking
	if r.uptimeTracker != nil {
		leaderID, _ := r.GetLeader()
		r.uptimeTracker.RecordStart(r.state, r.currentTerm, leaderID)
	}

	// Start RPC handler
	r.routinesWg.Add(1)
	go r.runRPCHandler(ctx)

	// Start main event loop
	r.routinesWg.Add(1)
	go r.run(ctx)

	// Start log applier
	r.routinesWg.Add(1)
	go r.runApplier(ctx)

	// Start snapshot routine
	r.routinesWg.Add(1)
	go r.runSnapshotter(ctx)

	// Start uptime metrics updater
	r.routinesWg.Add(1)
	go r.runUptimeMetricsUpdater(ctx)

	return nil
}

// Shutdown gracefully shuts down the Raft node
func (r *Raft) Shutdown() error {
	r.logger.Info("Shutting down Raft node")

	// Record stop event for uptime tracking before shutdown
	if r.uptimeTracker != nil {
		r.uptimeTracker.RecordStop("graceful shutdown")
		if err := r.uptimeTracker.Flush(); err != nil {
			r.logger.Error("Failed to flush uptime history", "error", err)
		}
	}

	close(r.shutdownCh)
	r.routinesWg.Wait()

	// Persist final state
	if err := r.persistState(); err != nil {
		r.logger.Error("Failed to persist state during shutdown", "error", err)
	}

	// Shutdown persistence manager
	if r.persistenceMgr != nil {
		r.persistenceMgr.Shutdown()
	}

	return nil
}

// runUptimeMetricsUpdater periodically updates Prometheus metrics for uptime
func (r *Raft) runUptimeMetricsUpdater(ctx context.Context) {
	defer r.routinesWg.Done()

	ticker := time.NewTicker(15 * time.Second) // Update every 15 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.shutdownCh:
			return
		case <-ticker.C:
			r.updateUptimeMetrics()
		}
	}
}

// updateUptimeMetrics updates the Prometheus metrics with current uptime stats
func (r *Raft) updateUptimeMetrics() {
	if r.uptimeTracker == nil {
		return
	}

	// Stats are exported via the uptime tracker and API
	// The Prometheus metrics are updated via the observability package
	// when the GetUptimeStats is called from the RaftAtlas layer
	// This ensures metrics are always consistent with the tracker state
}

// Public accessor methods for integration

// GetState returns the current term and whether this node is the leader
func (r *Raft) GetState() (uint64, bool) {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.currentTerm, r.state == Leader
}

// GetCurrentTerm returns the current term
func (r *Raft) GetCurrentTerm() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.currentTerm
}

// GetLeader returns the current leader ID and address
func (r *Raft) GetLeader() (string, string) {
	// Track the leader ID (simplified - could be enhanced)
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	if r.state == Leader {
		return r.config.NodeID, r.transport.Address()
	}
	return "", ""
}

// GetNodeState returns the current node state
func (r *Raft) GetNodeState() NodeState {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.state
}

// run is the main event loop
func (r *Raft) run(ctx context.Context) {
	defer r.routinesWg.Done()

	// Special case: single-node cluster
	// No peers means we can immediately become leader
	if len(r.peers) == 0 {
		r.logger.Info("Single-node cluster detected, becoming leader immediately")
		r.mu.Lock()
		r.currentTerm = 1
		r.votedFor = r.config.NodeID
		r.mu.Unlock()

		// Persist initial state
		if err := r.persistState(); err != nil {
			r.logger.Error("Failed to persist state", "error", err)
		}

		r.setState(Leader)
		r.resetElectionTimer()

		// Run as leader
		r.runLeader(ctx)
		return
	}

	// Multi-node cluster: add randomized startup delay to prevent all nodes
	// from timing out simultaneously. This is crucial for leader election convergence.
	startupDelay := time.Duration(r.random.Int63n(int64(r.config.ElectionTimeout * 2)))
	time.Sleep(startupDelay)

	r.resetElectionTimer()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.shutdownCh:
			return
		default:
		}

		state := r.getState()

		switch state {
		case Follower:
			r.runFollower(ctx)
		case Candidate:
			r.runCandidate(ctx)
		case Leader:
			r.runLeader(ctx)
		}
	}
}

// runFollower handles follower state
func (r *Raft) runFollower(ctx context.Context) {
	r.logger.Debug("Entering follower state", "term", r.getCurrentTerm())

	for r.getState() == Follower {
		select {
		case <-ctx.Done():
			return
		case <-r.shutdownCh:
			return
		case <-r.electionTimer.C:
			// Election timeout - become candidate
			r.logger.Info("Election timeout, transitioning to candidate")
			r.setState(Candidate)
			return
		case rpc := <-r.rpcCh:
			r.processRPC(rpc)
		}
	}
}

// runCandidate handles candidate state
func (r *Raft) runCandidate(ctx context.Context) {
	r.logger.Info("Entering candidate state", "term", r.getCurrentTerm())

	// Start election
	votesGranted := r.startElection()

	// Check if we won the election
	if votesGranted > len(r.peers)/2 {
		r.logger.Info("Won election", "votes", votesGranted, "term", r.getCurrentTerm())
		r.setState(Leader)
		return
	}

	// Wait for election resolution
	for r.getState() == Candidate {
		select {
		case <-ctx.Done():
			return
		case <-r.shutdownCh:
			return
		case <-r.electionTimer.C:
			// Election timeout - start new election
			r.logger.Info("Election timeout, starting new election")
			votesGranted = r.startElection()
			if votesGranted > len(r.peers)/2 {
				r.setState(Leader)
				return
			}
		case rpc := <-r.rpcCh:
			r.processRPC(rpc)
		}
	}
}

// runLeader handles leader state
func (r *Raft) runLeader(ctx context.Context) {
	r.logger.Info("Entering leader state", "term", r.getCurrentTerm())

	// Initialize leader state
	r.initLeaderState()

	// Send initial heartbeats
	r.sendHeartbeats()

	r.heartbeatTicker = time.NewTicker(r.config.HeartbeatTimeout)
	defer r.heartbeatTicker.Stop()

	for r.getState() == Leader {
		select {
		case <-ctx.Done():
			return
		case <-r.shutdownCh:
			return
		case <-r.heartbeatTicker.C:
			r.sendHeartbeats()
		case rpc := <-r.rpcCh:
			r.processRPC(rpc)
		}
	}
}

// Apply submits a command to the Raft cluster
func (r *Raft) Apply(command []byte, timeout time.Duration) (uint64, uint64, error) {
	if r.getState() != Leader {
		return 0, 0, fmt.Errorf("not leader")
	}

	term := r.getCurrentTerm()
	index := r.log.Append(&LogEntry{
		Term:    term,
		Command: command,
		Type:    LogCommand,
	})

	if err := r.persistState(); err != nil {
		return 0, 0, fmt.Errorf("failed to persist state: %w", err)
	}

	r.logger.Debug("Command appended to log", "index", index, "term", term)

	// Replicate immediately
	r.sendHeartbeats()

	return index, term, nil
}

// AddPeer adds a peer to the cluster
func (r *Raft) AddPeer(id, address string) error {
	if _, exists := r.peers[id]; exists {
		return fmt.Errorf("peer already exists: %s", id)
	}

	peer := &Peer{
		ID:      id,
		Address: address,
	}
	r.peers[id] = peer

	if r.getState() == Leader {
		r.nextIndex[id] = r.log.LastIndex() + 1
		r.matchIndex[id] = 0
	}

	r.logger.Info("Peer added", "peerID", id, "address", address)
	return nil
}

// RemovePeer removes a peer from the cluster
func (r *Raft) RemovePeer(id string) error {
	if _, exists := r.peers[id]; !exists {
		return fmt.Errorf("peer not found: %s", id)
	}

	delete(r.peers, id)
	delete(r.nextIndex, id)
	delete(r.matchIndex, id)

	r.logger.Info("Peer removed", "peerID", id)
	return nil
}

// GetPeers returns a map of all peers in the cluster
func (r *Raft) GetPeers() map[string]*Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to avoid race conditions
	peers := make(map[string]*Peer, len(r.peers))
	for id, peer := range r.peers {
		peers[id] = &Peer{
			ID:      peer.ID,
			Address: peer.Address,
		}
	}
	return peers
}

// Helper functions

func (r *Raft) getState() NodeState {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.state
}

func (r *Raft) setState(state NodeState) {
	r.stateMu.Lock()
	oldState := r.state
	r.state = state
	r.stateMu.Unlock()

	if oldState != state {
		r.logger.Info("State transition", "from", oldState, "to", state, "term", r.getCurrentTerm())

		// Record state change for uptime tracking
		if r.uptimeTracker != nil {
			leaderID, _ := r.GetLeader()
			r.uptimeTracker.RecordStateChange(state, r.getCurrentTerm(), leaderID)
		}

		// Send observation
		select {
		case r.observationsCh <- Observation{
			Raft:  r,
			State: state,
			Term:  r.getCurrentTerm(),
		}:
		default:
		}
	}

	if state == Follower || state == Candidate {
		r.resetElectionTimer()
	}
}

func (r *Raft) getCurrentTerm() uint64 {
	return atomic.LoadUint64(&r.currentTerm)
}

func (r *Raft) setCurrentTerm(term uint64) {
	atomic.StoreUint64(&r.currentTerm, term)
}

func (r *Raft) resetElectionTimer() {
	// Use 4x randomization range to prevent split votes in multi-node clusters
	// This gives us ElectionTimeout to 5*ElectionTimeout (e.g., 150ms to 750ms)
	// Per Raft paper recommendations for leader election stability
	timeout := r.config.ElectionTimeout + time.Duration(r.random.Int63n(int64(r.config.ElectionTimeout*4)))

	r.logger.Debug("Resetting election timer",
		"timeout", timeout,
		"base", r.config.ElectionTimeout)

	if r.electionTimer == nil {
		r.electionTimer = time.NewTimer(timeout)
	} else {
		r.electionTimer.Stop()
		r.electionTimer.Reset(timeout)
	}
}

func (r *Raft) updateLastContact() {
	r.lastContact.Store(time.Now())
}

// Peer represents a remote Raft peer
type Peer struct {
	ID      string
	Address string
}

// ReadConsistent performs a consistent read that respects read-your-writes semantics.
// It ensures that reads see at least the specified minimum index.
func (r *Raft) ReadConsistent(sessionID string, minIndex uint64) error {
	// Get current commit index
	r.mu.RLock()
	commitIndex := r.commitIndex
	state := r.state
	r.mu.RUnlock()

	// If we're the leader and commit index is sufficient, read is safe
	if state == Leader && commitIndex >= minIndex {
		return nil
	}

	// If we're a follower, wait for commit index to catch up
	if commitIndex < minIndex {
		// Wait up to 5 seconds for replication
		timeout := time.After(5 * time.Second)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-timeout:
				return fmt.Errorf("timeout waiting for replication: need index %d, have %d", minIndex, commitIndex)
			case <-ticker.C:
				r.mu.RLock()
				commitIndex = r.commitIndex
				r.mu.RUnlock()

				if commitIndex >= minIndex {
					return nil
				}
			case <-r.shutdownCh:
				return fmt.Errorf("raft shutting down")
			}
		}
	}

	return nil
}

// UpdateSessionAfterWrite updates the session index after a write operation.
func (r *Raft) UpdateSessionAfterWrite(sessionID string, index uint64) {
	if r.sessionMgr != nil {
		r.sessionMgr.UpdateSessionIndex(sessionID, index)
	}
}

// GetSessionIndex retrieves the last applied index for a session.
func (r *Raft) GetSessionIndex(sessionID string) uint64 {
	if r.sessionMgr != nil {
		return r.sessionMgr.GetSessionIndex(sessionID)
	}
	return 0
}

// GetSessionManager returns the session manager (for monitoring/testing).
func (r *Raft) GetSessionManager() *SessionManager {
	return r.sessionMgr
}

// GetNodeID returns the node ID
func (r *Raft) GetNodeID() string {
	return r.config.NodeID
}

// GetCommitIndex returns the current commit index
func (r *Raft) GetCommitIndex() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.commitIndex
}

// GetLastApplied returns the last applied index
func (r *Raft) GetLastApplied() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastApplied
}

// GetLastLogInfo returns the index and term of the last log entry
func (r *Raft) GetLastLogInfo() (uint64, uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.log.LastIndex(), r.log.LastTerm()
}

// GetTLSConfig returns the TLS configuration
func (r *Raft) GetTLSConfig() *TLSConfig {
	return r.config.TLS
}

// GetUptime returns the time since the node was started
func (r *Raft) GetUptime() time.Duration {
	// Store startTime in Raft struct when it's created
	if r.startTime.IsZero() {
		return 0
	}
	return time.Since(r.startTime)
}

// GetMatchIndex returns the match index for a specific peer
func (r *Raft) GetMatchIndex(peerID string) uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.matchIndex[peerID]
}

// GetNextIndex returns the next index for a specific peer
func (r *Raft) GetNextIndex(peerID string) uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nextIndex[peerID]
}

// GetPeerLatencyStats returns latency statistics for a specific peer
func (r *Raft) GetPeerLatencyStats(peerID string) (PeerLatencyInfo, bool) {
	if r.peerLatencyMgr == nil {
		return PeerLatencyInfo{}, false
	}
	return r.peerLatencyMgr.GetPeerStats(peerID)
}

// GetAllPeerLatencyStats returns latency statistics for all peers
func (r *Raft) GetAllPeerLatencyStats() map[string]PeerLatencyInfo {
	if r.peerLatencyMgr == nil {
		return nil
	}
	return r.peerLatencyMgr.GetAllPeerStats()
}

// GetAggregatedLatencyStats returns aggregated latency statistics across all peers
func (r *Raft) GetAggregatedLatencyStats() LatencyStatsJSON {
	if r.peerLatencyMgr == nil {
		return LatencyStatsJSON{}
	}
	return r.peerLatencyMgr.GetAggregatedStats()
}

// GetPeerHealthSummary returns a summary of healthy vs unhealthy peers
func (r *Raft) GetPeerHealthSummary() (healthy int, unhealthy int, total int) {
	if r.peerLatencyMgr == nil {
		return 0, 0, 0
	}
	return r.peerLatencyMgr.GetHealthySummary()
}

// ResetPeerLatencyStats resets all peer latency statistics
func (r *Raft) ResetPeerLatencyStats() {
	if r.peerLatencyMgr != nil {
		r.peerLatencyMgr.Reset()
	}
}

// GetUptimeStats returns current uptime statistics
func (r *Raft) GetUptimeStats() UptimeStats {
	if r.uptimeTracker == nil {
		return UptimeStats{}
	}
	return r.uptimeTracker.GetStats()
}

// GetUptimeStatsJSON returns uptime statistics in JSON-serializable format
func (r *Raft) GetUptimeStatsJSON() UptimeStatsJSON {
	if r.uptimeTracker == nil {
		return UptimeStatsJSON{}
	}
	return r.uptimeTracker.GetStatsJSON()
}

// GetUptimeHistory returns the complete uptime history
func (r *Raft) GetUptimeHistory(maxEvents, maxSessions int) UptimeHistoryJSON {
	if r.uptimeTracker == nil {
		return UptimeHistoryJSON{}
	}
	return r.uptimeTracker.GetHistory(maxEvents, maxSessions)
}

// GetCurrentSessionUptime returns the current session uptime duration
func (r *Raft) GetCurrentSessionUptime() time.Duration {
	if r.uptimeTracker == nil {
		return time.Since(r.startTime) // Fallback to simple uptime
	}
	return r.uptimeTracker.GetCurrentUptime()
}

// ResetUptimeHistory resets all uptime history (for testing)
func (r *Raft) ResetUptimeHistory() {
	if r.uptimeTracker != nil {
		r.uptimeTracker.Reset()
	}
}

// GetSnapshotStats returns current snapshot statistics
func (r *Raft) GetSnapshotStats() *SnapshotStats {
	if r.snapshotMetaTracker == nil {
		return nil
	}
	return r.snapshotMetaTracker.GetStats()
}

// GetSnapshotStatsJSON returns snapshot statistics in JSON-serializable format
func (r *Raft) GetSnapshotStatsJSON() *SnapshotStatsJSON {
	if r.snapshotMetaTracker == nil {
		return nil
	}
	return r.snapshotMetaTracker.GetStatsJSON()
}

// GetSnapshotHistory returns the complete snapshot history
func (r *Raft) GetSnapshotHistory(maxEvents, maxSnapshots int) *SnapshotHistory {
	if r.snapshotMetaTracker == nil {
		return nil
	}
	return r.snapshotMetaTracker.GetHistory(maxEvents, maxSnapshots)
}

// GetLatestSnapshotMetadata returns metadata about the latest snapshot
func (r *Raft) GetLatestSnapshotMetadata() *SnapshotMetadata {
	if r.snapshotMetaTracker == nil {
		return nil
	}
	return r.snapshotMetaTracker.GetLatestSnapshot()
}

// GetAllSnapshotMetadata returns metadata for all tracked snapshots
func (r *Raft) GetAllSnapshotMetadata() []SnapshotMetadata {
	if r.snapshotMetaTracker == nil {
		return nil
	}
	return r.snapshotMetaTracker.GetAllSnapshots()
}

// ResetSnapshotStats resets all snapshot statistics (for testing)
func (r *Raft) ResetSnapshotStats() {
	if r.snapshotMetaTracker != nil {
		r.snapshotMetaTracker.Reset()
	}
}
