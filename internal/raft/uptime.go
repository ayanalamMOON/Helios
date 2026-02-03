package raft

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// UptimeEvent represents a single uptime event (start or stop)
type UptimeEvent struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      UptimeEventType `json:"type"`
	Reason    string          `json:"reason,omitempty"`
	Duration  time.Duration   `json:"duration,omitempty"`  // Only set for Stop events
	State     NodeState       `json:"state,omitempty"`     // Node state at event time
	Term      uint64          `json:"term,omitempty"`      // Raft term at event time
	LeaderID  string          `json:"leader_id,omitempty"` // Leader at event time
}

// UptimeEventType represents the type of uptime event
type UptimeEventType string

const (
	UptimeEventStart    UptimeEventType = "start"
	UptimeEventStop     UptimeEventType = "stop"
	UptimeEventRestart  UptimeEventType = "restart"
	UptimeEventCrash    UptimeEventType = "crash"
	UptimeEventElection UptimeEventType = "election" // Leader election event
	UptimeEventDemote   UptimeEventType = "demote"   // Demoted from leader
)

// UptimeSession represents a single uptime session (start to stop)
type UptimeSession struct {
	StartTime     time.Time     `json:"start_time"`
	EndTime       *time.Time    `json:"end_time,omitempty"`
	Duration      time.Duration `json:"duration,omitempty"`
	EndReason     string        `json:"end_reason,omitempty"`
	LeaderTime    time.Duration `json:"leader_time"`    // Time spent as leader
	FollowerTime  time.Duration `json:"follower_time"`  // Time spent as follower
	CandidateTime time.Duration `json:"candidate_time"` // Time spent as candidate
	Elections     int           `json:"elections"`      // Number of elections participated
	TermsServed   int           `json:"terms_served"`   // Number of terms as leader
}

// UptimeStats represents aggregated uptime statistics
type UptimeStats struct {
	// Current session info
	CurrentSessionStart time.Time     `json:"current_session_start"`
	CurrentUptime       time.Duration `json:"current_uptime"`

	// Historical stats
	TotalSessions   int           `json:"total_sessions"`
	TotalUptime     time.Duration `json:"total_uptime"`
	TotalDowntime   time.Duration `json:"total_downtime"`
	LongestSession  time.Duration `json:"longest_session"`
	ShortestSession time.Duration `json:"shortest_session,omitempty"`
	AverageSession  time.Duration `json:"average_session"`

	// Availability metrics
	UptimePercentage24h float64 `json:"uptime_percentage_24h"`
	UptimePercentage7d  float64 `json:"uptime_percentage_7d"`
	UptimePercentage30d float64 `json:"uptime_percentage_30d"`
	UptimePercentageAll float64 `json:"uptime_percentage_all"`

	// Leadership stats
	TotalLeaderTime    time.Duration `json:"total_leader_time"`
	TotalFollowerTime  time.Duration `json:"total_follower_time"`
	TotalCandidateTime time.Duration `json:"total_candidate_time"`
	LeadershipRatio    float64       `json:"leadership_ratio"` // Time as leader / total uptime
	TotalElections     int           `json:"total_elections"`
	TotalTermsAsLeader int           `json:"total_terms_as_leader"`

	// Restart stats
	TotalRestarts int           `json:"total_restarts"`
	TotalCrashes  int           `json:"total_crashes"`
	LastRestart   time.Time     `json:"last_restart,omitempty"`
	LastCrash     time.Time     `json:"last_crash,omitempty"`
	MTBF          time.Duration `json:"mtbf"` // Mean Time Between Failures

	// First tracked event
	FirstTrackedTime time.Time `json:"first_tracked_time"`
}

