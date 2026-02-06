package graphql

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- ComputeHash tests ---

func TestComputeHash(t *testing.T) {
	hash := ComputeHash("{ health { status } }")
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if len(hash) != 64 { // SHA-256 = 64 hex chars
		t.Fatalf("expected 64-char hash, got %d", len(hash))
	}

	// Same query = same hash
	hash2 := ComputeHash("{ health { status } }")
	if hash != hash2 {
		t.Fatal("identical queries should produce identical hashes")
	}

	// Different query = different hash
	hash3 := ComputeHash("{ health { version } }")
	if hash == hash3 {
		t.Fatal("different queries should produce different hashes")
	}
}

func TestComputeHashDeterministic(t *testing.T) {
	query := `query GetUser($id: ID!) { user(id: $id) { name email } }`
	hashes := make(map[string]bool)
	for i := 0; i < 100; i++ {
		hashes[ComputeHash(query)] = true
	}
	if len(hashes) != 1 {
		t.Fatalf("expected 1 unique hash, got %d", len(hashes))
	}
}

// --- PersistedQueryStore basic tests ---

func TestNewPersistedQueryStore(t *testing.T) {
	store := NewPersistedQueryStore(nil)
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.Count() != 0 {
		t.Fatalf("expected 0 queries, got %d", store.Count())
	}
}

func TestRegisterAndLookup(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	query := "{ health { status } }"
	hash, err := store.Register(query, "HealthCheck")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if store.Count() != 1 {
		t.Fatalf("expected 1 query, got %d", store.Count())
	}

	// Lookup
	found, ok := store.Lookup(hash)
	if !ok {
		t.Fatal("expected query to be found")
	}
	if found != query {
		t.Fatalf("expected %q, got %q", query, found)
	}
}

func TestRegisterEmptyQuery(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	_, err := store.Register("", "empty")
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	query := "{ health { status } }"

	hash1, _ := store.Register(query, "first")
	hash2, _ := store.Register(query, "second")

	if hash1 != hash2 {
		t.Fatal("duplicate registrations should return same hash")
	}
	if store.Count() != 1 {
		t.Fatalf("expected 1 query after duplicate registration, got %d", store.Count())
	}
}

func TestRegisterWithHash(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	query := "{ health { status } }"
	hash := ComputeHash(query)

	err := store.RegisterWithHash(hash, query)
	if err != nil {
		t.Fatalf("RegisterWithHash failed: %v", err)
	}

	found, ok := store.Lookup(hash)
	if !ok {
		t.Fatal("expected query to be found")
	}
	if found != query {
		t.Fatalf("expected %q, got %q", query, found)
	}
}

func TestRegisterWithHashMismatch(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	err := store.RegisterWithHash("badhash", "{ health { status } }")
	if err == nil {
		t.Fatal("expected error for hash mismatch")
	}
}

