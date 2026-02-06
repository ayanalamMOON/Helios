package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/helios/helios/internal/auth"
	"github.com/helios/helios/internal/auth/rbac"
	"github.com/helios/helios/internal/config"
	"github.com/helios/helios/internal/graphql"
	"github.com/helios/helios/internal/observability"
	"github.com/helios/helios/internal/queue"
	"github.com/helios/helios/internal/rate"
)

// Gateway is the API gateway
type Gateway struct {
	authService    *auth.Service
	rbacService    *rbac.Service
	rateLimiter    *rate.Limiter
	queue          *queue.Queue
	rateConfig     *rate.Config
	raftAtlas      RaftManager      // Interface for Raft operations
	configManager  *config.Manager  // Configuration manager
	wsHub          *WSHub           // WebSocket hub for real-time updates
	graphqlHandler *graphql.Handler // GraphQL handler
	logger         *observability.Logger
}

// RaftManager interface for Raft operations
type RaftManager interface {
	AddPeer(id, address string) error
	RemovePeer(id string) error
	GetPeers() ([]PeerInfo, error)
	GetLeader() (string, error)
	IsLeader() bool
	GetClusterStatus() (interface{}, error)
	GetPeerLatencyStats(peerID string) (interface{}, bool)
	GetAllPeerLatencyStats() interface{}
	GetAggregatedLatencyStats() interface{}
	GetPeerHealthSummary() (healthy int, unhealthy int, total int)
	ResetPeerLatencyStats()
	// Uptime tracking methods
	GetUptimeStats() interface{}
	GetUptimeHistory(maxEvents, maxSessions int) interface{}
	ResetUptimeHistory()
	// Snapshot metadata methods
	GetSnapshotStats() interface{}
	GetSnapshotHistory(maxEvents, maxSnapshots int) interface{}
	GetLatestSnapshotMetadata() interface{}
	ResetSnapshotStats()
	// Custom metrics methods
	RegisterCustomMetric(name, metricType, help string, labels map[string]string, buckets []float64) error
	SetCustomMetric(name string, value float64) error
	IncrementCustomCounter(name string, delta float64) error
	ObserveCustomHistogram(name string, value float64) error
	GetCustomMetric(name string) (interface{}, error)
	GetCustomMetricsStats() interface{}
	DeleteCustomMetric(name string) error
	ResetCustomMetrics()
	SetEventListener(listener interface{})
}

// PeerInfo represents information about a peer
type PeerInfo struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	State   string `json:"state,omitempty"`
}

// ClusterStatus represents comprehensive cluster status information
type ClusterStatus struct {
	Node     NodeStatus   `json:"node"`
	Leader   LeaderInfo   `json:"leader"`
	Indices  IndicesInfo  `json:"indices"`
	Peers    []PeerStatus `json:"peers"`
	Sessions SessionsInfo `json:"sessions"`
	TLS      TLSStatus    `json:"tls"`
	Uptime   string       `json:"uptime"`
}

// NodeStatus represents the current node's status
type NodeStatus struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Term  uint64 `json:"term"`
}

// LeaderInfo represents leader information
type LeaderInfo struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	IsLeader bool   `json:"is_leader"`
}

// IndicesInfo represents log indices
type IndicesInfo struct {
	LastLog      uint64 `json:"last_log"`
	LastLogTerm  uint64 `json:"last_log_term"`
	CommitIndex  uint64 `json:"commit_index"`
	AppliedIndex uint64 `json:"applied_index"`
}

// PeerStatus represents a peer's status (includes replication info if leader)
type PeerStatus struct {
	ID         string  `json:"id"`
	Address    string  `json:"address"`
	MatchIndex *uint64 `json:"match_index,omitempty"`
	NextIndex  *uint64 `json:"next_index,omitempty"`
}

// SessionsInfo represents session statistics
type SessionsInfo struct {
	ActiveCount int      `json:"active_count"`
	SessionIDs  []string `json:"session_ids,omitempty"`
}

// TLSStatus represents TLS configuration status
type TLSStatus struct {
	Enabled    bool `json:"enabled"`
	VerifyPeer bool `json:"verify_peer"`
}

