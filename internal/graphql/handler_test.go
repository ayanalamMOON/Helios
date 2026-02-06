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
