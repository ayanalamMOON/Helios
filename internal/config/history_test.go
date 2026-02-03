package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryManager(t *testing.T) {
	t.Run("NewHistoryManager", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")

		// Create a dummy config file
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		// Check history directory was created
		historyDir := filepath.Join(tmpDir, ".helios", "history")
		if _, err := os.Stat(historyDir); os.IsNotExist(err) {
			t.Error("History directory not created")
		}

		if hm.GetEntryCount() != 0 {
			t.Errorf("Expected 0 entries, got %d", hm.GetEntryCount())
		}
	})

	t.Run("RecordChange", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		oldConfig := &Config{
			Observability: ObservabilityConfig{LogLevel: "info"},
		}
		newConfig := &Config{
			Observability: ObservabilityConfig{LogLevel: "debug"},
		}

		entry, err := hm.RecordChange(
			oldConfig, newConfig,
			ChangeTypeReload,
			SourceAPI,
			"testuser",
			"Test change",
			[]string{"test"},
		)
		if err != nil {
			t.Fatalf("RecordChange failed: %v", err)
		}

		if entry.Version != 1 {
			t.Errorf("Expected version 1, got %d", entry.Version)
		}
		if entry.ChangeType != ChangeTypeReload {
			t.Errorf("Expected change type reload, got %s", entry.ChangeType)
		}
		if entry.User != "testuser" {
			t.Errorf("Expected user testuser, got %s", entry.User)
		}
		if len(entry.Changes) == 0 {
			t.Error("Expected changes to be recorded")
		}
		if hm.GetEntryCount() != 1 {
			t.Errorf("Expected 1 entry, got %d", hm.GetEntryCount())
		}
	})

	t.Run("GetHistory", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		// Record multiple changes
		for i := 0; i < 5; i++ {
			oldConfig := &Config{
				Observability: ObservabilityConfig{LogLevel: "info"},
			}
			newConfig := &Config{
				Observability: ObservabilityConfig{LogLevel: "debug"},
			}
			_, err := hm.RecordChange(
				oldConfig, newConfig,
				ChangeTypeReload,
				SourceAPI,
				"testuser",
				"Change",
				nil,
			)
			if err != nil {
				t.Fatalf("RecordChange failed: %v", err)
			}
		}

		// Get all history
		history, err := hm.GetHistory(nil)
		if err != nil {
			t.Fatalf("GetHistory failed: %v", err)
		}
		if history.Total != 5 {
			t.Errorf("Expected 5 entries, got %d", history.Total)
		}

		// Test limit
		history, err = hm.GetHistory(&HistoryQuery{Limit: 3})
		if err != nil {
			t.Fatalf("GetHistory with limit failed: %v", err)
		}
		if len(history.Entries) != 3 {
			t.Errorf("Expected 3 entries, got %d", len(history.Entries))
		}
		if !history.HasMore {
			t.Error("Expected HasMore to be true")
		}

		// Test offset
		history, err = hm.GetHistory(&HistoryQuery{Offset: 3, Limit: 10})
		if err != nil {
			t.Fatalf("GetHistory with offset failed: %v", err)
		}
		if len(history.Entries) != 2 {
			t.Errorf("Expected 2 entries, got %d", len(history.Entries))
		}
	})

	t.Run("GetEntry", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		oldConfig := &Config{
			Observability: ObservabilityConfig{LogLevel: "info"},
		}
		newConfig := &Config{
			Observability: ObservabilityConfig{LogLevel: "debug"},
		}

		entry, _ := hm.RecordChange(oldConfig, newConfig, ChangeTypeReload, SourceAPI, "", "", nil)

		// Get by version
		retrieved, err := hm.GetEntry("1")
		if err != nil {
			t.Fatalf("GetEntry by version failed: %v", err)
		}
		if retrieved.ID != entry.ID {
			t.Errorf("IDs don't match")
		}

		// Get by ID
		retrieved, err = hm.GetEntry(entry.ID)
		if err != nil {
			t.Fatalf("GetEntry by ID failed: %v", err)
		}
		if retrieved.Version != entry.Version {
			t.Errorf("Versions don't match")
		}

		// Get non-existent
		_, err = hm.GetEntry("9999")
		if err == nil {
			t.Error("Expected error for non-existent entry")
		}
	})

	t.Run("Compare", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		// Version 1
		config1 := &Config{
			Observability: ObservabilityConfig{LogLevel: "info"},
		}
		hm.RecordChange(nil, config1, ChangeTypeInitial, SourceStartup, "", "", nil)

		// Version 2
		config2 := &Config{
			Observability: ObservabilityConfig{LogLevel: "debug"},
		}
		hm.RecordChange(config1, config2, ChangeTypeReload, SourceAPI, "", "", nil)

		comparison, err := hm.Compare(1, 2)
		if err != nil {
			t.Fatalf("Compare failed: %v", err)
		}

		if comparison.VersionA != 1 || comparison.VersionB != 2 {
			t.Errorf("Versions don't match")
		}
		if comparison.TotalChanges == 0 {
			t.Error("Expected changes to be detected")
		}
	})

	t.Run("GetStats", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		// Record various changes
		config := &Config{Observability: ObservabilityConfig{LogLevel: "info"}}
		hm.RecordChange(nil, config, ChangeTypeInitial, SourceStartup, "", "", nil)
		hm.RecordChange(config, config, ChangeTypeReload, SourceAPI, "", "", nil)
		hm.RecordChange(config, config, ChangeTypeReload, SourceFileWatch, "", "", nil)

		stats, err := hm.GetStats()
		if err != nil {
			t.Fatalf("GetStats failed: %v", err)
		}

		if stats.TotalEntries != 3 {
			t.Errorf("Expected 3 entries, got %d", stats.TotalEntries)
		}
		if stats.ChangesByType[ChangeTypeInitial] != 1 {
			t.Error("Expected 1 initial change")
		}
		if stats.ChangesByType[ChangeTypeReload] != 2 {
			t.Error("Expected 2 reload changes")
		}
		if stats.ChangesBySource[SourceStartup] != 1 {
			t.Error("Expected 1 startup source")
		}
	})

	t.Run("SnapshotSaveAndLoad", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, &HistoryConfig{
			HistoryDir:   ".helios/history",
			AutoSnapshot: true,
		})
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		config := &Config{
			Observability: ObservabilityConfig{
				LogLevel:       "debug",
				MetricsEnabled: true,
				MetricsPort:    "9090",
			},
		}

		entry, err := hm.RecordChange(nil, config, ChangeTypeInitial, SourceStartup, "", "", nil)
		if err != nil {
			t.Fatalf("RecordChange failed: %v", err)
		}

		// Snapshot should be saved
		if entry.Snapshot == nil {
			t.Error("Expected snapshot to be present")
		}

		// Load snapshot
		snapshot, err := hm.GetSnapshot(1)
		if err != nil {
			t.Fatalf("GetSnapshot failed: %v", err)
		}

		if snapshot == nil {
			t.Error("Expected snapshot to be loaded")
		}

		// Check snapshot contains expected data
		if obs, ok := snapshot["observability"].(map[string]interface{}); ok {
			if obs["log_level"] != "debug" {
				t.Error("Snapshot log_level incorrect")
			}
		} else {
			t.Error("Snapshot missing observability section")
		}
	})

	t.Run("RestoreVersion", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		config := &Config{
			Observability: ObservabilityConfig{LogLevel: "info"},
		}
		hm.RecordChange(nil, config, ChangeTypeInitial, SourceStartup, "", "", nil)

		// Restore to version 1
		err = hm.RestoreVersion(1, configPath)
		if err != nil {
			t.Fatalf("RestoreVersion failed: %v", err)
		}

		// Check file was restored
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("Failed to read restored config: %v", err)
		}
		if len(data) == 0 {
			t.Error("Restored config is empty")
		}
	})

	t.Run("PurgeOldEntries", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		config := &Config{Observability: ObservabilityConfig{LogLevel: "info"}}
		hm.RecordChange(nil, config, ChangeTypeInitial, SourceStartup, "", "", nil)
		hm.RecordChange(config, config, ChangeTypeReload, SourceAPI, "", "", nil)

		// Purge entries older than 1 hour (none should be purged)
		purged, err := hm.PurgeOldEntries(time.Hour)
		if err != nil {
			t.Fatalf("PurgeOldEntries failed: %v", err)
		}
		if purged != 0 {
			t.Errorf("Expected 0 purged, got %d", purged)
		}
		if hm.GetEntryCount() != 2 {
			t.Errorf("Expected 2 entries, got %d", hm.GetEntryCount())
		}
	})

	t.Run("Clear", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		config := &Config{Observability: ObservabilityConfig{LogLevel: "info"}}
		hm.RecordChange(nil, config, ChangeTypeInitial, SourceStartup, "", "", nil)
		hm.RecordChange(config, config, ChangeTypeReload, SourceAPI, "", "", nil)

		if hm.GetEntryCount() != 2 {
			t.Errorf("Expected 2 entries, got %d", hm.GetEntryCount())
		}

		err = hm.Clear()
		if err != nil {
			t.Fatalf("Clear failed: %v", err)
		}

		if hm.GetEntryCount() != 0 {
			t.Errorf("Expected 0 entries after clear, got %d", hm.GetEntryCount())
		}
	})

	t.Run("MaxEntriesLimit", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, &HistoryConfig{
			HistoryDir:   ".helios/history",
			MaxEntries:   3,
			AutoSnapshot: true,
		})
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		config := &Config{Observability: ObservabilityConfig{LogLevel: "info"}}

		// Add 5 entries
		for i := 0; i < 5; i++ {
			hm.RecordChange(nil, config, ChangeTypeReload, SourceAPI, "", "", nil)
		}

		// Should only have 3 entries (max limit)
		if hm.GetEntryCount() != 3 {
			t.Errorf("Expected 3 entries (max), got %d", hm.GetEntryCount())
		}
	})

	t.Run("FilterByChangeType", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		config := &Config{Observability: ObservabilityConfig{LogLevel: "info"}}
		hm.RecordChange(nil, config, ChangeTypeInitial, SourceStartup, "", "", nil)
		hm.RecordChange(config, config, ChangeTypeReload, SourceAPI, "", "", nil)
		hm.RecordChange(config, config, ChangeTypeMigration, SourceMigration, "", "", nil)

		// Filter by reload type
		history, err := hm.GetHistory(&HistoryQuery{ChangeType: ChangeTypeReload})
		if err != nil {
			t.Fatalf("GetHistory failed: %v", err)
		}
		if history.Total != 1 {
			t.Errorf("Expected 1 reload entry, got %d", history.Total)
		}
	})

	t.Run("FilterByTags", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		config := &Config{Observability: ObservabilityConfig{LogLevel: "info"}}
		hm.RecordChange(nil, config, ChangeTypeInitial, SourceStartup, "", "", []string{"initial"})
		hm.RecordChange(config, config, ChangeTypeReload, SourceAPI, "", "", []string{"manual"})
		hm.RecordChange(config, config, ChangeTypeReload, SourceFileWatch, "", "", []string{"auto"})

		// Filter by tag
		history, err := hm.GetHistory(&HistoryQuery{Tags: []string{"manual"}})
		if err != nil {
			t.Fatalf("GetHistory failed: %v", err)
		}
		if history.Total != 1 {
			t.Errorf("Expected 1 entry with 'manual' tag, got %d", history.Total)
		}
	})
}

