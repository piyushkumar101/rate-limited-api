// Package store defines the Store interface and provides both
// in-memory and Redis-backed implementations.
package store

import "github.com/rate-limited-api/internal/model"

// Store is the interface that both MemoryStore and RedisStore implement.
// This allows the application to switch between backends via configuration
// without changing any handler or business logic code.
type Store interface {
	// CheckAndRecord atomically checks the rate limit and, if allowed,
	// records the request. Returns the rate limit result and the stored
	// record (nil if rejected).
	CheckAndRecord(userID string, payload map[string]interface{}, requestID string) (model.RateLimitResult, *model.RequestRecord)

	// GetStats returns aggregated per-user statistics.
	GetStats() model.StatsResponse

	// GetConfig returns the rate limiter configuration.
	GetConfig() Config

	// Reset clears all data (used for testing).
	Reset()
}
