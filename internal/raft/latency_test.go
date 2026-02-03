package raft

import (
	"math"
	"testing"
	"time"
)

func TestCircularBuffer(t *testing.T) {
	t.Run("basic operations", func(t *testing.T) {
		cb := NewCircularBuffer(5)

		if cb.Count() != 0 {
			t.Errorf("Expected count 0, got %d", cb.Count())
		}

		// Add some samples
		cb.Add(10 * time.Millisecond)
		cb.Add(20 * time.Millisecond)
		cb.Add(30 * time.Millisecond)

		if cb.Count() != 3 {
			t.Errorf("Expected count 3, got %d", cb.Count())
		}

		samples := cb.GetSamples()
		if len(samples) != 3 {
			t.Errorf("Expected 3 samples, got %d", len(samples))
		}

		if samples[0] != 10*time.Millisecond || samples[1] != 20*time.Millisecond || samples[2] != 30*time.Millisecond {
			t.Errorf("Unexpected samples: %v", samples)
		}
	})

	t.Run("overflow behavior", func(t *testing.T) {
		cb := NewCircularBuffer(3)

		// Add more samples than buffer size
		cb.Add(10 * time.Millisecond)
		cb.Add(20 * time.Millisecond)
		cb.Add(30 * time.Millisecond)
		cb.Add(40 * time.Millisecond)
		cb.Add(50 * time.Millisecond)

		if cb.Count() != 3 {
			t.Errorf("Expected count 3 (buffer size), got %d", cb.Count())
		}

		samples := cb.GetSamples()
		// Should have the most recent 3 samples in order
		if samples[0] != 30*time.Millisecond || samples[1] != 40*time.Millisecond || samples[2] != 50*time.Millisecond {
			t.Errorf("Expected [30ms, 40ms, 50ms], got %v", samples)
		}
	})

	t.Run("empty buffer", func(t *testing.T) {
		cb := NewCircularBuffer(5)
		samples := cb.GetSamples()
		if samples != nil {
			t.Errorf("Expected nil for empty buffer, got %v", samples)
		}
	})
}

