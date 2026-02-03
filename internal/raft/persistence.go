package raft

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PersistentState represents the persistent state that must be saved
type PersistentState struct {
	CurrentTerm uint64 `json:"current_term"`
	VotedFor    string `json:"voted_for"`
}

// persistState saves the persistent state to disk
func (r *Raft) persistState() error {
	r.mu.RLock()
	state := PersistentState{
		CurrentTerm: r.currentTerm,
		VotedFor:    r.votedFor,
	}
	r.mu.RUnlock()

	return r.persistStateUnlocked(state)
}

// persistStateUnlocked persists the given state without locking
// Caller must ensure thread safety
func (r *Raft) persistStateUnlocked(state PersistentState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	statePath := filepath.Join(r.config.DataDir, "raft-state.json")

	// Use persistence manager for serialized write
	if err := r.persistenceMgr.WriteFile("state", statePath, data); err != nil {
		return err
	}

	// Persist log
	if err := r.log.Sync(); err != nil {
		return fmt.Errorf("failed to persist log: %w", err)
	}

	return nil
}

// loadState loads the persistent state from disk
func (r *Raft) loadState() error {
	statePath := filepath.Join(r.config.DataDir, "raft-state.json")

	// If file doesn't exist, use defaults
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		r.currentTerm = 0
		r.votedFor = ""
		return nil
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		return fmt.Errorf("failed to read state: %w", err)
	}

	var state PersistentState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	r.currentTerm = state.CurrentTerm
	r.votedFor = state.VotedFor

	return nil
}

// SnapshotStore manages snapshots
type SnapshotStore struct {
	mu sync.Mutex

	dataDir        string
	persistenceMgr *PersistenceManager
}

// NewSnapshotStore creates a new snapshot store (deprecated)
func NewSnapshotStore(dataDir string) *SnapshotStore {
	return &SnapshotStore{
		dataDir: dataDir,
	}
}

// NewSnapshotStoreWithPersistence creates a new snapshot store with persistence manager
func NewSnapshotStoreWithPersistence(dataDir string, persistenceMgr *PersistenceManager) *SnapshotStore {
	return &SnapshotStore{
		dataDir:        dataDir,
		persistenceMgr: persistenceMgr,
	}
}

// SnapshotMeta contains snapshot metadata
type SnapshotMeta struct {
	Index uint64 `json:"index"`
	Term  uint64 `json:"term"`
	Size  int64  `json:"size"`
}

// Create creates a new snapshot
func (s *SnapshotStore) Create(index, term uint64, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create snapshot metadata
	meta := SnapshotMeta{
		Index: index,
		Term:  term,
		Size:  int64(len(data)),
	}

	// Save metadata
	metaPath := filepath.Join(s.dataDir, fmt.Sprintf("snapshot-%d-%d.meta", term, index))
	metaData, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot meta: %w", err)
	}

	// Use persistence manager if available
	if s.persistenceMgr != nil {
		if err := s.persistenceMgr.WriteFile("snapshot-meta", metaPath, metaData); err != nil {
			return err
		}
	} else {
		if err := os.WriteFile(metaPath, metaData, 0644); err != nil {
			return fmt.Errorf("failed to write snapshot meta: %w", err)
		}
	}

	// Save snapshot data
	dataPath := filepath.Join(s.dataDir, fmt.Sprintf("snapshot-%d-%d.dat", term, index))
	if s.persistenceMgr != nil {
		if err := s.persistenceMgr.WriteFile("snapshot-data", dataPath, data); err != nil {
			return err
		}
	} else {
		if err := os.WriteFile(dataPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write snapshot data: %w", err)
		}
	}

	return nil
}

// GetLatest retrieves the latest snapshot
func (s *SnapshotStore) GetLatest() (*SnapshotMeta, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find all snapshot metadata files
	pattern := filepath.Join(s.dataDir, "snapshot-*.meta")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list snapshots: %w", err)
	}

	if len(matches) == 0 {
		return nil, nil, nil // No snapshots
	}

	// Find latest snapshot
	var latestMeta *SnapshotMeta
	var latestMetaPath string

	for _, metaPath := range matches {
		metaData, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		var meta SnapshotMeta
		if err := json.Unmarshal(metaData, &meta); err != nil {
			continue
		}

		if latestMeta == nil || meta.Index > latestMeta.Index {
			latestMeta = &meta
			latestMetaPath = metaPath
		}
	}

	if latestMeta == nil {
		return nil, nil, nil
	}

	// Read snapshot data
	dataPath := latestMetaPath[:len(latestMetaPath)-5] + ".dat" // Replace .meta with .dat
	data, err := os.ReadFile(dataPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read snapshot data: %w", err)
	}

	return latestMeta, data, nil
}

