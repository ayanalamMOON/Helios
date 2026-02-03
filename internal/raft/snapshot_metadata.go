package raft

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/helios/helios/internal/observability"
)

// SnapshotMetadata contains comprehensive information about a snapshot
type SnapshotMetadata struct {
	// Core identification
	ID    string `json:"id"`    // Unique identifier (term-index)
	Index uint64 `json:"index"` // Last included log index
	Term  uint64 `json:"term"`  // Last included log term

	// Size information
	Size             int64 `json:"size"`              // Snapshot data size in bytes
	CompressedSize   int64 `json:"compressed_size"`   // Compressed size (if compression enabled)
	UncompressedSize int64 `json:"uncompressed_size"` // Original uncompressed size

	// Timing information
	CreatedAt        time.Time     `json:"created_at"`        // When snapshot was created
	CreationDuration time.Duration `json:"creation_duration"` // How long it took to create
	LastRestoredAt   *time.Time    `json:"last_restored_at"`  // When last restored from this snapshot
	RestoreDuration  time.Duration `json:"restore_duration"`  // Last restore duration

	// Source information
	NodeID     string `json:"node_id"`      // Node that created the snapshot
	LeaderID   string `json:"leader_id"`    // Leader at time of snapshot
	IsFromPeer bool   `json:"is_from_peer"` // True if received via InstallSnapshot RPC

	// State information
	ConfigIndex    uint64   `json:"config_index"`    // Cluster configuration index
	MembershipHash string   `json:"membership_hash"` // Hash of cluster membership
	PeerCount      int      `json:"peer_count"`      // Number of peers at snapshot time
	Peers          []string `json:"peers,omitempty"` // List of peer IDs

	// Integrity
	Checksum   string `json:"checksum"`   // SHA-256 checksum of data
	Version    int    `json:"version"`    // Snapshot format version
	Compressed bool   `json:"compressed"` // Whether data is compressed
	Encryption bool   `json:"encryption"` // Whether data is encrypted

	// Metadata about log compaction
	EntriesCompacted uint64 `json:"entries_compacted"` // Number of log entries compacted
	PreviousIndex    uint64 `json:"previous_index"`    // Previous snapshot's index (0 if first)
}

// SnapshotStats contains aggregate snapshot statistics
type SnapshotStats struct {
	// Current snapshot info
	LatestSnapshot     *SnapshotMetadata `json:"latest_snapshot,omitempty"`
	LatestSnapshotTime string            `json:"latest_snapshot_time,omitempty"`
	LatestSnapshotAge  string            `json:"latest_snapshot_age,omitempty"`

	// Historical statistics
	TotalSnapshots         int    `json:"total_snapshots"`
	TotalSnapshotsCreated  int    `json:"total_snapshots_created"`  // Created by this node
	TotalSnapshotsReceived int    `json:"total_snapshots_received"` // Received from peers
	TotalBytesWritten      int64  `json:"total_bytes_written"`
	TotalBytesCompacted    int64  `json:"total_bytes_compacted"`
	TotalEntriesCompacted  uint64 `json:"total_entries_compacted"`

	// Performance metrics
	AvgCreationTimeMs  float64 `json:"avg_creation_time_ms"`
	MaxCreationTimeMs  float64 `json:"max_creation_time_ms"`
	MinCreationTimeMs  float64 `json:"min_creation_time_ms"`
	LastCreationTimeMs float64 `json:"last_creation_time_ms"`

	// Restore metrics
	TotalRestores     int     `json:"total_restores"`
	AvgRestoreTimeMs  float64 `json:"avg_restore_time_ms"`
	LastRestoreTimeMs float64 `json:"last_restore_time_ms"`

	// Storage metrics
	TotalStorageBytes    int64 `json:"total_storage_bytes"`
	AvgSnapshotSize      int64 `json:"avg_snapshot_size"`
	LargestSnapshotSize  int64 `json:"largest_snapshot_size"`
	SmallestSnapshotSize int64 `json:"smallest_snapshot_size"`

	// Error tracking
	FailedCreations int    `json:"failed_creations"`
	FailedRestores  int    `json:"failed_restores"`
	LastError       string `json:"last_error,omitempty"`
	LastErrorTime   string `json:"last_error_time,omitempty"`

	// Configuration
	SnapshotInterval   string `json:"snapshot_interval"`
	SnapshotThreshold  uint64 `json:"snapshot_threshold"`
	RetentionCount     int    `json:"retention_count"`
	CompressionEnabled bool   `json:"compression_enabled"`

	// Time tracking
	TimeSinceLastSnapshot   string `json:"time_since_last_snapshot"`
	NextScheduledSnapshot   string `json:"next_scheduled_snapshot,omitempty"`
	SnapshotsPendingCleanup int    `json:"snapshots_pending_cleanup"`
}

