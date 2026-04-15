package plugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrCircuitOpen indicates plugin execution is temporarily disabled due to repeated failures.
	ErrCircuitOpen = errors.New("plugin circuit breaker is open")
	// ErrPluginNotFound indicates a requested plugin name is not known to this manager.
	ErrPluginNotFound = errors.New("plugin not found")
	// ErrPluginNotReloadable indicates a plugin does not support runtime reload.
	ErrPluginNotReloadable = errors.New("plugin does not support runtime reload")
)

type managerHost struct {
	manager *Manager
}

func (h *managerHost) Publish(event Event) {
	h.manager.Publish(event)
}

func (h *managerHost) Logger() Logger {
	return h.manager.cfg.Logger
}

type pluginRuntime struct {
	mu sync.Mutex

	plugin          Plugin
	metadata        Metadata
	cfg             RuntimeConfig
	commandHook     CommandHook
	middleware      HTTPMiddleware
	eventSubscriber EventSubscriber
	configReloader  ConfigReloader
	healthChecker   HealthChecker
	lastHealth      PluginHealth

	started bool

	consecutiveFails int
	circuitOpenUntil time.Time

	totalCalls    uint64
	totalErrors   uint64
	totalPanics   uint64
	totalDuration time.Duration
	lastError     string
}

func (r *pluginRuntime) isStarted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started
}

func (r *pluginRuntime) setStarted(started bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = started
}

func (r *pluginRuntime) isCircuitOpen(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.circuitOpenUntil.IsZero() && now.Before(r.circuitOpenUntil)
}

func (r *pluginRuntime) recordInvocation(duration time.Duration, err error, panicked bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.totalCalls++
	r.totalDuration += duration

	if panicked {
		r.totalPanics++
	}

	if err != nil {
		r.totalErrors++
		r.consecutiveFails++
		r.lastError = err.Error()

		if r.cfg.MaxFailures > 0 && r.consecutiveFails >= r.cfg.MaxFailures {
			r.circuitOpenUntil = time.Now().UTC().Add(r.cfg.Cooldown)
		}
		return
	}

	// Success path: close any open-failure streak.
	r.consecutiveFails = 0
	if !r.circuitOpenUntil.IsZero() && time.Now().UTC().After(r.circuitOpenUntil) {
		r.circuitOpenUntil = time.Time{}
	}
}

func (r *pluginRuntime) snapshotStats() PluginStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	health := r.lastHealth
	if health.Status == "" {
		health = PluginHealth{
			Status:    HealthUnknown,
			Message:   "health check not run",
			Timestamp: time.Now().UTC(),
		}
	}

	return PluginStats{
		Name:             r.metadata.Name,
		Enabled:          r.cfg.Enabled,
		Started:          r.started,
		Priority:         r.cfg.Priority,
		Health:           health,
		ConsecutiveFails: r.consecutiveFails,
		CircuitOpen:      !r.circuitOpenUntil.IsZero() && time.Now().UTC().Before(r.circuitOpenUntil),
		CircuitOpenUntil: r.circuitOpenUntil,
		TotalCalls:       r.totalCalls,
		TotalErrors:      r.totalErrors,
		TotalPanics:      r.totalPanics,
		TotalDuration:    r.totalDuration,
		LastError:        r.lastError,
	}
}

func (r *pluginRuntime) runtimeConfig() RuntimeConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

func (r *pluginRuntime) setRuntimeConfig(cfg RuntimeConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = cfg
}

func (r *pluginRuntime) setHealth(health PluginHealth) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastHealth = health
}

// Manager orchestrates plugin lifecycle and hook execution.
type Manager struct {
	mu      sync.RWMutex
	cfg     ManagerConfig
	host    *managerHost
	plugins []*pluginRuntime
	byName  map[string]*pluginRuntime

	eventCh chan Event
	stopCh  chan struct{}
	running bool
	wg      sync.WaitGroup

	publishedEvents uint64
	droppedEvents   uint64
}

