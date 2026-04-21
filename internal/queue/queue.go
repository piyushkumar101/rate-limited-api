// Package queue implements a request queue with retry logic for
// rate-limited requests. Instead of immediately rejecting requests that
// exceed the rate limit, they can be queued and retried with exponential
// backoff until they succeed or the max retries are exhausted.
//
// Architecture:
//   - Requests that hit the rate limit are added to a per-user queue
//   - A background worker goroutine processes queued items
//   - Each retry uses exponential backoff (100ms, 200ms, 400ms, ...)
//   - An optional callback is invoked on success or final failure
//   - The queue is bounded to prevent memory exhaustion
package queue

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rate-limited-api/internal/store"
)

// QueuedRequest represents a rate-limited request waiting to be retried.
type QueuedRequest struct {
	ID        string                 `json:"id"`
	UserID    string                 `json:"user_id"`
	Payload   map[string]interface{} `json:"payload"`
	RequestID string                 `json:"request_id"`

	// Retry state
	Attempts    int       `json:"attempts"`
	MaxRetries  int       `json:"max_retries"`
	NextRetryAt time.Time `json:"next_retry_at"`
	QueuedAt    time.Time `json:"queued_at"`

	// Result
	Status string `json:"status"` // "queued", "processing", "completed", "failed"
}

// QueueStats holds queue statistics.
type QueueStats struct {
	TotalQueued    int `json:"total_queued"`
	TotalCompleted int `json:"total_completed"`
	TotalFailed    int `json:"total_failed"`
	CurrentSize    int `json:"current_size"`
}

// Config holds queue configuration.
type Config struct {
	MaxRetries     int           // Maximum retry attempts per request
	InitialBackoff time.Duration // Initial backoff duration (doubles each retry)
	MaxBackoff     time.Duration // Maximum backoff cap
	MaxQueueSize   int           // Maximum number of queued items
	WorkerInterval time.Duration // How often the worker checks the queue
}

// DefaultConfig returns sensible defaults for the queue.
func DefaultConfig() Config {
	return Config{
		MaxRetries:     5,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     30 * time.Second,
		MaxQueueSize:   1000,
		WorkerInterval: 50 * time.Millisecond,
	}
}

// RequestQueue manages queued requests with retry logic.
type RequestQueue struct {
	mu sync.Mutex

	store  store.Store
	config Config

	queue     []*QueuedRequest
	completed []*QueuedRequest
	failed    []*QueuedRequest

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// New creates and starts a new RequestQueue.
func New(s store.Store, config Config) *RequestQueue {
	q := &RequestQueue{
		store:  s,
		config: config,
		queue:  make([]*QueuedRequest, 0),
		stopCh: make(chan struct{}),
	}

	// Start the background worker
	q.wg.Add(1)
	go q.worker()

	log.Printf("[QUEUE] Started with maxRetries=%d, initialBackoff=%v, maxQueueSize=%d",
		config.MaxRetries, config.InitialBackoff, config.MaxQueueSize)

	return q
}

// Enqueue adds a rate-limited request to the retry queue.
// Returns an error if the queue is full.
func (q *RequestQueue) Enqueue(userID string, payload map[string]interface{}, requestID string) (*QueuedRequest, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.queue) >= q.config.MaxQueueSize {
		return nil, fmt.Errorf("queue is full (%d/%d)", len(q.queue), q.config.MaxQueueSize)
	}

	item := &QueuedRequest{
		ID:          fmt.Sprintf("q-%s", requestID),
		UserID:      userID,
		Payload:     payload,
		RequestID:   requestID,
		Attempts:    0,
		MaxRetries:  q.config.MaxRetries,
		NextRetryAt: time.Now().Add(q.config.InitialBackoff),
		QueuedAt:    time.Now(),
		Status:      "queued",
	}

	q.queue = append(q.queue, item)

	log.Printf("[QUEUE] Enqueued request %s for user '%s' (queue size: %d)",
		requestID, userID, len(q.queue))

	return item, nil
}

