package graphql

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// GraphQL Rate Limiting per Resolver
// Provides fine-grained rate limiting at the resolver level with support for:
// - Per-field rate limits with different limits for each resolver
// - User-based rate limiting (authenticated vs anonymous)
// - Sliding window rate limiting algorithm
// - Rate limit headers in responses
// - Configurable limits via code or configuration

// RateLimitConfig defines the rate limiting configuration
type RateLimitConfig struct {
	// Enabled determines if rate limiting is active
	Enabled bool `json:"enabled" yaml:"enabled"`

	// DefaultLimit is the default requests per window for fields not explicitly configured
	DefaultLimit int `json:"defaultLimit" yaml:"default_limit"`

	// DefaultWindow is the default time window in seconds
	DefaultWindow int `json:"defaultWindow" yaml:"default_window"`

	// AnonymousMultiplier reduces limits for anonymous users (0.5 = 50% of limit)
	AnonymousMultiplier float64 `json:"anonymousMultiplier" yaml:"anonymous_multiplier"`

	// IncludeHeadersInResponse adds rate limit headers to HTTP response
	IncludeHeadersInResponse bool `json:"includeHeadersInResponse" yaml:"include_headers_in_response"`

	// RejectOnExceed determines if requests should be rejected when limit exceeded
	RejectOnExceed bool `json:"rejectOnExceed" yaml:"reject_on_exceed"`

	// FieldLimits contains per-field rate limit configurations
	FieldLimits map[string]*FieldRateLimit `json:"fieldLimits" yaml:"field_limits"`

	// IPBasedForAnonymous uses IP address for anonymous user identification
	IPBasedForAnonymous bool `json:"ipBasedForAnonymous" yaml:"ip_based_for_anonymous"`
}

// FieldRateLimit defines rate limit for a specific field
type FieldRateLimit struct {
	// Limit is the number of requests allowed per window
	Limit int `json:"limit" yaml:"limit"`

	// Window is the time window in seconds
	Window int `json:"window" yaml:"window"`

	// AuthenticatedLimit is the limit for authenticated users (optional, uses Limit if not set)
	AuthenticatedLimit int `json:"authenticatedLimit" yaml:"authenticated_limit"`

	// BurstLimit allows temporary burst above the normal limit
	BurstLimit int `json:"burstLimit" yaml:"burst_limit"`

	// SkipAuthenticated skips rate limiting for authenticated users
	SkipAuthenticated bool `json:"skipAuthenticated" yaml:"skip_authenticated"`

	// Disabled completely disables rate limiting for this field
	Disabled bool `json:"disabled" yaml:"disabled"`
}

// DefaultRateLimitConfig returns the default rate limit configuration
func DefaultRateLimitConfig() *RateLimitConfig {
	return &RateLimitConfig{
		Enabled:                  true,
		DefaultLimit:             60,       // 60 requests
		DefaultWindow:            60,       // per minute
		AnonymousMultiplier:      0.5,      // 50% for anonymous
		IncludeHeadersInResponse: true,
		RejectOnExceed:           true,
		IPBasedForAnonymous:      true,
		FieldLimits:              make(map[string]*FieldRateLimit),
	}
}

// RateLimitResult contains the result of a rate limit check
type RateLimitResult struct {
	// Allowed indicates if the request is allowed
	Allowed bool `json:"allowed"`

	// Remaining is the number of requests remaining in the window
	Remaining int `json:"remaining"`

	// Limit is the total limit for the window
	Limit int `json:"limit"`

	// ResetAt is when the rate limit window resets
	ResetAt time.Time `json:"resetAt"`

	// RetryAfter is seconds until the client should retry (if rate limited)
	RetryAfter int `json:"retryAfter,omitempty"`

	// Field is the resolver field that was rate limited
	Field string `json:"field"`

	// ClientID is the identifier used for rate limiting
	ClientID string `json:"clientId"`
}

// rateLimitEntry represents a single rate limit entry in the sliding window
type rateLimitEntry struct {
	Count      int
	WindowEnd  time.Time
	BurstUsed  int
	LastAccess time.Time
}

