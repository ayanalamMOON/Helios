package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigVersion represents a configuration schema version
type ConfigVersion struct {
	Version     int       `yaml:"version" json:"version"`
	Description string    `yaml:"description" json:"description"`
	AppliedAt   time.Time `yaml:"applied_at" json:"applied_at"`
}

// Migration represents a configuration migration
type Migration struct {
	Version     int                                     // Target version after migration
	Description string                                  // Human-readable description
	Up          func(data map[string]interface{}) error // Apply migration
	Down        func(data map[string]interface{}) error // Rollback migration
}

// MigrationResult contains the result of a migration operation
type MigrationResult struct {
	Success           bool     `json:"success"`
	FromVersion       int      `json:"from_version"`
	ToVersion         int      `json:"to_version"`
	AppliedCount      int      `json:"applied_count"`
	BackupPath        string   `json:"backup_path,omitempty"`
	Errors            []string `json:"errors,omitempty"`
	DryRun            bool     `json:"dry_run"`
	MigrationsApplied []int    `json:"migrations_applied,omitempty"`
}

// MigrationStatus provides information about pending migrations
type MigrationStatus struct {
	CurrentVersion    int             `json:"current_version"`
	LatestVersion     int             `json:"latest_version"`
	PendingCount      int             `json:"pending_count"`
	PendingMigrations []MigrationInfo `json:"pending_migrations,omitempty"`
	HasPending        bool            `json:"has_pending"`
}

// MigrationInfo describes a single migration
type MigrationInfo struct {
	Version     int    `json:"version"`
	Description string `json:"description"`
}

// MigrationRegistry holds all registered migrations
type MigrationRegistry struct {
	migrations map[int]*Migration
}

// Global migration registry
var globalRegistry = &MigrationRegistry{
	migrations: make(map[int]*Migration),
}

// RegisterMigration registers a migration in the global registry
func RegisterMigration(m *Migration) error {
	if m.Version <= 0 {
		return fmt.Errorf("invalid migration version: %d (must be > 0)", m.Version)
	}
	if m.Up == nil {
		return fmt.Errorf("migration %d missing Up function", m.Version)
	}
	if m.Down == nil {
		return fmt.Errorf("migration %d missing Down function", m.Version)
	}
	if _, exists := globalRegistry.migrations[m.Version]; exists {
		return fmt.Errorf("migration version %d already registered", m.Version)
	}

	globalRegistry.migrations[m.Version] = m
	return nil
}

// GetMigration retrieves a migration by version
func GetMigration(version int) (*Migration, error) {
	m, exists := globalRegistry.migrations[version]
	if !exists {
		return nil, fmt.Errorf("migration version %d not found", version)
	}
	return m, nil
}

// GetAllMigrations returns all registered migrations sorted by version
func GetAllMigrations() []*Migration {
	versions := make([]int, 0, len(globalRegistry.migrations))
	for v := range globalRegistry.migrations {
		versions = append(versions, v)
	}
	sort.Ints(versions)

	migrations := make([]*Migration, len(versions))
	for i, v := range versions {
		migrations[i] = globalRegistry.migrations[v]
	}
	return migrations
}

// GetLatestVersion returns the highest registered migration version
func GetLatestVersion() int {
	maxVersion := 0
	for v := range globalRegistry.migrations {
		if v > maxVersion {
			maxVersion = v
		}
	}
	return maxVersion
}

// GetCurrentVersion reads the version from the config file
func GetCurrentVersion(configPath string) (int, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read config file: %w", err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return 0, fmt.Errorf("failed to parse config: %w", err)
	}

	// Check if version field exists
	if versionData, ok := raw["version"]; ok {
		switch v := versionData.(type) {
		case int:
			return v, nil
		case float64:
			return int(v), nil
		default:
			return 0, fmt.Errorf("invalid version type: %T", v)
		}
	}

	// No version field means version 0 (legacy config)
	return 0, nil
}

