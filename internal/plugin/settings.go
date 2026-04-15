package plugin

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func settingBool(settings map[string]interface{}, key string, defaultValue bool) bool {
	if settings == nil {
		return defaultValue
	}
	value, ok := settings[key]
	if !ok || value == nil {
		return defaultValue
	}

	switch v := value.(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return defaultValue
		}
		return parsed
	default:
		return defaultValue
	}
}

func settingInt(settings map[string]interface{}, key string, defaultValue int) int {
	if settings == nil {
		return defaultValue
	}
	value, ok := settings[key]
	if !ok || value == nil {
		return defaultValue
	}

	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return defaultValue
		}
		return parsed
	default:
		return defaultValue
	}
}

func settingString(settings map[string]interface{}, key string, defaultValue string) string {
	if settings == nil {
		return defaultValue
	}
	value, ok := settings[key]
	if !ok || value == nil {
		return defaultValue
	}
	s, ok := value.(string)
	if !ok {
		return defaultValue
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return defaultValue
	}
	return trimmed
}

func settingStringSlice(settings map[string]interface{}, key string, defaultValue []string) []string {
	if settings == nil {
		return defaultValue
	}
	value, ok := settings[key]
	if !ok || value == nil {
		return defaultValue
	}

	switch v := value.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) == 0 {
			return defaultValue
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			trimmed := strings.TrimSpace(fmt.Sprintf("%v", item))
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) == 0 {
			return defaultValue
		}
		return out
	case string:
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) == 0 {
			return defaultValue
		}
		return out
	default:
		return defaultValue
	}
}

func settingDuration(settings map[string]interface{}, key string, defaultValue time.Duration) time.Duration {
	if settings == nil {
		return defaultValue
	}
	value, ok := settings[key]
	if !ok || value == nil {
		return defaultValue
	}

	switch v := value.(type) {
	case time.Duration:
		if v <= 0 {
			return defaultValue
		}
		return v
	case int:
		if v <= 0 {
			return defaultValue
		}
		return time.Duration(v)
	case int64:
		if v <= 0 {
			return defaultValue
		}
		return time.Duration(v)
	case float64:
		if v <= 0 {
			return defaultValue
		}
		return time.Duration(v)
	case string:
		parsed, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil || parsed <= 0 {
			return defaultValue
		}
		return parsed
	default:
		return defaultValue
	}
}