// SnapshotStatsJSON is JSON-friendly version of snapshot stats
type SnapshotStatsJSON struct {
	// Current snapshot
	HasSnapshot            bool   `json:"has_snapshot"`
	LatestSnapshotID       string `json:"latest_snapshot_id,omitempty"`
	LatestSnapshotIndex    uint64 `json:"latest_snapshot_index"`
	LatestSnapshotTerm     uint64 `json:"latest_snapshot_term"`
	LatestSnapshotSize     int64  `json:"latest_snapshot_size"`
	LatestSnapshotTimeUnix int64  `json:"latest_snapshot_time_unix"`
	LatestSnapshotTime     string `json:"latest_snapshot_time,omitempty"`
	LatestSnapshotAgeMs    int64  `json:"latest_snapshot_age_ms"`
	LatestSnapshotAge      string `json:"latest_snapshot_age"`

	// Counts
	TotalSnapshots         int `json:"total_snapshots"`
	TotalSnapshotsCreated  int `json:"total_snapshots_created"`
	TotalSnapshotsReceived int `json:"total_snapshots_received"`
	TotalRestores          int `json:"total_restores"`
	FailedCreations        int `json:"failed_creations"`
	FailedRestores         int `json:"failed_restores"`

	// Size metrics
	TotalBytesWritten     int64  `json:"total_bytes_written"`
	TotalEntriesCompacted uint64 `json:"total_entries_compacted"`
	AvgSnapshotSize       int64  `json:"avg_snapshot_size"`
	LargestSnapshotSize   int64  `json:"largest_snapshot_size"`

	// Performance (in milliseconds)
	AvgCreationTimeMs  float64 `json:"avg_creation_time_ms"`
	MaxCreationTimeMs  float64 `json:"max_creation_time_ms"`
	LastCreationTimeMs float64 `json:"last_creation_time_ms"`
	AvgRestoreTimeMs   float64 `json:"avg_restore_time_ms"`
	LastRestoreTimeMs  float64 `json:"last_restore_time_ms"`

	// Configuration
	SnapshotIntervalMs int64  `json:"snapshot_interval_ms"`
	SnapshotInterval   string `json:"snapshot_interval"`
	SnapshotThreshold  uint64 `json:"snapshot_threshold"`
	RetentionCount     int    `json:"retention_count"`

	// Schedule
	TimeSinceLastSnapshotMs int64  `json:"time_since_last_snapshot_ms"`
	TimeSinceLastSnapshot   string `json:"time_since_last_snapshot"`
	SnapshotsOnDisk         int    `json:"snapshots_on_disk"`
}

// SnapshotHistoryEntry represents a historical snapshot event
type SnapshotHistoryEntry struct {
	Timestamp        time.Time `json:"timestamp"`
	Type             string    `json:"type"` // "created", "received", "restored", "deleted", "error"
	SnapshotID       string    `json:"snapshot_id"`
	Index            uint64    `json:"index"`
	Term             uint64    `json:"term"`
	Size             int64     `json:"size"`
	DurationMs       float64   `json:"duration_ms"`
	Success          bool      `json:"success"`
	ErrorMessage     string    `json:"error_message,omitempty"`
	FromPeer         string    `json:"from_peer,omitempty"` // For received snapshots
	EntriesCompacted uint64    `json:"entries_compacted,omitempty"`
}

// SnapshotHistory represents the snapshot event history
type SnapshotHistory struct {
	NodeID         string                 `json:"node_id"`
	Events         []SnapshotHistoryEntry `json:"events"`
	Snapshots      []SnapshotMetadata     `json:"snapshots"`
	TotalEvents    int                    `json:"total_events"`
	TotalSnapshots int                    `json:"total_snapshots"`
}