// GetMigrationStatus checks what migrations are pending
func GetMigrationStatus(configPath string) (*MigrationStatus, error) {
	currentVersion, err := GetCurrentVersion(configPath)
	if err != nil {
		return nil, err
	}

	latestVersion := GetLatestVersion()
	pendingMigrations := []MigrationInfo{}

	for v := currentVersion + 1; v <= latestVersion; v++ {
		if m, err := GetMigration(v); err == nil {
			pendingMigrations = append(pendingMigrations, MigrationInfo{
				Version:     m.Version,
				Description: m.Description,
			})
		}
	}

	return &MigrationStatus{
		CurrentVersion:    currentVersion,
		LatestVersion:     latestVersion,
		PendingCount:      len(pendingMigrations),
		PendingMigrations: pendingMigrations,
		HasPending:        len(pendingMigrations) > 0,
	}, nil
}

// ApplyMigrations applies all pending migrations
func ApplyMigrations(configPath string, dryRun bool) (*MigrationResult, error) {
	result := &MigrationResult{
		Success:           true,
		DryRun:            dryRun,
		MigrationsApplied: []int{},
	}

	// Get current version
	currentVersion, err := GetCurrentVersion(configPath)
	if err != nil {
		result.Success = false
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.FromVersion = currentVersion

	// Get latest version
	latestVersion := GetLatestVersion()
	result.ToVersion = latestVersion

	// Check if migrations are needed
	if currentVersion >= latestVersion {
		return result, nil // Already up to date
	}

	// Create backup before migrating
	if !dryRun {
		backupPath, err := createBackup(configPath)
		if err != nil {
			result.Success = false
			result.Errors = append(result.Errors, fmt.Sprintf("backup failed: %v", err))
			return result, err
		}
		result.BackupPath = backupPath
	}

	// Load config data
	data, err := os.ReadFile(configPath)
	if err != nil {
		result.Success = false
		result.Errors = append(result.Errors, fmt.Sprintf("read config: %v", err))
		return result, err
	}

	var configData map[string]interface{}
	if err := yaml.Unmarshal(data, &configData); err != nil {
		result.Success = false
		result.Errors = append(result.Errors, fmt.Sprintf("parse config: %v", err))
		return result, err
	}

	// Apply migrations in order
	for v := currentVersion + 1; v <= latestVersion; v++ {
		migration, err := GetMigration(v)
		if err != nil {
			// Skip missing migrations but continue
			continue
		}

		// Apply migration
		if err := migration.Up(configData); err != nil {
			result.Success = false
			result.Errors = append(result.Errors, fmt.Sprintf("migration %d failed: %v", v, err))

			// Attempt rollback if not dry run
			if !dryRun && result.AppliedCount > 0 {
				rollbackErr := rollbackMigrations(configData, result.MigrationsApplied)
				if rollbackErr != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("rollback failed: %v", rollbackErr))
				}
			}

			return result, err
		}

		result.MigrationsApplied = append(result.MigrationsApplied, v)
		result.AppliedCount++
	}

	// Update version in config
	configData["version"] = latestVersion

	// Update metadata
	if configData["migration_metadata"] == nil {
		configData["migration_metadata"] = make(map[string]interface{})
	}
	metadata := configData["migration_metadata"].(map[string]interface{})
	metadata["last_migration_at"] = time.Now().Format(time.RFC3339)
	metadata["migrations_applied"] = result.MigrationsApplied

	// Save if not dry run
	if !dryRun {
		if err := saveConfig(configPath, configData); err != nil {
			result.Success = false
			result.Errors = append(result.Errors, fmt.Sprintf("save config: %v", err))
			return result, err
		}

		result.ToVersion = latestVersion
	}

	return result, nil
}

