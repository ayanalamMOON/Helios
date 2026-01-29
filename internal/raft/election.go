package raft

import (
	"context"
	"fmt"
	"time"
)

// startElection initiates a leader election
func (r *Raft) startElection() int {
	// Increment current term
	term := r.getCurrentTerm() + 1
	r.setCurrentTerm(term)

	// Vote for self
	r.votedFor = r.config.NodeID

	// Persist state
	if err := r.persistState(); err != nil {
		r.logger.Error("Failed to persist state during election", "error", err)
		return 0
	}

	// Reset election timer
	r.resetElectionTimer()

	r.logger.Info("Starting election", "term", term)

	// Request votes from all peers
	lastLogIndex := r.log.LastIndex()
	lastLogTerm := r.log.GetTerm(lastLogIndex)

	req := &RequestVoteRequest{
		Term:         term,
		CandidateID:  r.config.NodeID,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  lastLogTerm,
	}

	// Start with one vote (self)
	votesGranted := 1

	// Send requests to all peers in parallel
	responseCh := make(chan *RequestVoteResponse, len(r.peers))

	for _, peer := range r.peers {
		go func(p *Peer) {
			resp, err := r.transport.RequestVote(p.Address, req)
			if err != nil {
				r.logger.Debug("RequestVote RPC failed", "peer", p.ID, "error", err)
				return
			}
			responseCh <- resp
		}(peer)
	}

	// Collect responses with timeout
	// Important: Also process incoming RPCs while waiting, otherwise deadlock can occur
	timeout := time.After(r.config.HeartbeatTimeout * 2) // Give nodes time to respond
	for i := 0; i < len(r.peers); i++ {
		select {
		case resp := <-responseCh:
			if resp.Term > term {
				// Discovered higher term, step down
				r.logger.Info("Discovered higher term during election", "term", resp.Term)
				r.setCurrentTerm(resp.Term)
				r.votedFor = ""
				r.setState(Follower)
				return 0
			}

			if resp.VoteGranted {
				votesGranted++
				r.logger.Debug("Vote granted", "votes", votesGranted, "term", term)

				// Check if we have majority
				if votesGranted > len(r.peers)/2 {
					r.logger.Info("Achieved majority", "votes", votesGranted, "required", len(r.peers)/2+1)
					return votesGranted
				}
			} else {
				r.logger.Debug("Vote denied", "term", term)
			}
		case rpc := <-r.rpcCh:
			// Process incoming RPCs while waiting for vote responses
			// This prevents deadlock when multiple nodes are candidates
			r.logger.Debug("Processing RPC during election", "rpcType", fmt.Sprintf("%T", rpc.Command))
			r.processRPC(rpc)
		case <-timeout:
			// Don't wait forever for responses
			r.logger.Debug("Election timeout waiting for responses", "votesGranted", votesGranted)
			return votesGranted
		case <-r.shutdownCh:
			return 0
		}
	}

	return votesGranted
}

// handleRequestVote processes a RequestVote RPC
func (r *Raft) handleRequestVote(req *RequestVoteRequest) *RequestVoteResponse {
	// Lock to ensure only one vote per term
	r.mu.Lock()
	defer r.mu.Unlock()

	resp := &RequestVoteResponse{
		Term:        r.getCurrentTerm(),
		VoteGranted: false,
	}

	// If candidate's term is less than ours, reject
	if req.Term < r.getCurrentTerm() {
		r.logger.Debug("Rejecting vote - lower term", "reqTerm", req.Term, "currentTerm", r.getCurrentTerm())
		return resp
	}

	// If we are a leader with the same term, reject the vote
	// A leader has already won an election for this term
	if req.Term == r.getCurrentTerm() && r.getState() == Leader {
		r.logger.Debug("Rejecting vote - we are leader for this term",
			"term", req.Term,
			"candidate", req.CandidateID)
		return resp
	}

	// If candidate's term is higher, update our term
	if req.Term > r.getCurrentTerm() {
		r.logger.Info("Discovered higher term in RequestVote", "term", req.Term)
		r.setCurrentTerm(req.Term)
		r.votedFor = ""
		r.setState(Follower)
	}

	// If we haven't voted or already voted for this candidate
	if r.votedFor == "" || r.votedFor == req.CandidateID {
		// Check if candidate's log is at least as up-to-date as ours
		lastLogIndex := r.log.LastIndex()
		lastLogTerm := r.log.GetTerm(lastLogIndex)

		logUpToDate := req.LastLogTerm > lastLogTerm ||
			(req.LastLogTerm == lastLogTerm && req.LastLogIndex >= lastLogIndex)

		if logUpToDate {
			r.votedFor = req.CandidateID
			resp.VoteGranted = true
			r.updateLastContact()
			r.resetElectionTimer()

			// Persist vote (use unlocked version since we're already holding the mutex)
			state := PersistentState{
				CurrentTerm: r.getCurrentTerm(),
				VotedFor:    r.votedFor,
			}
			if err := r.persistStateUnlocked(state); err != nil {
				r.logger.Error("Failed to persist vote", "error", err)
			}

			r.logger.Info("Granted vote", "candidate", req.CandidateID, "term", req.Term)
		} else {
			r.logger.Debug("Rejecting vote - log not up-to-date",
				"candidate", req.CandidateID,
				"candLastLogTerm", req.LastLogTerm,
				"candLastLogIndex", req.LastLogIndex,
				"ourLastLogTerm", lastLogTerm,
				"ourLastLogIndex", lastLogIndex)
		}
	} else {
		r.logger.Debug("Rejecting vote - already voted", "votedFor", r.votedFor, "candidate", req.CandidateID, "term", req.Term)
	}

	resp.Term = r.getCurrentTerm()
	return resp
}