func TestPeerLatencyTracker(t *testing.T) {
	t.Run("single peer tracking", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		// Record some latencies
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 20*time.Millisecond)
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 30*time.Millisecond)

		stats, exists := tracker.GetPeerStats("peer1")
		if !exists {
			t.Fatal("Expected peer1 to exist")
		}

		if stats.PeerID != "peer1" {
			t.Errorf("Expected peer_id 'peer1', got '%s'", stats.PeerID)
		}

		if stats.Address != "127.0.0.1:8000" {
			t.Errorf("Expected address '127.0.0.1:8000', got '%s'", stats.Address)
		}

		// Check append entries stats
		if stats.AppendEntries.Count != 3 {
			t.Errorf("Expected 3 append entries samples, got %d", stats.AppendEntries.Count)
		}

		if stats.AppendEntries.AvgValueMs != 20 {
			t.Errorf("Expected avg latency 20ms, got %.2fms", stats.AppendEntries.AvgValueMs)
		}

		if stats.AppendEntries.MinValueMs != 10 {
			t.Errorf("Expected min latency 10ms, got %.2fms", stats.AppendEntries.MinValueMs)
		}

		if stats.AppendEntries.MaxValueMs != 30 {
			t.Errorf("Expected max latency 30ms, got %.2fms", stats.AppendEntries.MaxValueMs)
		}
	})

	t.Run("multiple RPC types", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		// Record different RPC types
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeRequestVote, 5*time.Millisecond)
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeInstallSnapshot, 100*time.Millisecond)

		stats, _ := tracker.GetPeerStats("peer1")

		if stats.AppendEntries.Count != 1 {
			t.Errorf("Expected 1 append entries sample, got %d", stats.AppendEntries.Count)
		}

		if stats.RequestVote.Count != 1 {
			t.Errorf("Expected 1 request vote sample, got %d", stats.RequestVote.Count)
		}

		if stats.InstallSnapshot.Count != 1 {
			t.Errorf("Expected 1 install snapshot sample, got %d", stats.InstallSnapshot.Count)
		}
	})

	t.Run("multiple peers", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
		tracker.RecordLatency("peer2", "127.0.0.1:8001", RPCTypeAppendEntries, 20*time.Millisecond)
		tracker.RecordLatency("peer3", "127.0.0.1:8002", RPCTypeAppendEntries, 30*time.Millisecond)

		allStats := tracker.GetAllPeerStats()

		if len(allStats) != 3 {
			t.Errorf("Expected 3 peers, got %d", len(allStats))
		}

		if _, exists := allStats["peer1"]; !exists {
			t.Error("Expected peer1 to exist in all stats")
		}

		if _, exists := allStats["peer2"]; !exists {
			t.Error("Expected peer2 to exist in all stats")
		}

		if _, exists := allStats["peer3"]; !exists {
			t.Error("Expected peer3 to exist in all stats")
		}
	})

	t.Run("error tracking", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		// Record some successful latencies
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 20*time.Millisecond)

		// Record errors
		tracker.RecordError("peer1", "127.0.0.1:8000", RPCTypeAppendEntries)
		tracker.RecordError("peer1", "127.0.0.1:8000", RPCTypeAppendEntries)

		stats, _ := tracker.GetPeerStats("peer1")

		if stats.AggregatedLatency.ErrorCount != 2 {
			t.Errorf("Expected 2 errors, got %d", stats.AggregatedLatency.ErrorCount)
		}

		if stats.ConsecutiveErrors != 2 {
			t.Errorf("Expected 2 consecutive errors, got %d", stats.ConsecutiveErrors)
		}

		// Successful RPC should reset consecutive errors
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
		stats, _ = tracker.GetPeerStats("peer1")

		if stats.ConsecutiveErrors != 0 {
			t.Errorf("Expected 0 consecutive errors after success, got %d", stats.ConsecutiveErrors)
		}
	})

	t.Run("health status", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		// Add a healthy peer
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)

		// Add an unhealthy peer (3+ consecutive errors)
		tracker.RecordError("peer2", "127.0.0.1:8001", RPCTypeAppendEntries)
		tracker.RecordError("peer2", "127.0.0.1:8001", RPCTypeAppendEntries)
		tracker.RecordError("peer2", "127.0.0.1:8001", RPCTypeAppendEntries)

		healthy, unhealthy, total := tracker.GetHealthySummary()

		if total != 2 {
			t.Errorf("Expected 2 total peers, got %d", total)
		}

		if healthy != 1 {
			t.Errorf("Expected 1 healthy peer, got %d", healthy)
		}

		if unhealthy != 1 {
			t.Errorf("Expected 1 unhealthy peer, got %d", unhealthy)
		}
	})

	t.Run("aggregated stats", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		// Add latencies for multiple peers
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
		tracker.RecordLatency("peer2", "127.0.0.1:8001", RPCTypeAppendEntries, 30*time.Millisecond)

		aggregated := tracker.GetAggregatedStats()

		if aggregated.Count != 2 {
			t.Errorf("Expected 2 total samples, got %d", aggregated.Count)
		}

		if aggregated.MinValueMs != 10 {
			t.Errorf("Expected min latency 10ms, got %.2fms", aggregated.MinValueMs)
		}

		if aggregated.MaxValueMs != 30 {
			t.Errorf("Expected max latency 30ms, got %.2fms", aggregated.MaxValueMs)
		}

		expectedAvg := 20.0 // (10 + 30) / 2
		if aggregated.AvgValueMs != expectedAvg {
			t.Errorf("Expected avg latency %.2fms, got %.2fms", expectedAvg, aggregated.AvgValueMs)
		}
	})

	t.Run("reset", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
		tracker.RecordLatency("peer2", "127.0.0.1:8001", RPCTypeAppendEntries, 20*time.Millisecond)

		// Verify data exists
		allStats := tracker.GetAllPeerStats()
		if len(allStats) != 2 {
			t.Errorf("Expected 2 peers before reset, got %d", len(allStats))
		}

		// Reset
		tracker.Reset()

		// Check that trackers still exist but are empty
		allStats = tracker.GetAllPeerStats()
		if len(allStats) != 2 {
			t.Errorf("Expected 2 peers after reset (trackers preserved), got %d", len(allStats))
		}

		for _, stats := range allStats {
			if stats.AggregatedLatency.Count != 0 {
				t.Errorf("Expected 0 samples after reset, got %d", stats.AggregatedLatency.Count)
			}
		}
	})

	t.Run("add and remove peer", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		tracker.AddPeer("peer1", "127.0.0.1:8000")
		tracker.AddPeer("peer2", "127.0.0.1:8001")

		allStats := tracker.GetAllPeerStats()
		if len(allStats) != 2 {
			t.Errorf("Expected 2 peers, got %d", len(allStats))
		}

		tracker.RemovePeer("peer1")

		allStats = tracker.GetAllPeerStats()
		if len(allStats) != 1 {
			t.Errorf("Expected 1 peer after removal, got %d", len(allStats))
		}

		if _, exists := allStats["peer1"]; exists {
			t.Error("peer1 should have been removed")
		}
	})

	t.Run("peer not found", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		_, exists := tracker.GetPeerStats("nonexistent")
		if exists {
			t.Error("Expected nonexistent peer to not be found")
		}
	})
}

