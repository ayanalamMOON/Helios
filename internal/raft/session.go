package raft

import (
	"sync"
	"time"
)

// SessionManager manages client sessions for read-your-writes consistency.
// It tracks the last applied index for each client session, ensuring that
// reads return data at least as new as the client's last write.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	timeout  time.Duration
}

// Session represents a client session with consistency tracking.
type Session struct {
	ID           string
	LastIndex    uint64    // Last log index applied for this session
	LastActivity time.Time // Last activity timestamp for cleanup
	mu           sync.RWMutex
}

// NewSessionManager creates a new session manager with the specified timeout.
func NewSessionManager(timeout time.Duration) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		timeout:  timeout,
	}

	// Start cleanup goroutine
	go sm.cleanupLoop()

	return sm
}

// GetOrCreateSession retrieves an existing session or creates a new one.
func (sm *SessionManager) GetOrCreateSession(sessionID string) *Session {
	sm.mu.RLock()
	session, exists := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if exists {
		session.mu.Lock()
		session.LastActivity = time.Now()
		session.mu.Unlock()
		return session
	}

	// Create new session
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Double-check after acquiring write lock
	if session, exists := sm.sessions[sessionID]; exists {
		session.mu.Lock()
		session.LastActivity = time.Now()
		session.mu.Unlock()
		return session
	}

	session = &Session{
		ID:           sessionID,
		LastIndex:    0,
		LastActivity: time.Now(),
	}
	sm.sessions[sessionID] = session

	return session
}

// UpdateSessionIndex updates the last applied index for a session.
func (sm *SessionManager) UpdateSessionIndex(sessionID string, index uint64) {
	session := sm.GetOrCreateSession(sessionID)
	session.mu.Lock()
	if index > session.LastIndex {
		session.LastIndex = index
	}
	session.LastActivity = time.Now()
	session.mu.Unlock()
}

// GetSessionIndex retrieves the last applied index for a session.
func (sm *SessionManager) GetSessionIndex(sessionID string) uint64 {
	sm.mu.RLock()
	session, exists := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if !exists {
		return 0
	}

	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.LastIndex
}

// DeleteSession removes a session from tracking.
func (sm *SessionManager) DeleteSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}

// CleanupStale removes sessions that haven't been active recently.
func (sm *SessionManager) CleanupStale() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	count := 0

	for id, session := range sm.sessions {
		session.mu.RLock()
		lastActivity := session.LastActivity
		session.mu.RUnlock()

		if now.Sub(lastActivity) > sm.timeout {
			delete(sm.sessions, id)
			count++
		}
	}

	return count
}

// cleanupLoop periodically cleans up stale sessions.
func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(sm.timeout / 2)
	defer ticker.Stop()

	for range ticker.C {
		sm.CleanupStale()
	}
}

// SessionCount returns the current number of active sessions.
func (sm *SessionManager) SessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// GetAllSessions returns a snapshot of all active sessions (for debugging/monitoring).
func (sm *SessionManager) GetAllSessions() []SessionInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sessions := make([]SessionInfo, 0, len(sm.sessions))
	for _, session := range sm.sessions {
		session.mu.RLock()
		sessions = append(sessions, SessionInfo{
			ID:           session.ID,
			LastIndex:    session.LastIndex,
			LastActivity: session.LastActivity,
		})
		session.mu.RUnlock()
	}

	return sessions
}

// SessionInfo contains session information for monitoring.
type SessionInfo struct {
	ID           string
	LastIndex    uint64
	LastActivity time.Time
}