// initLeaderState initializes leader-specific state
func (r *Raft) initLeaderState() {
	lastLogIndex := r.log.LastIndex()

	for peerID := range r.peers {
		r.nextIndex[peerID] = lastLogIndex + 1
		r.matchIndex[peerID] = 0
	}

	// Append a no-op entry to commit entries from previous terms
	noopEntry := &LogEntry{
		Term:    r.getCurrentTerm(),
		Command: nil,
		Type:    LogNoop,
	}
	r.log.Append(noopEntry)

	if err := r.persistState(); err != nil {
		r.logger.Error("Failed to persist noop entry", "error", err)
	}
}

// sendHeartbeats sends AppendEntries RPCs to all peers
func (r *Raft) sendHeartbeats() {
	for _, peer := range r.peers {
		go r.replicateToPeer(peer)
	}
}

// replicateToPeer sends log entries to a specific peer
func (r *Raft) replicateToPeer(peer *Peer) {
	if r.getState() != Leader {
		return
	}

	nextIdx := r.nextIndex[peer.ID]
	prevLogIndex := nextIdx - 1
	prevLogTerm := r.log.GetTerm(prevLogIndex)

	// Get entries to send
	entries := r.log.GetEntriesFrom(nextIdx, r.config.MaxEntriesPerAppend)

	req := &AppendEntriesRequest{
		Term:         r.getCurrentTerm(),
		LeaderID:     r.config.NodeID,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      entries,
		LeaderCommit: r.commitIndex,
	}

	resp, err := r.transport.AppendEntries(peer.Address, req)
	if err != nil {
		r.logger.Debug("AppendEntries RPC failed", "peer", peer.ID, "error", err)
		return
	}

	// Process response
	r.handleAppendEntriesResponse(peer.ID, req, resp)
}

// handleAppendEntriesResponse processes AppendEntries RPC response
func (r *Raft) handleAppendEntriesResponse(peerID string, req *AppendEntriesRequest, resp *AppendEntriesResponse) {
	// If we're no longer leader, ignore
	if r.getState() != Leader {
		return
	}

	// If response contains higher term, step down
	if resp.Term > r.getCurrentTerm() {
		r.logger.Info("Discovered higher term in AppendEntries response", "term", resp.Term)
		r.setCurrentTerm(resp.Term)
		r.votedFor = ""
		r.setState(Follower)
		return
	}

	if resp.Success {
		// Update nextIndex and matchIndex
		if len(req.Entries) > 0 {
			lastIndex := req.Entries[len(req.Entries)-1].Index
			r.nextIndex[peerID] = lastIndex + 1
			r.matchIndex[peerID] = lastIndex

			r.logger.Debug("Log replication successful",
				"peer", peerID,
				"matchIndex", lastIndex)

			// Try to advance commit index
			r.advanceCommitIndex()
		}
	} else {
		// Log inconsistency - decrement nextIndex and retry
		if resp.ConflictTerm > 0 {
			// Optimization: skip to the end of the conflicting term
			conflictIndex := r.log.FindLastEntryWithTerm(resp.ConflictTerm)
			if conflictIndex > 0 {
				r.nextIndex[peerID] = conflictIndex + 1
			} else {
				r.nextIndex[peerID] = resp.ConflictIndex
			}
		} else {
			// Simple backtrack
			if r.nextIndex[peerID] > 1 {
				r.nextIndex[peerID]--
			}
		}

		r.logger.Debug("Log inconsistency detected",
			"peer", peerID,
			"newNextIndex", r.nextIndex[peerID])

		// Retry immediately
		go r.replicateToPeer(r.peers[peerID])
	}
}

