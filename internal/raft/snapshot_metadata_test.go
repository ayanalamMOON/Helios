package raft

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Helper function to create a test logger
func newTestLogger() *Logger {
	return NewLogger("test-node")
}

func TestNewSnapshotMetadataTracker(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()

	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	if tracker == nil {
		t.Fatal("Expected non-nil tracker")
	}

	if tracker.nodeID != "node-1" {
		t.Errorf("Expected nodeID 'node-1', got %s", tracker.nodeID)
	}

	if tracker.dataDir != tempDir {
		t.Errorf("Expected dataDir %s, got %s", tempDir, tracker.dataDir)
	}

	if tracker.snapshotInterval != 5*time.Minute {
		t.Errorf("Expected snapshotInterval 5m, got %v", tracker.snapshotInterval)
	}

	if tracker.snapshotThreshold != 1000 {
		t.Errorf("Expected snapshotThreshold 1000, got %d", tracker.snapshotThreshold)
	}
}

func TestSnapshotMetadataTracker_RecordSnapshotCreated(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	index := uint64(100)
	term := uint64(5)
	size := int64(1024000)
	duration := 500 * time.Millisecond
	entriesCompacted := uint64(50)
	leaderID := "leader-node"
	peers := []string{"peer-1", "peer-2"}

	tracker.RecordSnapshotCreated(index, term, size, duration, entriesCompacted, leaderID, peers)

	stats := tracker.GetStats()

	if stats.TotalSnapshotsCreated != 1 {
		t.Errorf("Expected 1 snapshot created, got %d", stats.TotalSnapshotsCreated)
	}

	if stats.TotalBytesWritten != size {
		t.Errorf("Expected %d bytes written, got %d", size, stats.TotalBytesWritten)
	}

	if stats.TotalEntriesCompacted != entriesCompacted {
		t.Errorf("Expected %d entries compacted, got %d", entriesCompacted, stats.TotalEntriesCompacted)
	}

	if stats.LatestSnapshot == nil {
		t.Fatal("Expected non-nil latest snapshot")
	}

	if stats.LatestSnapshot.Index != index {
		t.Errorf("Expected latest snapshot index %d, got %d", index, stats.LatestSnapshot.Index)
	}

	if stats.LatestSnapshot.Term != term {
		t.Errorf("Expected latest snapshot term %d, got %d", term, stats.LatestSnapshot.Term)
	}

	if stats.LatestSnapshot.LeaderID != leaderID {
		t.Errorf("Expected leader ID %s, got %s", leaderID, stats.LatestSnapshot.LeaderID)
	}

	history := tracker.GetHistory(100, 100)
	if len(history.Events) != 1 {
		t.Errorf("Expected 1 event in history, got %d", len(history.Events))
	}

	if len(history.Snapshots) != 1 {
		t.Errorf("Expected 1 snapshot in history, got %d", len(history.Snapshots))
	}

	// Check the event
	event := history.Events[0]
	if event.Type != "created" {
		t.Errorf("Expected event type 'created', got %s", event.Type)
	}
}

func TestSnapshotMetadataTracker_RecordSnapshotReceived(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	index := uint64(200)
	term := uint64(8)
	size := int64(2048000)
	duration := 300 * time.Millisecond
	fromPeerID := "leader-node"

	tracker.RecordSnapshotReceived(index, term, size, duration, fromPeerID)

	stats := tracker.GetStats()

	if stats.TotalSnapshotsReceived != 1 {
		t.Errorf("Expected 1 snapshot received, got %d", stats.TotalSnapshotsReceived)
	}

	history := tracker.GetHistory(100, 100)

	// Check the event
	if len(history.Events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(history.Events))
	}

	event := history.Events[0]
	if event.Type != "received" {
		t.Errorf("Expected event type 'received', got %s", event.Type)
	}
	if event.FromPeer != fromPeerID {
		t.Errorf("Expected from peer '%s', got %s", fromPeerID, event.FromPeer)
	}

	// Check the snapshot metadata
	if len(history.Snapshots) != 1 {
		t.Fatalf("Expected 1 snapshot, got %d", len(history.Snapshots))
	}

	snapshot := history.Snapshots[0]
	if snapshot.Index != index {
		t.Errorf("Expected snapshot index %d, got %d", index, snapshot.Index)
	}
	if !snapshot.IsFromPeer {
		t.Error("Expected snapshot to be from peer")
	}
}

