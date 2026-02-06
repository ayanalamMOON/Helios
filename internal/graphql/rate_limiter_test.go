package graphql

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestDefaultRateLimitConfig(t *testing.T) {
	config := DefaultRateLimitConfig()

	if !config.Enabled {
		t.Error("Expected Enabled to be true")
	}
	if config.DefaultLimit != 60 {
		t.Errorf("Expected DefaultLimit 60, got %d", config.DefaultLimit)
	}
	if config.DefaultWindow != 60 {
		t.Errorf("Expected DefaultWindow 60, got %d", config.DefaultWindow)
	}
	if config.AnonymousMultiplier != 0.5 {
		t.Errorf("Expected AnonymousMultiplier 0.5, got %f", config.AnonymousMultiplier)
	}
	if !config.IncludeHeadersInResponse {
		t.Error("Expected IncludeHeadersInResponse to be true")
	}
	if !config.RejectOnExceed {
		t.Error("Expected RejectOnExceed to be true")
	}
}

func TestNewResolverRateLimiter(t *testing.T) {
	t.Run("with default config", func(t *testing.T) {
		rl := NewResolverRateLimiter(nil)
		defer rl.Close()

		if rl == nil {
			t.Fatal("Expected non-nil rate limiter")
		}
		if !rl.config.Enabled {
			t.Error("Expected rate limiter to be enabled")
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &RateLimitConfig{
			Enabled:      true,
			DefaultLimit: 100,
			FieldLimits:  make(map[string]*FieldRateLimit),
		}
		rl := NewResolverRateLimiter(config)
		defer rl.Close()

		if rl.config.DefaultLimit != 100 {
			t.Errorf("Expected DefaultLimit 100, got %d", rl.config.DefaultLimit)
		}
	})
}

func TestResolverRateLimiter_Check(t *testing.T) {
	config := &RateLimitConfig{
		Enabled:             true,
		DefaultLimit:        5,
		DefaultWindow:       60,
		AnonymousMultiplier: 0.5,
		FieldLimits:         make(map[string]*FieldRateLimit),
	}
	rl := NewResolverRateLimiter(config)
	defer rl.Close()

	ctx := context.Background()
	clientID := "test-client"

	t.Run("allows requests within limit", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			result := rl.Check(ctx, "Query.test", clientID, true)
			if !result.Allowed {
				t.Errorf("Request %d should be allowed", i+1)
			}
			if result.Remaining != 4-i {
				t.Errorf("Expected remaining %d, got %d", 4-i, result.Remaining)
			}
		}
	})

	t.Run("blocks requests exceeding limit", func(t *testing.T) {
		// Reset and make 5 requests
		rl.Reset("Query.test2", clientID)
		for i := 0; i < 5; i++ {
			rl.Check(ctx, "Query.test2", clientID, true)
		}

		// 6th request should be blocked
		result := rl.Check(ctx, "Query.test2", clientID, true)
		if result.Allowed {
			t.Error("Request exceeding limit should be blocked")
		}
		if result.RetryAfter <= 0 {
			t.Error("Expected positive RetryAfter")
		}
	})
}

func TestResolverRateLimiter_AnonymousMultiplier(t *testing.T) {
	config := &RateLimitConfig{
		Enabled:             true,
		DefaultLimit:        10,
		DefaultWindow:       60,
		AnonymousMultiplier: 0.5,
		FieldLimits:         make(map[string]*FieldRateLimit),
	}
	rl := NewResolverRateLimiter(config)
	defer rl.Close()

	ctx := context.Background()
	clientID := "anon-client"

	// Anonymous users get 50% of the limit (5 requests)
	for i := 0; i < 5; i++ {
		result := rl.Check(ctx, "Query.anonTest", clientID, false)
		if !result.Allowed {
			t.Errorf("Anonymous request %d should be allowed", i+1)
		}
	}

	// 6th request should be blocked for anonymous
	result := rl.Check(ctx, "Query.anonTest", clientID, false)
	if result.Allowed {
		t.Error("Anonymous request exceeding reduced limit should be blocked")
	}
}