// Restore applies a snapshot
func (s *SnapshotStore) Restore(index, term uint64, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Simply create the snapshot - the Raft instance will handle application
	return s.Create(index, term, data)
}

// List returns all available snapshots
func (s *SnapshotStore) List() ([]SnapshotMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pattern := filepath.Join(s.dataDir, "snapshot-*.meta")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}

	snapshots := make([]SnapshotMeta, 0, len(matches))

	for _, metaPath := range matches {
		metaData, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		var meta SnapshotMeta
		if err := json.Unmarshal(metaData, &meta); err != nil {
			continue
		}

		snapshots = append(snapshots, meta)
	}

	return snapshots, nil
}

// DeleteOldSnapshots removes snapshots older than the given index
func (s *SnapshotStore) DeleteOldSnapshots(minIndex uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshots, err := s.List()
	if err != nil {
		return err
	}

	for _, snap := range snapshots {
		if snap.Index < minIndex {
			metaPath := filepath.Join(s.dataDir, fmt.Sprintf("snapshot-%d-%d.meta", snap.Term, snap.Index))
			dataPath := filepath.Join(s.dataDir, fmt.Sprintf("snapshot-%d-%d.dat", snap.Term, snap.Index))

			os.Remove(metaPath)
			os.Remove(dataPath)
		}
	}

	return nil
}

// restoreSnapshot restores the latest snapshot
func (r *Raft) restoreSnapshot() error {
	meta, data, err := r.snapshotStore.GetLatest()
	if err != nil {
		return fmt.Errorf("failed to get latest snapshot: %w", err)
	}

	if meta == nil {
		return nil // No snapshot to restore
	}

	startTime := time.Now()
	r.logger.Info("Restoring snapshot", "index", meta.Index, "term", meta.Term, "size", meta.Size)

	// Apply snapshot to state machine
	if r.fsm != nil {
		if err := r.fsm.Restore(data); err != nil {
			if r.snapshotMetaTracker != nil {
				r.snapshotMetaTracker.RecordSnapshotError("restore", meta.Index, meta.Term, err)
			}
			return fmt.Errorf("failed to restore FSM: %w", err)
		}
	}

	// Update Raft state
	r.mu.Lock()
	r.lastApplied = meta.Index
	r.commitIndex = meta.Index
	r.mu.Unlock()

	// Compact log
	if err := r.log.Compact(meta.Index, data); err != nil {
		return fmt.Errorf("failed to compact log: %w", err)
	}

	// Record successful restoration
	if r.snapshotMetaTracker != nil {
		duration := time.Since(startTime)
		r.snapshotMetaTracker.RecordSnapshotRestored(meta.Index, meta.Term, duration)

		// Also sync with existing snapshots on disk
		snapshots, _ := r.snapshotStore.List()
		r.snapshotMetaTracker.UpdateFromSnapshotStore(snapshots)
	}

	r.logger.Info("Snapshot restored", "index", meta.Index, "duration", time.Since(startTime))

	return nil
}

