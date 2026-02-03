# Configuration Migration System

## Overview

Helios includes a built-in configuration migration system that helps you manage configuration schema evolution over time. This is essential for:

- **Version upgrades**: Automatically updating configuration structure when upgrading Helios
- **Backward compatibility**: Supporting legacy configuration files
- **Safe transitions**: Providing rollback capabilities if issues arise
- **Audit trail**: Tracking configuration version history

## Quick Start

### Check Migration Status

```bash
# Using CLI tool
helios-migrate status -config config.yaml

# Using HTTP API
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/migrations/status
```

### Apply Pending Migrations

```bash
# Preview first (dry-run)
helios-migrate apply -config config.yaml -dry-run

# Apply migrations
helios-migrate apply -config config.yaml

# Via API
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/migrations/apply
```

## Configuration Versioning

### Version Field

Configurations include a `version` field that tracks the schema version:

```yaml
version: 5  # Schema version

immutable:
  node_id: "node-1"
  data_dir: "./data"

observability:
  log_level: "INFO"
# ...
```

A configuration without a `version` field is considered version 0 (legacy).

### Built-in Migrations

Helios includes these built-in migrations:

| Version | Description                                           |
| ------- | ----------------------------------------------------- |
| 1       | Add version field and metadata structure              |
| 2       | Add authentication configuration section              |
| 3       | Split observability into metrics and tracing sections |
| 4       | Add TLS/SSL configuration section                     |
| 5       | Add circuit breaker configuration for resilience      |

## CLI Tool Usage

### Commands

```bash
# Check current status
helios-migrate status -config config.yaml

# List all available migrations
helios-migrate list

# Apply pending migrations
helios-migrate apply -config config.yaml

# Apply with dry-run (preview)
helios-migrate apply -config config.yaml -dry-run

# Rollback to specific version
helios-migrate rollback -config config.yaml -version 2

# List backups
helios-migrate backups -config config.yaml

# Restore from backup
helios-migrate restore -config config.yaml -backup config.yaml.backup_20260204_153045
```

### Options

| Option           | Description                                        |
| ---------------- | -------------------------------------------------- |
| `-config string` | Path to configuration file (default "config.yaml") |
| `-version int`   | Target version for rollback                        |
| `-dry-run`       | Preview changes without applying                   |
| `-backup string` | Backup file path for restore                       |
| `-json`          | Output in JSON format                              |

### Examples

**Check status:**
```bash
$ helios-migrate status -config config.yaml

Configuration Migration Status
==============================
Config File:      config.yaml
Current Version:  2
Latest Version:   5
Pending Count:    3

Pending Migrations:
  - v3: Split observability into metrics and tracing sections
  - v4: Add TLS/SSL configuration section
  - v5: Add circuit breaker configuration for resilience

Run 'helios-migrate apply' to apply pending migrations.
```

**Apply migrations:**
```bash
$ helios-migrate apply -config config.yaml

Migration Results
=================
Success:         true
From Version:    2
To Version:      5
Migrations:      3
Backup Created:  config.yaml.backup_20260204_153045

Applied Migrations:
  - v3: Split observability into metrics and tracing sections
  - v4: Add TLS/SSL configuration section
  - v5: Add circuit breaker configuration for resilience

✓ Migrations applied successfully!
```

## HTTP API Reference

### GET /admin/config/migrations

List all registered migrations.

**Response:**
```json
{
  "migrations": [
    {"version": 1, "description": "Add version field and metadata structure"},
    {"version": 2, "description": "Add authentication configuration section"},
    {"version": 3, "description": "Split observability into metrics and tracing sections"},
    {"version": 4, "description": "Add TLS/SSL configuration section"},
    {"version": 5, "description": "Add circuit breaker configuration for resilience"}
  ],
  "total": 5,
  "latest_version": 5
}
```

### GET /admin/config/migrations/status

Get current migration status.

**Response:**
```json
{
  "current_version": 2,
  "latest_version": 5,
  "pending_count": 3,
  "pending_migrations": [
    {"version": 3, "description": "Split observability into metrics and tracing sections"},
    {"version": 4, "description": "Add TLS/SSL configuration section"},
    {"version": 5, "description": "Add circuit breaker configuration for resilience"}
  ],
  "has_pending": true
}
```

### POST /admin/config/migrations/apply

Apply pending migrations.

**Query Parameters:**
- `dry_run=true` - Preview without applying

**Response:**
```json
{
  "success": true,
  "from_version": 2,
  "to_version": 5,
  "applied_count": 3,
  "backup_path": "config.yaml.backup_20260204_153045",
  "dry_run": false,
  "migrations_applied": [3, 4, 5]
}
```

