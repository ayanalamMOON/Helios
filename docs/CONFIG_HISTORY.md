# Configuration History and Versioning

Helios provides comprehensive configuration history tracking, allowing you to monitor all changes to your configuration over time, compare versions, and restore to any previous state.

## Overview

The configuration history system automatically tracks every configuration change with:
- Timestamp of when the change occurred
- Change type (reload, migration, rollback, restore, etc.)
- Source of the change (API, CLI, file watching, signal)
- User who made the change (when available)
- Full configuration snapshot
- Detailed field-level changes

## Features

### Automatic History Recording

Every configuration change is automatically recorded:

```
Version 1 [a1b2c3d4]
  Timestamp:   2025-02-04 15:30:45
  Change Type: initial
  Source:      startup
  Description: Initial configuration load

Version 2 [e5f6g7h8]
  Timestamp:   2025-02-04 15:35:12
  Change Type: reload
  Source:      api
  User:        admin
  Changes:     3 field(s) modified
```

### Change Types

| Type         | Description                                   |
| ------------ | --------------------------------------------- |
| `initial`    | First configuration load at startup           |
| `reload`     | Configuration reloaded via API or signal      |
| `file_watch` | Automatic reload from file system watcher     |
| `migration`  | Migration applied to configuration            |
| `rollback`   | Migration rollback performed                  |
| `restore`    | Configuration restored from backup or history |
| `manual`     | Manual change via API                         |

### Change Sources

| Source       | Description             |
| ------------ | ----------------------- |
| `startup`    | Application startup     |
| `api`        | HTTP API call           |
| `cli`        | Command-line tool       |
| `file_watch` | Automatic file watching |
| `signal`     | SIGHUP signal handler   |
| `migration`  | Migration system        |

## API Reference

### GET /admin/config/history

List configuration change history.

**Query Parameters:**
- `limit` (optional): Maximum entries to return (default: 50)
- `offset` (optional): Pagination offset
- `change_type` (optional): Filter by change type
- `source` (optional): Filter by source
- `user` (optional): Filter by user
- `id` or `version` (optional): Get specific entry

**Response:**
```json
{
  "entries": [
    {
      "id": "a1b2c3d4e5f6g7h8",
      "version": 5,
      "timestamp": "2025-02-04T15:35:12Z",
      "change_type": "reload",
      "source": "api",
      "user": "admin",
      "description": "Updated rate limiting settings",
      "config_hash": "sha256...",
      "changes": [
        {
          "field": "requests_per_second",
          "old_value": 1000,
          "new_value": 2000,
          "category": "rate_limiting"
        }
      ],
      "tags": ["manual"]
    }
  ],
  "total": 25,
  "offset": 0,
  "limit": 50,
  "has_more": false
}
```

### GET /admin/config/history/stats

Get history statistics.

**Response:**
```json
{
  "total_entries": 25,
  "oldest_entry": "2025-01-01T00:00:00Z",
  "newest_entry": "2025-02-04T15:35:12Z",
  "current_version": 25,
  "changes_by_type": {
    "initial": 1,
    "reload": 18,
    "migration": 5,
    "rollback": 1
  },
  "changes_by_source": {
    "startup": 1,
    "api": 15,
    "file_watch": 8,
    "migration": 5
  },
  "average_interval": "2h30m",
  "storage_size_bytes": 125840
}
```

### GET /admin/config/history/compare

Compare two history versions.

**Query Parameters:**
- `from` (required): Source version number
- `to` (required): Target version number

**Response:**
```json
{
  "version_a": 1,
  "version_b": 5,
  "timestamp_a": "2025-01-01T00:00:00Z",
  "timestamp_b": "2025-02-04T15:35:12Z",
  "changes": [
    {
      "field": "observability.log_level",
      "old_value": "INFO",
      "new_value": "DEBUG",
      "category": "observability"
    }
  ],
  "total_changes": 8,
  "added_fields": ["performance.circuit_breaker_enabled"],
  "removed_fields": [],
  "modified_fields": ["observability.log_level", "rate_limiting.requests_per_second"]
}
```

### POST /admin/config/history/restore

Restore configuration to a specific history version.

**Request Body:**
```json
{
  "version": 3,
  "user": "admin",
  "description": "Rolling back to known good state"
}
```

**Response:**
```json
{
  "success": true,
  "message": "Restored configuration to version 3",
  "version": 3
}
```

### DELETE /admin/config/history

Clear or purge history entries.

**Query Parameters:**
- `older_than_hours` (optional): Purge entries older than specified hours

**Examples:**
```bash
# Purge entries older than 720 hours (30 days)
DELETE /admin/config/history?older_than_hours=720

# Clear all history
DELETE /admin/config/history
```

## CLI Usage

The `helios-migrate` tool includes history commands:

### View History

```bash
# View recent history (default 20 entries)
helios-migrate history -config config.yaml

# View more entries
helios-migrate history -config config.yaml -limit 50

# Output as JSON
helios-migrate history -config config.yaml -json
```

Example output:
```
Configuration Change History
============================
Total Entries: 25 (showing 20)

Version 25 [a1b2c3d4]
  Timestamp:   2025-02-04 15:35:12
  Change Type: reload
  Source:      api
  User:        admin
  Description: Updated rate limiting
  Changes:     3 field(s) modified

Version 24 [e5f6g7h8]
  Timestamp:   2025-02-04 14:20:00
  Change Type: file_watch
  Source:      file_watch
  Changes:     1 field(s) modified

...
```

