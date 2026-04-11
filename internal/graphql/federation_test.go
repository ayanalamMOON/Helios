package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFederationManagerDefaults(t *testing.T) {
	manager := NewFederationManager(nil)
	if manager == nil {
		t.Fatal("expected federation manager")
	}

	if !manager.IsEnabled() {
		t.Error("expected federation to be enabled by default")
	}

	cfg := manager.GetConfig()
	if cfg.ServiceName != "helios" {
		t.Errorf("expected default service name helios, got %q", cfg.ServiceName)
	}
	if cfg.ServiceVersion != "1.0.0" {
		t.Errorf("expected default service version 1.0.0, got %q", cfg.ServiceVersion)
	}
	if len(manager.SupportedEntityTypes()) == 0 {
		t.Error("expected default entity types")
	}
}

func TestFederationManagerServiceSDL(t *testing.T) {
	manager := NewFederationManagerWithSource(&FederationConfig{
		Enabled:           true,
		ServiceName:       "helios-test",
		ServiceVersion:    "2.0.0",
		IncludeServiceSDL: true,
		EntityTypes:       []string{"User"},
	}, func() string {
		return "type Query { health: String! }"
	})

	sdl, err := manager.GetServiceSDL()
	if err != nil {
		t.Fatalf("expected SDL, got error: %v", err)
	}
	if !strings.Contains(sdl, "type Query") {
		t.Errorf("expected SDL to include type Query, got %q", sdl)
	}

	manager.UpdateConfig(&FederationConfig{
		Enabled:           true,
		ServiceName:       "helios-test",
		ServiceVersion:    "2.0.0",
		IncludeServiceSDL: false,
		EntityTypes:       []string{"User"},
	})

	if _, err := manager.GetServiceSDL(); err == nil {
		t.Fatal("expected error when service SDL is disabled")
	}
}

func TestResolverFederationConfig(t *testing.T) {
	resolver := createTestResolver(t)
	resolver.SetFederationManager(NewFederationManager(&FederationConfig{
		Enabled:           true,
		ServiceName:       "helios-prod",
		ServiceVersion:    "3.4.5",
		IncludeServiceSDL: true,
		StrictEntities:    true,
		EntityTypes:       []string{"User", "KVPair"},
	}))

	cfg, err := resolver.FederationConfig(context.Background())
	if err != nil {
		t.Fatalf("expected federation config, got error: %v", err)
	}

	if cfg.ServiceName != "helios-prod" {
		t.Errorf("expected service name helios-prod, got %q", cfg.ServiceName)
	}
	if cfg.EntityTypeCount != 2 {
		t.Errorf("expected 2 entity types, got %d", cfg.EntityTypeCount)
	}
	if !cfg.StrictEntities {
		t.Error("expected strict entities true")
	}
}

func TestResolverFederationEntities(t *testing.T) {
	resolver := createTestResolver(t)

	user, err := resolver.authService.CreateUser("federated-user", "password123")
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	resolver.atlasStore.Set("fed:key", []byte("fed-value"), 0)

	resolver.SetFederationManager(NewFederationManager(&FederationConfig{
		Enabled:           true,
		ServiceName:       "helios",
		ServiceVersion:    "1.0.0",
		IncludeServiceSDL: true,
		StrictEntities:    true,
		EntityTypes:       []string{"User", "KVPair"},
	}))

	entities, err := resolver.FederationEntities(context.Background(), struct {
		Representations []map[string]interface{}
	}{Representations: []map[string]interface{}{
		{"__typename": "User", "id": user.ID},
		{"__typename": "KVPair", "key": "fed:key"},
	}})
	if err != nil {
		t.Fatalf("expected entities, got error: %v", err)
	}

	if len(entities) != 2 {
		t.Fatalf("expected 2 entities, got %d", len(entities))
	}

	if entities[0]["__typename"] != "User" {
		t.Errorf("expected first entity typename User, got %v", entities[0]["__typename"])
	}
	if entities[0]["username"] != "federated-user" {
		t.Errorf("expected username federated-user, got %v", entities[0]["username"])
	}

	if entities[1]["__typename"] != "KVPair" {
		t.Errorf("expected second entity typename KVPair, got %v", entities[1]["__typename"])
	}
	if entities[1]["value"] != "fed-value" {
		t.Errorf("expected kv value fed-value, got %v", entities[1]["value"])
	}
}

