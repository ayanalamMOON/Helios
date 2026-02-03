package raft

import (
	"math"
	"sort"
	"sync"
	"time"
)

// RPCType represents the type of RPC operation
type RPCType string

const (
	RPCTypeAppendEntries   RPCType = "append_entries"
	RPCTypeRequestVote     RPCType = "request_vote"
	RPCTypeInstallSnapshot RPCType = "install_snapshot"
)

// LatencyStats contains computed latency statistics
type LatencyStats struct {
	Count      int64         `json:"count"`       // Total number of samples
	LastValue  time.Duration `json:"last_value"`  // Most recent latency value
	MinValue   time.Duration `json:"min_value"`   // Minimum latency observed
	MaxValue   time.Duration `json:"max_value"`   // Maximum latency observed
	AvgValue   time.Duration `json:"avg_value"`   // Average latency
	P50Value   time.Duration `json:"p50_value"`   // 50th percentile (median)
	P90Value   time.Duration `json:"p90_value"`   // 90th percentile
	P99Value   time.Duration `json:"p99_value"`   // 99th percentile
	StdDev     time.Duration `json:"std_dev"`     // Standard deviation
	ErrorCount int64         `json:"error_count"` // Number of failed RPCs
	LastError  time.Time     `json:"last_error"`  // Timestamp of last error
}

// LatencyStatsJSON is the JSON-friendly version of LatencyStats
type LatencyStatsJSON struct {
	Count         int64   `json:"count"`
	LastValueMs   float64 `json:"last_value_ms"`
	MinValueMs    float64 `json:"min_value_ms"`
	MaxValueMs    float64 `json:"max_value_ms"`
	AvgValueMs    float64 `json:"avg_value_ms"`
	P50ValueMs    float64 `json:"p50_value_ms"`
	P90ValueMs    float64 `json:"p90_value_ms"`
	P99ValueMs    float64 `json:"p99_value_ms"`
	StdDevMs      float64 `json:"std_dev_ms"`
	ErrorCount    int64   `json:"error_count"`
	LastErrorUnix int64   `json:"last_error_unix,omitempty"`
}

// ToJSON converts LatencyStats to JSON-friendly format
func (ls *LatencyStats) ToJSON() LatencyStatsJSON {
	json := LatencyStatsJSON{
		Count:       ls.Count,
		LastValueMs: float64(ls.LastValue) / float64(time.Millisecond),
		MinValueMs:  float64(ls.MinValue) / float64(time.Millisecond),
		MaxValueMs:  float64(ls.MaxValue) / float64(time.Millisecond),
		AvgValueMs:  float64(ls.AvgValue) / float64(time.Millisecond),
		P50ValueMs:  float64(ls.P50Value) / float64(time.Millisecond),
		P90ValueMs:  float64(ls.P90Value) / float64(time.Millisecond),
		P99ValueMs:  float64(ls.P99Value) / float64(time.Millisecond),
		StdDevMs:    float64(ls.StdDev) / float64(time.Millisecond),
		ErrorCount:  ls.ErrorCount,
	}
	if !ls.LastError.IsZero() {
		json.LastErrorUnix = ls.LastError.Unix()
	}
	return json
}

// PeerLatencyInfo contains all latency information for a single peer
type PeerLatencyInfo struct {
	PeerID             string           `json:"peer_id"`
	Address            string           `json:"address"`
	AppendEntries      LatencyStatsJSON `json:"append_entries"`
	RequestVote        LatencyStatsJSON `json:"request_vote"`
	InstallSnapshot    LatencyStatsJSON `json:"install_snapshot"`
	AggregatedLatency  LatencyStatsJSON `json:"aggregated"`
	LastContact        time.Time        `json:"last_contact"`
	LastContactUnix    int64            `json:"last_contact_unix"`
	Reachable          bool             `json:"reachable"`
	ConsecutiveErrors  int              `json:"consecutive_errors"`
	ConnectionHealthy  bool             `json:"connection_healthy"`
}

// CircularBuffer stores a fixed number of samples in a ring buffer
type CircularBuffer struct {
	samples []time.Duration
	size    int
	head    int
	count   int
}

// NewCircularBuffer creates a new circular buffer with the given size
func NewCircularBuffer(size int) *CircularBuffer {
	return &CircularBuffer{
		samples: make([]time.Duration, size),
		size:    size,
	}
}

// Add adds a sample to the circular buffer
func (cb *CircularBuffer) Add(value time.Duration) {
	cb.samples[cb.head] = value
	cb.head = (cb.head + 1) % cb.size
	if cb.count < cb.size {
		cb.count++
	}
}

