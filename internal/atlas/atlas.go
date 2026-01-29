package atlas

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/helios/helios/internal/atlas/aof"
	"github.com/helios/helios/internal/atlas/protocol"
	"github.com/helios/helios/internal/atlas/recovery"
	"github.com/helios/helios/internal/atlas/snapshot"
	"github.com/helios/helios/internal/atlas/store"
)

// Atlas is the main coordinator for the KV store
type Atlas struct {
	store    *store.Store
	aof      *aof.AOF
	dataDir  string
	cmdQueue chan commandRequest
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

type commandRequest struct {
	cmd    *protocol.Command
	respCh chan *protocol.Response
}

// Config holds configuration for Atlas
type Config struct {
	DataDir          string
	AOFSyncMode      aof.SyncMode
	SnapshotInterval time.Duration
	MinCommands      int
}

// New creates a new Atlas instance
func New(cfg *Config) (*Atlas, error) {
	st := store.New()

	aofPath := filepath.Join(cfg.DataDir, "appendonly.aof")
	aofFile, err := aof.Open(aofPath, cfg.AOFSyncMode)
	if err != nil {
		return nil, fmt.Errorf("failed to open AOF: %w", err)
	}

	a := &Atlas{
		store:    st,
		aof:      aofFile,
		dataDir:  cfg.DataDir,
		cmdQueue: make(chan commandRequest, 1000),
		stopCh:   make(chan struct{}),
	}

	// Recover from snapshot and AOF
	if err := a.recover(); err != nil {
		return nil, fmt.Errorf("recovery failed: %w", err)
	}

	// Start command executor
	a.wg.Add(1)
	go a.executor()

	return a, nil
}

// recover loads snapshot and replays AOF
func (a *Atlas) recover() error {
	snapshotPath := snapshot.GetSnapshotPath(a.dataDir)

	// Load snapshot if exists
	if snapshot.Exists(snapshotPath) {
		data, timestamp, err := snapshot.Load(snapshotPath)
		if err != nil {
			return fmt.Errorf("failed to load snapshot: %w", err)
		}
		a.store.LoadAll(data)
		fmt.Printf("Loaded snapshot from %v\n", timestamp)
	}

	// Replay AOF
	aofPath := filepath.Join(a.dataDir, "appendonly.aof")
	err := recovery.Replay(aofPath, func(line string) error {
		cmd, err := protocol.Parse(line)
		if err != nil {
			return err
		}
		return a.applyCommand(cmd)
	})

	if err != nil {
		return fmt.Errorf("failed to replay AOF: %w", err)
	}

	return nil
}

// Handle processes a command (implements server.CommandHandler)
func (a *Atlas) Handle(cmd *protocol.Command) *protocol.Response {
	// For read commands, execute directly
	if cmd.Type == "GET" || cmd.Type == "TTL" {
		return a.executeRead(cmd)
	}

	// For write commands, go through the queue
	respCh := make(chan *protocol.Response, 1)
	a.cmdQueue <- commandRequest{cmd: cmd, respCh: respCh}
	return <-respCh
}

// executeRead handles read-only commands
func (a *Atlas) executeRead(cmd *protocol.Command) *protocol.Response {
	switch cmd.Type {
	case "GET":
		value, ok := a.store.Get(cmd.Key)
		if !ok {
			return protocol.NewErrorResponse(fmt.Errorf("key not found"))
		}
		return protocol.NewValueResponse(value)

	case "TTL":
		ttl := a.store.TTL(cmd.Key)
		return protocol.NewTTLResponse(ttl)

	default:
		return protocol.NewErrorResponse(fmt.Errorf("unknown read command: %s", cmd.Type))
	}
}

// executor runs the single-threaded command executor
func (a *Atlas) executor() {
	defer a.wg.Done()

	for {
		select {
		case req := <-a.cmdQueue:
			resp := a.executeWrite(req.cmd)
			req.respCh <- resp

		case <-a.stopCh:
			return
		}
	}
}

// executeWrite handles write commands with AOF logging
func (a *Atlas) executeWrite(cmd *protocol.Command) *protocol.Response {
	// 1. Serialize and append to AOF
	cmdStr, err := protocol.Serialize(cmd)
	if err != nil {
		return protocol.NewErrorResponse(err)
	}

	if err := a.aof.Append(cmdStr); err != nil {
		return protocol.NewErrorResponse(fmt.Errorf("AOF append failed: %w", err))
	}

	// 2. Apply to memory
	if err := a.applyCommand(cmd); err != nil {
		return protocol.NewErrorResponse(err)
	}

	// 3. Return success
	return protocol.NewSuccessResponse()
}

// ApplyCommand applies a command to the store (exposed for Raft integration)
func (a *Atlas) ApplyCommand(cmd *protocol.Command) error {
	return a.applyCommand(cmd)
}

// applyCommand applies a command to the store (internal implementation)
func (a *Atlas) applyCommand(cmd *protocol.Command) error {
	switch cmd.Type {
	case "SET":
		value, err := base64.StdEncoding.DecodeString(cmd.Value)
		if err != nil {
			value = []byte(cmd.Value) // fallback to plain text
		}
		a.store.Set(cmd.Key, value, cmd.TTL)
		return nil

	case "DEL":
		a.store.Delete(cmd.Key)
		return nil

	case "EXPIRE":
		a.store.Expire(cmd.Key, cmd.TTL)
		return nil

	default:
		return fmt.Errorf("unknown command: %s", cmd.Type)
	}
}

// Snapshot creates a snapshot of the current state
func (a *Atlas) Snapshot() error {
	data := a.store.GetAll()
	snapshotPath := snapshot.GetSnapshotPath(a.dataDir)
	return snapshot.Save(snapshotPath, data)
}

// Close shuts down Atlas gracefully
func (a *Atlas) Close() error {
	close(a.stopCh)
	a.wg.Wait()

	if err := a.aof.Close(); err != nil {
		return err
	}

	return nil
}

// Stats returns store statistics
func (a *Atlas) Stats() map[string]interface{} {
	return map[string]interface{}{
		"keys": a.store.Len(),
	}
}

// Store interface implementations for subsystems

// Get retrieves a value from the store (read-only, no command queue)
func (a *Atlas) Get(key string) ([]byte, bool) {
	return a.store.Get(key)
}

// Set stores a key-value pair through the command queue
func (a *Atlas) Set(key string, value []byte, ttl int64) {
	cmd := protocol.NewSetCommand(key, value, ttl)
	respCh := make(chan *protocol.Response, 1)
	a.cmdQueue <- commandRequest{cmd: cmd, respCh: respCh}
	<-respCh
}

// Delete removes a key through the command queue
func (a *Atlas) Delete(key string) bool {
	cmd := protocol.NewDelCommand(key)
	respCh := make(chan *protocol.Response, 1)
	a.cmdQueue <- commandRequest{cmd: cmd, respCh: respCh}
	resp := <-respCh
	return resp.OK
}

// Scan returns keys matching a prefix
func (a *Atlas) Scan(prefix string) []string {
	return a.store.Scan(prefix)
}