func TestRegisterWithHashEmpty(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	err := store.RegisterWithHash("", "{ health { status } }")
	if err == nil {
		t.Fatal("expected error for empty hash")
	}
	err = store.RegisterWithHash("somehash", "")
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestLookupNonExistent(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	_, ok := store.Lookup("nonexistent")
	if ok {
		t.Fatal("expected lookup to fail for nonexistent hash")
	}
}

func TestUnregister(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	query := "{ health { status } }"
	hash, _ := store.Register(query, "test")

	if !store.Unregister(hash) {
		t.Fatal("expected unregister to succeed")
	}
	if store.Count() != 0 {
		t.Fatalf("expected 0 queries after unregister, got %d", store.Count())
	}

	_, ok := store.Lookup(hash)
	if ok {
		t.Fatal("expected lookup to fail after unregister")
	}
}

func TestUnregisterNonExistent(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	if store.Unregister("nonexistent") {
		t.Fatal("expected unregister to fail for nonexistent hash")
	}
}

func TestPersistedQueryExists(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	query := "{ health { status } }"
	hash, _ := store.Register(query, "test")

	if !store.Exists(hash) {
		t.Fatal("expected Exists to return true")
	}
	if store.Exists("nonexistent") {
		t.Fatal("expected Exists to return false for nonexistent hash")
	}
}

func TestGetEntry(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	query := "{ health { status } }"
	hash, _ := store.Register(query, "HealthCheck")

	entry, ok := store.GetEntry(hash)
	if !ok {
		t.Fatal("expected entry to be found")
	}
	if entry.Query != query {
		t.Fatalf("expected query %q, got %q", query, entry.Query)
	}
	if entry.Name != "HealthCheck" {
		t.Fatalf("expected name %q, got %q", "HealthCheck", entry.Name)
	}
	if entry.AutoReg {
		t.Fatal("expected AutoReg to be false for manual registration")
	}
}

func TestGetEntryNonExistent(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	_, ok := store.GetEntry("nonexistent")
	if ok {
		t.Fatal("expected GetEntry to fail for nonexistent hash")
	}
}

func TestClear(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	store.Register("{ health }", "q1")
	store.Register("{ metrics }", "q2")
	store.Register("{ me { id } }", "q3")

	if store.Count() != 3 {
		t.Fatalf("expected 3 queries, got %d", store.Count())
	}

	store.Clear()
	if store.Count() != 0 {
		t.Fatalf("expected 0 queries after clear, got %d", store.Count())
	}
}

// --- LRU eviction ---

func TestLRUEviction(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.CacheSize = 3
	store := NewPersistedQueryStore(config)

	h1, _ := store.Register("query1", "q1")
	store.Register("query2", "q2")
	store.Register("query3", "q3")

	// Adding 4th should evict first
	store.Register("query4", "q4")
	if store.Count() != 3 {
		t.Fatalf("expected 3 queries after eviction, got %d", store.Count())
	}

	// First query should be evicted
	if store.Exists(h1) {
		t.Fatal("expected first query to be evicted")
	}
}

func TestLRUTouchKeepsAlive(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.CacheSize = 3
	store := NewPersistedQueryStore(config)

	h1, _ := store.Register("query1", "q1")
	h2, _ := store.Register("query2", "q2")
	store.Register("query3", "q3")

	// Touch h1 to make it most recently used
	store.Lookup(h1)

	// Adding 4th should evict h2 (least recently used)
	store.Register("query4", "q4")

	if !store.Exists(h1) {
		t.Fatal("expected h1 to survive eviction after touch")
	}
	if store.Exists(h2) {
		t.Fatal("expected h2 to be evicted")
	}
}

func TestUnlimitedCache(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.CacheSize = 0 // Unlimited
	store := NewPersistedQueryStore(config)

	for i := 0; i < 100; i++ {
		store.Register("query"+string(rune(i+'A')), "")
	}
	if store.Count() != 100 {
		t.Fatalf("expected 100 queries with unlimited cache, got %d", store.Count())
	}
}

// --- TTL / expiration ---

func TestTTLExpiration(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.TTL = 50 * time.Millisecond
	store := NewPersistedQueryStore(config)

	query := "{ health { status } }"
	hash := ComputeHash(query)
	store.RegisterWithHash(hash, query)

	// Should be found immediately
	_, ok := store.Lookup(hash)
	if !ok {
		t.Fatal("expected query to be found immediately")
	}

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	_, ok = store.Lookup(hash)
	if ok {
		t.Fatal("expected query to be expired")
	}
}

func TestCleanupExpired(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.TTL = 50 * time.Millisecond
	store := NewPersistedQueryStore(config)

	query := "{ health { status } }"
	hash := ComputeHash(query)
	store.RegisterWithHash(hash, query)

	time.Sleep(100 * time.Millisecond)

	cleaned := store.CleanupExpired()
	if cleaned != 1 {
		t.Fatalf("expected 1 cleaned entry, got %d", cleaned)
	}
	if store.Count() != 0 {
		t.Fatalf("expected 0 queries after cleanup, got %d", store.Count())
	}
}

func TestManualRegistrationNoTTL(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.TTL = 50 * time.Millisecond
	store := NewPersistedQueryStore(config)

	// Manual registration (via Register) should NOT have TTL
	hash, _ := store.Register("{ health { status } }", "test")
	time.Sleep(100 * time.Millisecond)

	_, ok := store.Lookup(hash)
	if !ok {
		t.Fatal("manually registered queries should not expire")
	}
}

// --- Stats ---

func TestStats(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	// Register some queries
	h1, _ := store.Register("query1", "q1")
	store.Register("query2", "q2")

	// Auto-register one
	autoQuery := "query3"
	autoHash := ComputeHash(autoQuery)
	store.RegisterWithHash(autoHash, autoQuery)

	// Perform some lookups
	store.Lookup(h1)
	store.Lookup(h1)
	store.Lookup("nonexistent")

	stats := store.Stats()
	if stats.TotalQueries != 3 {
		t.Fatalf("expected 3 total queries, got %d", stats.TotalQueries)
	}
	if stats.ManuallyRegistered != 2 {
		t.Fatalf("expected 2 manually registered, got %d", stats.ManuallyRegistered)
	}
	if stats.AutoRegistered != 1 {
		t.Fatalf("expected 1 auto registered, got %d", stats.AutoRegistered)
	}
	if stats.Hits != 2 {
		t.Fatalf("expected 2 hits, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Fatalf("expected 1 miss, got %d", stats.Misses)
	}
	if stats.HitRate < 60 || stats.HitRate > 70 {
		t.Fatalf("expected ~66.7%% hit rate, got %.1f%%", stats.HitRate)
	}
}

func TestStatsHitRateZero(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	stats := store.Stats()
	if stats.HitRate != 0 {
		t.Fatalf("expected 0%% hit rate with no requests, got %.1f%%", stats.HitRate)
	}
}

// --- ListQueries ---

func TestListQueries(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	store.Register("query1", "first")
	store.Register("query2", "second")
	store.Register("query3", "third")

	all := store.ListQueries(0, 0) // 0 limit = all
	if len(all) != 3 {
		t.Fatalf("expected 3 queries, got %d", len(all))
	}

	// With limit
	limited := store.ListQueries(2, 0)
	if len(limited) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(limited))
	}

	// With offset
	offset := store.ListQueries(10, 2)
	if len(offset) != 1 {
		t.Fatalf("expected 1 query with offset 2, got %d", len(offset))
	}

	// Offset beyond range
	beyond := store.ListQueries(10, 10)
	if len(beyond) != 0 {
		t.Fatalf("expected 0 queries with offset beyond range, got %d", len(beyond))
	}
}