// handleAppendEntries processes an AppendEntries RPC
func (r *Raft) handleAppendEntries(req *AppendEntriesRequest) *AppendEntriesResponse {
	resp := &AppendEntriesResponse{
		Term:    r.getCurrentTerm(),
		Success: false,
	}

	// If leader's term is less than ours, reject
	if req.Term < r.getCurrentTerm() {
		r.logger.Debug("Rejecting AppendEntries - lower term",
			"reqTerm", req.Term,
			"currentTerm", r.getCurrentTerm())
		return resp
	}

	// If we are a leader with the same term, reject the AppendEntries
	// There should only be one leader per term
	if req.Term == r.getCurrentTerm() && r.getState() == Leader {
		r.logger.Debug("Rejecting AppendEntries - we are leader for this term",
			"term", req.Term,
			"leaderID", req.LeaderID)
		return resp
	}

	// If leader's term is higher, update term and convert to follower
	if req.Term > r.getCurrentTerm() {
		r.logger.Info("Discovered higher term in AppendEntries", "term", req.Term)
		r.setCurrentTerm(req.Term)
		r.votedFor = ""
	}

	// Convert to follower if we're not already
	r.setState(Follower)
	r.updateLastContact()
	r.resetElectionTimer()

	// Check if log contains an entry at prevLogIndex with prevLogTerm
	if !r.log.HasEntry(req.PrevLogIndex, req.PrevLogTerm) {
		lastLogIndex := r.log.LastIndex()

		if req.PrevLogIndex > lastLogIndex {
			// Log is too short
			resp.ConflictIndex = lastLogIndex + 1
			resp.ConflictTerm = 0
		} else {
			// Log has entry but wrong term
			conflictTerm := r.log.GetTerm(req.PrevLogIndex)
			resp.ConflictTerm = conflictTerm
			resp.ConflictIndex = r.log.FindFirstEntryWithTerm(conflictTerm)
		}

		r.logger.Debug("Log consistency check failed",
			"prevLogIndex", req.PrevLogIndex,
			"prevLogTerm", req.PrevLogTerm,
			"conflictIndex", resp.ConflictIndex,
			"conflictTerm", resp.ConflictTerm)

		return resp
	}

	// Append new entries
	if len(req.Entries) > 0 {
		// Delete any conflicting entries and append new ones
		for _, entry := range req.Entries {
			if r.log.GetTerm(entry.Index) != entry.Term {
				// Delete this and all following entries
				r.log.DeleteFrom(entry.Index)
			}
			r.log.Append(&entry)
		}

		if err := r.persistState(); err != nil {
			r.logger.Error("Failed to persist log entries", "error", err)
		}

		r.logger.Debug("Appended entries",
			"count", len(req.Entries),
			"lastIndex", req.Entries[len(req.Entries)-1].Index)
	}

	// Update commit index
	if req.LeaderCommit > r.commitIndex {
		lastNewIndex := req.PrevLogIndex
		if len(req.Entries) > 0 {
			lastNewIndex = req.Entries[len(req.Entries)-1].Index
		}

		if req.LeaderCommit < lastNewIndex {
			r.commitIndex = req.LeaderCommit
		} else {
			r.commitIndex = lastNewIndex
		}

		r.logger.Debug("Updated commit index", "commitIndex", r.commitIndex)
	}

	resp.Success = true
	resp.Term = r.getCurrentTerm()
	return resp
}

// advanceCommitIndex tries to advance the commit index
func (r *Raft) advanceCommitIndex() {
	// Find the highest index that a majority has replicated
	for n := r.commitIndex + 1; n <= r.log.LastIndex(); n++ {
		// Only commit entries from current term
		if r.log.GetTerm(n) != r.getCurrentTerm() {
			continue
		}

		// Count replicas
		replicas := 1 // Count self
		for _, matchIdx := range r.matchIndex {
			if matchIdx >= n {
				replicas++
			}
		}

		// If majority has replicated this entry, commit it
		if replicas > len(r.peers)/2 {
			r.commitIndex = n
			r.logger.Debug("Advanced commit index", "commitIndex", n, "replicas", replicas)
		}
	}
}

// processRPC handles incoming RPC requests
func (r *Raft) processRPC(rpc RPC) {
	var response interface{}
	var err error

	switch req := rpc.Command.(type) {
	case *RequestVoteRequest:
		response = r.handleRequestVote(req)
	case *AppendEntriesRequest:
		response = r.handleAppendEntries(req)
	case *InstallSnapshotRequest:
		response = r.handleInstallSnapshot(req)
	default:
		err = fmt.Errorf("unknown RPC type: %T", req)
	}

	rpc.RespChan <- RPCResponse{
		Response: response,
		Error:    err,
	}
}

// runRPCHandler processes incoming RPCs
func (r *Raft) runRPCHandler(ctx context.Context) {
	defer r.routinesWg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.shutdownCh:
			return
		case rpc := <-r.rpcCh:
			r.processRPC(rpc)
		}
	}
}
