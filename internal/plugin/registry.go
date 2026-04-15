package plugin

import (
	"fmt"
	"sort"
	"sync"
)

// Factory builds a plugin instance.
type Factory func() Plugin

// Registry stores named plugin factories.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry creates an empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register registers a plugin factory by name.
func (r *Registry) Register(name string, factory Factory) error {
	if name == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}
	if factory == nil {
		return fmt.Errorf("plugin factory for %s cannot be nil", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("plugin factory already registered: %s", name)
	}

	r.factories[name] = factory
	return nil
}

// Create constructs a plugin by factory name.
func (r *Registry) Create(name string) (Plugin, error) {
	r.mu.RLock()
	factory, exists := r.factories[name]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("plugin factory not found: %s", name)
	}

	plugin := factory()
	if plugin == nil {
		return nil, fmt.Errorf("plugin factory returned nil: %s", name)
	}

	return plugin, nil
}

// Names returns registered plugin names in sorted order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var defaultRegistry = NewRegistry()

// RegisterFactory registers a factory in the default registry.
func RegisterFactory(name string, factory Factory) error {
	return defaultRegistry.Register(name, factory)
}

// MustRegisterFactory registers a factory in the default registry and panics on failure.
func MustRegisterFactory(name string, factory Factory) {
	if err := RegisterFactory(name, factory); err != nil {
		panic(err)
	}
}

// CreateFromDefault creates a plugin from the default registry.
func CreateFromDefault(name string) (Plugin, error) {
	return defaultRegistry.Create(name)
}

// KnownFactories returns all factories from the default registry.
func KnownFactories() []string {
	return defaultRegistry.Names()
}
