package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/helios/helios/internal/auth"
	"github.com/helios/helios/internal/auth/rbac"
	"github.com/helios/helios/internal/config"
	"github.com/helios/helios/internal/graphql"
	"github.com/helios/helios/internal/observability"
	"github.com/helios/helios/internal/plugin"
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
	pluginManager  *plugin.Manager  // Plugin manager (optional)
	logger         *observability.Logger

	pluginAuditMu      sync.RWMutex
	pluginAuditTrail   []pluginAdminAuditRecord
	pluginAuditLimit   int
	pluginAuditCounter uint64

	pluginAuditPersistenceEnabled  bool
	pluginAuditFilePath            string
	pluginAuditMaxPersistedRecords int
	pluginAuditMaxPersistedAge     time.Duration
	pluginAuditCompactEvery        int
	pluginAuditCompactTick         uint64
	pluginAuditLastPersistError    string
	pluginAuditRepairMode          pluginAdminAuditRepairMode
	pluginAuditRepairSkippedLines  uint64
	pluginAuditLastRepairAt        time.Time

	pluginAuditBackgroundCompactInterval  time.Duration
	pluginAuditBackgroundCompactLastRun   time.Time
	pluginAuditBackgroundCompactLastError string
	pluginAuditBackgroundCompactStopCh    chan struct{}
	pluginAuditBackgroundCompactDoneCh    chan struct{}

	pluginAuditFileMu sync.Mutex
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

	gateway := &Gateway{
		authService:                    authService,
		rbacService:                    rbacService,
		rateLimiter:                    rateLimiter,
		queue:                          queue,
		rateConfig:                     rateConfig,
		raftAtlas:                      nil,                   // Will be set later if Raft is enabled
		wsHub:                          NewWSHub(nil, logger), // WebSocket hub
		graphqlHandler:                 nil,                   // Will be set when GraphQL resolver is configured
		logger:                         obsLogger,
		pluginAuditTrail:               make([]pluginAdminAuditRecord, 0, 512),
		pluginAuditLimit:               defaultPluginAdminAuditMemoryLimit,
		pluginAuditMaxPersistedRecords: defaultPluginAdminAuditMaxRecords,
		pluginAuditMaxPersistedAge:     defaultPluginAdminAuditMaxAge,
		pluginAuditCompactEvery:        defaultPluginAdminAuditCompactEvery,
	}

	gateway.configurePluginAdminAuditStorage()

	return gateway
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

// SetPluginManager sets the plugin manager for middleware/event integrations.
func (g *Gateway) SetPluginManager(manager *plugin.Manager) {
	g.pluginManager = manager
}

// RegisterRoutes registers all API routes
func (g *Gateway) RegisterRoutes(mux *http.ServeMux) {
	// GraphQL endpoints
	if g.graphqlHandler != nil {
		mux.Handle("/graphql", g.graphqlHandler)
		mux.Handle("/graphql/playground", graphql.PlaygroundHandler())
		if g.graphqlHandler.IsSchemaDocumentationHTTPEnabled() {
			mux.Handle("/graphql/docs", g.graphqlHandler.SchemaDocumentationHTTPHandler())
		}
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

	// Plugin runtime operations endpoints
	mux.HandleFunc("/admin/plugins", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handlePlugins)))
	mux.HandleFunc("/admin/plugins/audit", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handlePluginAudit)))
	mux.HandleFunc("/admin/plugins/audit/", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handlePluginAudit)))
	mux.HandleFunc("/admin/plugins/audit/compact", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handlePluginAuditCompact)))
	mux.HandleFunc("/admin/plugins/audit/compact/", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handlePluginAuditCompact)))
	mux.HandleFunc("/admin/plugins/bulk", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handlePluginBulk)))
	mux.HandleFunc("/admin/plugins/bulk/", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handlePluginBulk)))
	mux.HandleFunc("/admin/plugins/", g.requireAuth(g.requirePermission(rbac.PermAdmin, g.handlePluginOperation)))
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
	g.stopPluginAdminAuditCompactionScheduler()

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

// Plugin runtime operation handlers

type pluginSetEnabledRequest struct {
	Enabled *bool `json:"enabled"`
}

type pluginReloadRequest struct {
	Enabled         *bool                  `json:"enabled,omitempty"`
	Priority        *int                   `json:"priority,omitempty"`
	Timeout         json.RawMessage        `json:"timeout,omitempty"`
	FailOpen        *bool                  `json:"fail_open,omitempty"`
	MaxFailures     *int                   `json:"max_failures,omitempty"`
	Cooldown        json.RawMessage        `json:"cooldown,omitempty"`
	Settings        map[string]interface{} `json:"settings,omitempty"`
	ReplaceSettings bool                   `json:"replace_settings,omitempty"`
}

type pluginBulkRequest struct {
	Action          string                  `json:"action"`
	Items           []pluginBulkItemRequest `json:"items"`
	Enabled         *bool                   `json:"enabled,omitempty"`
	DefaultReload   *pluginReloadRequest    `json:"default_reload,omitempty"`
	ContinueOnError *bool                   `json:"continue_on_error,omitempty"`
	RollbackOnError *bool                   `json:"rollback_on_error,omitempty"`
	DryRun          bool                    `json:"dry_run,omitempty"`
	RefreshHealth   *bool                   `json:"refresh_health,omitempty"`
	IncludeRuntime  *bool                   `json:"include_runtime,omitempty"`
	IncludeStats    *bool                   `json:"include_stats,omitempty"`
}

type pluginBulkItemRequest struct {
	Name    string               `json:"name"`
	Enabled *bool                `json:"enabled,omitempty"`
	Reload  *pluginReloadRequest `json:"reload,omitempty"`
}

type pluginBulkRollbackEntry struct {
	Name           string
	PreviousConfig plugin.RuntimeConfig
	ResultIndex    int
}

