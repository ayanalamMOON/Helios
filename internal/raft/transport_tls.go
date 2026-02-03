package raft

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/gob"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// NetworkTransport implements Transport using TCP with optional TLS
type NetworkTransport struct {
	address    string
	listener   net.Listener
	tlsConfig  *tls.Config
	raft       *Raft
	logger     *Logger
	shutdownCh chan struct{}
	wg         sync.WaitGroup
	mu         sync.RWMutex
}

// NewNetworkTransport creates a new network transport
func NewNetworkTransport(address string, tlsCfg *TLSConfig) (*NetworkTransport, error) {
	var tlsConfig *tls.Config
	var err error

	// Configure TLS if enabled
	if tlsCfg != nil && tlsCfg.Enabled {
		tlsConfig, err = buildTLSConfig(tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("failed to build TLS config: %w", err)
		}
	}

	return &NetworkTransport{
		address:    address,
		tlsConfig:  tlsConfig,
		logger:     NewLogger("transport"),
		shutdownCh: make(chan struct{}),
	}, nil
}

// buildTLSConfig creates a tls.Config from TLSConfig
func buildTLSConfig(cfg *TLSConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: cfg.ServerName,
	}

	// Load certificate and key for server auth
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load key pair: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Load CA certificate for client auth
	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		tlsConfig.RootCAs = caCertPool
		tlsConfig.ClientCAs = caCertPool

		if cfg.VerifyPeer {
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		}
	}

	if cfg.InsecureSkipVerify {
		tlsConfig.InsecureSkipVerify = true
	}

	return tlsConfig, nil
}

// SetRaft sets the Raft instance
func (t *NetworkTransport) SetRaft(r *Raft) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.raft = r
}

// Start starts the transport layer
func (t *NetworkTransport) Start(ctx context.Context) error {
	var listener net.Listener
	var err error

	// Create listener
	if t.tlsConfig != nil {
		listener, err = tls.Listen("tcp", t.address, t.tlsConfig)
		t.logger.Info("Starting TLS transport", "address", t.address)
	} else {
		listener, err = net.Listen("tcp", t.address)
		t.logger.Info("Starting plain TCP transport", "address", t.address)
	}

	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	t.listener = listener

	// Start accepting connections
	t.wg.Add(1)
	go t.acceptLoop(ctx)

	return nil
}

// acceptLoop accepts incoming connections
func (t *NetworkTransport) acceptLoop(ctx context.Context) {
	defer t.wg.Done()

	for {
		select {
		case <-t.shutdownCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		conn, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.shutdownCh:
				return
			default:
				t.logger.Error("Failed to accept connection", "error", err)
				continue
			}
		}

		// Handle connection in a goroutine
		t.wg.Add(1)
		go t.handleConnection(conn)
	}
}

// handleConnection handles an incoming connection
func (t *NetworkTransport) handleConnection(conn net.Conn) {
	defer t.wg.Done()
	defer conn.Close()

	// Set read deadline
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	decoder := gob.NewDecoder(conn)
	encoder := gob.NewEncoder(conn)

	for {
		select {
		case <-t.shutdownCh:
			return
		default:
		}

		// Read RPC type
		var rpcType string
		if err := decoder.Decode(&rpcType); err != nil {
			if err != io.EOF {
				t.logger.Error("Failed to decode RPC type", "error", err)
			}
			return
		}

		// Handle different RPC types
		switch rpcType {
		case "AppendEntries":
			t.handleAppendEntries(decoder, encoder)
		case "RequestVote":
			t.handleRequestVote(decoder, encoder)
		case "InstallSnapshot":
			t.handleInstallSnapshot(decoder, encoder)
		default:
			t.logger.Error("Unknown RPC type", "type", rpcType)
			return
		}

		// Reset deadline after successful RPC
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	}
}

// handleAppendEntries handles AppendEntries RPC
func (t *NetworkTransport) handleAppendEntries(decoder *gob.Decoder, encoder *gob.Encoder) {
	var req AppendEntriesRequest
	if err := decoder.Decode(&req); err != nil {
		t.logger.Error("Failed to decode AppendEntries request", "error", err)
		return
	}

	t.mu.RLock()
	raft := t.raft
	t.mu.RUnlock()

	if raft == nil {
		t.logger.Error("Raft not set")
		return
	}

	// Send to Raft
	rpc := RPC{
		Command:  &req,
		RespChan: make(chan RPCResponse, 1),
	}

	raft.rpcCh <- rpc

	// Wait for response
	rpcResp := <-rpc.RespChan

	var resp AppendEntriesResponse
	if rpcResp.Error != nil {
		t.logger.Error("AppendEntries RPC error", "error", rpcResp.Error)
		// Send error response
		resp = AppendEntriesResponse{Success: false}
	} else {
		resp = *rpcResp.Response.(*AppendEntriesResponse)
	}

	// Send response
	if err := encoder.Encode(&resp); err != nil {
		t.logger.Error("Failed to encode AppendEntries response", "error", err)
	}
}

