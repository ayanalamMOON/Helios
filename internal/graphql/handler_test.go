package graphql

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/helios/helios/internal/atlas"
	"github.com/helios/helios/internal/atlas/aof"
	"github.com/helios/helios/internal/auth"
)

func createTestHandler(t *testing.T) (*Handler, *Resolver) {
	// Create in-memory atlas
	cfg := &atlas.Config{
		DataDir:          t.TempDir(),
		AOFSyncMode:      aof.SyncEvery,
		SnapshotInterval: time.Hour,
	}

	atlasStore, err := atlas.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create atlas: %v", err)
	}
	t.Cleanup(func() {
		atlasStore.Close()
	})

	authService := auth.NewService(atlasStore)

	resolver := NewResolver(
		atlasStore,
		nil,
		authService,
		nil,
		nil,
		nil,
	)

	handler := NewHandler(resolver, authService)

	return handler, resolver
}

func TestHandlerHealthQuery(t *testing.T) {
	handler, _ := createTestHandler(t)

	req := GraphQLRequest{
		Query: `query { health { status timestamp checks } }`,
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(response.Errors) > 0 {
		t.Errorf("GraphQL errors: %+v", response.Errors)
	}

	if response.Data == nil {
		t.Errorf("Expected data in response, got body: %s", recorder.Body.String())
	}
}

func TestHandlerMetricsQuery(t *testing.T) {
	handler, _ := createTestHandler(t)

	req := GraphQLRequest{
		Query: `query { metrics { timestamp data } }`,
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(response.Errors) > 0 {
		t.Errorf("GraphQL errors: %+v", response.Errors)
	}

	if response.Data == nil {
		t.Errorf("Expected data in response, got body: %s", recorder.Body.String())
	}
}

func TestHandlerAuthenticatedMeQuery(t *testing.T) {
	handler, resolver := createTestHandler(t)

	// Create a user and token
	user, _ := resolver.authService.CreateUser("testuser", "password123")
	token, _ := resolver.authService.CreateToken(user.ID, time.Hour)

	req := GraphQLRequest{
		Query: `query { me { id username } }`,
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token.TokenHash)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(response.Errors) > 0 {
		t.Errorf("GraphQL errors: %+v", response.Errors)
	}

	if response.Data == nil {
		t.Errorf("Expected data in response, got body: %s", recorder.Body.String())
	}
}

func TestHandlerRegisterMutation(t *testing.T) {
	handler, _ := createTestHandler(t)

	req := GraphQLRequest{
		Query: `mutation($input: RegisterInput!) { register(input: $input) { token user { username } } }`,
		Variables: map[string]interface{}{
			"input": map[string]interface{}{
				"username": "newuser",
				"password": "password123",
			},
		},
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(response.Errors) > 0 {
		t.Errorf("GraphQL errors: %+v", response.Errors)
	}

	if response.Data == nil {
		t.Errorf("Expected data in response, got body: %s", recorder.Body.String())
	}
}

func TestHandlerLoginMutation(t *testing.T) {
	handler, resolver := createTestHandler(t)

	// Create a user first
	resolver.authService.CreateUser("loginuser", "password123")

	req := GraphQLRequest{
		Query: `mutation($input: LoginInput!) { login(input: $input) { token user { username } } }`,
		Variables: map[string]interface{}{
			"input": map[string]interface{}{
				"username": "loginuser",
				"password": "password123",
			},
		},
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(response.Errors) > 0 {
		t.Errorf("GraphQL errors: %+v", response.Errors)
	}

	if response.Data == nil {
		t.Errorf("Expected data in response, got body: %s", recorder.Body.String())
	}
}

func TestHandlerSetMutation(t *testing.T) {
	handler, _ := createTestHandler(t)

	req := GraphQLRequest{
		Query: `mutation($input: SetInput!) { set(input: $input) { key value } }`,
		Variables: map[string]interface{}{
			"input": map[string]interface{}{
				"key":   "test-key",
				"value": "test-value",
			},
		},
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(response.Errors) > 0 {
		t.Errorf("GraphQL errors: %+v", response.Errors)
	}

	if response.Data == nil {
		t.Errorf("Expected data in response, got body: %s", recorder.Body.String())
	}
}

func TestHandlerDeleteMutation(t *testing.T) {
	handler, resolver := createTestHandler(t)

	// Set a key first
	resolver.atlasStore.Set("delete-test", []byte("value"), 0)

	req := GraphQLRequest{
		Query: `mutation($key: String!) { delete(key: $key) }`,
		Variables: map[string]interface{}{
			"key": "delete-test",
		},
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response.Data == nil {
		t.Error("Expected data in response")
	}
}

func TestHandlerCORS(t *testing.T) {
	handler, _ := createTestHandler(t)

	httpReq := httptest.NewRequest("OPTIONS", "/graphql", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200 for OPTIONS, got %d", recorder.Code)
	}

	corsHeader := recorder.Header().Get("Access-Control-Allow-Origin")
	if corsHeader != "*" {
		t.Errorf("Expected CORS header '*', got '%s'", corsHeader)
	}
}

func TestHandlerInvalidJSON(t *testing.T) {
	handler, _ := createTestHandler(t)

	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBufferString("invalid json"))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for invalid JSON, got %d", recorder.Code)
	}
}

func TestHandlerMethodNotAllowed(t *testing.T) {
	handler, _ := createTestHandler(t)

	httpReq := httptest.NewRequest("PUT", "/graphql", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405 for PUT method, got %d", recorder.Code)
	}
}

func TestHandlerIntrospection(t *testing.T) {
	handler, _ := createTestHandler(t)

	req := GraphQLRequest{
		Query: `query { __schema { queryType { name } } }`,
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response.Data == nil {
		t.Error("Expected data in response for introspection")
	}
}

func TestPlaygroundHandler(t *testing.T) {
	handler := PlaygroundHandler()

	httpReq := httptest.NewRequest("GET", "/graphql/playground", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	contentType := recorder.Header().Get("Content-Type")
	if contentType != "text/html" {
		t.Errorf("Expected Content-Type 'text/html', got '%s'", contentType)
	}

	body := recorder.Body.String()
	if !bytes.Contains(recorder.Body.Bytes(), []byte("GraphQL Playground")) {
		t.Error("Expected playground HTML content")
	}

	if len(body) == 0 {
		t.Error("Expected non-empty playground HTML")
	}
}

func TestHandlerQueryCostConfig(t *testing.T) {
	handler, _ := createTestHandler(t)

	req := GraphQLRequest{
		Query: `query { queryCostConfig { enabled maxComplexity maxDepth rejectOnExceed } }`,
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(response.Errors) > 0 {
		t.Errorf("GraphQL errors: %+v", response.Errors)
	}

	data, ok := response.Data.(map[string]interface{})
	if !ok {
		t.Fatal("Expected data map in response")
	}

	config, ok := data["queryCostConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected queryCostConfig in response, got: %+v", data)
	}

	// Check Enabled field (JSON uses capitalized field names from struct)
	enabled, ok := config["Enabled"].(bool)
	if !ok {
		// Try lowercase (might vary by JSON encoder behavior)
		enabled, ok = config["enabled"].(bool)
	}
	if !enabled {
		t.Errorf("Expected cost analysis to be enabled by default, config: %+v", config)
	}

	// Check MaxComplexity - int32 comes through as float64 in JSON
	maxComplexity, ok := config["MaxComplexity"].(float64)
	if !ok {
		maxComplexity, ok = config["maxComplexity"].(float64)
	}
	if !ok || maxComplexity != 1000 {
		t.Errorf("Expected maxComplexity 1000, got %v (config: %+v)", maxComplexity, config)
	}
}

func TestHandlerCostAnalysisInResponse(t *testing.T) {
	handler, _ := createTestHandler(t)

	// Configure to include cost in response
	handler.costAnalyzer.config.IncludeCostInResponse = true

	req := GraphQLRequest{
		Query: `query { health { status } }`,
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(response.Errors) > 0 {
		t.Errorf("GraphQL errors: %+v", response.Errors)
	}

	// Check for cost extension
	if response.Extensions == nil {
		t.Fatal("Expected extensions in response")
	}

	cost, ok := response.Extensions["cost"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected cost in extensions")
	}

	if cost["totalCost"] == nil {
		t.Error("Expected totalCost in cost extension")
	}
}

func TestHandlerCostExceeded(t *testing.T) {
	handler, _ := createTestHandler(t)

	// Set very low complexity limit
	handler.costAnalyzer.UpdateConfig(&CostConfig{
		MaxComplexity:         5,
		MaxDepth:              10,
		DefaultFieldCost:      1,
		Enabled:               true,
		RejectOnExceed:        true,
		IncludeCostInResponse: true,
	})

	// Query that exceeds the limit (metrics has cost 5, plus nested fields)
	req := GraphQLRequest{
		Query: `query { metrics { requestsTotal requestsPerSecond } health { status } }`,
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	// GraphQL always returns 200, errors are in the body
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Should have error about complexity
	if len(response.Errors) == 0 {
		t.Fatal("Expected error for exceeding complexity")
	}

	// Check error has proper extension
	if response.Errors[0].Extensions == nil {
		t.Fatal("Expected extensions in error")
	}

	code, ok := response.Errors[0].Extensions["code"].(string)
	if !ok || code != "QUERY_COMPLEXITY_EXCEEDED" {
		t.Errorf("Expected error code QUERY_COMPLEXITY_EXCEEDED, got %v", code)
	}
}

func TestHandlerCostDisabled(t *testing.T) {
	handler, _ := createTestHandler(t)

	// Disable cost analysis
	handler.EnableCostAnalysis(false)

	// Query should work without cost checking
	req := GraphQLRequest{
		Query: `query { health { status } }`,
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(response.Errors) > 0 {
		t.Errorf("GraphQL errors: %+v", response.Errors)
	}

	// No extensions when disabled
	if response.Extensions != nil && response.Extensions["cost"] != nil {
		t.Error("Expected no cost extension when disabled")
	}
}

func TestHandlerWithCostConfig(t *testing.T) {
	// Create handler with custom cost config
	cfg := &atlas.Config{
		DataDir:          t.TempDir(),
		AOFSyncMode:      aof.SyncEvery,
		SnapshotInterval: time.Hour,
	}

	atlasStore, err := atlas.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create atlas: %v", err)
	}
	t.Cleanup(func() {
		atlasStore.Close()
	})

	authService := auth.NewService(atlasStore)
	resolver := NewResolver(atlasStore, nil, authService, nil, nil, nil)

	customConfig := &CostConfig{
		MaxComplexity:         500,
		MaxDepth:              5,
		DefaultFieldCost:      2,
		Enabled:               true,
		RejectOnExceed:        true,
		IncludeCostInResponse: true,
	}

	handler := NewHandler(resolver, authService, WithCostConfig(customConfig))

	// Verify config was applied
	if handler.costAnalyzer.config.MaxComplexity != 500 {
		t.Errorf("Expected MaxComplexity 500, got %d", handler.costAnalyzer.config.MaxComplexity)
	}
	if handler.costAnalyzer.config.MaxDepth != 5 {
		t.Errorf("Expected MaxDepth 5, got %d", handler.costAnalyzer.config.MaxDepth)
	}
}

// Rate Limiting Handler Integration Tests

func TestHandlerRateLimitingHeaders(t *testing.T) {
	handler, _ := createTestHandler(t)

	// First request should include rate limit headers
	req := GraphQLRequest{
		Query: `query { health { status } }`,
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.RemoteAddr = "192.168.1.1:12345"

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	// Check rate limit headers
	if recorder.Header().Get("X-RateLimit-Limit") == "" {
		t.Error("Expected X-RateLimit-Limit header")
	}
	if recorder.Header().Get("X-RateLimit-Remaining") == "" {
		t.Error("Expected X-RateLimit-Remaining header")
	}
	if recorder.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("Expected X-RateLimit-Reset header")
	}
}

func TestHandlerRateLimitingExtension(t *testing.T) {
	handler, _ := createTestHandler(t)

	req := GraphQLRequest{
		Query: `query { health { status } }`,
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.RemoteAddr = "192.168.1.2:12345"

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Check rate limit extension
	if response.Extensions == nil {
		t.Fatal("Expected extensions in response")
	}

	rateLimit, ok := response.Extensions["rateLimit"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected rateLimit in extensions")
	}

	if _, ok := rateLimit["limit"]; !ok {
		t.Error("Expected limit in rateLimit extension")
	}
	if _, ok := rateLimit["remaining"]; !ok {
		t.Error("Expected remaining in rateLimit extension")
	}
}

func TestHandlerRateLimitExceeded(t *testing.T) {
	// Create handler with very low rate limit
	cfg := &atlas.Config{
		DataDir:          t.TempDir(),
		AOFSyncMode:      aof.SyncEvery,
		SnapshotInterval: time.Hour,
	}

	atlasStore, err := atlas.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create atlas: %v", err)
	}
	t.Cleanup(func() {
		atlasStore.Close()
	})

	authService := auth.NewService(atlasStore)
	resolver := NewResolver(atlasStore, nil, authService, nil, nil, nil)

	// Very strict rate limit config
	rateLimitConfig := &RateLimitConfig{
		Enabled:                  true,
		DefaultLimit:             2, // Only 2 requests
		DefaultWindow:            60,
		AnonymousMultiplier:      1.0,
		IncludeHeadersInResponse: true,
		RejectOnExceed:           true,
		IPBasedForAnonymous:      true,
		FieldLimits:              make(map[string]*FieldRateLimit),
	}
	rateLimitConfig.FieldLimits["Query.health"] = &FieldRateLimit{
		Limit:  2,
		Window: 60,
	}

	handler := NewHandler(resolver, authService, WithRateLimitConfig(rateLimitConfig))
	defer handler.Close()

	req := GraphQLRequest{
		Query: `query { health { status } }`,
	}

	reqBody, _ := json.Marshal(req)

	// Make 2 successful requests
	for i := 0; i < 2; i++ {
		httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.RemoteAddr = "10.0.0.1:12345"

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httpReq)

		if recorder.Code != http.StatusOK {
			t.Errorf("Request %d: Expected status 200, got %d", i+1, recorder.Code)
		}
	}

	// 3rd request should be rate limited
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.RemoteAddr = "10.0.0.1:12345"

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429 (Too Many Requests), got %d", recorder.Code)
	}

	// Should have Retry-After header
	if recorder.Header().Get("Retry-After") == "" {
		t.Error("Expected Retry-After header when rate limited")
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(response.Errors) == 0 {
		t.Error("Expected errors when rate limited")
	}

	if len(response.Errors) > 0 {
		ext, ok := response.Errors[0].Extensions["code"].(string)
		if !ok || ext != "RATE_LIMIT_EXCEEDED" {
			t.Errorf("Expected RATE_LIMIT_EXCEEDED error code, got %v", response.Errors[0].Extensions)
		}
	}
}

func TestHandlerWithRateLimitConfig(t *testing.T) {
	// Create handler with custom rate limit config
	cfg := &atlas.Config{
		DataDir:          t.TempDir(),
		AOFSyncMode:      aof.SyncEvery,
		SnapshotInterval: time.Hour,
	}

	atlasStore, err := atlas.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create atlas: %v", err)
	}
	t.Cleanup(func() {
		atlasStore.Close()
	})

	authService := auth.NewService(atlasStore)
	resolver := NewResolver(atlasStore, nil, authService, nil, nil, nil)

	customConfig := &RateLimitConfig{
		Enabled:             true,
		DefaultLimit:        100,
		DefaultWindow:       30,
		AnonymousMultiplier: 0.25,
		RejectOnExceed:      false,
		FieldLimits:         make(map[string]*FieldRateLimit),
	}

	handler := NewHandler(resolver, authService, WithRateLimitConfig(customConfig))
	defer handler.Close()

	// Verify config was applied
	if handler.rateLimiter.config.DefaultLimit != 100 {
		t.Errorf("Expected DefaultLimit 100, got %d", handler.rateLimiter.config.DefaultLimit)
	}
	if handler.rateLimiter.config.DefaultWindow != 30 {
		t.Errorf("Expected DefaultWindow 30, got %d", handler.rateLimiter.config.DefaultWindow)
	}
	if handler.rateLimiter.config.AnonymousMultiplier != 0.25 {
		t.Errorf("Expected AnonymousMultiplier 0.25, got %f", handler.rateLimiter.config.AnonymousMultiplier)
	}
}

func TestHandlerRateLimitDisabled(t *testing.T) {
	// Create handler with rate limiting disabled
	cfg := &atlas.Config{
		DataDir:          t.TempDir(),
		AOFSyncMode:      aof.SyncEvery,
		SnapshotInterval: time.Hour,
	}

	atlasStore, err := atlas.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create atlas: %v", err)
	}
	t.Cleanup(func() {
		atlasStore.Close()
	})

	authService := auth.NewService(atlasStore)
	resolver := NewResolver(atlasStore, nil, authService, nil, nil, nil)

	disabledConfig := &RateLimitConfig{
		Enabled:       false,
		DefaultLimit:  1, // Very low limit, but disabled
		DefaultWindow: 60,
		FieldLimits:   make(map[string]*FieldRateLimit),
	}

	handler := NewHandler(resolver, authService, WithRateLimitConfig(disabledConfig))
	defer handler.Close()

	req := GraphQLRequest{
		Query: `query { health { status } }`,
	}

	reqBody, _ := json.Marshal(req)

	// Even with limit of 1, all requests should succeed when disabled
	for i := 0; i < 10; i++ {
		httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.RemoteAddr = "192.168.1.100:12345"

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httpReq)

		if recorder.Code != http.StatusOK {
			t.Errorf("Request %d: Expected status 200 with rate limiting disabled, got %d", i+1, recorder.Code)
		}
	}
}

func TestExtractOperationField(t *testing.T) {
	handler, _ := createTestHandler(t)

	tests := []struct {
		query    string
		expected string
	}{
		{`query { health { status } }`, "Query.health"},
		{`{ health { status } }`, "Query.health"},
		{`mutation { login(input: {}) { token } }`, "Mutation.login"},
		{`subscription { keyChanged(pattern: "*") { key } }`, "Subscription.keyChanged"},
		{`query { me { id username } }`, "Query.me"},
		{`query GetHealth { health { status } }`, "Query.health"},
		{`mutation CreateUser { register(input: {}) { user } }`, "Mutation.register"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			result := handler.extractOperationField(tt.query)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}