// --- Disk persistence ---

func TestDiskPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "queries.json")

	config := DefaultPersistedQueryConfig()
	config.PersistencePath = path

	// Create store and register queries
	store1 := NewPersistedQueryStore(config)
	store1.Register("{ health { status } }", "HealthCheck")
	store1.Register("{ metrics }", "Metrics")

	// Verify file was created
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("expected persistence file to be created")
	}

	// Create new store from same path - should load queries
	store2 := NewPersistedQueryStore(config)
	if store2.Count() != 2 {
		t.Fatalf("expected 2 queries loaded from disk, got %d", store2.Count())
	}

	hash := ComputeHash("{ health { status } }")
	query, ok := store2.Lookup(hash)
	if !ok {
		t.Fatal("expected query to be found in loaded store")
	}
	if query != "{ health { status } }" {
		t.Fatalf("expected correct query, got %q", query)
	}
}

func TestDiskPersistenceNonExistentFile(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.PersistencePath = filepath.Join(t.TempDir(), "nonexistent", "queries.json")

	// Should not panic
	store := NewPersistedQueryStore(config)
	if store.Count() != 0 {
		t.Fatal("expected empty store when persistence file doesn't exist")
	}

	// Register should create the directory and file
	store.Register("{ health }", "test")
	if _, err := os.Stat(config.PersistencePath); os.IsNotExist(err) {
		t.Fatal("expected persistence file to be created")
	}
}