// NewGateway creates a new API gateway
func NewGateway(
	authService *auth.Service,
	rbacService *rbac.Service,
	rateLimiter *rate.Limiter,
	queue *queue.Queue,
	rateConfig *rate.Config,
) *Gateway {
	logger := log.New(log.Writer(), "[Gateway] ", log.LstdFlags)
	obsLogger := observability.NewLogger("gateway")

	return &Gateway{
		authService:    authService,
		rbacService:    rbacService,
		rateLimiter:    rateLimiter,
		queue:          queue,
		rateConfig:     rateConfig,
		raftAtlas:      nil,                   // Will be set later if Raft is enabled
		wsHub:          NewWSHub(nil, logger), // WebSocket hub
		graphqlHandler: nil,                   // Will be set when GraphQL resolver is configured
		logger:         obsLogger,
	}
}

// SetRaftManager sets the Raft manager for cluster operations
func (g *Gateway) SetRaftManager(rm RaftManager) {
	g.raftAtlas = rm

	// Update WebSocket hub with Raft manager
	if g.wsHub != nil {
		g.wsHub.raftMgr = rm
		// Register WebSocket hub as event listener
		rm.SetEventListener(g.wsHub)
	}
}

// SetGraphQLHandler sets the GraphQL handler
func (g *Gateway) SetGraphQLHandler(handler *graphql.Handler) {
	g.graphqlHandler = handler
}

// SetConfigManager sets the configuration manager
func (g *Gateway) SetConfigManager(cm *config.Manager) {
	g.configManager = cm
}

// RegisterRoutes registers all API routes
func (g *Gateway) RegisterRoutes(mux *http.ServeMux) {
	// GraphQL endpoints
	if g.graphqlHandler != nil {
		mux.Handle("/graphql", g.graphqlHandler)
		mux.Handle("/graphql/playground", graphql.PlaygroundHandler())
	}

	// Auth endpoints
	mux.HandleFunc("/api/v1/auth/login", g.handleLogin)
	mux.HandleFunc("/api/v1/auth/register", g.handleRegister)
	mux.HandleFunc("/api/v1/auth/logout", g.requireAuth(g.handleLogout))

	// KV endpoints
	mux.HandleFunc("/api/v1/kv/", g.requireAuth(g.requirePermission(rbac.PermKVGet, g.handleKV)))

	// Job endpoints
	mux.HandleFunc("/api/v1/jobs", g.requireAuth(g.requirePermission(rbac.PermJobCreate, g.handleJobs)))
	mux.HandleFunc("/api/v1/jobs/", g.requireAuth(g.requirePermission(rbac.PermJobView, g.handleJobDetail)))

	// Admin endpoints
	mux.HandleFunc("/admin/metrics", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleMetrics)))

	// Cluster management endpoints
	mux.HandleFunc("/admin/cluster/peers", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleClusterPeers)))
	mux.HandleFunc("/admin/cluster/status", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleClusterStatus)))
	mux.HandleFunc("/admin/cluster/latency", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleClusterLatency)))
	mux.HandleFunc("/admin/cluster/latency/reset", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleClusterLatencyReset)))
	mux.HandleFunc("/admin/cluster/uptime", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleClusterUptime)))
	mux.HandleFunc("/admin/cluster/uptime/history", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleClusterUptimeHistory)))
	mux.HandleFunc("/admin/cluster/uptime/reset", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleClusterUptimeReset)))
	mux.HandleFunc("/admin/cluster/snapshot", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleClusterSnapshot)))
	mux.HandleFunc("/admin/cluster/snapshot/history", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleClusterSnapshotHistory)))
	mux.HandleFunc("/admin/cluster/snapshot/reset", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleClusterSnapshotReset)))
	mux.HandleFunc("/admin/cluster/metrics", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleCustomMetrics)))
	mux.HandleFunc("/admin/cluster/metrics/reset", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleCustomMetricsReset)))
	mux.HandleFunc("/ws/cluster", g.handleWebSocket) // WebSocket endpoint (auth handled in handler)

	// Configuration management endpoints
	mux.HandleFunc("/admin/config", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleConfig)))
	mux.HandleFunc("/admin/config/reload", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleConfigReload)))
	mux.HandleFunc("/admin/config/diff", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleConfigDiff)))

	// Migration management endpoints
	mux.HandleFunc("/admin/config/migrations", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleMigrations)))
	mux.HandleFunc("/admin/config/migrations/status", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleMigrationStatus)))
	mux.HandleFunc("/admin/config/migrations/apply", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleMigrationApply)))
	mux.HandleFunc("/admin/config/migrations/rollback", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleMigrationRollback)))
	mux.HandleFunc("/admin/config/backups", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleBackups)))

	// History management endpoints
	mux.HandleFunc("/admin/config/history", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleHistory)))
	mux.HandleFunc("/admin/config/history/stats", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleHistoryStats)))
	mux.HandleFunc("/admin/config/history/compare", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleHistoryCompare)))
	mux.HandleFunc("/admin/config/history/restore", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handleHistoryRestore)))
}

