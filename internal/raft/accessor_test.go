package raft

import (
	"testing"
	"time"
)

func TestRaftAccessorMethods(t *testing.T) {
	// Create test log
	dataDir := t.TempDir()
	log, err := NewLog(dataDir)
	if err != nil {
		t.Fatalf("Failed to create log: %v", err)
	}

	// Create test Raft config
	config := &Config{
		NodeID:              "test-node",
		HeartbeatTimeout:    100 * time.Millisecond,
		ElectionTimeout:     500 * time.Millisecond,
		SnapshotInterval:    5 * time.Minute,
		SnapshotThreshold:   10000,
		MaxEntriesPerAppend: 100,
		DataDir:             dataDir,
	}

	// Create a minimal Raft instance for testing accessors
	r := &Raft{
		config:      config,
		currentTerm: 5,
		log:         log,
		commitIndex: 10,
		lastApplied: 8,
		nextIndex:   map[string]uint64{"peer1": 15},
		matchIndex:  map[string]uint64{"peer1": 12},
		sessionMgr:  NewSessionManager(10 * time.Minute),
		startTime:   time.Now().Add(-2 * time.Hour),
	}

	// Test GetNodeID
	t.Run("GetNodeID", func(t *testing.T) {
		nodeID := r.GetNodeID()
		if nodeID != "test-node" {
			t.Errorf("Expected node ID 'test-node', got '%s'", nodeID)
		}
	})

	// Test GetCurrentTerm
	t.Run("GetCurrentTerm", func(t *testing.T) {
		term := r.GetCurrentTerm()
		if term != 5 {
			t.Errorf("Expected term 5, got %d", term)
		}
	})

	// Test GetCommitIndex
	t.Run("GetCommitIndex", func(t *testing.T) {
		commitIdx := r.GetCommitIndex()
		if commitIdx != 10 {
			t.Errorf("Expected commit index 10, got %d", commitIdx)
		}
	})

	// Test GetLastApplied
	t.Run("GetLastApplied", func(t *testing.T) {
		appliedIdx := r.GetLastApplied()
		if appliedIdx != 8 {
			t.Errorf("Expected applied index 8, got %d", appliedIdx)
		}
	})

	// Test GetLastLogInfo with empty log
	t.Run("GetLastLogInfo_Empty", func(t *testing.T) {
		lastIdx, lastTerm := r.GetLastLogInfo()
		if lastIdx != 0 {
			t.Errorf("Expected last index 0, got %d", lastIdx)
		}
		if lastTerm != 0 {
			t.Errorf("Expected last term 0, got %d", lastTerm)
		}
	})

	// Test GetLastLogInfo with entries
	t.Run("GetLastLogInfo_WithEntries", func(t *testing.T) {
		// Add some log entries
		r.log.Append(&LogEntry{Term: 3, Command: []byte("entry1")})
		r.log.Append(&LogEntry{Term: 5, Command: []byte("entry2")})

		lastIdx, lastTerm := r.GetLastLogInfo()
		if lastIdx != 2 {
			t.Errorf("Expected last index 2, got %d", lastIdx)
		}
		if lastTerm != 5 {
			t.Errorf("Expected last term 5, got %d", lastTerm)
		}
	})

	// Test GetTLSConfig (nil when not configured)
	t.Run("GetTLSConfig_Nil", func(t *testing.T) {
		tlsConfig := r.GetTLSConfig()
		if tlsConfig != nil {
			t.Errorf("Expected nil TLS config, got %v", tlsConfig)
		}
	})

	// Test GetTLSConfig with config
	t.Run("GetTLSConfig_WithConfig", func(t *testing.T) {
		r.config.TLS = &TLSConfig{
			Enabled:    true,
			CertFile:   "/path/to/cert.pem",
			VerifyPeer: true,
		}

		tlsConfig := r.GetTLSConfig()
		if tlsConfig == nil {
			t.Fatal("Expected TLS config, got nil")
		}
		if !tlsConfig.Enabled {
			t.Error("Expected TLS to be enabled")
		}
		if tlsConfig.CertFile != "/path/to/cert.pem" {
			t.Errorf("Expected cert file '/path/to/cert.pem', got '%s'", tlsConfig.CertFile)
		}
	})

	// Test GetUptime
	t.Run("GetUptime", func(t *testing.T) {
		uptime := r.GetUptime()
		// Should be approximately 2 hours (allow some variance)
		expectedMin := 119 * time.Minute
		expectedMax := 121 * time.Minute
		if uptime < expectedMin || uptime > expectedMax {
			t.Errorf("Expected uptime around 2 hours, got %v", uptime)
		}
	})

	// Test GetMatchIndex
	t.Run("GetMatchIndex", func(t *testing.T) {
		matchIdx := r.GetMatchIndex("peer1")
		if matchIdx != 12 {
			t.Errorf("Expected match index 12 for peer1, got %d", matchIdx)
		}

		// Non-existent peer
		matchIdx = r.GetMatchIndex("unknown")
		if matchIdx != 0 {
			t.Errorf("Expected match index 0 for unknown peer, got %d", matchIdx)
		}
	})

	// Test GetNextIndex
	t.Run("GetNextIndex", func(t *testing.T) {
		nextIdx := r.GetNextIndex("peer1")
		if nextIdx != 15 {
			t.Errorf("Expected next index 15 for peer1, got %d", nextIdx)
		}

		// Non-existent peer
		nextIdx = r.GetNextIndex("unknown")
		if nextIdx != 0 {
			t.Errorf("Expected next index 0 for unknown peer, got %d", nextIdx)
		}
	})

	// Test GetSessionManager
	t.Run("GetSessionManager", func(t *testing.T) {
		sessionMgr := r.GetSessionManager()
		if sessionMgr == nil {
			t.Fatal("Expected session manager, got nil")
		}

		// Verify it's the same instance
		if sessionMgr != r.sessionMgr {
			t.Error("Expected same session manager instance")
		}
	})
}

func TestLogLastTerm(t *testing.T) {
	dataDir := t.TempDir()
	log, err := NewLog(dataDir)
	if err != nil {
		t.Fatalf("Failed to create log: %v", err)
	}

	// Test with empty log
	t.Run("EmptyLog", func(t *testing.T) {
		lastTerm := log.LastTerm()
		if lastTerm != 0 {
			t.Errorf("Expected last term 0 for empty log, got %d", lastTerm)
		}
	})

	// Test with entries
	t.Run("WithEntries", func(t *testing.T) {
		log.Append(&LogEntry{Term: 1, Command: []byte("entry1")})
		log.Append(&LogEntry{Term: 2, Command: []byte("entry2")})
		log.Append(&LogEntry{Term: 2, Command: []byte("entry3")})
		log.Append(&LogEntry{Term: 5, Command: []byte("entry4")})

		lastTerm := log.LastTerm()
		if lastTerm != 5 {
			t.Errorf("Expected last term 5, got %d", lastTerm)
		}

		lastIdx := log.LastIndex()
		if lastIdx != 4 {
			t.Errorf("Expected last index 4, got %d", lastIdx)
		}
	})
}