func TestLatencyStats(t *testing.T) {
	t.Run("percentile calculations", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		// Add samples that give us clear percentile values
		for i := 1; i <= 100; i++ {
			tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, time.Duration(i)*time.Millisecond)
		}

		stats, _ := tracker.GetPeerStats("peer1")

		// P50 should be around 50
		if stats.AppendEntries.P50ValueMs < 49 || stats.AppendEntries.P50ValueMs > 51 {
			t.Errorf("Expected P50 around 50ms, got %.2fms", stats.AppendEntries.P50ValueMs)
		}

		// P90 should be around 90
		if stats.AppendEntries.P90ValueMs < 89 || stats.AppendEntries.P90ValueMs > 91 {
			t.Errorf("Expected P90 around 90ms, got %.2fms", stats.AppendEntries.P90ValueMs)
		}

		// P99 should be around 99
		if stats.AppendEntries.P99ValueMs < 98 || stats.AppendEntries.P99ValueMs > 100 {
			t.Errorf("Expected P99 around 99ms, got %.2fms", stats.AppendEntries.P99ValueMs)
		}
	})

	t.Run("standard deviation", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		// Add identical samples - std dev should be 0
		for i := 0; i < 10; i++ {
			tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
		}

		stats, _ := tracker.GetPeerStats("peer1")

		if stats.AppendEntries.StdDevMs != 0 {
			t.Errorf("Expected std dev 0 for identical samples, got %.2fms", stats.AppendEntries.StdDevMs)
		}

		// Add varied samples
		tracker2 := NewPeerLatencyTracker(100, nil)
		tracker2.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
		tracker2.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 20*time.Millisecond)
		tracker2.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 30*time.Millisecond)

		stats2, _ := tracker2.GetPeerStats("peer1")

		if stats2.AppendEntries.StdDevMs == 0 {
			t.Error("Expected non-zero std dev for varied samples")
		}
	})
}

func TestLatencyStatsJSON(t *testing.T) {
	t.Run("ToJSON conversion", func(t *testing.T) {
		stats := LatencyStats{
			Count:      100,
			LastValue:  50 * time.Millisecond,
			MinValue:   10 * time.Millisecond,
			MaxValue:   100 * time.Millisecond,
			AvgValue:   50 * time.Millisecond,
			P50Value:   50 * time.Millisecond,
			P90Value:   90 * time.Millisecond,
			P99Value:   99 * time.Millisecond,
			StdDev:     20 * time.Millisecond,
			ErrorCount: 5,
			LastError:  time.Now(),
		}

		json := stats.ToJSON()

		if json.Count != 100 {
			t.Errorf("Expected count 100, got %d", json.Count)
		}

		if json.LastValueMs != 50 {
			t.Errorf("Expected last value 50ms, got %.2fms", json.LastValueMs)
		}

		if json.MinValueMs != 10 {
			t.Errorf("Expected min value 10ms, got %.2fms", json.MinValueMs)
		}

		if json.MaxValueMs != 100 {
			t.Errorf("Expected max value 100ms, got %.2fms", json.MaxValueMs)
		}

		if json.ErrorCount != 5 {
			t.Errorf("Expected error count 5, got %d", json.ErrorCount)
		}

		if json.LastErrorUnix == 0 {
			t.Error("Expected LastErrorUnix to be set")
		}
	})

	t.Run("ToJSON with zero error time", func(t *testing.T) {
		stats := LatencyStats{
			Count:     10,
			LastError: time.Time{}, // Zero time
		}

		json := stats.ToJSON()

		if json.LastErrorUnix != 0 {
			t.Errorf("Expected LastErrorUnix to be 0 for zero time, got %d", json.LastErrorUnix)
		}
	})
}

