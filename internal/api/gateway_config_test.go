package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/helios/helios/internal/atlas"
	"github.com/helios/helios/internal/auth"
	"github.com/helios/helios/internal/auth/rbac"
	"github.com/helios/helios/internal/config"
	"github.com/helios/helios/internal/queue"
	"github.com/helios/helios/internal/rate"
)

func TestConfigEndpoints(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
  listen_addr: ":8443"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: false

observability:
  log_level: "INFO"
  metrics_enabled: true
  metrics_port: ":9090"

rate_limiting:
  enabled: true
  requests_per_second: 1000
  burst_size: 100

timeouts:
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 120s
  shutdown_timeout: 30s

performance:
  max_connections: 10000
  worker_pool_size: 100
  queue_size: 10000
  snapshot_interval: 5m
  aof_sync_mode: "every"
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Create config manager
	configManager, err := config.NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// Setup gateway with services
	atlasInstance, err := atlas.New(&atlas.Config{
		DataDir:     t.TempDir(),
		AOFSyncMode: 0,
	})
	if err != nil {
		t.Fatalf("Failed to create Atlas: %v", err)
	}
	defer atlasInstance.Close()

	authService := auth.NewService(atlasInstance)
	rbacService := rbac.NewService(atlasInstance)
	rateLimiter := rate.NewLimiter(atlasInstance)
	queueInstance := queue.NewQueue(atlasInstance, queue.DefaultConfig())

	if err := rbacService.InitializeDefaultRoles(); err != nil {
		t.Fatalf("Failed to initialize RBAC: %v", err)
	}

	gateway := NewGateway(
		authService,
		rbacService,
		rateLimiter,
		queueInstance,
		rate.DefaultConfig(),
	)
	gateway.SetConfigManager(configManager)

	// Create admin user and token
	user, err := authService.CreateUser("admin", "password123")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	if err := rbacService.AssignRole(user.ID, "admin"); err != nil {
		t.Fatalf("Failed to assign admin role: %v", err)
	}
	token, err := authService.CreateToken(user.ID, 3600*1000000000)
	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	// Test GET /admin/config (JSON)
	t.Run("GetConfig_JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleConfig(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		var cfg config.Config
		if err := json.NewDecoder(rec.Body).Decode(&cfg); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if cfg.Observability.LogLevel != "INFO" {
			t.Errorf("Expected log level INFO, got %s", cfg.Observability.LogLevel)
		}
		if cfg.RateLimiting.RequestsPerSecond != 1000 {
			t.Errorf("Expected RPS 1000, got %d", cfg.RateLimiting.RequestsPerSecond)
		}
	})

	// Test GET /admin/config (YAML)
	t.Run("GetConfig_YAML", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config?format=yaml", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleConfig(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		contentType := rec.Header().Get("Content-Type")
		if contentType != "application/x-yaml" {
			t.Errorf("Expected Content-Type application/x-yaml, got %s", contentType)
		}

		body := rec.Body.String()
		if body == "" {
			t.Error("Expected non-empty YAML response")
		}
	})

	// Test POST /admin/config/reload (successful)
	t.Run("ReloadConfig_Success", func(t *testing.T) {
		// Update config file
		updatedConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
  listen_addr: ":8443"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: false

observability:
  log_level: "DEBUG"
  metrics_enabled: false
  metrics_port: ":9090"

rate_limiting:
  enabled: true
  requests_per_second: 2000
  burst_size: 200

timeouts:
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 120s
  shutdown_timeout: 30s

performance:
  max_connections: 10000
  worker_pool_size: 100
  queue_size: 10000
  snapshot_interval: 5m
  aof_sync_mode: "every"
`

		if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
			t.Fatalf("Failed to write updated config: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/admin/config/reload", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleConfigReload(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if !resp["success"].(bool) {
			t.Error("Expected success to be true")
		}
		if resp["reload_count"].(float64) != 1 {
			t.Errorf("Expected reload_count 1, got %v", resp["reload_count"])
		}

		// Verify config was actually updated
		cfg := configManager.GetConfig()
		if cfg.Observability.LogLevel != "DEBUG" {
			t.Errorf("Expected log level DEBUG after reload, got %s", cfg.Observability.LogLevel)
		}
		if cfg.RateLimiting.RequestsPerSecond != 2000 {
			t.Errorf("Expected RPS 2000 after reload, got %d", cfg.RateLimiting.RequestsPerSecond)
		}
	})

	// Test POST /admin/config/reload (immutable field change)
	t.Run("ReloadConfig_ImmutableChange", func(t *testing.T) {
		// Try to change node_id
		badConfig := `
immutable:
  node_id: "different-node"
  data_dir: "/data/test"
  listen_addr: ":8443"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: false

observability:
  log_level: "INFO"
`

		if err := os.WriteFile(configPath, []byte(badConfig), 0644); err != nil {
			t.Fatalf("Failed to write bad config: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/admin/config/reload", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleConfigReload(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", rec.Code)
		}

		var resp map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if resp["success"].(bool) {
			t.Error("Expected success to be false")
		}
		if resp["error"] == nil {
			t.Error("Expected error message")
		}
	})

	// Test without authentication
	t.Run("Unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)

		rec := httptest.NewRecorder()
		gateway.requireAuth(gateway.handleConfig)(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rec.Code)
		}
	})
}

func TestConfigReloadListener(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
observability:
  log_level: "INFO"
rate_limiting:
  requests_per_second: 1000
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	configManager, err := config.NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// Add listener that tracks changes
	var listenerCalled bool
	var oldLevel, newLevel string
	var oldRPS, newRPS int

	configManager.AddReloadListener(func(old, new *config.Config) error {
		listenerCalled = true
		oldLevel = old.Observability.LogLevel
		newLevel = new.Observability.LogLevel
		oldRPS = old.RateLimiting.RequestsPerSecond
		newRPS = new.RateLimiting.RequestsPerSecond
		return nil
	})

	// Update and reload
	updatedConfig := `
immutable:
  node_id: "test-node"
observability:
  log_level: "DEBUG"
rate_limiting:
  requests_per_second: 2000
`

	if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
		t.Fatalf("Failed to write updated config: %v", err)
	}

	if err := configManager.Reload(); err != nil {
		t.Fatalf("Failed to reload: %v", err)
	}

	if !listenerCalled {
		t.Error("Listener was not called")
	}
	if oldLevel != "INFO" || newLevel != "DEBUG" {
		t.Errorf("Log level change not detected: %s -> %s", oldLevel, newLevel)
	}
	if oldRPS != 1000 || newRPS != 2000 {
		t.Errorf("RPS change not detected: %d -> %d", oldRPS, newRPS)
	}
}

func TestConfigDiffEndpoint(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
  listen_addr: ":8443"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: false

observability:
  log_level: "INFO"
  metrics_enabled: true
  metrics_port: ":9090"

rate_limiting:
  enabled: true
  requests_per_second: 1000
  burst_size: 100

timeouts:
  read_timeout: 30s
  write_timeout: 30s

performance:
  max_connections: 10000
  worker_pool_size: 100
  aof_sync_mode: "every"
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	configManager, err := config.NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// Setup gateway
	atlasInstance, err := atlas.New(&atlas.Config{
		DataDir:     t.TempDir(),
		AOFSyncMode: 0,
	})
	if err != nil {
		t.Fatalf("Failed to create Atlas: %v", err)
	}
	defer atlasInstance.Close()

	authService := auth.NewService(atlasInstance)
	rbacService := rbac.NewService(atlasInstance)
	rateLimiter := rate.NewLimiter(atlasInstance)
	queueInstance := queue.NewQueue(atlasInstance, queue.DefaultConfig())

	if err := rbacService.InitializeDefaultRoles(); err != nil {
		t.Fatalf("Failed to initialize RBAC: %v", err)
	}

	gateway := NewGateway(
		authService,
		rbacService,
		rateLimiter,
		queueInstance,
		rate.DefaultConfig(),
	)
	gateway.SetConfigManager(configManager)

	// Create admin user
	user, err := authService.CreateUser("admin", "password123")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	if err := rbacService.AssignRole(user.ID, "admin"); err != nil {
		t.Fatalf("Failed to assign admin role: %v", err)
	}
	token, err := authService.CreateToken(user.ID, 3600*1000000000)
	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	// Test diff with no changes
	t.Run("DiffNoChanges", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/diff", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleConfigDiff(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var diff config.ConfigDiff
		if err := json.NewDecoder(rec.Body).Decode(&diff); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if diff.HasChanges {
			t.Error("Expected no changes")
		}

		if diff.Summary != "No changes detected" {
			t.Errorf("Expected 'No changes detected', got '%s'", diff.Summary)
		}
	})

	// Test diff with changes
	t.Run("DiffWithChanges", func(t *testing.T) {
		updatedConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
  listen_addr: ":8443"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: false

observability:
  log_level: "DEBUG"
  metrics_enabled: false
  metrics_port: ":9091"

rate_limiting:
  enabled: true
  requests_per_second: 2000
  burst_size: 200

timeouts:
  read_timeout: 60s
  write_timeout: 45s

performance:
  max_connections: 20000
  worker_pool_size: 200
  aof_sync_mode: "always"
`

		if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
			t.Fatalf("Failed to write updated config: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/admin/config/diff", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleConfigDiff(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var diff config.ConfigDiff
		if err := json.NewDecoder(rec.Body).Decode(&diff); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if !diff.HasChanges {
			t.Error("Expected changes to be detected")
		}

		// Verify observability changes
		if obsChanges, ok := diff.Changes["observability"]; ok {
			if len(obsChanges) != 3 {
				t.Errorf("Expected 3 observability changes, got %d", len(obsChanges))
			}
		} else {
			t.Error("Expected observability changes")
		}

		// Verify rate limiting changes
		if rateLimitChanges, ok := diff.Changes["rate_limiting"]; ok {
			if len(rateLimitChanges) != 2 {
				t.Errorf("Expected 2 rate_limiting changes, got %d", len(rateLimitChanges))
			}
		} else {
			t.Error("Expected rate_limiting changes")
		}

		// Verify no immutable changes
		if len(diff.ImmutableChanges) > 0 {
			t.Errorf("Expected no immutable changes, got %d", len(diff.ImmutableChanges))
		}

		// Verify no validation errors
		if len(diff.ValidationErrors) > 0 {
			t.Errorf("Expected no validation errors, got: %v", diff.ValidationErrors)
		}
	})

	// Test diff with immutable changes
	t.Run("DiffWithImmutableChanges", func(t *testing.T) {
		updatedConfig := `
immutable:
  node_id: "different-node"
  data_dir: "/different/path"
  listen_addr: ":9443"
  raft_addr: "127.0.0.1:8000"
  raft_enabled: true

observability:
  log_level: "INFO"
`

		if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
			t.Fatalf("Failed to write updated config: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/admin/config/diff", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleConfigDiff(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		var diff config.ConfigDiff
		if err := json.NewDecoder(rec.Body).Decode(&diff); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if !diff.HasChanges {
			t.Error("Expected changes to be detected")
		}

		// Should have immutable changes
		if len(diff.ImmutableChanges) != 5 {
			t.Errorf("Expected 5 immutable changes, got %d", len(diff.ImmutableChanges))
		}
	})

	// Test unauthorized access
	t.Run("Unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/diff", nil)

		rec := httptest.NewRecorder()
		gateway.requireAuth(gateway.handleConfigDiff)(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rec.Code)
		}
	})
}

