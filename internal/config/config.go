package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// Manager handles configuration loading and reloading
type Manager struct {
	mu          sync.RWMutex
	config      *Config
	configPath  string
	listeners   []ReloadListener
	listenersMu sync.RWMutex
	reloadCount int
	lastReload  time.Time

	// File watching
	watcher       *fsnotify.Watcher
	watcherStopCh chan struct{}
	watcherDone   chan struct{}
	watchEnabled  bool
	debounceTimer *time.Timer

	// History tracking
	historyManager *HistoryManager
	historyEnabled bool
}

// ReloadListener is called when configuration is reloaded
type ReloadListener func(old, new *Config) error

// Config represents the complete Helios configuration
type Config struct {
	// Immutable fields (cannot be changed after startup)
	Immutable ImmutableConfig `yaml:"immutable" json:"immutable"`

	// Hot-reloadable fields
	Observability ObservabilityConfig `yaml:"observability" json:"observability"`
	RateLimiting  RateLimitConfig     `yaml:"rate_limiting" json:"rate_limiting"`
	Timeouts      TimeoutConfig       `yaml:"timeouts" json:"timeouts"`
	Performance   PerformanceConfig   `yaml:"performance" json:"performance"`
	Sharding      ShardingConfig      `yaml:"sharding" json:"sharding"`
	GraphQL       GraphQLConfig       `yaml:"graphql" json:"graphql"`
}

// ImmutableConfig contains settings that cannot be changed after startup
type ImmutableConfig struct {
	NodeID      string `yaml:"node_id" json:"node_id"`
	DataDir     string `yaml:"data_dir" json:"data_dir"`
	ListenAddr  string `yaml:"listen_addr" json:"listen_addr"`
	RaftAddr    string `yaml:"raft_addr" json:"raft_addr"`
	RaftEnabled bool   `yaml:"raft_enabled" json:"raft_enabled"`
}

// ObservabilityConfig contains logging and monitoring settings
type ObservabilityConfig struct {
	LogLevel        string `yaml:"log_level" json:"log_level"`
	MetricsEnabled  bool   `yaml:"metrics_enabled" json:"metrics_enabled"`
	MetricsPort     string `yaml:"metrics_port" json:"metrics_port"`
	TracingEnabled  bool   `yaml:"tracing_enabled" json:"tracing_enabled"`
	TracingEndpoint string `yaml:"tracing_endpoint" json:"tracing_endpoint"`
}

// RateLimitConfig contains rate limiting settings
type RateLimitConfig struct {
	Enabled           bool          `yaml:"enabled" json:"enabled"`
	RequestsPerSecond int           `yaml:"requests_per_second" json:"requests_per_second"`
	BurstSize         int           `yaml:"burst_size" json:"burst_size"`
	CleanupInterval   time.Duration `yaml:"cleanup_interval" json:"cleanup_interval"`
}