// ResolverRateLimiter handles per-resolver rate limiting
type ResolverRateLimiter struct {
	config  *RateLimitConfig
	entries map[string]*rateLimitEntry
	mu      sync.RWMutex
	cleanup *time.Ticker
	done    chan struct{}
}

// NewResolverRateLimiter creates a new resolver rate limiter
func NewResolverRateLimiter(config *RateLimitConfig) *ResolverRateLimiter {
	if config == nil {
		config = DefaultRateLimitConfig()
	}

	rl := &ResolverRateLimiter{
		config:  config,
		entries: make(map[string]*rateLimitEntry),
		done:    make(chan struct{}),
	}

	// Initialize default field limits
	rl.initializeDefaultLimits()

	// Start cleanup goroutine
	rl.cleanup = time.NewTicker(time.Minute)
	go rl.cleanupRoutine()

	return rl
}

// initializeDefaultLimits sets up default rate limits for GraphQL fields
func (rl *ResolverRateLimiter) initializeDefaultLimits() {
	if rl.config.FieldLimits == nil {
		rl.config.FieldLimits = make(map[string]*FieldRateLimit)
	}

	// Helper function to set default only if not already set
	setDefault := func(field string, limit *FieldRateLimit) {
		if _, exists := rl.config.FieldLimits[field]; !exists {
			rl.config.FieldLimits[field] = limit
		}
	}

	// Query rate limits
	setDefault("Query.health", &FieldRateLimit{
		Limit: 120, Window: 60, SkipAuthenticated: true,
	})
	setDefault("Query.me", &FieldRateLimit{
		Limit: 60, Window: 60,
	})
	setDefault("Query.get", &FieldRateLimit{
		Limit: 100, Window: 60, AuthenticatedLimit: 200,
	})
	setDefault("Query.keys", &FieldRateLimit{
		Limit: 30, Window: 60, AuthenticatedLimit: 60,
	})
	setDefault("Query.exists", &FieldRateLimit{
		Limit: 100, Window: 60, AuthenticatedLimit: 200,
	})
	setDefault("Query.job", &FieldRateLimit{
		Limit: 60, Window: 60,
	})
	setDefault("Query.jobs", &FieldRateLimit{
		Limit: 20, Window: 60, AuthenticatedLimit: 40,
	})
	setDefault("Query.shardNodes", &FieldRateLimit{
		Limit: 30, Window: 60,
	})
	setDefault("Query.shardStats", &FieldRateLimit{
		Limit: 30, Window: 60,
	})
	setDefault("Query.raftStatus", &FieldRateLimit{
		Limit: 30, Window: 60,
	})
	setDefault("Query.raftPeers", &FieldRateLimit{
		Limit: 30, Window: 60,
	})
	setDefault("Query.clusterStatus", &FieldRateLimit{
		Limit: 20, Window: 60,
	})
	setDefault("Query.users", &FieldRateLimit{
		Limit: 20, Window: 60, AuthenticatedLimit: 40,
	})
	setDefault("Query.metrics", &FieldRateLimit{
		Limit: 30, Window: 60,
	})
	setDefault("Query.queryCostConfig", &FieldRateLimit{
		Limit: 30, Window: 60,
	})
	setDefault("Query.estimateQueryCost", &FieldRateLimit{
		Limit: 30, Window: 60,
	})
	setDefault("Query.rateLimitConfig", &FieldRateLimit{
		Limit: 30, Window: 60,
	})
	setDefault("Query.rateLimitStatus", &FieldRateLimit{
		Limit: 30, Window: 60,
	})
	setDefault("Query.fieldRateLimits", &FieldRateLimit{
		Limit: 30, Window: 60,
	})

	// Mutation rate limits (stricter)
	setDefault("Mutation.register", &FieldRateLimit{
		Limit: 5, Window: 60, BurstLimit: 10, // Very strict for registration
	})
	setDefault("Mutation.login", &FieldRateLimit{
		Limit: 10, Window: 60, BurstLimit: 20, // Strict for login
	})
	setDefault("Mutation.logout", &FieldRateLimit{
		Limit: 30, Window: 60,
	})
	setDefault("Mutation.refreshToken", &FieldRateLimit{
		Limit: 20, Window: 60,
	})
	setDefault("Mutation.set", &FieldRateLimit{
		Limit: 60, Window: 60, AuthenticatedLimit: 120,
	})
	setDefault("Mutation.delete", &FieldRateLimit{
		Limit: 60, Window: 60, AuthenticatedLimit: 120,
	})
	setDefault("Mutation.expire", &FieldRateLimit{
		Limit: 60, Window: 60, AuthenticatedLimit: 120,
	})
	setDefault("Mutation.enqueueJob", &FieldRateLimit{
		Limit: 30, Window: 60, AuthenticatedLimit: 60,
	})
	setDefault("Mutation.cancelJob", &FieldRateLimit{
		Limit: 30, Window: 60,
	})
	setDefault("Mutation.retryJob", &FieldRateLimit{
		Limit: 30, Window: 60,
	})

	// Admin mutations (very strict)
	setDefault("Mutation.addShardNode", &FieldRateLimit{
		Limit: 5, Window: 60, SkipAuthenticated: false,
	})
	setDefault("Mutation.removeShardNode", &FieldRateLimit{
		Limit: 5, Window: 60,
	})
	setDefault("Mutation.triggerRebalance", &FieldRateLimit{
		Limit: 2, Window: 60, // Very limited
	})
	setDefault("Mutation.cancelMigration", &FieldRateLimit{
		Limit: 10, Window: 60,
	})
	setDefault("Mutation.cleanupMigrations", &FieldRateLimit{
		Limit: 5, Window: 60,
	})
	setDefault("Mutation.addRaftPeer", &FieldRateLimit{
		Limit: 3, Window: 60,
	})
	setDefault("Mutation.removeRaftPeer", &FieldRateLimit{
		Limit: 3, Window: 60,
	})
	setDefault("Mutation.createUser", &FieldRateLimit{
		Limit: 10, Window: 60,
	})
	setDefault("Mutation.updateUser", &FieldRateLimit{
		Limit: 20, Window: 60,
	})
	setDefault("Mutation.deleteUser", &FieldRateLimit{
		Limit: 10, Window: 60,
	})
	setDefault("Mutation.assignRole", &FieldRateLimit{
		Limit: 20, Window: 60,
	})
	setDefault("Mutation.revokeRole", &FieldRateLimit{
		Limit: 20, Window: 60,
	})
	setDefault("Mutation.createRole", &FieldRateLimit{
		Limit: 10, Window: 60,
	})
	setDefault("Mutation.updateRole", &FieldRateLimit{
		Limit: 20, Window: 60,
	})
	setDefault("Mutation.deleteRole", &FieldRateLimit{
		Limit: 10, Window: 60,
	})

	// Subscription rate limits
	setDefault("Subscription.jobUpdated", &FieldRateLimit{
		Limit: 10, Window: 60,
	})
	setDefault("Subscription.migrationProgress", &FieldRateLimit{
		Limit: 10, Window: 60,
	})
	setDefault("Subscription.clusterEvent", &FieldRateLimit{
		Limit: 5, Window: 60,
	})
	setDefault("Subscription.keyChanged", &FieldRateLimit{
		Limit: 10, Window: 60,
	})
}