// GetSamples returns all samples in the buffer
func (cb *CircularBuffer) GetSamples() []time.Duration {
	if cb.count == 0 {
		return nil
	}

	result := make([]time.Duration, cb.count)
	if cb.count < cb.size {
		// Buffer not yet full, samples start from 0
		copy(result, cb.samples[:cb.count])
	} else {
		// Buffer is full, wrap around
		tailLen := cb.size - cb.head
		copy(result[:tailLen], cb.samples[cb.head:])
		copy(result[tailLen:], cb.samples[:cb.head])
	}
	return result
}

// Count returns the number of samples in the buffer
func (cb *CircularBuffer) Count() int {
	return cb.count
}

// peerLatencyTracker tracks latency for a single peer
type peerLatencyTracker struct {
	mu sync.RWMutex

	peerID  string
	address string

	// Per-RPC type tracking
	appendEntries   *CircularBuffer
	requestVote     *CircularBuffer
	installSnapshot *CircularBuffer

	// Aggregated stats
	totalCount int64
	errorCount int64
	lastError  time.Time

	// Connection health tracking
	lastContact       time.Time
	consecutiveErrors int

	// Min/max tracking (for all time, not just buffer)
	minLatency time.Duration
	maxLatency time.Duration
}

// newPeerLatencyTracker creates a new tracker for a peer
func newPeerLatencyTracker(peerID, address string, bufferSize int) *peerLatencyTracker {
	return &peerLatencyTracker{
		peerID:          peerID,
		address:         address,
		appendEntries:   NewCircularBuffer(bufferSize),
		requestVote:     NewCircularBuffer(bufferSize),
		installSnapshot: NewCircularBuffer(bufferSize),
		minLatency:      time.Duration(math.MaxInt64),
		maxLatency:      0,
	}
}

// RecordLatency records a successful RPC latency
func (plt *peerLatencyTracker) RecordLatency(rpcType RPCType, latency time.Duration) {
	plt.mu.Lock()
	defer plt.mu.Unlock()

	// Add to appropriate buffer
	switch rpcType {
	case RPCTypeAppendEntries:
		plt.appendEntries.Add(latency)
	case RPCTypeRequestVote:
		plt.requestVote.Add(latency)
	case RPCTypeInstallSnapshot:
		plt.installSnapshot.Add(latency)
	}

	// Update aggregated stats
	plt.totalCount++
	plt.lastContact = time.Now()
	plt.consecutiveErrors = 0 // Reset on success

	// Update min/max
	if latency < plt.minLatency {
		plt.minLatency = latency
	}
	if latency > plt.maxLatency {
		plt.maxLatency = latency
	}
}

// RecordError records an RPC error
func (plt *peerLatencyTracker) RecordError(rpcType RPCType) {
	plt.mu.Lock()
	defer plt.mu.Unlock()

	plt.errorCount++
	plt.lastError = time.Now()
	plt.consecutiveErrors++
}

// computeStats computes statistics for a buffer
func (plt *peerLatencyTracker) computeStats(buffer *CircularBuffer) LatencyStats {
	samples := buffer.GetSamples()
	if len(samples) == 0 {
		return LatencyStats{}
	}

	// Create a sorted copy for percentile calculations
	sortedSamples := make([]time.Duration, len(samples))
	copy(sortedSamples, samples)
	sort.Slice(sortedSamples, func(i, j int) bool {
		return sortedSamples[i] < sortedSamples[j]
	})

	// Calculate basic stats
	var sum time.Duration
	min := sortedSamples[0]
	max := sortedSamples[len(sortedSamples)-1]

	for _, s := range samples {
		sum += s
	}
	avg := time.Duration(int64(sum) / int64(len(samples)))

	// Calculate standard deviation
	var variance float64
	avgFloat := float64(avg)
	for _, s := range samples {
		diff := float64(s) - avgFloat
		variance += diff * diff
	}
	variance /= float64(len(samples))
	stdDev := time.Duration(math.Sqrt(variance))

	// Calculate percentiles
	p50 := sortedSamples[percentileIndex(len(sortedSamples), 50)]
	p90 := sortedSamples[percentileIndex(len(sortedSamples), 90)]
	p99 := sortedSamples[percentileIndex(len(sortedSamples), 99)]

	return LatencyStats{
		Count:     int64(len(samples)),
		LastValue: samples[len(samples)-1], // Most recent sample (last added)
		MinValue:  min,
		MaxValue:  max,
		AvgValue:  avg,
		P50Value:  p50,
		P90Value:  p90,
		P99Value:  p99,
		StdDev:    stdDev,
	}
}