func TestSnapshotMetadataTracker_RecordSnapshotRestored(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	index := uint64(300)
	term := uint64(10)
	duration := 200 * time.Millisecond

	// First create a snapshot so there's something to restore
	tracker.RecordSnapshotCreated(index, term, 1024, 100*time.Millisecond, 50, "leader", nil)

	// Now record restoration
	tracker.RecordSnapshotRestored(index, term, duration)

	stats := tracker.GetStats()

	if stats.TotalRestores != 1 {
		t.Errorf("Expected 1 restore, got %d", stats.TotalRestores)
	}

	history := tracker.GetHistory(100, 100)

	// Find the restored event
	foundRestored := false
	for _, event := range history.Events {
		if event.Type == "restored" {
			foundRestored = true
			break
		}
	}

	if !foundRestored {
		t.Error("Expected to find restored event in history")
	}
}

func TestSnapshotMetadataTracker_RecordSnapshotDeleted(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	index := uint64(400)
	term := uint64(12)

	// First create a snapshot
	tracker.RecordSnapshotCreated(index, term, 1024, 100*time.Millisecond, 10, "leader", nil)

	// Now delete it
	tracker.RecordSnapshotDeleted(index, term)

	history := tracker.GetHistory(100, 100)

	// Find the deleted event
	foundDeleted := false
	for _, event := range history.Events {
		if event.Type == "deleted" {
			foundDeleted = true
			if event.Index != index {
				t.Errorf("Expected index %d, got %d", index, event.Index)
			}
			break
		}
	}

	if !foundDeleted {
		t.Error("Expected to find deleted event in history")
	}
}

func TestSnapshotMetadataTracker_RecordSnapshotError(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	operation := "create"
	errMsg := "disk full"

	tracker.RecordSnapshotError(operation, 100, 5, errors.New(errMsg))

	stats := tracker.GetStats()

	if stats.FailedCreations != 1 {
		t.Errorf("Expected 1 failed creation, got %d", stats.FailedCreations)
	}

	// LastError includes operation prefix
	expectedLastError := operation + ": " + errMsg
	if stats.LastError != expectedLastError {
		t.Errorf("Expected last error '%s', got %s", expectedLastError, stats.LastError)
	}

	history := tracker.GetHistory(100, 100)

	// Check the error event
	if len(history.Events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(history.Events))
	}

	event := history.Events[0]
	if event.Type != "error" {
		t.Errorf("Expected event type 'error', got %s", event.Type)
	}
}

func TestSnapshotMetadataTracker_GetStatsJSON(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Create some activity
	tracker.RecordSnapshotCreated(500, 15, 2048000, 300*time.Millisecond, 100, "leader", nil)

	statsJSON := tracker.GetStatsJSON()

	if statsJSON == nil {
		t.Fatal("Expected non-nil stats JSON")
	}

	if !statsJSON.HasSnapshot {
		t.Error("Expected HasSnapshot to be true")
	}

	if statsJSON.TotalSnapshotsCreated != 1 {
		t.Errorf("Expected 1 snapshot created, got %d", statsJSON.TotalSnapshotsCreated)
	}

	if statsJSON.TotalBytesWritten != 2048000 {
		t.Errorf("Expected 2048000 bytes written, got %d", statsJSON.TotalBytesWritten)
	}

	if statsJSON.LatestSnapshotIndex != 500 {
		t.Errorf("Expected latest snapshot index 500, got %d", statsJSON.LatestSnapshotIndex)
	}

	// Verify it can be marshaled to JSON
	data, err := json.Marshal(statsJSON)
	if err != nil {
		t.Fatalf("Failed to marshal stats to JSON: %v", err)
	}

	if len(data) == 0 {
		t.Error("Expected non-empty JSON data")
	}
}

func TestSnapshotMetadataTracker_GetLatestSnapshot(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Initially should be nil
	latest := tracker.GetLatestSnapshot()
	if latest != nil {
		t.Error("Expected nil when no snapshots exist")
	}

	// Create first snapshot
	tracker.RecordSnapshotCreated(100, 5, 1024, 100*time.Millisecond, 10, "leader", nil)

	latest = tracker.GetLatestSnapshot()
	if latest == nil {
		t.Fatal("Expected non-nil after creating snapshot")
	}
	if latest.Index != 100 {
		t.Errorf("Expected index 100, got %d", latest.Index)
	}

	// Create second snapshot
	tracker.RecordSnapshotCreated(200, 8, 2048, 100*time.Millisecond, 20, "leader", nil)

	latest = tracker.GetLatestSnapshot()
	if latest == nil {
		t.Fatal("Expected non-nil after creating second snapshot")
	}
	if latest.Index != 200 {
		t.Errorf("Expected index 200, got %d", latest.Index)
	}
}

