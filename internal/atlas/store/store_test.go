package store

import (
	"testing"
	"time"
)

func TestStoreBasicOperations(t *testing.T) {
	s := New()

	// Test Set and Get
	s.Set("key1", []byte("value1"), 0)
	val, ok := s.Get("key1")
	if !ok {
		t.Fatal("Expected key1 to exist")
	}
	if string(val) != "value1" {
		t.Errorf("Expected 'value1', got '%s'", string(val))
	}

	// Test Delete
	deleted := s.Delete("key1")
	if !deleted {
		t.Error("Expected delete to return true")
	}
	_, ok = s.Get("key1")
	if ok {
		t.Error("Expected key1 to be deleted")
	}
}

func TestStoreTTL(t *testing.T) {
	s := New()

	// Set key with 1 second TTL
	s.Set("expiring_key", []byte("value"), 1)

	// Should exist immediately
	val, ok := s.Get("expiring_key")
	if !ok {
		t.Fatal("Expected key to exist")
	}
	if string(val) != "value" {
		t.Errorf("Expected 'value', got '%s'", string(val))
	}

	// Check TTL
	ttl := s.TTL("expiring_key")
	if ttl < 0 || ttl > 1 {
		t.Errorf("Expected TTL between 0 and 1, got %d", ttl)
	}

	// Wait for expiration
	time.Sleep(1100 * time.Millisecond)

	// Should not exist after expiration
	_, ok = s.Get("expiring_key")
	if ok {
		t.Error("Expected key to be expired")
	}

	// TTL should return -2 for non-existent key
	ttl = s.TTL("expiring_key")
	if ttl != -2 {
		t.Errorf("Expected TTL -2 for expired key, got %d", ttl)
	}
}

func TestStoreExpire(t *testing.T) {
	s := New()

	// Set key without expiration
	s.Set("key1", []byte("value1"), 0)

	// TTL should be -1 (no expiry)
	ttl := s.TTL("key1")
	if ttl != -1 {
		t.Errorf("Expected TTL -1 for key without expiry, got %d", ttl)
	}

	// Add expiration
	ok := s.Expire("key1", 2)
	if !ok {
		t.Error("Expected Expire to return true")
	}

	// TTL should now be set
	ttl = s.TTL("key1")
	if ttl < 1 || ttl > 2 {
		t.Errorf("Expected TTL between 1 and 2, got %d", ttl)
	}
}

func TestStoreScan(t *testing.T) {
	s := New()

	// Add multiple keys
	s.Set("user:1", []byte("alice"), 0)
	s.Set("user:2", []byte("bob"), 0)
	s.Set("user:3", []byte("charlie"), 0)
	s.Set("post:1", []byte("post1"), 0)

	// Scan with prefix
	keys := s.Scan("user:")
	if len(keys) != 3 {
		t.Errorf("Expected 3 keys with 'user:' prefix, got %d", len(keys))
	}

	// Scan all keys
	allKeys := s.Scan("")
	if len(allKeys) != 4 {
		t.Errorf("Expected 4 total keys, got %d", len(allKeys))
	}
}

func TestStoreClear(t *testing.T) {
	s := New()

	// Add keys
	s.Set("key1", []byte("value1"), 0)
	s.Set("key2", []byte("value2"), 0)

	if s.Len() != 2 {
		t.Errorf("Expected 2 keys, got %d", s.Len())
	}

	// Clear
	s.Clear()

	if s.Len() != 0 {
		t.Errorf("Expected 0 keys after clear, got %d", s.Len())
	}
}

func TestStoreGetAll(t *testing.T) {
	s := New()

	// Add keys
	s.Set("key1", []byte("value1"), 0)
	s.Set("key2", []byte("value2"), 0)

	// Get snapshot
	snapshot := s.GetAll()

	if len(snapshot) != 2 {
		t.Errorf("Expected 2 keys in snapshot, got %d", len(snapshot))
	}

	// Verify data
	if string(snapshot["key1"].Value) != "value1" {
		t.Error("Snapshot data mismatch for key1")
	}
}
