package graphql

import (
	"context"
	"sync"
	"testing"
	"time"
)

// MockAuthService for testing
type mockAuthServiceForLoader struct {
	users    map[string]*User
	byName   map[string]*User
	getCalls int
	mu       sync.Mutex
}

func newMockAuthServiceForLoader() *mockAuthServiceForLoader {
	return &mockAuthServiceForLoader{
		users:  make(map[string]*User),
		byName: make(map[string]*User),
	}
}

func (m *mockAuthServiceForLoader) addUser(id, username string) {
	user := &User{
		ID:        id,
		Username:  username,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	m.users[id] = user
	m.byName[username] = user
}

func (m *mockAuthServiceForLoader) getUser(id string) (*User, error) {
	m.mu.Lock()
	m.getCalls++
	m.mu.Unlock()
	
	if user, ok := m.users[id]; ok {
		return user, nil
	}
	return nil, nil
}

func (m *mockAuthServiceForLoader) getUserByName(name string) (*User, error) {
	m.mu.Lock()
	m.getCalls++
	m.mu.Unlock()
	
	if user, ok := m.byName[name]; ok {
		return user, nil
	}
	return nil, nil
}

// TestUserLoaderBatching tests that multiple Load calls are batched
func TestUserLoaderBatching(t *testing.T) {
	batchCalls := 0
	batchSizes := []int{}
	
	config := LoaderConfig{
		Wait:     10 * time.Millisecond,
		MaxBatch: 100,
		Cache:    true,
	}
	
	batchFn := func(ids []string) []*UserResult {
		batchCalls++
		batchSizes = append(batchSizes, len(ids))
		
		results := make([]*UserResult, len(ids))
		for i, id := range ids {
			results[i] = &UserResult{
				User: &User{
					ID:       id,
					Username: "user_" + id,
				},
			}
		}
		return results
	}
	
	loader := NewUserLoader(config, batchFn)
	ctx := context.Background()
	
	// Make 10 concurrent requests
	var wg sync.WaitGroup
	users := make([]*User, 10)
	errors := make([]error, 10)
	
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			users[idx], errors[idx] = loader.Load(ctx, string(rune('a'+idx)))
		}(i)
	}
	
	wg.Wait()
	
	// Verify all users were loaded
	for i, user := range users {
		if user == nil {
			t.Errorf("User %d was nil", i)
		}
		if errors[i] != nil {
			t.Errorf("Error loading user %d: %v", i, errors[i])
		}
	}
	
	// Verify batching occurred (should be 1-2 batches, not 10)
	if batchCalls > 2 {
		t.Errorf("Expected 1-2 batch calls, got %d (sizes: %v)", batchCalls, batchSizes)
	}
	
	t.Logf("Batch calls: %d, sizes: %v", batchCalls, batchSizes)
}

// TestUserLoaderCaching tests that results are cached
func TestUserLoaderCaching(t *testing.T) {
	batchCalls := 0
	
	config := LoaderConfig{
		Wait:     5 * time.Millisecond,
		MaxBatch: 100,
		Cache:    true,
	}
	
	batchFn := func(ids []string) []*UserResult {
		batchCalls++
		results := make([]*UserResult, len(ids))
		for i, id := range ids {
			results[i] = &UserResult{
				User: &User{
					ID:       id,
					Username: "user_" + id,
				},
			}
		}
		return results
	}
	
	loader := NewUserLoader(config, batchFn)
	ctx := context.Background()
	
	// First load
	user1, err := loader.Load(ctx, "user1")
	if err != nil {
		t.Fatalf("First load failed: %v", err)
	}
	if user1 == nil {
		t.Fatal("First load returned nil user")
	}
	
	// Wait for batch to complete
	time.Sleep(20 * time.Millisecond)
	
	firstBatchCalls := batchCalls
	
	// Second load of same key (should use cache)
	user2, err := loader.Load(ctx, "user1")
	if err != nil {
		t.Fatalf("Second load failed: %v", err)
	}
	if user2 == nil {
		t.Fatal("Second load returned nil user")
	}
	
	// Verify cache was used (no additional batch call)
	if batchCalls != firstBatchCalls {
		t.Errorf("Expected cache hit, but batch was called again. Calls: %d -> %d", firstBatchCalls, batchCalls)
	}
	
	// Verify same user returned
	if user1.ID != user2.ID {
		t.Errorf("Cache returned different user: %s vs %s", user1.ID, user2.ID)
	}
}