func TestMigrationEndpoints(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
  listen_addr: ":8443"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: false

observability:
  log_level: "INFO"
  metrics_enabled: true
  metrics_port: ":9090"

rate_limiting:
  enabled: true
  requests_per_second: 1000
  burst_size: 100

timeouts:
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 120s
  shutdown_timeout: 30s

performance:
  max_connections: 10000
  worker_pool_size: 100
  queue_size: 10000
  snapshot_interval: 5m
  aof_sync_mode: "every"
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Create config manager
	configManager, err := config.NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// Setup gateway with services
	atlasInstance, err := atlas.New(&atlas.Config{
		DataDir:     t.TempDir(),
		AOFSyncMode: 0,
	})
	if err != nil {
		t.Fatalf("Failed to create Atlas: %v", err)
	}
	defer atlasInstance.Close()

	authService := auth.NewService(atlasInstance)
	rbacService := rbac.NewService(atlasInstance)
	rateLimiter := rate.NewLimiter(atlasInstance)
	queueInstance := queue.NewQueue(atlasInstance, queue.DefaultConfig())

	if err := rbacService.InitializeDefaultRoles(); err != nil {
		t.Fatalf("Failed to initialize RBAC: %v", err)
	}

	gateway := NewGateway(
		authService,
		rbacService,
		rateLimiter,
		queueInstance,
		rate.DefaultConfig(),
	)
	gateway.SetConfigManager(configManager)

	// Create admin user and token
	user, err := authService.CreateUser("admin", "password123")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	if err := rbacService.AssignRole(user.ID, "admin"); err != nil {
		t.Fatalf("Failed to assign admin role: %v", err)
	}
	token, err := authService.CreateToken(user.ID, 3600*1000000000)
	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	// Test GET /admin/config/migrations - List all migrations
	t.Run("ListMigrations", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/migrations", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleMigrations(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		var response map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		// Should have migrations array
		if _, ok := response["migrations"]; !ok {
			t.Error("Expected 'migrations' field in response")
		}

		// Should have total count
		if _, ok := response["total"]; !ok {
			t.Error("Expected 'total' field in response")
		}

		// Should have latest_version
		if _, ok := response["latest_version"]; !ok {
			t.Error("Expected 'latest_version' field in response")
		}
	})

	// Test GET /admin/config/migrations/status
	t.Run("MigrationStatus", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/migrations/status", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleMigrationStatus(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		var status config.MigrationStatus
		if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		// Config at version 0, should have pending migrations
		if status.CurrentVersion != 0 {
			t.Errorf("Expected current version 0, got %d", status.CurrentVersion)
		}

		// Should have latest version from built-in migrations
		if status.LatestVersion < 5 {
			t.Errorf("Expected latest version >= 5, got %d", status.LatestVersion)
		}

		// Should have pending migrations
		if !status.HasPending {
			t.Error("Expected has_pending=true for version 0 config")
		}
	})

	// Test POST /admin/config/migrations/apply?dry_run=true
	t.Run("ApplyMigrationsDryRun", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/config/migrations/apply?dry_run=true", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleMigrationApply(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var result config.MigrationResult
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if !result.DryRun {
			t.Error("Expected dry_run=true")
		}

		if !result.Success {
			t.Errorf("Expected success, got failure: %v", result.Errors)
		}

		// Should have applied some migrations (in dry run)
		if result.AppliedCount == 0 {
			t.Error("Expected some migrations to be applied (dry run)")
		}

		// Version should still be 0 (dry run)
		currentVersion, err := config.GetCurrentVersion(configPath)
		if err != nil {
			t.Fatalf("GetCurrentVersion failed: %v", err)
		}
		if currentVersion != 0 {
			t.Errorf("Expected version still 0 after dry run, got %d", currentVersion)
		}
	})

	// Test GET /admin/config/backups
	t.Run("ListBackups", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/backups", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleBackups(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		var response map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		// Should have backups array
		if _, ok := response["backups"]; !ok {
			t.Error("Expected 'backups' field in response")
		}

		// Should have count
		if _, ok := response["count"]; !ok {
			t.Error("Expected 'count' field in response")
		}
	})

	// Test method not allowed
	t.Run("MethodNotAllowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/admin/config/migrations", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)

		rec := httptest.NewRecorder()
		gateway.handleMigrations(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("Expected status 405, got %d", rec.Code)
		}
	})

	// Test unauthorized
	t.Run("Unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/migrations/status", nil)

		rec := httptest.NewRecorder()
		gateway.requireAuth(gateway.handleMigrationStatus)(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rec.Code)
		}
	})
}