// cleanupRoutine removes expired entries
func (rl *ResolverRateLimiter) cleanupRoutine() {
	for {
		select {
		case <-rl.cleanup.C:
			rl.cleanupExpired()
		case <-rl.done:
			rl.cleanup.Stop()
			return
		}
	}
}

// cleanupExpired removes entries that have expired
func (rl *ResolverRateLimiter) cleanupExpired() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	expiredKeys := make([]string, 0)

	for key, entry := range rl.entries {
		// Remove entries that haven't been accessed in 5 minutes
		if now.Sub(entry.LastAccess) > 5*time.Minute {
			expiredKeys = append(expiredKeys, key)
		}
	}

	for _, key := range expiredKeys {
		delete(rl.entries, key)
	}
}

// Close stops the rate limiter cleanup routine
func (rl *ResolverRateLimiter) Close() {
	close(rl.done)
}

// Check performs a rate limit check for a field
func (rl *ResolverRateLimiter) Check(ctx context.Context, field string, clientID string, isAuthenticated bool) *RateLimitResult {
	if !rl.config.Enabled {
		return &RateLimitResult{Allowed: true, Field: field, ClientID: clientID}
	}

	// Get field limit configuration
	fieldLimit := rl.getFieldLimit(field)
	if fieldLimit.Disabled {
		return &RateLimitResult{Allowed: true, Field: field, ClientID: clientID}
	}

	// Skip for authenticated users if configured
	if isAuthenticated && fieldLimit.SkipAuthenticated {
		return &RateLimitResult{Allowed: true, Field: field, ClientID: clientID}
	}

	// Determine the limit to use
	limit := fieldLimit.Limit
	if isAuthenticated && fieldLimit.AuthenticatedLimit > 0 {
		limit = fieldLimit.AuthenticatedLimit
	} else if !isAuthenticated {
		limit = int(float64(limit) * rl.config.AnonymousMultiplier)
		if limit < 1 {
			limit = 1
		}
	}

	window := time.Duration(fieldLimit.Window) * time.Second
	burstLimit := fieldLimit.BurstLimit

	// Create entry key
	entryKey := fmt.Sprintf("%s:%s", field, clientID)

	// Check rate limit
	result := rl.checkLimit(entryKey, limit, window, burstLimit)
	result.Field = field
	result.ClientID = clientID

	return result
}