### POST /admin/config/migrations/rollback

Rollback to a specific version.

**Request Body:**
```json
{
  "target_version": 2,
  "dry_run": false
}
```

**Response:**
```json
{
  "success": true,
  "from_version": 5,
  "to_version": 2,
  "applied_count": 3,
  "backup_path": "config.yaml.backup_20260204_153245"
}
```

### GET /admin/config/backups

List configuration backups.

**Response:**
```json
{
  "backups": [
    "config.yaml.backup_20260204_153245",
    "config.yaml.backup_20260204_153045",
    "config.yaml.backup_20260203_120000"
  ],
  "count": 3
}
```

### POST /admin/config/backups

Restore from a backup.

**Request Body:**
```json
{
  "backup_path": "config.yaml.backup_20260204_153045"
}
```

**Response:**
```json
{
  "success": true,
  "message": "Backup restored successfully"
}
```

## Programmatic Usage

### Check Status

```go
package main

import (
    "fmt"
    "github.com/helios/helios/internal/config"
)

func main() {
    status, err := config.GetMigrationStatus("config.yaml")
    if err != nil {
        panic(err)
    }

    fmt.Printf("Current version: %d\n", status.CurrentVersion)
    fmt.Printf("Latest version: %d\n", status.LatestVersion)
    fmt.Printf("Pending: %d\n", status.PendingCount)

    if status.HasPending {
        for _, m := range status.PendingMigrations {
            fmt.Printf("  - v%d: %s\n", m.Version, m.Description)
        }
    }
}
```

### Apply Migrations

```go
// Apply with dry-run first
result, err := config.ApplyMigrations("config.yaml", true)
if err != nil {
    panic(err)
}

fmt.Printf("Would apply %d migrations\n", result.AppliedCount)

// Apply for real
result, err = config.ApplyMigrations("config.yaml", false)
if err != nil {
    panic(err)
}

fmt.Printf("Applied %d migrations, backup at %s\n",
    result.AppliedCount, result.BackupPath)
```

### Using Config Manager

```go
mgr, err := config.NewManager("config.yaml")
if err != nil {
    panic(err)
}

// Check status through manager
status, err := mgr.GetMigrationStatus()
if err != nil {
    panic(err)
}

// Apply migrations (auto-reloads config after)
result, err := mgr.ApplyMigrations(false)
if err != nil {
    panic(err)
}

// Rollback if needed
result, err = mgr.RollbackMigration(2, false)
if err != nil {
    panic(err)
}

// List backups
backups, err := mgr.ListBackups()
if err != nil {
    panic(err)
}

// Restore from backup (auto-reloads config)
err = mgr.RestoreBackup(backups[0])
if err != nil {
    panic(err)
}
```

## Creating Custom Migrations

### Register a Migration

```go
package migrations

import (
    "github.com/helios/helios/internal/config"
)

func init() {
    config.RegisterMigration(&config.Migration{
        Version:     6,
        Description: "Add custom feature configuration",
        Up: func(data map[string]interface{}) error {
            // Add new section
            data["custom_feature"] = map[string]interface{}{
                "enabled": true,
                "setting1": "value1",
                "setting2": 100,
            }
            return nil
        },
        Down: func(data map[string]interface{}) error {
            // Remove section on rollback
            delete(data, "custom_feature")
            return nil
        },
    })
}
```

### Migration Best Practices

1. **Always provide Down function** - Enables rollback capability
2. **Make migrations idempotent** - Should be safe to run multiple times
3. **Validate data before changes** - Return errors for invalid states
4. **Keep migrations atomic** - One logical change per migration
5. **Use helper functions** - Consistent patterns reduce errors

### Helper Functions

```go
// Add field with default value (won't overwrite existing)
config.AddFieldWithDefault(section, "new_field", "default")

// Rename a field
config.RenameField(section, "old_name", "new_name")

// Move field between sections
config.MoveField(fromSection, toSection, "field_name")
```

### Complex Migration Example

