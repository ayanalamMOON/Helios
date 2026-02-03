package raft

import (
	"context"
	"time"
)

// runApplier applies committed log entries to the FSM
func (r *Raft) runApplier(ctx context.Context) {
	defer r.routinesWg.Done()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.shutdownCh:
			return
		case <-ticker.C:
			r.applyCommitted()
		}
	}
}

// applyCommitted applies all committed but not yet applied entries
func (r *Raft) applyCommitted() {
	r.mu.Lock()
	commitIndex := r.commitIndex
	lastApplied := r.lastApplied
	r.mu.Unlock()

	if commitIndex <= lastApplied {
		return
	}

	// Apply entries from lastApplied+1 to commitIndex
	for index := lastApplied + 1; index <= commitIndex; index++ {
		entry, err := r.log.Get(index)
		if err != nil {
			r.logger.Error("Failed to get log entry", "index", index, "error", err)
			break
		}

		// Skip no-op entries
		if entry.Type == LogNoop {
			r.mu.Lock()
			r.lastApplied = index
			r.mu.Unlock()
			continue
		}

		// Apply to FSM
		if r.fsm != nil {
			r.fsm.Apply(entry.Command)
		}

		// Send to apply channel
		msg := ApplyMsg{
			CommandValid: true,
			Command:      entry.Command,
			CommandIndex: entry.Index,
		}

		select {
		case r.applyCh <- msg:
			r.mu.Lock()
			r.lastApplied = index
			r.mu.Unlock()

			r.logger.Debug("Applied entry", "index", index)
		case <-r.shutdownCh:
			return
		}
	}
}

// runSnapshotter periodically takes snapshots
func (r *Raft) runSnapshotter(ctx context.Context) {
	defer r.routinesWg.Done()

	ticker := time.NewTicker(r.config.SnapshotInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.shutdownCh:
			return
		case <-ticker.C:
			r.checkSnapshot()
		}
	}
}

// checkSnapshot checks if a snapshot should be taken
func (r *Raft) checkSnapshot() {
	r.mu.RLock()
	lastApplied := r.lastApplied
	r.mu.RUnlock()

	logSize := r.log.Size()

	// Take snapshot if log size exceeds threshold
	if uint64(logSize) > r.config.SnapshotThreshold {
		r.logger.Info("Log size exceeds threshold, taking snapshot",
			"logSize", logSize,
			"threshold", r.config.SnapshotThreshold,
			"lastApplied", lastApplied)

		if err := r.takeSnapshot(); err != nil {
			r.logger.Error("Failed to take snapshot", "error", err)
		}
	}
}

// sendInstallSnapshot sends a snapshot to a peer that is too far behind
func (r *Raft) sendInstallSnapshot(peer *Peer) {
	// Get latest snapshot
	meta, data, err := r.snapshotStore.GetLatest()
	if err != nil {
		r.logger.Error("Failed to get snapshot", "error", err)
		return
	}

	if meta == nil {
		r.logger.Debug("No snapshot available to send", "peer", peer.ID)
		return
	}

	r.logger.Info("Sending snapshot to peer",
		"peer", peer.ID,
		"index", meta.Index,
		"term", meta.Term,
		"size", len(data))

	req := &InstallSnapshotRequest{
		Term:              r.getCurrentTerm(),
		LeaderID:          r.config.NodeID,
		LastIncludedIndex: meta.Index,
		LastIncludedTerm:  meta.Term,
		Data:              data,
	}

	// Track latency for InstallSnapshot RPC
	startTime := time.Now()
	resp, err := r.transport.InstallSnapshot(peer.Address, req)
	latency := time.Since(startTime)

	if err != nil {
		r.logger.Error("InstallSnapshot RPC failed", "peer", peer.ID, "error", err)
		// Record error in latency tracker
		if r.peerLatencyMgr != nil {
			r.peerLatencyMgr.RecordError(peer.ID, peer.Address, RPCTypeInstallSnapshot)
		}
		return
	}

	// Record successful latency
	if r.peerLatencyMgr != nil {
		r.peerLatencyMgr.RecordLatency(peer.ID, peer.Address, RPCTypeInstallSnapshot, latency)
	}

	// Check if we need to step down
	if resp.Term > r.getCurrentTerm() {
		r.logger.Info("Discovered higher term in InstallSnapshot response", "term", resp.Term)
		r.setCurrentTerm(resp.Term)
		r.votedFor = ""
		r.setState(Follower)
		return
	}

	// Update peer's nextIndex and matchIndex
	r.nextIndex[peer.ID] = meta.Index + 1
	r.matchIndex[peer.ID] = meta.Index

	r.logger.Info("Snapshot sent successfully", "peer", peer.ID)
}
