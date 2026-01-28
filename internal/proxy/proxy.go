package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// Backend represents a backend server
type Backend struct {
	ID               string    `json:"id"`
	Address          string    `json:"address"`
	Weight           int       `json:"weight"`
	Healthy          bool      `json:"healthy"`
	LastChecked      time.Time `json:"last_checked"`
	ActiveConns      int       `json:"active_connections"`
	CircuitOpen      bool      `json:"circuit_open"`
	FailureCount     int       `json:"failure_count"`
	ConsecutiveFails int       `json:"consecutive_fails"`
}

// Route represents a routing rule
type Route struct {
	ID          string   `json:"id"`
	HostMatch   string   `json:"host_match"`
	PathMatch   string   `json:"path_match"`
	BackendPool []string `json:"backend_pool"` // backend IDs
	Algorithm   string   `json:"algorithm"`    // round-robin, least-conn, weighted
}

// Store defines the interface for proxy storage
type Store interface {
	Get(key string) ([]byte, bool)
	Set(key string, value []byte, ttl int64)
	Delete(key string) bool
	Scan(prefix string) []string
}

// Proxy manages reverse proxy functionality
type Proxy struct {
	store            Store
	backends         map[string]*Backend
	routes           map[string]*Route
	proxies          map[string]*httputil.ReverseProxy
	mu               sync.RWMutex
	healthInterval   time.Duration
	circuitThreshold int
	circuitCooldown  time.Duration
	roundRobinIndex  map[string]int
}

// Config holds proxy configuration
type Config struct {
	HealthCheckInterval time.Duration
	CircuitThreshold    int
	CircuitCooldown     time.Duration
}

// NewProxy creates a new proxy
func NewProxy(store Store, cfg *Config) *Proxy {
	p := &Proxy{
		store:            store,
		backends:         make(map[string]*Backend),
		routes:           make(map[string]*Route),
		proxies:          make(map[string]*httputil.ReverseProxy),
		healthInterval:   cfg.HealthCheckInterval,
		circuitThreshold: cfg.CircuitThreshold,
		circuitCooldown:  cfg.CircuitCooldown,
		roundRobinIndex:  make(map[string]int),
	}

	// Load backends and routes from store
	p.loadFromStore()

	return p
}

// AddBackend adds a backend to the proxy
func (p *Proxy) AddBackend(id, address string, weight int) error {
	backend := &Backend{
		ID:          id,
		Address:     address,
		Weight:      weight,
		Healthy:     true,
		LastChecked: time.Now(),
	}

	p.mu.Lock()
	p.backends[id] = backend
	p.mu.Unlock()

	// Save to store
	return p.saveBackend(backend)
}

// RemoveBackend removes a backend
func (p *Proxy) RemoveBackend(id string) error {
	p.mu.Lock()
	delete(p.backends, id)
	delete(p.proxies, id)
	p.mu.Unlock()

	backendKey := fmt.Sprintf("proxy:backend:%s", id)
	p.store.Delete(backendKey)
	return nil
}

// AddRoute adds a routing rule
func (p *Proxy) AddRoute(id, hostMatch, pathMatch string, backendPool []string, algorithm string) error {
	route := &Route{
		ID:          id,
		HostMatch:   hostMatch,
		PathMatch:   pathMatch,
		BackendPool: backendPool,
		Algorithm:   algorithm,
	}

	p.mu.Lock()
	p.routes[id] = route
	p.mu.Unlock()

	// Save to store
	return p.saveRoute(route)
}

// RemoveRoute removes a routing rule
func (p *Proxy) RemoveRoute(id string) error {
	p.mu.Lock()
	delete(p.routes, id)
	p.mu.Unlock()

	routeKey := fmt.Sprintf("proxy:route:%s", id)
	p.store.Delete(routeKey)
	return nil
}

// ServeHTTP implements http.Handler
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Find matching route
	route := p.findRoute(r)
	if route == nil {
		http.Error(w, "No route found", http.StatusNotFound)
		return
	}

	// Select backend
	backend := p.selectBackend(route)
	if backend == nil {
		http.Error(w, "No healthy backend available", http.StatusServiceUnavailable)
		return
	}

	// Get or create reverse proxy
	proxy := p.getOrCreateProxy(backend)

	// Track active connection
	p.mu.Lock()
	backend.ActiveConns++
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		backend.ActiveConns--
		p.mu.Unlock()
	}()

	// Forward request
	proxy.ServeHTTP(w, r)
}

// HealthCheck performs health checks on all backends
func (p *Proxy) HealthCheck() {
	p.mu.RLock()
	backends := make([]*Backend, 0, len(p.backends))
	for _, b := range p.backends {
		backends = append(backends, b)
	}
	p.mu.RUnlock()

	for _, backend := range backends {
		healthy := p.checkBackendHealth(backend)

		p.mu.Lock()
		backend.Healthy = healthy
		backend.LastChecked = time.Now()

		if !healthy {
			backend.ConsecutiveFails++
			backend.FailureCount++
			if backend.ConsecutiveFails >= p.circuitThreshold {
				backend.CircuitOpen = true
			}
		} else {
			backend.ConsecutiveFails = 0
			backend.CircuitOpen = false
		}
		p.mu.Unlock()

		p.saveBackend(backend)
	}
}

