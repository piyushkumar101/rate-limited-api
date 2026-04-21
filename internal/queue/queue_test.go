package queue

import (
	"testing"
	"time"

	"github.com/rate-limited-api/internal/store"
)

func TestEnqueue(t *testing.T) {
	s := store.New(store.Config{MaxRequests: 5, WindowSize: time.Minute})
	q := New(s, DefaultConfig())
	defer q.Stop()

	item, err := q.Enqueue("user1", map[string]interface{}{"a": 1}, "req-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.Status != "queued" {
		t.Fatalf("expected status 'queued', got '%s'", item.Status)
	}
	if item.UserID != "user1" {
		t.Fatalf("expected user_id 'user1', got '%s'", item.UserID)
	}

	stats := q.GetStats()
	if stats.CurrentSize != 1 {
		t.Fatalf("expected queue size 1, got %d", stats.CurrentSize)
	}
}

func TestEnqueue_MaxQueueSize(t *testing.T) {
	s := store.New(store.Config{MaxRequests: 5, WindowSize: time.Minute})
	cfg := DefaultConfig()
	cfg.MaxQueueSize = 3
	q := New(s, cfg)
	defer q.Stop()

	for i := 0; i < 3; i++ {
		_, err := q.Enqueue("user1", map[string]interface{}{"i": i}, "req")
		if err != nil {
			t.Fatalf("enqueue %d should succeed: %v", i, err)
		}
	}

	_, err := q.Enqueue("user1", map[string]interface{}{"i": 3}, "req-4")
	if err == nil {
		t.Fatal("expected error when queue is full")
	}
}

func TestQueue_RetriesAndCompletes(t *testing.T) {
	// Use a very short window so requests expire quickly
	s := store.New(store.Config{MaxRequests: 1, WindowSize: 100 * time.Millisecond})
	cfg := Config{
		MaxRetries:     10,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
		MaxQueueSize:   100,
		WorkerInterval: 20 * time.Millisecond,
	}
	q := New(s, cfg)
	defer q.Stop()

	// Fill the rate limit
	s.CheckAndRecord("user1", map[string]interface{}{"a": 1}, "req-0")

	// Queue a request that should eventually succeed after the window expires
	item, _ := q.Enqueue("user1", map[string]interface{}{"a": 2}, "req-1")

	// Wait for the queue to process (window expires after 100ms)
	time.Sleep(500 * time.Millisecond)

	found := q.GetQueuedRequest(item.ID)
	if found == nil {
		t.Fatal("expected to find queued request")
	}
	if found.Status != "completed" {
		t.Fatalf("expected status 'completed', got '%s' (attempts: %d)", found.Status, found.Attempts)
	}
}

func TestQueue_FailsAfterMaxRetries(t *testing.T) {
	// Use a long window so requests never expire during the test
	s := store.New(store.Config{MaxRequests: 1, WindowSize: 10 * time.Second})
	cfg := Config{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		MaxQueueSize:   100,
		WorkerInterval: 5 * time.Millisecond,
	}
	q := New(s, cfg)
	defer q.Stop()

	// Fill the rate limit permanently
	s.CheckAndRecord("user1", map[string]interface{}{"a": 1}, "req-0")

	// Queue a request that will never succeed
	item, _ := q.Enqueue("user1", map[string]interface{}{"a": 2}, "req-1")

	// Wait for retries to exhaust
	time.Sleep(500 * time.Millisecond)

	found := q.GetQueuedRequest(item.ID)
	if found == nil {
		t.Fatal("expected to find queued request")
	}
	if found.Status != "failed" {
		t.Fatalf("expected status 'failed', got '%s' (attempts: %d)", found.Status, found.Attempts)
	}
	if found.Attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", found.Attempts)
	}
}

func TestQueue_Stats(t *testing.T) {
	s := store.New(store.Config{MaxRequests: 5, WindowSize: time.Minute})
	q := New(s, DefaultConfig())
	defer q.Stop()

	stats := q.GetStats()
	if stats.TotalQueued != 0 || stats.CurrentSize != 0 {
		t.Fatal("expected empty queue stats")
	}

	q.Enqueue("user1", map[string]interface{}{"a": 1}, "req-1")
	q.Enqueue("user1", map[string]interface{}{"a": 2}, "req-2")

	stats = q.GetStats()
	if stats.CurrentSize < 1 { // may have been processed already
		// At least total should be 2
		if stats.TotalQueued < 2 {
			t.Fatalf("expected at least 2 total queued, got %d", stats.TotalQueued)
		}
	}
}