// checkLimit performs the actual rate limit check using sliding window
func (rl *ResolverRateLimiter) checkLimit(key string, limit int, window time.Duration, burstLimit int) *RateLimitResult {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	entry, exists := rl.entries[key]

	if !exists || now.After(entry.WindowEnd) {
		// Create new window
		entry = &rateLimitEntry{
			Count:      1,
			WindowEnd:  now.Add(window),
			BurstUsed:  0,
			LastAccess: now,
		}
		rl.entries[key] = entry

		return &RateLimitResult{
			Allowed:   true,
			Remaining: limit - 1,
			Limit:     limit,
			ResetAt:   entry.WindowEnd,
		}
	}

	entry.LastAccess = now

	// Check if within limit
	if entry.Count < limit {
		entry.Count++
		return &RateLimitResult{
			Allowed:   true,
			Remaining: limit - entry.Count,
			Limit:     limit,
			ResetAt:   entry.WindowEnd,
		}
	}

	// Check burst allowance
	if burstLimit > 0 && entry.BurstUsed < burstLimit {
		entry.BurstUsed++
		entry.Count++
		return &RateLimitResult{
			Allowed:   true,
			Remaining: 0,
			Limit:     limit,
			ResetAt:   entry.WindowEnd,
		}
	}

	// Rate limited
	retryAfter := int(entry.WindowEnd.Sub(now).Seconds())
	if retryAfter < 1 {
		retryAfter = 1
	}

	return &RateLimitResult{
		Allowed:    false,
		Remaining:  0,
		Limit:      limit,
		ResetAt:    entry.WindowEnd,
		RetryAfter: retryAfter,
	}
}

// getFieldLimit returns the rate limit configuration for a field
func (rl *ResolverRateLimiter) getFieldLimit(field string) *FieldRateLimit {
	if limit, ok := rl.config.FieldLimits[field]; ok {
		return limit
	}

	// Return default limit
	return &FieldRateLimit{
		Limit:  rl.config.DefaultLimit,
		Window: rl.config.DefaultWindow,
	}
}

// SetFieldLimit sets a custom rate limit for a field
func (rl *ResolverRateLimiter) SetFieldLimit(field string, limit *FieldRateLimit) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.config.FieldLimits[field] = limit
}

// GetFieldLimit returns the current limit configuration for a field
func (rl *ResolverRateLimiter) GetFieldLimit(field string) *FieldRateLimit {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.getFieldLimit(field)
}

// GetConfig returns the current configuration
func (rl *ResolverRateLimiter) GetConfig() *RateLimitConfig {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.config
}