func TestResolverRateLimiter_AuthenticatedLimit(t *testing.T) {
	config := &RateLimitConfig{
		Enabled:       true,
		DefaultLimit:  10,
		DefaultWindow: 60,
		FieldLimits: map[string]*FieldRateLimit{
			"Query.special": {
				Limit:              5,
				Window:             60,
				AuthenticatedLimit: 20,
			},
		},
	}
	rl := NewResolverRateLimiter(config)
	defer rl.Close()

	ctx := context.Background()

	// Authenticated user gets 20 requests
	for i := 0; i < 20; i++ {
		result := rl.Check(ctx, "Query.special", "auth-user", true)
		if !result.Allowed {
			t.Errorf("Authenticated request %d should be allowed", i+1)
		}
		if result.Limit != 20 {
			t.Errorf("Expected limit 20, got %d", result.Limit)
		}
	}

	// 21st should be blocked
	result := rl.Check(ctx, "Query.special", "auth-user", true)
	if result.Allowed {
		t.Error("Request exceeding authenticated limit should be blocked")
	}
}

func TestResolverRateLimiter_SkipAuthenticated(t *testing.T) {
	config := &RateLimitConfig{
		Enabled:       true,
		DefaultLimit:  5,
		DefaultWindow: 60,
		FieldLimits: map[string]*FieldRateLimit{
			"Query.health": {
				Limit:             5,
				Window:            60,
				SkipAuthenticated: true,
			},
		},
	}
	rl := NewResolverRateLimiter(config)
	defer rl.Close()

	ctx := context.Background()

	// Authenticated users should always be allowed
	for i := 0; i < 100; i++ {
		result := rl.Check(ctx, "Query.health", "auth-user", true)
		if !result.Allowed {
			t.Errorf("Authenticated request %d should be allowed with SkipAuthenticated", i+1)
		}
	}
}

func TestResolverRateLimiter_Disabled(t *testing.T) {
	config := &RateLimitConfig{
		Enabled:       false,
		DefaultLimit:  1,
		DefaultWindow: 60,
		FieldLimits:   make(map[string]*FieldRateLimit),
	}
	rl := NewResolverRateLimiter(config)
	defer rl.Close()

	ctx := context.Background()

	// All requests should be allowed when disabled
	for i := 0; i < 100; i++ {
		result := rl.Check(ctx, "Query.test", "client", true)
		if !result.Allowed {
			t.Errorf("Request %d should be allowed when rate limiting disabled", i+1)
		}
	}
}

func TestResolverRateLimiter_DisabledField(t *testing.T) {
	config := &RateLimitConfig{
		Enabled:       true,
		DefaultLimit:  5,
		DefaultWindow: 60,
		FieldLimits: map[string]*FieldRateLimit{
			"Query.internal": {
				Disabled: true,
			},
		},
	}
	rl := NewResolverRateLimiter(config)
	defer rl.Close()

	ctx := context.Background()

	// Field with Disabled=true should always allow
	for i := 0; i < 100; i++ {
		result := rl.Check(ctx, "Query.internal", "client", false)
		if !result.Allowed {
			t.Errorf("Request %d should be allowed for disabled field", i+1)
		}
	}
}

func TestResolverRateLimiter_BurstLimit(t *testing.T) {
	config := &RateLimitConfig{
		Enabled:       true,
		DefaultLimit:  5,
		DefaultWindow: 60,
		FieldLimits: map[string]*FieldRateLimit{
			"Mutation.login": {
				Limit:      5,
				Window:     60,
				BurstLimit: 3,
			},
		},
	}
	rl := NewResolverRateLimiter(config)
	defer rl.Close()

	ctx := context.Background()
	clientID := "burst-client"

	// Should allow 5 regular + 3 burst = 8 total
	for i := 0; i < 8; i++ {
		result := rl.Check(ctx, "Mutation.login", clientID, true)
		if !result.Allowed {
			t.Errorf("Request %d should be allowed (within burst)", i+1)
		}
	}

	// 9th should be blocked
	result := rl.Check(ctx, "Mutation.login", clientID, true)
	if result.Allowed {
		t.Error("Request exceeding burst limit should be blocked")
	}
}

