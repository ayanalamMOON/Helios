package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationRegistry(t *testing.T) {
	// Save and restore global registry state
	originalMigrations := globalRegistry.migrations
	defer func() {
		globalRegistry.migrations = originalMigrations
	}()

	t.Run("RegisterMigration", func(t *testing.T) {
		// Reset registry for test
		globalRegistry.migrations = make(map[int]*Migration)

		migration := &Migration{
			Version:     100,
			Description: "Test migration",
			Up: func(data map[string]interface{}) error {
				data["test_field"] = "applied"
				return nil
			},
			Down: func(data map[string]interface{}) error {
				delete(data, "test_field")
				return nil
			},
		}

		err := RegisterMigration(migration)
		if err != nil {
			t.Fatalf("RegisterMigration failed: %v", err)
		}

		// Verify registered
		m, err := GetMigration(100)
		if err != nil {
			t.Fatalf("GetMigration failed: %v", err)
		}
		if m.Description != "Test migration" {
			t.Errorf("Expected description 'Test migration', got '%s'", m.Description)
		}
	})

	t.Run("DuplicateVersion", func(t *testing.T) {
		globalRegistry.migrations = make(map[int]*Migration)

		migration := &Migration{
			Version:     100,
			Description: "First",
			Up:          func(data map[string]interface{}) error { return nil },
			Down:        func(data map[string]interface{}) error { return nil },
		}

		err := RegisterMigration(migration)
		if err != nil {
			t.Fatalf("First registration failed: %v", err)
		}

		// Try to register same version
		err = RegisterMigration(migration)
		if err == nil {
			t.Error("Expected error for duplicate version, got nil")
		}
	})

	t.Run("InvalidVersion", func(t *testing.T) {
		globalRegistry.migrations = make(map[int]*Migration)

		migration := &Migration{
			Version:     0, // Invalid
			Description: "Invalid",
			Up:          func(data map[string]interface{}) error { return nil },
			Down:        func(data map[string]interface{}) error { return nil },
		}

		err := RegisterMigration(migration)
		if err == nil {
			t.Error("Expected error for invalid version, got nil")
		}
	})

	t.Run("MissingUpFunction", func(t *testing.T) {
		globalRegistry.migrations = make(map[int]*Migration)

		migration := &Migration{
			Version:     100,
			Description: "Missing Up",
			Up:          nil,
			Down:        func(data map[string]interface{}) error { return nil },
		}

		err := RegisterMigration(migration)
		if err == nil {
			t.Error("Expected error for missing Up function, got nil")
		}
	})

	t.Run("MissingDownFunction", func(t *testing.T) {
		globalRegistry.migrations = make(map[int]*Migration)

		migration := &Migration{
			Version:     100,
			Description: "Missing Down",
			Up:          func(data map[string]interface{}) error { return nil },
			Down:        nil,
		}

		err := RegisterMigration(migration)
		if err == nil {
			t.Error("Expected error for missing Down function, got nil")
		}
	})
}

