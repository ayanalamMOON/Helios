package aof

import (
	"bufio"
	"os"
	"sync"
)

// AOF handles append-only file operations for durability
type AOF struct {
	mu       sync.Mutex
	file     *os.File
	writer   *bufio.Writer
	path     string
	syncMode SyncMode
}

// SyncMode defines when to sync to disk
type SyncMode int

const (
	// SyncEvery syncs after every write (strongest durability)
	SyncEvery SyncMode = iota
	// SyncInterval syncs periodically (balanced)
	SyncInterval
	// SyncNone relies on OS buffering (weakest durability)
	SyncNone
)

// Open creates or opens an AOF file
func Open(path string, mode SyncMode) (*AOF, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	return &AOF{
		file:     f,
		writer:   bufio.NewWriter(f),
		path:     path,
		syncMode: mode,
	}, nil
}

// Append writes a command to the AOF
func (a *AOF) Append(cmd string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, err := a.writer.WriteString(cmd + "\n"); err != nil {
		return err
	}

	if err := a.writer.Flush(); err != nil {
		return err
	}

	// Sync to disk based on mode
	if a.syncMode == SyncEvery {
		if err := a.file.Sync(); err != nil {
			return err
		}
	}

	return nil
}

// Sync forces a flush and fsync
func (a *AOF) Sync() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.writer.Flush(); err != nil {
		return err
	}

	return a.file.Sync()
}

// Close closes the AOF file
func (a *AOF) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.writer.Flush(); err != nil {
		return err
	}

	return a.file.Close()
}

// Truncate truncates the AOF file (used after snapshots)
func (a *AOF) Truncate() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Close current file
	if err := a.writer.Flush(); err != nil {
		return err
	}
	if err := a.file.Close(); err != nil {
		return err
	}

	// Recreate empty file
	f, err := os.Create(a.path)
	if err != nil {
		return err
	}

	a.file = f
	a.writer = bufio.NewWriter(f)
	return nil
}

// Rewrite creates a new compacted AOF file
func (a *AOF) Rewrite(newPath string, commands []string) error {
	f, err := os.Create(newPath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, cmd := range commands {
		if _, err := w.WriteString(cmd + "\n"); err != nil {
			return err
		}
	}

	if err := w.Flush(); err != nil {
		return err
	}

	return f.Sync()
}

// Size returns the current size of the AOF file
func (a *AOF) Size() (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	stat, err := a.file.Stat()
	if err != nil {
		return 0, err
	}
	return stat.Size(), nil
}
