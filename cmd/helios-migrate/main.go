package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/helios/helios/internal/config"
)

const usage = `Helios Configuration Migration Tool

Usage:
  helios-migrate [command] [options]

Commands:
  status       Show migration status
  apply        Apply pending migrations
  rollback     Rollback to a specific version
  list         List all available migrations
  backups      List configuration backups
  restore      Restore from a backup
  history      View configuration change history
  history-stats  Show history statistics
  history-compare  Compare two history versions
  history-restore  Restore to a history version

Options:
  -config string    Path to configuration file (default "config.yaml")
  -version int      Target version for rollback/history-restore
  -dry-run          Preview changes without applying
  -backup string    Backup file path (required for restore)
  -json             Output in JSON format
  -limit int        Limit number of history entries (default 20)
  -from int         Start version for comparison
  -to int           End version for comparison

Examples:
  # Check migration status
  helios-migrate status -config config.yaml

  # Apply all pending migrations (dry-run first)
  helios-migrate apply -config config.yaml -dry-run
  helios-migrate apply -config config.yaml

  # Rollback to version 2
  helios-migrate rollback -config config.yaml -version 2

  # List all migrations
  helios-migrate list

  # List and restore backups
  helios-migrate backups -config config.yaml
  helios-migrate restore -config config.yaml -backup config.yaml.backup_20260204_153045

  # View configuration change history
  helios-migrate history -config config.yaml
  helios-migrate history -config config.yaml -limit 10

  # View history statistics
  helios-migrate history-stats -config config.yaml

  # Compare two history versions
  helios-migrate history-compare -config config.yaml -from 1 -to 3

  # Restore to a specific history version
  helios-migrate history-restore -config config.yaml -version 2
`

func main() {
	// Parse flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	targetVersion := flag.Int("version", -1, "Target version for rollback or history-restore")
	dryRun := flag.Bool("dry-run", false, "Preview changes without applying")
	backupPath := flag.String("backup", "", "Backup file path for restore")
	jsonOutput := flag.Bool("json", false, "Output in JSON format")
	limit := flag.Int("limit", 20, "Limit number of history entries")
	fromVersion := flag.Int("from", -1, "Start version for comparison")
	toVersion := flag.Int("to", -1, "End version for comparison")

	flag.Usage = func() {
		fmt.Print(usage)
	}

	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	command := args[0]

	switch command {
	case "status":
		cmdStatus(*configPath, *jsonOutput)
	case "apply":
		cmdApply(*configPath, *dryRun, *jsonOutput)
	case "rollback":
		cmdRollback(*configPath, *targetVersion, *dryRun, *jsonOutput)
	case "list":
		cmdList(*jsonOutput)
	case "backups":
		cmdBackups(*configPath, *jsonOutput)
	case "restore":
		cmdRestore(*configPath, *backupPath)
	case "history":
		cmdHistory(*configPath, *limit, *jsonOutput)
	case "history-stats":
		cmdHistoryStats(*configPath, *jsonOutput)
	case "history-compare":
		cmdHistoryCompare(*configPath, *fromVersion, *toVersion, *jsonOutput)
	case "history-restore":
		cmdHistoryRestore(*configPath, *targetVersion)
	case "help":
		flag.Usage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		flag.Usage()
		os.Exit(1)
	}
}

func cmdStatus(configPath string, jsonOutput bool) {
	status, err := config.GetMigrationStatus(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(status)
		return
	}

	fmt.Println("Configuration Migration Status")
	fmt.Println("==============================")
	fmt.Printf("Config File:      %s\n", configPath)
	fmt.Printf("Current Version:  %d\n", status.CurrentVersion)
	fmt.Printf("Latest Version:   %d\n", status.LatestVersion)
	fmt.Printf("Pending Count:    %d\n", status.PendingCount)

	if status.HasPending {
		fmt.Println("\nPending Migrations:")
		for _, m := range status.PendingMigrations {
			fmt.Printf("  - v%d: %s\n", m.Version, m.Description)
		}
		fmt.Println("\nRun 'helios-migrate apply' to apply pending migrations.")
	} else {
		fmt.Println("\n✓ Configuration is up to date!")
	}
}

func cmdApply(configPath string, dryRun, jsonOutput bool) {
	if dryRun {
		fmt.Println("Performing dry-run (no changes will be made)...")
	}

	result, err := config.ApplyMigrations(configPath, dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if result != nil && jsonOutput {
			printJSON(result)
		}
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(result)
		return
	}

	fmt.Println("Migration Results")
	fmt.Println("=================")
	fmt.Printf("Success:         %v\n", result.Success)
	fmt.Printf("From Version:    %d\n", result.FromVersion)
	fmt.Printf("To Version:      %d\n", result.ToVersion)
	fmt.Printf("Migrations:      %d\n", result.AppliedCount)

	if result.BackupPath != "" {
		fmt.Printf("Backup Created:  %s\n", result.BackupPath)
	}

	if len(result.MigrationsApplied) > 0 {
		fmt.Println("\nApplied Migrations:")
		for _, v := range result.MigrationsApplied {
			m, _ := config.GetMigration(v)
			if m != nil {
				fmt.Printf("  - v%d: %s\n", v, m.Description)
			} else {
				fmt.Printf("  - v%d\n", v)
			}
		}
	}

	if len(result.Errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range result.Errors {
			fmt.Printf("  - %s\n", e)
		}
	}

	if result.AppliedCount == 0 {
		fmt.Println("\n✓ No migrations needed. Configuration is up to date!")
	} else if dryRun {
		fmt.Println("\nDry-run complete. Run without -dry-run to apply changes.")
	} else {
		fmt.Println("\n✓ Migrations applied successfully!")
	}
}