// StartHealthChecker starts periodic health checking
func (p *Proxy) StartHealthChecker() chan struct{} {
	stopCh := make(chan struct{})
	ticker := time.NewTicker(p.healthInterval)

	go func() {
		for {
			select {
			case <-ticker.C:
				p.HealthCheck()
			case <-stopCh:
				ticker.Stop()
				return
			}
		}
	}()

	return stopCh
}

// Helper functions

func (p *Proxy) findRoute(r *http.Request) *Route {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Simple matching - in production, use more sophisticated matching
	for _, route := range p.routes {
		if (route.HostMatch == "" || route.HostMatch == r.Host) &&
			(route.PathMatch == "" || route.PathMatch == r.URL.Path) {
			return route
		}
	}
	return nil
}

func (p *Proxy) selectBackend(route *Route) *Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var healthyBackends []*Backend
	for _, backendID := range route.BackendPool {
		backend, exists := p.backends[backendID]
		if exists && backend.Healthy && !backend.CircuitOpen {
			healthyBackends = append(healthyBackends, backend)
		}
	}

	if len(healthyBackends) == 0 {
		return nil
	}

	switch route.Algorithm {
	case "least-conn":
		return p.selectLeastConn(healthyBackends)
	case "weighted":
		return p.selectWeighted(healthyBackends)
	default: // round-robin
		return p.selectRoundRobin(route.ID, healthyBackends)
	}
}

func (p *Proxy) selectRoundRobin(routeID string, backends []*Backend) *Backend {
	if len(backends) == 0 {
		return nil
	}
	index := p.roundRobinIndex[routeID]
	backend := backends[index%len(backends)]
	p.roundRobinIndex[routeID] = (index + 1) % len(backends)
	return backend
}

func (p *Proxy) selectLeastConn(backends []*Backend) *Backend {
	if len(backends) == 0 {
		return nil
	}
	minConns := backends[0].ActiveConns
	selected := backends[0]
	for _, b := range backends {
		if b.ActiveConns < minConns {
			minConns = b.ActiveConns
			selected = b
		}
	}
	return selected
}

func (p *Proxy) selectWeighted(backends []*Backend) *Backend {
	if len(backends) == 0 {
		return nil
	}
	// Simplified weighted selection
	totalWeight := 0
	for _, b := range backends {
		totalWeight += b.Weight
	}
	if totalWeight == 0 {
		return backends[0]
	}
	// For simplicity, just return first backend
	// In production, implement proper weighted random selection
	return backends[0]
}

func (p *Proxy) getOrCreateProxy(backend *Backend) *httputil.ReverseProxy {
	p.mu.Lock()
	defer p.mu.Unlock()

	if proxy, exists := p.proxies[backend.ID]; exists {
		return proxy
	}

	target, _ := url.Parse("http://" + backend.Address)
	proxy := httputil.NewSingleHostReverseProxy(target)
	p.proxies[backend.ID] = proxy
	return proxy
}

func (p *Proxy) checkBackendHealth(backend *Backend) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + backend.Address + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (p *Proxy) saveBackend(backend *Backend) error {
	backendKey := fmt.Sprintf("proxy:backend:%s", backend.ID)
	data, err := json.Marshal(backend)
	if err != nil {
		return err
	}
	p.store.Set(backendKey, data, 0)
	return nil
}

func (p *Proxy) saveRoute(route *Route) error {
	routeKey := fmt.Sprintf("proxy:route:%s", route.ID)
	data, err := json.Marshal(route)
	if err != nil {
		return err
	}
	p.store.Set(routeKey, data, 0)
	return nil
}

func (p *Proxy) loadFromStore() {
	// Load backends
	backendKeys := p.store.Scan("proxy:backend:")
	for _, key := range backendKeys {
		data, exists := p.store.Get(key)
		if !exists {
			continue
		}
		var backend Backend
		if err := json.Unmarshal(data, &backend); err != nil {
			continue
		}
		p.backends[backend.ID] = &backend
	}

	// Load routes
	routeKeys := p.store.Scan("proxy:route:")
	for _, key := range routeKeys {
		data, exists := p.store.Get(key)
		if !exists {
			continue
		}
		var route Route
		if err := json.Unmarshal(data, &route); err != nil {
			continue
		}
		p.routes[route.ID] = &route
	}
}

// DefaultConfig returns default proxy configuration
func DefaultConfig() *Config {
	return &Config{
		HealthCheckInterval: 5 * time.Second,
		CircuitThreshold:    5,
		CircuitCooldown:     60 * time.Second,
	}
}