func TestHistoryPersistence(t *testing.T) {
	t.Run("PersistAndReload", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		// Create first manager and add entries
		hm1, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager 1 failed: %v", err)
		}

		config := &Config{Observability: ObservabilityConfig{LogLevel: "info"}}
		hm1.RecordChange(nil, config, ChangeTypeInitial, SourceStartup, "user1", "Initial", []string{"test"})
		hm1.RecordChange(config, config, ChangeTypeReload, SourceAPI, "user2", "Reload", nil)

		if hm1.GetEntryCount() != 2 {
			t.Errorf("Expected 2 entries, got %d", hm1.GetEntryCount())
		}

		// Create second manager - should load existing entries
		hm2, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager 2 failed: %v", err)
		}

		if hm2.GetEntryCount() != 2 {
			t.Errorf("Expected 2 entries after reload, got %d", hm2.GetEntryCount())
		}

		// Verify entry details
		entry, err := hm2.GetEntry("1")
		if err != nil {
			t.Fatalf("GetEntry failed: %v", err)
		}
		if entry.User != "user1" {
			t.Errorf("Expected user1, got %s", entry.User)
		}
		if entry.Description != "Initial" {
			t.Errorf("Expected 'Initial', got %s", entry.Description)
		}
	})
}

func TestHistoryCompareSnapshots(t *testing.T) {
	t.Run("DetectAddedFields", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "test.yaml")
		if err := os.WriteFile(configPath, []byte("test: true"), 0600); err != nil {
			t.Fatalf("Failed to create config file: %v", err)
		}

		hm, err := NewHistoryManager(configPath, nil)
		if err != nil {
			t.Fatalf("NewHistoryManager failed: %v", err)
		}

		config1 := &Config{
			Observability: ObservabilityConfig{
				LogLevel: "info",
			},
		}
		hm.RecordChange(nil, config1, ChangeTypeInitial, SourceStartup, "", "", nil)

		config2 := &Config{
			Observability: ObservabilityConfig{
				LogLevel:       "info",
				MetricsEnabled: true,
				MetricsPort:    "9090",
			},
		}
		hm.RecordChange(config1, config2, ChangeTypeReload, SourceAPI, "", "", nil)

		comparison, err := hm.Compare(1, 2)
		if err != nil {
			t.Fatalf("Compare failed: %v", err)
		}

		if len(comparison.ModifiedFields) == 0 && len(comparison.AddedFields) == 0 {
			t.Error("Expected some field changes to be detected")
		}
	})
}

func TestHistoryChangeTypes(t *testing.T) {
	types := []HistoryChangeType{
		ChangeTypeReload,
		ChangeTypeMigration,
		ChangeTypeRollback,
		ChangeTypeRestore,
		ChangeTypeManual,
		ChangeTypeInitial,
		ChangeTypeFileWatch,
	}

	for _, ct := range types {
		if ct == "" {
			t.Errorf("Change type should not be empty")
		}
	}
}

func TestHistorySources(t *testing.T) {
	sources := []string{
		SourceAPI,
		SourceCLI,
		SourceFileWatch,
		SourceSignal,
		SourceStartup,
		SourceMigration,
	}

	for _, src := range sources {
		if src == "" {
			t.Errorf("Source should not be empty")
		}
	}
}