// SnapshotMetadataTracker tracks snapshot metadata and history
type SnapshotMetadataTracker struct {
	mu sync.RWMutex

	nodeID  string
	dataDir string
	logger  *Logger

	// Configuration
	snapshotInterval  time.Duration
	snapshotThreshold uint64
	retentionCount    int

	// Current state
	latestSnapshot *SnapshotMetadata
	snapshots      []SnapshotMetadata
	history        []SnapshotHistoryEntry

	// Statistics
	totalCreated          int
	totalReceived         int
	totalRestores         int
	totalBytesWritten     int64
	totalEntriesCompacted uint64
	failedCreations       int
	failedRestores        int
	lastError             string
	lastErrorTime         time.Time

	// Timing statistics
	creationTimes []float64 // in ms
	restoreTimes  []float64 // in ms

	// Persistence
	persistPath string
	dirty       bool

	// Scheduling
	lastSnapshotTime      time.Time
	nextScheduledSnapshot time.Time
}

const (
	maxHistoryEntries   = 1000
	maxSnapshotsTracked = 100
	maxTimingSamples    = 100
	snapshotStatsFile   = "snapshot-stats.json"
)

// NewSnapshotMetadataTracker creates a new snapshot metadata tracker
func NewSnapshotMetadataTracker(nodeID, dataDir string, snapshotInterval time.Duration, snapshotThreshold uint64, logger *Logger) *SnapshotMetadataTracker {
	tracker := &SnapshotMetadataTracker{
		nodeID:            nodeID,
		dataDir:           dataDir,
		logger:            logger,
		snapshotInterval:  snapshotInterval,
		snapshotThreshold: snapshotThreshold,
		retentionCount:    2,
		snapshots:         make([]SnapshotMetadata, 0),
		history:           make([]SnapshotHistoryEntry, 0),
		creationTimes:     make([]float64, 0, maxTimingSamples),
		restoreTimes:      make([]float64, 0, maxTimingSamples),
	}

	if dataDir != "" {
		tracker.persistPath = filepath.Join(dataDir, snapshotStatsFile)
		tracker.load()
	}

	return tracker
}

// RecordSnapshotCreated records a newly created snapshot
func (t *SnapshotMetadataTracker) RecordSnapshotCreated(index, term uint64, size int64, duration time.Duration, entriesCompacted uint64, leaderID string, peers []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	snapshotID := fmt.Sprintf("%d-%d", term, index)

	metadata := SnapshotMetadata{
		ID:               snapshotID,
		Index:            index,
		Term:             term,
		Size:             size,
		UncompressedSize: size,
		CreatedAt:        now,
		CreationDuration: duration,
		NodeID:           t.nodeID,
		LeaderID:         leaderID,
		IsFromPeer:       false,
		PeerCount:        len(peers),
		Peers:            peers,
		EntriesCompacted: entriesCompacted,
		Version:          1,
	}

	// Set previous index
	if t.latestSnapshot != nil {
		metadata.PreviousIndex = t.latestSnapshot.Index
	}

	// Update state
	t.latestSnapshot = &metadata
	t.addSnapshot(metadata)
	t.totalCreated++
	t.totalBytesWritten += size
	t.totalEntriesCompacted += entriesCompacted
	t.lastSnapshotTime = now

	// Record timing
	durationMs := float64(duration.Milliseconds())
	t.addCreationTime(durationMs)

	// Add history entry
	t.addHistoryEntry(SnapshotHistoryEntry{
		Timestamp:        now,
		Type:             "created",
		SnapshotID:       snapshotID,
		Index:            index,
		Term:             term,
		Size:             size,
		DurationMs:       durationMs,
		Success:          true,
		EntriesCompacted: entriesCompacted,
	})

	// Update Prometheus metrics
	observability.RaftSnapshotTotal.WithLabelValues(t.nodeID, "created").Inc()
	observability.RaftSnapshotSize.WithLabelValues(t.nodeID).Set(float64(size))
	observability.RaftSnapshotDurationSeconds.WithLabelValues(t.nodeID, "create").Observe(duration.Seconds())
	observability.RaftSnapshotIndex.WithLabelValues(t.nodeID).Set(float64(index))
	observability.RaftSnapshotTerm.WithLabelValues(t.nodeID).Set(float64(term))
	observability.RaftSnapshotAgeSeconds.WithLabelValues(t.nodeID).Set(0) // Just created
	observability.RaftSnapshotBytesWritten.WithLabelValues(t.nodeID).Add(float64(size))
	observability.RaftSnapshotEntriesCompacted.WithLabelValues(t.nodeID).Add(float64(entriesCompacted))
	observability.RaftSnapshotOnDisk.WithLabelValues(t.nodeID).Set(float64(len(t.snapshots)))

	t.dirty = true
	t.logger.Info("Snapshot created", "id", snapshotID, "index", index, "term", term, "size", size, "duration", duration)
}

