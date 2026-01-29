package raft

import (
	"context"
	"fmt"
	"sync"
)

// Transport defines the interface for RPC communication
type Transport interface {
	// AppendEntries sends an AppendEntries RPC to a peer
	AppendEntries(address string, req *AppendEntriesRequest) (*AppendEntriesResponse, error)

	// RequestVote sends a RequestVote RPC to a peer
	RequestVote(address string, req *RequestVoteRequest) (*RequestVoteResponse, error)

	// InstallSnapshot sends an InstallSnapshot RPC to a peer
	InstallSnapshot(address string, req *InstallSnapshotRequest) (*InstallSnapshotResponse, error)

	// Start starts the transport layer
	Start(ctx context.Context) error

	// Shutdown shuts down the transport
	Shutdown() error

	// Address returns the local address
	Address() string
}

// Global registry for local transports (for testing)
var (
	localTransportRegistry = make(map[string]*LocalTransport)
	localTransportMu       sync.RWMutex
)

// LocalTransport is an in-memory transport for testing
type LocalTransport struct {
	address string
	raft    *Raft
}

// NewLocalTransport creates a new local transport
func NewLocalTransport(address string) *LocalTransport {
	localTransportMu.Lock()
	defer localTransportMu.Unlock()

	t := &LocalTransport{
		address: address,
	}
	localTransportRegistry[address] = t
	return t
}

// SetRaft sets the Raft instance
func (t *LocalTransport) SetRaft(r *Raft) {
	t.raft = r
}

// AppendEntries sends an AppendEntries RPC
func (t *LocalTransport) AppendEntries(address string, req *AppendEntriesRequest) (*AppendEntriesResponse, error) {
	localTransportMu.RLock()
	target := localTransportRegistry[address]
	localTransportMu.RUnlock()

	if target == nil || target.raft == nil {
		return nil, fmt.Errorf("raft not found for address: %s", address)
	}

	// Send to the TARGET raft instance, not our own
	rpc := RPC{
		Command:  req,
		RespChan: make(chan RPCResponse, 1),
	}

	target.raft.rpcCh <- rpc

	resp := <-rpc.RespChan
	if resp.Error != nil {
		return nil, resp.Error
	}

	return resp.Response.(*AppendEntriesResponse), nil
}

// RequestVote sends a RequestVote RPC
func (t *LocalTransport) RequestVote(address string, req *RequestVoteRequest) (*RequestVoteResponse, error) {
	localTransportMu.RLock()
	target := localTransportRegistry[address]
	localTransportMu.RUnlock()

	if target == nil || target.raft == nil {
		return nil, fmt.Errorf("raft not found for address: %s", address)
	}

	// Send to the TARGET raft instance, not our own
	rpc := RPC{
		Command:  req,
		RespChan: make(chan RPCResponse, 1),
	}

	target.raft.rpcCh <- rpc

	resp := <-rpc.RespChan
	if resp.Error != nil {
		return nil, resp.Error
	}

	return resp.Response.(*RequestVoteResponse), nil
}

// InstallSnapshot sends an InstallSnapshot RPC
func (t *LocalTransport) InstallSnapshot(address string, req *InstallSnapshotRequest) (*InstallSnapshotResponse, error) {
	localTransportMu.RLock()
	target := localTransportRegistry[address]
	localTransportMu.RUnlock()

	if target == nil || target.raft == nil {
		return nil, fmt.Errorf("raft not found for address: %s", address)
	}

	// Send to the TARGET raft instance, not our own
	rpc := RPC{
		Command:  req,
		RespChan: make(chan RPCResponse, 1),
	}

	target.raft.rpcCh <- rpc

	resp := <-rpc.RespChan
	if resp.Error != nil {
		return nil, resp.Error
	}

	return resp.Response.(*InstallSnapshotResponse), nil
}

// Start starts the transport
func (t *LocalTransport) Start(ctx context.Context) error {
	return nil
}

// Shutdown shuts down the transport
func (t *LocalTransport) Shutdown() error {
	return nil
}

// Address returns the local address
func (t *LocalTransport) Address() string {
	return t.address
}
