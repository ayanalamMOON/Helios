package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/helios/helios/internal/auth"
	"github.com/helios/helios/internal/auth/rbac"
	"github.com/helios/helios/internal/queue"
	"github.com/helios/helios/internal/rate"
)

// Gateway is the API gateway
type Gateway struct {
	authService *auth.Service
	rbacService *rbac.Service
	rateLimiter *rate.Limiter
	queue       *queue.Queue
	rateConfig  *rate.Config
}

// NewGateway creates a new API gateway
func NewGateway(
	authService *auth.Service,
	rbacService *rbac.Service,
	rateLimiter *rate.Limiter,
	queue *queue.Queue,
	rateConfig *rate.Config,
) *Gateway {
	return &Gateway{
		authService: authService,
		rbacService: rbacService,
		rateLimiter: rateLimiter,
		queue:       queue,
		rateConfig:  rateConfig,
	}
}

// RegisterRoutes registers all API routes
func (g *Gateway) RegisterRoutes(mux *http.ServeMux) {
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
	// Implementation would interact with ATLAS
	respondJSON(w, http.StatusOK, map[string]interface{}{"message": "GET KV"})
}

func (g *Gateway) handleKVSet(w http.ResponseWriter, r *http.Request) {
	// Implementation would interact with ATLAS
	respondJSON(w, http.StatusOK, map[string]interface{}{"message": "SET KV"})
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
