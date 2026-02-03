package raft

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Log manages the replicated log
type Log struct {
	mu sync.RWMutex

	// Entries in the log
	entries []LogEntry

	// Path to log file
	logPath string

	// Persistence manager for serialized writes
	persistenceMgr *PersistenceManager
}

// NewLog creates a new log (deprecated - use NewLogWithPersistence)
func NewLog(dataDir string) (*Log, error) {
	return NewLogWithPersistence(dataDir, nil)
}

// NewLogWithPersistence creates a new log with a persistence manager
func NewLogWithPersistence(dataDir string, persistenceMgr *PersistenceManager) (*Log, error) {
	logPath := filepath.Join(dataDir, "raft-log.json")

	log := &Log{
		entries:        make([]LogEntry, 0),
		logPath:        logPath,
		persistenceMgr: persistenceMgr,
	}

	// Load existing log if it exists
	if err := log.load(); err != nil {
		return nil, fmt.Errorf("failed to load log: %w", err)
	}

	return log, nil
}

// Append adds a new entry to the log
func (l *Log) Append(entry *LogEntry) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Assign index
	if len(l.entries) > 0 {
		entry.Index = l.entries[len(l.entries)-1].Index + 1
	} else {
		entry.Index = 1
	}

	l.entries = append(l.entries, *entry)
	return entry.Index
}

// Get retrieves an entry by index
func (l *Log) Get(index uint64) (*LogEntry, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if index == 0 || index > uint64(len(l.entries)) {
		return nil, fmt.Errorf("index out of range: %d", index)
	}

	// Find entry with matching index
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].Index == index {
			entry := l.entries[i]
			return &entry, nil
		}
	}

	return nil, fmt.Errorf("entry not found at index %d", index)
}

// LastIndex returns the index of the last entry
func (l *Log) LastIndex() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if len(l.entries) == 0 {
		return 0
	}

	return l.entries[len(l.entries)-1].Index
}

// LastTerm returns the term of the last entry
func (l *Log) LastTerm() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if len(l.entries) == 0 {
		return 0
	}

	return l.entries[len(l.entries)-1].Term
}

// GetTerm returns the term of the entry at index
func (l *Log) GetTerm(index uint64) uint64 {
	if index == 0 {
		return 0
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	// Find entry with matching index
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].Index == index {
			return l.entries[i].Term
		}
	}

	return 0
}

// HasEntry checks if log contains an entry at index with term
func (l *Log) HasEntry(index, term uint64) bool {
	if index == 0 {
		return true // prevIndex 0 means no previous entry
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	// Check if we have an entry at this index
	if index > uint64(len(l.entries)) {
		return false
	}

	// Find entry with matching index
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].Index == index {
			return l.entries[i].Term == term
		}
	}

	return false
}

// GetEntriesFrom returns entries starting from index
func (l *Log) GetEntriesFrom(startIndex uint64, maxEntries int) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if startIndex > uint64(len(l.entries)) {
		return nil
	}

	result := make([]LogEntry, 0)

	// Find starting position
	startPos := -1
	for i, entry := range l.entries {
		if entry.Index >= startIndex {
			startPos = i
			break
		}
	}

	if startPos < 0 {
		return nil
	}

	// Collect entries up to maxEntries
	for i := startPos; i < len(l.entries) && len(result) < maxEntries; i++ {
		result = append(result, l.entries[i])
	}

	return result
}

// DeleteFrom deletes entries from index onwards
func (l *Log) DeleteFrom(index uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Find position to truncate
	truncatePos := -1
	for i, entry := range l.entries {
		if entry.Index >= index {
			truncatePos = i
			break
		}
	}

	if truncatePos >= 0 {
		l.entries = l.entries[:truncatePos]
	}
}

// FindLastEntryWithTerm finds the last entry with given term
func (l *Log) FindLastEntryWithTerm(term uint64) uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].Term == term {
			return l.entries[i].Index
		}
	}

	return 0
}

// FindFirstEntryWithTerm finds the first entry with given term
func (l *Log) FindFirstEntryWithTerm(term uint64) uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, entry := range l.entries {
		if entry.Term == term {
			return entry.Index
		}
	}

	return 0
}

// GetEntries returns all entries from startIndex to endIndex (inclusive)
func (l *Log) GetEntries(startIndex, endIndex uint64) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]LogEntry, 0)

	for _, entry := range l.entries {
		if entry.Index >= startIndex && entry.Index <= endIndex {
			result = append(result, entry)
		}
	}

	return result
}

// Compact removes entries up to index and updates base
func (l *Log) Compact(index uint64, snapshot []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Find position to compact
	compactPos := -1
	for i, entry := range l.entries {
		if entry.Index > index {
			compactPos = i
			break
		}
	}

	if compactPos >= 0 {
		l.entries = l.entries[compactPos:]
	} else {
		l.entries = make([]LogEntry, 0)
	}

	return l.persist()
}

// Persist saves the log to disk
func (l *Log) persist() error {
	data, err := json.Marshal(l.entries)
	if err != nil {
		return fmt.Errorf("failed to marshal log: %w", err)
	}

	// Use persistence manager if available, otherwise fallback to direct write
	if l.persistenceMgr != nil {
		return l.persistenceMgr.WriteFile("log", l.logPath, data)
	}

	// Fallback for backward compatibility (direct write)
	tmpPath := l.logPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write log: %w", err)
	}

	if err := os.Rename(tmpPath, l.logPath); err != nil {
		return fmt.Errorf("failed to rename log file: %w", err)
	}

	return nil
}

// load reads the log from disk
func (l *Log) load() error {
	// If file doesn't exist, start with empty log
	if _, err := os.Stat(l.logPath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(l.logPath)
	if err != nil {
		return fmt.Errorf("failed to read log: %w", err)
	}

	if len(data) == 0 {
		return nil
	}

	if err := json.Unmarshal(data, &l.entries); err != nil {
		return fmt.Errorf("failed to unmarshal log: %w", err)
	}

	return nil
}

// Sync persists the log to disk
func (l *Log) Sync() error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.persist()
}

// Size returns the number of entries in the log
func (l *Log) Size() int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return len(l.entries)
}

// Reset clears all entries
func (l *Log) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries = make([]LogEntry, 0)
}
