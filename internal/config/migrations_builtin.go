package config

import (
	"fmt"
)

// init registers all built-in migrations
func init() {
	// Migration 1: Add version field and restructure config
	RegisterMigration(&Migration{
		Version:     1,
		Description: "Add version field and metadata structure",
		Up: func(data map[string]interface{}) error {
			// Add version field if not present
			if _, exists := data["version"]; !exists {
				data["version"] = 1
			}

			// Add migration metadata
			if _, exists := data["migration_metadata"]; !exists {
				data["migration_metadata"] = map[string]interface{}{
					"created_at":  "2026-02-04T00:00:00Z",
					"description": "Initial migration setup",
				}
			}

			return nil
		},
		Down: func(data map[string]interface{}) error {
			// Remove version field
			delete(data, "version")
			delete(data, "migration_metadata")
			return nil
		},
	})

	// Migration 2: Add authentication config section
	RegisterMigration(&Migration{
		Version:     2,
		Description: "Add authentication configuration section",
		Up: func(data map[string]interface{}) error {
			// Check if auth section exists
			if _, exists := data["authentication"]; !exists {
				data["authentication"] = map[string]interface{}{
					"enabled":              true,
					"token_expiry":         "24h",
					"refresh_enabled":      true,
					"refresh_token_expiry": "168h", // 7 days
					"bcrypt_cost":          10,
				}
			}
			return nil
		},
		Down: func(data map[string]interface{}) error {
			delete(data, "authentication")
			return nil
		},
	})

	// Migration 3: Split metrics and tracing into separate sections
	RegisterMigration(&Migration{
		Version:     3,
		Description: "Split observability into metrics and tracing sections",
		Up: func(data map[string]interface{}) error {
			obs, ok := data["observability"].(map[string]interface{})
			if !ok {
				return fmt.Errorf("observability section not found or invalid type")
			}

			// Extract metrics config
			if _, exists := data["metrics"]; !exists {
				metricsEnabled := false
				metricsPort := ":9090"

				if val, ok := obs["metrics_enabled"].(bool); ok {
					metricsEnabled = val
				}
				if val, ok := obs["metrics_port"].(string); ok {
					metricsPort = val
				}

				data["metrics"] = map[string]interface{}{
					"enabled":   metricsEnabled,
					"port":      metricsPort,
					"path":      "/metrics",
					"namespace": "helios",
				}
			}

			// Extract tracing config
			if _, exists := data["tracing"]; !exists {
				tracingEnabled := false
				tracingEndpoint := ""

				if val, ok := obs["tracing_enabled"].(bool); ok {
					tracingEnabled = val
				}
				if val, ok := obs["tracing_endpoint"].(string); ok {
					tracingEndpoint = val
				}

				data["tracing"] = map[string]interface{}{
					"enabled":      tracingEnabled,
					"endpoint":     tracingEndpoint,
					"service_name": "helios",
					"sample_rate":  1.0,
				}
			}

			// Keep only log_level in observability
			newObs := map[string]interface{}{
				"log_level": obs["log_level"],
			}
			data["observability"] = newObs

			return nil
		},
		Down: func(data map[string]interface{}) error {
			// Merge metrics and tracing back into observability
			obs, ok := data["observability"].(map[string]interface{})
			if !ok {
				obs = make(map[string]interface{})
			}

			// Get metrics config
			if metrics, ok := data["metrics"].(map[string]interface{}); ok {
				if val, ok := metrics["enabled"].(bool); ok {
					obs["metrics_enabled"] = val
				}
				if val, ok := metrics["port"].(string); ok {
					obs["metrics_port"] = val
				}
			}

			// Get tracing config
			if tracing, ok := data["tracing"].(map[string]interface{}); ok {
				if val, ok := tracing["enabled"].(bool); ok {
					obs["tracing_enabled"] = val
				}
				if val, ok := tracing["endpoint"].(string); ok {
					obs["tracing_endpoint"] = val
				}
			}

			data["observability"] = obs
			delete(data, "metrics")
			delete(data, "tracing")

			return nil
		},
	})

	// Migration 4: Add TLS configuration
	RegisterMigration(&Migration{
		Version:     4,
		Description: "Add TLS/SSL configuration section",
		Up: func(data map[string]interface{}) error {
			if _, exists := data["tls"]; !exists {
				data["tls"] = map[string]interface{}{
					"enabled":       false,
					"cert_file":     "",
					"key_file":      "",
					"ca_file":       "",
					"min_version":   "1.2",
					"verify_client": false,
				}
			}
			return nil
		},
		Down: func(data map[string]interface{}) error {
			delete(data, "tls")
			return nil
		},
	})

	// Migration 5: Add circuit breaker configuration
	RegisterMigration(&Migration{
		Version:     5,
		Description: "Add circuit breaker configuration for resilience",
		Up: func(data map[string]interface{}) error {
			if _, exists := data["circuit_breaker"]; !exists {
				data["circuit_breaker"] = map[string]interface{}{
					"enabled":            false,
					"failure_threshold":  5,
					"success_threshold":  2,
					"timeout":            "30s",
					"half_open_requests": 1,
				}
			}
			return nil
		},
		Down: func(data map[string]interface{}) error {
			delete(data, "circuit_breaker")
			return nil
		},
	})
}

// Helper functions for common migration patterns

// AddFieldWithDefault adds a field to a section if it doesn't exist
func AddFieldWithDefault(section map[string]interface{}, field string, defaultValue interface{}) {
	if _, exists := section[field]; !exists {
		section[field] = defaultValue
	}
}

// RenameField renames a field in a section
func RenameField(section map[string]interface{}, oldName, newName string) error {
	if val, exists := section[oldName]; exists {
		section[newName] = val
		delete(section, oldName)
	}
	return nil
}

// MoveField moves a field from one section to another
func MoveField(from, to map[string]interface{}, fieldName string) error {
	if val, exists := from[fieldName]; exists {
		to[fieldName] = val
		delete(from, fieldName)
	}
	return nil
}

// ConvertDuration converts duration string format (if needed)
func ConvertDuration(section map[string]interface{}, field string, multiplier float64) error {
	if val, ok := section[field].(string); ok {
		// Duration conversion logic here
		section[field] = val // Placeholder
	}
	return nil
}
