package raft

import (
	"time"
)

// NodeState represents the current state of a Raft node
type NodeState int

const (
	Follower NodeState = iota
	Candidate
	Leader
)

func (s NodeState) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Unknown"
	}
}

// LogEntry represents a single entry in the Raft log
type LogEntry struct {
	Index   uint64 // Position in the log
	Term    uint64 // Election term when entry was received
	Command []byte // State machine command
	Type    LogType
}

// LogType indicates the type of log entry
type LogType int

const (
	LogCommand       LogType = iota // Normal command
	LogConfiguration                // Configuration change
	LogNoop                         // No-op entry (used by new leaders)
)

// AppendEntriesRequest is the RPC request for log replication
type AppendEntriesRequest struct {
	Term         uint64     // Leader's term
	LeaderID     string     // Leader's ID for followers to redirect clients
	PrevLogIndex uint64     // Index of log entry immediately preceding new ones
	PrevLogTerm  uint64     // Term of prevLogIndex entry
	Entries      []LogEntry // Log entries to store (empty for heartbeat)
	LeaderCommit uint64     // Leader's commitIndex
}

// AppendEntriesResponse is the RPC response for log replication
type AppendEntriesResponse struct {
	Term          uint64 // Current term for leader to update itself
	Success       bool   // True if follower contained entry matching prevLogIndex and prevLogTerm
	ConflictIndex uint64 // Index of first entry with ConflictTerm (if any)
	ConflictTerm  uint64 // Term of conflicting entry at ConflictIndex (if any)
}

// RequestVoteRequest is the RPC request for voting
type RequestVoteRequest struct {
	Term         uint64 // Candidate's term
	CandidateID  string // Candidate requesting vote
	LastLogIndex uint64 // Index of candidate's last log entry
	LastLogTerm  uint64 // Term of candidate's last log entry
}

// RequestVoteResponse is the RPC response for voting
type RequestVoteResponse struct {
	Term        uint64 // Current term for candidate to update itself
	VoteGranted bool   // True means candidate received vote
}

// InstallSnapshotRequest is the RPC request for snapshot installation
type InstallSnapshotRequest struct {
	Term              uint64 // Leader's term
	LeaderID          string // Leader's ID
	LastIncludedIndex uint64 // Snapshot replaces all entries up through and including this index
	LastIncludedTerm  uint64 // Term of lastIncludedIndex
	Offset            uint64 // Byte offset where chunk is positioned in the snapshot file
	Data              []byte // Raw bytes of the snapshot chunk, starting at offset
	Done              bool   // True if this is the last chunk
}

// InstallSnapshotResponse is the RPC response for snapshot installation
type InstallSnapshotResponse struct {
	Term uint64 // Current term for leader to update itself
}

// Config holds Raft node configuration
type Config struct {
	NodeID              string        // Unique identifier for this node
	ListenAddr          string        // Address to listen on for RPC
	DataDir             string        // Directory for persistent state
	HeartbeatTimeout    time.Duration // Heartbeat interval for leader
	ElectionTimeout     time.Duration // Base election timeout
	SnapshotInterval    time.Duration // How often to take snapshots
	SnapshotThreshold   uint64        // Number of log entries before snapshot
	MaxEntriesPerAppend int           // Maximum entries to send in one AppendEntries
}

// DefaultConfig returns a configuration with production-ready defaults
func DefaultConfig() *Config {
	return &Config{
		HeartbeatTimeout:    100 * time.Millisecond,
		ElectionTimeout:     500 * time.Millisecond, // Increased to 500ms for reliable leader convergence
		SnapshotInterval:    5 * time.Minute,
		SnapshotThreshold:   10000,
		MaxEntriesPerAppend: 100,
	}
}

// PeerInfo contains information about a peer node
type PeerInfo struct {
	ID      string
	Address string
}

// ClusterConfig represents the current cluster membership
type ClusterConfig struct {
	Peers []PeerInfo
}