// GetStats returns all latency statistics for this peer
func (plt *peerLatencyTracker) GetStats() PeerLatencyInfo {
	plt.mu.RLock()
	defer plt.mu.RUnlock()

	appendEntriesStats := plt.computeStats(plt.appendEntries)
	requestVoteStats := plt.computeStats(plt.requestVote)
	installSnapshotStats := plt.computeStats(plt.installSnapshot)

	// Compute aggregated stats from all buffers
	allSamples := make([]time.Duration, 0)
	allSamples = append(allSamples, plt.appendEntries.GetSamples()...)
	allSamples = append(allSamples, plt.requestVote.GetSamples()...)
	allSamples = append(allSamples, plt.installSnapshot.GetSamples()...)

	var aggregatedStats LatencyStats
	if len(allSamples) > 0 {
		// Create a temporary buffer for aggregated stats computation
		tempBuffer := NewCircularBuffer(len(allSamples))
		for _, s := range allSamples {
			tempBuffer.Add(s)
		}
		aggregatedStats = plt.computeStats(tempBuffer)
		aggregatedStats.Count = plt.totalCount // Use total count, not buffer count
		aggregatedStats.ErrorCount = plt.errorCount
		aggregatedStats.LastError = plt.lastError
	}

	// Determine connection health
	isReachable := plt.consecutiveErrors < 3
	timeSinceContact := time.Since(plt.lastContact)
	connectionHealthy := isReachable && (plt.lastContact.IsZero() || timeSinceContact < 30*time.Second)

	return PeerLatencyInfo{
		PeerID:            plt.peerID,
		Address:           plt.address,
		AppendEntries:     appendEntriesStats.ToJSON(),
		RequestVote:       requestVoteStats.ToJSON(),
		InstallSnapshot:   installSnapshotStats.ToJSON(),
		AggregatedLatency: aggregatedStats.ToJSON(),
		LastContact:       plt.lastContact,
		LastContactUnix:   plt.lastContact.Unix(),
		Reachable:         isReachable,
		ConsecutiveErrors: plt.consecutiveErrors,
		ConnectionHealthy: connectionHealthy,
	}
}

// GetQuickStats returns a quick summary of latency stats
func (plt *peerLatencyTracker) GetQuickStats() (avgLatency time.Duration, errorRate float64, isHealthy bool) {
	plt.mu.RLock()
	defer plt.mu.RUnlock()

	// Calculate average from all recent samples
	allSamples := make([]time.Duration, 0)
	allSamples = append(allSamples, plt.appendEntries.GetSamples()...)
	allSamples = append(allSamples, plt.requestVote.GetSamples()...)
	allSamples = append(allSamples, plt.installSnapshot.GetSamples()...)

	if len(allSamples) > 0 {
		var sum time.Duration
		for _, s := range allSamples {
			sum += s
		}
		avgLatency = time.Duration(int64(sum) / int64(len(allSamples)))
	}

	if plt.totalCount > 0 {
		errorRate = float64(plt.errorCount) / float64(plt.totalCount+plt.errorCount)
	}

	isHealthy = plt.consecutiveErrors < 3
	return
}

// percentileIndex returns the index for a given percentile
func percentileIndex(length, percentile int) int {
	index := (percentile * length) / 100
	if index >= length {
		index = length - 1
	}
	return index
}

// PeerLatencyTracker tracks latency metrics for all peers
type PeerLatencyTracker struct {
	mu         sync.RWMutex
	peers      map[string]*peerLatencyTracker
	bufferSize int
	logger     *Logger
}

// NewPeerLatencyTracker creates a new latency tracker
func NewPeerLatencyTracker(bufferSize int, logger *Logger) *PeerLatencyTracker {
	if bufferSize <= 0 {
		bufferSize = 100 // Default to 100 samples per RPC type
	}

	return &PeerLatencyTracker{
		peers:      make(map[string]*peerLatencyTracker),
		bufferSize: bufferSize,
		logger:     logger,
	}
}

// AddPeer adds a peer to track
func (plt *PeerLatencyTracker) AddPeer(peerID, address string) {
	plt.mu.Lock()
	defer plt.mu.Unlock()

	if _, exists := plt.peers[peerID]; !exists {
		plt.peers[peerID] = newPeerLatencyTracker(peerID, address, plt.bufferSize)
		if plt.logger != nil {
			plt.logger.Debug("Started tracking latency for peer", "peer_id", peerID)
		}
	}
}

// RemovePeer removes a peer from tracking
func (plt *PeerLatencyTracker) RemovePeer(peerID string) {
	plt.mu.Lock()
	defer plt.mu.Unlock()

	delete(plt.peers, peerID)
	if plt.logger != nil {
		plt.logger.Debug("Stopped tracking latency for peer", "peer_id", peerID)
	}
}

// RecordLatency records a successful RPC latency for a peer
func (plt *PeerLatencyTracker) RecordLatency(peerID, address string, rpcType RPCType, latency time.Duration) {
	plt.mu.Lock()
	tracker, exists := plt.peers[peerID]
	if !exists {
		// Auto-create tracker if peer doesn't exist
		tracker = newPeerLatencyTracker(peerID, address, plt.bufferSize)
		plt.peers[peerID] = tracker
	}
	plt.mu.Unlock()

	tracker.RecordLatency(rpcType, latency)
}