// --- APQ Protocol ---

func TestAPQNormalRequest(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	req := &GraphQLRequest{
		Query: "{ health { status } }",
	}

	result := store.ProcessAPQ(req)
	if result != nil {
		t.Fatal("expected nil result for normal request without APQ extension")
	}
}

func TestAPQRejectUnpersisted(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.RejectUnpersistedQueries = true
	store := NewPersistedQueryStore(config)

	req := &GraphQLRequest{
		Query: "{ health { status } }",
	}

	result := store.ProcessAPQ(req)
	if result == nil {
		t.Fatal("expected non-nil result when unpersisted queries are rejected")
	}
	if result.Error == nil {
		t.Fatal("expected error when unpersisted queries are rejected")
	}
	if !strings.Contains(result.Error.Error(), "unpersisted") {
		t.Fatalf("unexpected error message: %s", result.Error.Error())
	}
}

func TestAPQHashOnlyMiss(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	hash := ComputeHash("{ health { status } }")
	req := &GraphQLRequest{
		Extensions: map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    float64(1),
				"sha256Hash": hash,
			},
		},
	}

	result := store.ProcessAPQ(req)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Error == nil {
		t.Fatal("expected error for cache miss")
	}
	if !IsPersistedQueryNotFound(result.Error) {
		t.Fatalf("expected PersistedQueryNotFoundError, got: %v", result.Error)
	}
}

func TestAPQHashOnlyHit(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	query := "{ health { status } }"
	hash := ComputeHash(query)
	store.RegisterWithHash(hash, query)

	req := &GraphQLRequest{
		Extensions: map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    float64(1),
				"sha256Hash": hash,
			},
		},
	}

	result := store.ProcessAPQ(req)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Query != query {
		t.Fatalf("expected query %q, got %q", query, result.Query)
	}
	if !result.WasCacheHit {
		t.Fatal("expected cache hit")
	}
}

func TestAPQRegistration(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	query := "{ health { status } }"
	hash := ComputeHash(query)

	req := &GraphQLRequest{
		Query: query,
		Extensions: map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    float64(1),
				"sha256Hash": hash,
			},
		},
	}

	result := store.ProcessAPQ(req)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Query != query {
		t.Fatalf("expected query %q, got %q", query, result.Query)
	}
	if !result.WasRegistered {
		t.Fatal("expected WasRegistered to be true")
	}

	// Subsequent hash-only request should succeed
	req2 := &GraphQLRequest{
		Extensions: map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    float64(1),
				"sha256Hash": hash,
			},
		},
	}

	result2 := store.ProcessAPQ(req2)
	if result2.Error != nil {
		t.Fatalf("unexpected error on second lookup: %v", result2.Error)
	}
	if !result2.WasCacheHit {
		t.Fatal("expected cache hit on second lookup")
	}
}

func TestAPQAutoRegisterDisabled(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.AllowAutoRegister = false
	store := NewPersistedQueryStore(config)

	query := "{ health { status } }"
	hash := ComputeHash(query)

	req := &GraphQLRequest{
		Query: query,
		Extensions: map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    float64(1),
				"sha256Hash": hash,
			},
		},
	}

	result := store.ProcessAPQ(req)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Error == nil {
		t.Fatal("expected error when auto-register is disabled")
	}
}

func TestAPQInvalidVersion(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	req := &GraphQLRequest{
		Extensions: map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    float64(2), // Unsupported
				"sha256Hash": "somehash",
			},
		},
	}

	result := store.ProcessAPQ(req)
	// Version 2 not supported; extension is ignored
	if result != nil {
		t.Fatal("expected nil result for unsupported APQ version")
	}
}

func TestAPQMissingSha256Hash(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	req := &GraphQLRequest{
		Extensions: map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version": float64(1),
			},
		},
	}

	result := store.ProcessAPQ(req)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Error == nil {
		t.Fatal("expected error for missing sha256Hash")
	}
}

// --- Batch Registration ---