// RollbackMigration rolls back to a specific version
func RollbackMigration(configPath string, targetVersion int, dryRun bool) (*MigrationResult, error) {
	result := &MigrationResult{
		Success: true,
		DryRun:  dryRun,
	}

	// Get current version
	currentVersion, err := GetCurrentVersion(configPath)
	if err != nil {
		result.Success = false
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	result.FromVersion = currentVersion
	result.ToVersion = targetVersion

	if targetVersion >= currentVersion {
		return result, fmt.Errorf("target version %d must be less than current version %d", targetVersion, currentVersion)
	}

	if targetVersion < 0 {
		return result, fmt.Errorf("target version %d must be >= 0", targetVersion)
	}

	// Create backup before rolling back
	if !dryRun {
		backupPath, err := createBackup(configPath)
		if err != nil {
			result.Success = false
			result.Errors = append(result.Errors, fmt.Sprintf("backup failed: %v", err))
			return result, err
		}
		result.BackupPath = backupPath
	}

	// Load config data
	data, err := os.ReadFile(configPath)
	if err != nil {
		result.Success = false
		result.Errors = append(result.Errors, fmt.Sprintf("read config: %v", err))
		return result, err
	}

	var configData map[string]interface{}
	if err := yaml.Unmarshal(data, &configData); err != nil {
		result.Success = false
		result.Errors = append(result.Errors, fmt.Sprintf("parse config: %v", err))
		return result, err
	}

	// Apply rollbacks in reverse order
	for v := currentVersion; v > targetVersion; v-- {
		migration, err := GetMigration(v)
		if err != nil {
			// Skip missing migrations
			continue
		}

		if err := migration.Down(configData); err != nil {
			result.Success = false
			result.Errors = append(result.Errors, fmt.Sprintf("rollback %d failed: %v", v, err))
			return result, err
		}

		result.AppliedCount++
	}

	// Update version
	configData["version"] = targetVersion

	// Save if not dry run
	if !dryRun {
		if err := saveConfig(configPath, configData); err != nil {
			result.Success = false
			result.Errors = append(result.Errors, fmt.Sprintf("save config: %v", err))
			return result, err
		}
	}

	return result, nil
}

// createBackup creates a timestamped backup of the config file
func createBackup(configPath string) (string, error) {
	timestamp := time.Now().Format("20060102_150405")
	backupPath := fmt.Sprintf("%s.backup_%s", configPath, timestamp)

	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		return "", err
	}

	return backupPath, nil
}

// saveConfig saves configuration data to file
func saveConfig(configPath string, data map[string]interface{}) error {
	yamlData, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// Write to temp file first
	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, yamlData, 0600); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, configPath); err != nil {
		os.Remove(tmpPath) // Clean up temp file
		return fmt.Errorf("rename config: %w", err)
	}

	return nil
}

// rollbackMigrations attempts to rollback applied migrations
func rollbackMigrations(configData map[string]interface{}, appliedVersions []int) error {
	// Rollback in reverse order
	for i := len(appliedVersions) - 1; i >= 0; i-- {
		v := appliedVersions[i]
		migration, err := GetMigration(v)
		if err != nil {
			return fmt.Errorf("get migration %d for rollback: %w", v, err)
		}

		if err := migration.Down(configData); err != nil {
			return fmt.Errorf("rollback migration %d: %w", v, err)
		}
	}

	return nil
}

// ListBackups lists all backup files for a config
func ListBackups(configPath string) ([]string, error) {
	dir := filepath.Dir(configPath)
	base := filepath.Base(configPath)
	pattern := filepath.Join(dir, base+".backup_*")

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	// Sort by modification time (newest first)
	sort.Slice(matches, func(i, j int) bool {
		infoI, errI := os.Stat(matches[i])
		infoJ, errJ := os.Stat(matches[j])
		if errI != nil || errJ != nil {
			return false
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	return matches, nil
}

// RestoreBackup restores a config from a backup file
func RestoreBackup(backupPath, configPath string) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}