// Auth handlers

func (g *Gateway) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Get user
	user, err := g.authService.GetUserByUsername(req.Username)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Validate password
	if !g.authService.ValidatePassword(user, req.Password) {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Create token (15 minutes)
	token, err := g.authService.CreateToken(user.ID, 15*60*1000000000) // 15 minutes in nanoseconds
	if err != nil {
		http.Error(w, "Failed to create token", http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"token":   token.TokenHash,
		"user_id": user.ID,
	})
}

func (g *Gateway) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Create user
	user, err := g.authService.CreateUser(req.Username, req.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Assign default role
	g.rbacService.AssignRole(user.ID, "user")

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"user_id":  user.ID,
		"username": user.Username,
	})
}

func (g *Gateway) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := r.Header.Get("Authorization")
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
		g.authService.RevokeToken(token)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// KV handlers

func (g *Gateway) handleKV(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.handleKVGet(w, r)
	case http.MethodPost, http.MethodPut:
		g.handleKVSet(w, r)
	case http.MethodDelete:
		g.handleKVDelete(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *Gateway) handleKVGet(w http.ResponseWriter, r *http.Request) {
	// Extract key from URL path /api/v1/kv/{key}
	key := r.URL.Path[len("/api/v1/kv/"):]
	if key == "" {
		http.Error(w, "Key is required", http.StatusBadRequest)
		return
	}
	// Implementation would interact with ATLAS
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"key":     key,
		"message": "GET KV",
	})
}

func (g *Gateway) handleKVSet(w http.ResponseWriter, r *http.Request) {
	// Extract key from URL path /api/v1/kv/{key}
	key := r.URL.Path[len("/api/v1/kv/"):]
	if key == "" {
		http.Error(w, "Key is required", http.StatusBadRequest)
		return
	}

	// Decode request body for value
	var req struct {
		Value string `json:"value"`
		TTL   int    `json:"ttl,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Implementation would interact with ATLAS
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"key":     key,
		"value":   req.Value,
		"message": "SET KV",
	})
}

func (g *Gateway) handleKVDelete(w http.ResponseWriter, r *http.Request) {
	// Implementation would interact with ATLAS
	respondJSON(w, http.StatusOK, map[string]interface{}{"message": "DELETE KV"})
}

// Job handlers

func (g *Gateway) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		g.handleJobCreate(w, r)
	case http.MethodGet:
		g.handleJobList(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *Gateway) handleJobCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Payload map[string]interface{} `json:"payload"`
		DedupID string                 `json:"dedup_id,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	jobID, err := g.queue.Enqueue(req.Payload, req.DedupID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"job_id": jobID,
	})
}

func (g *Gateway) handleJobList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	jobs, err := g.queue.ListJobs(queue.JobStatus(status))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"jobs": jobs,
	})
}

func (g *Gateway) handleJobDetail(w http.ResponseWriter, r *http.Request) {
	// Extract job ID from path
	jobID := r.URL.Path[len("/api/v1/jobs/"):]

	job, err := g.queue.GetJob(jobID)
	if err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	respondJSON(w, http.StatusOK, job)
}

// Admin handlers

func (g *Gateway) handleMetrics(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Metrics endpoint",
	})
}

