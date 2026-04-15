package queue

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/helios/helios/internal/plugin"
)

type memoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMemoryStore() *memoryStore {
	return &memoryStore{data: make(map[string][]byte)}
}

func (s *memoryStore) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data[key]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out, true
}

func (s *memoryStore) Set(key string, value []byte, _ int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(value))
	copy(out, value)
	s.data[key] = out
}

func (s *memoryStore) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.data[key]
	if exists {
		delete(s.data, key)
	}
	return exists
}

func (s *memoryStore) Scan(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0)
	for key := range s.data {
		if prefix == "" || (len(key) >= len(prefix) && key[:len(prefix)] == prefix) {
			keys = append(keys, key)
		}
	}
	return keys
}

func TestQueuePublishesPluginEvents(t *testing.T) {
	store := newMemoryStore()
	q := NewQueue(store, DefaultConfig())

	audit := plugin.NewAuditPlugin()
	manager := plugin.NewManager(plugin.ManagerConfig{})
	if err := manager.Register(audit, plugin.RuntimeConfig{Enabled: true, Settings: map[string]interface{}{"capture_events": true}}); err != nil {
		t.Fatalf("register audit plugin: %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start plugin manager: %v", err)
	}
	defer manager.Stop(context.Background())

	q.SetPluginManager(manager)

	jobID, err := q.Enqueue(map[string]interface{}{"task": "email"}, "")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	job, err := q.Dequeue("worker-1")
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if job.ID != jobID {
		t.Fatalf("expected dequeued job %s, got %s", jobID, job.ID)
	}

	if err := q.Ack(job.ID); err != nil {
		t.Fatalf("ack: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		records := audit.Records()
		seen := map[string]bool{}
		for _, rec := range records {
			if rec.Kind == "event" {
				seen[rec.EventType] = true
			}
		}
		if seen["queue.job.enqueued"] && seen["queue.job.dequeued"] && seen["queue.job.acked"] {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("expected queue events were not captured by audit plugin, records=%v", audit.Records())
}