func TestMigrationExecution(t *testing.T) {
	// Create temp directory for test configs
	tmpDir, err := os.MkdirTemp("", "helios-migration-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Save and restore global registry
	originalMigrations := globalRegistry.migrations
	defer func() {
		globalRegistry.migrations = originalMigrations
	}()

	t.Run("ApplyMigrations", func(t *testing.T) {
		// Create test config
		configPath := filepath.Join(tmpDir, "apply_test.yaml")
		configContent := `
immutable:
  node_id: "node-1"
  data_dir: "./data"
  listen_addr: ":6379"
observability:
  log_level: "INFO"
`
		if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		// Setup test migrations
		globalRegistry.migrations = make(map[int]*Migration)
		RegisterMigration(&Migration{
			Version:     1,
			Description: "Add version field",
			Up: func(data map[string]interface{}) error {
				data["migrated_v1"] = true
				return nil
			},
			Down: func(data map[string]interface{}) error {
				delete(data, "migrated_v1")
				return nil
			},
		})
		RegisterMigration(&Migration{
			Version:     2,
			Description: "Add test section",
			Up: func(data map[string]interface{}) error {
				data["migrated_v2"] = true
				return nil
			},
			Down: func(data map[string]interface{}) error {
				delete(data, "migrated_v2")
				return nil
			},
		})

		// Apply migrations
		result, err := ApplyMigrations(configPath, false)
		if err != nil {
			t.Fatalf("ApplyMigrations failed: %v", err)
		}

		if !result.Success {
			t.Errorf("Expected success, got failure: %v", result.Errors)
		}

		if result.FromVersion != 0 {
			t.Errorf("Expected from_version 0, got %d", result.FromVersion)
		}

		if result.ToVersion != 2 {
			t.Errorf("Expected to_version 2, got %d", result.ToVersion)
		}

		if result.AppliedCount != 2 {
			t.Errorf("Expected 2 migrations applied, got %d", result.AppliedCount)
		}

		// Verify backup was created
		if result.BackupPath == "" {
			t.Error("Expected backup path, got empty string")
		}

		// Verify version was updated
		version, err := GetCurrentVersion(configPath)
		if err != nil {
			t.Fatalf("GetCurrentVersion failed: %v", err)
		}
		if version != 2 {
			t.Errorf("Expected version 2, got %d", version)
		}
	})

	t.Run("ApplyMigrationsDryRun", func(t *testing.T) {
		// Create test config
		configPath := filepath.Join(tmpDir, "dryrun_test.yaml")
		configContent := `
immutable:
  node_id: "node-1"
observability:
  log_level: "INFO"
`
		if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		// Setup test migration
		globalRegistry.migrations = make(map[int]*Migration)
		RegisterMigration(&Migration{
			Version:     1,
			Description: "Test migration",
			Up: func(data map[string]interface{}) error {
				data["test"] = true
				return nil
			},
			Down: func(data map[string]interface{}) error {
				delete(data, "test")
				return nil
			},
		})

		// Apply migrations in dry-run mode
		result, err := ApplyMigrations(configPath, true)
		if err != nil {
			t.Fatalf("ApplyMigrations dry-run failed: %v", err)
		}

		if !result.Success {
			t.Errorf("Expected success, got failure")
		}

		if !result.DryRun {
			t.Error("Expected dry_run=true")
		}

		// Verify config was NOT modified (version should still be 0)
		version, err := GetCurrentVersion(configPath)
		if err != nil {
			t.Fatalf("GetCurrentVersion failed: %v", err)
		}
		if version != 0 {
			t.Errorf("Expected version 0 (unchanged), got %d", version)
		}
	})

	t.Run("RollbackMigration", func(t *testing.T) {
		// Create test config at version 2
		configPath := filepath.Join(tmpDir, "rollback_test.yaml")
		configContent := `
version: 2
immutable:
  node_id: "node-1"
observability:
  log_level: "INFO"
migrated_v1: true
migrated_v2: true
`
		if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		// Setup test migrations
		globalRegistry.migrations = make(map[int]*Migration)
		RegisterMigration(&Migration{
			Version:     1,
			Description: "Add v1 field",
			Up: func(data map[string]interface{}) error {
				data["migrated_v1"] = true
				return nil
			},
			Down: func(data map[string]interface{}) error {
				delete(data, "migrated_v1")
				return nil
			},
		})
		RegisterMigration(&Migration{
			Version:     2,
			Description: "Add v2 field",
			Up: func(data map[string]interface{}) error {
				data["migrated_v2"] = true
				return nil
			},
			Down: func(data map[string]interface{}) error {
				delete(data, "migrated_v2")
				return nil
			},
		})

		// Rollback to version 1
		result, err := RollbackMigration(configPath, 1, false)
		if err != nil {
			t.Fatalf("RollbackMigration failed: %v", err)
		}

		if !result.Success {
			t.Errorf("Expected success, got failure: %v", result.Errors)
		}

		if result.FromVersion != 2 {
			t.Errorf("Expected from_version 2, got %d", result.FromVersion)
		}

		if result.ToVersion != 1 {
			t.Errorf("Expected to_version 1, got %d", result.ToVersion)
		}

		// Verify version was updated
		version, err := GetCurrentVersion(configPath)
		if err != nil {
			t.Fatalf("GetCurrentVersion failed: %v", err)
		}
		if version != 1 {
			t.Errorf("Expected version 1, got %d", version)
		}
	})

	t.Run("NoMigrationNeeded", func(t *testing.T) {
		// Create test config already at latest version
		configPath := filepath.Join(tmpDir, "no_migration_test.yaml")
		configContent := `
version: 2
immutable:
  node_id: "node-1"
`
		if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		// Setup test migrations up to version 2
		globalRegistry.migrations = make(map[int]*Migration)
		RegisterMigration(&Migration{
			Version:     1,
			Description: "v1",
			Up:          func(data map[string]interface{}) error { return nil },
			Down:        func(data map[string]interface{}) error { return nil },
		})
		RegisterMigration(&Migration{
			Version:     2,
			Description: "v2",
			Up:          func(data map[string]interface{}) error { return nil },
			Down:        func(data map[string]interface{}) error { return nil },
		})

		// Apply migrations
		result, err := ApplyMigrations(configPath, false)
		if err != nil {
			t.Fatalf("ApplyMigrations failed: %v", err)
		}

		if !result.Success {
			t.Errorf("Expected success, got failure")
		}

		if result.AppliedCount != 0 {
			t.Errorf("Expected 0 migrations applied, got %d", result.AppliedCount)
		}
	})
}

func TestMigrationStatus(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "helios-migration-status-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Save and restore global registry
	originalMigrations := globalRegistry.migrations
	defer func() {
		globalRegistry.migrations = originalMigrations
	}()

	t.Run("StatusWithPendingMigrations", func(t *testing.T) {
		// Create test config at version 1
		configPath := filepath.Join(tmpDir, "status_test.yaml")
		configContent := `
version: 1
immutable:
  node_id: "node-1"
`
		if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		// Setup test migrations
		globalRegistry.migrations = make(map[int]*Migration)
		RegisterMigration(&Migration{
			Version:     1,
			Description: "Version 1",
			Up:          func(data map[string]interface{}) error { return nil },
			Down:        func(data map[string]interface{}) error { return nil },
		})
		RegisterMigration(&Migration{
			Version:     2,
			Description: "Version 2",
			Up:          func(data map[string]interface{}) error { return nil },
			Down:        func(data map[string]interface{}) error { return nil },
		})
		RegisterMigration(&Migration{
			Version:     3,
			Description: "Version 3",
			Up:          func(data map[string]interface{}) error { return nil },
			Down:        func(data map[string]interface{}) error { return nil },
		})

		status, err := GetMigrationStatus(configPath)
		if err != nil {
			t.Fatalf("GetMigrationStatus failed: %v", err)
		}

		if status.CurrentVersion != 1 {
			t.Errorf("Expected current version 1, got %d", status.CurrentVersion)
		}

		if status.LatestVersion != 3 {
			t.Errorf("Expected latest version 3, got %d", status.LatestVersion)
		}

		if status.PendingCount != 2 {
			t.Errorf("Expected 2 pending, got %d", status.PendingCount)
		}

		if !status.HasPending {
			t.Error("Expected HasPending=true")
		}

		if len(status.PendingMigrations) != 2 {
			t.Errorf("Expected 2 pending migrations, got %d", len(status.PendingMigrations))
		}
	})

	t.Run("StatusNoPendingMigrations", func(t *testing.T) {
		// Create test config at latest version
		configPath := filepath.Join(tmpDir, "status_current_test.yaml")
		configContent := `
version: 2
immutable:
  node_id: "node-1"
`
		if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		// Setup test migrations
		globalRegistry.migrations = make(map[int]*Migration)
		RegisterMigration(&Migration{
			Version:     1,
			Description: "Version 1",
			Up:          func(data map[string]interface{}) error { return nil },
			Down:        func(data map[string]interface{}) error { return nil },
		})
		RegisterMigration(&Migration{
			Version:     2,
			Description: "Version 2",
			Up:          func(data map[string]interface{}) error { return nil },
			Down:        func(data map[string]interface{}) error { return nil },
		})

		status, err := GetMigrationStatus(configPath)
		if err != nil {
			t.Fatalf("GetMigrationStatus failed: %v", err)
		}

		if status.PendingCount != 0 {
			t.Errorf("Expected 0 pending, got %d", status.PendingCount)
		}

		if status.HasPending {
			t.Error("Expected HasPending=false")
		}
	})
}

func TestBackupAndRestore(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "helios-backup-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("CreateBackup", func(t *testing.T) {
		configPath := filepath.Join(tmpDir, "backup_test.yaml")
		originalContent := `
version: 1
test: original
`
		if err := os.WriteFile(configPath, []byte(originalContent), 0600); err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		backupPath, err := createBackup(configPath)
		if err != nil {
			t.Fatalf("createBackup failed: %v", err)
		}

		// Verify backup exists
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			t.Error("Backup file was not created")
		}

		// Verify backup content matches original
		backupContent, err := os.ReadFile(backupPath)
		if err != nil {
			t.Fatalf("Failed to read backup: %v", err)
		}
		if string(backupContent) != originalContent {
			t.Error("Backup content doesn't match original")
		}
	})

	t.Run("ListBackups", func(t *testing.T) {
		// Create a unique subdirectory for this test
		listDir, err := os.MkdirTemp(tmpDir, "list_backups")
		if err != nil {
			t.Fatalf("Failed to create list backup dir: %v", err)
		}

		configPath := filepath.Join(listDir, "list_backup_test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true\n"), 0600); err != nil {
			t.Fatalf("Failed to write test config: %v", err)
		}

		// Create multiple backups with unique timestamps
		createdBackups := []string{}
		for i := 0; i < 3; i++ {
			backupPath, err := createBackup(configPath)
			if err != nil {
				t.Fatalf("createBackup %d failed: %v", i, err)
			}
			createdBackups = append(createdBackups, backupPath)
			// Small delay to ensure unique timestamps
			if i < 2 {
				// Rename to ensure unique backup files
				newPath := backupPath + fmt.Sprintf("_%d", i)
				os.Rename(backupPath, newPath)
				createdBackups[i] = newPath
			}
		}

		backups, err := ListBackups(configPath)
		if err != nil {
			t.Fatalf("ListBackups failed: %v", err)
		}

		// Should have at least 1 backup (last one created)
		if len(backups) < 1 {
			t.Errorf("Expected at least 1 backup, got %d", len(backups))
		}
	})

	t.Run("RestoreBackup", func(t *testing.T) {
		configPath := filepath.Join(tmpDir, "restore_test.yaml")
		originalContent := "version: 1\ntest: original\n"
		modifiedContent := "version: 2\ntest: modified\n"

		// Write original
		if err := os.WriteFile(configPath, []byte(originalContent), 0600); err != nil {
			t.Fatalf("Failed to write original config: %v", err)
		}

		// Create backup
		backupPath, err := createBackup(configPath)
		if err != nil {
			t.Fatalf("createBackup failed: %v", err)
		}

		// Modify config
		if err := os.WriteFile(configPath, []byte(modifiedContent), 0600); err != nil {
			t.Fatalf("Failed to write modified config: %v", err)
		}

		// Restore from backup
		if err := RestoreBackup(backupPath, configPath); err != nil {
			t.Fatalf("RestoreBackup failed: %v", err)
		}

		// Verify restored content
		restoredContent, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("Failed to read restored config: %v", err)
		}
		if string(restoredContent) != originalContent {
			t.Errorf("Restored content doesn't match original: got %s, want %s", restoredContent, originalContent)
		}
	})
}