// takeSnapshot creates a snapshot of the current state
func (r *Raft) takeSnapshot() error {
	startTime := time.Now()

	// Get current state
	r.mu.RLock()
	lastApplied := r.lastApplied
	lastTerm := r.log.GetTerm(lastApplied)
	logSize := r.log.Size()
	r.mu.RUnlock()

	if lastApplied == 0 {
		return nil // Nothing to snapshot
	}

	r.logger.Info("Taking snapshot", "index", lastApplied, "term", lastTerm)

	// Get snapshot from FSM
	var snapshot []byte
	var err error

	if r.fsm != nil {
		snapshot, err = r.fsm.Snapshot()
		if err != nil {
			// Record error
			if r.snapshotMetaTracker != nil {
				r.snapshotMetaTracker.RecordSnapshotError("create", lastApplied, lastTerm, err)
			}
			return fmt.Errorf("failed to get FSM snapshot: %w", err)
		}
	}

	// Save snapshot
	if err := r.snapshotStore.Create(lastApplied, lastTerm, snapshot); err != nil {
		// Record error
		if r.snapshotMetaTracker != nil {
			r.snapshotMetaTracker.RecordSnapshotError("create", lastApplied, lastTerm, err)
		}
		return fmt.Errorf("failed to create snapshot: %w", err)
	}

	// Calculate entries compacted
	entriesCompacted := uint64(logSize)

	// Compact log
	if err := r.log.Compact(lastApplied, snapshot); err != nil {
		return fmt.Errorf("failed to compact log: %w", err)
	}

	// Record successful snapshot creation
	if r.snapshotMetaTracker != nil {
		// Get peer list
		peers := make([]string, 0, len(r.peers))
		r.mu.RLock()
		for peerID := range r.peers {
			peers = append(peers, peerID)
		}
		leaderID, _ := r.GetLeader()
		r.mu.RUnlock()

		duration := time.Since(startTime)
		r.snapshotMetaTracker.RecordSnapshotCreated(
			lastApplied, lastTerm, int64(len(snapshot)), duration, entriesCompacted, leaderID, peers,
		)
	}

	// Delete old snapshots (keep last 2)
	if err := r.snapshotStore.DeleteOldSnapshots(lastApplied - 1); err != nil {
		r.logger.Warn("Failed to delete old snapshots", "error", err)
	}

	r.logger.Info("Snapshot complete", "index", lastApplied, "size", len(snapshot), "duration", time.Since(startTime))

	return nil
}

// handleInstallSnapshot processes an InstallSnapshot RPC
func (r *Raft) handleInstallSnapshot(req *InstallSnapshotRequest) *InstallSnapshotResponse {
	startTime := time.Now()

	resp := &InstallSnapshotResponse{
		Term: r.getCurrentTerm(),
	}

	// If leader's term is less than ours, reject
	if req.Term < r.getCurrentTerm() {
		return resp
	}

	// Update term if higher
	if req.Term > r.getCurrentTerm() {
		r.setCurrentTerm(req.Term)
		r.votedFor = ""
		r.setState(Follower)
	}

	r.updateLastContact()
	r.resetElectionTimer()

	r.logger.Info("Receiving snapshot", "index", req.LastIncludedIndex, "term", req.LastIncludedTerm, "size", len(req.Data))

	// Save snapshot
	if err := r.snapshotStore.Create(req.LastIncludedIndex, req.LastIncludedTerm, req.Data); err != nil {
		r.logger.Error("Failed to save snapshot", "error", err)
		if r.snapshotMetaTracker != nil {
			r.snapshotMetaTracker.RecordSnapshotError("receive", req.LastIncludedIndex, req.LastIncludedTerm, err)
		}
		return resp
	}

	// Apply snapshot to FSM
	if r.fsm != nil {
		if err := r.fsm.Restore(req.Data); err != nil {
			r.logger.Error("Failed to restore FSM from snapshot", "error", err)
			if r.snapshotMetaTracker != nil {
				r.snapshotMetaTracker.RecordSnapshotError("restore", req.LastIncludedIndex, req.LastIncludedTerm, err)
			}
			return resp
		}
	}

	// Update state
	r.mu.Lock()
	r.lastApplied = req.LastIncludedIndex
	r.commitIndex = req.LastIncludedIndex
	r.mu.Unlock()

	// Compact log
	if err := r.log.Compact(req.LastIncludedIndex, req.Data); err != nil {
		r.logger.Error("Failed to compact log", "error", err)
	}

	// Record successful snapshot receipt
	if r.snapshotMetaTracker != nil {
		duration := time.Since(startTime)
		r.snapshotMetaTracker.RecordSnapshotReceived(
			req.LastIncludedIndex, req.LastIncludedTerm, int64(len(req.Data)), duration, req.LeaderID,
		)
	}

	r.logger.Info("Snapshot installed", "index", req.LastIncludedIndex, "duration", time.Since(startTime))

	return resp
}