// RecordSnapshotReceived records a snapshot received from a peer
func (t *SnapshotMetadataTracker) RecordSnapshotReceived(index, term uint64, size int64, duration time.Duration, fromPeerID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	snapshotID := fmt.Sprintf("%d-%d", term, index)

	metadata := SnapshotMetadata{
		ID:               snapshotID,
		Index:            index,
		Term:             term,
		Size:             size,
		UncompressedSize: size,
		CreatedAt:        now,
		CreationDuration: duration,
		NodeID:           t.nodeID,
		IsFromPeer:       true,
		Version:          1,
	}

	// Set previous index
	if t.latestSnapshot != nil {
		metadata.PreviousIndex = t.latestSnapshot.Index
	}

	// Update state
	t.latestSnapshot = &metadata
	t.addSnapshot(metadata)
	t.totalReceived++
	t.totalBytesWritten += size
	t.lastSnapshotTime = now

	// Add history entry
	t.addHistoryEntry(SnapshotHistoryEntry{
		Timestamp:  now,
		Type:       "received",
		SnapshotID: snapshotID,
		Index:      index,
		Term:       term,
		Size:       size,
		DurationMs: float64(duration.Milliseconds()),
		Success:    true,
		FromPeer:   fromPeerID,
	})

	// Update Prometheus metrics
	observability.RaftSnapshotTotal.WithLabelValues(t.nodeID, "received").Inc()
	observability.RaftSnapshotSize.WithLabelValues(t.nodeID).Set(float64(size))
	observability.RaftSnapshotIndex.WithLabelValues(t.nodeID).Set(float64(index))
	observability.RaftSnapshotTerm.WithLabelValues(t.nodeID).Set(float64(term))
	observability.RaftSnapshotAgeSeconds.WithLabelValues(t.nodeID).Set(0)
	observability.RaftSnapshotBytesWritten.WithLabelValues(t.nodeID).Add(float64(size))
	observability.RaftSnapshotOnDisk.WithLabelValues(t.nodeID).Set(float64(len(t.snapshots)))

	t.dirty = true
	t.logger.Info("Snapshot received from peer", "id", snapshotID, "from", fromPeerID, "index", index, "size", size)
}

// RecordSnapshotRestored records a snapshot restoration
func (t *SnapshotMetadataTracker) RecordSnapshotRestored(index, term uint64, duration time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	snapshotID := fmt.Sprintf("%d-%d", term, index)

	t.totalRestores++
	durationMs := float64(duration.Milliseconds())
	t.addRestoreTime(durationMs)

	// Update latest snapshot's restore info
	if t.latestSnapshot != nil && t.latestSnapshot.Index == index && t.latestSnapshot.Term == term {
		t.latestSnapshot.LastRestoredAt = &now
		t.latestSnapshot.RestoreDuration = duration
	}

	// Add history entry
	t.addHistoryEntry(SnapshotHistoryEntry{
		Timestamp:  now,
		Type:       "restored",
		SnapshotID: snapshotID,
		Index:      index,
		Term:       term,
		DurationMs: durationMs,
		Success:    true,
	})

	// Update Prometheus metrics
	observability.RaftSnapshotTotal.WithLabelValues(t.nodeID, "restored").Inc()
	observability.RaftSnapshotDurationSeconds.WithLabelValues(t.nodeID, "restore").Observe(duration.Seconds())

	t.dirty = true
	t.logger.Info("Snapshot restored", "id", snapshotID, "index", index, "duration", duration)
}

// RecordSnapshotDeleted records a snapshot deletion
func (t *SnapshotMetadataTracker) RecordSnapshotDeleted(index, term uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	snapshotID := fmt.Sprintf("%d-%d", term, index)

	// Remove from snapshots list
	for i, s := range t.snapshots {
		if s.Index == index && s.Term == term {
			t.snapshots = append(t.snapshots[:i], t.snapshots[i+1:]...)
			break
		}
	}

	// Add history entry
	t.addHistoryEntry(SnapshotHistoryEntry{
		Timestamp:  time.Now(),
		Type:       "deleted",
		SnapshotID: snapshotID,
		Index:      index,
		Term:       term,
		Success:    true,
	})

	// Update Prometheus metrics
	observability.RaftSnapshotOnDisk.WithLabelValues(t.nodeID).Set(float64(len(t.snapshots)))

	t.dirty = true
	t.logger.Debug("Snapshot deleted", "id", snapshotID, "index", index)
}

