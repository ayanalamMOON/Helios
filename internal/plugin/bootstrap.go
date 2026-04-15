package plugin

import (
	"fmt"
	"os"
	"strings"
)

// ParsePluginList converts a comma-separated plugin list into normalized names.
func ParsePluginList(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{})

	for _, part := range parts {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}

	return result
}

// LoadFromEnv loads plugins listed in an environment variable.
// Example: HELIOS_PLUGINS="audit,keyguard,http_access"
func LoadFromEnv(manager *Manager, envVar string, defaultConfig RuntimeConfig) ([]string, error) {
	if manager == nil {
		return nil, fmt.Errorf("plugin manager cannot be nil")
	}
	if strings.TrimSpace(envVar) == "" {
		return nil, fmt.Errorf("environment variable name cannot be empty")
	}

	names := ParsePluginList(os.Getenv(envVar))
	for _, name := range names {
		if err := manager.Load(name, defaultConfig); err != nil {
			return names, fmt.Errorf("failed loading plugin %s: %w", name, err)
		}
	}

	return names, nil
}