// Cluster management handlers

func (g *Gateway) handleClusterPeers(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// List all peers
		peers, err := g.raftAtlas.GetPeers()
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get peers: %v", err), http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"peers": peers,
		})

	case http.MethodPost:
		// Add a new peer
		var req struct {
			ID      string `json:"id"`
			Address string `json:"address"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if req.ID == "" || req.Address == "" {
			http.Error(w, "ID and address are required", http.StatusBadRequest)
			return
		}

		// Only leader can add peers
		if !g.raftAtlas.IsLeader() {
			leader, _ := g.raftAtlas.GetLeader()
			respondJSON(w, http.StatusBadRequest, map[string]string{
				"error":  "Not the leader",
				"leader": leader,
			})
			return
		}

		if err := g.raftAtlas.AddPeer(req.ID, req.Address); err != nil {
			http.Error(w, fmt.Sprintf("Failed to add peer: %v", err), http.StatusInternalServerError)
			return
		}

		respondJSON(w, http.StatusCreated, map[string]interface{}{
			"message": "Peer added successfully",
			"peer": map[string]string{
				"id":      req.ID,
				"address": req.Address,
			},
		})

	case http.MethodDelete:
		// Remove a peer
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if req.ID == "" {
			http.Error(w, "ID is required", http.StatusBadRequest)
			return
		}

		// Only leader can remove peers
		if !g.raftAtlas.IsLeader() {
			leader, _ := g.raftAtlas.GetLeader()
			respondJSON(w, http.StatusBadRequest, map[string]string{
				"error":  "Not the leader",
				"leader": leader,
			})
			return
		}

		if err := g.raftAtlas.RemovePeer(req.ID); err != nil {
			http.Error(w, fmt.Sprintf("Failed to remove peer: %v", err), http.StatusInternalServerError)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"message": "Peer removed successfully",
			"peer_id": req.ID,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *Gateway) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get comprehensive cluster status
	status, err := g.raftAtlas.GetClusterStatus()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get cluster status: %v", err), http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, status)
}

// handleClusterLatency returns detailed peer latency metrics
func (g *Gateway) handleClusterLatency(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if requesting a specific peer
	peerID := r.URL.Query().Get("peer_id")
	if peerID != "" {
		// Get stats for specific peer
		stats, exists := g.raftAtlas.GetPeerLatencyStats(peerID)
		if !exists {
			http.Error(w, fmt.Sprintf("Peer not found: %s", peerID), http.StatusNotFound)
			return
		}
		respondJSON(w, http.StatusOK, stats)
		return
	}

	// Get all peer latency stats
	allStats := g.raftAtlas.GetAllPeerLatencyStats()
	aggregatedStats := g.raftAtlas.GetAggregatedLatencyStats()
	healthy, unhealthy, total := g.raftAtlas.GetPeerHealthSummary()

	response := map[string]interface{}{
		"peers":      allStats,
		"aggregated": aggregatedStats,
		"health_summary": map[string]int{
			"healthy":   healthy,
			"unhealthy": unhealthy,
			"total":     total,
		},
	}

	respondJSON(w, http.StatusOK, response)
}

// handleClusterLatencyReset resets all peer latency metrics
func (g *Gateway) handleClusterLatencyReset(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Reset latency stats
	g.raftAtlas.ResetPeerLatencyStats()

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Peer latency metrics reset successfully",
	})
}

// handleClusterUptime returns current uptime statistics
func (g *Gateway) handleClusterUptime(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := g.raftAtlas.GetUptimeStats()
	if stats == nil {
		http.Error(w, "Uptime tracking not available", http.StatusServiceUnavailable)
		return
	}

	respondJSON(w, http.StatusOK, stats)
}

// handleClusterUptimeHistory returns detailed uptime history
func (g *Gateway) handleClusterUptimeHistory(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse optional query parameters for limiting results
	maxEvents := 100  // Default
	maxSessions := 50 // Default

	if eventsStr := r.URL.Query().Get("max_events"); eventsStr != "" {
		var events int
		if _, err := fmt.Sscanf(eventsStr, "%d", &events); err == nil && events > 0 {
			maxEvents = events
		}
	}

	if sessionsStr := r.URL.Query().Get("max_sessions"); sessionsStr != "" {
		var sessions int
		if _, err := fmt.Sscanf(sessionsStr, "%d", &sessions); err == nil && sessions > 0 {
			maxSessions = sessions
		}
	}

	history := g.raftAtlas.GetUptimeHistory(maxEvents, maxSessions)
	if history == nil {
		http.Error(w, "Uptime tracking not available", http.StatusServiceUnavailable)
		return
	}

	respondJSON(w, http.StatusOK, history)
}

// handleClusterUptimeReset resets all uptime history
func (g *Gateway) handleClusterUptimeReset(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	g.raftAtlas.ResetUptimeHistory()

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Uptime history reset successfully",
	})
}

// handleClusterSnapshot returns current snapshot statistics
func (g *Gateway) handleClusterSnapshot(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := g.raftAtlas.GetSnapshotStats()
	if stats == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"has_snapshot": false,
			"message":      "No snapshot statistics available",
		})
		return
	}

	respondJSON(w, http.StatusOK, stats)
}

// handleClusterSnapshotHistory returns snapshot history
func (g *Gateway) handleClusterSnapshotHistory(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	maxEvents := 100
	maxSnapshots := 10

	if eventsStr := r.URL.Query().Get("events"); eventsStr != "" {
		if events, err := strconv.Atoi(eventsStr); err == nil && events > 0 {
			maxEvents = events
			if maxEvents > 1000 {
				maxEvents = 1000
			}
		}
	}

	if snapshotsStr := r.URL.Query().Get("snapshots"); snapshotsStr != "" {
		if snapshots, err := strconv.Atoi(snapshotsStr); err == nil && snapshots > 0 {
			maxSnapshots = snapshots
			if maxSnapshots > 100 {
				maxSnapshots = 100
			}
		}
	}

	history := g.raftAtlas.GetSnapshotHistory(maxEvents, maxSnapshots)
	if history == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"events":          []interface{}{},
			"snapshots":       []interface{}{},
			"total_events":    0,
			"total_snapshots": 0,
		})
		return
	}

	respondJSON(w, http.StatusOK, history)
}

// handleClusterSnapshotReset resets all snapshot statistics
func (g *Gateway) handleClusterSnapshotReset(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	g.raftAtlas.ResetSnapshotStats()

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Snapshot statistics reset successfully",
	})
}

// Custom metrics handlers

func (g *Gateway) handleCustomMetrics(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get custom metrics stats or specific metric
		name := r.URL.Query().Get("name")
		if name != "" {
			// Get specific metric
			metric, err := g.raftAtlas.GetCustomMetric(name)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			respondJSON(w, http.StatusOK, metric)
		} else {
			// Get all metrics stats
			stats := g.raftAtlas.GetCustomMetricsStats()
			if stats == nil {
				http.Error(w, "Custom metrics not available", http.StatusServiceUnavailable)
				return
			}
			respondJSON(w, http.StatusOK, stats)
		}

	case http.MethodPost:
		// Register new metric
		var req struct {
			Name    string            `json:"name"`
			Type    string            `json:"type"`
			Help    string            `json:"help"`
			Labels  map[string]string `json:"labels"`
			Buckets []float64         `json:"buckets"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.Name == "" {
			http.Error(w, "Metric name is required", http.StatusBadRequest)
			return
		}

		if req.Type == "" {
			http.Error(w, "Metric type is required", http.StatusBadRequest)
			return
		}

		if err := g.raftAtlas.RegisterCustomMetric(req.Name, req.Type, req.Help, req.Labels, req.Buckets); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Notify WebSocket clients of metrics update
		if g.wsHub != nil {
			g.wsHub.OnMetricsUpdate()
		}

		respondJSON(w, http.StatusCreated, map[string]string{
			"message": "Custom metric registered successfully",
			"name":    req.Name,
		})

	case http.MethodPut:
		// Update metric value
		var req struct {
			Name  string  `json:"name"`
			Value float64 `json:"value"`
			Delta float64 `json:"delta"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.Name == "" {
			http.Error(w, "Metric name is required", http.StatusBadRequest)
			return
		}

		// Try to determine metric type and update accordingly
		metric, err := g.raftAtlas.GetCustomMetric(req.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		metricMap, ok := metric.(map[string]interface{})
		if !ok {
			http.Error(w, "Invalid metric format", http.StatusInternalServerError)
			return
		}

		metricType, _ := metricMap["type"].(string)
		switch metricType {
		case "gauge":
			if err := g.raftAtlas.SetCustomMetric(req.Name, req.Value); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		case "counter":
			delta := req.Delta
			if delta == 0 {
				delta = 1 // Default increment by 1
			}
			if err := g.raftAtlas.IncrementCustomCounter(req.Name, delta); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		case "histogram":
			if err := g.raftAtlas.ObserveCustomHistogram(req.Name, req.Value); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		default:
			http.Error(w, "Unknown metric type", http.StatusBadRequest)
			return
		}

		// Notify WebSocket clients of metrics update
		if g.wsHub != nil {
			g.wsHub.OnMetricsUpdate()
		}

		respondJSON(w, http.StatusOK, map[string]string{
			"message": "Metric updated successfully",
			"name":    req.Name,
		})

	case http.MethodDelete:
		// Delete metric
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "Metric name is required", http.StatusBadRequest)
			return
		}

		if err := g.raftAtlas.DeleteCustomMetric(name); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		// Notify WebSocket clients of metrics update
		if g.wsHub != nil {
			g.wsHub.OnMetricsUpdate()
		}

		respondJSON(w, http.StatusOK, map[string]string{
			"message": "Custom metric deleted successfully",
			"name":    name,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *Gateway) handleCustomMetricsReset(w http.ResponseWriter, r *http.Request) {
	if g.raftAtlas == nil {
		http.Error(w, "Raft not enabled", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	g.raftAtlas.ResetCustomMetrics()

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Custom metrics reset successfully",
	})
}

// WebSocket handler

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins for now - in production, restrict to trusted origins
		return true
	},
}

func (g *Gateway) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract token from query parameter or header
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("Authorization")
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}
	}

	if token == "" {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	}

	// Validate token
	userID, err := g.authService.ValidateToken(token)
	if err != nil {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	// Check admin permission
	if !g.rbacService.HasPermission(userID, rbac.PermAdmin) {
		http.Error(w, "Admin permission required", http.StatusForbidden)
		return
	}

	// Get user role
	role := "admin" // In a real implementation, get actual role

	// Upgrade connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to upgrade connection: %v", err), http.StatusInternalServerError)
		return
	}

	// Create client
	clientID := fmt.Sprintf("%s-%d", userID, time.Now().UnixNano())
	client := NewWSClient(clientID, conn, userID, role)

	// Register client
	g.wsHub.RegisterClient(client)

	// Start pumps
	go client.WritePump()
	go client.ReadPump(g.wsHub)
}

// Start starts the gateway and WebSocket hub
func (g *Gateway) Start() {
	if g.wsHub != nil {
		g.wsHub.Start()
	}
}

// Stop stops the gateway and WebSocket hub
func (g *Gateway) Stop() {
	if g.wsHub != nil {
		g.wsHub.Stop()
	}
}

// GetWSHub returns the WebSocket hub
func (g *Gateway) GetWSHub() *WSHub {
	return g.wsHub
}

// Configuration management handlers

func (g *Gateway) handleConfig(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get current configuration
		cfg := g.configManager.GetConfig()

		// Choose format based on Accept header or query parameter
		format := r.URL.Query().Get("format")
		if format == "" {
			acceptHeader := r.Header.Get("Accept")
			if acceptHeader == "application/x-yaml" || acceptHeader == "text/yaml" {
				format = "yaml"
			} else {
				format = "json"
			}
		}

		if format == "yaml" {
			yamlStr, err := cfg.ExportYAML()
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to export YAML: %v", err), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/x-yaml")
			w.Write([]byte(yamlStr))
		} else {
			respondJSON(w, http.StatusOK, cfg)
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *Gateway) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Reload configuration
	if err := g.configManager.Reload(); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Get reload statistics
	count, lastReload := g.configManager.GetReloadStats()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"message":      "Configuration reloaded successfully",
		"reload_count": count,
		"last_reload":  lastReload.Format("2006-01-02T15:04:05Z07:00"),
	})
}

func (g *Gateway) handleConfigDiff(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Preview reload to get diff
	diff, err := g.configManager.PreviewReload()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": fmt.Sprintf("Failed to compute diff: %v", err),
		})
		return
	}

	// Return diff as JSON
	respondJSON(w, http.StatusOK, diff)
}

// Migration handlers

func (g *Gateway) handleMigrations(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get all registered migrations
	migrations := config.GetAllMigrations()
	migrationList := make([]map[string]interface{}, len(migrations))

	for i, m := range migrations {
		migrationList[i] = map[string]interface{}{
			"version":     m.Version,
			"description": m.Description,
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"migrations":     migrationList,
		"total":          len(migrations),
		"latest_version": config.GetLatestVersion(),
	})
}

func (g *Gateway) handleMigrationStatus(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status, err := g.configManager.GetMigrationStatus()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": fmt.Sprintf("Failed to get migration status: %v", err),
		})
		return
	}

	respondJSON(w, http.StatusOK, status)
}

func (g *Gateway) handleMigrationApply(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check for dry-run flag
	dryRun := r.URL.Query().Get("dry_run") == "true"

	result, err := g.configManager.ApplyMigrations(dryRun)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, result)
		return
	}

	respondJSON(w, http.StatusOK, result)
}

func (g *Gateway) handleMigrationRollback(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse target version
	var req struct {
		TargetVersion int  `json:"target_version"`
		DryRun        bool `json:"dry_run"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	// Check for dry-run flag in query string too
	if r.URL.Query().Get("dry_run") == "true" {
		req.DryRun = true
	}

	result, err := g.configManager.RollbackMigration(req.TargetVersion, req.DryRun)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, result)
		return
	}

	respondJSON(w, http.StatusOK, result)
}

