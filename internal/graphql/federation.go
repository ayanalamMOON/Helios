package graphql

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/helios/helios/internal/observability"
)

// FederationConfig controls GraphQL Federation behavior.
type FederationConfig struct {
	// Enabled toggles federation support.
	Enabled bool `json:"enabled" yaml:"enabled"`

	// ServiceName identifies this service in a supergraph.
	ServiceName string `json:"serviceName" yaml:"service_name"`

	// ServiceVersion provides service version metadata.
	ServiceVersion string `json:"serviceVersion" yaml:"service_version"`

	// IncludeServiceSDL enables _service.sdl responses.
	IncludeServiceSDL bool `json:"includeServiceSDL" yaml:"include_service_sdl"`

	// StrictEntities determines whether unknown entity representations fail the request.
	StrictEntities bool `json:"strictEntities" yaml:"strict_entities"`

	// EntityTypes lists supported entity type names.
	EntityTypes []string `json:"entityTypes" yaml:"entity_types"`
}

// DefaultFederationConfig returns production-safe defaults.
func DefaultFederationConfig() *FederationConfig {
	return &FederationConfig{
		Enabled:           true,
		ServiceName:       "helios",
		ServiceVersion:    "1.0.0",
		IncludeServiceSDL: true,
		StrictEntities:    false,
		EntityTypes: []string{
			"User",
			"KVPair",
			"Job",
			"ShardNode",
			"RaftPeer",
		},
	}
}

// FederationService represents Apollo Federation _service response payload.
type FederationService struct {
	SDL string `json:"sdl"`
}

// FederationConfigResponse exposes federation runtime configuration via GraphQL.
type FederationConfigResponse struct {
	Enabled           bool     `json:"enabled"`
	ServiceName       string   `json:"serviceName"`
	ServiceVersion    string   `json:"serviceVersion"`
	IncludeServiceSDL bool     `json:"includeServiceSDL"`
	StrictEntities    bool     `json:"strictEntities"`
	EntityTypeCount   int32    `json:"entityTypeCount"`
	EntityTypes       []string `json:"entityTypes"`
}

// FederationManager coordinates federation config and service SDL access.
type FederationManager struct {
	mu           sync.RWMutex
	config       *FederationConfig
	schemaSource func() string
	logger       *observability.Logger
}

// NewFederationManager creates a federation manager using package schema source.
func NewFederationManager(config *FederationConfig) *FederationManager {
	return NewFederationManagerWithSource(config, func() string { return Schema })
}

// NewFederationManagerWithSource creates a federation manager with custom schema source.
func NewFederationManagerWithSource(config *FederationConfig, schemaSource func() string) *FederationManager {
	if config == nil {
		config = DefaultFederationConfig()
	}
	if schemaSource == nil {
		schemaSource = func() string { return Schema }
	}

	m := &FederationManager{
		config:       normalizeFederationConfig(config),
		schemaSource: schemaSource,
		logger:       observability.NewLogger("graphql-federation"),
	}
	return m
}

// GetConfig returns a copy of current federation config.
func (m *FederationManager) GetConfig() *FederationConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg := *m.config
	cfg.EntityTypes = append([]string{}, m.config.EntityTypes...)
	return &cfg
}

// UpdateConfig replaces federation config.
func (m *FederationManager) UpdateConfig(config *FederationConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = normalizeFederationConfig(config)
}

// IsEnabled reports whether federation is currently enabled.
func (m *FederationManager) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Enabled
}

// GetService returns federation service metadata.
func (m *FederationManager) GetService() (*FederationService, error) {
	sdl, err := m.GetServiceSDL()
	if err != nil {
		return nil, err
	}
	return &FederationService{SDL: sdl}, nil
}

// GetServiceSDL returns the schema definition language for federation composition.
func (m *FederationManager) GetServiceSDL() (string, error) {
	m.mu.RLock()
	cfg := m.config
	m.mu.RUnlock()

	if !cfg.Enabled {
		return "", fmt.Errorf("federation is disabled")
	}
	if !cfg.IncludeServiceSDL {
		return "", fmt.Errorf("service SDL is disabled")
	}

	sdl := strings.TrimSpace(m.schemaSource())
	if sdl == "" {
		return "", fmt.Errorf("schema SDL is empty")
	}
	return sdl, nil
}

// IsEntityTypeSupported checks whether a type is allowed as a federation entity.
func (m *FederationManager) IsEntityTypeSupported(typeName string) bool {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" {
		return false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.config.EntityTypes {
		if t == typeName {
			return true
		}
	}
	return false
}

// SupportedEntityTypes returns a sorted copy of supported entity names.
func (m *FederationManager) SupportedEntityTypes() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	types := append([]string{}, m.config.EntityTypes...)
	sort.Strings(types)
	return types
}

func normalizeFederationConfig(config *FederationConfig) *FederationConfig {
	if config == nil {
		config = DefaultFederationConfig()
	}

	cfg := *config
	if strings.TrimSpace(cfg.ServiceName) == "" {
		cfg.ServiceName = "helios"
	}
	if strings.TrimSpace(cfg.ServiceVersion) == "" {
		cfg.ServiceVersion = "1.0.0"
	}

	if len(cfg.EntityTypes) == 0 {
		cfg.EntityTypes = append([]string{}, DefaultFederationConfig().EntityTypes...)
	} else {
		seen := make(map[string]struct{}, len(cfg.EntityTypes))
		dedup := make([]string, 0, len(cfg.EntityTypes))
		for _, t := range cfg.EntityTypes {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			dedup = append(dedup, t)
		}
		if len(dedup) == 0 {
			dedup = append(dedup, DefaultFederationConfig().EntityTypes...)
		}
		cfg.EntityTypes = dedup
	}

	return &cfg
}