// RecordSnapshotError records a snapshot operation error
func (t *SnapshotMetadataTracker) RecordSnapshotError(operation string, index, term uint64, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	snapshotID := fmt.Sprintf("%d-%d", term, index)
	errMsg := err.Error()

	switch operation {
	case "create":
		t.failedCreations++
	case "restore":
		t.failedRestores++
	}

	t.lastError = fmt.Sprintf("%s: %s", operation, errMsg)
	t.lastErrorTime = now

	// Add history entry
	t.addHistoryEntry(SnapshotHistoryEntry{
		Timestamp:    now,
		Type:         "error",
		SnapshotID:   snapshotID,
		Index:        index,
		Term:         term,
		Success:      false,
		ErrorMessage: errMsg,
	})

	// Update Prometheus metrics
	observability.RaftSnapshotErrors.WithLabelValues(t.nodeID, operation).Inc()

	t.dirty = true
	t.logger.Error("Snapshot operation failed", "operation", operation, "id", snapshotID, "error", errMsg)
}

// GetLatestSnapshot returns the latest snapshot metadata
func (t *SnapshotMetadataTracker) GetLatestSnapshot() *SnapshotMetadata {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.latestSnapshot == nil {
		return nil
	}

	// Return a copy
	snapshot := *t.latestSnapshot
	return &snapshot
}

// GetStats returns comprehensive snapshot statistics
func (t *SnapshotMetadataTracker) GetStats() *SnapshotStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	stats := &SnapshotStats{
		TotalSnapshots:         len(t.snapshots),
		TotalSnapshotsCreated:  t.totalCreated,
		TotalSnapshotsReceived: t.totalReceived,
		TotalBytesWritten:      t.totalBytesWritten,
		TotalEntriesCompacted:  t.totalEntriesCompacted,
		TotalRestores:          t.totalRestores,
		FailedCreations:        t.failedCreations,
		FailedRestores:         t.failedRestores,
		SnapshotInterval:       t.snapshotInterval.String(),
		SnapshotThreshold:      t.snapshotThreshold,
		RetentionCount:         t.retentionCount,
	}

	// Latest snapshot info
	if t.latestSnapshot != nil {
		snapshot := *t.latestSnapshot
		stats.LatestSnapshot = &snapshot
		stats.LatestSnapshotTime = snapshot.CreatedAt.Format(time.RFC3339)
		stats.LatestSnapshotAge = time.Since(snapshot.CreatedAt).Round(time.Second).String()
	}

	// Creation time stats
	if len(t.creationTimes) > 0 {
		stats.AvgCreationTimeMs = t.average(t.creationTimes)
		stats.MaxCreationTimeMs = t.max(t.creationTimes)
		stats.MinCreationTimeMs = t.min(t.creationTimes)
		stats.LastCreationTimeMs = t.creationTimes[len(t.creationTimes)-1]
	}

	// Restore time stats
	if len(t.restoreTimes) > 0 {
		stats.AvgRestoreTimeMs = t.average(t.restoreTimes)
		stats.LastRestoreTimeMs = t.restoreTimes[len(t.restoreTimes)-1]
	}

	// Storage stats
	if len(t.snapshots) > 0 {
		var totalSize int64
		var maxSize, minSize int64 = 0, t.snapshots[0].Size

		for _, s := range t.snapshots {
			totalSize += s.Size
			if s.Size > maxSize {
				maxSize = s.Size
			}
			if s.Size < minSize {
				minSize = s.Size
			}
		}

		stats.TotalStorageBytes = totalSize
		stats.AvgSnapshotSize = totalSize / int64(len(t.snapshots))
		stats.LargestSnapshotSize = maxSize
		stats.SmallestSnapshotSize = minSize
	}

	// Error info
	if t.lastError != "" {
		stats.LastError = t.lastError
		stats.LastErrorTime = t.lastErrorTime.Format(time.RFC3339)
	}

	// Time tracking
	if !t.lastSnapshotTime.IsZero() {
		stats.TimeSinceLastSnapshot = time.Since(t.lastSnapshotTime).Round(time.Second).String()
	}

	return stats
}