```go
config.RegisterMigration(&config.Migration{
    Version:     7,
    Description: "Migrate rate limiting to per-endpoint configuration",
    Up: func(data map[string]interface{}) error {
        // Get old rate limiting config
        rateLimit, ok := data["rate_limiting"].(map[string]interface{})
        if !ok {
            // No rate limiting config, nothing to migrate
            return nil
        }

        // Get global settings
        enabled := rateLimit["enabled"]
        rps := rateLimit["requests_per_second"]
        burst := rateLimit["burst_size"]

        // Create per-endpoint structure
        endpoints := map[string]interface{}{
            "default": map[string]interface{}{
                "enabled": enabled,
                "requests_per_second": rps,
                "burst_size": burst,
            },
            "api": map[string]interface{}{
                "enabled": enabled,
                "requests_per_second": rps,
                "burst_size": burst,
            },
            "admin": map[string]interface{}{
                "enabled": enabled,
                "requests_per_second": 100, // Lower for admin
                "burst_size": 10,
            },
        }

        // Replace with new structure
        data["rate_limiting"] = map[string]interface{}{
            "enabled":   enabled,
            "endpoints": endpoints,
        }

        return nil
    },
    Down: func(data map[string]interface{}) error {
        // Get new rate limiting config
        rateLimit, ok := data["rate_limiting"].(map[string]interface{})
        if !ok {
            return nil
        }

        // Get default endpoint config
        endpoints, ok := rateLimit["endpoints"].(map[string]interface{})
        if !ok {
            return nil
        }

        defaultEndpoint, ok := endpoints["default"].(map[string]interface{})
        if !ok {
            return nil
        }

        // Restore flat structure
        data["rate_limiting"] = map[string]interface{}{
            "enabled":             rateLimit["enabled"],
            "requests_per_second": defaultEndpoint["requests_per_second"],
            "burst_size":          defaultEndpoint["burst_size"],
            "cleanup_interval":    "5m",
        }

        return nil
    },
})
```

## Automatic Migrations on Startup

Configure Helios to automatically apply migrations on startup:

```go
func main() {
    // Load configuration
    mgr, err := config.NewManager("config.yaml")
    if err != nil {
        log.Fatal(err)
    }

    // Check for pending migrations
    status, err := mgr.GetMigrationStatus()
    if err != nil {
        log.Fatal(err)
    }

    if status.HasPending {
        log.Printf("Found %d pending migrations", status.PendingCount)

        // Apply migrations
        result, err := mgr.ApplyMigrations(false)
        if err != nil {
            log.Fatalf("Migration failed: %v", err)
        }

        log.Printf("Applied %d migrations", result.AppliedCount)
        log.Printf("Backup created: %s", result.BackupPath)
    }

    // Continue with normal startup...
}
```

## Backup Management

### Backup Strategy

Migrations automatically create timestamped backups:

```
config.yaml
config.yaml.backup_20260204_153045
config.yaml.backup_20260204_120000
config.yaml.backup_20260203_090000
```

### Cleanup Old Backups

```bash
# Keep last 5 backups
ls -t config.yaml.backup_* | tail -n +6 | xargs rm -f
```

### Backup Retention Script

```bash
#!/bin/bash
CONFIG_DIR="/etc/helios"
KEEP_BACKUPS=5

cd "$CONFIG_DIR"
ls -t config.yaml.backup_* 2>/dev/null | tail -n +$((KEEP_BACKUPS + 1)) | xargs -r rm -f
echo "Cleaned up old backups, keeping $KEEP_BACKUPS most recent"
```

## Integration with Configuration Reloading

Migrations integrate seamlessly with the configuration reload system:

```go
mgr, err := config.NewManager("config.yaml")
if err != nil {
    log.Fatal(err)
}

// Apply migrations (auto-reloads)
result, err := mgr.ApplyMigrations(false)
if err != nil {
    log.Fatal(err)
}

// Config is now updated and reloaded
cfg := mgr.GetConfig()
```

### Workflow

1. **Check status** - See what migrations are pending
2. **Dry-run** - Preview changes without applying
3. **Apply** - Run migrations with automatic backup
4. **Verify** - Check new configuration is valid
5. **Rollback** - If issues, rollback to previous version

## Troubleshooting

### Migration Failed Midway

If a migration fails partway through:

```bash
# Check backup was created
helios-migrate backups -config config.yaml

# Restore from backup
helios-migrate restore -config config.yaml -backup config.yaml.backup_20260204_153045

# Fix issue and try again
```

### Configuration Validation Errors

If migrated configuration fails validation:

```bash
# Check current state
helios-migrate status -config config.yaml

# Preview diff before reload
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/diff

# Restore backup if needed
helios-migrate restore -config config.yaml -backup <backup_path>
```

### Version Mismatch

If config version doesn't match expected:

```yaml
# Check version in config file
head -1 config.yaml
# Should show: version: X

# Compare to latest
helios-migrate list -json | jq '.latest_version'
```

## Security Considerations

1. **Backup files** contain sensitive configuration - secure permissions
2. **Migration endpoints** require admin authentication
3. **Rollback capability** should be restricted in production
4. **Audit logging** should track all migration operations
5. **Pre-production testing** - always test migrations before production

## See Also

- [CONFIG_RELOAD.md](CONFIG_RELOAD.md) - Configuration reloading documentation
- [examples/config-reload/](../examples/config-reload/) - Example configuration
