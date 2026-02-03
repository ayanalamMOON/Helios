package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigLoading(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configYAML := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
  listen_addr: ":6379"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: true

observability:
  log_level: "DEBUG"
  metrics_enabled: true
  metrics_port: ":9090"

rate_limiting:
  enabled: true
  requests_per_second: 500
  burst_size: 50
  cleanup_interval: 3m

timeouts:
  read_timeout: 10s
  write_timeout: 10s
  idle_timeout: 60s
  shutdown_timeout: 15s

performance:
  max_connections: 5000
  worker_pool_size: 50
  queue_size: 5000
  snapshot_interval: 10m
  aof_sync_mode: "always"
`

	if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Load configuration
	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	cfg := mgr.GetConfig()

	// Validate immutable fields
	if cfg.Immutable.NodeID != "test-node" {
		t.Errorf("Expected node_id 'test-node', got '%s'", cfg.Immutable.NodeID)
	}
	if cfg.Immutable.DataDir != "/data/test" {
		t.Errorf("Expected data_dir '/data/test', got '%s'", cfg.Immutable.DataDir)
	}

	// Validate observability
	if cfg.Observability.LogLevel != "DEBUG" {
		t.Errorf("Expected log_level 'DEBUG', got '%s'", cfg.Observability.LogLevel)
	}
	if !cfg.Observability.MetricsEnabled {
		t.Error("Expected metrics_enabled to be true")
	}

	// Validate rate limiting
	if !cfg.RateLimiting.Enabled {
		t.Error("Expected rate_limiting.enabled to be true")
	}
	if cfg.RateLimiting.RequestsPerSecond != 500 {
		t.Errorf("Expected requests_per_second 500, got %d", cfg.RateLimiting.RequestsPerSecond)
	}

	// Validate timeouts
	if cfg.Timeouts.ReadTimeout != 10*time.Second {
		t.Errorf("Expected read_timeout 10s, got %v", cfg.Timeouts.ReadTimeout)
	}

	// Validate performance
	if cfg.Performance.MaxConnections != 5000 {
		t.Errorf("Expected max_connections 5000, got %d", cfg.Performance.MaxConnections)
	}
	if cfg.Performance.AOFSyncMode != "always" {
		t.Errorf("Expected aof_sync_mode 'always', got '%s'", cfg.Performance.AOFSyncMode)
	}
}

func TestConfigReload(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
  listen_addr: ":6379"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: true

observability:
  log_level: "INFO"
  metrics_enabled: true

rate_limiting:
  enabled: true
  requests_per_second: 1000
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write initial config: %v", err)
	}

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Verify initial config
	cfg := mgr.GetConfig()
	if cfg.Observability.LogLevel != "INFO" {
		t.Fatalf("Initial log level should be INFO, got %s", cfg.Observability.LogLevel)
	}
	if cfg.RateLimiting.RequestsPerSecond != 1000 {
		t.Fatalf("Initial RPS should be 1000, got %d", cfg.RateLimiting.RequestsPerSecond)
	}

	// Update config file with new hot-reloadable values
	updatedConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
  listen_addr: ":6379"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: true

observability:
  log_level: "DEBUG"
  metrics_enabled: false

rate_limiting:
  enabled: true
  requests_per_second: 2000
`

	if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
		t.Fatalf("Failed to write updated config: %v", err)
	}

	// Reload configuration
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Failed to reload config: %v", err)
	}

	// Verify reloaded config
	cfg = mgr.GetConfig()
	if cfg.Observability.LogLevel != "DEBUG" {
		t.Errorf("Log level should be DEBUG after reload, got %s", cfg.Observability.LogLevel)
	}
	if cfg.Observability.MetricsEnabled {
		t.Error("Metrics should be disabled after reload")
	}
	if cfg.RateLimiting.RequestsPerSecond != 2000 {
		t.Errorf("RPS should be 2000 after reload, got %d", cfg.RateLimiting.RequestsPerSecond)
	}

	// Verify reload stats
	count, _ := mgr.GetReloadStats()
	if count != 1 {
		t.Errorf("Expected reload count 1, got %d", count)
	}
}

