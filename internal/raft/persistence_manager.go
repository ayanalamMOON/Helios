package raft

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// WriteRequest represents a pending disk write operation
type WriteRequest struct {
	Type     string // "state", "log", "snapshot-meta", "snapshot-data"
	Data     []byte
	Path     string
	TmpPath  string
	Response chan error
}

// PersistenceManager serializes all disk writes through a single goroutine
// This prevents file locking issues on Windows when multiple goroutines
// attempt concurrent writes to the same files
type PersistenceManager struct {
	mu      sync.RWMutex
	writeCh chan *WriteRequest
	done    chan struct{}
	running bool
}

// NewPersistenceManager creates a new persistence manager
func NewPersistenceManager() *PersistenceManager {
	pm := &PersistenceManager{
		writeCh: make(chan *WriteRequest, 100), // Buffer for write requests
		done:    make(chan struct{}),
		running: true,
	}
	go pm.run()
	return pm
}

// run is the main loop that processes write requests sequentially
func (pm *PersistenceManager) run() {
	for {
		select {
		case req := <-pm.writeCh:
			err := pm.handleWrite(req)
			req.Response <- err
			close(req.Response)
		case <-pm.done:
			// Drain remaining requests
			for len(pm.writeCh) > 0 {
				req := <-pm.writeCh
				req.Response <- fmt.Errorf("persistence manager shutting down")
				close(req.Response)
			}
			return
		}
	}
}

// handleWrite executes the actual disk write operation
func (pm *PersistenceManager) handleWrite(req *WriteRequest) error {
	switch req.Type {
	case "state", "log", "snapshot-meta", "snapshot-data":
		// Ensure directory exists
		dir := filepath.Dir(req.Path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}

		// Write to temporary file
		if err := os.WriteFile(req.TmpPath, req.Data, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", req.Type, err)
		}

		// Atomic rename
		if err := os.Rename(req.TmpPath, req.Path); err != nil {
			// Clean up temp file on error
			os.Remove(req.TmpPath)
			return fmt.Errorf("failed to rename %s file: %w", req.Type, err)
		}

		return nil
	default:
		return fmt.Errorf("unknown write type: %s", req.Type)
	}
}

// WriteFile queues a file write operation and waits for completion
func (pm *PersistenceManager) WriteFile(writeType, path string, data []byte) error {
	pm.mu.RLock()
	if !pm.running {
		pm.mu.RUnlock()
		return fmt.Errorf("persistence manager not running")
	}
	pm.mu.RUnlock()

	// Create temporary file path
	tmpPath := path + ".tmp"

	// Create write request with response channel
	req := &WriteRequest{
		Type:     writeType,
		Data:     data,
		Path:     path,
		TmpPath:  tmpPath,
		Response: make(chan error, 1),
	}

	// Queue the write request
	select {
	case pm.writeCh <- req:
		// Wait for the write to complete
		return <-req.Response
	case <-pm.done:
		return fmt.Errorf("persistence manager shutting down")
	}
}

// Shutdown gracefully stops the persistence manager
func (pm *PersistenceManager) Shutdown() {
	pm.mu.Lock()
	if !pm.running {
		pm.mu.Unlock()
		return
	}
	pm.running = false
	pm.mu.Unlock()

	close(pm.done)
}

// EnsureDir creates a directory if it doesn't exist
func (pm *PersistenceManager) EnsureDir(dir string) error {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.MkdirAll(dir, 0755)
	}
	return nil
}

// ReadFile is a convenience method for reading files (doesn't need serialization)
func (pm *PersistenceManager) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// FileExists checks if a file exists (doesn't need serialization)
func (pm *PersistenceManager) FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ListFiles lists files matching a pattern (doesn't need serialization)
func (pm *PersistenceManager) ListFiles(pattern string) ([]string, error) {
	return filepath.Glob(pattern)
}