// TimeoutConfig contains various timeout settings
type TimeoutConfig struct {
	ReadTimeout     time.Duration `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout     time.Duration `yaml:"idle_timeout" json:"idle_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout"`
}

// PerformanceConfig contains performance tuning settings
type PerformanceConfig struct {
	MaxConnections   int           `yaml:"max_connections" json:"max_connections"`
	WorkerPoolSize   int           `yaml:"worker_pool_size" json:"worker_pool_size"`
	QueueSize        int           `yaml:"queue_size" json:"queue_size"`
	SnapshotInterval time.Duration `yaml:"snapshot_interval" json:"snapshot_interval"`
	AOFSyncMode      string        `yaml:"aof_sync_mode" json:"aof_sync_mode"`
}

// ShardingConfig contains horizontal sharding settings
type ShardingConfig struct {
	Enabled           bool   `yaml:"enabled" json:"enabled"`
	VirtualNodes      int    `yaml:"virtual_nodes" json:"virtual_nodes"`
	ReplicationFactor int    `yaml:"replication_factor" json:"replication_factor"`
	MigrationRate     int    `yaml:"migration_rate" json:"migration_rate"`
	AutoRebalance     bool   `yaml:"auto_rebalance" json:"auto_rebalance"`
	RebalanceInterval string `yaml:"rebalance_interval" json:"rebalance_interval"`
}

// GraphQLConfig contains GraphQL API settings
type GraphQLConfig struct {
	Enabled              bool     `yaml:"enabled" json:"enabled"`
	Endpoint             string   `yaml:"endpoint" json:"endpoint"`
	PlaygroundEnabled    bool     `yaml:"playground_enabled" json:"playground_enabled"`
	PlaygroundEndpoint   string   `yaml:"playground_endpoint" json:"playground_endpoint"`
	IntrospectionEnabled bool     `yaml:"introspection_enabled" json:"introspection_enabled"`
	MaxQueryDepth        int      `yaml:"max_query_depth" json:"max_query_depth"`
	MaxComplexity        int      `yaml:"max_complexity" json:"max_complexity"`
	AllowedOrigins       []string `yaml:"allowed_origins" json:"allowed_origins"`
}

// NewManager creates a new configuration manager
func NewManager(configPath string) (*Manager, error) {
	cfg, err := loadConfigFromFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	m := &Manager{
		config:     cfg,
		configPath: configPath,
		listeners:  make([]ReloadListener, 0),
		lastReload: time.Now(),
	}

	// Initialize history manager
	historyMgr, err := NewHistoryManager(configPath, nil)
	if err != nil {
		// History is optional, log but don't fail
		fmt.Fprintf(os.Stderr, "Warning: failed to initialize history manager: %v\n", err)
	} else {
		m.historyManager = historyMgr
		m.historyEnabled = true

		// Record initial configuration if history is empty
		if historyMgr.GetEntryCount() == 0 {
			historyMgr.RecordChange(
				nil, cfg,
				ChangeTypeInitial,
				SourceStartup,
				"",
				"Initial configuration load",
				nil,
			)
		}
	}

	return m, nil
}

// GetConfig returns a copy of the current configuration
func (m *Manager) GetConfig() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a deep copy to prevent external modifications
	return m.config.Clone()
}

// Reload reloads configuration from file and notifies listeners
func (m *Manager) Reload() error {
	return m.ReloadWithContext(ChangeTypeReload, SourceAPI, "", "", nil)
}

// ReloadWithContext reloads configuration with additional context for history tracking
func (m *Manager) ReloadWithContext(changeType HistoryChangeType, source, user, description string, tags []string) error {
	// Load new configuration
	newConfig, err := loadConfigFromFile(m.configPath)
	if err != nil {
		return fmt.Errorf("failed to load new config: %w", err)
	}

	// Validate that immutable fields haven't changed
	m.mu.RLock()
	oldConfig := m.config
	m.mu.RUnlock()

	if err := validateImmutableFields(oldConfig, newConfig); err != nil {
		return fmt.Errorf("immutable field change detected: %w", err)
	}

	// Validate new configuration
	if err := newConfig.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Notify listeners (they can veto the reload)
	m.listenersMu.RLock()
	listeners := make([]ReloadListener, len(m.listeners))
	copy(listeners, m.listeners)
	m.listenersMu.RUnlock()

	for _, listener := range listeners {
		if err := listener(oldConfig, newConfig); err != nil {
			return fmt.Errorf("reload listener rejected change: %w", err)
		}
	}

	// Apply new configuration
	m.mu.Lock()
	m.config = newConfig
	m.reloadCount++
	m.lastReload = time.Now()
	m.mu.Unlock()

	// Record in history
	if m.historyEnabled && m.historyManager != nil {
		if _, err := m.historyManager.RecordChange(
			oldConfig, newConfig,
			changeType, source, user, description, tags,
		); err != nil {
			// History failure shouldn't prevent reload
			fmt.Fprintf(os.Stderr, "Warning: failed to record config change in history: %v\n", err)
		}
	}

	return nil
}

// AddReloadListener registers a callback for configuration reloads
func (m *Manager) AddReloadListener(listener ReloadListener) {
	m.listenersMu.Lock()
	defer m.listenersMu.Unlock()
	m.listeners = append(m.listeners, listener)
}

// GetReloadStats returns reload statistics
func (m *Manager) GetReloadStats() (count int, lastReload time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reloadCount, m.lastReload
}

// ConfigDiff represents the differences between two configurations
type ConfigDiff struct {
	HasChanges       bool                     `json:"has_changes"`
	ImmutableChanges []FieldChange            `json:"immutable_changes,omitempty"`
	Changes          map[string][]FieldChange `json:"changes,omitempty"`
	ValidationErrors []string                 `json:"validation_errors,omitempty"`
	Summary          string                   `json:"summary"`
}

// FieldChange represents a change in a configuration field
type FieldChange struct {
	Field    string      `json:"field"`
	OldValue interface{} `json:"old_value"`
	NewValue interface{} `json:"new_value"`
	Category string      `json:"category"` // e.g., "observability", "rate_limiting"
}

// PreviewReload loads the configuration from file and computes a diff
// without actually applying the changes
func (m *Manager) PreviewReload() (*ConfigDiff, error) {
	newConfig, err := loadConfigFromFile(m.configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return m.ComputeDiff(newConfig)
}

// ComputeDiff computes the differences between current and new configuration
func (m *Manager) ComputeDiff(newConfig *Config) (*ConfigDiff, error) {
	m.mu.RLock()
	oldConfig := m.config
	m.mu.RUnlock()

	diff := &ConfigDiff{
		Changes: make(map[string][]FieldChange),
	}

	// Check immutable fields
	immutableChanges := checkImmutableChanges(oldConfig, newConfig)
	if len(immutableChanges) > 0 {
		diff.ImmutableChanges = immutableChanges
		diff.HasChanges = true
	}

	// Check observability changes
	obsChanges := checkObservabilityChanges(oldConfig, newConfig)
	if len(obsChanges) > 0 {
		diff.Changes["observability"] = obsChanges
		diff.HasChanges = true
	}

	// Check rate limiting changes
	rateLimitChanges := checkRateLimitChanges(oldConfig, newConfig)
	if len(rateLimitChanges) > 0 {
		diff.Changes["rate_limiting"] = rateLimitChanges
		diff.HasChanges = true
	}

	// Check timeout changes
	timeoutChanges := checkTimeoutChanges(oldConfig, newConfig)
	if len(timeoutChanges) > 0 {
		diff.Changes["timeouts"] = timeoutChanges
		diff.HasChanges = true
	}

	// Check performance changes
	perfChanges := checkPerformanceChanges(oldConfig, newConfig)
	if len(perfChanges) > 0 {
		diff.Changes["performance"] = perfChanges
		diff.HasChanges = true
	}

	// Validate new configuration
	if err := newConfig.Validate(); err != nil {
		diff.ValidationErrors = append(diff.ValidationErrors, err.Error())
	}

	// Generate summary
	diff.Summary = generateDiffSummary(diff)

	return diff, nil
}

// StartWatching starts watching the configuration file for changes
// and automatically reloads when changes are detected.
// Debouncing is applied to handle rapid successive changes (e.g., from editors).
func (m *Manager) StartWatching() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.watchEnabled {
		return fmt.Errorf("file watching already enabled")
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	if err := watcher.Add(m.configPath); err != nil {
		watcher.Close()
		return fmt.Errorf("failed to watch config file: %w", err)
	}

	m.watcher = watcher
	m.watcherStopCh = make(chan struct{})
	m.watcherDone = make(chan struct{})
	m.watchEnabled = true

	go m.watchLoop()

	return nil
}

// StopWatching stops watching the configuration file
func (m *Manager) StopWatching() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.watchEnabled {
		return nil
	}

	close(m.watcherStopCh)
	<-m.watcherDone

	if m.debounceTimer != nil {
		m.debounceTimer.Stop()
	}

	if err := m.watcher.Close(); err != nil {
		return fmt.Errorf("failed to close watcher: %w", err)
	}

	m.watcher = nil
	m.watcherStopCh = nil
	m.watcherDone = nil
	m.watchEnabled = false

	return nil
}

// IsWatching returns whether file watching is enabled
func (m *Manager) IsWatching() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.watchEnabled
}

// GetMigrationStatus returns the current migration status for the config file
func (m *Manager) GetMigrationStatus() (*MigrationStatus, error) {
	return GetMigrationStatus(m.configPath)
}

// ApplyMigrations applies all pending migrations to the config file
func (m *Manager) ApplyMigrations(dryRun bool) (*MigrationResult, error) {
	result, err := ApplyMigrations(m.configPath, dryRun)
	if err != nil {
		return result, err
	}

	// If migrations were applied (not dry run), reload config
	if !dryRun && result.Success && result.AppliedCount > 0 {
		if reloadErr := m.ReloadWithContext(
			ChangeTypeMigration,
			SourceMigration,
			"",
			fmt.Sprintf("Applied %d migrations (v%d → v%d)", result.AppliedCount, result.FromVersion, result.ToVersion),
			[]string{"migration"},
		); reloadErr != nil {
			// Add warning but don't fail - config was migrated successfully
			if result.Errors == nil {
				result.Errors = []string{}
			}
			result.Errors = append(result.Errors, fmt.Sprintf("config migrated but reload failed: %v", reloadErr))
		}
	}

	return result, nil
}

// RollbackMigration rolls back migrations to a specific version
func (m *Manager) RollbackMigration(targetVersion int, dryRun bool) (*MigrationResult, error) {
	result, err := RollbackMigration(m.configPath, targetVersion, dryRun)
	if err != nil {
		return result, err
	}

	// If rollback was applied (not dry run), reload config
	if !dryRun && result.Success && result.AppliedCount > 0 {
		if reloadErr := m.ReloadWithContext(
			ChangeTypeRollback,
			SourceMigration,
			"",
			fmt.Sprintf("Rolled back %d migrations (v%d → v%d)", result.AppliedCount, result.FromVersion, result.ToVersion),
			[]string{"rollback"},
		); reloadErr != nil {
			if result.Errors == nil {
				result.Errors = []string{}
			}
			result.Errors = append(result.Errors, fmt.Sprintf("config rolled back but reload failed: %v", reloadErr))
		}
	}

	return result, nil
}

// ListBackups lists all backup files for this config
func (m *Manager) ListBackups() ([]string, error) {
	return ListBackups(m.configPath)
}

// RestoreBackup restores config from a backup file
func (m *Manager) RestoreBackup(backupPath string) error {
	if err := RestoreBackup(backupPath, m.configPath); err != nil {
		return err
	}

	// Reload after restore with history context
	return m.ReloadWithContext(
		ChangeTypeRestore,
		SourceAPI,
		"",
		fmt.Sprintf("Restored from backup: %s", backupPath),
		[]string{"backup-restore"},
	)
}

// GetConfigPath returns the configuration file path
func (m *Manager) GetConfigPath() string {
	return m.configPath
}

// History Management Methods

// GetHistory returns configuration history entries based on query
func (m *Manager) GetHistory(query *HistoryQuery) (*HistoryList, error) {
	if !m.historyEnabled || m.historyManager == nil {
		return nil, fmt.Errorf("history tracking is not enabled")
	}
	return m.historyManager.GetHistory(query)
}

// GetHistoryEntry returns a specific history entry by ID or version
func (m *Manager) GetHistoryEntry(idOrVersion string) (*HistoryEntry, error) {
	if !m.historyEnabled || m.historyManager == nil {
		return nil, fmt.Errorf("history tracking is not enabled")
	}
	return m.historyManager.GetEntry(idOrVersion)
}

// GetHistoryStats returns history statistics
func (m *Manager) GetHistoryStats() (*HistoryStats, error) {
	if !m.historyEnabled || m.historyManager == nil {
		return nil, fmt.Errorf("history tracking is not enabled")
	}
	return m.historyManager.GetStats()
}

// CompareHistoryVersions compares two history versions
func (m *Manager) CompareHistoryVersions(versionA, versionB int) (*HistoryComparison, error) {
	if !m.historyEnabled || m.historyManager == nil {
		return nil, fmt.Errorf("history tracking is not enabled")
	}
	return m.historyManager.Compare(versionA, versionB)
}

// GetHistorySnapshot returns the configuration snapshot for a version
func (m *Manager) GetHistorySnapshot(version int) (map[string]interface{}, error) {
	if !m.historyEnabled || m.historyManager == nil {
		return nil, fmt.Errorf("history tracking is not enabled")
	}
	return m.historyManager.GetSnapshot(version)
}

// RestoreHistoryVersion restores configuration to a specific history version
func (m *Manager) RestoreHistoryVersion(version int, user, description string) error {
	if !m.historyEnabled || m.historyManager == nil {
		return fmt.Errorf("history tracking is not enabled")
	}

	// Restore the config file
	if err := m.historyManager.RestoreVersion(version, m.configPath); err != nil {
		return fmt.Errorf("failed to restore version: %w", err)
	}

	// Reload with history context
	if description == "" {
		description = fmt.Sprintf("Restored to version %d", version)
	}
	return m.ReloadWithContext(ChangeTypeRestore, SourceAPI, user, description, []string{"restore"})
}

// PurgeHistory removes history entries older than the specified duration
func (m *Manager) PurgeHistory(olderThan time.Duration) (int, error) {
	if !m.historyEnabled || m.historyManager == nil {
		return 0, fmt.Errorf("history tracking is not enabled")
	}
	return m.historyManager.PurgeOldEntries(olderThan)
}

// ClearHistory removes all history entries
func (m *Manager) ClearHistory() error {
	if !m.historyEnabled || m.historyManager == nil {
		return fmt.Errorf("history tracking is not enabled")
	}
	return m.historyManager.Clear()
}

// IsHistoryEnabled returns whether history tracking is enabled
func (m *Manager) IsHistoryEnabled() bool {
	return m.historyEnabled
}

// GetHistoryManager returns the history manager (for advanced use cases)
func (m *Manager) GetHistoryManager() *HistoryManager {
	return m.historyManager
}

// watchLoop monitors file system events and triggers reloads
func (m *Manager) watchLoop() {
	defer close(m.watcherDone)

	const debounceDelay = 100 * time.Millisecond

	for {
		select {
		case <-m.watcherStopCh:
			return

		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}

			// Handle Write and Create events (editors may create temp files)
			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				// Debounce: wait for a short period to handle rapid changes
				if m.debounceTimer != nil {
					m.debounceTimer.Stop()
				}

				m.debounceTimer = time.AfterFunc(debounceDelay, func() {
					if err := m.ReloadWithContext(ChangeTypeFileWatch, SourceFileWatch, "", "Automatic reload from file change", nil); err != nil {
						// Log error but don't crash
						fmt.Fprintf(os.Stderr, "Auto-reload failed: %v\n", err)
					}
				})
			}

			// Handle Remove/Rename events (some editors delete and recreate files)
			if event.Op&fsnotify.Remove == fsnotify.Remove || event.Op&fsnotify.Rename == fsnotify.Rename {
				// Re-add the watch after a delay to handle editor patterns
				// The file might be recreated after removal
				time.AfterFunc(200*time.Millisecond, func() {
					m.mu.Lock()
					defer m.mu.Unlock()
					if m.watcher != nil {
						// Remove old watch (may already be gone)
						m.watcher.Remove(m.configPath)

						// Try to add new watch with retries
						maxRetries := 5
						for i := 0; i < maxRetries; i++ {
							if err := m.watcher.Add(m.configPath); err == nil {
								// Successfully re-watched, trigger reload
								time.AfterFunc(50*time.Millisecond, func() {
									if err := m.ReloadWithContext(ChangeTypeFileWatch, SourceFileWatch, "", "Automatic reload after file recreate", nil); err != nil {
										fmt.Fprintf(os.Stderr, "Auto-reload after recreate failed: %v\n", err)
									}
								})
								break
							}
							// File might not exist yet, wait and retry
							if i < maxRetries-1 {
								time.Sleep(100 * time.Millisecond)
							}
						}
					}
				})
			}

		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "File watcher error: %v\n", err)
		}
	}
}

// loadConfigFromFile loads configuration from a YAML or JSON file
func loadConfigFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := &Config{}

	// Try YAML first, then JSON
	if err := yaml.Unmarshal(data, cfg); err != nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config (tried YAML and JSON): %w", err)
		}
	}

	// Apply defaults
	cfg.ApplyDefaults()

	return cfg, nil
}

// validateImmutableFields ensures immutable fields haven't changed
func validateImmutableFields(old, new *Config) error {
	if old.Immutable.NodeID != new.Immutable.NodeID {
		return fmt.Errorf("node_id cannot be changed (old: %s, new: %s)",
			old.Immutable.NodeID, new.Immutable.NodeID)
	}
	if old.Immutable.DataDir != new.Immutable.DataDir {
		return fmt.Errorf("data_dir cannot be changed (old: %s, new: %s)",
			old.Immutable.DataDir, new.Immutable.DataDir)
	}
	if old.Immutable.ListenAddr != new.Immutable.ListenAddr {
		return fmt.Errorf("listen_addr cannot be changed (old: %s, new: %s)",
			old.Immutable.ListenAddr, new.Immutable.ListenAddr)
	}
	if old.Immutable.RaftAddr != new.Immutable.RaftAddr {
		return fmt.Errorf("raft_addr cannot be changed (old: %s, new: %s)",
			old.Immutable.RaftAddr, new.Immutable.RaftAddr)
	}
	if old.Immutable.RaftEnabled != new.Immutable.RaftEnabled {
		return fmt.Errorf("raft_enabled cannot be changed (old: %v, new: %v)",
			old.Immutable.RaftEnabled, new.Immutable.RaftEnabled)
	}
	return nil
}

// Clone creates a deep copy of the configuration
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}

	return &Config{
		Immutable:     c.Immutable,
		Observability: c.Observability,
		RateLimiting:  c.RateLimiting,
		Timeouts:      c.Timeouts,
		Performance:   c.Performance,
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	// Validate observability
	validLogLevels := map[string]bool{
		"DEBUG": true, "INFO": true, "WARN": true, "ERROR": true, "FATAL": true,
	}
	if !validLogLevels[c.Observability.LogLevel] {
		return fmt.Errorf("invalid log level: %s", c.Observability.LogLevel)
	}

	// Validate rate limiting
	if c.RateLimiting.Enabled {
		if c.RateLimiting.RequestsPerSecond <= 0 {
			return fmt.Errorf("requests_per_second must be positive")
		}
		if c.RateLimiting.BurstSize <= 0 {
			return fmt.Errorf("burst_size must be positive")
		}
	}

	// Validate timeouts
	if c.Timeouts.ReadTimeout < 0 {
		return fmt.Errorf("read_timeout cannot be negative")
	}
	if c.Timeouts.WriteTimeout < 0 {
		return fmt.Errorf("write_timeout cannot be negative")
	}

	// Validate performance
	if c.Performance.MaxConnections < 0 {
		return fmt.Errorf("max_connections cannot be negative")
	}
	if c.Performance.WorkerPoolSize <= 0 {
		return fmt.Errorf("worker_pool_size must be positive")
	}
	if c.Performance.QueueSize <= 0 {
		return fmt.Errorf("queue_size must be positive")
	}

	validAOFModes := map[string]bool{
		"always": true, "every": true, "no": true,
	}
	if !validAOFModes[c.Performance.AOFSyncMode] {
		return fmt.Errorf("invalid aof_sync_mode: %s (must be always, every, or no)",
			c.Performance.AOFSyncMode)
	}

	return nil
}

// ApplyDefaults applies default values to missing configuration fields
func (c *Config) ApplyDefaults() {
	// Observability defaults
	if c.Observability.LogLevel == "" {
		c.Observability.LogLevel = "INFO"
	}
	if c.Observability.MetricsPort == "" {
		c.Observability.MetricsPort = ":9090"
	}

	// Rate limiting defaults
	if c.RateLimiting.RequestsPerSecond == 0 {
		c.RateLimiting.RequestsPerSecond = 1000
	}
	if c.RateLimiting.BurstSize == 0 {
		c.RateLimiting.BurstSize = 100
	}
	if c.RateLimiting.CleanupInterval == 0 {
		c.RateLimiting.CleanupInterval = 5 * time.Minute
	}

	// Timeout defaults
	if c.Timeouts.ReadTimeout == 0 {
		c.Timeouts.ReadTimeout = 30 * time.Second
	}
	if c.Timeouts.WriteTimeout == 0 {
		c.Timeouts.WriteTimeout = 30 * time.Second
	}
	if c.Timeouts.IdleTimeout == 0 {
		c.Timeouts.IdleTimeout = 120 * time.Second
	}
	if c.Timeouts.ShutdownTimeout == 0 {
		c.Timeouts.ShutdownTimeout = 30 * time.Second
	}

	// Performance defaults
	if c.Performance.MaxConnections == 0 {
		c.Performance.MaxConnections = 10000
	}
	if c.Performance.WorkerPoolSize == 0 {
		c.Performance.WorkerPoolSize = 100
	}
	if c.Performance.QueueSize == 0 {
		c.Performance.QueueSize = 10000
	}
	if c.Performance.SnapshotInterval == 0 {
		c.Performance.SnapshotInterval = 5 * time.Minute
	}
	if c.Performance.AOFSyncMode == "" {
		c.Performance.AOFSyncMode = "every"
	}
}

// Export exports the configuration as YAML
func (c *Config) ExportYAML() (string, error) {
	data, err := yaml.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("failed to marshal config: %w", err)
	}
	return string(data), nil
}

// Export exports the configuration as JSON
func (c *Config) ExportJSON() (string, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal config: %w", err)
	}
	return string(data), nil
}

// checkImmutableChanges checks for changes in immutable fields
func checkImmutableChanges(old, new *Config) []FieldChange {
	var changes []FieldChange

	if old.Immutable.NodeID != new.Immutable.NodeID {
		changes = append(changes, FieldChange{
			Field:    "node_id",
			OldValue: old.Immutable.NodeID,
			NewValue: new.Immutable.NodeID,
			Category: "immutable",
		})
	}

	if old.Immutable.DataDir != new.Immutable.DataDir {
		changes = append(changes, FieldChange{
			Field:    "data_dir",
			OldValue: old.Immutable.DataDir,
			NewValue: new.Immutable.DataDir,
			Category: "immutable",
		})
	}

	if old.Immutable.ListenAddr != new.Immutable.ListenAddr {
		changes = append(changes, FieldChange{
			Field:    "listen_addr",
			OldValue: old.Immutable.ListenAddr,
			NewValue: new.Immutable.ListenAddr,
			Category: "immutable",
		})
	}

	if old.Immutable.RaftAddr != new.Immutable.RaftAddr {
		changes = append(changes, FieldChange{
			Field:    "raft_addr",
			OldValue: old.Immutable.RaftAddr,
			NewValue: new.Immutable.RaftAddr,
			Category: "immutable",
		})
	}

	if old.Immutable.RaftEnabled != new.Immutable.RaftEnabled {
		changes = append(changes, FieldChange{
			Field:    "raft_enabled",
			OldValue: old.Immutable.RaftEnabled,
			NewValue: new.Immutable.RaftEnabled,
			Category: "immutable",
		})
	}

	return changes
}

// checkObservabilityChanges checks for changes in observability settings
func checkObservabilityChanges(old, new *Config) []FieldChange {
	var changes []FieldChange

	if old.Observability.LogLevel != new.Observability.LogLevel {
		changes = append(changes, FieldChange{
			Field:    "log_level",
			OldValue: old.Observability.LogLevel,
			NewValue: new.Observability.LogLevel,
			Category: "observability",
		})
	}

	if old.Observability.MetricsEnabled != new.Observability.MetricsEnabled {
		changes = append(changes, FieldChange{
			Field:    "metrics_enabled",
			OldValue: old.Observability.MetricsEnabled,
			NewValue: new.Observability.MetricsEnabled,
			Category: "observability",
		})
	}

	if old.Observability.MetricsPort != new.Observability.MetricsPort {
		changes = append(changes, FieldChange{
			Field:    "metrics_port",
			OldValue: old.Observability.MetricsPort,
			NewValue: new.Observability.MetricsPort,
			Category: "observability",
		})
	}

	if old.Observability.TracingEnabled != new.Observability.TracingEnabled {
		changes = append(changes, FieldChange{
			Field:    "tracing_enabled",
			OldValue: old.Observability.TracingEnabled,
			NewValue: new.Observability.TracingEnabled,
			Category: "observability",
		})
	}

	if old.Observability.TracingEndpoint != new.Observability.TracingEndpoint {
		changes = append(changes, FieldChange{
			Field:    "tracing_endpoint",
			OldValue: old.Observability.TracingEndpoint,
			NewValue: new.Observability.TracingEndpoint,
			Category: "observability",
		})
	}

	return changes
}

// checkRateLimitChanges checks for changes in rate limiting settings
func checkRateLimitChanges(old, new *Config) []FieldChange {
	var changes []FieldChange

	if old.RateLimiting.Enabled != new.RateLimiting.Enabled {
		changes = append(changes, FieldChange{
			Field:    "enabled",
			OldValue: old.RateLimiting.Enabled,
			NewValue: new.RateLimiting.Enabled,
			Category: "rate_limiting",
		})
	}

	if old.RateLimiting.RequestsPerSecond != new.RateLimiting.RequestsPerSecond {
		changes = append(changes, FieldChange{
			Field:    "requests_per_second",
			OldValue: old.RateLimiting.RequestsPerSecond,
			NewValue: new.RateLimiting.RequestsPerSecond,
			Category: "rate_limiting",
		})
	}

	if old.RateLimiting.BurstSize != new.RateLimiting.BurstSize {
		changes = append(changes, FieldChange{
			Field:    "burst_size",
			OldValue: old.RateLimiting.BurstSize,
			NewValue: new.RateLimiting.BurstSize,
			Category: "rate_limiting",
		})
	}

	if old.RateLimiting.CleanupInterval != new.RateLimiting.CleanupInterval {
		changes = append(changes, FieldChange{
			Field:    "cleanup_interval",
			OldValue: old.RateLimiting.CleanupInterval.String(),
			NewValue: new.RateLimiting.CleanupInterval.String(),
			Category: "rate_limiting",
		})
	}

	return changes
}

// checkTimeoutChanges checks for changes in timeout settings
func checkTimeoutChanges(old, new *Config) []FieldChange {
	var changes []FieldChange

	if old.Timeouts.ReadTimeout != new.Timeouts.ReadTimeout {
		changes = append(changes, FieldChange{
			Field:    "read_timeout",
			OldValue: old.Timeouts.ReadTimeout.String(),
			NewValue: new.Timeouts.ReadTimeout.String(),
			Category: "timeouts",
		})
	}

	if old.Timeouts.WriteTimeout != new.Timeouts.WriteTimeout {
		changes = append(changes, FieldChange{
			Field:    "write_timeout",
			OldValue: old.Timeouts.WriteTimeout.String(),
			NewValue: new.Timeouts.WriteTimeout.String(),
			Category: "timeouts",
		})
	}

	if old.Timeouts.IdleTimeout != new.Timeouts.IdleTimeout {
		changes = append(changes, FieldChange{
			Field:    "idle_timeout",
			OldValue: old.Timeouts.IdleTimeout.String(),
			NewValue: new.Timeouts.IdleTimeout.String(),
			Category: "timeouts",
		})
	}

	if old.Timeouts.ShutdownTimeout != new.Timeouts.ShutdownTimeout {
		changes = append(changes, FieldChange{
			Field:    "shutdown_timeout",
			OldValue: old.Timeouts.ShutdownTimeout.String(),
			NewValue: new.Timeouts.ShutdownTimeout.String(),
			Category: "timeouts",
		})
	}

	return changes
}

// checkPerformanceChanges checks for changes in performance settings
func checkPerformanceChanges(old, new *Config) []FieldChange {
	var changes []FieldChange

	if old.Performance.MaxConnections != new.Performance.MaxConnections {
		changes = append(changes, FieldChange{
			Field:    "max_connections",
			OldValue: old.Performance.MaxConnections,
			NewValue: new.Performance.MaxConnections,
			Category: "performance",
		})
	}

	if old.Performance.WorkerPoolSize != new.Performance.WorkerPoolSize {
		changes = append(changes, FieldChange{
			Field:    "worker_pool_size",
			OldValue: old.Performance.WorkerPoolSize,
			NewValue: new.Performance.WorkerPoolSize,
			Category: "performance",
		})
	}

	if old.Performance.QueueSize != new.Performance.QueueSize {
		changes = append(changes, FieldChange{
			Field:    "queue_size",
			OldValue: old.Performance.QueueSize,
			NewValue: new.Performance.QueueSize,
			Category: "performance",
		})
	}

	if old.Performance.SnapshotInterval != new.Performance.SnapshotInterval {
		changes = append(changes, FieldChange{
			Field:    "snapshot_interval",
			OldValue: old.Performance.SnapshotInterval.String(),
			NewValue: new.Performance.SnapshotInterval.String(),
			Category: "performance",
		})
	}

	if old.Performance.AOFSyncMode != new.Performance.AOFSyncMode {
		changes = append(changes, FieldChange{
			Field:    "aof_sync_mode",
			OldValue: old.Performance.AOFSyncMode,
			NewValue: new.Performance.AOFSyncMode,
			Category: "performance",
		})
	}

	return changes
}

// generateDiffSummary generates a human-readable summary of changes
func generateDiffSummary(diff *ConfigDiff) string {
	if !diff.HasChanges {
		if len(diff.ValidationErrors) > 0 {
			return "No changes detected, but new configuration has validation errors"
		}
		return "No changes detected"
	}

	changeCount := 0
	for _, changes := range diff.Changes {
		changeCount += len(changes)
	}

	summary := fmt.Sprintf("%d field(s) will be updated", changeCount)

	if len(diff.ImmutableChanges) > 0 {
		summary += fmt.Sprintf(", %d immutable field(s) cannot be changed", len(diff.ImmutableChanges))
	}

	if len(diff.ValidationErrors) > 0 {
		summary += fmt.Sprintf(", %d validation error(s)", len(diff.ValidationErrors))
	}

	return summary
}