// TestUserLoaderClear tests cache clearing
func TestUserLoaderClear(t *testing.T) {
	batchCalls := 0
	
	config := LoaderConfig{
		Wait:     5 * time.Millisecond,
		MaxBatch: 100,
		Cache:    true,
	}
	
	batchFn := func(ids []string) []*UserResult {
		batchCalls++
		results := make([]*UserResult, len(ids))
		for i, id := range ids {
			results[i] = &UserResult{
				User: &User{
					ID:       id,
					Username: "user_" + id + "_v" + string(rune('0'+batchCalls)),
				},
			}
		}
		return results
	}
	
	loader := NewUserLoader(config, batchFn)
	ctx := context.Background()
	
	// First load
	user1, _ := loader.Load(ctx, "user1")
	time.Sleep(20 * time.Millisecond)
	
	// Clear cache
	loader.Clear("user1")
	
	// Second load (should trigger new batch)
	user2, _ := loader.Load(ctx, "user1")
	time.Sleep(20 * time.Millisecond)
	
	if batchCalls < 2 {
		t.Errorf("Expected 2 batch calls after clear, got %d", batchCalls)
	}
	
	// Verify different versions
	if user1.Username == user2.Username {
		t.Error("Expected different usernames after clear")
	}
}

// TestUserLoaderLoadMany tests loading multiple users at once
func TestUserLoaderLoadMany(t *testing.T) {
	config := LoaderConfig{
		Wait:     5 * time.Millisecond,
		MaxBatch: 100,
		Cache:    true,
	}
	
	batchFn := func(ids []string) []*UserResult {
		results := make([]*UserResult, len(ids))
		for i, id := range ids {
			results[i] = &UserResult{
				User: &User{
					ID:       id,
					Username: "user_" + id,
				},
			}
		}
		return results
	}
	
	loader := NewUserLoader(config, batchFn)
	ctx := context.Background()
	
	ids := []string{"a", "b", "c", "d", "e"}
	users, errors := loader.LoadMany(ctx, ids)
	
	if len(users) != len(ids) {
		t.Errorf("Expected %d users, got %d", len(ids), len(users))
	}
	
	for i, user := range users {
		if user == nil {
			t.Errorf("User %d was nil", i)
		}
		if errors[i] != nil {
			t.Errorf("Error loading user %d: %v", i, errors[i])
		}
		if user != nil && user.ID != ids[i] {
			t.Errorf("Wrong user ID: expected %s, got %s", ids[i], user.ID)
		}
	}
}

// TestUserLoaderPrime tests priming the cache
func TestUserLoaderPrime(t *testing.T) {
	batchCalls := 0
	
	config := LoaderConfig{
		Wait:     5 * time.Millisecond,
		MaxBatch: 100,
		Cache:    true,
	}
	
	batchFn := func(ids []string) []*UserResult {
		batchCalls++
		results := make([]*UserResult, len(ids))
		for i, id := range ids {
			results[i] = &UserResult{
				User: &User{
					ID:       id,
					Username: "loaded_" + id,
				},
			}
		}
		return results
	}
	
	loader := NewUserLoader(config, batchFn)
	ctx := context.Background()
	
	// Prime the cache
	primedUser := &User{
		ID:       "user1",
		Username: "primed_user",
	}
	loader.Prime("user1", primedUser, nil)
	
	// Load should use primed value
	user, err := loader.Load(ctx, "user1")
	
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	
	if user.Username != "primed_user" {
		t.Errorf("Expected primed username, got %s", user.Username)
	}
	
	// No batch call should have been made
	time.Sleep(20 * time.Millisecond)
	if batchCalls != 0 {
		t.Errorf("Expected 0 batch calls (cache hit), got %d", batchCalls)
	}
}

