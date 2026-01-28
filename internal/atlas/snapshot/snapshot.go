package snapshot

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/helios/helios/internal/atlas/store"
)

// Snapshot represents a point-in-time snapshot
type Snapshot struct {
	Timestamp time.Time
	Data      map[string]store.Entry
}

// Save writes a snapshot to disk atomically
func Save(path string, data map[string]store.Entry) error {
	// Write to temporary file first
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp snapshot: %w", err)
	}

	encoder := gob.NewEncoder(f)
	snapshot := Snapshot{
		Timestamp: time.Now(),
		Data:      data,
	}

	if err := encoder.Encode(snapshot); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to encode snapshot: %w", err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync snapshot: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close snapshot: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename snapshot: %w", err)
	}

	return nil
}

// Load reads a snapshot from disk
func Load(path string) (map[string]store.Entry, time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]store.Entry), time.Time{}, nil
		}
		return nil, time.Time{}, fmt.Errorf("failed to open snapshot: %w", err)
	}
	defer f.Close()

	decoder := gob.NewDecoder(f)
	var snapshot Snapshot

	if err := decoder.Decode(&snapshot); err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to decode snapshot: %w", err)
	}

	return snapshot.Data, snapshot.Timestamp, nil
}

// Exists checks if a snapshot file exists
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Delete removes a snapshot file
func Delete(path string) error {
	return os.Remove(path)
}

// Manager handles periodic snapshotting
type Manager struct {
	dataDir     string
	interval    time.Duration
	minCommands int
	stopCh      chan struct{}
	doneCh      chan struct{}
}

// NewManager creates a new snapshot manager
func NewManager(dataDir string, interval time.Duration, minCommands int) *Manager {
	return &Manager{
		dataDir:     dataDir,
		interval:    interval,
		minCommands: minCommands,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
}

// Start begins the snapshot manager
func (m *Manager) Start(snapshotFunc func() error) {
	go func() {
		defer close(m.doneCh)
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := snapshotFunc(); err != nil {
					// Log error but continue
					fmt.Printf("snapshot error: %v\n", err)
				}
			case <-m.stopCh:
				return
			}
		}
	}()
}

// Stop stops the snapshot manager
func (m *Manager) Stop() {
	close(m.stopCh)
	<-m.doneCh
}

// GetSnapshotPath returns the full path to the snapshot file
func GetSnapshotPath(dataDir string) string {
	return filepath.Join(dataDir, "dump.rdb")
}
