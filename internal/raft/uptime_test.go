package raft

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUptimeTracker_NewUptimeTracker(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")

	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)
	if ut == nil {
		t.Fatal("Expected non-nil UptimeTracker")
	}

	if ut.nodeID != "test-node" {
		t.Errorf("Expected nodeID 'test-node', got '%s'", ut.nodeID)
	}

	if ut.dataDir != tmpDir {
		t.Errorf("Expected dataDir '%s', got '%s'", tmpDir, ut.dataDir)
	}
}

func TestUptimeTracker_RecordStart(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	ut.RecordStart(Follower, 1, "leader-1")

	if ut.currentSession == nil {
		t.Fatal("Expected current session to be set")
	}

	if ut.currentState != Follower {
		t.Errorf("Expected state Follower, got %v", ut.currentState)
	}

	if ut.currentTerm != 1 {
		t.Errorf("Expected term 1, got %d", ut.currentTerm)
	}

	if len(ut.events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(ut.events))
	}

	if ut.events[0].Type != UptimeEventStart {
		t.Errorf("Expected event type start, got %s", ut.events[0].Type)
	}
}

func TestUptimeTracker_RecordStop(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	ut.RecordStart(Follower, 1, "leader-1")
	time.Sleep(10 * time.Millisecond)
	ut.RecordStop("test shutdown")

	if ut.currentSession != nil {
		t.Error("Expected current session to be nil after stop")
	}

	if len(ut.sessions) != 1 {
		t.Errorf("Expected 1 session, got %d", len(ut.sessions))
	}

	if ut.sessions[0].EndReason != "test shutdown" {
		t.Errorf("Expected end reason 'test shutdown', got '%s'", ut.sessions[0].EndReason)
	}

	if ut.sessions[0].Duration <= 0 {
		t.Error("Expected positive duration")
	}
}

func TestUptimeTracker_RecordStateChange(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	ut.RecordStart(Follower, 1, "leader-1")
	time.Sleep(10 * time.Millisecond)

	// Change to candidate
	ut.RecordStateChange(Candidate, 2, "")
	if ut.currentState != Candidate {
		t.Errorf("Expected state Candidate, got %v", ut.currentState)
	}

	time.Sleep(10 * time.Millisecond)

	// Become leader
	ut.RecordStateChange(Leader, 2, "test-node")

	if ut.currentState != Leader {
		t.Errorf("Expected state Leader, got %v", ut.currentState)
	}

	// Check election event was recorded
	foundElection := false
	for _, e := range ut.events {
		if e.Type == UptimeEventElection {
			foundElection = true
			break
		}
	}
	if !foundElection {
		t.Error("Expected election event to be recorded")
	}

	// Check session stats
	if ut.currentSession.Elections != 1 {
		t.Errorf("Expected 1 election, got %d", ut.currentSession.Elections)
	}

	if ut.currentSession.TermsServed != 1 {
		t.Errorf("Expected 1 term served, got %d", ut.currentSession.TermsServed)
	}
}

func TestUptimeTracker_RecordDemote(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	ut.RecordStart(Leader, 1, "test-node")
	time.Sleep(10 * time.Millisecond)

	// Demote to follower
	ut.RecordStateChange(Follower, 2, "other-leader")

	// Check demote event was recorded
	foundDemote := false
	for _, e := range ut.events {
		if e.Type == UptimeEventDemote {
			foundDemote = true
			break
		}
	}
	if !foundDemote {
		t.Error("Expected demote event to be recorded")
	}
}

func TestUptimeTracker_GetCurrentUptime(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	// Before start
	if uptime := ut.GetCurrentUptime(); uptime != 0 {
		t.Errorf("Expected 0 uptime before start, got %v", uptime)
	}

	ut.RecordStart(Follower, 1, "leader-1")
	time.Sleep(50 * time.Millisecond)

	uptime := ut.GetCurrentUptime()
	if uptime < 50*time.Millisecond {
		t.Errorf("Expected at least 50ms uptime, got %v", uptime)
	}
}