// NewManager creates a plugin manager.
func NewManager(cfg ManagerConfig) *Manager {
	cfg = normalizeManagerConfig(cfg)

	m := &Manager{
		cfg:    cfg,
		byName: make(map[string]*pluginRuntime),
	}
	m.host = &managerHost{manager: m}

	return m
}

func normalizeManagerConfig(cfg ManagerConfig) ManagerConfig {
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 150 * time.Millisecond
	}
	if cfg.EventDispatchTimeout <= 0 {
		cfg.EventDispatchTimeout = cfg.DefaultTimeout
	}
	if cfg.EventBuffer <= 0 {
		cfg.EventBuffer = 256
	}
	if cfg.Logger == nil {
		cfg.Logger = NoopLogger{}
	}
	if cfg.Registry == nil {
		cfg.Registry = defaultRegistry
	}
	return cfg
}

func isZeroRuntimeConfig(cfg RuntimeConfig) bool {
	return !cfg.Enabled &&
		cfg.Priority == 0 &&
		cfg.Timeout == 0 &&
		!cfg.FailOpen &&
		cfg.MaxFailures == 0 &&
		cfg.Cooldown == 0 &&
		len(cfg.Settings) == 0
}

func (m *Manager) applyRuntimeDefaults(cfg RuntimeConfig) RuntimeConfig {
	if isZeroRuntimeConfig(cfg) {
		cfg.Enabled = true
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = m.cfg.DefaultTimeout
	}
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = 5
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Second
	}
	if !cfg.FailOpen {
		cfg.FailOpen = m.cfg.DefaultFailOpen
	}
	if cfg.Settings == nil {
		cfg.Settings = make(map[string]interface{})
	}
	return cfg
}

// Register registers an already-constructed plugin instance.
func (m *Manager) Register(p Plugin, cfg RuntimeConfig) error {
	if p == nil {
		return fmt.Errorf("plugin cannot be nil")
	}

	meta := p.Metadata()
	if meta.Name == "" {
		return fmt.Errorf("plugin metadata name cannot be empty")
	}

	cfg = m.applyRuntimeDefaults(cfg)

	runtime := &pluginRuntime{
		plugin:   p,
		metadata: meta,
		cfg:      cfg,
	}

	if hook, ok := p.(CommandHook); ok {
		runtime.commandHook = hook
	}
	if middleware, ok := p.(HTTPMiddleware); ok {
		runtime.middleware = middleware
	}
	if subscriber, ok := p.(EventSubscriber); ok {
		runtime.eventSubscriber = subscriber
	}
	if reloader, ok := p.(ConfigReloader); ok {
		runtime.configReloader = reloader
	}
	if checker, ok := p.(HealthChecker); ok {
		runtime.healthChecker = checker
	}
	runtime.lastHealth = PluginHealth{
		Status:    HealthUnknown,
		Message:   "health check not run",
		Timestamp: time.Now().UTC(),
	}

	// Init before registration to fail fast.
	if err := p.Init(context.Background(), m.host, cfg); err != nil {
		return fmt.Errorf("plugin init failed (%s): %w", meta.Name, err)
	}

	m.mu.Lock()
	if _, exists := m.byName[meta.Name]; exists {
		m.mu.Unlock()
		return fmt.Errorf("plugin already registered: %s", meta.Name)
	}

	m.plugins = append(m.plugins, runtime)
	m.byName[meta.Name] = runtime
	m.sortPluginsLocked()
	shouldStart := m.running && cfg.Enabled
	m.mu.Unlock()

	if shouldStart {
		if err := m.startPlugin(context.Background(), runtime); err != nil {
			return fmt.Errorf("failed to start plugin %s after registration: %w", meta.Name, err)
		}
	}

	return nil
}