func TestSnapshotMetadataTracker_GetAllSnapshots(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Initially should be empty
	all := tracker.GetAllSnapshots()
	if len(all) != 0 {
		t.Errorf("Expected 0 snapshots, got %d", len(all))
	}

	// Create multiple snapshots
	for i := 1; i <= 5; i++ {
		tracker.RecordSnapshotCreated(uint64(i*100), uint64(i), int64(i*1024), 100*time.Millisecond, uint64(i*10), "leader", nil)
	}

	all = tracker.GetAllSnapshots()
	if len(all) != 5 {
		t.Errorf("Expected 5 snapshots, got %d", len(all))
	}
}

func TestSnapshotMetadataTracker_Reset(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Create some activity
	tracker.RecordSnapshotCreated(100, 5, 1024, 100*time.Millisecond, 10, "leader", nil)
	tracker.RecordSnapshotError("restore", 100, 5, errors.New("test error"))

	// Verify stats exist
	stats := tracker.GetStats()
	if stats.TotalSnapshotsCreated == 0 {
		t.Error("Expected non-zero stats before reset")
	}

	// Reset
	tracker.Reset()

	// Verify stats are cleared
	stats = tracker.GetStats()
	if stats.TotalSnapshotsCreated != 0 {
		t.Errorf("Expected 0 snapshots created after reset, got %d", stats.TotalSnapshotsCreated)
	}
	if stats.FailedCreations != 0 {
		t.Errorf("Expected 0 failed creations after reset, got %d", stats.FailedCreations)
	}

	history := tracker.GetHistory(100, 100)
	if len(history.Events) != 0 {
		t.Errorf("Expected 0 events after reset, got %d", len(history.Events))
	}
	if len(history.Snapshots) != 0 {
		t.Errorf("Expected 0 snapshots after reset, got %d", len(history.Snapshots))
	}
}

func TestSnapshotMetadataTracker_Persistence(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()

	// Create tracker and add some data
	tracker1 := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	tracker1.RecordSnapshotCreated(500, 10, 4096000, 250*time.Millisecond, 200, "leader", nil)
	tracker1.RecordSnapshotRestored(500, 10, 100*time.Millisecond)

	// Force save
	tracker1.Flush()

	// Create new tracker and verify it loads the data
	tracker2 := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	stats := tracker2.GetStats()
	if stats.TotalSnapshotsCreated != 1 {
		t.Errorf("Expected 1 snapshot created after reload, got %d", stats.TotalSnapshotsCreated)
	}
	if stats.TotalRestores != 1 {
		t.Errorf("Expected 1 restore after reload, got %d", stats.TotalRestores)
	}
	if stats.TotalBytesWritten != 4096000 {
		t.Errorf("Expected 4096000 bytes written after reload, got %d", stats.TotalBytesWritten)
	}
}

func TestSnapshotMetadataTracker_PersistenceFileExists(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	tracker.RecordSnapshotCreated(100, 5, 1024, 100*time.Millisecond, 10, "leader", nil)
	tracker.Flush()

	// Verify file exists
	filePath := filepath.Join(tempDir, "snapshot-stats.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("Expected snapshot-stats.json file to exist")
	}

	// Verify file contains valid JSON
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read snapshot-stats.json: %v", err)
	}

	var jsonData map[string]interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		t.Errorf("Failed to unmarshal JSON: %v", err)
	}
}

func TestSnapshotMetadataTracker_ConcurrentAccess(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	numGoroutines := 10
	numOpsPerGoroutine := 100

	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				tracker.RecordSnapshotCreated(uint64(id*1000+j), uint64(id), 1024, time.Millisecond, 10, "leader", nil)
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				_ = tracker.GetStats()
				_ = tracker.GetStatsJSON()
				_ = tracker.GetHistory(10, 10)
				_ = tracker.GetLatestSnapshot()
			}
		}()
	}

	wg.Wait()

	// Verify no data corruption - total should be numGoroutines * numOpsPerGoroutine
	stats := tracker.GetStats()
	expected := numGoroutines * numOpsPerGoroutine
	if stats.TotalSnapshotsCreated != expected {
		t.Errorf("Expected %d snapshots created, got %d", expected, stats.TotalSnapshotsCreated)
	}
}