// RecordError records an RPC error for a peer
func (plt *PeerLatencyTracker) RecordError(peerID, address string, rpcType RPCType) {
	plt.mu.Lock()
	tracker, exists := plt.peers[peerID]
	if !exists {
		tracker = newPeerLatencyTracker(peerID, address, plt.bufferSize)
		plt.peers[peerID] = tracker
	}
	plt.mu.Unlock()

	tracker.RecordError(rpcType)
}

// GetPeerStats returns latency statistics for a specific peer
func (plt *PeerLatencyTracker) GetPeerStats(peerID string) (PeerLatencyInfo, bool) {
	plt.mu.RLock()
	tracker, exists := plt.peers[peerID]
	plt.mu.RUnlock()

	if !exists {
		return PeerLatencyInfo{}, false
	}

	return tracker.GetStats(), true
}

// GetAllPeerStats returns latency statistics for all peers
func (plt *PeerLatencyTracker) GetAllPeerStats() map[string]PeerLatencyInfo {
	plt.mu.RLock()
	defer plt.mu.RUnlock()

	result := make(map[string]PeerLatencyInfo, len(plt.peers))
	for peerID, tracker := range plt.peers {
		result[peerID] = tracker.GetStats()
	}
	return result
}

// GetPeerQuickStats returns quick latency summary for a peer
func (plt *PeerLatencyTracker) GetPeerQuickStats(peerID string) (avgLatency time.Duration, errorRate float64, isHealthy bool, exists bool) {
	plt.mu.RLock()
	tracker, exists := plt.peers[peerID]
	plt.mu.RUnlock()

	if !exists {
		return 0, 0, false, false
	}

	avgLatency, errorRate, isHealthy = tracker.GetQuickStats()
	return avgLatency, errorRate, isHealthy, true
}

// GetAggregatedStats returns aggregated latency statistics across all peers
func (plt *PeerLatencyTracker) GetAggregatedStats() LatencyStatsJSON {
	plt.mu.RLock()
	defer plt.mu.RUnlock()

	var totalCount int64
	var errorCount int64
	var latencySum time.Duration
	var minLatency time.Duration = time.Duration(math.MaxInt64)
	var maxLatency time.Duration
	sampleCount := 0
	var lastError time.Time

	for _, tracker := range plt.peers {
		stats := tracker.GetStats()
		totalCount += stats.AggregatedLatency.Count
		errorCount += stats.AggregatedLatency.ErrorCount

		// Get samples for more accurate aggregation
		tracker.mu.RLock()
		allSamples := make([]time.Duration, 0)
		allSamples = append(allSamples, tracker.appendEntries.GetSamples()...)
		allSamples = append(allSamples, tracker.requestVote.GetSamples()...)
		allSamples = append(allSamples, tracker.installSnapshot.GetSamples()...)
		tracker.mu.RUnlock()

		for _, s := range allSamples {
			latencySum += s
			sampleCount++
			if s < minLatency {
				minLatency = s
			}
			if s > maxLatency {
				maxLatency = s
			}
		}

		if stats.AggregatedLatency.LastErrorUnix > lastError.Unix() {
			lastError = time.Unix(stats.AggregatedLatency.LastErrorUnix, 0)
		}
	}

	if sampleCount == 0 {
		return LatencyStatsJSON{}
	}

	avgLatency := time.Duration(int64(latencySum) / int64(sampleCount))

	result := LatencyStatsJSON{
		Count:       totalCount,
		LastValueMs: float64(avgLatency) / float64(time.Millisecond),
		MinValueMs:  float64(minLatency) / float64(time.Millisecond),
		MaxValueMs:  float64(maxLatency) / float64(time.Millisecond),
		AvgValueMs:  float64(avgLatency) / float64(time.Millisecond),
		ErrorCount:  errorCount,
	}

	if !lastError.IsZero() {
		result.LastErrorUnix = lastError.Unix()
	}

	return result
}

// GetHealthySummary returns a summary of healthy vs unhealthy peers
func (plt *PeerLatencyTracker) GetHealthySummary() (healthy int, unhealthy int, total int) {
	plt.mu.RLock()
	defer plt.mu.RUnlock()

	for _, tracker := range plt.peers {
		total++
		_, _, isHealthy := tracker.GetQuickStats()
		if isHealthy {
			healthy++
		} else {
			unhealthy++
		}
	}
	return
}

// Reset clears all latency data
func (plt *PeerLatencyTracker) Reset() {
	plt.mu.Lock()
	defer plt.mu.Unlock()

	for peerID, tracker := range plt.peers {
		plt.peers[peerID] = newPeerLatencyTracker(tracker.peerID, tracker.address, plt.bufferSize)
	}
}