func TestResolverRateLimiter_ConcurrentAccess(t *testing.T) {
	config := &RateLimitConfig{
		Enabled:       true,
		DefaultLimit:  100,
		DefaultWindow: 60,
		FieldLimits:   make(map[string]*FieldRateLimit),
	}
	rl := NewResolverRateLimiter(config)
	defer rl.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	allowed := make(chan bool, 200)

	// 10 goroutines making 20 requests each = 200 total
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			clientID := "concurrent-client"
			for j := 0; j < 20; j++ {
				result := rl.Check(ctx, "Query.concurrent", clientID, true)
				allowed <- result.Allowed
			}
		}(i)
	}

	wg.Wait()
	close(allowed)

	allowedCount := 0
	blockedCount := 0
	for a := range allowed {
		if a {
			allowedCount++
		} else {
			blockedCount++
		}
	}

	// Should allow exactly 100 requests
	if allowedCount != 100 {
		t.Errorf("Expected 100 allowed requests, got %d", allowedCount)
	}
	if blockedCount != 100 {
		t.Errorf("Expected 100 blocked requests, got %d", blockedCount)
	}
}

func TestResolverRateLimiter_Reset(t *testing.T) {
	config := &RateLimitConfig{
		Enabled:       true,
		DefaultLimit:  5,
		DefaultWindow: 60,
		FieldLimits:   make(map[string]*FieldRateLimit),
	}
	rl := NewResolverRateLimiter(config)
	defer rl.Close()

	ctx := context.Background()
	clientID := "reset-client"

	// Use up all requests
	for i := 0; i < 5; i++ {
		rl.Check(ctx, "Query.reset", clientID, true)
	}

	// Should be blocked
	result := rl.Check(ctx, "Query.reset", clientID, true)
	if result.Allowed {
		t.Error("Should be blocked before reset")
	}

	// Reset
	rl.Reset("Query.reset", clientID)

	// Should be allowed again
	result = rl.Check(ctx, "Query.reset", clientID, true)
	if !result.Allowed {
		t.Error("Should be allowed after reset")
	}
}

func TestResolverRateLimiter_ResetAll(t *testing.T) {
	config := &RateLimitConfig{
		Enabled:       true,
		DefaultLimit:  5,
		DefaultWindow: 60,
		FieldLimits:   make(map[string]*FieldRateLimit),
	}
	rl := NewResolverRateLimiter(config)
	defer rl.Close()

	ctx := context.Background()
	clientID := "resetall-client"

	// Use up requests on multiple fields
	for i := 0; i < 5; i++ {
		rl.Check(ctx, "Query.field1", clientID, true)
		rl.Check(ctx, "Query.field2", clientID, true)
	}

	// Both should be blocked
	if rl.Check(ctx, "Query.field1", clientID, true).Allowed {
		t.Error("field1 should be blocked")
	}
	if rl.Check(ctx, "Query.field2", clientID, true).Allowed {
		t.Error("field2 should be blocked")
	}

	// Reset all
	rl.ResetAll(clientID)

	// Both should be allowed
	if !rl.Check(ctx, "Query.field1", clientID, true).Allowed {
		t.Error("field1 should be allowed after ResetAll")
	}
	if !rl.Check(ctx, "Query.field2", clientID, true).Allowed {
		t.Error("field2 should be allowed after ResetAll")
	}
}

func TestResolverRateLimiter_SetFieldLimit(t *testing.T) {
	rl := NewResolverRateLimiter(DefaultRateLimitConfig())
	defer rl.Close()

	// Set custom limit
	rl.SetFieldLimit("Query.custom", &FieldRateLimit{
		Limit:  1000,
		Window: 60,
	})

	// Verify it was set
	limit := rl.GetFieldLimit("Query.custom")
	if limit.Limit != 1000 {
		t.Errorf("Expected limit 1000, got %d", limit.Limit)
	}
}

