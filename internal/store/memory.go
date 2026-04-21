// Package store implements the in-memory data store with thread-safe
// sliding window rate limiting.
//
// Concurrency Design:
//   Unlike Node.js (single-threaded), Go serves each HTTP request in its
//   own goroutine. Multiple goroutines run truly in parallel on multiple
//   CPU cores. Without synchronization, concurrent read-modify-write
//   operations on shared maps would cause data races and panics.
//
//   This store uses sync.Mutex to protect all shared state. The critical
//   section in CheckAndRecord is kept as short as possible — the lock is
//   held only during the atomic check-prune-insert operation, ensuring
//   that rate limiting remains accurate even under heavy parallel load.
//
//   A sync.RWMutex could be used for read-heavy workloads (stats queries),
//   but since writes are frequent and the critical section is small, a
//   regular Mutex avoids the overhead of read-lock upgrades.
package store

import (
	"sort"
	"sync"
	"time"

	"github.com/rate-limited-api/internal/model"
)

// Config holds the rate limiter settings.
type Config struct {
	MaxRequests int
	WindowSize  time.Duration
}

// DefaultConfig returns the default rate limiter configuration.
func DefaultConfig() Config {
	return Config{
		MaxRequests: 5,
		WindowSize:  time.Minute,
	}
}

// MemoryStore is a thread-safe in-memory store for request records
// and rate limiting state.
type MemoryStore struct {
	mu sync.Mutex

	// rateLimitWindows tracks per-user request timestamps within the
	// sliding window. Used for rate limit decisions.
	rateLimitWindows map[string][]time.Time

	// records stores all processed request records, keyed by user_id.
	records map[string][]model.RequestRecord

	config Config
}

// New creates a new MemoryStore with the given configuration.
func New(config Config) *MemoryStore {
	return &MemoryStore{
		rateLimitWindows: make(map[string][]time.Time),
		records:          make(map[string][]model.RequestRecord),
		config:           config,
	}
}

// CheckAndRecord atomically checks the rate limit and, if allowed,
// records the request. This is the core operation that MUST be atomic
// to prevent race conditions under concurrent access.
//
// The entire check-prune-insert sequence runs under a single mutex lock,
// guaranteeing that no two goroutines can interleave between the
// "check remaining quota" and "consume a slot" steps.
func (s *MemoryStore) CheckAndRecord(userID string, payload map[string]interface{}, requestID string) (model.RateLimitResult, *model.RequestRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-s.config.WindowSize)

	// Step 1: Get the user's sliding window timestamps
	timestamps := s.rateLimitWindows[userID]

	// Step 2: Prune expired entries (outside the window)
	pruned := make([]time.Time, 0, len(timestamps))
	for _, ts := range timestamps {
		if ts.After(windowStart) {
			pruned = append(pruned, ts)
		}
	}

	// Step 3: Check if adding a new request would exceed the limit
	if len(pruned) >= s.config.MaxRequests {
		// Find the oldest timestamp to calculate retry-after
		oldest := pruned[0]
		for _, ts := range pruned[1:] {
			if ts.Before(oldest) {
				oldest = ts
			}
		}
		resetAt := oldest.Add(s.config.WindowSize)
		retryAfter := resetAt.Sub(now).Milliseconds()
		if retryAfter < 0 {
			retryAfter = 0
		}

		// Save pruned timestamps back
		s.rateLimitWindows[userID] = pruned

		return model.RateLimitResult{
			Allowed:      false,
			Remaining:    0,
			ResetAt:      resetAt,
			RetryAfterMs: retryAfter,
		}, nil
	}

	// Step 4: Record the timestamp and store the request
	pruned = append(pruned, now)
	s.rateLimitWindows[userID] = pruned

	record := model.RequestRecord{
		ID:        requestID,
		UserID:    userID,
		Payload:   payload,
		Timestamp: now,
	}

	s.records[userID] = append(s.records[userID], record)

	// Step 5: Calculate reset time from the oldest entry in window
	oldest := pruned[0]
	for _, ts := range pruned[1:] {
		if ts.Before(oldest) {
			oldest = ts
		}
	}

	return model.RateLimitResult{
		Allowed:   true,
		Remaining: s.config.MaxRequests - len(pruned),
		ResetAt:   oldest.Add(s.config.WindowSize),
	}, &record
}

// GetStats returns aggregated per-user statistics.
func (s *MemoryStore) GetStats() model.StatsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-s.config.WindowSize)

	users := make([]model.UserStats, 0, len(s.records))
	totalRequests := 0

	for userID, records := range s.records {
		// Count active timestamps in the current window
		timestamps := s.rateLimitWindows[userID]
		activeCount := 0
		for _, ts := range timestamps {
			if ts.After(windowStart) {
				activeCount++
			}
		}

		remaining := s.config.MaxRequests - activeCount
		if remaining < 0 {
			remaining = 0
		}

		// Sort records by timestamp
		sorted := make([]model.RequestRecord, len(records))
		copy(sorted, records)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Timestamp.Before(sorted[j].Timestamp)
		})

		var oldest, newest *string
		if len(sorted) > 0 {
			o := sorted[0].Timestamp.UTC().Format(time.RFC3339Nano)
			n := sorted[len(sorted)-1].Timestamp.UTC().Format(time.RFC3339Nano)
			oldest = &o
			newest = &n
		}

		users = append(users, model.UserStats{
			UserID:            userID,
			TotalRequests:     len(records),
			RequestsInWindow:  activeCount,
			RemainingRequests: remaining,
			OldestRequest:     oldest,
			NewestRequest:     newest,
		})

		totalRequests += len(records)
	}

	return model.StatsResponse{
		TotalUsers:    len(users),
		TotalRequests: totalRequests,
		Users:         users,
	}
}

// GetConfig returns the current configuration.
func (s *MemoryStore) GetConfig() Config {
	return s.config
}

// Reset clears all stored data. Used for testing.
func (s *MemoryStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.rateLimitWindows = make(map[string][]time.Time)
	s.records = make(map[string][]model.RequestRecord)
}