// GetStatsJSON returns stats in JSON-friendly format
func (t *SnapshotMetadataTracker) GetStatsJSON() *SnapshotStatsJSON {
	t.mu.RLock()
	defer t.mu.RUnlock()

	stats := &SnapshotStatsJSON{
		TotalSnapshots:         len(t.snapshots),
		TotalSnapshotsCreated:  t.totalCreated,
		TotalSnapshotsReceived: t.totalReceived,
		TotalRestores:          t.totalRestores,
		FailedCreations:        t.failedCreations,
		FailedRestores:         t.failedRestores,
		TotalBytesWritten:      t.totalBytesWritten,
		TotalEntriesCompacted:  t.totalEntriesCompacted,
		SnapshotIntervalMs:     t.snapshotInterval.Milliseconds(),
		SnapshotInterval:       t.snapshotInterval.String(),
		SnapshotThreshold:      t.snapshotThreshold,
		RetentionCount:         t.retentionCount,
		SnapshotsOnDisk:        len(t.snapshots),
	}

	// Latest snapshot info
	if t.latestSnapshot != nil {
		stats.HasSnapshot = true
		stats.LatestSnapshotID = t.latestSnapshot.ID
		stats.LatestSnapshotIndex = t.latestSnapshot.Index
		stats.LatestSnapshotTerm = t.latestSnapshot.Term
		stats.LatestSnapshotSize = t.latestSnapshot.Size
		stats.LatestSnapshotTimeUnix = t.latestSnapshot.CreatedAt.Unix()
		stats.LatestSnapshotTime = t.latestSnapshot.CreatedAt.Format(time.RFC3339)

		age := time.Since(t.latestSnapshot.CreatedAt)
		stats.LatestSnapshotAgeMs = age.Milliseconds()
		stats.LatestSnapshotAge = age.Round(time.Second).String()
	}

	// Creation time stats
	if len(t.creationTimes) > 0 {
		stats.AvgCreationTimeMs = t.average(t.creationTimes)
		stats.MaxCreationTimeMs = t.max(t.creationTimes)
		stats.LastCreationTimeMs = t.creationTimes[len(t.creationTimes)-1]
	}

	// Restore time stats
	if len(t.restoreTimes) > 0 {
		stats.AvgRestoreTimeMs = t.average(t.restoreTimes)
		stats.LastRestoreTimeMs = t.restoreTimes[len(t.restoreTimes)-1]
	}

	// Storage stats
	if len(t.snapshots) > 0 {
		var totalSize, maxSize int64
		for _, s := range t.snapshots {
			totalSize += s.Size
			if s.Size > maxSize {
				maxSize = s.Size
			}
		}
		stats.AvgSnapshotSize = totalSize / int64(len(t.snapshots))
		stats.LargestSnapshotSize = maxSize
	}

	// Time tracking
	if !t.lastSnapshotTime.IsZero() {
		since := time.Since(t.lastSnapshotTime)
		stats.TimeSinceLastSnapshotMs = since.Milliseconds()
		stats.TimeSinceLastSnapshot = since.Round(time.Second).String()
	}

	return stats
}

// GetHistory returns snapshot history
func (t *SnapshotMetadataTracker) GetHistory(maxEvents, maxSnapshots int) *SnapshotHistory {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if maxEvents <= 0 {
		maxEvents = 100
	}
	if maxEvents > maxHistoryEntries {
		maxEvents = maxHistoryEntries
	}

	if maxSnapshots <= 0 {
		maxSnapshots = 10
	}
	if maxSnapshots > maxSnapshotsTracked {
		maxSnapshots = maxSnapshotsTracked
	}

	history := &SnapshotHistory{
		NodeID:         t.nodeID,
		TotalEvents:    len(t.history),
		TotalSnapshots: len(t.snapshots),
	}

	// Get recent events (reverse chronological)
	eventCount := min(maxEvents, len(t.history))
	history.Events = make([]SnapshotHistoryEntry, eventCount)
	for i := 0; i < eventCount; i++ {
		history.Events[i] = t.history[len(t.history)-1-i]
	}

	// Get recent snapshots (reverse chronological)
	snapshotCount := min(maxSnapshots, len(t.snapshots))
	history.Snapshots = make([]SnapshotMetadata, snapshotCount)
	for i := 0; i < snapshotCount; i++ {
		history.Snapshots[i] = t.snapshots[len(t.snapshots)-1-i]
	}

	return history
}