func TestRPCTypes(t *testing.T) {
	// Verify RPC type constants
	if RPCTypeAppendEntries != "append_entries" {
		t.Errorf("Expected RPCTypeAppendEntries to be 'append_entries', got '%s'", RPCTypeAppendEntries)
	}

	if RPCTypeRequestVote != "request_vote" {
		t.Errorf("Expected RPCTypeRequestVote to be 'request_vote', got '%s'", RPCTypeRequestVote)
	}

	if RPCTypeInstallSnapshot != "install_snapshot" {
		t.Errorf("Expected RPCTypeInstallSnapshot to be 'install_snapshot', got '%s'", RPCTypeInstallSnapshot)
	}
}

func TestPercentileIndex(t *testing.T) {
	tests := []struct {
		length     int
		percentile int
		expected   int
	}{
		{100, 50, 50},
		{100, 90, 90},
		{100, 99, 99},
		{100, 100, 99}, // Should cap at length - 1
		{10, 50, 5},
		{10, 90, 9},
		{1, 50, 0},
		{1, 99, 0},
	}

	for _, test := range tests {
		result := percentileIndex(test.length, test.percentile)
		if result != test.expected {
			t.Errorf("percentileIndex(%d, %d) = %d, expected %d",
				test.length, test.percentile, result, test.expected)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	tracker := NewPeerLatencyTracker(100, nil)

	// Run concurrent operations
	done := make(chan bool)

	// Writer goroutine 1
	go func() {
		for i := 0; i < 100; i++ {
			tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, time.Duration(i)*time.Millisecond)
		}
		done <- true
	}()

	// Writer goroutine 2
	go func() {
		for i := 0; i < 100; i++ {
			tracker.RecordLatency("peer2", "127.0.0.1:8001", RPCTypeRequestVote, time.Duration(i)*time.Millisecond)
		}
		done <- true
	}()

	// Error writer goroutine
	go func() {
		for i := 0; i < 50; i++ {
			tracker.RecordError("peer1", "127.0.0.1:8000", RPCTypeAppendEntries)
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			tracker.GetAllPeerStats()
			tracker.GetAggregatedStats()
			tracker.GetHealthySummary()
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 4; i++ {
		<-done
	}

	// Verify no panic occurred and data is reasonable
	allStats := tracker.GetAllPeerStats()
	if len(allStats) != 2 {
		t.Errorf("Expected 2 peers, got %d", len(allStats))
	}
}

func TestConnectionHealth(t *testing.T) {
	t.Run("healthy connection", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)

		stats, _ := tracker.GetPeerStats("peer1")

		if !stats.Reachable {
			t.Error("Expected peer to be reachable")
		}

		if !stats.ConnectionHealthy {
			t.Error("Expected connection to be healthy")
		}
	})

	t.Run("unhealthy after consecutive errors", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		// Record 3 consecutive errors
		tracker.RecordError("peer1", "127.0.0.1:8000", RPCTypeAppendEntries)
		tracker.RecordError("peer1", "127.0.0.1:8000", RPCTypeAppendEntries)
		tracker.RecordError("peer1", "127.0.0.1:8000", RPCTypeAppendEntries)

		stats, _ := tracker.GetPeerStats("peer1")

		if stats.Reachable {
			t.Error("Expected peer to be unreachable after 3+ errors")
		}

		if stats.ConnectionHealthy {
			t.Error("Expected connection to be unhealthy after 3+ errors")
		}
	})

	t.Run("health recovery after success", func(t *testing.T) {
		tracker := NewPeerLatencyTracker(100, nil)

		// Make peer unhealthy
		tracker.RecordError("peer1", "127.0.0.1:8000", RPCTypeAppendEntries)
		tracker.RecordError("peer1", "127.0.0.1:8000", RPCTypeAppendEntries)
		tracker.RecordError("peer1", "127.0.0.1:8000", RPCTypeAppendEntries)

		// Recover with success
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)

		stats, _ := tracker.GetPeerStats("peer1")

		if !stats.Reachable {
			t.Error("Expected peer to be reachable after successful RPC")
		}
	})
}

