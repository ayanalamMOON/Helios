package raft

import (
	"fmt"
	"testing"
	"time"
)

func TestSessionManager(t *testing.T) {
	sm := NewSessionManager(1 * time.Second)

	t.Run("CreateAndRetrieve", func(t *testing.T) {
		sessionID := "test-session-1"
		session := sm.GetOrCreateSession(sessionID)

		if session.ID != sessionID {
			t.Errorf("Expected session ID %s, got %s", sessionID, session.ID)
		}

		if session.LastIndex != 0 {
			t.Errorf("Expected initial last index 0, got %d", session.LastIndex)
		}
	})

	t.Run("UpdateIndex", func(t *testing.T) {
		sessionID := "test-session-2"
		sm.UpdateSessionIndex(sessionID, 10)

		index := sm.GetSessionIndex(sessionID)
		if index != 10 {
			t.Errorf("Expected index 10, got %d", index)
		}

		// Update with higher index
		sm.UpdateSessionIndex(sessionID, 20)
		index = sm.GetSessionIndex(sessionID)
		if index != 20 {
			t.Errorf("Expected index 20, got %d", index)
		}

		// Update with lower index should not change
		sm.UpdateSessionIndex(sessionID, 15)
		index = sm.GetSessionIndex(sessionID)
		if index != 20 {
			t.Errorf("Expected index to remain 20, got %d", index)
		}
	})

	t.Run("SessionCount", func(t *testing.T) {
		sm2 := NewSessionManager(10 * time.Second)

		if count := sm2.SessionCount(); count != 0 {
			t.Errorf("Expected 0 sessions, got %d", count)
		}

		sm2.GetOrCreateSession("session-1")
		sm2.GetOrCreateSession("session-2")
		sm2.GetOrCreateSession("session-3")

		if count := sm2.SessionCount(); count != 3 {
			t.Errorf("Expected 3 sessions, got %d", count)
		}
	})

	t.Run("DeleteSession", func(t *testing.T) {
		sessionID := "test-session-3"
		sm.UpdateSessionIndex(sessionID, 100)

		index := sm.GetSessionIndex(sessionID)
		if index != 100 {
			t.Errorf("Expected index 100, got %d", index)
		}

		sm.DeleteSession(sessionID)

		index = sm.GetSessionIndex(sessionID)
		if index != 0 {
			t.Errorf("Expected index 0 after deletion, got %d", index)
		}
	})

	t.Run("CleanupStale", func(t *testing.T) {
		// Use a very long timeout so the background cleanup doesn't interfere
		sm3 := NewSessionManager(10 * time.Second)

		// Create sessions and manually set their LastActivity to the past
		for i := 1; i <= 3; i++ {
			sessionID := fmt.Sprintf("session-%d", i)
			session := sm3.GetOrCreateSession(sessionID)
			// Manually set LastActivity to the past
			session.mu.Lock()
			session.LastActivity = time.Now().Add(-15 * time.Second)
			session.mu.Unlock()
		}

		if count := sm3.SessionCount(); count != 3 {
			t.Errorf("Expected 3 sessions before cleanup, got %d", count)
		}

		removed := sm3.CleanupStale()
		if removed != 3 {
			t.Errorf("Expected 3 sessions to be removed, got %d", removed)
		}

		if count := sm3.SessionCount(); count != 0 {
			t.Errorf("Expected 0 sessions after cleanup, got %d", count)
		}
	})

	t.Run("GetAllSessions", func(t *testing.T) {
		sm4 := NewSessionManager(10 * time.Second)

		sm4.UpdateSessionIndex("session-1", 10)
		sm4.UpdateSessionIndex("session-2", 20)
		sm4.UpdateSessionIndex("session-3", 30)

		sessions := sm4.GetAllSessions()
		if len(sessions) != 3 {
			t.Errorf("Expected 3 sessions, got %d", len(sessions))
		}

		// Verify session data
		foundIndexes := make(map[uint64]bool)
		for _, s := range sessions {
			foundIndexes[s.LastIndex] = true
		}

		if !foundIndexes[10] || !foundIndexes[20] || !foundIndexes[30] {
			t.Error("Expected to find indexes 10, 20, 30")
		}
	})

	t.Run("ConcurrentAccess", func(t *testing.T) {
		sm5 := NewSessionManager(10 * time.Second)
		done := make(chan bool)

		// Spawn multiple goroutines updating the same session
		for i := 0; i < 10; i++ {
			go func(index int) {
				for j := 0; j < 100; j++ {
					sm5.UpdateSessionIndex("concurrent-session", uint64(index*100+j))
				}
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		// Should have a high index value
		index := sm5.GetSessionIndex("concurrent-session")
		if index < 900 {
			t.Errorf("Expected high index value (>=900), got %d", index)
		}
	})
}

func TestSessionLastActivity(t *testing.T) {
	sm := NewSessionManager(10 * time.Second)
	sessionID := "activity-test"

	// Create session
	session1 := sm.GetOrCreateSession(sessionID)
	activity1 := session1.LastActivity

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Access session again
	session2 := sm.GetOrCreateSession(sessionID)
	activity2 := session2.LastActivity

	// Activity should be updated
	if !activity2.After(activity1) {
		t.Error("Expected last activity to be updated")
	}

	// Should be the same session object
	if session1.ID != session2.ID {
		t.Error("Expected same session object")
	}
}