func TestResolverRateLimiter_GetStats(t *testing.T) {
	rl := NewResolverRateLimiter(DefaultRateLimitConfig())
	defer rl.Close()

	ctx := context.Background()

	// Make some requests
	for i := 0; i < 10; i++ {
		rl.Check(ctx, "Query.stats", "client1", true)
		rl.Check(ctx, "Query.stats", "client2", true)
	}

	stats := rl.GetStats()

	if stats["enabled"] != true {
		t.Error("Expected enabled to be true")
	}
	if stats["totalEntries"].(int) < 2 {
		t.Errorf("Expected at least 2 entries, got %d", stats["totalEntries"])
	}
}

func TestResolverRateLimiter_DefaultFieldLimits(t *testing.T) {
	rl := NewResolverRateLimiter(nil)
	defer rl.Close()

	// Check that default limits are set for common fields
	tests := []struct {
		field    string
		hasLimit bool
	}{
		{"Query.health", true},
		{"Query.me", true},
		{"Query.get", true},
		{"Mutation.register", true},
		{"Mutation.login", true},
		{"Mutation.set", true},
		{"Mutation.triggerRebalance", true},
	}

	for _, tt := range tests {
		limit := rl.GetFieldLimit(tt.field)
		if tt.hasLimit && limit.Limit == 0 {
			t.Errorf("Expected non-zero limit for %s", tt.field)
		}
	}
}

func TestAddRateLimitHeaders(t *testing.T) {
	w := httptest.NewRecorder()

	result := &RateLimitResult{
		Allowed:    true,
		Remaining:  50,
		Limit:      100,
		ResetAt:    time.Now().Add(time.Minute),
		RetryAfter: 0,
		Field:      "Query.test",
	}

	AddRateLimitHeaders(w, result)

	if w.Header().Get("X-RateLimit-Limit") != "100" {
		t.Errorf("Expected X-RateLimit-Limit 100, got %s", w.Header().Get("X-RateLimit-Limit"))
	}
	if w.Header().Get("X-RateLimit-Remaining") != "50" {
		t.Errorf("Expected X-RateLimit-Remaining 50, got %s", w.Header().Get("X-RateLimit-Remaining"))
	}
	if w.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("Expected X-RateLimit-Reset to be set")
	}
	if w.Header().Get("Retry-After") != "" {
		t.Error("Expected Retry-After to not be set for allowed request")
	}
}

func TestAddRateLimitHeaders_Blocked(t *testing.T) {
	w := httptest.NewRecorder()

	result := &RateLimitResult{
		Allowed:    false,
		Remaining:  0,
		Limit:      100,
		ResetAt:    time.Now().Add(time.Minute),
		RetryAfter: 60,
		Field:      "Query.test",
	}

	AddRateLimitHeaders(w, result)

	if w.Header().Get("Retry-After") != "60" {
		t.Errorf("Expected Retry-After 60, got %s", w.Header().Get("Retry-After"))
	}
}

func TestGetRateLimitExtension(t *testing.T) {
	result := &RateLimitResult{
		Allowed:   true,
		Remaining: 50,
		Limit:     100,
		ResetAt:   time.Now().Add(time.Minute),
		Field:     "Query.test",
	}

	ext := GetRateLimitExtension(result)

	if ext == nil {
		t.Fatal("Expected non-nil extension")
	}

	rateLimit, ok := ext["rateLimit"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected rateLimit in extension")
	}

	if rateLimit["limit"] != 100 {
		t.Errorf("Expected limit 100, got %v", rateLimit["limit"])
	}
	if rateLimit["remaining"] != 50 {
		t.Errorf("Expected remaining 50, got %v", rateLimit["remaining"])
	}
}

func TestContextWithRateLimit(t *testing.T) {
	ctx := context.Background()
	result := &RateLimitResult{
		Allowed:   true,
		Remaining: 50,
		Limit:     100,
		Field:     "Query.test",
	}

	ctx = ContextWithRateLimit(ctx, result)
	retrieved := RateLimitFromContext(ctx)

	if retrieved == nil {
		t.Fatal("Expected non-nil result from context")
	}
	if retrieved.Remaining != 50 {
		t.Errorf("Expected remaining 50, got %d", retrieved.Remaining)
	}
}