// GetAllSnapshots returns all tracked snapshots
func (t *SnapshotMetadataTracker) GetAllSnapshots() []SnapshotMetadata {
	t.mu.RLock()
	defer t.mu.RUnlock()

	snapshots := make([]SnapshotMetadata, len(t.snapshots))
	copy(snapshots, t.snapshots)
	return snapshots
}

// Reset clears all snapshot tracking data
func (t *SnapshotMetadataTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.latestSnapshot = nil
	t.snapshots = make([]SnapshotMetadata, 0)
	t.history = make([]SnapshotHistoryEntry, 0)
	t.totalCreated = 0
	t.totalReceived = 0
	t.totalRestores = 0
	t.totalBytesWritten = 0
	t.totalEntriesCompacted = 0
	t.failedCreations = 0
	t.failedRestores = 0
	t.lastError = ""
	t.creationTimes = make([]float64, 0, maxTimingSamples)
	t.restoreTimes = make([]float64, 0, maxTimingSamples)
	t.lastSnapshotTime = time.Time{}
	t.dirty = true

	t.logger.Info("Snapshot metadata tracker reset")

	t.saveInternal()
}

// SetConfiguration updates the tracker configuration
func (t *SnapshotMetadataTracker) SetConfiguration(interval time.Duration, threshold uint64, retention int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.snapshotInterval = interval
	t.snapshotThreshold = threshold
	t.retentionCount = retention
	t.dirty = true
}

// UpdateFromSnapshotStore syncs with existing snapshots on disk
func (t *SnapshotMetadataTracker) UpdateFromSnapshotStore(snapshots []SnapshotMeta) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, meta := range snapshots {
		// Check if we already have this snapshot
		exists := false
		for _, s := range t.snapshots {
			if s.Index == meta.Index && s.Term == meta.Term {
				exists = true
				break
			}
		}

		if !exists {
			snapshotID := fmt.Sprintf("%d-%d", meta.Term, meta.Index)
			t.addSnapshot(SnapshotMetadata{
				ID:    snapshotID,
				Index: meta.Index,
				Term:  meta.Term,
				Size:  meta.Size,
				// We don't know the creation time for existing snapshots
				CreatedAt: time.Now(),
				NodeID:    t.nodeID,
				Version:   1,
			})

			// Set as latest if it's the highest index
			if t.latestSnapshot == nil || meta.Index > t.latestSnapshot.Index {
				t.latestSnapshot = &t.snapshots[len(t.snapshots)-1]
			}
		}
	}

	t.dirty = true
}

// Flush persists the current state to disk
func (t *SnapshotMetadataTracker) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.dirty {
		return nil
	}

	return t.saveInternal()
}

// Close flushes and closes the tracker
func (t *SnapshotMetadataTracker) Close() error {
	return t.Flush()
}

// Internal helper methods

func (t *SnapshotMetadataTracker) addSnapshot(s SnapshotMetadata) {
	t.snapshots = append(t.snapshots, s)

	// Sort by index descending
	sort.Slice(t.snapshots, func(i, j int) bool {
		return t.snapshots[i].Index > t.snapshots[j].Index
	})

	// Trim to max
	if len(t.snapshots) > maxSnapshotsTracked {
		t.snapshots = t.snapshots[:maxSnapshotsTracked]
	}
}

func (t *SnapshotMetadataTracker) addHistoryEntry(entry SnapshotHistoryEntry) {
	t.history = append(t.history, entry)

	// Trim to max
	if len(t.history) > maxHistoryEntries {
		t.history = t.history[len(t.history)-maxHistoryEntries:]
	}
}

func (t *SnapshotMetadataTracker) addCreationTime(ms float64) {
	t.creationTimes = append(t.creationTimes, ms)
	if len(t.creationTimes) > maxTimingSamples {
		t.creationTimes = t.creationTimes[1:]
	}
}

func (t *SnapshotMetadataTracker) addRestoreTime(ms float64) {
	t.restoreTimes = append(t.restoreTimes, ms)
	if len(t.restoreTimes) > maxTimingSamples {
		t.restoreTimes = t.restoreTimes[1:]
	}
}

func (t *SnapshotMetadataTracker) average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func (t *SnapshotMetadataTracker) max(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	maxVal := values[0]
	for _, v := range values[1:] {
		if v > maxVal {
			maxVal = v
		}
	}
	return maxVal
}

func (t *SnapshotMetadataTracker) min(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	minVal := values[0]
	for _, v := range values[1:] {
		if v < minVal {
			minVal = v
		}
	}
	return minVal
}

// Persistence