func TestRegisterBatch(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	queries := map[string]string{
		"Health":    "{ health { status } }",
		"Metrics":   "{ metrics }",
		"Me":        "{ me { id username } }",
	}

	result, err := store.RegisterBatch(queries)
	if err != nil {
		t.Fatalf("RegisterBatch failed: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 results, got %d", len(result))
	}
	if store.Count() != 3 {
		t.Fatalf("expected 3 queries, got %d", store.Count())
	}

	// Verify each hash is correct
	for name, hash := range result {
		query := queries[name]
		expectedHash := ComputeHash(query)
		if hash != expectedHash {
			t.Fatalf("hash mismatch for %s: expected %s, got %s", name, expectedHash, hash)
		}
	}
}

func TestRegisterFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "queries.json")

	queries := map[string]string{
		"Health":  "{ health { status } }",
		"Metrics": "{ metrics }",
	}

	data, _ := json.Marshal(queries)
	os.WriteFile(path, data, 0o644)

	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	count, err := store.RegisterFromFile(path)
	if err != nil {
		t.Fatalf("RegisterFromFile failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 registered, got %d", count)
	}
	if store.Count() != 2 {
		t.Fatalf("expected 2 queries, got %d", store.Count())
	}
}

func TestRegisterFromFileInvalid(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	_, err := store.RegisterFromFile("/nonexistent/file.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// --- Common Queries ---

func TestRegisterCommonQueries(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	store.RegisterCommonQueries()

	if store.Count() == 0 {
		t.Fatal("expected common queries to be registered")
	}
	// Should have at least 10 common queries
	if store.Count() < 10 {
		t.Fatalf("expected at least 10 common queries, got %d", store.Count())
	}

	stats := store.Stats()
	if stats.ManuallyRegistered == 0 {
		t.Fatal("expected manually registered queries")
	}
}

// --- Config management ---

func TestGetConfig(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.CacheSize = 500
	store := NewPersistedQueryStore(config)

	got := store.GetConfig()
	if got.CacheSize != 500 {
		t.Fatalf("expected CacheSize 500, got %d", got.CacheSize)
	}
}

func TestUpdateConfig(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	newConfig := DefaultPersistedQueryConfig()
	newConfig.CacheSize = 200
	newConfig.AllowAutoRegister = false
	store.UpdateConfig(newConfig)

	got := store.GetConfig()
	if got.CacheSize != 200 {
		t.Fatalf("expected CacheSize 200, got %d", got.CacheSize)
	}
	if got.AllowAutoRegister {
		t.Fatal("expected AllowAutoRegister to be false")
	}
}

// --- Concurrent access ---

func TestConcurrentAccess(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			query := "query" + string(rune('A'+idx%26))
			hash, _ := store.Register(query, "")
			store.Lookup(hash)
			store.Exists(hash)
			store.GetEntry(hash)
			store.Stats()
			store.Count()
			store.ListQueries(10, 0)
		}(i)
	}
	wg.Wait()

	if store.Count() == 0 {
		t.Fatal("expected some queries after concurrent access")
	}
}

func TestConcurrentAPQ(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	query := "{ health { status } }"
	hash := ComputeHash(query)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Register
			req := &GraphQLRequest{
				Query: query,
				Extensions: map[string]interface{}{
					"persistedQuery": map[string]interface{}{
						"version":    float64(1),
						"sha256Hash": hash,
					},
				},
			}
			store.ProcessAPQ(req)

			// Lookup
			req2 := &GraphQLRequest{
				Extensions: map[string]interface{}{
					"persistedQuery": map[string]interface{}{
						"version":    float64(1),
						"sha256Hash": hash,
					},
				},
			}
			store.ProcessAPQ(req2)
		}()
	}
	wg.Wait()

	if store.Count() != 1 {
		t.Fatalf("expected exactly 1 query after concurrent APQ, got %d", store.Count())
	}
}

// --- normalizeQuery ---