// Load creates a plugin from the configured registry and registers it.
func (m *Manager) Load(name string, cfg RuntimeConfig) error {
	plugin, err := m.cfg.Registry.Create(name)
	if err != nil {
		return err
	}
	return m.Register(plugin, cfg)
}

func (m *Manager) sortPluginsLocked() {
	sort.SliceStable(m.plugins, func(i, j int) bool {
		if m.plugins[i].cfg.Priority == m.plugins[j].cfg.Priority {
			return m.plugins[i].metadata.Name < m.plugins[j].metadata.Name
		}
		return m.plugins[i].cfg.Priority > m.plugins[j].cfg.Priority
	})
}

func (m *Manager) startPlugin(ctx context.Context, runtime *pluginRuntime) error {
	if runtime == nil || runtime.isStarted() {
		return nil
	}
	cfg := runtime.runtimeConfig()

	err, panicked := m.callWithTimeout(ctx, cfg.Timeout, func(callCtx context.Context) error {
		return runtime.plugin.Start(callCtx)
	})
	runtime.recordInvocation(0, err, panicked)
	if err != nil {
		return fmt.Errorf("plugin start failed: %w", err)
	}

	runtime.setStarted(true)
	m.cfg.Logger.Info("Plugin started", map[string]interface{}{"plugin": runtime.metadata.Name})
	return nil
}

func (m *Manager) stopPlugin(ctx context.Context, runtime *pluginRuntime) error {
	if runtime == nil || !runtime.isStarted() {
		return nil
	}
	cfg := runtime.runtimeConfig()

	err, panicked := m.callWithTimeout(ctx, cfg.Timeout, func(callCtx context.Context) error {
		return runtime.plugin.Stop(callCtx)
	})
	runtime.recordInvocation(0, err, panicked)
	if err != nil {
		return fmt.Errorf("plugin stop failed: %w", err)
	}

	runtime.setStarted(false)
	m.cfg.Logger.Info("Plugin stopped", map[string]interface{}{"plugin": runtime.metadata.Name})
	return nil
}

// Start starts event processing and all enabled plugins.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}

	plugins := make([]*pluginRuntime, len(m.plugins))
	copy(plugins, m.plugins)

	m.eventCh = make(chan Event, m.cfg.EventBuffer)
	m.stopCh = make(chan struct{})
	m.running = true
	m.mu.Unlock()

	started := make([]*pluginRuntime, 0, len(plugins))
	for _, rt := range plugins {
		if !rt.cfg.Enabled {
			continue
		}
		if err := m.startPlugin(ctx, rt); err != nil {
			for i := len(started) - 1; i >= 0; i-- {
				_ = m.stopPlugin(ctx, started[i])
			}
			m.mu.Lock()
			m.running = false
			close(m.stopCh)
			m.mu.Unlock()
			return err
		}
		started = append(started, rt)
	}

	m.wg.Add(1)
	go m.eventLoop()
	return nil
}