func TestSnapshotMetadataTracker_HistoryMaxSize(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Add more than maxHistoryEntries events
	maxEvents := 1000
	for i := 0; i < maxEvents+100; i++ {
		tracker.RecordSnapshotCreated(uint64(i), 1, 100, time.Millisecond, 1, "leader", nil)
	}

	// Request all events
	history := tracker.GetHistory(maxEvents*2, 200)

	// Should be capped at maxHistoryEntries
	if len(history.Events) > maxEvents {
		t.Errorf("Expected at most %d events, got %d", maxEvents, len(history.Events))
	}
}

func TestSnapshotMetadataTracker_SnapshotsMaxSize(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Add more than maxSnapshotsTracked unique snapshots
	maxSnapshots := 100
	for i := 0; i < maxSnapshots+50; i++ {
		// Use unique index for each snapshot
		tracker.RecordSnapshotCreated(uint64(i), uint64(1), 100, time.Millisecond, 1, "leader", nil)
	}

	// Request all snapshots
	history := tracker.GetHistory(2000, maxSnapshots*2)

	// Should be capped at maxSnapshotsTracked
	if len(history.Snapshots) > maxSnapshots {
		t.Errorf("Expected at most %d snapshots, got %d", maxSnapshots, len(history.Snapshots))
	}
}

func TestSnapshotMetadataTracker_AverageTimingCalculations(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Create multiple snapshots with different durations
	durations := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		300 * time.Millisecond,
	}

	for i, dur := range durations {
		tracker.RecordSnapshotCreated(uint64(i*100), 1, 1024, dur, 10, "leader", nil)
	}

	stats := tracker.GetStats()

	// Average should be (100+200+300)/3 = 200ms
	expectedAvgMs := 200.0
	if stats.AvgCreationTimeMs != expectedAvgMs {
		t.Errorf("Expected avg creation time %.2f ms, got %.2f ms", expectedAvgMs, stats.AvgCreationTimeMs)
	}
}

func TestSnapshotMetadataTracker_MinMaxTimingCalculations(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Create snapshots with known durations
	durations := []time.Duration{
		150 * time.Millisecond,
		50 * time.Millisecond,  // min
		300 * time.Millisecond, // max
		100 * time.Millisecond,
	}

	for i, dur := range durations {
		tracker.RecordSnapshotCreated(uint64(i*100), 1, 1024, dur, 10, "leader", nil)
	}

	stats := tracker.GetStats()

	expectedMinMs := 50.0
	expectedMaxMs := 300.0

	if stats.MinCreationTimeMs != expectedMinMs {
		t.Errorf("Expected min creation time %.2f ms, got %.2f ms", expectedMinMs, stats.MinCreationTimeMs)
	}

	if stats.MaxCreationTimeMs != expectedMaxMs {
		t.Errorf("Expected max creation time %.2f ms, got %.2f ms", expectedMaxMs, stats.MaxCreationTimeMs)
	}
}

func TestSnapshotMetadataTracker_RestoreTimingCalculations(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Create a snapshot first
	tracker.RecordSnapshotCreated(100, 1, 1024, 50*time.Millisecond, 10, "leader", nil)

	// Restore multiple times with different durations
	durations := []time.Duration{
		50 * time.Millisecond,
		100 * time.Millisecond,
		150 * time.Millisecond,
	}

	for _, dur := range durations {
		tracker.RecordSnapshotRestored(100, 1, dur)
	}

	stats := tracker.GetStats()

	// Average should be (50+100+150)/3 = 100ms
	expectedAvgMs := 100.0
	if stats.AvgRestoreTimeMs != expectedAvgMs {
		t.Errorf("Expected avg restore time %.2f ms, got %.2f ms", expectedAvgMs, stats.AvgRestoreTimeMs)
	}

	if stats.TotalRestores != 3 {
		t.Errorf("Expected 3 restores, got %d", stats.TotalRestores)
	}
}