// UptimeStatsJSON is the JSON-serializable version of UptimeStats
type UptimeStatsJSON struct {
	CurrentSessionStart string `json:"current_session_start"`
	CurrentUptimeMs     int64  `json:"current_uptime_ms"`
	CurrentUptimeStr    string `json:"current_uptime_str"`

	TotalSessions      int    `json:"total_sessions"`
	TotalUptimeMs      int64  `json:"total_uptime_ms"`
	TotalUptimeStr     string `json:"total_uptime_str"`
	TotalDowntimeMs    int64  `json:"total_downtime_ms"`
	TotalDowntimeStr   string `json:"total_downtime_str"`
	LongestSessionMs   int64  `json:"longest_session_ms"`
	LongestSessionStr  string `json:"longest_session_str"`
	ShortestSessionMs  int64  `json:"shortest_session_ms,omitempty"`
	ShortestSessionStr string `json:"shortest_session_str,omitempty"`
	AverageSessionMs   int64  `json:"average_session_ms"`
	AverageSessionStr  string `json:"average_session_str"`

	UptimePercentage24h float64 `json:"uptime_percentage_24h"`
	UptimePercentage7d  float64 `json:"uptime_percentage_7d"`
	UptimePercentage30d float64 `json:"uptime_percentage_30d"`
	UptimePercentageAll float64 `json:"uptime_percentage_all"`

	TotalLeaderTimeMs     int64   `json:"total_leader_time_ms"`
	TotalLeaderTimeStr    string  `json:"total_leader_time_str"`
	TotalFollowerTimeMs   int64   `json:"total_follower_time_ms"`
	TotalFollowerTimeStr  string  `json:"total_follower_time_str"`
	TotalCandidateTimeMs  int64   `json:"total_candidate_time_ms"`
	TotalCandidateTimeStr string  `json:"total_candidate_time_str"`
	LeadershipRatio       float64 `json:"leadership_ratio"`
	TotalElections        int     `json:"total_elections"`
	TotalTermsAsLeader    int     `json:"total_terms_as_leader"`

	TotalRestarts int    `json:"total_restarts"`
	TotalCrashes  int    `json:"total_crashes"`
	LastRestart   string `json:"last_restart,omitempty"`
	LastCrash     string `json:"last_crash,omitempty"`
	MTBFMs        int64  `json:"mtbf_ms"`
	MTBFStr       string `json:"mtbf_str"`

	FirstTrackedTime string `json:"first_tracked_time"`
}

// UptimeHistory stores the complete uptime history
type UptimeHistory struct {
	NodeID   string          `json:"node_id"`
	Events   []UptimeEvent   `json:"events"`
	Sessions []UptimeSession `json:"sessions"`
}

// UptimeHistoryJSON is the JSON-serializable version for API responses
type UptimeHistoryJSON struct {
	NodeID         string              `json:"node_id"`
	Events         []UptimeEventJSON   `json:"events"`
	Stats          UptimeStatsJSON     `json:"stats"`
	RecentSessions []UptimeSessionJSON `json:"recent_sessions"`
}