func TestMigrationHelpers(t *testing.T) {
	t.Run("AddFieldWithDefault", func(t *testing.T) {
		section := map[string]interface{}{}

		AddFieldWithDefault(section, "new_field", "default_value")

		if section["new_field"] != "default_value" {
			t.Errorf("Expected 'default_value', got '%v'", section["new_field"])
		}

		// Should not overwrite existing
		section["existing"] = "original"
		AddFieldWithDefault(section, "existing", "new_value")

		if section["existing"] != "original" {
			t.Errorf("Expected 'original', got '%v'", section["existing"])
		}
	})

	t.Run("RenameField", func(t *testing.T) {
		section := map[string]interface{}{
			"old_name": "value",
		}

		RenameField(section, "old_name", "new_name")

		if _, exists := section["old_name"]; exists {
			t.Error("Old field should be deleted")
		}

		if section["new_name"] != "value" {
			t.Errorf("Expected 'value', got '%v'", section["new_name"])
		}
	})

	t.Run("MoveField", func(t *testing.T) {
		from := map[string]interface{}{
			"field_to_move": "value",
			"other_field":   "stays",
		}
		to := map[string]interface{}{}

		MoveField(from, to, "field_to_move")

		if _, exists := from["field_to_move"]; exists {
			t.Error("Field should be removed from source")
		}

		if to["field_to_move"] != "value" {
			t.Errorf("Expected 'value' in destination, got '%v'", to["field_to_move"])
		}

		if from["other_field"] != "stays" {
			t.Error("Other fields should remain")
		}
	})
}

func TestBuiltInMigrations(t *testing.T) {
	// This test verifies that built-in migrations are properly registered
	t.Run("BuiltInMigrationsRegistered", func(t *testing.T) {
		// Should have built-in migrations registered (from init())
		migrations := GetAllMigrations()

		if len(migrations) == 0 {
			t.Error("Expected built-in migrations to be registered")
		}

		// Verify migrations 1-5 exist
		for v := 1; v <= 5; v++ {
			m, err := GetMigration(v)
			if err != nil {
				t.Errorf("Migration %d not found: %v", v, err)
				continue
			}
			if m.Description == "" {
				t.Errorf("Migration %d has empty description", v)
			}
		}
	})

	t.Run("LatestVersionIsCorrect", func(t *testing.T) {
		latest := GetLatestVersion()
		if latest < 5 {
			t.Errorf("Expected latest version >= 5, got %d", latest)
		}
	})
}
