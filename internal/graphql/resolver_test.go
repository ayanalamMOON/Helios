package graphql

import (
	"context"
	"testing"
	"time"

	"github.com/helios/helios/internal/atlas"
	"github.com/helios/helios/internal/atlas/aof"
	"github.com/helios/helios/internal/auth"
)

// Test helper to create a test resolver
func createTestResolver(t *testing.T) *Resolver {
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
		nil, // No sharded atlas for basic tests
		authService,
		nil, // No queue for basic tests
		nil, // No raft for basic tests
		nil, // No shard manager for basic tests
	)

	return resolver
}

func TestMe_Unauthenticated(t *testing.T) {
	resolver := createTestResolver(t)
	ctx := context.Background()

	_, err := resolver.Me(ctx)
	if err == nil {
		t.Error("Expected error for unauthenticated request")
	}
}

func TestMe_Authenticated(t *testing.T) {
	resolver := createTestResolver(t)

	// Create a test user
	user, err := resolver.authService.CreateUser("testuser", "password123")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	// Create context with user ID
	ctx := context.WithValue(context.Background(), "user_id", user.ID)

	result, err := resolver.Me(ctx)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	if result.Username != "testuser" {
		t.Errorf("Expected username 'testuser', got '%s'", result.Username)
	}
}

func TestSetAndGet(t *testing.T) {
	resolver := createTestResolver(t)
	ctx := context.Background()

	// Test Set
	setArgs := struct{ Input SetInput }{
		Input: SetInput{
			Key:   "test-key",
			Value: "test-value",
			TTL:   nil,
		},
	}

	kvPair, err := resolver.Set(ctx, setArgs)
	if err != nil {
		t.Errorf("Unexpected error on Set: %v", err)
	}

	if kvPair.Key != "test-key" || kvPair.Value != "test-value" {
		t.Errorf("Set returned incorrect data: %+v", kvPair)
	}

	// Test Get
	getArgs := struct{ Key string }{Key: "test-key"}
	result, err := resolver.Get(ctx, getArgs)
	if err != nil {
		t.Errorf("Unexpected error on Get: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	if result.Value != "test-value" {
		t.Errorf("Expected value 'test-value', got '%s'", result.Value)
	}
}

func TestDelete(t *testing.T) {
	resolver := createTestResolver(t)
	ctx := context.Background()

	// Set a key first
	resolver.atlasStore.Set("delete-test", []byte("value"), 0)

	// Delete it
	deleteArgs := struct{ Key string }{Key: "delete-test"}
	deleted, err := resolver.Delete(ctx, deleteArgs)
	if err != nil {
		t.Errorf("Unexpected error on Delete: %v", err)
	}

	if !deleted {
		t.Error("Expected delete to return true")
	}

	// Verify it's gone
	_, exists := resolver.atlasStore.Get("delete-test")
	if exists {
		t.Error("Key should not exist after delete")
	}
}

func TestKeys(t *testing.T) {
	resolver := createTestResolver(t)
	ctx := context.Background()

	// Set some test keys
	resolver.atlasStore.Set("data:key1", []byte("value1"), 0)
	resolver.atlasStore.Set("data:key2", []byte("value2"), 0)
	resolver.atlasStore.Set("data:key3", []byte("value3"), 0)

	// Get all keys
	pattern := "*"
	keysArgs := struct{ Pattern *string }{Pattern: &pattern}
	keys, err := resolver.Keys(ctx, keysArgs)
	if err != nil {
		t.Errorf("Unexpected error on Keys: %v", err)
	}

	if len(keys) < 3 {
		t.Errorf("Expected at least 3 keys, got %d", len(keys))
	}
}

func TestExists(t *testing.T) {
	resolver := createTestResolver(t)
	ctx := context.Background()

	// Set a key
	resolver.atlasStore.Set("exists-test", []byte("value"), 0)

	// Test exists for existing key
	existsArgs := struct{ Key string }{Key: "exists-test"}
	exists, err := resolver.Exists(ctx, existsArgs)
	if err != nil {
		t.Errorf("Unexpected error on Exists: %v", err)
	}

	if !exists {
		t.Error("Expected key to exist")
	}

	// Test exists for non-existing key
	notExistsArgs := struct{ Key string }{Key: "nonexistent"}
	exists, err = resolver.Exists(ctx, notExistsArgs)
	if err != nil {
		t.Errorf("Unexpected error on Exists: %v", err)
	}

	if exists {
		t.Error("Expected key to not exist")
	}
}

func TestHealth(t *testing.T) {
	resolver := createTestResolver(t)
	ctx := context.Background()

	health, err := resolver.Health(ctx)
	if err != nil {
		t.Errorf("Unexpected error on Health: %v", err)
	}

	if health == nil {
		t.Fatal("Expected health result, got nil")
	}

	if health.Status != "healthy" && health.Status != "degraded" {
		t.Errorf("Unexpected health status: %s", health.Status)
	}

	if len(health.Checks) == 0 {
		t.Error("Expected health checks, got none")
	}
}

func TestMetrics(t *testing.T) {
	resolver := createTestResolver(t)
	ctx := context.Background()

	metrics, err := resolver.Metrics(ctx)
	if err != nil {
		t.Errorf("Unexpected error on Metrics: %v", err)
	}

	if metrics == nil {
		t.Fatal("Expected metrics result, got nil")
	}

	if metrics.Data == "" {
		t.Error("Expected metrics data, got empty string")
	}

	if metrics.Timestamp == "" {
		t.Error("Expected metrics timestamp, got empty string")
	}
}

func TestRegisterAndLogin(t *testing.T) {
	resolver := createTestResolver(t)
	ctx := context.Background()

	// Test Register
	registerArgs := struct{ Input RegisterInput }{
		Input: RegisterInput{
			Username: "newuser",
			Password: "securepass123",
			Email:    nil,
		},
	}

	authPayload, err := resolver.Register(ctx, registerArgs)
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}

	if authPayload.Token == "" {
		t.Error("Expected token, got empty string")
	}

	if authPayload.User == nil {
		t.Fatal("Expected user, got nil")
	}

	if authPayload.User.Username != "newuser" {
		t.Errorf("Expected username 'newuser', got '%s'", authPayload.User.Username)
	}

	// Test Login
	loginArgs := struct{ Input LoginInput }{
		Input: LoginInput{
			Username: "newuser",
			Password: "securepass123",
		},
	}

	authPayload, err = resolver.Login(ctx, loginArgs)
	if err != nil {
		t.Fatalf("Failed to login: %v", err)
	}

	if authPayload.Token == "" {
		t.Error("Expected token, got empty string")
	}

	// Test Login with wrong password
	wrongPassArgs := struct{ Input LoginInput }{
		Input: LoginInput{
			Username: "newuser",
			Password: "wrongpassword",
		},
	}

	_, err = resolver.Login(ctx, wrongPassArgs)
	if err == nil {
		t.Error("Expected error for wrong password")
	}
}