func TestUptimeTracker_GetStats(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	// First session
	ut.RecordStart(Follower, 1, "leader-1")
	time.Sleep(20 * time.Millisecond)
	ut.RecordStateChange(Leader, 2, "test-node")
	time.Sleep(30 * time.Millisecond)
	ut.RecordRestart("restart") // Use restart explicitly

	// Second session
	ut.RecordStart(Follower, 3, "leader-2")
	time.Sleep(20 * time.Millisecond)

	stats := ut.GetStats()

	if stats.TotalSessions != 2 {
		t.Errorf("Expected 2 sessions, got %d", stats.TotalSessions)
	}

	if stats.TotalUptime < 70*time.Millisecond {
		t.Errorf("Expected at least 70ms total uptime, got %v", stats.TotalUptime)
	}

	if stats.TotalLeaderTime < 25*time.Millisecond {
		t.Errorf("Expected at least 25ms leader time, got %v", stats.TotalLeaderTime)
	}

	if stats.TotalFollowerTime < 35*time.Millisecond {
		t.Errorf("Expected at least 35ms follower time, got %v", stats.TotalFollowerTime)
	}

	if stats.TotalElections != 1 {
		t.Errorf("Expected 1 election, got %d", stats.TotalElections)
	}
}

func TestUptimeTracker_UptimePercentage(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	ut.RecordStart(Follower, 1, "leader-1")
	time.Sleep(50 * time.Millisecond)

	stats := ut.GetStats()

	// Uptime percentage should be close to 100% for a running session
	if stats.UptimePercentage24h < 90 {
		t.Errorf("Expected high uptime percentage, got %.2f%%", stats.UptimePercentage24h)
	}

	if stats.UptimePercentageAll < 90 {
		t.Errorf("Expected high overall uptime percentage, got %.2f%%", stats.UptimePercentageAll)
	}
}

func TestUptimeTracker_LeadershipRatio(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	ut.RecordStart(Follower, 1, "leader-1")
	time.Sleep(25 * time.Millisecond)
	ut.RecordStateChange(Leader, 2, "test-node")
	time.Sleep(75 * time.Millisecond)
	ut.RecordStop("test")

	stats := ut.GetStats()

	// Should be around 75% leadership
	if stats.LeadershipRatio < 0.5 || stats.LeadershipRatio > 0.95 {
		t.Errorf("Expected leadership ratio around 0.75, got %.2f", stats.LeadershipRatio)
	}
}

func TestUptimeTracker_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")

	// First tracker - create some history
	ut1 := NewUptimeTracker("test-node", tmpDir, nil, logger)
	ut1.RecordStart(Follower, 1, "leader-1")
	ut1.RecordStateChange(Leader, 2, "test-node")
	time.Sleep(20 * time.Millisecond)
	ut1.RecordStop("shutdown")
	ut1.Flush()

	// Second tracker - should load history
	ut2 := NewUptimeTracker("test-node", tmpDir, nil, logger)

	if len(ut2.sessions) != 1 {
		t.Errorf("Expected 1 session loaded, got %d", len(ut2.sessions))
	}

	if len(ut2.events) < 2 {
		t.Errorf("Expected at least 2 events loaded, got %d", len(ut2.events))
	}

	// Start new session and check stats accumulate
	ut2.RecordStart(Follower, 3, "leader-2")
	stats := ut2.GetStats()

	if stats.TotalSessions != 2 {
		t.Errorf("Expected 2 total sessions, got %d", stats.TotalSessions)
	}
}

func TestUptimeTracker_CrashDetection(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")

	// Create history file with unclosed session
	historyPath := filepath.Join(tmpDir, "uptime-history.json")
	historyJSON := `{
		"node_id": "test-node",
		"events": [
			{"timestamp": "2024-01-01T10:00:00Z", "type": "start", "state": 1, "term": 1}
		],
		"sessions": [
			{"start_time": "2024-01-01T10:00:00Z"}
		]
	}`
	if err := os.WriteFile(historyPath, []byte(historyJSON), 0644); err != nil {
		t.Fatalf("Failed to write test history: %v", err)
	}

	// Load tracker - should detect crash on next start
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	// Previous unclosed session should be in sessions
	if len(ut.sessions) != 1 {
		t.Errorf("Expected 1 session loaded, got %d", len(ut.sessions))
	}

	// Start new session - should mark previous as crashed
	ut.RecordStart(Follower, 2, "leader-1")

	// Check that previous session is now closed with crash reason
	if len(ut.sessions) < 1 {
		t.Fatal("Expected at least 1 closed session")
	}

	// The first session should now be closed (as a crash)
	firstSession := ut.sessions[0]
	if firstSession.EndTime == nil {
		t.Error("Expected first session to have end time")
	}
	if firstSession.EndReason != "crash" {
		t.Errorf("Expected first session end reason 'crash', got '%s'", firstSession.EndReason)
	}
}