func TestRateLimitFromContext_Nil(t *testing.T) {
	ctx := context.Background()
	result := RateLimitFromContext(ctx)
	if result != nil {
		t.Error("Expected nil result from empty context")
	}
}

func TestExtractClientID(t *testing.T) {
	t.Run("authenticated user", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), "user_id", "user123")
		clientID := ExtractClientID(ctx, nil, true)
		if clientID != "user:user123" {
			t.Errorf("Expected 'user:user123', got '%s'", clientID)
		}
	})

	t.Run("anonymous with IP", func(t *testing.T) {
		ctx := context.Background()
		req := httptest.NewRequest("GET", "/graphql", nil)
		req.RemoteAddr = "192.168.1.1:12345"

		clientID := ExtractClientID(ctx, req, true)
		if clientID != "ip:192.168.1.1:12345" {
			t.Errorf("Expected 'ip:192.168.1.1:12345', got '%s'", clientID)
		}
	})

	t.Run("anonymous with X-Forwarded-For", func(t *testing.T) {
		ctx := context.Background()
		req := httptest.NewRequest("GET", "/graphql", nil)
		req.Header.Set("X-Forwarded-For", "10.0.0.1")

		clientID := ExtractClientID(ctx, req, true)
		if clientID != "ip:10.0.0.1" {
			t.Errorf("Expected 'ip:10.0.0.1', got '%s'", clientID)
		}
	})

	t.Run("anonymous without IP", func(t *testing.T) {
		ctx := context.Background()
		clientID := ExtractClientID(ctx, nil, false)
		if clientID != "anonymous" {
			t.Errorf("Expected 'anonymous', got '%s'", clientID)
		}
	})
}

func TestRateLimitError(t *testing.T) {
	result := &RateLimitResult{
		Allowed:    false,
		Remaining:  0,
		Limit:      10,
		ResetAt:    time.Now().Add(time.Minute),
		RetryAfter: 60,
		Field:      "Mutation.login",
	}

	err := NewRateLimitError(result)

	if err.Field != "Mutation.login" {
		t.Errorf("Expected field 'Mutation.login', got '%s'", err.Field)
	}
	if err.RetryAfter != 60 {
		t.Errorf("Expected RetryAfter 60, got %d", err.RetryAfter)
	}

	if !IsRateLimitError(err) {
		t.Error("Expected IsRateLimitError to return true")
	}

	// Test error message
	if err.Error() == "" {
		t.Error("Expected non-empty error message")
	}
}

func TestResolverRateLimiter_WindowReset(t *testing.T) {
	config := &RateLimitConfig{
		Enabled:       true,
		DefaultLimit:  5,
		DefaultWindow: 1, // 1 second window
		FieldLimits:   make(map[string]*FieldRateLimit),
	}
	rl := NewResolverRateLimiter(config)
	defer rl.Close()

	ctx := context.Background()
	clientID := "window-client"

	// Use up all requests
	for i := 0; i < 5; i++ {
		rl.Check(ctx, "Query.window", clientID, true)
	}

	// Should be blocked
	result := rl.Check(ctx, "Query.window", clientID, true)
	if result.Allowed {
		t.Error("Should be blocked")
	}

	// Wait for window to reset
	time.Sleep(1100 * time.Millisecond)

	// Should be allowed again
	result = rl.Check(ctx, "Query.window", clientID, true)
	if !result.Allowed {
		t.Error("Should be allowed after window reset")
	}
}

func BenchmarkResolverRateLimiter_Check(b *testing.B) {
	rl := NewResolverRateLimiter(DefaultRateLimitConfig())
	defer rl.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rl.Check(ctx, "Query.benchmark", "client", true)
	}
}

func BenchmarkResolverRateLimiter_CheckConcurrent(b *testing.B) {
	rl := NewResolverRateLimiter(DefaultRateLimitConfig())
	defer rl.Close()
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rl.Check(ctx, "Query.benchmark", "client", true)
		}
	})
}