// UpdateConfig updates the rate limit configuration
func (rl *ResolverRateLimiter) UpdateConfig(config *RateLimitConfig) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.config = config
}

// Reset resets the rate limit for a specific client and field
func (rl *ResolverRateLimiter) Reset(field, clientID string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	key := fmt.Sprintf("%s:%s", field, clientID)
	delete(rl.entries, key)
}

// ResetAll resets all rate limits for a client
func (rl *ResolverRateLimiter) ResetAll(clientID string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	keysToDelete := make([]string, 0)
	suffix := ":" + clientID
	for key := range rl.entries {
		if len(key) > len(suffix) && key[len(key)-len(suffix):] == suffix {
			keysToDelete = append(keysToDelete, key)
		}
	}

	for _, key := range keysToDelete {
		delete(rl.entries, key)
	}
}

// GetStats returns statistics about rate limiting
func (rl *ResolverRateLimiter) GetStats() map[string]interface{} {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	stats := map[string]interface{}{
		"enabled":       rl.config.Enabled,
		"totalEntries":  len(rl.entries),
		"defaultLimit":  rl.config.DefaultLimit,
		"defaultWindow": rl.config.DefaultWindow,
		"fieldLimits":   len(rl.config.FieldLimits),
	}

	return stats
}

// RateLimitError represents a rate limit error
type RateLimitError struct {
	Message    string
	Field      string
	Limit      int
	Remaining  int
	ResetAt    time.Time
	RetryAfter int
}

func (e *RateLimitError) Error() string {
	return e.Message
}

// IsRateLimitError checks if an error is a rate limit error
func IsRateLimitError(err error) bool {
	_, ok := err.(*RateLimitError)
	return ok
}

// NewRateLimitError creates a new rate limit error from a result
func NewRateLimitError(result *RateLimitResult) *RateLimitError {
	return &RateLimitError{
		Message:    fmt.Sprintf("Rate limit exceeded for %s. Try again in %d seconds", result.Field, result.RetryAfter),
		Field:      result.Field,
		Limit:      result.Limit,
		Remaining:  result.Remaining,
		ResetAt:    result.ResetAt,
		RetryAfter: result.RetryAfter,
	}
}

// AddRateLimitHeaders adds rate limit headers to HTTP response
func AddRateLimitHeaders(w http.ResponseWriter, result *RateLimitResult) {
	if result == nil {
		return
	}

	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))

	if !result.Allowed {
		w.Header().Set("Retry-After", strconv.Itoa(result.RetryAfter))
	}
}

// GetRateLimitExtension returns rate limit info for GraphQL response extensions
func GetRateLimitExtension(result *RateLimitResult) map[string]interface{} {
	if result == nil {
		return nil
	}

	return map[string]interface{}{
		"rateLimit": map[string]interface{}{
			"limit":     result.Limit,
			"remaining": result.Remaining,
			"resetAt":   result.ResetAt.Format(time.RFC3339),
			"field":     result.Field,
		},
	}
}

// rateLimitContextKey is the context key for rate limit results
type rateLimitContextKey struct{}

// ContextWithRateLimit adds rate limit result to context
func ContextWithRateLimit(ctx context.Context, result *RateLimitResult) context.Context {
	return context.WithValue(ctx, rateLimitContextKey{}, result)
}

// RateLimitFromContext retrieves rate limit result from context
func RateLimitFromContext(ctx context.Context) *RateLimitResult {
	if result, ok := ctx.Value(rateLimitContextKey{}).(*RateLimitResult); ok {
		return result
	}
	return nil
}

// ExtractClientID extracts the client identifier from the request
func ExtractClientID(ctx context.Context, r *http.Request, useIP bool) string {
	// First try to get authenticated user ID
	if userID, ok := ctx.Value("user_id").(string); ok && userID != "" {
		return "user:" + userID
	}

	// Fall back to IP-based identification
	if useIP && r != nil {
		// Try X-Forwarded-For header first
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return "ip:" + xff
		}
		// Fall back to RemoteAddr
		return "ip:" + r.RemoteAddr
	}

	return "anonymous"
}