// handleRequestVote handles RequestVote RPC
func (t *NetworkTransport) handleRequestVote(decoder *gob.Decoder, encoder *gob.Encoder) {
	var req RequestVoteRequest
	if err := decoder.Decode(&req); err != nil {
		t.logger.Error("Failed to decode RequestVote request", "error", err)
		return
	}

	t.mu.RLock()
	raft := t.raft
	t.mu.RUnlock()

	if raft == nil {
		t.logger.Error("Raft not set")
		return
	}

	// Send to Raft
	rpc := RPC{
		Command:  &req,
		RespChan: make(chan RPCResponse, 1),
	}

	raft.rpcCh <- rpc

	// Wait for response
	rpcResp := <-rpc.RespChan

	var resp RequestVoteResponse
	if rpcResp.Error != nil {
		t.logger.Error("RequestVote RPC error", "error", rpcResp.Error)
		// Send error response
		resp = RequestVoteResponse{VoteGranted: false}
	} else {
		resp = *rpcResp.Response.(*RequestVoteResponse)
	}

	// Send response
	if err := encoder.Encode(&resp); err != nil {
		t.logger.Error("Failed to encode RequestVote response", "error", err)
	}
}

// handleInstallSnapshot handles InstallSnapshot RPC
func (t *NetworkTransport) handleInstallSnapshot(decoder *gob.Decoder, encoder *gob.Encoder) {
	var req InstallSnapshotRequest
	if err := decoder.Decode(&req); err != nil {
		t.logger.Error("Failed to decode InstallSnapshot request", "error", err)
		return
	}

	t.mu.RLock()
	raft := t.raft
	t.mu.RUnlock()

	if raft == nil {
		t.logger.Error("Raft not set")
		return
	}

	// Send to Raft
	rpc := RPC{
		Command:  &req,
		RespChan: make(chan RPCResponse, 1),
	}

	raft.rpcCh <- rpc

	// Wait for response
	rpcResp := <-rpc.RespChan

	var resp InstallSnapshotResponse
	if rpcResp.Error != nil {
		t.logger.Error("InstallSnapshot RPC error", "error", rpcResp.Error)
		// Send error response
		resp = InstallSnapshotResponse{}
	} else {
		resp = *rpcResp.Response.(*InstallSnapshotResponse)
	}

	// Send response
	if err := encoder.Encode(&resp); err != nil {
		t.logger.Error("Failed to encode InstallSnapshot response", "error", err)
	}
}

// AppendEntries sends an AppendEntries RPC to a peer
func (t *NetworkTransport) AppendEntries(address string, req *AppendEntriesRequest) (*AppendEntriesResponse, error) {
	resp := &AppendEntriesResponse{}
	if err := t.sendRPC(address, "AppendEntries", req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// RequestVote sends a RequestVote RPC to a peer
func (t *NetworkTransport) RequestVote(address string, req *RequestVoteRequest) (*RequestVoteResponse, error) {
	resp := &RequestVoteResponse{}
	if err := t.sendRPC(address, "RequestVote", req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// InstallSnapshot sends an InstallSnapshot RPC to a peer
func (t *NetworkTransport) InstallSnapshot(address string, req *InstallSnapshotRequest) (*InstallSnapshotResponse, error) {
	resp := &InstallSnapshotResponse{}
	if err := t.sendRPC(address, "InstallSnapshot", req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// sendRPC sends an RPC to a peer
func (t *NetworkTransport) sendRPC(address string, rpcType string, req interface{}, resp interface{}) error {
	// Dial the peer
	var conn net.Conn
	var err error

	if t.tlsConfig != nil {
		conn, err = tls.Dial("tcp", address, t.tlsConfig)
	} else {
		conn, err = net.Dial("tcp", address)
	}

	if err != nil {
		return fmt.Errorf("failed to dial %s: %w", address, err)
	}
	defer conn.Close()

	// Set deadline
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	encoder := gob.NewEncoder(conn)
	decoder := gob.NewDecoder(conn)

	// Send RPC type
	if err := encoder.Encode(rpcType); err != nil {
		return fmt.Errorf("failed to encode RPC type: %w", err)
	}

	// Send request
	if err := encoder.Encode(req); err != nil {
		return fmt.Errorf("failed to encode request: %w", err)
	}

	// Receive response
	if err := decoder.Decode(resp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// Shutdown shuts down the transport
func (t *NetworkTransport) Shutdown() error {
	close(t.shutdownCh)

	if t.listener != nil {
		t.listener.Close()
	}

	// Wait for all goroutines to finish
	t.wg.Wait()

	t.logger.Info("Transport shut down")
	return nil
}

// Address returns the local address
func (t *NetworkTransport) Address() string {
	return t.address
}