type pluginAdminAuditRecord struct {
	ID         string                 `json:"id"`
	Timestamp  time.Time              `json:"timestamp"`
	Action     string                 `json:"action"`
	Target     string                 `json:"target"`
	Success    bool                   `json:"success"`
	UserID     string                 `json:"user_id"`
	Method     string                 `json:"method"`
	Path       string                 `json:"path"`
	RemoteAddr string                 `json:"remote_addr"`
	Error      string                 `json:"error,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

const (
	pluginAdminAuditPersistenceEnv               = "HELIOS_PLUGIN_ADMIN_AUDIT_PERSIST"
	pluginAdminAuditDirEnv                       = "HELIOS_PLUGIN_ADMIN_AUDIT_DIR"
	pluginAdminAuditFileEnv                      = "HELIOS_PLUGIN_ADMIN_AUDIT_FILE"
	pluginAdminAuditMemoryLimitEnv               = "HELIOS_PLUGIN_ADMIN_AUDIT_MEMORY_LIMIT"
	pluginAdminAuditMaxRecordsEnv                = "HELIOS_PLUGIN_ADMIN_AUDIT_MAX_RECORDS"
	pluginAdminAuditMaxAgeEnv                    = "HELIOS_PLUGIN_ADMIN_AUDIT_MAX_AGE"
	pluginAdminAuditCompactEveryEnv              = "HELIOS_PLUGIN_ADMIN_AUDIT_COMPACT_EVERY"
	pluginAdminAuditRepairModeEnv                = "HELIOS_PLUGIN_ADMIN_AUDIT_REPAIR_MODE"
	pluginAdminAuditBackgroundCompactIntervalEnv = "HELIOS_PLUGIN_ADMIN_AUDIT_BACKGROUND_COMPACT_INTERVAL"

	defaultPluginAdminAuditMemoryLimit  = 4096
	defaultPluginAdminAuditMaxRecords   = 50000
	defaultPluginAdminAuditCompactEvery = 250
)

var defaultPluginAdminAuditMaxAge = 30 * 24 * time.Hour

type pluginAdminAuditSource string

const (
	pluginAuditSourceAuto       pluginAdminAuditSource = "auto"
	pluginAuditSourceMemory     pluginAdminAuditSource = "memory"
	pluginAuditSourcePersistent pluginAdminAuditSource = "persistent"
)

type pluginAdminAuditRepairMode string

const (
	pluginAuditRepairModeStrict       pluginAdminAuditRepairMode = "strict"
	pluginAuditRepairModeSkipBadLines pluginAdminAuditRepairMode = "skip_bad_lines"
)

func parsePluginAdminAuditSource(raw string) (pluginAdminAuditSource, error) {
	source := strings.ToLower(strings.TrimSpace(raw))
	if source == "" {
		return pluginAuditSourceAuto, nil
	}

	parsed := pluginAdminAuditSource(source)
	switch parsed {
	case pluginAuditSourceAuto, pluginAuditSourceMemory, pluginAuditSourcePersistent:
		return parsed, nil
	default:
		return "", fmt.Errorf("query parameter %q must be one of auto, memory, persistent", "source")
	}
}

func parsePluginAdminAuditRepairMode(raw string, defaultMode pluginAdminAuditRepairMode) pluginAdminAuditRepairMode {
	mode := pluginAdminAuditRepairMode(strings.ToLower(strings.TrimSpace(raw)))
	if mode == "" {
		return defaultMode
	}

	switch mode {
	case pluginAuditRepairModeStrict, pluginAuditRepairModeSkipBadLines:
		return mode
	default:
		return defaultMode
	}
}

func parseEnvIntInRange(key string, defaultValue, minValue, maxValue int) int {
	if maxValue < minValue {
		minValue, maxValue = maxValue, minValue
	}

	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		if defaultValue < minValue {
			return minValue
		}
		if defaultValue > maxValue {
			return maxValue
		}
		return defaultValue
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		if defaultValue < minValue {
			return minValue
		}
		if defaultValue > maxValue {
			return maxValue
		}
		return defaultValue
	}

	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}

	return value
}

func parseEnvDurationWithMinimum(key string, defaultValue, minValue time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil {
		if seconds, intErr := strconv.Atoi(raw); intErr == nil {
			parsed = time.Duration(seconds) * time.Second
		} else {
			return defaultValue
		}
	}

	if parsed <= 0 {
		return 0
	}

	if parsed < minValue {
		return minValue
	}

	return parsed
}

func parseEnvBool(key string, defaultValue bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}

	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return defaultValue
	}

	return parsed
}

func (g *Gateway) configurePluginAdminAuditStorage() {
	g.pluginAuditLimit = parseEnvIntInRange(pluginAdminAuditMemoryLimitEnv, g.pluginAuditLimit, 1, 500000)
	g.pluginAuditMaxPersistedRecords = parseEnvIntInRange(pluginAdminAuditMaxRecordsEnv, g.pluginAuditMaxPersistedRecords, 1, 5000000)
	g.pluginAuditMaxPersistedAge = parseEnvDurationWithMinimum(pluginAdminAuditMaxAgeEnv, g.pluginAuditMaxPersistedAge, time.Minute)
	g.pluginAuditCompactEvery = parseEnvIntInRange(pluginAdminAuditCompactEveryEnv, g.pluginAuditCompactEvery, 1, 100000)
	g.pluginAuditRepairMode = parsePluginAdminAuditRepairMode(os.Getenv(pluginAdminAuditRepairModeEnv), pluginAuditRepairModeStrict)
	g.pluginAuditBackgroundCompactInterval = parseEnvDurationWithMinimum(pluginAdminAuditBackgroundCompactIntervalEnv, g.pluginAuditBackgroundCompactInterval, time.Second)
	g.pluginAuditPersistenceEnabled = parseEnvBool(pluginAdminAuditPersistenceEnv, true)
	g.recordPluginAdminPersistError(nil)

	if !g.pluginAuditPersistenceEnabled {
		g.stopPluginAdminAuditCompactionScheduler()
		return
	}

	path := strings.TrimSpace(os.Getenv(pluginAdminAuditFileEnv))
	if path == "" {
		dir := strings.TrimSpace(os.Getenv(pluginAdminAuditDirEnv))
		if dir == "" {
			dir = filepath.Join("data", "audit")
		}
		path = filepath.Join(dir, "plugin-admin-audit.jsonl")
	}

	g.pluginAuditFilePath = filepath.Clean(path)

	if err := os.MkdirAll(filepath.Dir(g.pluginAuditFilePath), 0o755); err != nil {
		g.recordPluginAdminPersistError(fmt.Errorf("failed to create audit storage directory: %w", err))
		g.pluginAuditPersistenceEnabled = false
		g.stopPluginAdminAuditCompactionScheduler()
		return
	}

	records, err := g.pluginAdminAuditSnapshotFromPersistent()
	if err != nil {
		g.recordPluginAdminPersistError(fmt.Errorf("failed to read existing persistent audit records: %w", err))
		return
	}

	g.replacePluginAdminAuditTrail(records)

	if _, err := g.compactPluginAdminAuditStore(true); err != nil {
		g.recordPluginAdminPersistError(fmt.Errorf("failed to compact persistent audit records during startup: %w", err))
	}

	g.startPluginAdminAuditCompactionScheduler()
}

func (g *Gateway) recordPluginAdminPersistError(err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}

	g.pluginAuditMu.Lock()
	g.pluginAuditLastPersistError = message
	g.pluginAuditMu.Unlock()

	if err != nil && g.logger != nil {
		g.logger.Warn("Plugin admin audit persistence issue", map[string]interface{}{
			"error": message,
			"path":  g.pluginAuditFilePath,
		})
	}
}

func (g *Gateway) pluginAdminAuditPersistenceMetadata() map[string]interface{} {
	g.pluginAuditMu.RLock()
	enabled := g.pluginAuditPersistenceEnabled
	filePath := g.pluginAuditFilePath
	maxRecords := g.pluginAuditMaxPersistedRecords
	maxAge := g.pluginAuditMaxPersistedAge
	compactEvery := g.pluginAuditCompactEvery
	repairMode := g.pluginAuditRepairMode
	repairSkippedLines := g.pluginAuditRepairSkippedLines
	lastRepairAt := g.pluginAuditLastRepairAt
	backgroundInterval := g.pluginAuditBackgroundCompactInterval
	backgroundLastRun := g.pluginAuditBackgroundCompactLastRun
	backgroundLastError := g.pluginAuditBackgroundCompactLastError
	backgroundRunning := g.pluginAuditBackgroundCompactStopCh != nil
	lastPersistError := g.pluginAuditLastPersistError
	inMemory := len(g.pluginAuditTrail)
	g.pluginAuditMu.RUnlock()

	metadata := map[string]interface{}{
		"enabled":                     enabled,
		"file_path":                   filePath,
		"memory_limit":                g.pluginAuditLimit,
		"max_persisted_records":       maxRecords,
		"compact_every":               compactEvery,
		"repair_mode":                 repairMode,
		"skipped_corrupt_lines_total": repairSkippedLines,
		"records_in_memory":           inMemory,
	}

	if maxAge > 0 {
		metadata["max_persisted_age"] = maxAge.String()
		metadata["max_persisted_age_seconds"] = int64(maxAge.Seconds())
	} else {
		metadata["max_persisted_age"] = "disabled"
		metadata["max_persisted_age_seconds"] = int64(0)
	}

	if strings.TrimSpace(lastPersistError) != "" {
		metadata["last_persist_error"] = lastPersistError
	}

	if !lastRepairAt.IsZero() {
		metadata["last_repair_at"] = lastRepairAt.UTC().Format(time.RFC3339Nano)
	}

	metadata["background_compaction_running"] = backgroundRunning
	if backgroundInterval > 0 {
		metadata["background_compaction_interval"] = backgroundInterval.String()
		metadata["background_compaction_interval_seconds"] = int64(backgroundInterval.Seconds())
	} else {
		metadata["background_compaction_interval"] = "disabled"
		metadata["background_compaction_interval_seconds"] = int64(0)
	}

	if !backgroundLastRun.IsZero() {
		metadata["background_compaction_last_run"] = backgroundLastRun.UTC().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(backgroundLastError) != "" {
		metadata["background_compaction_last_error"] = backgroundLastError
	}

	if enabled && strings.TrimSpace(filePath) != "" {
		if info, err := os.Stat(filePath); err == nil {
			metadata["file_size_bytes"] = info.Size()
			metadata["file_last_modified"] = info.ModTime().UTC().Format(time.RFC3339Nano)
		}
	}

	return metadata
}

func (g *Gateway) pluginAdminAuditSnapshotForSource(source pluginAdminAuditSource) ([]pluginAdminAuditRecord, pluginAdminAuditSource, string, error) {
	switch source {
	case pluginAuditSourceMemory:
		return g.pluginAdminAuditSnapshot(), pluginAuditSourceMemory, "", nil
	case pluginAuditSourcePersistent:
		if !g.pluginAuditPersistenceEnabled || strings.TrimSpace(g.pluginAuditFilePath) == "" {
			return nil, "", "", errors.New("persistent plugin admin audit storage is not enabled")
		}
		records, err := g.pluginAdminAuditSnapshotFromPersistent()
		if err != nil {
			g.recordPluginAdminPersistError(fmt.Errorf("failed to read persistent plugin admin audit records: %w", err))
			return nil, "", "", err
		}
		return records, pluginAuditSourcePersistent, "", nil
	default:
		if g.pluginAuditPersistenceEnabled && strings.TrimSpace(g.pluginAuditFilePath) != "" {
			records, err := g.pluginAdminAuditSnapshotFromPersistent()
			if err == nil {
				return records, pluginAuditSourcePersistent, "", nil
			}

			g.recordPluginAdminPersistError(fmt.Errorf("failed to read persistent plugin admin audit records: %w", err))
			warning := fmt.Sprintf("Persistent source unavailable: %v. Falling back to in-memory records.", err)
			return g.pluginAdminAuditSnapshot(), pluginAuditSourceMemory, warning, nil
		}

		return g.pluginAdminAuditSnapshot(), pluginAuditSourceMemory, "", nil
	}
}

func (g *Gateway) pluginAdminAuditSnapshotFromPersistent() ([]pluginAdminAuditRecord, error) {
	g.pluginAuditFileMu.Lock()
	defer g.pluginAuditFileMu.Unlock()

	return g.pluginAdminAuditSnapshotFromPersistentLocked()
}

func (g *Gateway) pluginAdminAuditSnapshotFromPersistentLocked() ([]pluginAdminAuditRecord, error) {
	records, err := g.readPluginAdminAuditRecordsLocked()
	if err != nil {
		return nil, err
	}

	return g.applyPluginAdminAuditRetention(records), nil
}

func (g *Gateway) shouldSkipCorruptPluginAdminAuditLines() bool {
	g.pluginAuditMu.RLock()
	mode := g.pluginAuditRepairMode
	g.pluginAuditMu.RUnlock()

	return mode == pluginAuditRepairModeSkipBadLines
}

func (g *Gateway) recordPluginAdminAuditRepairSkippedLines(skipped int) {
	if skipped <= 0 {
		return
	}

	g.pluginAuditMu.Lock()
	g.pluginAuditRepairSkippedLines += uint64(skipped)
	g.pluginAuditLastRepairAt = time.Now().UTC()
	g.pluginAuditMu.Unlock()
}

func (g *Gateway) readPluginAdminAuditRecordsLocked() ([]pluginAdminAuditRecord, error) {
	path := strings.TrimSpace(g.pluginAuditFilePath)
	if path == "" {
		return nil, errors.New("persistent plugin admin audit path is not configured")
	}

	records, skippedCorruptLines, err := g.readPluginAdminAuditRecordsFromPathLocked(path, g.shouldSkipCorruptPluginAdminAuditLines())
	if err != nil {
		return nil, err
	}

	if skippedCorruptLines > 0 {
		g.recordPluginAdminAuditRepairSkippedLines(skippedCorruptLines)
		if g.logger != nil {
			g.logger.Warn("Skipped corrupt plugin admin audit lines during load", map[string]interface{}{
				"path":                  path,
				"skipped_corrupt_lines": skippedCorruptLines,
				"repair_mode":           g.pluginAuditRepairMode,
			})
		}
	}

	return records, nil
}

func (g *Gateway) readPluginAdminAuditRecordsFromPathLocked(path string, skipCorruptLines bool) ([]pluginAdminAuditRecord, int, error) {

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []pluginAdminAuditRecord{}, 0, nil
		}
		return nil, 0, fmt.Errorf("failed to open audit storage file %q: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	records := make([]pluginAdminAuditRecord, 0, 1024)
	skippedCorruptLines := 0
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record pluginAdminAuditRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			if skipCorruptLines {
				skippedCorruptLines++
				continue
			}
			return nil, skippedCorruptLines, fmt.Errorf("failed to decode audit record from %q at line %d: %w", path, lineNo, err)
		}
		if record.Metadata != nil {
			record.Metadata = cloneStringAnyMap(record.Metadata)
		}
		records = append(records, record)
	}

	if err := scanner.Err(); err != nil {
		return nil, skippedCorruptLines, fmt.Errorf("failed to read audit storage file %q: %w", path, err)
	}

	return records, skippedCorruptLines, nil
}

func (g *Gateway) applyPluginAdminAuditRetention(records []pluginAdminAuditRecord) []pluginAdminAuditRecord {
	if len(records) == 0 {
		return []pluginAdminAuditRecord{}
	}

	g.pluginAuditMu.RLock()
	maxRecords := g.pluginAuditMaxPersistedRecords
	maxAge := g.pluginAuditMaxPersistedAge
	g.pluginAuditMu.RUnlock()

	retained := make([]pluginAdminAuditRecord, 0, len(records))
	cutoff := time.Time{}
	if maxAge > 0 {
		cutoff = time.Now().UTC().Add(-maxAge)
	}

	for _, record := range records {
		if !cutoff.IsZero() && !record.Timestamp.IsZero() && record.Timestamp.Before(cutoff) {
			continue
		}
		retained = append(retained, record)
	}

	if maxRecords > 0 && len(retained) > maxRecords {
		retained = retained[len(retained)-maxRecords:]
	}

	return clonePluginAdminAuditRecords(retained)
}

func clonePluginAdminAuditRecords(records []pluginAdminAuditRecord) []pluginAdminAuditRecord {
	if len(records) == 0 {
		return []pluginAdminAuditRecord{}
	}

	cloned := make([]pluginAdminAuditRecord, len(records))
	for i, record := range records {
		cloned[i] = record
		if record.Metadata != nil {
			cloned[i].Metadata = cloneStringAnyMap(record.Metadata)
		}
	}

	return cloned
}

func (g *Gateway) replacePluginAdminAuditTrail(records []pluginAdminAuditRecord) {
	cloned := clonePluginAdminAuditRecords(records)

	g.pluginAuditMu.Lock()
	defer g.pluginAuditMu.Unlock()

	if g.pluginAuditLimit <= 0 {
		g.pluginAuditLimit = defaultPluginAdminAuditMemoryLimit
	}

	if len(cloned) > g.pluginAuditLimit {
		cloned = cloned[len(cloned)-g.pluginAuditLimit:]
	}

	g.pluginAuditTrail = cloned
}

func (g *Gateway) appendPluginAdminAuditRecordToPersistent(record pluginAdminAuditRecord) {
	g.pluginAuditMu.RLock()
	persistenceEnabled := g.pluginAuditPersistenceEnabled
	storagePath := strings.TrimSpace(g.pluginAuditFilePath)
	compactEvery := g.pluginAuditCompactEvery
	g.pluginAuditMu.RUnlock()

	if !persistenceEnabled || storagePath == "" {
		return
	}

	payload, err := json.Marshal(record)
	if err != nil {
		g.recordPluginAdminPersistError(fmt.Errorf("failed to encode plugin admin audit record for persistence: %w", err))
		return
	}

	g.pluginAuditFileMu.Lock()
	if err := os.MkdirAll(filepath.Dir(storagePath), 0o755); err != nil {
		g.pluginAuditFileMu.Unlock()
		g.recordPluginAdminPersistError(fmt.Errorf("failed to create audit storage directory: %w", err))
		return
	}

	file, err := os.OpenFile(storagePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		g.pluginAuditFileMu.Unlock()
		g.recordPluginAdminPersistError(fmt.Errorf("failed to open plugin admin audit storage for append: %w", err))
		return
	}

	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		g.pluginAuditFileMu.Unlock()
		g.recordPluginAdminPersistError(fmt.Errorf("failed to append plugin admin audit record: %w", err))
		return
	}

	if _, err := file.Write([]byte("\n")); err != nil {
		_ = file.Close()
		g.pluginAuditFileMu.Unlock()
		g.recordPluginAdminPersistError(fmt.Errorf("failed to write plugin admin audit record delimiter: %w", err))
		return
	}

	if err := file.Close(); err != nil {
		g.pluginAuditFileMu.Unlock()
		g.recordPluginAdminPersistError(fmt.Errorf("failed to close plugin admin audit storage after append: %w", err))
		return
	}
	g.pluginAuditFileMu.Unlock()

	g.recordPluginAdminPersistError(nil)

	if compactEvery <= 0 {
		return
	}

	tick := atomic.AddUint64(&g.pluginAuditCompactTick, 1)
	if tick%uint64(compactEvery) == 0 {
		if _, err := g.compactPluginAdminAuditStore(false); err != nil {
			g.recordPluginAdminPersistError(fmt.Errorf("periodic plugin admin audit compaction failed: %w", err))
		}
	}
}

func (g *Gateway) compactPluginAdminAuditStore(force bool) (map[string]interface{}, error) {
	if !g.pluginAuditPersistenceEnabled || strings.TrimSpace(g.pluginAuditFilePath) == "" {
		return nil, errors.New("persistent plugin admin audit storage is not enabled")
	}

	g.pluginAuditFileMu.Lock()
	compaction, retained, err := g.compactPluginAdminAuditStoreLocked(force)
	g.pluginAuditFileMu.Unlock()
	if err != nil {
		return nil, err
	}

	g.replacePluginAdminAuditTrail(retained)
	g.recordPluginAdminPersistError(nil)

	return compaction, nil
}

func (g *Gateway) compactPluginAdminAuditStoreLocked(force bool) (map[string]interface{}, []pluginAdminAuditRecord, error) {
	rawRecords, err := g.readPluginAdminAuditRecordsLocked()
	if err != nil {
		return nil, nil, err
	}

	beforeRecords := len(rawRecords)
	retained := g.applyPluginAdminAuditRetention(rawRecords)
	afterRecords := len(retained)

	rewritten := force || beforeRecords != afterRecords
	if rewritten {
		if err := g.writePluginAdminAuditRecordsLocked(retained); err != nil {
			return nil, nil, err
		}
	}

	compaction := map[string]interface{}{
		"forced":          force,
		"rewritten":       rewritten,
		"before_records":  beforeRecords,
		"after_records":   afterRecords,
		"removed_records": beforeRecords - afterRecords,
		"timestamp":       time.Now().UTC().Format(time.RFC3339Nano),
	}

	return compaction, retained, nil
}

func (g *Gateway) writePluginAdminAuditRecordsLocked(records []pluginAdminAuditRecord) error {
	path := strings.TrimSpace(g.pluginAuditFilePath)
	if path == "" {
		return errors.New("persistent plugin admin audit path is not configured")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create audit storage directory: %w", err)
	}

	tmpPath := path + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open temporary plugin admin audit storage file: %w", err)
	}

	writer := bufio.NewWriter(file)
	for _, record := range records {
		payload, err := json.Marshal(record)
		if err != nil {
			_ = file.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to encode plugin admin audit record for compaction: %w", err)
		}

		if _, err := writer.Write(payload); err != nil {
			_ = file.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to write compacted plugin admin audit record: %w", err)
		}

		if err := writer.WriteByte('\n'); err != nil {
			_ = file.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to write compacted plugin admin audit newline: %w", err)
		}
	}

	if err := writer.Flush(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to flush compacted plugin admin audit storage: %w", err)
	}

	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to sync compacted plugin admin audit storage: %w", err)
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close compacted plugin admin audit storage: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(path)
		if renameErr := os.Rename(tmpPath, path); renameErr != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to replace plugin admin audit storage file: %w", renameErr)
		}
	}

	return nil
}

func (g *Gateway) startPluginAdminAuditCompactionScheduler() {
	g.pluginAuditMu.Lock()
	interval := g.pluginAuditBackgroundCompactInterval
	enabled := g.pluginAuditPersistenceEnabled && strings.TrimSpace(g.pluginAuditFilePath) != ""
	if !enabled || interval <= 0 {
		g.pluginAuditMu.Unlock()
		return
	}

	if g.pluginAuditBackgroundCompactStopCh != nil {
		g.pluginAuditMu.Unlock()
		return
	}

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	g.pluginAuditBackgroundCompactStopCh = stopCh
	g.pluginAuditBackgroundCompactDoneCh = doneCh
	g.pluginAuditBackgroundCompactLastError = ""
	g.pluginAuditMu.Unlock()

	go g.runPluginAdminAuditCompactionScheduler(interval, stopCh, doneCh)
}

func (g *Gateway) runPluginAdminAuditCompactionScheduler(interval time.Duration, stopCh <-chan struct{}, doneCh chan<- struct{}) {
	ticker := time.NewTicker(interval)
	defer func() {
		ticker.Stop()
		close(doneCh)
	}()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			compaction, err := g.compactPluginAdminAuditStore(false)
			now := time.Now().UTC()

			g.pluginAuditMu.Lock()
			g.pluginAuditBackgroundCompactLastRun = now
			if err != nil {
				g.pluginAuditBackgroundCompactLastError = err.Error()
			} else {
				g.pluginAuditBackgroundCompactLastError = ""
			}
			g.pluginAuditMu.Unlock()

			if err != nil {
				g.recordPluginAdminPersistError(fmt.Errorf("background plugin admin audit compaction failed: %w", err))
				continue
			}

			if g.logger != nil {
				g.logger.Info("Background plugin admin audit compaction completed", map[string]interface{}{
					"interval":   interval.String(),
					"compaction": compaction,
				})
			}
		}
	}
}

func (g *Gateway) stopPluginAdminAuditCompactionScheduler() {
	g.pluginAuditMu.Lock()
	stopCh := g.pluginAuditBackgroundCompactStopCh
	doneCh := g.pluginAuditBackgroundCompactDoneCh
	g.pluginAuditBackgroundCompactStopCh = nil
	g.pluginAuditBackgroundCompactDoneCh = nil
	g.pluginAuditMu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}
}

func (g *Gateway) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if !g.ensurePluginManager(w) {
		return
	}

	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false,
			"error":   "Method not allowed",
		})
		return
	}

	refreshHealth, err := parseOptionalBoolQuery(r, "refresh_health", true)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	includeRuntime, err := parseOptionalBoolQuery(r, "include_runtime", true)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	includeStats, err := parseOptionalBoolQuery(r, "include_stats", true)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	var health map[string]plugin.PluginHealth
	if refreshHealth {
		health = g.pluginManager.HealthCheck(r.Context())
	}

	stats := g.pluginManager.Stats()
	statsByName := make(map[string]plugin.PluginStats, len(stats.Plugins))
	for _, st := range stats.Plugins {
		statsByName[st.Name] = st
	}

	pluginNames := g.pluginManager.PluginNames()
	plugins := make([]map[string]interface{}, 0, len(pluginNames))
	for _, name := range pluginNames {
		entry := map[string]interface{}{"name": name}

		if includeRuntime {
			if cfg, ok := g.pluginManager.RuntimeConfig(name); ok {
				entry["runtime"] = runtimeConfigToJSON(cfg)
			}
		}

		if includeStats {
			if st, ok := statsByName[name]; ok {
				entry["state"] = pluginStatsToStateJSON(st)
			}
		}

		if refreshHealth {
			if h, ok := health[name]; ok {
				entry["health"] = h
			}
		} else if st, ok := statsByName[name]; ok {
			entry["health"] = st.Health
		}

		plugins = append(plugins, entry)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"manager": map[string]interface{}{
			"running":          stats.Running,
			"plugin_count":     stats.PluginCount,
			"published_events": stats.PublishedEvents,
			"dropped_events":   stats.DroppedEvents,
			"health_refreshed": refreshHealth,
		},
		"plugins": plugins,
		"count":   len(plugins),
	})
}

func (g *Gateway) handlePluginAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false,
			"error":   "Method not allowed",
		})
		return
	}

	if r.URL.Path != "/admin/plugins/audit" && r.URL.Path != "/admin/plugins/audit/" {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   "Endpoint not found",
		})
		return
	}

	limit, err := parseOptionalIntQuery(r, "limit", 100, 1, 1000)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	offset, err := parseOptionalIntQuery(r, "offset", 0, 0, 1000000)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	sortOrder := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))
	if sortOrder == "" {
		sortOrder = "desc"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "query parameter \"sort\" must be either \"asc\" or \"desc\"",
		})
		return
	}

	successFilter, hasSuccessFilter, err := parseOptionalBoolQueryPresence(r, "success")
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	since, hasSince, err := parseOptionalTimeQuery(r, "since")
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	until, hasUntil, err := parseOptionalTimeQuery(r, "until")
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	if hasSince && hasUntil && since.After(until) {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "query parameter \"since\" cannot be after \"until\"",
		})
		return
	}

	actionFilter := strings.TrimSpace(r.URL.Query().Get("action"))
	targetFilter := strings.TrimSpace(r.URL.Query().Get("target"))
	userIDFilter := strings.TrimSpace(r.URL.Query().Get("user_id"))
	pathFilter := strings.TrimSpace(r.URL.Query().Get("path"))
	containsFilter := strings.TrimSpace(r.URL.Query().Get("contains"))

	source, err := parsePluginAdminAuditSource(r.URL.Query().Get("source"))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	snapshot, snapshotSource, sourceWarning, err := g.pluginAdminAuditSnapshotForSource(source)
	if err != nil {
		status := http.StatusInternalServerError
		if source == pluginAuditSourcePersistent {
			status = http.StatusServiceUnavailable
		}
		respondJSON(w, status, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	filtered := make([]pluginAdminAuditRecord, 0, len(snapshot))
	actionBreakdown := make(map[string]int)
	successCount := 0
	failureCount := 0

	for _, record := range snapshot {
		if actionFilter != "" && !strings.EqualFold(record.Action, actionFilter) {
			continue
		}
		if targetFilter != "" && !strings.EqualFold(record.Target, targetFilter) {
			continue
		}
		if userIDFilter != "" && !strings.EqualFold(record.UserID, userIDFilter) {
			continue
		}
		if pathFilter != "" && !strings.EqualFold(record.Path, pathFilter) {
			continue
		}
		if hasSuccessFilter && record.Success != successFilter {
			continue
		}
		if hasSince && record.Timestamp.Before(since) {
			continue
		}
		if hasUntil && record.Timestamp.After(until) {
			continue
		}
		if containsFilter != "" && !pluginAdminAuditRecordContains(record, containsFilter) {
			continue
		}

		filtered = append(filtered, record)
		actionBreakdown[record.Action]++
		if record.Success {
			successCount++
		} else {
			failureCount++
		}
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		if sortOrder == "asc" {
			if filtered[i].Timestamp.Equal(filtered[j].Timestamp) {
				return filtered[i].ID < filtered[j].ID
			}
			return filtered[i].Timestamp.Before(filtered[j].Timestamp)
		}

		if filtered[i].Timestamp.Equal(filtered[j].Timestamp) {
			return filtered[i].ID > filtered[j].ID
		}
		return filtered[i].Timestamp.After(filtered[j].Timestamp)
	})

	totalMatched := len(filtered)
	if offset > totalMatched {
		offset = totalMatched
	}

	end := offset + limit
	if end > totalMatched {
		end = totalMatched
	}

	window := filtered[offset:end]

	filters := map[string]interface{}{
		"action":   actionFilter,
		"target":   targetFilter,
		"user_id":  userIDFilter,
		"path":     pathFilter,
		"contains": containsFilter,
		"source":   source,
		"sort":     sortOrder,
		"limit":    limit,
		"offset":   offset,
	}
	if hasSuccessFilter {
		filters["success"] = successFilter
	}
	if hasSince {
		filters["since"] = since.Format(time.RFC3339Nano)
	}
	if hasUntil {
		filters["until"] = until.Format(time.RFC3339Nano)
	}

	summary := map[string]interface{}{
		"total_records":       len(snapshot),
		"matched":             totalMatched,
		"returned":            len(window),
		"limit":               limit,
		"offset":              offset,
		"has_more":            end < totalMatched,
		"success_count":       successCount,
		"failure_count":       failureCount,
		"action_breakdown":    actionBreakdown,
		"source":              snapshotSource,
		"persistence_enabled": g.pluginAuditPersistenceEnabled,
	}
	if sourceWarning != "" {
		summary["source_warning"] = sourceWarning
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"summary":     summary,
		"filters":     filters,
		"records":     window,
		"persistence": g.pluginAdminAuditPersistenceMetadata(),
	})
}

func (g *Gateway) handlePluginAuditCompact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false,
			"error":   "Method not allowed",
		})
		return
	}

	if r.URL.Path != "/admin/plugins/audit/compact" && r.URL.Path != "/admin/plugins/audit/compact/" {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   "Endpoint not found",
		})
		return
	}

	if !g.pluginAuditPersistenceEnabled || strings.TrimSpace(g.pluginAuditFilePath) == "" {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false,
			"error":   "Persistent plugin admin audit storage is not enabled",
		})
		return
	}

	compaction, err := g.compactPluginAdminAuditStore(true)
	if err != nil {
		g.auditPluginAdminAction(r, "audit.compact", "audit", false, err, nil)
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	g.auditPluginAdminAction(r, "audit.compact", "audit", true, nil, map[string]interface{}{
		"compaction": compaction,
	})

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"message":    "Plugin admin audit storage compacted successfully",
		"compaction": compaction,
	})
}

func (g *Gateway) handlePluginBulk(w http.ResponseWriter, r *http.Request) {
	if !g.ensurePluginManager(w) {
		return
	}

	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false,
			"error":   "Method not allowed",
		})
		return
	}

	if r.URL.Path != "/admin/plugins/bulk" && r.URL.Path != "/admin/plugins/bulk/" {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   "Endpoint not found",
		})
		return
	}

	var req pluginBulkRequest
	if err := decodeJSONBodyStrict(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	if err := validatePluginBulkRequest(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	continueOnErrorRequested := boolValueOrDefault(req.ContinueOnError, true)
	rollbackOnError := boolValueOrDefault(req.RollbackOnError, false)
	continueOnError := continueOnErrorRequested
	if rollbackOnError {
		// Transactional rollback mode takes precedence and stops on first failure.
		continueOnError = false
	}

	refreshHealth := boolValueOrDefault(req.RefreshHealth, true)
	includeRuntime := boolValueOrDefault(req.IncludeRuntime, true)
	includeStats := boolValueOrDefault(req.IncludeStats, true)

	startedAt := time.Now().UTC()
	results := make([]map[string]interface{}, 0, len(req.Items))
	rollbackEntries := make([]pluginBulkRollbackEntry, 0, len(req.Items))
	var rollbackSummary map[string]interface{}

	var bulkHealthSnapshot map[string]plugin.PluginHealth
	if req.Action == "health" {
		bulkHealthSnapshot = g.pluginManager.HealthCheck(r.Context())
	}

	succeeded := 0
	failed := 0
	processed := 0

	for idx, item := range req.Items {
		result, ok, rollbackEntry := g.executePluginBulkItem(r, req, item, refreshHealth, includeRuntime, includeStats, bulkHealthSnapshot)
		result["index"] = idx
		results = append(results, result)
		processed++

		if ok {
			succeeded++
			if rollbackEntry != nil {
				rollbackEntry.ResultIndex = len(results) - 1
				rollbackEntries = append(rollbackEntries, *rollbackEntry)
			}
			continue
		}

		failed++

		if rollbackOnError && !req.DryRun && isMutatingPluginBulkAction(req.Action) && len(rollbackEntries) > 0 {
			rollbackSummary = g.rollbackPluginBulkChanges(r, req.Action, rollbackEntries, results, refreshHealth, includeRuntime, includeStats)
		}

		if !continueOnError {
			break
		}
	}

	skipped := len(req.Items) - processed
	duration := time.Since(startedAt)
	overallSuccess := failed == 0

	status := http.StatusOK
	if failed > 0 {
		status = http.StatusMultiStatus
	}

	auditExtra := map[string]interface{}{
		"bulk":                        true,
		"requested":                   len(req.Items),
		"processed":                   processed,
		"succeeded":                   succeeded,
		"failed":                      failed,
		"skipped":                     skipped,
		"continue_on_error":           continueOnError,
		"continue_on_error_requested": continueOnErrorRequested,
		"rollback_on_error":           rollbackOnError,
		"rollback_performed":          rollbackSummary != nil,
		"dry_run":                     req.DryRun,
	}
	if rollbackSummary != nil {
		auditExtra["rollback"] = rollbackSummary
	}

	var auditErr error
	if !overallSuccess {
		auditErr = fmt.Errorf("bulk %s completed with %d failures", req.Action, failed)
	}
	g.auditPluginAdminAction(r, "bulk."+req.Action, "bulk", overallSuccess, auditErr, auditExtra)

	response := map[string]interface{}{
		"success": overallSuccess,
		"action":  req.Action,
		"summary": map[string]interface{}{
			"requested":                   len(req.Items),
			"processed":                   processed,
			"succeeded":                   succeeded,
			"failed":                      failed,
			"skipped":                     skipped,
			"continue_on_error":           continueOnError,
			"continue_on_error_requested": continueOnErrorRequested,
			"rollback_on_error":           rollbackOnError,
			"rollback_performed":          rollbackSummary != nil,
			"dry_run":                     req.DryRun,
			"duration_ms":                 float64(duration.Microseconds()) / 1000.0,
			"started_at":                  startedAt.Format(time.RFC3339Nano),
			"completed_at":                startedAt.Add(duration).Format(time.RFC3339Nano),
		},
		"results": results,
	}

	if rollbackSummary != nil {
		response["rollback"] = rollbackSummary
	}

	respondJSON(w, status, response)
}

func (g *Gateway) executePluginBulkItem(r *http.Request, req pluginBulkRequest, item pluginBulkItemRequest, refreshHealth, includeRuntime, includeStats bool, healthSnapshot map[string]plugin.PluginHealth) (map[string]interface{}, bool, *pluginBulkRollbackEntry) {
	result := map[string]interface{}{
		"name":    item.Name,
		"action":  req.Action,
		"dry_run": req.DryRun,
	}

	var rollbackEntry *pluginBulkRollbackEntry

	auditExtra := map[string]interface{}{
		"bulk":    true,
		"dry_run": req.DryRun,
	}

	markFailure := func(err error) (map[string]interface{}, bool, *pluginBulkRollbackEntry) {
		statusCode, message := classifyPluginOperationError(item.Name, err)
		result["success"] = false
		result["status_code"] = statusCode
		result["error"] = message
		g.auditPluginAdminAction(r, req.Action, item.Name, false, err, auditExtra)
		return result, false, nil
	}

	switch req.Action {
	case "enable", "disable":
		enabled := req.Action == "enable"
		auditExtra["enabled"] = enabled

		currentCfg, ok := g.pluginManager.RuntimeConfig(item.Name)
		if !ok {
			return markFailure(fmt.Errorf("%w: %s", plugin.ErrPluginNotFound, item.Name))
		}

		if req.DryRun {
			preview := runtimeConfigToJSON(currentCfg)
			preview["enabled"] = enabled
			result["preview_runtime"] = preview
		} else {
			if err := g.pluginManager.SetEnabled(r.Context(), item.Name, enabled); err != nil {
				return markFailure(err)
			}
			rollbackEntry = &pluginBulkRollbackEntry{
				Name:           item.Name,
				PreviousConfig: clonePluginRuntimeConfig(currentCfg),
			}
		}

	case "set_enabled":
		enabled, err := resolveBulkEnabled(item.Enabled, req.Enabled)
		if err != nil {
			result["success"] = false
			result["status_code"] = http.StatusBadRequest
			result["error"] = err.Error()
			g.auditPluginAdminAction(r, req.Action, item.Name, false, err, auditExtra)
			return result, false, nil
		}

		auditExtra["enabled"] = enabled

		currentCfg, ok := g.pluginManager.RuntimeConfig(item.Name)
		if !ok {
			return markFailure(fmt.Errorf("%w: %s", plugin.ErrPluginNotFound, item.Name))
		}

		if req.DryRun {
			preview := runtimeConfigToJSON(currentCfg)
			preview["enabled"] = enabled
			result["preview_runtime"] = preview
		} else {
			if err := g.pluginManager.SetEnabled(r.Context(), item.Name, enabled); err != nil {
				return markFailure(err)
			}
			rollbackEntry = &pluginBulkRollbackEntry{
				Name:           item.Name,
				PreviousConfig: clonePluginRuntimeConfig(currentCfg),
			}
		}

	case "reload":
		currentCfg, ok := g.pluginManager.RuntimeConfig(item.Name)
		if !ok {
			return markFailure(fmt.Errorf("%w: %s", plugin.ErrPluginNotFound, item.Name))
		}

		effectiveReload := pluginReloadRequest{}
		if req.DefaultReload != nil {
			effectiveReload = clonePluginReloadRequest(*req.DefaultReload)
		}
		if item.Reload != nil {
			effectiveReload = mergePluginReloadRequests(effectiveReload, *item.Reload)
		}

		nextCfg, err := mergePluginRuntimeConfig(currentCfg, effectiveReload)
		if err != nil {
			return markFailure(err)
		}

		auditExtra["replace_settings"] = effectiveReload.ReplaceSettings
		if req.DryRun {
			result["preview_runtime"] = runtimeConfigToJSON(nextCfg)
		} else {
			if err := g.pluginManager.Reload(r.Context(), item.Name, nextCfg); err != nil {
				return markFailure(err)
			}
			rollbackEntry = &pluginBulkRollbackEntry{
				Name:           item.Name,
				PreviousConfig: clonePluginRuntimeConfig(currentCfg),
			}
		}

	case "health":
		pluginHealth, ok := healthSnapshot[item.Name]
		if !ok {
			if healthSnapshot == nil {
				healthMap := g.pluginManager.HealthCheck(r.Context())
				pluginHealth, ok = healthMap[item.Name]
			}
		}
		if !ok {
			return markFailure(fmt.Errorf("%w: %s", plugin.ErrPluginNotFound, item.Name))
		}

		result["health"] = pluginHealth
		if includeRuntime || includeStats {
			detail, err := g.pluginDetailPayload(r, item.Name, false, includeRuntime, includeStats)
			if err != nil {
				return markFailure(err)
			}
			detail["health"] = pluginHealth
			result["plugin"] = detail
		}

	default:
		return markFailure(fmt.Errorf("unsupported bulk action: %s", req.Action))
	}

	if !req.DryRun && req.Action != "health" {
		detail, err := g.pluginDetailPayload(r, item.Name, refreshHealth, includeRuntime, includeStats)
		if err != nil {
			return markFailure(err)
		}
		result["plugin"] = detail
	}

	result["success"] = true
	result["status_code"] = http.StatusOK
	g.auditPluginAdminAction(r, req.Action, item.Name, true, nil, auditExtra)
	return result, true, rollbackEntry
}

func validatePluginBulkRequest(req *pluginBulkRequest) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}

	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	if req.Action == "" {
		return fmt.Errorf("action is required")
	}

	switch req.Action {
	case "enable", "disable", "set_enabled", "reload", "health":
	default:
		return fmt.Errorf("unsupported action %q; supported actions: enable, disable, set_enabled, reload, health", req.Action)
	}

	if len(req.Items) == 0 {
		return fmt.Errorf("items must contain at least one plugin")
	}
	if len(req.Items) > 500 {
		return fmt.Errorf("items cannot exceed 500 plugins per request")
	}

	seen := make(map[string]struct{}, len(req.Items))
	hasPerItemEnabled := false
	for i := range req.Items {
		name := strings.TrimSpace(req.Items[i].Name)
		if name == "" {
			return fmt.Errorf("items[%d].name is required", i)
		}

		lookup := strings.ToLower(name)
		if _, exists := seen[lookup]; exists {
			return fmt.Errorf("duplicate plugin name in items: %s", name)
		}
		seen[lookup] = struct{}{}
		req.Items[i].Name = name

		if req.Items[i].Enabled != nil {
			hasPerItemEnabled = true
		}

		switch req.Action {
		case "enable", "disable":
			if req.Items[i].Enabled != nil {
				return fmt.Errorf("items[%d].enabled is not valid for %s action", i, req.Action)
			}
			if req.Items[i].Reload != nil {
				return fmt.Errorf("items[%d].reload is not valid for %s action", i, req.Action)
			}
		case "set_enabled":
			if req.Items[i].Reload != nil {
				return fmt.Errorf("items[%d].reload is not valid for set_enabled action", i)
			}
		case "reload":
			if req.Items[i].Enabled != nil {
				return fmt.Errorf("items[%d].enabled is not valid for reload action", i)
			}
		case "health":
			if req.Items[i].Enabled != nil {
				return fmt.Errorf("items[%d].enabled is not valid for health action", i)
			}
			if req.Items[i].Reload != nil {
				return fmt.Errorf("items[%d].reload is not valid for health action", i)
			}
		}
	}

	if req.Action == "set_enabled" && req.Enabled == nil && !hasPerItemEnabled {
		return fmt.Errorf("set_enabled action requires top-level enabled or per-item enabled values")
	}

	if req.Action != "set_enabled" && req.Enabled != nil {
		return fmt.Errorf("enabled is only valid for set_enabled action")
	}

	if req.Action != "reload" && req.DefaultReload != nil {
		return fmt.Errorf("default_reload is only valid for reload action")
	}

	if req.Action == "health" && boolValueOrDefault(req.RollbackOnError, false) {
		return fmt.Errorf("rollback_on_error is not valid for health action")
	}

	return nil
}

func resolveBulkEnabled(itemValue, defaultValue *bool) (bool, error) {
	if itemValue != nil {
		return *itemValue, nil
	}
	if defaultValue != nil {
		return *defaultValue, nil
	}
	return false, fmt.Errorf("enabled is required for set_enabled action")
}

func mergePluginReloadRequests(base, override pluginReloadRequest) pluginReloadRequest {
	out := clonePluginReloadRequest(base)

	if override.Enabled != nil {
		out.Enabled = override.Enabled
	}
	if override.Priority != nil {
		out.Priority = override.Priority
	}
	if len(override.Timeout) > 0 {
		out.Timeout = cloneRawMessage(override.Timeout)
	}
	if override.FailOpen != nil {
		out.FailOpen = override.FailOpen
	}
	if override.MaxFailures != nil {
		out.MaxFailures = override.MaxFailures
	}
	if len(override.Cooldown) > 0 {
		out.Cooldown = cloneRawMessage(override.Cooldown)
	}
	if override.ReplaceSettings {
		out.ReplaceSettings = true
		if override.Settings != nil {
			out.Settings = cloneStringAnyMap(override.Settings)
		}
	} else if override.Settings != nil {
		if out.Settings == nil {
			out.Settings = cloneStringAnyMap(override.Settings)
		} else {
			for key, value := range override.Settings {
				out.Settings[key] = value
			}
		}
	}

	return out
}

func clonePluginReloadRequest(in pluginReloadRequest) pluginReloadRequest {
	out := in
	out.Timeout = cloneRawMessage(in.Timeout)
	out.Cooldown = cloneRawMessage(in.Cooldown)
	if in.Settings != nil {
		out.Settings = cloneStringAnyMap(in.Settings)
	}
	return out
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}

func boolValueOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func clonePluginRuntimeConfig(cfg plugin.RuntimeConfig) plugin.RuntimeConfig {
	out := cfg
	if cfg.Settings != nil {
		out.Settings = cloneStringAnyMap(cfg.Settings)
	}
	return out
}

func isMutatingPluginBulkAction(action string) bool {
	switch action {
	case "enable", "disable", "set_enabled", "reload":
		return true
	default:
		return false
	}
}

func (g *Gateway) rollbackPluginBulkChanges(
	r *http.Request,
	action string,
	entries []pluginBulkRollbackEntry,
	results []map[string]interface{},
	refreshHealth, includeRuntime, includeStats bool,
) map[string]interface{} {
	rollbackResults := make([]map[string]interface{}, 0, len(entries))
	attempted := 0
	succeeded := 0
	failed := 0

	for idx := len(entries) - 1; idx >= 0; idx-- {
		entry := entries[idx]
		attempted++

		var rollbackErr error
		if action == "reload" {
			rollbackErr = g.pluginManager.Reload(r.Context(), entry.Name, clonePluginRuntimeConfig(entry.PreviousConfig))
		} else {
			rollbackErr = g.pluginManager.SetEnabled(r.Context(), entry.Name, entry.PreviousConfig.Enabled)
		}

		rollbackRecord := map[string]interface{}{
			"name": entry.Name,
		}

		if rollbackErr != nil {
			failed++
			rollbackRecord["success"] = false
			rollbackRecord["error"] = rollbackErr.Error()
		} else {
			succeeded++
			rollbackRecord["success"] = true
			if detail, err := g.pluginDetailPayload(r, entry.Name, refreshHealth, includeRuntime, includeStats); err == nil {
				rollbackRecord["plugin"] = detail
			}
		}

		if entry.ResultIndex >= 0 && entry.ResultIndex < len(results) {
			results[entry.ResultIndex]["rolled_back"] = rollbackErr == nil
			if rollbackErr != nil {
				results[entry.ResultIndex]["rollback_error"] = rollbackErr.Error()
			}
		}

		g.auditPluginAdminAction(r, "rollback."+action, entry.Name, rollbackErr == nil, rollbackErr, map[string]interface{}{
			"bulk": true,
		})

		rollbackResults = append(rollbackResults, rollbackRecord)
	}

	rollbackSummary := map[string]interface{}{
		"performed": true,
		"action":    action,
		"attempted": attempted,
		"succeeded": succeeded,
		"failed":    failed,
		"results":   rollbackResults,
	}

	var rollbackSummaryErr error
	if failed > 0 {
		rollbackSummaryErr = fmt.Errorf("rollback for bulk %s completed with %d failures", action, failed)
	}

	g.auditPluginAdminAction(r, "rollback_summary."+action, "bulk", failed == 0, rollbackSummaryErr, map[string]interface{}{
		"bulk":      true,
		"action":    action,
		"attempted": attempted,
		"succeeded": succeeded,
		"failed":    failed,
	})

	return rollbackSummary
}

func pluginAdminAuditRecordContains(record pluginAdminAuditRecord, rawNeedle string) bool {
	needle := strings.ToLower(strings.TrimSpace(rawNeedle))
	if needle == "" {
		return true
	}

	candidates := []string{
		record.ID,
		record.Action,
		record.Target,
		record.UserID,
		record.Method,
		record.Path,
		record.RemoteAddr,
		record.Error,
	}

	for _, candidate := range candidates {
		if strings.Contains(strings.ToLower(candidate), needle) {
			return true
		}
	}

	for key, value := range record.Metadata {
		serialized := fmt.Sprintf("%s=%v", key, value)
		if strings.Contains(strings.ToLower(serialized), needle) {
			return true
		}
	}

	return false
}

func (g *Gateway) pluginAdminAuditSnapshot() []pluginAdminAuditRecord {
	g.pluginAuditMu.RLock()
	defer g.pluginAuditMu.RUnlock()

	out := make([]pluginAdminAuditRecord, len(g.pluginAuditTrail))
	for i, record := range g.pluginAuditTrail {
		out[i] = record
		if record.Metadata != nil {
			out[i].Metadata = cloneStringAnyMap(record.Metadata)
		}
	}

	return out
}

func (g *Gateway) nextPluginAdminAuditID() string {
	seq := atomic.AddUint64(&g.pluginAuditCounter, 1)
	return fmt.Sprintf("pa-%d-%06d", time.Now().UTC().UnixNano(), seq)
}

func (g *Gateway) appendPluginAdminAuditRecord(record pluginAdminAuditRecord) {
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	if strings.TrimSpace(record.ID) == "" {
		record.ID = g.nextPluginAdminAuditID()
	}
	if record.Metadata != nil {
		record.Metadata = cloneStringAnyMap(record.Metadata)
	}

	g.pluginAuditMu.Lock()
	if g.pluginAuditLimit <= 0 {
		g.pluginAuditLimit = defaultPluginAdminAuditMemoryLimit
	}

	if len(g.pluginAuditTrail) >= g.pluginAuditLimit {
		copy(g.pluginAuditTrail, g.pluginAuditTrail[1:])
		g.pluginAuditTrail[len(g.pluginAuditTrail)-1] = record
	} else {
		g.pluginAuditTrail = append(g.pluginAuditTrail, record)
	}
	g.pluginAuditMu.Unlock()

	g.appendPluginAdminAuditRecordToPersistent(record)
}

func (g *Gateway) handlePluginOperation(w http.ResponseWriter, r *http.Request) {
	if !g.ensurePluginManager(w) {
		return
	}

	relative := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/plugins/"), "/")
	if relative == "" {
		g.handlePlugins(w, r)
		return
	}

	parts := strings.Split(relative, "/")
	if len(parts) == 0 {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Plugin name is required",
		})
		return
	}

	name := strings.TrimSpace(parts[0])
	if name == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Plugin name is required",
		})
		return
	}

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
				"success": false,
				"error":   "Method not allowed",
			})
			return
		}

		refreshHealth, err := parseOptionalBoolQuery(r, "refresh_health", true)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		includeRuntime, err := parseOptionalBoolQuery(r, "include_runtime", true)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		includeStats, err := parseOptionalBoolQuery(r, "include_stats", true)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		detail, err := g.pluginDetailPayload(r, name, refreshHealth, includeRuntime, includeStats)
		if err != nil {
			g.respondPluginOperationError(w, r, name, "detail", err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"plugin":  detail,
		})
		return
	}

	if len(parts) != 2 {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   "Endpoint not found",
		})
		return
	}

	action := strings.ToLower(strings.TrimSpace(parts[1]))
	switch action {
	case "health":
		if r.Method != http.MethodGet {
			respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "Method not allowed"})
			return
		}

		health := g.pluginManager.HealthCheck(r.Context())
		pluginHealth, ok := health[name]
		if !ok {
			g.respondPluginOperationError(w, r, name, "health", fmt.Errorf("%w: %s", plugin.ErrPluginNotFound, name))
			return
		}

		g.auditPluginAdminAction(r, "health", name, true, nil, nil)

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"plugin":  name,
			"health":  pluginHealth,
		})

	case "reload":
		if r.Method != http.MethodPost {
			respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "Method not allowed"})
			return
		}

		var req pluginReloadRequest
		if err := decodeJSONBodyStrict(r, &req); err != nil {
			g.auditPluginAdminAction(r, "reload", name, false, err, nil)
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("Invalid request body: %v", err),
			})
			return
		}

		currentCfg, ok := g.pluginManager.RuntimeConfig(name)
		if !ok {
			g.respondPluginOperationError(w, r, name, "reload", fmt.Errorf("%w: %s", plugin.ErrPluginNotFound, name))
			return
		}

		nextCfg, err := mergePluginRuntimeConfig(currentCfg, req)
		if err != nil {
			g.auditPluginAdminAction(r, "reload", name, false, err, nil)
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		if err := g.pluginManager.Reload(r.Context(), name, nextCfg); err != nil {
			g.respondPluginOperationError(w, r, name, "reload", err)
			return
		}

		detail, err := g.pluginDetailPayload(r, name, true, true, true)
		if err != nil {
			g.respondPluginOperationError(w, r, name, "reload", err)
			return
		}

		g.auditPluginAdminAction(r, "reload", name, true, nil, nil)

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "Plugin reloaded successfully",
			"plugin":  detail,
		})

	case "enable":
		if r.Method != http.MethodPost {
			respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "Method not allowed"})
			return
		}
		g.setPluginEnabled(w, r, name, true, "enable")

	case "disable":
		if r.Method != http.MethodPost {
			respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "Method not allowed"})
			return
		}
		g.setPluginEnabled(w, r, name, false, "disable")

	case "enabled":
		if r.Method != http.MethodPut {
			respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "Method not allowed"})
			return
		}

		var req pluginSetEnabledRequest
		if err := decodeJSONBodyStrict(r, &req); err != nil {
			g.auditPluginAdminAction(r, "set_enabled", name, false, err, nil)
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("Invalid request body: %v", err),
			})
			return
		}
		if req.Enabled == nil {
			err := fmt.Errorf("enabled is required")
			g.auditPluginAdminAction(r, "set_enabled", name, false, err, nil)
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		g.setPluginEnabled(w, r, name, *req.Enabled, "set_enabled")

	default:
		err := fmt.Errorf("unknown plugin action: %s", action)
		g.auditPluginAdminAction(r, action, name, false, err, nil)
		respondJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
	}
}

func (g *Gateway) setPluginEnabled(w http.ResponseWriter, r *http.Request, name string, enabled bool, action string) {
	if err := g.pluginManager.SetEnabled(r.Context(), name, enabled); err != nil {
		g.respondPluginOperationError(w, r, name, action, err)
		return
	}

	detail, err := g.pluginDetailPayload(r, name, true, true, true)
	if err != nil {
		g.respondPluginOperationError(w, r, name, action, err)
		return
	}

	state := "disabled"
	if enabled {
		state = "enabled"
	}

	g.auditPluginAdminAction(r, action, name, true, nil, map[string]interface{}{"enabled": enabled})

	message := fmt.Sprintf("Plugin %s %s successfully", name, state)
	if action == "set_enabled" {
		message = fmt.Sprintf("Plugin %s set to %s successfully", name, state)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": message,
		"plugin":  detail,
	})
}

func (g *Gateway) pluginDetailPayload(r *http.Request, name string, refreshHealth, includeRuntime, includeStats bool) (map[string]interface{}, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("plugin name is required")
	}

	cfg, ok := g.pluginManager.RuntimeConfig(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", plugin.ErrPluginNotFound, name)
	}

	stats := g.pluginManager.Stats()
	var pluginStat plugin.PluginStats
	statFound := false
	for _, st := range stats.Plugins {
		if st.Name == name {
			pluginStat = st
			statFound = true
			break
		}
	}

	detail := map[string]interface{}{
		"name": name,
	}

	if includeRuntime {
		detail["runtime"] = runtimeConfigToJSON(cfg)
	}

	if includeStats && statFound {
		detail["state"] = pluginStatsToStateJSON(pluginStat)
	}

	if refreshHealth {
		healthMap := g.pluginManager.HealthCheck(r.Context())
		if health, exists := healthMap[name]; exists {
			detail["health"] = health
		}
	} else if includeStats && statFound {
		detail["health"] = pluginStat.Health
	}

	return detail, nil
}

func (g *Gateway) respondPluginOperationError(w http.ResponseWriter, r *http.Request, name, action string, err error) {
	statusCode, message := classifyPluginOperationError(name, err)
	g.auditPluginAdminAction(r, action, name, false, err, nil)

	respondJSON(w, statusCode, map[string]interface{}{
		"success": false,
		"error":   message,
	})
}

func classifyPluginOperationError(name string, err error) (int, string) {
	if errors.Is(err, plugin.ErrPluginNotFound) {
		return http.StatusNotFound, fmt.Sprintf("plugin not found: %s", name)
	}

	if errors.Is(err, plugin.ErrPluginNotReloadable) {
		return http.StatusConflict, fmt.Sprintf("plugin does not support reload: %s", name)
	}

	if err == nil {
		return http.StatusBadRequest, "unknown plugin operation error"
	}

	return http.StatusBadRequest, err.Error()
}

func (g *Gateway) auditPluginAdminAction(r *http.Request, action, target string, success bool, operationErr error, extra map[string]interface{}) {
	if r == nil {
		return
	}

	action = strings.TrimSpace(action)
	target = strings.TrimSpace(target)

	fields := map[string]interface{}{
		"action":      action,
		"target":      target,
		"success":     success,
		"method":      r.Method,
		"path":        r.URL.Path,
		"remote_addr": r.RemoteAddr,
	}

	userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
	if userID == "" {
		userID = "unknown"
	}
	fields["user_id"] = userID

	if operationErr != nil {
		fields["error"] = operationErr.Error()
	}

	for key, value := range extra {
		fields[key] = value
	}

	g.appendPluginAdminAuditRecord(pluginAdminAuditRecord{
		Action:     action,
		Target:     target,
		Success:    success,
		UserID:     userID,
		Method:     r.Method,
		Path:       r.URL.Path,
		RemoteAddr: r.RemoteAddr,
		Error: func() string {
			if operationErr != nil {
				return operationErr.Error()
			}
			return ""
		}(),
		Metadata: cloneStringAnyMap(extra),
	})

	if g.logger != nil {
		if success {
			g.logger.Info("Plugin admin action completed", fields)
		} else {
			g.logger.Warn("Plugin admin action failed", fields)
		}
	}

	if g.pluginManager != nil {
		g.pluginManager.Publish(plugin.NewEvent("admin.plugin.action", "gateway", cloneStringAnyMap(fields)))
		g.pluginManager.Publish(plugin.NewEvent("admin.plugin."+sanitizePluginAdminAction(action), "gateway", cloneStringAnyMap(fields)))
	}
}

func sanitizePluginAdminAction(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return "unknown"
	}

	action = strings.NewReplacer(
		" ", "_",
		"/", "_",
		".", "_",
		"-", "_",
		":", "_",
	).Replace(action)

	for strings.Contains(action, "__") {
		action = strings.ReplaceAll(action, "__", "_")
	}
	action = strings.Trim(action, "_")
	if action == "" {
		return "unknown"
	}

	return action
}

func (g *Gateway) ensurePluginManager(w http.ResponseWriter) bool {
	if g.pluginManager != nil {
		return true
	}

	respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
		"success": false,
		"error":   "Plugin manager not initialized",
	})
	return false
}

func parseOptionalBoolQuery(r *http.Request, key string, defaultValue bool) (bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return defaultValue, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("query parameter %q must be a boolean", key)
	}

	return value, nil
}

func parseOptionalBoolQueryPresence(r *http.Request, key string) (bool, bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return false, false, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, true, fmt.Errorf("query parameter %q must be a boolean", key)
	}

	return value, true, nil
}

func parseOptionalIntQuery(r *http.Request, key string, defaultValue, minValue, maxValue int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return defaultValue, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("query parameter %q must be an integer", key)
	}

	if value < minValue || value > maxValue {
		return 0, fmt.Errorf("query parameter %q must be between %d and %d", key, minValue, maxValue)
	}

	return value, nil
}

func parseOptionalTimeQuery(r *http.Request, key string) (time.Time, bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return time.Time{}, false, nil
	}

	value, err := time.Parse(time.RFC3339Nano, raw)
	if err == nil {
		return value, true, nil
	}

	value, err = time.Parse(time.RFC3339, raw)
	if err == nil {
		return value, true, nil
	}

	return time.Time{}, true, fmt.Errorf("query parameter %q must be RFC3339/RFC3339Nano timestamp", key)
}

func decodeJSONBodyStrict(r *http.Request, dst interface{}) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return err
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request body must contain a single JSON object")
	}

	return nil
}

func runtimeConfigToJSON(cfg plugin.RuntimeConfig) map[string]interface{} {
	settings := map[string]interface{}{}
	for key, value := range cfg.Settings {
		settings[key] = value
	}

	return map[string]interface{}{
		"enabled":      cfg.Enabled,
		"priority":     cfg.Priority,
		"timeout":      cfg.Timeout.String(),
		"timeout_ns":   cfg.Timeout.Nanoseconds(),
		"fail_open":    cfg.FailOpen,
		"max_failures": cfg.MaxFailures,
		"cooldown":     cfg.Cooldown.String(),
		"cooldown_ns":  cfg.Cooldown.Nanoseconds(),
		"settings":     settings,
	}
}

func pluginStatsToStateJSON(st plugin.PluginStats) map[string]interface{} {
	state := map[string]interface{}{
		"enabled":           st.Enabled,
		"started":           st.Started,
		"priority":          st.Priority,
		"consecutive_fails": st.ConsecutiveFails,
		"circuit_open":      st.CircuitOpen,
		"total_calls":       st.TotalCalls,
		"total_errors":      st.TotalErrors,
		"total_panics":      st.TotalPanics,
		"total_duration":    st.TotalDuration.String(),
		"total_duration_ms": st.TotalDuration.Milliseconds(),
		"last_error":        st.LastError,
	}

	if !st.CircuitOpenUntil.IsZero() {
		state["circuit_open_until"] = st.CircuitOpenUntil.Format(time.RFC3339Nano)
	}

	return state
}

func mergePluginRuntimeConfig(current plugin.RuntimeConfig, req pluginReloadRequest) (plugin.RuntimeConfig, error) {
	next := current
	if current.Settings != nil {
		next.Settings = cloneStringAnyMap(current.Settings)
	}

	if req.Enabled != nil {
		next.Enabled = *req.Enabled
	}
	if req.Priority != nil {
		next.Priority = *req.Priority
	}
	if req.FailOpen != nil {
		next.FailOpen = *req.FailOpen
	}
	if req.MaxFailures != nil {
		next.MaxFailures = *req.MaxFailures
	}

	if duration, hasValue, err := parseDurationField(req.Timeout, "timeout"); err != nil {
		return plugin.RuntimeConfig{}, err
	} else if hasValue {
		next.Timeout = duration
	}

	if duration, hasValue, err := parseDurationField(req.Cooldown, "cooldown"); err != nil {
		return plugin.RuntimeConfig{}, err
	} else if hasValue {
		next.Cooldown = duration
	}

	if req.Settings != nil {
		if req.ReplaceSettings {
			next.Settings = cloneStringAnyMap(req.Settings)
		} else {
			if next.Settings == nil {
				next.Settings = map[string]interface{}{}
			}
			for key, value := range req.Settings {
				next.Settings[key] = value
			}
		}
	}

	return next, nil
}

func parseDurationField(raw json.RawMessage, fieldName string) (time.Duration, bool, error) {
	if len(raw) == 0 {
		return 0, false, nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		asString = strings.TrimSpace(asString)
		if asString == "" {
			return 0, false, fmt.Errorf("%s cannot be empty", fieldName)
		}

		duration, err := time.ParseDuration(asString)
		if err != nil {
			return 0, false, fmt.Errorf("%s must be a valid duration string", fieldName)
		}
		if duration < 0 {
			return 0, false, fmt.Errorf("%s must be non-negative", fieldName)
		}

		return duration, true, nil
	}

	var asNumber float64
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		if asNumber < 0 {
			return 0, false, fmt.Errorf("%s must be non-negative", fieldName)
		}
		return time.Duration(asNumber), true, nil
	}

	return 0, false, fmt.Errorf("%s must be a duration string (e.g., \"250ms\") or a numeric nanosecond value", fieldName)
}

func cloneStringAnyMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
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

	var handler http.Handler = mux
	if gateway.pluginManager != nil {
		handler = gateway.pluginManager.WrapHTTP(handler)
	}

	return &Server{
		gateway: gateway,
		server: &http.Server{
			Addr:    address,
			Handler: handler,
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
	if s.gateway != nil {
		s.gateway.Stop()
	}
	return s.server.Close()
}