func (g *Gateway) handleBackups(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// List backups
		backups, err := g.configManager.ListBackups()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": fmt.Sprintf("Failed to list backups: %v", err),
			})
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"backups": backups,
			"count":   len(backups),
		})

	case http.MethodPost:
		// Restore from backup
		var req struct {
			BackupPath string `json:"backup_path"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"error": fmt.Sprintf("Invalid request body: %v", err),
			})
			return
		}

		if req.BackupPath == "" {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"error": "backup_path is required",
			})
			return
		}

		if err := g.configManager.RestoreBackup(req.BackupPath); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("Failed to restore backup: %v", err),
			})
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "Backup restored successfully",
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// History handlers

func (g *Gateway) handleHistory(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	if !g.configManager.IsHistoryEnabled() {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "History tracking is not enabled",
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get history with optional filtering
		query := &config.HistoryQuery{}

		// Parse query parameters
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			var limit int
			fmt.Sscanf(limitStr, "%d", &limit)
			query.Limit = limit
		}
		if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
			var offset int
			fmt.Sscanf(offsetStr, "%d", &offset)
			query.Offset = offset
		}
		if changeType := r.URL.Query().Get("change_type"); changeType != "" {
			query.ChangeType = config.HistoryChangeType(changeType)
		}
		if source := r.URL.Query().Get("source"); source != "" {
			query.Source = source
		}
		if user := r.URL.Query().Get("user"); user != "" {
			query.User = user
		}

		// Check for specific version/id request
		versionOrID := r.URL.Query().Get("id")
		if versionOrID == "" {
			versionOrID = r.URL.Query().Get("version")
		}

		if versionOrID != "" {
			// Get specific entry
			entry, err := g.configManager.GetHistoryEntry(versionOrID)
			if err != nil {
				respondJSON(w, http.StatusNotFound, map[string]interface{}{
					"error": err.Error(),
				})
				return
			}
			respondJSON(w, http.StatusOK, entry)
			return
		}

		// Get history list
		history, err := g.configManager.GetHistory(query)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
			return
		}

		respondJSON(w, http.StatusOK, history)

	case http.MethodDelete:
		// Clear history or purge old entries
		purgeHours := r.URL.Query().Get("older_than_hours")
		if purgeHours != "" {
			var hours int
			fmt.Sscanf(purgeHours, "%d", &hours)
			if hours > 0 {
				purged, err := g.configManager.PurgeHistory(time.Duration(hours) * time.Hour)
				if err != nil {
					respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
						"error": err.Error(),
					})
					return
				}
				respondJSON(w, http.StatusOK, map[string]interface{}{
					"success": true,
					"purged":  purged,
					"message": fmt.Sprintf("Purged %d entries older than %d hours", purged, hours),
				})
				return
			}
		}

		// Clear all history
		if err := g.configManager.ClearHistory(); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "History cleared",
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *Gateway) handleHistoryStats(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !g.configManager.IsHistoryEnabled() {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "History tracking is not enabled",
		})
		return
	}

	stats, err := g.configManager.GetHistoryStats()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	respondJSON(w, http.StatusOK, stats)
}

func (g *Gateway) handleHistoryCompare(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !g.configManager.IsHistoryEnabled() {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "History tracking is not enabled",
		})
		return
	}

	// Parse version parameters
	versionAStr := r.URL.Query().Get("from")
	versionBStr := r.URL.Query().Get("to")

	if versionAStr == "" || versionBStr == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "Both 'from' and 'to' version parameters are required",
		})
		return
	}

	var versionA, versionB int
	if _, err := fmt.Sscanf(versionAStr, "%d", &versionA); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": fmt.Sprintf("Invalid 'from' version: %s", versionAStr),
		})
		return
	}
	if _, err := fmt.Sscanf(versionBStr, "%d", &versionB); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": fmt.Sprintf("Invalid 'to' version: %s", versionBStr),
		})
		return
	}

	comparison, err := g.configManager.CompareHistoryVersions(versionA, versionB)
	if err != nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	respondJSON(w, http.StatusOK, comparison)
}

func (g *Gateway) handleHistoryRestore(w http.ResponseWriter, r *http.Request) {
	if g.configManager == nil {
		http.Error(w, "Configuration manager not initialized", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !g.configManager.IsHistoryEnabled() {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "History tracking is not enabled",
		})
		return
	}

	var req struct {
		Version     int    `json:"version"`
		User        string `json:"user,omitempty"`
		Description string `json:"description,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	if req.Version <= 0 {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "version is required and must be positive",
		})
		return
	}

	// Get user from request header if not provided
	if req.User == "" {
		req.User = r.Header.Get("X-User-ID")
	}

	if err := g.configManager.RestoreHistoryVersion(req.Version, req.User, req.Description); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Restored configuration to version %d", req.Version),
		"version": req.Version,
	})
}