// Stop stops all plugins and event processing.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}

	plugins := make([]*pluginRuntime, len(m.plugins))
	copy(plugins, m.plugins)

	stopCh := m.stopCh
	m.running = false
	m.mu.Unlock()

	close(stopCh)
	m.wg.Wait()

	var firstErr error
	for i := len(plugins) - 1; i >= 0; i-- {
		rt := plugins[i]
		if err := m.stopPlugin(ctx, rt); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

func (m *Manager) eventLoop() {
	defer m.wg.Done()

	for {
		select {
		case <-m.stopCh:
			return
		case event := <-m.eventCh:
			m.dispatchEvent(event)
		}
	}
}

func (m *Manager) dispatchEvent(event Event) {
	plugins := m.eventSubscribers()
	for _, rt := range plugins {
		if !matchesEvent(rt.eventSubscriber.EventTypes(), event.Type) {
			continue
		}

		_ = m.invokeWithPolicy(context.Background(), rt, "event", func(callCtx context.Context) error {
			return rt.eventSubscriber.OnEvent(callCtx, event)
		})
	}
}

func matchesEvent(supported []string, eventType string) bool {
	if len(supported) == 0 {
		return false
	}
	for _, pattern := range supported {
		if eventPatternMatches(pattern, eventType) {
			return true
		}
	}
	return false
}

func eventPatternMatches(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}

	tokens := strings.Split(pattern, "*")
	anchoredStart := !strings.HasPrefix(pattern, "*")
	anchoredEnd := !strings.HasSuffix(pattern, "*")

	pos := 0
	for i, token := range tokens {
		if token == "" {
			continue
		}

		idx := strings.Index(value[pos:], token)
		if idx < 0 {
			return false
		}

		if i == 0 && anchoredStart && idx != 0 {
			return false
		}

		pos += idx + len(token)
	}

	if anchoredEnd {
		last := tokens[len(tokens)-1]
		if last == "" {
			return true
		}
		return strings.HasSuffix(value, last)
	}

	return true
}

// Publish emits an event to subscribers. It is non-blocking.
func (m *Manager) Publish(event Event) {
	if event.Type == "" {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.Source == "" {
		event.Source = "unknown"
	}

	atomic.AddUint64(&m.publishedEvents, 1)

	m.mu.RLock()
	running := m.running
	eventCh := m.eventCh
	m.mu.RUnlock()

	if !running || eventCh == nil {
		atomic.AddUint64(&m.droppedEvents, 1)
		return
	}

	select {
	case eventCh <- event:
	default:
		atomic.AddUint64(&m.droppedEvents, 1)
		m.cfg.Logger.Warn("Plugin event dropped because event buffer is full", map[string]interface{}{
			"event_type": event.Type,
			"source":     event.Source,
		})
	}
}

// ExecuteBeforeCommand runs command pre-hooks in deterministic priority order.
func (m *Manager) ExecuteBeforeCommand(ctx context.Context, req *CommandContext) (*CommandContext, error) {
	if req == nil {
		return nil, fmt.Errorf("command request cannot be nil")
	}

	current := req.Clone()
	for _, rt := range m.commandHooks() {
		cfg := rt.runtimeConfig()
		var nextReq *CommandContext
		err := m.invokeWithPolicy(ctx, rt, "before_command", func(callCtx context.Context) error {
			localReq := current.Clone()
			result, err := rt.commandHook.BeforeCommand(callCtx, localReq)
			nextReq = result
			return err
		})
		if err != nil {
			if !cfg.FailOpen {
				return nil, fmt.Errorf("plugin %s rejected command: %w", rt.metadata.Name, err)
			}
			continue
		}

		if nextReq != nil {
			current = nextReq.Clone()
		}
	}

	return current, nil
}

// ExecuteAfterCommand runs command post-hooks.
func (m *Manager) ExecuteAfterCommand(ctx context.Context, result *CommandResult) {
	if result == nil {
		return
	}

	for _, rt := range m.commandHooks() {
		localResult := *result
		if result.Request != nil {
			localResult.Request = result.Request.Clone()
		}

		_ = m.invokeWithPolicy(ctx, rt, "after_command", func(callCtx context.Context) error {
			return rt.commandHook.AfterCommand(callCtx, &localResult)
		})
	}
}

// WrapHTTP wraps an HTTP handler with plugin-provided middleware.
func (m *Manager) WrapHTTP(next http.Handler) http.Handler {
	if next == nil {
		return nil
	}

	middlewares := m.httpMiddlewares()
	if len(middlewares) == 0 {
		return next
	}

	wrapped := next
	for i := len(middlewares) - 1; i >= 0; i-- {
		rt := middlewares[i]

		func(runtime *pluginRuntime) {
			defer func() {
				if recovered := recover(); recovered != nil {
					runtime.recordInvocation(0, fmt.Errorf("panic in WrapHTTP: %v", recovered), true)
					m.cfg.Logger.Error("Plugin middleware panic", map[string]interface{}{
						"plugin": runtime.metadata.Name,
						"panic":  recovered,
					})
				}
			}()

			candidate := runtime.middleware.WrapHTTP(wrapped)
			if candidate != nil {
				wrapped = candidate
			}
		}(rt)
	}

	return wrapped
}

func (m *Manager) invokeWithPolicy(ctx context.Context, rt *pluginRuntime, stage string, fn func(context.Context) error) error {
	if rt == nil {
		return nil
	}

	cfg := rt.runtimeConfig()
	if !rt.isStarted() || !cfg.Enabled {
		return nil
	}

	if rt.isCircuitOpen(time.Now().UTC()) {
		m.cfg.Logger.Warn("Plugin skipped because circuit breaker is open", map[string]interface{}{
			"plugin": rt.metadata.Name,
			"stage":  stage,
		})
		return ErrCircuitOpen
	}

	start := time.Now()
	err, panicked := m.callWithTimeout(ctx, cfg.Timeout, fn)
	rt.recordInvocation(time.Since(start), err, panicked)

	if err != nil {
		m.cfg.Logger.Warn("Plugin hook returned error", map[string]interface{}{
			"plugin": rt.metadata.Name,
			"stage":  stage,
			"error":  err.Error(),
		})
	}

	return err
}

type callResult struct {
	err      error
	panicked bool
}

func (m *Manager) callWithTimeout(ctx context.Context, timeout time.Duration, fn func(context.Context) error) (error, bool) {
	if timeout <= 0 {
		timeout = m.cfg.DefaultTimeout
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan callResult, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- callResult{err: fmt.Errorf("panic: %v", recovered), panicked: true}
			}
		}()
		done <- callResult{err: fn(callCtx)}
	}()

	select {
	case result := <-done:
		return result.err, result.panicked
	case <-callCtx.Done():
		return fmt.Errorf("plugin timeout after %s: %w", timeout, callCtx.Err()), false
	}
}