// TestKeyLoaderBatching tests key-value batching
func TestKeyLoaderBatching(t *testing.T) {
	batchCalls := 0
	
	config := LoaderConfig{
		Wait:     10 * time.Millisecond,
		MaxBatch: 100,
		Cache:    true,
	}
	
	batchFn := func(keys []string) []*KeyResult {
		batchCalls++
		results := make([]*KeyResult, len(keys))
		for i, key := range keys {
			results[i] = &KeyResult{
				KVPair: &KVPair{
					Key:   key,
					Value: "value_" + key,
				},
			}
		}
		return results
	}
	
	loader := NewKeyLoader(config, batchFn)
	ctx := context.Background()
	
	// Make concurrent requests
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			loader.Load(ctx, "key"+string(rune('1'+idx)))
		}(i)
	}
	
	wg.Wait()
	time.Sleep(20 * time.Millisecond)
	
	// Should be batched into 1-2 calls
	if batchCalls > 2 {
		t.Errorf("Expected 1-2 batch calls, got %d", batchCalls)
	}
}

// TestMaxBatch tests that batches are split when exceeding max size
func TestMaxBatch(t *testing.T) {
	batchCalls := 0
	batchSizes := []int{}
	
	config := LoaderConfig{
		Wait:     50 * time.Millisecond,  // Long wait to ensure we hit max batch
		MaxBatch: 5,                       // Small max batch for testing
		Cache:    true,
	}
	
	batchFn := func(ids []string) []*UserResult {
		batchCalls++
		batchSizes = append(batchSizes, len(ids))
		
		results := make([]*UserResult, len(ids))
		for i, id := range ids {
			results[i] = &UserResult{
				User: &User{ID: id},
			}
		}
		return results
	}
	
	loader := NewUserLoader(config, batchFn)
	ctx := context.Background()
	
	// Make more requests than max batch
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			loader.Load(ctx, string(rune('a'+idx)))
		}(i)
	}
	
	wg.Wait()
	
	// Verify batches were split
	t.Logf("Batch calls: %d, sizes: %v", batchCalls, batchSizes)
	
	for _, size := range batchSizes {
		if size > config.MaxBatch {
			t.Errorf("Batch size %d exceeded max %d", size, config.MaxBatch)
		}
	}
}

// TestLoaderContextCancellation tests context cancellation
func TestLoaderContextCancellation(t *testing.T) {
	config := LoaderConfig{
		Wait:     100 * time.Millisecond, // Long wait
		MaxBatch: 100,
		Cache:    true,
	}
	
	batchFn := func(ids []string) []*UserResult {
		results := make([]*UserResult, len(ids))
		for i, id := range ids {
			results[i] = &UserResult{
				User: &User{ID: id},
			}
		}
		return results
	}
	
	loader := NewUserLoader(config, batchFn)
	
	// Create a context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	
	// Load should be cancelled before batch executes
	_, err := loader.Load(ctx, "user1")
	
	if err != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}
}

// TestContextWithLoaders tests adding/retrieving loaders from context
func TestContextWithLoaders(t *testing.T) {
	loaders := &Loaders{}
	
	ctx := context.Background()
	ctx = ContextWithLoaders(ctx, loaders)
	
	retrieved := LoadersFromContext(ctx)
	
	if retrieved != loaders {
		t.Error("Retrieved loaders don't match original")
	}
}

// TestLoadersFromContextNil tests retrieving loaders from context without loaders
func TestLoadersFromContextNil(t *testing.T) {
	ctx := context.Background()
	
	retrieved := LoadersFromContext(ctx)
	
	if retrieved != nil {
		t.Error("Expected nil loaders from empty context")
	}
}