type snapshotTrackerPersistence struct {
	NodeID                string                 `json:"node_id"`
	LatestSnapshot        *SnapshotMetadata      `json:"latest_snapshot,omitempty"`
	Snapshots             []SnapshotMetadata     `json:"snapshots"`
	History               []SnapshotHistoryEntry `json:"history"`
	TotalCreated          int                    `json:"total_created"`
	TotalReceived         int                    `json:"total_received"`
	TotalRestores         int                    `json:"total_restores"`
	TotalBytesWritten     int64                  `json:"total_bytes_written"`
	TotalEntriesCompacted uint64                 `json:"total_entries_compacted"`
	FailedCreations       int                    `json:"failed_creations"`
	FailedRestores        int                    `json:"failed_restores"`
	LastError             string                 `json:"last_error,omitempty"`
	LastErrorTime         *time.Time             `json:"last_error_time,omitempty"`
	CreationTimes         []float64              `json:"creation_times"`
	RestoreTimes          []float64              `json:"restore_times"`
	LastSnapshotTime      *time.Time             `json:"last_snapshot_time,omitempty"`
}

func (t *SnapshotMetadataTracker) load() {
	if t.persistPath == "" {
		return
	}

	data, err := os.ReadFile(t.persistPath)
	if err != nil {
		if !os.IsNotExist(err) {
			t.logger.Warn("Failed to load snapshot stats", "error", err)
		}
		return
	}

	var state snapshotTrackerPersistence
	if err := json.Unmarshal(data, &state); err != nil {
		t.logger.Warn("Failed to parse snapshot stats", "error", err)
		return
	}

	t.latestSnapshot = state.LatestSnapshot
	t.snapshots = state.Snapshots
	if t.snapshots == nil {
		t.snapshots = make([]SnapshotMetadata, 0)
	}
	t.history = state.History
	if t.history == nil {
		t.history = make([]SnapshotHistoryEntry, 0)
	}
	t.totalCreated = state.TotalCreated
	t.totalReceived = state.TotalReceived
	t.totalRestores = state.TotalRestores
	t.totalBytesWritten = state.TotalBytesWritten
	t.totalEntriesCompacted = state.TotalEntriesCompacted
	t.failedCreations = state.FailedCreations
	t.failedRestores = state.FailedRestores
	t.lastError = state.LastError
	if state.LastErrorTime != nil {
		t.lastErrorTime = *state.LastErrorTime
	}
	t.creationTimes = state.CreationTimes
	if t.creationTimes == nil {
		t.creationTimes = make([]float64, 0, maxTimingSamples)
	}
	t.restoreTimes = state.RestoreTimes
	if t.restoreTimes == nil {
		t.restoreTimes = make([]float64, 0, maxTimingSamples)
	}
	if state.LastSnapshotTime != nil {
		t.lastSnapshotTime = *state.LastSnapshotTime
	}

	t.logger.Info("Loaded snapshot stats", "snapshots", len(t.snapshots), "history", len(t.history))
}

func (t *SnapshotMetadataTracker) saveInternal() error {
	if t.persistPath == "" {
		return nil
	}

	var lastErrorTime *time.Time
	if !t.lastErrorTime.IsZero() {
		lastErrorTime = &t.lastErrorTime
	}

	var lastSnapshotTime *time.Time
	if !t.lastSnapshotTime.IsZero() {
		lastSnapshotTime = &t.lastSnapshotTime
	}

	state := snapshotTrackerPersistence{
		NodeID:                t.nodeID,
		LatestSnapshot:        t.latestSnapshot,
		Snapshots:             t.snapshots,
		History:               t.history,
		TotalCreated:          t.totalCreated,
		TotalReceived:         t.totalReceived,
		TotalRestores:         t.totalRestores,
		TotalBytesWritten:     t.totalBytesWritten,
		TotalEntriesCompacted: t.totalEntriesCompacted,
		FailedCreations:       t.failedCreations,
		FailedRestores:        t.failedRestores,
		LastError:             t.lastError,
		LastErrorTime:         lastErrorTime,
		CreationTimes:         t.creationTimes,
		RestoreTimes:          t.restoreTimes,
		LastSnapshotTime:      lastSnapshotTime,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot stats: %w", err)
	}

	tmpPath := t.persistPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write snapshot stats: %w", err)
	}

	if err := os.Rename(tmpPath, t.persistPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename snapshot stats: %w", err)
	}

	t.dirty = false
	return nil
}