func (m *Manager) commandHooks() []*pluginRuntime {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*pluginRuntime, 0)
	for _, rt := range m.plugins {
		if rt.commandHook != nil && rt.runtimeConfig().Enabled {
			result = append(result, rt)
		}
	}
	return result
}

func (m *Manager) httpMiddlewares() []*pluginRuntime {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*pluginRuntime, 0)
	for _, rt := range m.plugins {
		if rt.middleware != nil && rt.runtimeConfig().Enabled {
			result = append(result, rt)
		}
	}
	return result
}

func (m *Manager) eventSubscribers() []*pluginRuntime {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*pluginRuntime, 0)
	for _, rt := range m.plugins {
		if rt.eventSubscriber != nil && rt.runtimeConfig().Enabled {
			result = append(result, rt)
		}
	}
	return result
}

// SetEnabled enables/disables a plugin at runtime. When running, lifecycle hooks are invoked.
func (m *Manager) SetEnabled(ctx context.Context, name string, enabled bool) error {
	m.mu.RLock()
	rt, exists := m.byName[name]
	running := m.running
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("%w: %s", ErrPluginNotFound, name)
	}

	cfg := rt.runtimeConfig()
	if cfg.Enabled == enabled {
		return nil
	}

	cfg.Enabled = enabled
	rt.setRuntimeConfig(cfg)

	if !running {
		return nil
	}

	if enabled {
		return m.startPlugin(ctx, rt)
	}

	return m.stopPlugin(ctx, rt)
}