func cmdRollback(configPath string, targetVersion int, dryRun, jsonOutput bool) {
	if targetVersion < 0 {
		fmt.Fprintf(os.Stderr, "Error: -version is required for rollback\n")
		os.Exit(1)
	}

	if dryRun {
		fmt.Println("Performing dry-run (no changes will be made)...")
	}

	result, err := config.RollbackMigration(configPath, targetVersion, dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if result != nil && jsonOutput {
			printJSON(result)
		}
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(result)
		return
	}

	fmt.Println("Rollback Results")
	fmt.Println("================")
	fmt.Printf("Success:         %v\n", result.Success)
	fmt.Printf("From Version:    %d\n", result.FromVersion)
	fmt.Printf("To Version:      %d\n", result.ToVersion)
	fmt.Printf("Rollbacks:       %d\n", result.AppliedCount)

	if result.BackupPath != "" {
		fmt.Printf("Backup Created:  %s\n", result.BackupPath)
	}

	if len(result.Errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range result.Errors {
			fmt.Printf("  - %s\n", e)
		}
	}

	if dryRun {
		fmt.Println("\nDry-run complete. Run without -dry-run to apply rollback.")
	} else if result.Success {
		fmt.Println("\n✓ Rollback completed successfully!")
	}
}

func cmdList(jsonOutput bool) {
	migrations := config.GetAllMigrations()

	if jsonOutput {
		migrationList := make([]map[string]interface{}, len(migrations))
		for i, m := range migrations {
			migrationList[i] = map[string]interface{}{
				"version":     m.Version,
				"description": m.Description,
			}
		}
		printJSON(map[string]interface{}{
			"migrations":     migrationList,
			"total":          len(migrations),
			"latest_version": config.GetLatestVersion(),
		})
		return
	}

	fmt.Println("Available Migrations")
	fmt.Println("====================")
	fmt.Printf("Latest Version: %d\n\n", config.GetLatestVersion())

	if len(migrations) == 0 {
		fmt.Println("No migrations registered.")
		return
	}

	for _, m := range migrations {
		fmt.Printf("v%d: %s\n", m.Version, m.Description)
	}
}

func cmdBackups(configPath string, jsonOutput bool) {
	backups, err := config.ListBackups(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(map[string]interface{}{
			"config_path": configPath,
			"backups":     backups,
			"count":       len(backups),
		})
		return
	}

	fmt.Println("Configuration Backups")
	fmt.Println("=====================")
	fmt.Printf("Config File: %s\n\n", configPath)

	if len(backups) == 0 {
		fmt.Println("No backups found.")
		return
	}

	fmt.Printf("Found %d backup(s):\n", len(backups))
	for i, b := range backups {
		fmt.Printf("  %d. %s\n", i+1, b)
	}

	fmt.Println("\nTo restore: helios-migrate restore -config", configPath, "-backup <backup_path>")
}