// GetStats returns queue statistics.
func (q *RequestQueue) GetStats() QueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()

	return QueueStats{
		TotalQueued:    len(q.queue) + len(q.completed) + len(q.failed),
		TotalCompleted: len(q.completed),
		TotalFailed:    len(q.failed),
		CurrentSize:    len(q.queue),
	}
}

// GetQueuedRequest returns the status of a queued request by ID.
func (q *RequestQueue) GetQueuedRequest(queueID string) *QueuedRequest {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, item := range q.queue {
		if item.ID == queueID {
			return item
		}
	}
	for _, item := range q.completed {
		if item.ID == queueID {
			return item
		}
	}
	for _, item := range q.failed {
		if item.ID == queueID {
			return item
		}
	}
	return nil
}

// Stop gracefully shuts down the queue worker.
func (q *RequestQueue) Stop() {
	close(q.stopCh)
	q.wg.Wait()
	log.Println("[QUEUE] Worker stopped")
}

// worker is the background goroutine that processes queued requests.
func (q *RequestQueue) worker() {
	defer q.wg.Done()

	ticker := time.NewTicker(q.config.WorkerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-q.stopCh:
			return
		case <-ticker.C:
			q.processQueue()
		}
	}
}

// processQueue attempts to process ready items from the queue.
func (q *RequestQueue) processQueue() {
	q.mu.Lock()

	now := time.Now()
	var remaining []*QueuedRequest

	// Collect items that are ready to retry
	var ready []*QueuedRequest
	for _, item := range q.queue {
		if now.After(item.NextRetryAt) || now.Equal(item.NextRetryAt) {
			ready = append(ready, item)
		} else {
			remaining = append(remaining, item)
		}
	}

	q.queue = remaining
	q.mu.Unlock()

	// Process ready items outside the lock to avoid holding it during store calls
	for _, item := range ready {
		item.Attempts++
		item.Status = "processing"

		result, record := q.store.CheckAndRecord(item.UserID, item.Payload, item.RequestID)

		if result.Allowed && record != nil {
			// Success!
			item.Status = "completed"
			q.mu.Lock()
			q.completed = append(q.completed, item)
			q.mu.Unlock()

			log.Printf("[QUEUE] ✓ Request %s completed after %d attempt(s)", item.RequestID, item.Attempts)
		} else if item.Attempts >= item.MaxRetries {
			// Max retries exhausted
			item.Status = "failed"
			q.mu.Lock()
			q.failed = append(q.failed, item)
			q.mu.Unlock()

			log.Printf("[QUEUE] ✗ Request %s failed after %d attempts (max retries exhausted)", item.RequestID, item.Attempts)
		} else {
			// Schedule next retry with exponential backoff
			backoff := q.calculateBackoff(item.Attempts)
			item.NextRetryAt = time.Now().Add(backoff)
			item.Status = "queued"

			q.mu.Lock()
			q.queue = append(q.queue, item)
			q.mu.Unlock()

			log.Printf("[QUEUE] ↻ Request %s retry #%d scheduled in %v", item.RequestID, item.Attempts, backoff)
		}
	}
}

// calculateBackoff returns the backoff duration for a given attempt number.
// Uses exponential backoff: initial * 2^(attempt-1), capped at maxBackoff.
func (q *RequestQueue) calculateBackoff(attempt int) time.Duration {
	backoff := q.config.InitialBackoff
	for i := 1; i < attempt; i++ {
		backoff *= 2
		if backoff > q.config.MaxBackoff {
			return q.config.MaxBackoff
		}
	}
	return backoff
}

// Reset clears all queue state. Used for testing.
func (q *RequestQueue) Reset() {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.queue = make([]*QueuedRequest, 0)
	q.completed = make([]*QueuedRequest, 0)
	q.failed = make([]*QueuedRequest, 0)
}
