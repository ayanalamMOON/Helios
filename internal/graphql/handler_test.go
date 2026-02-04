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