// Middleware

func (g *Gateway) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token == "" {
			http.Error(w, "Missing authorization", http.StatusUnauthorized)
			return
		}

		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}

		userID, err := g.authService.ValidateToken(token)
		if err != nil {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		// Add user ID to context (simplified)
		r.Header.Set("X-User-ID", userID)

		// Check rate limit
		allowed, err := g.rateLimiter.Allow(
			userID,
			g.rateConfig.DefaultCapacity,
			g.rateConfig.DefaultRateNum,
			g.rateConfig.DefaultRateDen,
		)
		if err != nil || !allowed {
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next(w, r)
	}
}

func (g *Gateway) requirePermission(permission rbac.Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-ID")
		if userID == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if !g.rbacService.HasPermission(userID, permission) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		next(w, r)
	}
}

// Helper functions

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// Server wraps the HTTP server
type Server struct {
	gateway *Gateway
	server  *http.Server
}

// NewServer creates a new API server
func NewServer(address string, gateway *Gateway) *Server {
	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)

	return &Server{
		gateway: gateway,
		server: &http.Server{
			Addr:    address,
			Handler: mux,
		},
	}
}

// Start starts the server
func (s *Server) Start() error {
	fmt.Printf("API Gateway listening on %s\n", s.server.Addr)
	return s.server.ListenAndServe()
}

// Stop stops the server gracefully
func (s *Server) Stop() error {
	return s.server.Close()
}
