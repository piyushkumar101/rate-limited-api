package handler

import (
	"github.com/rate-limited-api/internal/queue"
	"github.com/rate-limited-api/internal/store"
)

// newTestQueue creates a queue for testing purposes.
func newTestQueue(s store.Store) *queue.RequestQueue {
	return queue.New(s, queue.DefaultConfig())
}