func TestSnapshotMetadataTracker_MultipleErrors(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Record multiple errors
	errs := []struct {
		operation string
		errMsg    string
	}{
		{"create", "disk full"},
		{"restore", "corrupt data"},
		{"create", "network timeout"},
	}

	for _, e := range errs {
		tracker.RecordSnapshotError(e.operation, 100, 5, errors.New(e.errMsg))
	}

	stats := tracker.GetStats()

	// 2 create errors + 1 restore error
	expectedCreationErrors := 2
	expectedRestoreErrors := 1

	if stats.FailedCreations != expectedCreationErrors {
		t.Errorf("Expected %d failed creations, got %d", expectedCreationErrors, stats.FailedCreations)
	}

	if stats.FailedRestores != expectedRestoreErrors {
		t.Errorf("Expected %d failed restores, got %d", expectedRestoreErrors, stats.FailedRestores)
	}

	// Last error should be the most recent with operation prefix
	expectedLastError := "create: network timeout"
	if stats.LastError != expectedLastError {
		t.Errorf("Expected last error '%s', got %s", expectedLastError, stats.LastError)
	}
}

func TestSnapshotMetadataTracker_HistoryEventDetails(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	index := uint64(999)
	term := uint64(42)
	duration := 123 * time.Millisecond

	tracker.RecordSnapshotCreated(index, term, 5000, duration, 50, "leader", nil)

	history := tracker.GetHistory(10, 10)

	if len(history.Events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(history.Events))
	}

	event := history.Events[0]

	if event.Index != index {
		t.Errorf("Expected index %d, got %d", index, event.Index)
	}

	if event.Term != term {
		t.Errorf("Expected term %d, got %d", term, event.Term)
	}

	if event.DurationMs != float64(duration.Milliseconds()) {
		t.Errorf("Expected duration %.2f ms, got %.2f ms", float64(duration.Milliseconds()), event.DurationMs)
	}

	if !event.Success {
		t.Error("Expected success to be true for created event")
	}
}

func TestSnapshotMetadataTracker_UpdateFromSnapshotStore(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Simulate syncing with existing snapshots on disk
	existing := []SnapshotMeta{
		{Index: 100, Term: 1},
		{Index: 200, Term: 2},
		{Index: 300, Term: 3},
	}

	tracker.UpdateFromSnapshotStore(existing)

	history := tracker.GetHistory(100, 100)

	if len(history.Snapshots) != 3 {
		t.Errorf("Expected 3 snapshots after sync, got %d", len(history.Snapshots))
	}
}

func TestSnapshotMetadataTracker_EmptyHistory(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	history := tracker.GetHistory(100, 100)

	if history == nil {
		t.Fatal("Expected non-nil history")
	}

	if history.Events == nil {
		t.Error("Expected non-nil events slice")
	}

	if len(history.Events) != 0 {
		t.Errorf("Expected 0 events, got %d", len(history.Events))
	}

	if history.Snapshots == nil {
		t.Error("Expected non-nil snapshots slice")
	}

	if len(history.Snapshots) != 0 {
		t.Errorf("Expected 0 snapshots, got %d", len(history.Snapshots))
	}
}

func TestSnapshotMetadataTracker_GetHistoryLimits(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Create 20 events
	for i := 0; i < 20; i++ {
		tracker.RecordSnapshotCreated(uint64(i), 1, 100, time.Millisecond, 1, "leader", nil)
	}

	// Request only 5 events
	history := tracker.GetHistory(5, 5)

	if len(history.Events) != 5 {
		t.Errorf("Expected 5 events, got %d", len(history.Events))
	}
}

func TestSnapshotMetadataTracker_SetConfiguration(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	newInterval := 10 * time.Minute
	newThreshold := uint64(2000)
	newRetention := 5

	tracker.SetConfiguration(newInterval, newThreshold, newRetention)

	// Verify through GetStats
	stats := tracker.GetStats()
	if stats.SnapshotInterval != "10m0s" {
		t.Errorf("Expected interval '10m0s', got %s", stats.SnapshotInterval)
	}
	if stats.SnapshotThreshold != newThreshold {
		t.Errorf("Expected threshold %d, got %d", newThreshold, stats.SnapshotThreshold)
	}
	if stats.RetentionCount != newRetention {
		t.Errorf("Expected retention %d, got %d", newRetention, stats.RetentionCount)
	}
}

