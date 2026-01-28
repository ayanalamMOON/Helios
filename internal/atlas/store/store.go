package store

import (
	"sync"
	"time"
)

// Entry represents a key-value entry with optional expiration
type Entry struct {
	Value    []byte
	ExpireAt time.Time // zero value means no expiry
}

// Store is the in-memory key-value store with TTL support
type Store struct {
	mu   sync.RWMutex
	data map[string]Entry
}

// New creates a new Store instance
func New() *Store {
	return &Store{
		data: make(map[string]Entry),
	}
}

// Set stores a key-value pair with optional TTL
func (s *Store) Set(key string, value []byte, ttlSeconds int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var exp time.Time
	if ttlSeconds > 0 {
		exp = time.Now().Add(time.Duration(ttlSeconds) * time.Second)
	}

	s.data[key] = Entry{
		Value:    value,
		ExpireAt: exp,
	}
}

// Get retrieves a value by key, checking expiration
func (s *Store) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	entry, ok := s.data[key]
	s.mu.RUnlock()

	if !ok {
		return nil, false
	}

	// Check if expired
	if !entry.ExpireAt.IsZero() && time.Now().After(entry.ExpireAt) {
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
		return nil, false
	}

	return entry.Value, true
}

// Delete removes a key from the store
func (s *Store) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data[key]; ok {
		delete(s.data, key)
		return true
	}
	return false
}

// Expire sets a TTL on an existing key
func (s *Store) Expire(key string, ttlSeconds int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.data[key]
	if !ok {
		return false
	}

	if ttlSeconds > 0 {
		entry.ExpireAt = time.Now().Add(time.Duration(ttlSeconds) * time.Second)
	} else {
		entry.ExpireAt = time.Time{}
	}
	s.data[key] = entry
	return true
}

// TTL returns the remaining time to live in seconds
// Returns -2 if key doesn't exist, -1 if no expiry
func (s *Store) TTL(key string) int64 {
	s.mu.RLock()
	entry, ok := s.data[key]
	s.mu.RUnlock()

	if !ok {
		return -2 // key does not exist
	}

	if entry.ExpireAt.IsZero() {
		return -1 // no expiry
	}

	remaining := time.Until(entry.ExpireAt).Seconds()
	if remaining < 0 {
		return -2
	}
	return int64(remaining)
}

// Scan returns all keys matching a prefix
func (s *Store) Scan(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var keys []string
	for k := range s.data {
		if len(prefix) == 0 || len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	return keys
}

// Keys returns all keys in the store
func (s *Store) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

// Len returns the number of keys in the store
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// Clear removes all keys from the store
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make(map[string]Entry)
}

// GetAll returns a snapshot of all data (for snapshotting)
func (s *Store) GetAll() map[string]Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := make(map[string]Entry, len(s.data))
	for k, v := range s.data {
		snapshot[k] = v
	}
	return snapshot
}

// LoadAll loads data from a snapshot
func (s *Store) LoadAll(data map[string]Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = data
}