func TestNormalizeQuery(t *testing.T) {
	query := `
		query GetUser($id: ID!) {
			user(id: $id) {
				name
				email
			}
		}
	`
	normalized := normalizeQuery(query)
	if strings.HasPrefix(normalized, "\n") || strings.HasPrefix(normalized, "\t") || strings.HasPrefix(normalized, " ") {
		t.Fatal("expected no leading whitespace")
	}
	if strings.HasSuffix(normalized, "\n") || strings.HasSuffix(normalized, "\t") || strings.HasSuffix(normalized, " ") {
		t.Fatal("expected no trailing whitespace")
	}
	if strings.Contains(normalized, "\t") {
		t.Fatal("expected no tabs")
	}
}

// --- Handler integration tests ---

func TestHandlerAPQProtocol(t *testing.T) {
	handler := NewHandler(nil, nil)

	query := "{ health { status } }"
	hash := ComputeHash(query)

	// Step 1: Send hash only — should get PERSISTED_QUERY_NOT_FOUND
	reqBody := map[string]interface{}{
		"extensions": map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    1,
				"sha256Hash": hash,
			},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	var resp GraphQLResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if len(resp.Errors) == 0 {
		t.Fatal("expected PERSISTED_QUERY_NOT_FOUND error")
	}
	if resp.Errors[0].Extensions == nil {
		t.Fatal("expected error extensions")
	}
	code, ok := resp.Errors[0].Extensions["code"].(string)
	if !ok || code != "PERSISTED_QUERY_NOT_FOUND" {
		t.Fatalf("expected PERSISTED_QUERY_NOT_FOUND, got %v", code)
	}

	// Step 2: Send hash + query — should register and execute
	reqBody2 := map[string]interface{}{
		"query": query,
		"extensions": map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    1,
				"sha256Hash": hash,
			},
		},
	}
	body2, _ := json.Marshal(reqBody2)
	req2 := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body2)))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	handler.ServeHTTP(w2, req2)

	var resp2 GraphQLResponse
	json.Unmarshal(w2.Body.Bytes(), &resp2)

	// Should execute (possibly with error from nil resolver, but no APQ error)
	hasAPQError := false
	for _, err := range resp2.Errors {
		if ext, ok := err.Extensions["code"]; ok && ext == "PERSISTED_QUERY_NOT_FOUND" {
			hasAPQError = true
		}
	}
	if hasAPQError {
		t.Fatal("expected no APQ error after registration")
	}

	// Step 3: Send hash only again — should succeed (cache hit)
	body3, _ := json.Marshal(reqBody)
	req3 := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body3)))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()

	handler.ServeHTTP(w3, req3)

	var resp3 GraphQLResponse
	json.Unmarshal(w3.Body.Bytes(), &resp3)

	hasAPQError = false
	for _, err := range resp3.Errors {
		if ext, ok := err.Extensions["code"]; ok && ext == "PERSISTED_QUERY_NOT_FOUND" {
			hasAPQError = true
		}
	}
	if hasAPQError {
		t.Fatal("expected no APQ error on third attempt (should be cached)")
	}
}

func TestHandlerPersistedQueryDisabled(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.Enabled = false
	handler := NewHandler(nil, nil, WithPersistedQueryConfig(config))

	query := "{ health { status } }"
	hash := ComputeHash(query)

	reqBody := map[string]interface{}{
		"extensions": map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    1,
				"sha256Hash": hash,
			},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// When disabled, APQ extension should be ignored, and request
	// proceeds as normal (empty query = no result or error)
	if w.Code == http.StatusOK {
		// OK — just verify there's no PERSISTED_QUERY_NOT_FOUND error
		var resp GraphQLResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		for _, err := range resp.Errors {
			if ext, ok := err.Extensions["code"]; ok && ext == "PERSISTED_QUERY_NOT_FOUND" {
				t.Fatal("APQ should be disabled, not return PERSISTED_QUERY_NOT_FOUND")
			}
		}
	}
}