// UptimeEventJSON is the JSON-serializable version of UptimeEvent
type UptimeEventJSON struct {
	Timestamp     string `json:"timestamp"`
	TimestampUnix int64  `json:"timestamp_unix"`
	Type          string `json:"type"`
	Reason        string `json:"reason,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
	DurationStr   string `json:"duration_str,omitempty"`
	State         string `json:"state,omitempty"`
	Term          uint64 `json:"term,omitempty"`
	LeaderID      string `json:"leader_id,omitempty"`
}

// UptimeSessionJSON is the JSON-serializable version of UptimeSession
type UptimeSessionJSON struct {
	StartTime        string `json:"start_time"`
	StartTimeUnix    int64  `json:"start_time_unix"`
	EndTime          string `json:"end_time,omitempty"`
	EndTimeUnix      int64  `json:"end_time_unix,omitempty"`
	DurationMs       int64  `json:"duration_ms"`
	DurationStr      string `json:"duration_str"`
	EndReason        string `json:"end_reason,omitempty"`
	LeaderTimeMs     int64  `json:"leader_time_ms"`
	LeaderTimeStr    string `json:"leader_time_str"`
	FollowerTimeMs   int64  `json:"follower_time_ms"`
	FollowerTimeStr  string `json:"follower_time_str"`
	CandidateTimeMs  int64  `json:"candidate_time_ms"`
	CandidateTimeStr string `json:"candidate_time_str"`
	Elections        int    `json:"elections"`
	TermsServed      int    `json:"terms_served"`
}

// UptimeTracker tracks node uptime history
type UptimeTracker struct {
	mu sync.RWMutex

	nodeID         string
	dataDir        string
	persistenceMgr *PersistenceManager
	logger         *Logger

	// Current session tracking
	currentSession  *UptimeSession
	currentState    NodeState
	stateChangeTime time.Time
	currentTerm     uint64
	currentLeaderID string

	// History storage
	events   []UptimeEvent
	sessions []UptimeSession

	// Configuration
	maxEvents       int // Maximum events to keep in memory
	maxSessions     int // Maximum sessions to keep
	persistInterval time.Duration

	// Persistence
	dirty       bool // Whether there are unsaved changes
	lastPersist time.Time
}

// NewUptimeTracker creates a new uptime tracker
func NewUptimeTracker(nodeID, dataDir string, persistenceMgr *PersistenceManager, logger *Logger) *UptimeTracker {
	ut := &UptimeTracker{
		nodeID:          nodeID,
		dataDir:         dataDir,
		persistenceMgr:  persistenceMgr,
		logger:          logger,
		events:          make([]UptimeEvent, 0, 1000),
		sessions:        make([]UptimeSession, 0, 100),
		maxEvents:       10000, // Keep last 10k events
		maxSessions:     1000,  // Keep last 1000 sessions
		persistInterval: 1 * time.Minute,
		lastPersist:     time.Now(),
	}

	// Load existing history
	if err := ut.load(); err != nil {
		if logger != nil {
			logger.Warn("Failed to load uptime history", "error", err)
		}
	}

	return ut
}

// RecordStart records the start of a new session
func (ut *UptimeTracker) RecordStart(state NodeState, term uint64, leaderID string) {
	ut.mu.Lock()
	defer ut.mu.Unlock()

	now := time.Now()

	// Check if there's a previous session that wasn't properly closed
	if ut.currentSession != nil && ut.currentSession.EndTime == nil {
		// Mark previous session as crashed
		crashTime := now.Add(-1 * time.Millisecond)
		ut.currentSession.EndTime = &crashTime
		ut.currentSession.Duration = crashTime.Sub(ut.currentSession.StartTime)
		ut.currentSession.EndReason = "crash"
		ut.finalizeStateTime()
		ut.sessions = append(ut.sessions, *ut.currentSession)

		ut.events = append(ut.events, UptimeEvent{
			Timestamp: crashTime,
			Type:      UptimeEventCrash,
			Reason:    "Previous session not properly closed",
			State:     ut.currentState,
			Term:      ut.currentTerm,
			LeaderID:  ut.currentLeaderID,
		})
	}

	// Start new session
	ut.currentSession = &UptimeSession{
		StartTime: now,
	}
	ut.currentState = state
	ut.stateChangeTime = now
	ut.currentTerm = term
	ut.currentLeaderID = leaderID

	ut.events = append(ut.events, UptimeEvent{
		Timestamp: now,
		Type:      UptimeEventStart,
		State:     state,
		Term:      term,
		LeaderID:  leaderID,
	})

	ut.dirty = true
	ut.trimHistory()
	ut.maybePersist()
}

// RecordStop records the end of the current session
func (ut *UptimeTracker) RecordStop(reason string) {
	ut.mu.Lock()
	defer ut.mu.Unlock()

	ut.recordStopLocked(reason, UptimeEventStop)
}

// RecordRestart records a restart event
func (ut *UptimeTracker) RecordRestart(reason string) {
	ut.mu.Lock()
	defer ut.mu.Unlock()

	ut.recordStopLocked(reason, UptimeEventRestart)
}

// RecordCrash records a crash event (should be called on recovery when detecting unclean shutdown)
func (ut *UptimeTracker) RecordCrash(reason string) {
	ut.mu.Lock()
	defer ut.mu.Unlock()

	ut.recordStopLocked(reason, UptimeEventCrash)
}

func (ut *UptimeTracker) recordStopLocked(reason string, eventType UptimeEventType) {
	now := time.Now()

	if ut.currentSession == nil {
		return
	}

	ut.finalizeStateTime()
	ut.currentSession.EndTime = &now
	ut.currentSession.Duration = now.Sub(ut.currentSession.StartTime)
	ut.currentSession.EndReason = reason
	ut.sessions = append(ut.sessions, *ut.currentSession)

	ut.events = append(ut.events, UptimeEvent{
		Timestamp: now,
		Type:      eventType,
		Reason:    reason,
		Duration:  ut.currentSession.Duration,
		State:     ut.currentState,
		Term:      ut.currentTerm,
		LeaderID:  ut.currentLeaderID,
	})

	ut.currentSession = nil
	ut.dirty = true
	ut.trimHistory()
	ut.persist() // Force persist on stop
}

// RecordStateChange records a state change (follower, candidate, leader)
func (ut *UptimeTracker) RecordStateChange(newState NodeState, term uint64, leaderID string) {
	ut.mu.Lock()
	defer ut.mu.Unlock()

	if ut.currentSession == nil {
		return
	}

	now := time.Now()

	// Update time spent in previous state
	ut.finalizeStateTime()

	// Check for leadership events
	var eventType UptimeEventType
	if newState == Leader && ut.currentState != Leader {
		eventType = UptimeEventElection
		ut.currentSession.Elections++
		ut.currentSession.TermsServed++
	} else if ut.currentState == Leader && newState != Leader {
		eventType = UptimeEventDemote
	}

	if eventType != "" {
		ut.events = append(ut.events, UptimeEvent{
			Timestamp: now,
			Type:      eventType,
			State:     newState,
			Term:      term,
			LeaderID:  leaderID,
		})
	}

	ut.currentState = newState
	ut.stateChangeTime = now
	ut.currentTerm = term
	ut.currentLeaderID = leaderID
	ut.dirty = true
	ut.maybePersist()
}

// finalizeStateTime updates time counters for the current state
func (ut *UptimeTracker) finalizeStateTime() {
	if ut.currentSession == nil || ut.stateChangeTime.IsZero() {
		return
	}

	elapsed := time.Since(ut.stateChangeTime)
	switch ut.currentState {
	case Leader:
		ut.currentSession.LeaderTime += elapsed
	case Follower:
		ut.currentSession.FollowerTime += elapsed
	case Candidate:
		ut.currentSession.CandidateTime += elapsed
	}
	ut.stateChangeTime = time.Now()
}

// GetCurrentUptime returns the current session uptime
func (ut *UptimeTracker) GetCurrentUptime() time.Duration {
	ut.mu.RLock()
	defer ut.mu.RUnlock()

	if ut.currentSession == nil {
		return 0
	}
	return time.Since(ut.currentSession.StartTime)
}

// GetStats returns aggregated uptime statistics
func (ut *UptimeTracker) GetStats() UptimeStats {
	ut.mu.RLock()
	defer ut.mu.RUnlock()

	stats := UptimeStats{}

	// Current session info
	if ut.currentSession != nil {
		stats.CurrentSessionStart = ut.currentSession.StartTime
		stats.CurrentUptime = time.Since(ut.currentSession.StartTime)
	}

	// Compile session stats
	allSessions := ut.getAllSessionsLocked()
	stats.TotalSessions = len(allSessions)

	if len(allSessions) == 0 {
		return stats
	}

	var totalUptime time.Duration
	var longestSession time.Duration
	var shortestSession time.Duration = -1
	var totalLeaderTime, totalFollowerTime, totalCandidateTime time.Duration
	var totalElections, totalTerms int
	var restarts, crashes int
	var lastRestart, lastCrash time.Time

	for i, session := range allSessions {
		duration := session.Duration
		if duration == 0 && session.EndTime == nil {
			// Current session
			duration = time.Since(session.StartTime)
		}

		totalUptime += duration

		if duration > longestSession {
			longestSession = duration
		}
		if shortestSession < 0 || (duration < shortestSession && duration > 0) {
			shortestSession = duration
		}

		totalLeaderTime += session.LeaderTime
		totalFollowerTime += session.FollowerTime
		totalCandidateTime += session.CandidateTime
		totalElections += session.Elections
		totalTerms += session.TermsServed

		// Count restarts and crashes based on end reason
		// Only count sessions after the first one (i > 0) because
		// the first session starting doesn't indicate a previous crash/restart
		if i > 0 && session.EndReason != "" {
			// Check if this session ended due to a crash
			isCrash := session.EndReason == "crash" ||
				(len(session.EndReason) > 5 && session.EndReason[:5] == "crash")

			if isCrash {
				crashes++
				if session.EndTime != nil && session.EndTime.After(lastCrash) {
					lastCrash = *session.EndTime
				}
			} else {
				restarts++
				if session.EndTime != nil && session.EndTime.After(lastRestart) {
					lastRestart = *session.EndTime
				}
			}
		}
	}

	stats.TotalUptime = totalUptime
	stats.LongestSession = longestSession
	if shortestSession > 0 {
		stats.ShortestSession = shortestSession
	}
	if len(allSessions) > 0 {
		stats.AverageSession = totalUptime / time.Duration(len(allSessions))
	}

	stats.TotalLeaderTime = totalLeaderTime
	stats.TotalFollowerTime = totalFollowerTime
	stats.TotalCandidateTime = totalCandidateTime
	stats.TotalElections = totalElections
	stats.TotalTermsAsLeader = totalTerms

	if totalUptime > 0 {
		stats.LeadershipRatio = float64(totalLeaderTime) / float64(totalUptime)
	}

	stats.TotalRestarts = restarts
	stats.TotalCrashes = crashes
	stats.LastRestart = lastRestart
	stats.LastCrash = lastCrash

	// Calculate MTBF (Mean Time Between Failures)
	if crashes > 0 {
		stats.MTBF = totalUptime / time.Duration(crashes)
	}

	// Calculate first tracked time
	if len(allSessions) > 0 {
		stats.FirstTrackedTime = allSessions[0].StartTime
	}

	// Calculate downtime
	stats.TotalDowntime = ut.calculateDowntime(allSessions)

	// Calculate uptime percentages
	stats.UptimePercentage24h = ut.calculateUptimePercentage(allSessions, 24*time.Hour)
	stats.UptimePercentage7d = ut.calculateUptimePercentage(allSessions, 7*24*time.Hour)
	stats.UptimePercentage30d = ut.calculateUptimePercentage(allSessions, 30*24*time.Hour)
	stats.UptimePercentageAll = ut.calculateUptimePercentageAll(allSessions)

	return stats
}

// getAllSessionsLocked returns all sessions including the current one
func (ut *UptimeTracker) getAllSessionsLocked() []UptimeSession {
	allSessions := make([]UptimeSession, len(ut.sessions))
	copy(allSessions, ut.sessions)

	if ut.currentSession != nil {
		// Create a copy of current session with updated state times
		current := *ut.currentSession
		elapsed := time.Since(ut.stateChangeTime)
		switch ut.currentState {
		case Leader:
			current.LeaderTime += elapsed
		case Follower:
			current.FollowerTime += elapsed
		case Candidate:
			current.CandidateTime += elapsed
		}
		allSessions = append(allSessions, current)
	}

	return allSessions
}

// calculateDowntime calculates total downtime between sessions
func (ut *UptimeTracker) calculateDowntime(sessions []UptimeSession) time.Duration {
	if len(sessions) < 2 {
		return 0
	}

	var totalDowntime time.Duration
	for i := 1; i < len(sessions); i++ {
		prev := sessions[i-1]
		curr := sessions[i]

		if prev.EndTime != nil {
			gap := curr.StartTime.Sub(*prev.EndTime)
			if gap > 0 {
				totalDowntime += gap
			}
		}
	}

	return totalDowntime
}

// calculateUptimePercentage calculates uptime percentage for a given period
func (ut *UptimeTracker) calculateUptimePercentage(sessions []UptimeSession, period time.Duration) float64 {
	if len(sessions) == 0 {
		return 0
	}

	now := time.Now()
	periodStart := now.Add(-period)

	var uptimeInPeriod time.Duration
	for _, session := range sessions {
		sessionStart := session.StartTime
		var sessionEnd time.Time

		if session.EndTime != nil {
			sessionEnd = *session.EndTime
		} else {
			sessionEnd = now
		}

		// Skip sessions entirely outside the period
		if sessionEnd.Before(periodStart) {
			continue
		}

		// Clamp to period boundaries
		if sessionStart.Before(periodStart) {
			sessionStart = periodStart
		}
		if sessionEnd.After(now) {
			sessionEnd = now
		}

		if sessionEnd.After(sessionStart) {
			uptimeInPeriod += sessionEnd.Sub(sessionStart)
		}
	}

	// Calculate percentage
	if period > 0 {
		// Use the actual tracking period if it's shorter than requested
		firstSession := sessions[0]
		actualPeriod := period
		if firstSession.StartTime.After(periodStart) {
			actualPeriod = now.Sub(firstSession.StartTime)
		}
		if actualPeriod > 0 {
			return float64(uptimeInPeriod) / float64(actualPeriod) * 100
		}
	}

	return 0
}

// calculateUptimePercentageAll calculates overall uptime percentage since first tracking
func (ut *UptimeTracker) calculateUptimePercentageAll(sessions []UptimeSession) float64 {
	if len(sessions) == 0 {
		return 0
	}

	firstStart := sessions[0].StartTime
	now := time.Now()
	totalPeriod := now.Sub(firstStart)

	if totalPeriod <= 0 {
		return 100
	}

	var totalUptime time.Duration
	for _, session := range sessions {
		if session.EndTime != nil {
			totalUptime += session.EndTime.Sub(session.StartTime)
		} else {
			totalUptime += now.Sub(session.StartTime)
		}
	}

	return float64(totalUptime) / float64(totalPeriod) * 100
}

// GetHistory returns the complete uptime history
func (ut *UptimeTracker) GetHistory(maxEvents, maxSessions int) UptimeHistoryJSON {
	ut.mu.RLock()
	defer ut.mu.RUnlock()

	stats := ut.GetStats()
	statsJSON := ut.statsToJSON(stats)

	// Get recent events
	events := ut.events
	if maxEvents > 0 && len(events) > maxEvents {
		events = events[len(events)-maxEvents:]
	}

	eventsJSON := make([]UptimeEventJSON, len(events))
	for i, e := range events {
		eventsJSON[i] = UptimeEventJSON{
			Timestamp:     e.Timestamp.Format(time.RFC3339),
			TimestampUnix: e.Timestamp.Unix(),
			Type:          string(e.Type),
			Reason:        e.Reason,
			DurationMs:    e.Duration.Milliseconds(),
			DurationStr:   formatDuration(e.Duration),
			State:         e.State.String(),
			Term:          e.Term,
			LeaderID:      e.LeaderID,
		}
	}

	// Get recent sessions
	allSessions := ut.getAllSessionsLocked()
	if maxSessions > 0 && len(allSessions) > maxSessions {
		allSessions = allSessions[len(allSessions)-maxSessions:]
	}

	sessionsJSON := make([]UptimeSessionJSON, len(allSessions))
	for i, s := range allSessions {
		duration := s.Duration
		if duration == 0 && s.EndTime == nil {
			duration = time.Since(s.StartTime)
		}

		sj := UptimeSessionJSON{
			StartTime:        s.StartTime.Format(time.RFC3339),
			StartTimeUnix:    s.StartTime.Unix(),
			DurationMs:       duration.Milliseconds(),
			DurationStr:      formatDuration(duration),
			EndReason:        s.EndReason,
			LeaderTimeMs:     s.LeaderTime.Milliseconds(),
			LeaderTimeStr:    formatDuration(s.LeaderTime),
			FollowerTimeMs:   s.FollowerTime.Milliseconds(),
			FollowerTimeStr:  formatDuration(s.FollowerTime),
			CandidateTimeMs:  s.CandidateTime.Milliseconds(),
			CandidateTimeStr: formatDuration(s.CandidateTime),
			Elections:        s.Elections,
			TermsServed:      s.TermsServed,
		}
		if s.EndTime != nil {
			endStr := s.EndTime.Format(time.RFC3339)
			sj.EndTime = endStr
			sj.EndTimeUnix = s.EndTime.Unix()
		}
		sessionsJSON[i] = sj
	}

	return UptimeHistoryJSON{
		NodeID:         ut.nodeID,
		Events:         eventsJSON,
		Stats:          statsJSON,
		RecentSessions: sessionsJSON,
	}
}

// GetStatsJSON returns stats in JSON-serializable format
func (ut *UptimeTracker) GetStatsJSON() UptimeStatsJSON {
	stats := ut.GetStats()
	return ut.statsToJSON(stats)
}

func (ut *UptimeTracker) statsToJSON(stats UptimeStats) UptimeStatsJSON {
	json := UptimeStatsJSON{
		CurrentSessionStart: stats.CurrentSessionStart.Format(time.RFC3339),
		CurrentUptimeMs:     stats.CurrentUptime.Milliseconds(),
		CurrentUptimeStr:    formatDuration(stats.CurrentUptime),

		TotalSessions:     stats.TotalSessions,
		TotalUptimeMs:     stats.TotalUptime.Milliseconds(),
		TotalUptimeStr:    formatDuration(stats.TotalUptime),
		TotalDowntimeMs:   stats.TotalDowntime.Milliseconds(),
		TotalDowntimeStr:  formatDuration(stats.TotalDowntime),
		LongestSessionMs:  stats.LongestSession.Milliseconds(),
		LongestSessionStr: formatDuration(stats.LongestSession),
		AverageSessionMs:  stats.AverageSession.Milliseconds(),
		AverageSessionStr: formatDuration(stats.AverageSession),

		UptimePercentage24h: stats.UptimePercentage24h,
		UptimePercentage7d:  stats.UptimePercentage7d,
		UptimePercentage30d: stats.UptimePercentage30d,
		UptimePercentageAll: stats.UptimePercentageAll,

		TotalLeaderTimeMs:     stats.TotalLeaderTime.Milliseconds(),
		TotalLeaderTimeStr:    formatDuration(stats.TotalLeaderTime),
		TotalFollowerTimeMs:   stats.TotalFollowerTime.Milliseconds(),
		TotalFollowerTimeStr:  formatDuration(stats.TotalFollowerTime),
		TotalCandidateTimeMs:  stats.TotalCandidateTime.Milliseconds(),
		TotalCandidateTimeStr: formatDuration(stats.TotalCandidateTime),
		LeadershipRatio:       stats.LeadershipRatio,
		TotalElections:        stats.TotalElections,
		TotalTermsAsLeader:    stats.TotalTermsAsLeader,

		TotalRestarts: stats.TotalRestarts,
		TotalCrashes:  stats.TotalCrashes,
		MTBFMs:        stats.MTBF.Milliseconds(),
		MTBFStr:       formatDuration(stats.MTBF),

		FirstTrackedTime: stats.FirstTrackedTime.Format(time.RFC3339),
	}

	if stats.ShortestSession > 0 {
		json.ShortestSessionMs = stats.ShortestSession.Milliseconds()
		json.ShortestSessionStr = formatDuration(stats.ShortestSession)
	}

	if !stats.LastRestart.IsZero() {
		json.LastRestart = stats.LastRestart.Format(time.RFC3339)
	}
	if !stats.LastCrash.IsZero() {
		json.LastCrash = stats.LastCrash.Format(time.RFC3339)
	}

	return json
}

// formatDuration formats a duration in a human-readable format
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}

	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	result := ""
	if days > 0 {
		result += fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		result += fmt.Sprintf("%dh", hours)
	}
	if minutes > 0 {
		result += fmt.Sprintf("%dm", minutes)
	}
	if seconds > 0 || result == "" {
		result += fmt.Sprintf("%ds", seconds)
	}

	return result
}

// Persistence methods

func (ut *UptimeTracker) load() error {
	historyPath := filepath.Join(ut.dataDir, "uptime-history.json")

	data, err := os.ReadFile(historyPath)
	if os.IsNotExist(err) {
		return nil // No history yet
	}
	if err != nil {
		return fmt.Errorf("failed to read uptime history: %w", err)
	}

	var history UptimeHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return fmt.Errorf("failed to unmarshal uptime history: %w", err)
	}

	ut.events = history.Events
	ut.sessions = history.Sessions

	// Check if the last session wasn't properly closed (indicates crash)
	if len(ut.sessions) > 0 {
		lastIdx := len(ut.sessions) - 1
		lastSession := &ut.sessions[lastIdx]
		if lastSession.EndTime == nil {
			// Previous session wasn't closed - mark it as a crash
			ut.logger.Warn("Detected unclean shutdown from previous session")

			// Mark the session as crashed
			crashTime := time.Now().Add(-1 * time.Millisecond)
			lastSession.EndTime = &crashTime
			lastSession.Duration = crashTime.Sub(lastSession.StartTime)
			lastSession.EndReason = "crash"

			// Record crash event
			ut.events = append(ut.events, UptimeEvent{
				Timestamp: crashTime,
				Type:      UptimeEventCrash,
				Reason:    "Unclean shutdown detected on startup",
			})
			ut.dirty = true
		}
	}

	return nil
}

func (ut *UptimeTracker) persist() error {
	if ut.dataDir == "" {
		return nil
	}

	history := UptimeHistory{
		NodeID:   ut.nodeID,
		Events:   ut.events,
		Sessions: ut.sessions,
	}

	// Include current session (without end time) for crash detection
	if ut.currentSession != nil {
		// Create a snapshot of current session state times
		current := *ut.currentSession
		elapsed := time.Since(ut.stateChangeTime)
		switch ut.currentState {
		case Leader:
			current.LeaderTime += elapsed
		case Follower:
			current.FollowerTime += elapsed
		case Candidate:
			current.CandidateTime += elapsed
		}
		history.Sessions = append(history.Sessions, current)
	}

	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal uptime history: %w", err)
	}

	historyPath := filepath.Join(ut.dataDir, "uptime-history.json")

	if ut.persistenceMgr != nil {
		if err := ut.persistenceMgr.WriteFile("uptime", historyPath, data); err != nil {
			return err
		}
	} else {
		if err := os.WriteFile(historyPath, data, 0644); err != nil {
			return err
		}
	}

	ut.dirty = false
	ut.lastPersist = time.Now()

	return nil
}

func (ut *UptimeTracker) maybePersist() {
	if !ut.dirty {
		return
	}

	if time.Since(ut.lastPersist) < ut.persistInterval {
		return
	}

	if err := ut.persist(); err != nil && ut.logger != nil {
		ut.logger.Error("Failed to persist uptime history", "error", err)
	}
}

func (ut *UptimeTracker) trimHistory() {
	// Trim events
	if len(ut.events) > ut.maxEvents {
		ut.events = ut.events[len(ut.events)-ut.maxEvents:]
	}

	// Trim sessions
	if len(ut.sessions) > ut.maxSessions {
		ut.sessions = ut.sessions[len(ut.sessions)-ut.maxSessions:]
	}
}

// Flush forces persistence of current state
func (ut *UptimeTracker) Flush() error {
	ut.mu.Lock()
	defer ut.mu.Unlock()

	return ut.persist()
}

// Reset clears all uptime history (for testing)
func (ut *UptimeTracker) Reset() {
	ut.mu.Lock()
	defer ut.mu.Unlock()

	ut.events = make([]UptimeEvent, 0, 1000)
	ut.sessions = make([]UptimeSession, 0, 100)
	ut.currentSession = nil
	ut.dirty = true
}