func cmdRestore(configPath, backupPath string) {
	if backupPath == "" {
		fmt.Fprintf(os.Stderr, "Error: -backup is required for restore\n")
		os.Exit(1)
	}

	fmt.Printf("Restoring %s from %s...\n", configPath, backupPath)

	if err := config.RestoreBackup(backupPath, configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ Configuration restored successfully!")
}

func printJSON(data interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

// History commands

func cmdHistory(configPath string, limit int, jsonOutput bool) {
	// Create manager to access history
	manager, err := config.NewManager(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if !manager.IsHistoryEnabled() {
		fmt.Fprintf(os.Stderr, "Error: History tracking is not enabled\n")
		os.Exit(1)
	}

	query := &config.HistoryQuery{Limit: limit}
	history, err := manager.GetHistory(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(history)
		return
	}

	fmt.Println("Configuration Change History")
	fmt.Println("============================")
	fmt.Printf("Total Entries: %d (showing %d)\n\n", history.Total, len(history.Entries))

	if len(history.Entries) == 0 {
		fmt.Println("No history entries found.")
		return
	}

	for _, entry := range history.Entries {
		fmt.Printf("Version %d [%s]\n", entry.Version, entry.ID[:8])
		fmt.Printf("  Timestamp:   %s\n", entry.Timestamp.Format("2006-01-02 15:04:05"))
		fmt.Printf("  Change Type: %s\n", entry.ChangeType)
		fmt.Printf("  Source:      %s\n", entry.Source)
		if entry.User != "" {
			fmt.Printf("  User:        %s\n", entry.User)
		}
		if entry.Description != "" {
			fmt.Printf("  Description: %s\n", entry.Description)
		}
		if len(entry.Changes) > 0 {
			fmt.Printf("  Changes:     %d field(s) modified\n", len(entry.Changes))
		}
		fmt.Println()
	}

	if history.HasMore {
		fmt.Printf("... and %d more entries. Use -limit to see more.\n", history.Total-len(history.Entries))
	}
}

func cmdHistoryStats(configPath string, jsonOutput bool) {
	manager, err := config.NewManager(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if !manager.IsHistoryEnabled() {
		fmt.Fprintf(os.Stderr, "Error: History tracking is not enabled\n")
		os.Exit(1)
	}

	stats, err := manager.GetHistoryStats()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(stats)
		return
	}

	fmt.Println("Configuration History Statistics")
	fmt.Println("================================")
	fmt.Printf("Total Entries:    %d\n", stats.TotalEntries)
	fmt.Printf("Current Version:  %d\n", stats.CurrentVersion)
	if stats.OldestEntry != nil {
		fmt.Printf("Oldest Entry:     %s\n", stats.OldestEntry.Format("2006-01-02 15:04:05"))
	}
	if stats.NewestEntry != nil {
		fmt.Printf("Newest Entry:     %s\n", stats.NewestEntry.Format("2006-01-02 15:04:05"))
	}
	if stats.AverageInterval != "" {
		fmt.Printf("Avg Interval:     %s\n", stats.AverageInterval)
	}
	fmt.Printf("Storage Size:     %d bytes\n", stats.StorageSize)

	if len(stats.ChangesByType) > 0 {
		fmt.Println("\nChanges by Type:")
		for ct, count := range stats.ChangesByType {
			fmt.Printf("  %-12s: %d\n", ct, count)
		}
	}

	if len(stats.ChangesBySource) > 0 {
		fmt.Println("\nChanges by Source:")
		for src, count := range stats.ChangesBySource {
			fmt.Printf("  %-12s: %d\n", src, count)
		}
	}
}

func cmdHistoryCompare(configPath string, fromVersion, toVersion int, jsonOutput bool) {
	if fromVersion < 1 || toVersion < 1 {
		fmt.Fprintf(os.Stderr, "Error: -from and -to are required and must be positive\n")
		os.Exit(1)
	}

	manager, err := config.NewManager(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if !manager.IsHistoryEnabled() {
		fmt.Fprintf(os.Stderr, "Error: History tracking is not enabled\n")
		os.Exit(1)
	}

	comparison, err := manager.CompareHistoryVersions(fromVersion, toVersion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonOutput {
		printJSON(comparison)
		return
	}

	fmt.Println("Configuration Version Comparison")
	fmt.Println("=================================")
	fmt.Printf("Version %d (%s) → Version %d (%s)\n\n",
		comparison.VersionA, comparison.TimestampA.Format("2006-01-02 15:04:05"),
		comparison.VersionB, comparison.TimestampB.Format("2006-01-02 15:04:05"))

	fmt.Printf("Total Changes: %d\n", comparison.TotalChanges)

	if len(comparison.AddedFields) > 0 {
		fmt.Println("\nAdded Fields:")
		for _, f := range comparison.AddedFields {
			fmt.Printf("  + %s\n", f)
		}
	}

	if len(comparison.RemovedFields) > 0 {
		fmt.Println("\nRemoved Fields:")
		for _, f := range comparison.RemovedFields {
			fmt.Printf("  - %s\n", f)
		}
	}

	if len(comparison.ModifiedFields) > 0 {
		fmt.Println("\nModified Fields:")
		for _, f := range comparison.ModifiedFields {
			fmt.Printf("  ~ %s\n", f)
		}
	}

	if len(comparison.Changes) > 0 && !jsonOutput {
		fmt.Println("\nDetailed Changes:")
		for _, change := range comparison.Changes {
			fmt.Printf("  %s:\n", change.Field)
			fmt.Printf("    Old: %v\n", change.OldValue)
			fmt.Printf("    New: %v\n", change.NewValue)
		}
	}
}

func cmdHistoryRestore(configPath string, version int) {
	if version < 1 {
		fmt.Fprintf(os.Stderr, "Error: -version is required and must be positive\n")
		os.Exit(1)
	}

	manager, err := config.NewManager(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if !manager.IsHistoryEnabled() {
		fmt.Fprintf(os.Stderr, "Error: History tracking is not enabled\n")
		os.Exit(1)
	}

	fmt.Printf("Restoring configuration to version %d...\n", version)

	if err := manager.RestoreHistoryVersion(version, "cli", "Restored via CLI"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Configuration restored to version %d successfully!\n", version)
}
