package plugin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var httpAccessRequestCounter uint64

// HTTPAccessPlugin adds request logging and optional event emission around HTTP handlers.
type HTTPAccessPlugin struct {
	mu sync.RWMutex

	host Host
	cfg  RuntimeConfig

	publishEvents    bool
	skipPaths        []string
	slowRequestMS    int
	includeUserAgent bool
	includeRemote    bool
	redactQuery      bool
	eventName        string
	emitRequestID    bool
	requestIDHeader  string
	allowedMethods   map[string]struct{}
}

// NewHTTPAccessPlugin creates an HTTP access middleware plugin.
func NewHTTPAccessPlugin() *HTTPAccessPlugin {
	return &HTTPAccessPlugin{}
}

func (p *HTTPAccessPlugin) Metadata() Metadata {
	return Metadata{
		Name:        "http_access",
		Version:     "1.0.0",
		Description: "HTTP access logging middleware with event emission",
		Author:      "Helios",
		Tags:        []string{"http", "observability", "middleware"},
	}
}

func (p *HTTPAccessPlugin) Init(_ context.Context, host Host, cfg RuntimeConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.host = host
	p.cfg = cfg
	p.applyConfigLocked(cfg)
	return nil
}

func (p *HTTPAccessPlugin) applyConfigLocked(cfg RuntimeConfig) {
	p.publishEvents = settingBool(cfg.Settings, "publish_events", true)
	p.skipPaths = settingStringSlice(cfg.Settings, "skip_paths", nil)
	p.slowRequestMS = settingInt(cfg.Settings, "slow_request_ms", 1000)
	if p.slowRequestMS <= 0 {
		p.slowRequestMS = 1000
	}
	p.includeUserAgent = settingBool(cfg.Settings, "include_user_agent", false)
	p.includeRemote = settingBool(cfg.Settings, "include_remote_addr", false)
	p.redactQuery = settingBool(cfg.Settings, "redact_query", true)
	p.eventName = settingString(cfg.Settings, "event_name", "http.request.completed")
	p.emitRequestID = settingBool(cfg.Settings, "emit_request_id", true)
	p.requestIDHeader = settingString(cfg.Settings, "request_id_header", "X-Request-ID")
	p.allowedMethods = toUpperSet(settingStringSlice(cfg.Settings, "methods", nil))
}

func (p *HTTPAccessPlugin) Start(_ context.Context) error { return nil }
func (p *HTTPAccessPlugin) Stop(_ context.Context) error  { return nil }

func (p *HTTPAccessPlugin) WrapHTTP(next http.Handler) http.Handler {
	if next == nil {
		return nil
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.RLock()
		publishEvents := p.publishEvents
		skipPaths := append([]string(nil), p.skipPaths...)
		slowRequestMS := p.slowRequestMS
		includeUserAgent := p.includeUserAgent
		includeRemote := p.includeRemote
		redactQuery := p.redactQuery
		eventName := p.eventName
		emitRequestID := p.emitRequestID
		requestIDHeader := p.requestIDHeader
		allowedMethods := cloneSet(p.allowedMethods)
		p.mu.RUnlock()

		if p.shouldSkipPath(r.URL.Path, skipPaths) {
			next.ServeHTTP(w, r)
			return
		}

		if !commandAllowed(allowedMethods, strings.ToUpper(r.Method)) {
			next.ServeHTTP(w, r)
			return
		}

		requestID := ""
		if emitRequestID {
			requestID = strings.TrimSpace(r.Header.Get(requestIDHeader))
			if requestID == "" {
				requestID = nextHTTPAccessRequestID()
			}
			w.Header().Set(requestIDHeader, requestID)
		}

		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(recorder, r)

		duration := time.Since(start)
		path := r.URL.Path
		if !redactQuery && strings.TrimSpace(r.URL.RawQuery) != "" {
			path = path + "?" + r.URL.RawQuery
		}
		fields := map[string]interface{}{
			"method":      r.Method,
			"path":        path,
			"status":      recorder.statusCode,
			"duration_ms": float64(duration.Microseconds()) / 1000.0,
			"bytes":       recorder.bytesWritten,
		}
		if includeUserAgent {
			fields["user_agent"] = r.UserAgent()
		}
		if includeRemote {
			fields["remote_addr"] = r.RemoteAddr
		}
		if requestID != "" {
			fields["request_id"] = requestID
		}

		if p.host != nil {
			if duration.Milliseconds() >= int64(slowRequestMS) {
				p.host.Logger().Warn("Slow HTTP request", fields)
			} else {
				p.host.Logger().Info("HTTP request", fields)
			}

			if publishEvents {
				p.host.Publish(NewEvent(eventName, "http_access", fields))
			}
		}
	})
}

func (p *HTTPAccessPlugin) shouldSkipPath(path string, skipPaths []string) bool {
	for _, skipped := range skipPaths {
		if skipped != "" && strings.HasPrefix(path, skipped) {
			return true
		}
	}
	return false
}

// Reload updates middleware settings at runtime.
func (p *HTTPAccessPlugin) Reload(_ context.Context, cfg RuntimeConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfg = cfg
	p.applyConfigLocked(cfg)
	return nil
}

// Health reports middleware plugin operational state.
func (p *HTTPAccessPlugin) Health(_ context.Context) (PluginHealth, error) {
	p.mu.RLock()
	eventName := p.eventName
	slowRequestMS := p.slowRequestMS
	p.mu.RUnlock()

	return PluginHealth{
		Status:    HealthHealthy,
		Message:   "http middleware active",
		Timestamp: time.Now().UTC(),
		Details: map[string]interface{}{
			"event_name":      eventName,
			"slow_request_ms": slowRequestMS,
		},
	}, nil
}

func nextHTTPAccessRequestID() string {
	seq := atomic.AddUint64(&httpAccessRequestCounter, 1)
	return fmt.Sprintf("hx-%d-%d", time.Now().UTC().UnixNano(), seq)
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	n, err := r.ResponseWriter.Write(data)
	r.bytesWritten += n
	return n, err
}

func init() {
	MustRegisterFactory("http_access", func() Plugin { return NewHTTPAccessPlugin() })
}