func TestSnapshotHistoryEntry_Fields(t *testing.T) {
	entry := SnapshotHistoryEntry{
		Timestamp:  time.Now(),
		Type:       "created",
		SnapshotID: "test-snap",
		Index:      100,
		Term:       5,
		Size:       1024,
		DurationMs: 100.0,
		Success:    true,
	}

	if entry.Type != "created" {
		t.Error("Type field mismatch")
	}

	if entry.SnapshotID != "test-snap" {
		t.Error("SnapshotID field mismatch")
	}

	if entry.Index != 100 {
		t.Error("Index field mismatch")
	}

	if entry.Term != 5 {
		t.Error("Term field mismatch")
	}

	if entry.Size != 1024 {
		t.Error("Size field mismatch")
	}

	if entry.DurationMs != 100.0 {
		t.Error("DurationMs field mismatch")
	}

	if !entry.Success {
		t.Error("Success field mismatch")
	}
}

func TestSnapshotMetadata_Fields(t *testing.T) {
	now := time.Now()
	meta := SnapshotMetadata{
		ID:               "meta-test",
		Index:            500,
		Term:             10,
		Size:             2048000,
		CreatedAt:        now,
		CreationDuration: 200 * time.Millisecond,
		NodeID:           "node-1",
		IsFromPeer:       false,
		EntriesCompacted: 100,
	}

	if meta.ID != "meta-test" {
		t.Error("ID field mismatch")
	}

	if meta.Index != 500 {
		t.Error("Index field mismatch")
	}

	if meta.Term != 10 {
		t.Error("Term field mismatch")
	}

	if meta.Size != 2048000 {
		t.Error("Size field mismatch")
	}

	if meta.IsFromPeer {
		t.Error("IsFromPeer field mismatch")
	}

	if meta.EntriesCompacted != 100 {
		t.Error("EntriesCompacted field mismatch")
	}
}

func TestSnapshotStats_Fields(t *testing.T) {
	stats := SnapshotStats{
		TotalSnapshots:         5,
		TotalSnapshotsCreated:  3,
		TotalSnapshotsReceived: 2,
		TotalBytesWritten:      5000000,
		TotalRestores:          2,
		AvgCreationTimeMs:      150.5,
		MaxCreationTimeMs:      300.0,
		MinCreationTimeMs:      50.0,
	}

	if stats.TotalSnapshots != 5 {
		t.Error("TotalSnapshots mismatch")
	}

	if stats.TotalSnapshotsCreated != 3 {
		t.Error("TotalSnapshotsCreated mismatch")
	}

	if stats.TotalSnapshotsReceived != 2 {
		t.Error("TotalSnapshotsReceived mismatch")
	}

	if stats.TotalBytesWritten != 5000000 {
		t.Error("TotalBytesWritten mismatch")
	}
}

func TestSnapshotStatsJSON_Fields(t *testing.T) {
	statsJSON := SnapshotStatsJSON{
		HasSnapshot:           true,
		LatestSnapshotID:      "5-100",
		LatestSnapshotIndex:   100,
		LatestSnapshotTerm:    5,
		TotalSnapshotsCreated: 10,
		TotalBytesWritten:     10000000,
	}

	if !statsJSON.HasSnapshot {
		t.Error("HasSnapshot mismatch")
	}

	if statsJSON.LatestSnapshotID != "5-100" {
		t.Error("LatestSnapshotID mismatch")
	}

	if statsJSON.LatestSnapshotIndex != 100 {
		t.Error("LatestSnapshotIndex mismatch")
	}

	if statsJSON.TotalSnapshotsCreated != 10 {
		t.Error("TotalSnapshotsCreated mismatch")
	}
}

func TestSnapshotMetadataTracker_Close(t *testing.T) {
	tempDir := t.TempDir()
	logger := newTestLogger()
	tracker := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)

	// Add some data
	tracker.RecordSnapshotCreated(100, 5, 1024, 100*time.Millisecond, 10, "leader", nil)

	// Close should save and not panic
	err := tracker.Close()
	if err != nil {
		t.Errorf("Close returned error: %v", err)
	}

	// Verify data was persisted by creating a new tracker
	tracker2 := NewSnapshotMetadataTracker("node-1", tempDir, 5*time.Minute, 1000, logger)
	stats := tracker2.GetStats()
	if stats.TotalSnapshotsCreated != 1 {
		t.Errorf("Expected data to be persisted after Close, got %d snapshots", stats.TotalSnapshotsCreated)
	}
}