func TestQuickStats(t *testing.T) {
	tracker := NewPeerLatencyTracker(100, nil)

	// Record some data
	tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
	tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 20*time.Millisecond)
	tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 30*time.Millisecond)

	avgLatency, errorRate, isHealthy, exists := tracker.GetPeerQuickStats("peer1")

	if !exists {
		t.Fatal("Expected peer to exist")
	}

	expectedAvg := 20 * time.Millisecond
	if avgLatency != expectedAvg {
		t.Errorf("Expected avg latency %v, got %v", expectedAvg, avgLatency)
	}

	if errorRate != 0 {
		t.Errorf("Expected 0 error rate, got %f", errorRate)
	}

	if !isHealthy {
		t.Error("Expected peer to be healthy")
	}

	// Test nonexistent peer
	_, _, _, exists = tracker.GetPeerQuickStats("nonexistent")
	if exists {
		t.Error("Expected nonexistent peer to not exist")
	}
}

func TestMinMaxTracking(t *testing.T) {
	// Test that min/max values are tracked correctly
	tracker := NewPeerLatencyTracker(3, nil) // Small buffer to test overflow

	// Add values
	tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 50*time.Millisecond)
	tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
	tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 100*time.Millisecond)

	stats, _ := tracker.GetPeerStats("peer1")

	if stats.AppendEntries.MinValueMs != 10 {
		t.Errorf("Expected min 10ms, got %.2fms", stats.AppendEntries.MinValueMs)
	}

	if stats.AppendEntries.MaxValueMs != 100 {
		t.Errorf("Expected max 100ms, got %.2fms", stats.AppendEntries.MaxValueMs)
	}

	// Add more values (buffer overflow)
	tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 200*time.Millisecond)
	tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 5*time.Millisecond)

	stats, _ = tracker.GetPeerStats("peer1")

	// After overflow, min/max should still reflect all-time values
	// Note: Current implementation recalculates from buffer, so this tests buffer behavior
	if stats.AppendEntries.MinValueMs != 5 {
		t.Errorf("Expected min 5ms after new min added, got %.2fms", stats.AppendEntries.MinValueMs)
	}

	if stats.AppendEntries.MaxValueMs != 200 {
		t.Errorf("Expected max 200ms after new max added, got %.2fms", stats.AppendEntries.MaxValueMs)
	}
}

func TestEmptyStats(t *testing.T) {
	tracker := NewPeerLatencyTracker(100, nil)

	// Add peer but don't record any latencies
	tracker.AddPeer("peer1", "127.0.0.1:8000")

	stats, _ := tracker.GetPeerStats("peer1")

	// All values should be zero/empty
	if stats.AggregatedLatency.Count != 0 {
		t.Errorf("Expected 0 count, got %d", stats.AggregatedLatency.Count)
	}

	if stats.AppendEntries.Count != 0 {
		t.Errorf("Expected 0 append entries count, got %d", stats.AppendEntries.Count)
	}
}

func TestNilLogger(t *testing.T) {
	// Should not panic with nil logger
	tracker := NewPeerLatencyTracker(100, nil)

	tracker.AddPeer("peer1", "127.0.0.1:8000")
	tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)
	tracker.RemovePeer("peer1")

	// No panic means success
}

func TestAutoCreatePeer(t *testing.T) {
	tracker := NewPeerLatencyTracker(100, nil)

	// Record latency without explicitly adding peer
	tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, 10*time.Millisecond)

	// Peer should be auto-created
	stats, exists := tracker.GetPeerStats("peer1")
	if !exists {
		t.Fatal("Expected peer to be auto-created")
	}

	if stats.Address != "127.0.0.1:8000" {
		t.Errorf("Expected address '127.0.0.1:8000', got '%s'", stats.Address)
	}
}

func TestDefaultBufferSize(t *testing.T) {
	// Test with 0 buffer size - should use default
	tracker := NewPeerLatencyTracker(0, nil)

	// Should be able to add at least 100 samples (default size)
	for i := 0; i < 150; i++ {
		tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, time.Duration(i)*time.Millisecond)
	}

	stats, _ := tracker.GetPeerStats("peer1")

	// Should have default buffer size (100) samples
	if stats.AppendEntries.Count != 100 {
		t.Errorf("Expected 100 samples (default buffer), got %d", stats.AppendEntries.Count)
	}
}

func TestMathSafety(t *testing.T) {
	// Test with values that could cause overflow
	tracker := NewPeerLatencyTracker(100, nil)

	// Very large value
	tracker.RecordLatency("peer1", "127.0.0.1:8000", RPCTypeAppendEntries, time.Duration(math.MaxInt64/2))

	// Should not panic
	stats, _ := tracker.GetPeerStats("peer1")

	if stats.AppendEntries.Count != 1 {
		t.Errorf("Expected 1 sample, got %d", stats.AppendEntries.Count)
	}
}