func TestLogout(t *testing.T) {
	resolver := createTestResolver(t)

	// Create a token
	user, _ := resolver.authService.CreateUser("logoutuser", "password123")
	token, _ := resolver.authService.CreateToken(user.ID, time.Hour)

	// Create context with token
	ctx := context.WithValue(context.Background(), "token", token.TokenHash)

	// Test Logout
	result, err := resolver.Logout(ctx)
	if err != nil {
		t.Errorf("Unexpected error on Logout: %v", err)
	}

	if !result {
		t.Error("Expected logout to return true")
	}

	// Verify token is revoked
	_, err = resolver.authService.ValidateToken(token.TokenHash)
	if err == nil {
		t.Error("Expected token to be invalid after logout")
	}
}

func TestExpire(t *testing.T) {
	resolver := createTestResolver(t)
	ctx := context.Background()

	// Set a key
	resolver.atlasStore.Set("expire-test", []byte("value"), 0)

	// Set TTL
	expireArgs := struct {
		Key string
		TTL int32
	}{
		Key: "expire-test",
		TTL: 60,
	}

	result, err := resolver.Expire(ctx, expireArgs)
	if err != nil {
		t.Errorf("Unexpected error on Expire: %v", err)
	}

	if !result {
		t.Error("Expected expire to return true")
	}

	// Test expire on non-existent key
	nonExistArgs := struct {
		Key string
		TTL int32
	}{
		Key: "nonexistent",
		TTL: 60,
	}

	result, err = resolver.Expire(ctx, nonExistArgs)
	if err != nil {
		t.Errorf("Unexpected error on Expire: %v", err)
	}

	if result {
		t.Error("Expected expire to return false for non-existent key")
	}
}
