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
	lastContact atomic.Value // time.Time of last contact from leader

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
		config:         config,
		logger:         logger,
		currentTerm:    0,
		votedFor:       "",
		log:            log,
		commitIndex:    0,
		lastApplied:    0,
		state:          Follower,
		nextIndex:      make(map[string]uint64),
		matchIndex:     make(map[string]uint64),
		applyCh:        applyCh,
		rpcCh:          make(chan RPC, 256),
		shutdownCh:     make(chan struct{}),
		peers:          make(map[string]*Peer),
		transport:      transport,
		fsm:            fsm,
		snapshotStore:  snapshotStore,
		observationsCh: make(chan Observation, 16),
		persistenceMgr: persistenceMgr,
		random:         random,
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

	return nil
}

// Shutdown gracefully shuts down the Raft node
func (r *Raft) Shutdown() error {
	r.logger.Info("Shutting down Raft node")
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