func TestUptimeTracker_GetHistory(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	ut.RecordStart(Follower, 1, "leader-1")
	ut.RecordStateChange(Leader, 2, "test-node")
	time.Sleep(10 * time.Millisecond)
	ut.RecordStop("test")

	ut.RecordStart(Follower, 3, "leader-2")

	history := ut.GetHistory(100, 50)

	if history.NodeID != "test-node" {
		t.Errorf("Expected node ID 'test-node', got '%s'", history.NodeID)
	}

	if len(history.Events) < 3 {
		t.Errorf("Expected at least 3 events, got %d", len(history.Events))
	}

	if len(history.RecentSessions) != 2 {
		t.Errorf("Expected 2 sessions, got %d", len(history.RecentSessions))
	}

	// Check event types are strings
	for _, e := range history.Events {
		if e.Type == "" {
			t.Error("Expected non-empty event type")
		}
	}

	// Check timestamps are formatted
	for _, e := range history.Events {
		if e.Timestamp == "" {
			t.Error("Expected non-empty timestamp")
		}
	}
}

func TestUptimeTracker_GetStatsJSON(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	ut.RecordStart(Follower, 1, "leader-1")
	time.Sleep(20 * time.Millisecond)

	statsJSON := ut.GetStatsJSON()

	if statsJSON.CurrentUptimeMs < 15 {
		t.Errorf("Expected at least 15ms current uptime, got %d", statsJSON.CurrentUptimeMs)
	}

	if statsJSON.CurrentUptimeStr == "" {
		t.Error("Expected non-empty uptime string")
	}

	if statsJSON.TotalSessions != 1 {
		t.Errorf("Expected 1 session, got %d", statsJSON.TotalSessions)
	}
}

func TestUptimeTracker_Reset(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	ut.RecordStart(Follower, 1, "leader-1")
	ut.RecordStop("test")
	ut.RecordStart(Follower, 2, "leader-2")

	// Verify data exists
	if len(ut.sessions) != 1 || len(ut.events) < 2 {
		t.Fatal("Expected some history before reset")
	}

	ut.Reset()

	if len(ut.sessions) != 0 {
		t.Errorf("Expected 0 sessions after reset, got %d", len(ut.sessions))
	}

	if len(ut.events) != 0 {
		t.Errorf("Expected 0 events after reset, got %d", len(ut.events))
	}

	if ut.currentSession != nil {
		t.Error("Expected nil current session after reset")
	}
}

func TestUptimeTracker_TrimHistory(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)
	ut.maxEvents = 5
	ut.maxSessions = 3

	// Create more events and sessions than the limit
	for i := 0; i < 10; i++ {
		ut.RecordStart(Follower, uint64(i), "leader")
		time.Sleep(1 * time.Millisecond)
		ut.RecordStop("test")
	}

	if len(ut.events) > 5 {
		t.Errorf("Expected max 5 events, got %d", len(ut.events))
	}

	if len(ut.sessions) > 3 {
		t.Errorf("Expected max 3 sessions, got %d", len(ut.sessions))
	}
}

func TestUptimeTracker_MultipleSessions(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	// Session 1: 50ms
	ut.RecordStart(Follower, 1, "leader-1")
	time.Sleep(50 * time.Millisecond)
	ut.RecordStop("restart-1")

	// Session 2: 30ms
	ut.RecordStart(Follower, 2, "leader-2")
	time.Sleep(30 * time.Millisecond)
	ut.RecordStop("restart-2")

	// Session 3: 40ms (ongoing)
	ut.RecordStart(Follower, 3, "leader-3")
	time.Sleep(40 * time.Millisecond)

	stats := ut.GetStats()

	if stats.TotalSessions != 3 {
		t.Errorf("Expected 3 sessions, got %d", stats.TotalSessions)
	}

	// Total uptime should be at least 120ms
	if stats.TotalUptime < 100*time.Millisecond {
		t.Errorf("Expected at least 100ms total uptime, got %v", stats.TotalUptime)
	}

	// Longest session should be at least 45ms (first session)
	if stats.LongestSession < 45*time.Millisecond {
		t.Errorf("Expected longest session at least 45ms, got %v", stats.LongestSession)
	}

	// Shortest session should be around 30ms
	if stats.ShortestSession > 35*time.Millisecond || stats.ShortestSession < 25*time.Millisecond {
		t.Errorf("Expected shortest session around 30ms, got %v", stats.ShortestSession)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input    time.Duration
		expected string
	}{
		{0, "0s"},
		{5 * time.Second, "5s"},
		{65 * time.Second, "1m5s"},
		{3661 * time.Second, "1h1m1s"},
		{90061 * time.Second, "1d1h1m1s"},
		{48 * time.Hour, "2d"},
		{time.Hour + 30*time.Minute, "1h30m"},
	}

	for _, tc := range tests {
		result := formatDuration(tc.input)
		if result != tc.expected {
			t.Errorf("formatDuration(%v) = %s, expected %s", tc.input, result, tc.expected)
		}
	}
}