func TestImmutableFieldValidation(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
  listen_addr: ":6379"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: true

observability:
  log_level: "INFO"
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Try to change node_id (should fail)
	badConfig := `
immutable:
  node_id: "different-node"
  data_dir: "/data/test"
  listen_addr: ":6379"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: true

observability:
  log_level: "DEBUG"
`

	if err := os.WriteFile(configPath, []byte(badConfig), 0644); err != nil {
		t.Fatalf("Failed to write bad config: %v", err)
	}

	err = mgr.Reload()
	if err == nil {
		t.Fatal("Expected reload to fail when changing immutable field")
	}
	if err.Error() != "immutable field change detected: node_id cannot be changed (old: test-node, new: different-node)" {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid_config",
			config: `
immutable:
  node_id: "test"
observability:
  log_level: "INFO"
rate_limiting:
  enabled: true
  requests_per_second: 100
  burst_size: 10
performance:
  worker_pool_size: 10
  queue_size: 100
  aof_sync_mode: "every"
`,
			expectError: false,
		},
		{
			name: "invalid_log_level",
			config: `
immutable:
  node_id: "test"
observability:
  log_level: "INVALID"
`,
			expectError: true,
			errorMsg:    "invalid log level",
		},
		{
			name: "invalid_aof_mode",
			config: `
immutable:
  node_id: "test"
performance:
  worker_pool_size: 10
  queue_size: 100
  aof_sync_mode: "invalid"
`,
			expectError: true,
			errorMsg:    "invalid aof_sync_mode",
		},
		{
			name: "negative_max_connections",
			config: `
immutable:
  node_id: "test"
performance:
  max_connections: -1
  worker_pool_size: 10
  queue_size: 100
`,
			expectError: true,
			errorMsg:    "max_connections cannot be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")

			if err := os.WriteFile(configPath, []byte(tt.config), 0644); err != nil {
				t.Fatalf("Failed to write config: %v", err)
			}

			_, err := NewManager(configPath)
			if tt.expectError {
				if err == nil {
					t.Fatal("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestReloadListener(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
observability:
  log_level: "INFO"
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Add listener that tracks reload calls
	var listenerCalled bool
	var oldLogLevel, newLogLevel string

	mgr.AddReloadListener(func(old, new *Config) error {
		listenerCalled = true
		oldLogLevel = old.Observability.LogLevel
		newLogLevel = new.Observability.LogLevel
		return nil
	})

	// Update config
	updatedConfig := `
immutable:
  node_id: "test-node"
observability:
  log_level: "DEBUG"
`

	if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
		t.Fatalf("Failed to write updated config: %v", err)
	}

	// Reload
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Failed to reload: %v", err)
	}

	if !listenerCalled {
		t.Error("Reload listener was not called")
	}
	if oldLogLevel != "INFO" {
		t.Errorf("Expected old log level INFO, got %s", oldLogLevel)
	}
	if newLogLevel != "DEBUG" {
		t.Errorf("Expected new log level DEBUG, got %s", newLogLevel)
	}
}

func TestReloadListenerVeto(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
rate_limiting:
  requests_per_second: 1000
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Add listener that vetoes low RPS values
	mgr.AddReloadListener(func(old, new *Config) error {
		if new.RateLimiting.RequestsPerSecond < 500 {
			return fmt.Errorf("RPS too low: %d", new.RateLimiting.RequestsPerSecond)
		}
		return nil
	})

	// Try to set RPS to 100 (should be rejected)
	badConfig := `
immutable:
  node_id: "test-node"
rate_limiting:
  requests_per_second: 100
`

	if err := os.WriteFile(configPath, []byte(badConfig), 0644); err != nil {
		t.Fatalf("Failed to write bad config: %v", err)
	}

	err = mgr.Reload()
	if err == nil {
		t.Fatal("Expected reload to be vetoed by listener")
	}

	// Verify config wasn't changed
	cfg := mgr.GetConfig()
	if cfg.RateLimiting.RequestsPerSecond != 1000 {
		t.Errorf("Config should not have changed, but RPS is now %d",
			cfg.RateLimiting.RequestsPerSecond)
	}
}

func TestConfigExport(t *testing.T) {
	cfg := &Config{
		Immutable: ImmutableConfig{
			NodeID:  "test-node",
			DataDir: "/data",
		},
		Observability: ObservabilityConfig{
			LogLevel:       "INFO",
			MetricsEnabled: true,
		},
	}
	cfg.ApplyDefaults()

	// Test YAML export
	yamlStr, err := cfg.ExportYAML()
	if err != nil {
		t.Fatalf("Failed to export YAML: %v", err)
	}
	if yamlStr == "" {
		t.Error("YAML export is empty")
	}

	// Test JSON export
	jsonStr, err := cfg.ExportJSON()
	if err != nil {
		t.Fatalf("Failed to export JSON: %v", err)
	}
	if jsonStr == "" {
		t.Error("JSON export is empty")
	}
}

func TestFileWatching(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
observability:
  log_level: "INFO"
rate_limiting:
  requests_per_second: 1000
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Test starting file watching
	if err := mgr.StartWatching(); err != nil {
		t.Fatalf("Failed to start watching: %v", err)
	}

	if !mgr.IsWatching() {
		t.Error("IsWatching() should return true after StartWatching()")
	}

	// Test that starting watching again fails
	if err := mgr.StartWatching(); err == nil {
		t.Error("Starting watching twice should return error")
	}

	// Add listener to track reloads
	var reloadCalled bool
	var newLogLevel string
	mgr.AddReloadListener(func(old, new *Config) error {
		reloadCalled = true
		newLogLevel = new.Observability.LogLevel
		return nil
	})

	// Update config file
	updatedConfig := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
observability:
  log_level: "DEBUG"
rate_limiting:
  requests_per_second: 2000
`

	if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
		t.Fatalf("Failed to write updated config: %v", err)
	}

	// Wait for debounced reload (with timeout)
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for !reloadCalled {
		select {
		case <-timeout:
			t.Fatal("Reload not triggered within timeout")
		case <-ticker.C:
			// Keep checking
		}
	}

	if newLogLevel != "DEBUG" {
		t.Errorf("Expected log level DEBUG, got %s", newLogLevel)
	}

	// Verify config was actually updated
	cfg := mgr.GetConfig()
	if cfg.Observability.LogLevel != "DEBUG" {
		t.Errorf("Config not updated: expected DEBUG, got %s", cfg.Observability.LogLevel)
	}
	if cfg.RateLimiting.RequestsPerSecond != 2000 {
		t.Errorf("Config not updated: expected RPS 2000, got %d", cfg.RateLimiting.RequestsPerSecond)
	}

	// Test stopping file watching
	if err := mgr.StopWatching(); err != nil {
		t.Fatalf("Failed to stop watching: %v", err)
	}

	if mgr.IsWatching() {
		t.Error("IsWatching() should return false after StopWatching()")
	}

	// Test that stopping watching again is safe
	if err := mgr.StopWatching(); err != nil {
		t.Error("Stopping watching twice should not return error")
	}

	// Verify no more reloads after stopping
	reloadCalled = false
	anotherUpdate := `
immutable:
  node_id: "test-node"
  data_dir: "/data/test"
observability:
  log_level: "WARN"
`

	if err := os.WriteFile(configPath, []byte(anotherUpdate), 0644); err != nil {
		t.Fatalf("Failed to write another update: %v", err)
	}

	time.Sleep(300 * time.Millisecond) // Wait longer than debounce

	if reloadCalled {
		t.Error("Reload should not be triggered after StopWatching()")
	}
}

func TestFileWatchingDebounce(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
observability:
  log_level: "INFO"
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	if err := mgr.StartWatching(); err != nil {
		t.Fatalf("Failed to start watching: %v", err)
	}
	defer mgr.StopWatching()

	// Track number of reloads
	var reloadCount int
	mgr.AddReloadListener(func(old, new *Config) error {
		reloadCount++
		return nil
	})

	// Make rapid successive changes
	for i := 0; i < 5; i++ {
		config := fmt.Sprintf(`
immutable:
  node_id: "test-node"
observability:
  log_level: "DEBUG"
rate_limiting:
  requests_per_second: %d
`, 1000+i*100)

		if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
			t.Fatalf("Failed to write config: %v", err)
		}

		time.Sleep(20 * time.Millisecond) // Rapid changes
	}

	// Wait for debounce to settle
	time.Sleep(300 * time.Millisecond)

	// Should have only reloaded once or a few times (not 5 times)
	// due to debouncing
	if reloadCount >= 5 {
		t.Errorf("Debouncing failed: expected < 5 reloads, got %d", reloadCount)
	}
	if reloadCount == 0 {
		t.Error("No reload occurred")
	}
}

func TestFileWatchingListenerVeto(t *testing.T) {
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

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	if err := mgr.StartWatching(); err != nil {
		t.Fatalf("Failed to start watching: %v", err)
	}
	defer mgr.StopWatching()

	// Add listener that rejects low RPS values
	mgr.AddReloadListener(func(old, new *Config) error {
		if new.RateLimiting.RequestsPerSecond < 500 {
			return fmt.Errorf("RPS too low: %d", new.RateLimiting.RequestsPerSecond)
		}
		return nil
	})

	// Try to set RPS to 100 (should be rejected)
	badConfig := `
immutable:
  node_id: "test-node"
observability:
  log_level: "INFO"
rate_limiting:
  requests_per_second: 100
`

	if err := os.WriteFile(configPath, []byte(badConfig), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Wait for reload attempt
	time.Sleep(300 * time.Millisecond)

	// Config should remain unchanged (veto prevented update)
	cfg := mgr.GetConfig()
	if cfg.RateLimiting.RequestsPerSecond != 1000 {
		t.Errorf("Config should not have changed due to veto, expected 1000, got %d",
			cfg.RateLimiting.RequestsPerSecond)
	}
}

func TestFileWatchingEditorPattern(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialConfig := `
immutable:
  node_id: "test-node"
observability:
  log_level: "INFO"
`

	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	if err := mgr.StartWatching(); err != nil {
		t.Fatalf("Failed to start watching: %v", err)
	}
	defer mgr.StopWatching()

	var reloadCalled bool
	mgr.AddReloadListener(func(old, new *Config) error {
		reloadCalled = true
		return nil
	})

	// Simulate editor pattern: remove and recreate file
	// (some editors like vim do this)
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("Failed to remove config: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	updatedConfig := `
immutable:
  node_id: "test-node"
observability:
  log_level: "DEBUG"
`

	if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
		t.Fatalf("Failed to recreate config: %v", err)
	}

	// Wait for reload
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for !reloadCalled {
		select {
		case <-timeout:
			t.Fatal("Reload not triggered after file recreation")
		case <-ticker.C:
			// Keep checking
		}
	}

	cfg := mgr.GetConfig()
	if cfg.Observability.LogLevel != "DEBUG" {
		t.Errorf("Config not updated after file recreation: expected DEBUG, got %s",
			cfg.Observability.LogLevel)
	}
}

func TestConfigDiff(t *testing.T) {
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
		t.Fatalf("Failed to write config: %v", err)
	}

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Test diff with no changes
	t.Run("NoChanges", func(t *testing.T) {
		diff, err := mgr.PreviewReload()
		if err != nil {
			t.Fatalf("Failed to preview reload: %v", err)
		}

		if diff.HasChanges {
			t.Error("Expected no changes")
		}

		if diff.Summary != "No changes detected" {
			t.Errorf("Expected 'No changes detected', got '%s'", diff.Summary)
		}
	})

	// Test diff with hot-reloadable changes
	t.Run("HotReloadableChanges", func(t *testing.T) {
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
			t.Fatalf("Failed to write config: %v", err)
		}

		diff, err := mgr.PreviewReload()
		if err != nil {
			t.Fatalf("Failed to preview reload: %v", err)
		}

		if !diff.HasChanges {
			t.Fatal("Expected changes to be detected")
		}

		// Check observability changes
		obsChanges, ok := diff.Changes["observability"]
		if !ok {
			t.Error("Expected observability changes")
		}
		if len(obsChanges) != 3 {
			t.Errorf("Expected 3 observability changes, got %d", len(obsChanges))
		}

		// Check rate limiting changes
		rateLimitChanges, ok := diff.Changes["rate_limiting"]
		if !ok {
			t.Error("Expected rate_limiting changes")
		}
		if len(rateLimitChanges) != 2 {
			t.Errorf("Expected 2 rate_limiting changes, got %d", len(rateLimitChanges))
		}

		// Check timeout changes
		timeoutChanges, ok := diff.Changes["timeouts"]
		if !ok {
			t.Error("Expected timeout changes")
		}
		if len(timeoutChanges) != 2 {
			t.Errorf("Expected 2 timeout changes, got %d", len(timeoutChanges))
		}

		// Check performance changes
		perfChanges, ok := diff.Changes["performance"]
		if !ok {
			t.Error("Expected performance changes")
		}
		if len(perfChanges) != 3 {
			t.Errorf("Expected 3 performance changes, got %d", len(perfChanges))
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
	t.Run("ImmutableChanges", func(t *testing.T) {
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
			t.Fatalf("Failed to write config: %v", err)
		}

		diff, err := mgr.PreviewReload()
		if err != nil {
			t.Fatalf("Failed to preview reload: %v", err)
		}

		if !diff.HasChanges {
			t.Fatal("Expected changes to be detected")
		}

		// Should detect 5 immutable changes
		if len(diff.ImmutableChanges) != 5 {
			t.Errorf("Expected 5 immutable changes, got %d", len(diff.ImmutableChanges))
		}

		// Verify each immutable change
		fieldsSeen := make(map[string]bool)
		for _, change := range diff.ImmutableChanges {
			fieldsSeen[change.Field] = true
			if change.Category != "immutable" {
				t.Errorf("Expected category 'immutable', got '%s'", change.Category)
			}
		}

		expectedFields := []string{"node_id", "data_dir", "listen_addr", "raft_addr", "raft_enabled"}
		for _, field := range expectedFields {
			if !fieldsSeen[field] {
				t.Errorf("Expected to see immutable change for field '%s'", field)
			}
		}
	})

	// Test diff with validation errors
	t.Run("ValidationErrors", func(t *testing.T) {
		invalidConfig := `
immutable:
  node_id: "test-node"
observability:
  log_level: "INVALID_LEVEL"
rate_limiting:
  requests_per_second: -100
performance:
  max_connections: -50
  aof_sync_mode: "invalid_mode"
`

		if err := os.WriteFile(configPath, []byte(invalidConfig), 0644); err != nil {
			t.Fatalf("Failed to write config: %v", err)
		}

		diff, err := mgr.PreviewReload()
		if err != nil {
			t.Fatalf("Failed to preview reload: %v", err)
		}

		// Should have validation errors
		if len(diff.ValidationErrors) == 0 {
			t.Error("Expected validation errors")
		}
	})
}

func TestConfigDiffFieldChanges(t *testing.T) {
	oldCfg := &Config{
		Immutable: ImmutableConfig{
			NodeID:      "node-1",
			DataDir:     "/data",
			ListenAddr:  ":8443",
			RaftAddr:    "127.0.0.1:7000",
			RaftEnabled: false,
		},
		Observability: ObservabilityConfig{
			LogLevel:       "INFO",
			MetricsEnabled: true,
			MetricsPort:    ":9090",
		},
		RateLimiting: RateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 1000,
			BurstSize:         100,
			CleanupInterval:   5 * time.Minute,
		},
	}

	newCfg := &Config{
		Immutable: ImmutableConfig{
			NodeID:      "node-1", // Same
			DataDir:     "/data",  // Same
			ListenAddr:  ":8443",  // Same
			RaftAddr:    "127.0.0.1:7000",
			RaftEnabled: false,
		},
		Observability: ObservabilityConfig{
			LogLevel:       "DEBUG", // Changed
			MetricsEnabled: false,   // Changed
			MetricsPort:    ":9091", // Changed
		},
		RateLimiting: RateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 2000,             // Changed
			BurstSize:         200,              // Changed
			CleanupInterval:   10 * time.Minute, // Changed
		},
	}

	// Test observability changes
	obsChanges := checkObservabilityChanges(oldCfg, newCfg)
	if len(obsChanges) != 3 {
		t.Errorf("Expected 3 observability changes, got %d", len(obsChanges))
	}

	// Test rate limiting changes
	rateLimitChanges := checkRateLimitChanges(oldCfg, newCfg)
	if len(rateLimitChanges) != 3 {
		t.Errorf("Expected 3 rate limiting changes, got %d", len(rateLimitChanges))
	}

	// Test immutable changes (none expected)
	immutableChanges := checkImmutableChanges(oldCfg, newCfg)
	if len(immutableChanges) != 0 {
		t.Errorf("Expected 0 immutable changes, got %d", len(immutableChanges))
	}
}