### View Statistics

```bash
helios-migrate history-stats -config config.yaml
```

Example output:
```
Configuration History Statistics
================================
Total Entries:    25
Current Version:  25
Oldest Entry:     2025-01-01 00:00:00
Newest Entry:     2025-02-04 15:35:12
Avg Interval:     2h30m0s
Storage Size:     125840 bytes

Changes by Type:
  initial     : 1
  reload      : 18
  migration   : 5
  rollback    : 1

Changes by Source:
  startup     : 1
  api         : 15
  file_watch  : 8
  migration   : 5
```

### Compare Versions

```bash
helios-migrate history-compare -config config.yaml -from 1 -to 25
```

Example output:
```
Configuration Version Comparison
=================================
Version 1 (2025-01-01 00:00:00) → Version 25 (2025-02-04 15:35:12)

Total Changes: 8

Added Fields:
  + performance.circuit_breaker_enabled
  + auth.session_timeout

Modified Fields:
  ~ observability.log_level
  ~ rate_limiting.requests_per_second
  ~ timeouts.read_timeout

Detailed Changes:
  observability.log_level:
    Old: INFO
    New: DEBUG
  rate_limiting.requests_per_second:
    Old: 1000
    New: 2000
```

### Restore to Version

```bash
helios-migrate history-restore -config config.yaml -version 20
```

Example output:
```
Restoring configuration to version 20...
✓ Configuration restored to version 20 successfully!
```

## Programmatic Usage

### Using the Manager

```go
import "github.com/helios/helios/internal/config"

// Create manager (history is enabled automatically)
manager, err := config.NewManager("config.yaml")
if err != nil {
    log.Fatal(err)
}

// Check if history is enabled
if manager.IsHistoryEnabled() {
    // Get history
    history, err := manager.GetHistory(&config.HistoryQuery{
        Limit: 10,
        ChangeType: config.ChangeTypeReload,
    })

    // Get statistics
    stats, err := manager.GetHistoryStats()

    // Compare versions
    comparison, err := manager.CompareHistoryVersions(1, 5)

    // Restore to version
    err := manager.RestoreHistoryVersion(3, "admin", "Rolling back")
}
```

### Using HistoryManager Directly

```go
import "github.com/helios/helios/internal/config"

// Create history manager
historyMgr, err := config.NewHistoryManager("config.yaml", &config.HistoryConfig{
    HistoryDir:   ".helios/history",
    MaxEntries:   1000,
    AutoSnapshot: true,
})

// Record a change
entry, err := historyMgr.RecordChange(
    oldConfig, newConfig,
    config.ChangeTypeReload,
    config.SourceAPI,
    "admin",
    "Updated rate limiting",
    []string{"manual", "rate-limiting"},
)

// Get entry by version
entry, err := historyMgr.GetEntry("5")

// Get snapshot
snapshot, err := historyMgr.GetSnapshot(5)

// Purge old entries
purged, err := historyMgr.PurgeOldEntries(30 * 24 * time.Hour)
```

## Configuration

History tracking is enabled automatically with sensible defaults:

```go
// Default configuration
&config.HistoryConfig{
    HistoryDir:   ".helios/history",  // Directory for history files
    MaxEntries:   1000,               // Maximum entries to keep
    AutoSnapshot: true,               // Save full snapshots
}
```

### History Storage

History is stored in the `.helios/history` directory relative to the config file:

```
config.yaml
.helios/
  history/
    index.yaml                    # History index file
    snapshot_a1b2c3d4.yaml       # Full config snapshot
    snapshot_e5f6g7h8.yaml
    ...
```

The index file contains metadata for all history entries, while individual snapshots are stored in separate files for efficient retrieval.

## Best Practices

1. **Regular Monitoring**: Periodically check history stats to monitor configuration change patterns
2. **Before Major Changes**: Always note the current version before making significant changes
3. **Use Descriptions**: Provide meaningful descriptions when making changes via API
4. **Periodic Cleanup**: Purge old history entries to manage storage
5. **Compare Before Restore**: Always compare versions before restoring to understand what will change
6. **Backup Integration**: History complements but doesn't replace regular backups

## Integration with Other Features

### With Configuration Reloading

Every configuration reload is automatically recorded in history:

```go
// These all create history entries automatically
manager.Reload()
manager.ReloadWithContext(config.ChangeTypeManual, config.SourceAPI, "admin", "Manual update", nil)
```

### With Migrations

Migration operations are tracked with detailed context:

```
Version 10 [m1n2o3p4]
  Timestamp:   2025-02-04 10:00:00
  Change Type: migration
  Source:      migration
  Description: Applied 3 migrations (v2 → v5)
  Tags:        [migration]
```

### With File Watching

Automatic reloads from file watching are recorded:

```
Version 15 [q5r6s7t8]
  Timestamp:   2025-02-04 11:30:00
  Change Type: file_watch
  Source:      file_watch
  Description: Automatic reload from file change
```

## Troubleshooting

### History Not Recording

1. Check if history is enabled: `manager.IsHistoryEnabled()`
2. Verify the history directory is writable
3. Check for errors in application logs

### Missing Snapshots

Snapshots might be missing if:
- `AutoSnapshot` was disabled
- The snapshot file was manually deleted
- Max entries limit caused automatic cleanup

### Large Storage Usage

If history storage grows too large:
1. Reduce `MaxEntries` in configuration
2. Periodically purge old entries
3. Consider disabling `AutoSnapshot` for less critical environments