// Reload updates a plugin runtime config using ConfigReloader when supported.
func (m *Manager) Reload(ctx context.Context, name string, cfg RuntimeConfig) error {
	m.mu.RLock()
	rt, exists := m.byName[name]
	running := m.running
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("%w: %s", ErrPluginNotFound, name)
	}
	if rt.configReloader == nil {
		return fmt.Errorf("%w: %s", ErrPluginNotReloadable, name)
	}

	oldCfg := rt.runtimeConfig()
	cfg = m.applyRuntimeDefaults(cfg)

	err, panicked := m.callWithTimeout(ctx, cfg.Timeout, func(callCtx context.Context) error {
		return rt.configReloader.Reload(callCtx, cfg)
	})
	rt.recordInvocation(0, err, panicked)
	if err != nil {
		return fmt.Errorf("plugin reload failed (%s): %w", name, err)
	}

	rt.setRuntimeConfig(cfg)

	if !running {
		return nil
	}

	if oldCfg.Enabled && !cfg.Enabled {
		return m.stopPlugin(ctx, rt)
	}
	if !oldCfg.Enabled && cfg.Enabled {
		return m.startPlugin(ctx, rt)
	}

	return nil
}

// RuntimeConfig returns a copy of runtime config for a plugin.
func (m *Manager) RuntimeConfig(name string) (RuntimeConfig, bool) {
	m.mu.RLock()
	rt, exists := m.byName[name]
	m.mu.RUnlock()
	if !exists {
		return RuntimeConfig{}, false
	}
	return rt.runtimeConfig(), true
}

// HealthCheck gathers health snapshots for all loaded plugins.
func (m *Manager) HealthCheck(ctx context.Context) map[string]PluginHealth {
	m.mu.RLock()
	plugins := make([]*pluginRuntime, len(m.plugins))
	copy(plugins, m.plugins)
	m.mu.RUnlock()

	out := make(map[string]PluginHealth, len(plugins))
	now := time.Now().UTC()

	for _, rt := range plugins {
		cfg := rt.runtimeConfig()
		health := PluginHealth{
			Status:    HealthUnknown,
			Message:   "health check not implemented",
			Timestamp: now,
		}

		if !cfg.Enabled {
			health = PluginHealth{
				Status:    HealthUnknown,
				Message:   "plugin disabled",
				Timestamp: now,
			}
		} else if rt.healthChecker != nil {
			var checked PluginHealth
			err, panicked := m.callWithTimeout(ctx, cfg.Timeout, func(callCtx context.Context) error {
				var err error
				checked, err = rt.healthChecker.Health(callCtx)
				return err
			})
			rt.recordInvocation(0, err, panicked)
			if err != nil {
				health = PluginHealth{
					Status:    HealthUnhealthy,
					Message:   err.Error(),
					Timestamp: now,
				}
			} else {
				if checked.Status == "" {
					checked.Status = HealthHealthy
				}
				if checked.Timestamp.IsZero() {
					checked.Timestamp = now
				}
				health = checked
			}
		} else if rt.isStarted() {
			health = PluginHealth{
				Status:    HealthHealthy,
				Message:   "plugin running",
				Timestamp: now,
			}
		}

		rt.setHealth(health)
		out[rt.metadata.Name] = health
	}

	return out
}

// PluginNames returns loaded plugin names in execution order.
func (m *Manager) PluginNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.plugins))
	for _, rt := range m.plugins {
		names = append(names, rt.metadata.Name)
	}
	return names
}

// Stats returns manager and plugin runtime stats.
func (m *Manager) Stats() ManagerStats {
	m.mu.RLock()
	plugins := make([]*pluginRuntime, len(m.plugins))
	copy(plugins, m.plugins)
	running := m.running
	m.mu.RUnlock()

	stats := ManagerStats{
		PublishedEvents: atomic.LoadUint64(&m.publishedEvents),
		DroppedEvents:   atomic.LoadUint64(&m.droppedEvents),
		PluginCount:     len(plugins),
		Running:         running,
		Plugins:         make([]PluginStats, 0, len(plugins)),
	}

	for _, rt := range plugins {
		stats.Plugins = append(stats.Plugins, rt.snapshotStats())
	}

	return stats
}