func TestHistoryEndpoints(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
  listen_addr: ":8443"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: false

observability:
  log_level: "INFO"
  metrics_enabled: true
  metrics_port: ":9090"

rate_limiting:
  enabled: true
  requests_per_second: 1000
  burst_size: 100

timeouts:
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 120s
  shutdown_timeout: 30s

performance:
  max_connections: 10000
  worker_pool_size: 100
  queue_size: 10000
  snapshot_interval: 5m
  aof_sync_mode: "every"
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Create config manager
	configManager, err := config.NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// Setup gateway with services
	atlasInstance, err := atlas.New(&atlas.Config{
		DataDir:     t.TempDir(),
		AOFSyncMode: 0,
	})
	if err != nil {
		t.Fatalf("Failed to create Atlas: %v", err)
	}
	defer atlasInstance.Close()

	authService := auth.NewService(atlasInstance)
	rbacService := rbac.NewService(atlasInstance)
	rateLimiter := rate.NewLimiter(atlasInstance)
	queueInstance := queue.NewQueue(atlasInstance, queue.DefaultConfig())

	if err := rbacService.InitializeDefaultRoles(); err != nil {
		t.Fatalf("Failed to initialize RBAC: %v", err)
	}

	gateway := NewGateway(
		authService,
		rbacService,
		rateLimiter,
		queueInstance,
		rate.DefaultConfig(),
	)
	gateway.SetConfigManager(configManager)

	// Create admin user and token
	user, err := authService.CreateUser("admin", "password123")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	if err := rbacService.AssignRole(user.ID, "admin"); err != nil {
		t.Fatalf("Failed to assign admin role: %v", err)
	}
	token, err := authService.CreateToken(user.ID, 3600*1000000000)
	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	// Test GET /admin/config/history
	t.Run("GetHistory", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/history", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)
		req.Header.Set("X-User-ID", user.ID)

		rec := httptest.NewRecorder()
		gateway.handleHistory(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response config.HistoryList
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		// Should have at least the initial entry
		if response.Total < 1 {
			t.Error("Expected at least 1 history entry")
		}
	})

	// Test GET /admin/config/history with specific version
	t.Run("GetHistoryEntry", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/history?version=1", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)
		req.Header.Set("X-User-ID", user.ID)

		rec := httptest.NewRecorder()
		gateway.handleHistory(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response config.HistoryEntry
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if response.Version != 1 {
			t.Errorf("Expected version 1, got %d", response.Version)
		}
	})

	// Test GET /admin/config/history/stats
	t.Run("GetHistoryStats", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/history/stats", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)
		req.Header.Set("X-User-ID", user.ID)

		rec := httptest.NewRecorder()
		gateway.handleHistoryStats(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response config.HistoryStats
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if response.TotalEntries < 1 {
			t.Error("Expected at least 1 entry in stats")
		}
	})

	// Create a second entry for comparison
	updatedConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
  listen_addr: ":8443"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: false

observability:
  log_level: "DEBUG"
  metrics_enabled: false
  metrics_port: ":9090"

rate_limiting:
  enabled: true
  requests_per_second: 2000
  burst_size: 200

timeouts:
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 120s
  shutdown_timeout: 30s

performance:
  max_connections: 10000
  worker_pool_size: 100
  queue_size: 10000
  snapshot_interval: 5m
  aof_sync_mode: "every"
`

	if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
		t.Fatalf("Failed to write updated config: %v", err)
	}

	// Reload to create version 2
	if err := configManager.Reload(); err != nil {
		t.Fatalf("Failed to reload config: %v", err)
	}

	// Test GET /admin/config/history/compare
	t.Run("CompareHistoryVersions", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/history/compare?from=1&to=2", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)
		req.Header.Set("X-User-ID", user.ID)

		rec := httptest.NewRecorder()
		gateway.handleHistoryCompare(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response config.HistoryComparison
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if response.VersionA != 1 || response.VersionB != 2 {
			t.Errorf("Expected versions 1 and 2, got %d and %d", response.VersionA, response.VersionB)
		}
		if response.TotalChanges == 0 {
			t.Error("Expected changes to be detected between versions")
		}
	})

	// Test comparison without required params
	t.Run("CompareHistoryVersions_MissingParams", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/history/compare?from=1", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)
		req.Header.Set("X-User-ID", user.ID)

		rec := httptest.NewRecorder()
		gateway.handleHistoryCompare(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", rec.Code)
		}
	})

	// Test method not allowed on stats
	t.Run("HistoryStats_MethodNotAllowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/config/history/stats", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)
		req.Header.Set("X-User-ID", user.ID)

		rec := httptest.NewRecorder()
		gateway.handleHistoryStats(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("Expected status 405, got %d", rec.Code)
		}
	})

	// Test method not allowed on compare
	t.Run("HistoryCompare_MethodNotAllowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/config/history/compare", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)
		req.Header.Set("X-User-ID", user.ID)

		rec := httptest.NewRecorder()
		gateway.handleHistoryCompare(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("Expected status 405, got %d", rec.Code)
		}
	})

	// Test history with pagination
	t.Run("GetHistory_Pagination", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/history?limit=1&offset=0", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)
		req.Header.Set("X-User-ID", user.ID)

		rec := httptest.NewRecorder()
		gateway.handleHistory(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response config.HistoryList
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if len(response.Entries) > 1 {
			t.Errorf("Expected at most 1 entry with limit=1, got %d", len(response.Entries))
		}
		if response.Total < 2 {
			t.Error("Expected total >= 2")
		}
	})

	// Test history with filtering
	t.Run("GetHistory_FilterBySource", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/history?source=startup", nil)
		req.Header.Set("Authorization", "Bearer "+token.TokenHash)
		req.Header.Set("X-User-ID", user.ID)

		rec := httptest.NewRecorder()
		gateway.handleHistory(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response config.HistoryList
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		// All entries should have startup source
		for _, entry := range response.Entries {
			if entry.Source != "startup" {
				t.Errorf("Expected source 'startup', got '%s'", entry.Source)
			}
		}
	})
}