func TestUptimeTracker_MTBF(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	// Session 1 (closed with crash)
	ut.RecordStart(Follower, 1, "leader-1")
	time.Sleep(20 * time.Millisecond)
	ut.RecordCrash("crash-1")

	// Session 2 (closed with crash)
	ut.RecordStart(Follower, 2, "leader-2")
	time.Sleep(30 * time.Millisecond)
	ut.RecordCrash("crash-2")

	// Session 3 (ongoing)
	ut.RecordStart(Follower, 3, "leader-3")
	time.Sleep(10 * time.Millisecond)

	stats := ut.GetStats()

	// Should have 2 sessions with crash end reason
	crashCount := 0
	for _, s := range ut.sessions {
		if s.EndReason == "crash-1" || s.EndReason == "crash-2" {
			crashCount++
		}
	}

	// Crashes are counted from sessions after the first one
	// The first crash doesn't count (no previous session to compare)
	// So we expect TotalCrashes = number of sessions with "crash" reason - 1
	// But actually, our logic counts sessions AFTER i=0 that have crash reason
	if stats.TotalCrashes < 1 {
		t.Errorf("Expected at least 1 crash in stats, got %d", stats.TotalCrashes)
	}

	// MTBF should be total uptime / number of crashes
	if stats.TotalCrashes > 0 && stats.MTBF <= 0 {
		t.Error("Expected positive MTBF when crashes > 0")
	}
}

func TestUptimeTracker_StateTimeTacking(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	// Start as follower
	ut.RecordStart(Follower, 1, "leader-1")
	time.Sleep(20 * time.Millisecond)

	// Become candidate
	ut.RecordStateChange(Candidate, 2, "")
	time.Sleep(10 * time.Millisecond)

	// Become leader
	ut.RecordStateChange(Leader, 2, "test-node")
	time.Sleep(30 * time.Millisecond)

	// Become follower again
	ut.RecordStateChange(Follower, 3, "other-leader")
	time.Sleep(15 * time.Millisecond)

	ut.RecordStop("test")

	stats := ut.GetStats()

	// Check time spent in each state
	if stats.TotalFollowerTime < 30*time.Millisecond {
		t.Errorf("Expected at least 30ms follower time, got %v", stats.TotalFollowerTime)
	}

	if stats.TotalCandidateTime < 5*time.Millisecond {
		t.Errorf("Expected at least 5ms candidate time, got %v", stats.TotalCandidateTime)
	}

	if stats.TotalLeaderTime < 25*time.Millisecond {
		t.Errorf("Expected at least 25ms leader time, got %v", stats.TotalLeaderTime)
	}
}

func TestUptimeTracker_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	ut.RecordStart(Follower, 1, "leader-1")

	var wg sync.WaitGroup
	wg.Add(3)

	// Concurrent state changes
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			ut.RecordStateChange(Candidate, uint64(i), "")
			ut.RecordStateChange(Leader, uint64(i), "test-node")
			ut.RecordStateChange(Follower, uint64(i), "other")
		}
	}()

	// Concurrent stats reads
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = ut.GetStats()
			_ = ut.GetStatsJSON()
			_ = ut.GetHistory(10, 5)
		}
	}()

	// Concurrent uptime reads
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = ut.GetCurrentUptime()
		}
	}()

	// Wait for all goroutines
	wg.Wait()

	// Should complete without deadlock or panic
	ut.RecordStop("test")
}

func TestUptimeTracker_EmptyHistory(t *testing.T) {
	tmpDir := t.TempDir()
	logger := NewLogger("test")
	ut := NewUptimeTracker("test-node", tmpDir, nil, logger)

	stats := ut.GetStats()

	if stats.TotalSessions != 0 {
		t.Errorf("Expected 0 sessions, got %d", stats.TotalSessions)
	}

	if stats.TotalUptime != 0 {
		t.Errorf("Expected 0 uptime, got %v", stats.TotalUptime)
	}

	history := ut.GetHistory(100, 50)
	if len(history.Events) != 0 {
		t.Errorf("Expected 0 events, got %d", len(history.Events))
	}
}