func TestResolverFederationEntitiesStrictMode(t *testing.T) {
	resolver := createTestResolver(t)

	resolver.SetFederationManager(NewFederationManager(&FederationConfig{
		Enabled:           true,
		ServiceName:       "helios",
		ServiceVersion:    "1.0.0",
		IncludeServiceSDL: true,
		StrictEntities:    false,
		EntityTypes:       []string{"User"},
	}))

	entities, err := resolver.FederationEntities(context.Background(), struct {
		Representations []map[string]interface{}
	}{Representations: []map[string]interface{}{
		{"__typename": "UnknownEntity", "id": "1"},
	}})
	if err != nil {
		t.Fatalf("expected no error in non-strict mode, got %v", err)
	}
	if len(entities) != 1 {
		t.Fatalf("expected one result, got %d", len(entities))
	}
	if entities[0] != nil {
		t.Errorf("expected nil entity for unknown type in non-strict mode, got %v", entities[0])
	}

	resolver.SetFederationManager(NewFederationManager(&FederationConfig{
		Enabled:           true,
		ServiceName:       "helios",
		ServiceVersion:    "1.0.0",
		IncludeServiceSDL: true,
		StrictEntities:    true,
		EntityTypes:       []string{"User"},
	}))

	_, err = resolver.FederationEntities(context.Background(), struct {
		Representations []map[string]interface{}
	}{Representations: []map[string]interface{}{
		{"__typename": "UnknownEntity", "id": "1"},
	}})
	if err == nil {
		t.Fatal("expected strict mode error for unknown entity type")
	}
}

func TestHandlerFederationServiceQuery(t *testing.T) {
	handler, _ := createTestHandler(t)

	handler.SetFederationConfig(&FederationConfig{
		Enabled:           true,
		ServiceName:       "helios",
		ServiceVersion:    "1.0.0",
		IncludeServiceSDL: true,
		StrictEntities:    false,
		EntityTypes:       []string{"User", "KVPair", "Job", "ShardNode", "RaftPeer"},
	})

	req := GraphQLRequest{Query: `query { _service { sdl } }`}
	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(response.Errors) > 0 {
		t.Fatalf("unexpected graphql errors: %+v", response.Errors)
	}

	data, ok := response.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected response data map, got %T", response.Data)
	}
	service, ok := data["_service"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected _service object, got %T", data["_service"])
	}
	if sdl, _ := service["sdl"].(string); !strings.Contains(sdl, "type Query") {
		t.Errorf("expected service SDL to include type Query, got %q", sdl)
	}
}

func TestHandlerFederationEntitiesQuery(t *testing.T) {
	handler, resolver := createTestHandler(t)

	user, err := resolver.authService.CreateUser("fed-handler-user", "password123")
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	resolver.atlasStore.Set("fed:handler:key", []byte("handler-value"), 0)

	handler.SetFederationConfig(&FederationConfig{
		Enabled:           true,
		ServiceName:       "helios",
		ServiceVersion:    "1.0.0",
		IncludeServiceSDL: true,
		StrictEntities:    true,
		EntityTypes:       []string{"User", "KVPair"},
	})

	req := GraphQLRequest{
		Query: `query($representations: [_Any!]!) { _entities(representations: $representations) { __typename } }`,
		Variables: map[string]interface{}{
			"representations": []interface{}{
				map[string]interface{}{"__typename": "User", "id": user.ID},
				map[string]interface{}{"__typename": "KVPair", "key": "fed:handler:key"},
			},
		},
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/graphql", bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	var response GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(response.Errors) > 0 {
		t.Fatalf("unexpected graphql errors: %+v", response.Errors)
	}

	data, ok := response.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected response data map, got %T", response.Data)
	}
	entities, ok := data["_entities"].([]interface{})
	if !ok {
		t.Fatalf("expected _entities array, got %T", data["_entities"])
	}
	if len(entities) != 2 {
		t.Fatalf("expected 2 entities, got %d", len(entities))
	}

	first, ok := entities[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected first entity object, got %T", entities[0])
	}
	if first["__typename"] != "User" {
		t.Errorf("expected first entity typename User, got %v", first["__typename"])
	}

	second, ok := entities[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected second entity object, got %T", entities[1])
	}
	if second["__typename"] != "KVPair" {
		t.Errorf("expected second entity typename KVPair, got %v", second["__typename"])
	}
	if second["value"] != "handler-value" {
		t.Errorf("expected second entity value handler-value, got %v", second["value"])
	}
}
