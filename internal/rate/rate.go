package rate

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Bucket represents a token bucket for rate limiting
type Bucket struct {
	Tokens     int64     `json:"tokens"`
	LastRefill time.Time `json:"last_refill"`
	Capacity   int64     `json:"capacity"`
	RateNum    int64     `json:"rate_num"` // numerator for refill rate
	RateDen    int64     `json:"rate_den"` // denominator for refill rate
}

// Store defines the interface for rate limiter storage
type Store interface {
	Get(key string) ([]byte, bool)
	Set(key string, value []byte, ttl int64)
}

// Limiter handles rate limiting logic
type Limiter struct {
	store Store
	mu    sync.Mutex
}

// NewLimiter creates a new rate limiter
func NewLimiter(store Store) *Limiter {
	return &Limiter{
		store: store,
	}
}

// Allow checks if a request is allowed for a client
func (l *Limiter) Allow(clientID string, capacity, rateNum, rateDen int64) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucketKey := fmt.Sprintf("rate:%s", clientID)

	// Get or create bucket
	bucket, err := l.getBucket(bucketKey, capacity, rateNum, rateDen)
	if err != nil {
		return false, err
	}

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(bucket.LastRefill)

	// Calculate refill using integer arithmetic
	// refill = (elapsed_ms * rateNum) / (1000 * rateDen)
	elapsedMs := elapsed.Milliseconds()
	refill := (elapsedMs * rateNum) / (1000 * rateDen)

	// Update tokens
	bucket.Tokens += refill
	if bucket.Tokens > capacity {
		bucket.Tokens = capacity
	}
	bucket.LastRefill = now

	// Check if we can take a token
	allowed := false
	if bucket.Tokens >= 1 {
		bucket.Tokens--
		allowed = true
	}

	// Save bucket
	if err := l.saveBucket(bucketKey, bucket); err != nil {
		return false, err
	}

	return allowed, nil
}

// AllowN checks if N requests are allowed
func (l *Limiter) AllowN(clientID string, n int, capacity, rateNum, rateDen int64) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucketKey := fmt.Sprintf("rate:%s", clientID)

	bucket, err := l.getBucket(bucketKey, capacity, rateNum, rateDen)
	if err != nil {
		return false, err
	}

	now := time.Now()
	elapsed := now.Sub(bucket.LastRefill)
	elapsedMs := elapsed.Milliseconds()
	refill := (elapsedMs * rateNum) / (1000 * rateDen)

	bucket.Tokens += refill
	if bucket.Tokens > capacity {
		bucket.Tokens = capacity
	}
	bucket.LastRefill = now

	allowed := false
	if bucket.Tokens >= int64(n) {
		bucket.Tokens -= int64(n)
		allowed = true
	}

	if err := l.saveBucket(bucketKey, bucket); err != nil {
		return false, err
	}

	return allowed, nil
}

// GetBucketState returns current bucket state for a client
func (l *Limiter) GetBucketState(clientID string) (*Bucket, error) {
	bucketKey := fmt.Sprintf("rate:%s", clientID)
	data, exists := l.store.Get(bucketKey)
	if !exists {
		return nil, fmt.Errorf("bucket not found")
	}

	var bucket Bucket
	if err := json.Unmarshal(data, &bucket); err != nil {
		return nil, err
	}

	return &bucket, nil
}

// ResetBucket resets a client's bucket
func (l *Limiter) ResetBucket(clientID string, capacity, rateNum, rateDen int64) error {
	bucketKey := fmt.Sprintf("rate:%s", clientID)
	bucket := &Bucket{
		Tokens:     capacity,
		LastRefill: time.Now(),
		Capacity:   capacity,
		RateNum:    rateNum,
		RateDen:    rateDen,
	}
	return l.saveBucket(bucketKey, bucket)
}

// Helper functions

func (l *Limiter) getBucket(key string, capacity, rateNum, rateDen int64) (*Bucket, error) {
	data, exists := l.store.Get(key)
	if !exists {
		// Create new bucket
		return &Bucket{
			Tokens:     capacity,
			LastRefill: time.Now(),
			Capacity:   capacity,
			RateNum:    rateNum,
			RateDen:    rateDen,
		}, nil
	}

	var bucket Bucket
	if err := json.Unmarshal(data, &bucket); err != nil {
		return nil, err
	}

	return &bucket, nil
}

func (l *Limiter) saveBucket(key string, bucket *Bucket) error {
	data, err := json.Marshal(bucket)
	if err != nil {
		return err
	}
	l.store.Set(key, data, 0)
	return nil
}

// Config represents rate limiter configuration
type Config struct {
	DefaultCapacity int64
	DefaultRateNum  int64
	DefaultRateDen  int64
}

// DefaultConfig returns default rate limiter configuration
func DefaultConfig() *Config {
	return &Config{
		DefaultCapacity: 100,
		DefaultRateNum:  1, // 1 token
		DefaultRateDen:  1, // per second
	}
}