func TestHandlerWithPersistedQueryStore(t *testing.T) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())

	// Pre-register a query
	query := "{ health { status } }"
	hash := ComputeHash(query)
	store.RegisterWithHash(hash, query)

	handler := NewHandler(nil, nil, WithPersistedQueries(store))

	// Send hash only — should succeed because pre-registered
	reqBody := map[string]interface{}{
		"extensions": map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    1,
				"sha256Hash": hash,
			},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	var resp GraphQLResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	for _, err := range resp.Errors {
		if ext, ok := err.Extensions["code"]; ok && ext == "PERSISTED_QUERY_NOT_FOUND" {
			t.Fatal("expected pre-registered query to be found")
		}
	}
}

func TestHandlerRejectUnpersisted(t *testing.T) {
	config := DefaultPersistedQueryConfig()
	config.RejectUnpersistedQueries = true
	handler := NewHandler(nil, nil, WithPersistedQueryConfig(config))

	// Send a normal query without APQ extension
	reqBody := map[string]interface{}{
		"query": "{ health { status } }",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unpersisted query, got %d", w.Code)
	}
}

func TestHandlerGetPersistedQueryStore(t *testing.T) {
	handler := NewHandler(nil, nil)
	store := handler.GetPersistedQueryStore()
	if store == nil {
		t.Fatal("expected non-nil persisted query store")
	}
}

func TestHandlerEnablePersistedQueries(t *testing.T) {
	handler := NewHandler(nil, nil)

	handler.EnablePersistedQueries(false)
	config := handler.GetPersistedQueryStore().GetConfig()
	if config.Enabled {
		t.Fatal("expected persisted queries to be disabled")
	}

	handler.EnablePersistedQueries(true)
	config = handler.GetPersistedQueryStore().GetConfig()
	if !config.Enabled {
		t.Fatal("expected persisted queries to be enabled")
	}
}

func TestHandlerSetPersistedQueryConfig(t *testing.T) {
	handler := NewHandler(nil, nil)

	newConfig := DefaultPersistedQueryConfig()
	newConfig.CacheSize = 42
	handler.SetPersistedQueryConfig(newConfig)

	config := handler.GetPersistedQueryStore().GetConfig()
	if config.CacheSize != 42 {
		t.Fatalf("expected CacheSize 42, got %d", config.CacheSize)
	}
}

// --- PersistedQueryNotFoundError ---

func TestPersistedQueryNotFoundError(t *testing.T) {
	err := &PersistedQueryNotFoundError{Hash: "abc123"}
	if err.Error() != "PersistedQueryNotFound" {
		t.Fatalf("unexpected error message: %s", err.Error())
	}
	if !IsPersistedQueryNotFound(err) {
		t.Fatal("expected IsPersistedQueryNotFound to return true")
	}
	if IsPersistedQueryNotFound(nil) {
		t.Fatal("expected IsPersistedQueryNotFound to return false for nil")
	}
	var otherErr error = fmt.Errorf("not a PQ error")
	if IsPersistedQueryNotFound(otherErr) {
		t.Fatal("expected IsPersistedQueryNotFound to return false for non-PQ error")
	}
}

// --- Benchmarks ---

func BenchmarkComputeHash(b *testing.B) {
	query := "query GetUser($id: ID!) { user(id: $id) { name email roles createdAt } }"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComputeHash(query)
	}
}

func BenchmarkLookup(b *testing.B) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	query := "{ health { status } }"
	hash, _ := store.Register(query, "test")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Lookup(hash)
	}
}

func BenchmarkAPQHit(b *testing.B) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	query := "{ health { status } }"
	hash := ComputeHash(query)
	store.RegisterWithHash(hash, query)

	req := &GraphQLRequest{
		Extensions: map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    float64(1),
				"sha256Hash": hash,
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.ProcessAPQ(req)
	}
}

func BenchmarkConcurrentLookup(b *testing.B) {
	store := NewPersistedQueryStore(DefaultPersistedQueryConfig())
	query := "{ health { status } }"
	hash, _ := store.Register(query, "test")

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			store.Lookup(hash)
		}
	})
}
