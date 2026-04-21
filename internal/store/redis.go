package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rate-limited-api/internal/model"
)

// luaCheckAndRecord is a Lua script that runs atomically on Redis.
// It implements the sliding window rate limit check + record in a single
// atomic operation using Redis sorted sets.
//
// Why Lua? Redis executes Lua scripts atomically — no other Redis commands
// can run between the lines of this script. This gives us the same
// atomicity guarantee as sync.Mutex in the memory store, but works
// across multiple API server instances (distributed rate limiting).
//
// Data model:
//   - Sorted Set  "ratelimit:{user_id}" → scores are timestamps (ms)
//   - Hash        "record:{request_id}" → the request record as JSON
//   - Set         "users"               → set of all user IDs
//   - List        "records:{user_id}"   → list of request IDs for this user
const luaCheckAndRecord = `
local ratelimit_key = KEYS[1]
local records_key   = KEYS[2]
local users_key     = KEYS[3]

local max_requests  = tonumber(ARGV[1])
local window_ms     = tonumber(ARGV[2])
local now_ms        = tonumber(ARGV[3])
local request_id    = ARGV[4]
local record_json   = ARGV[5]
local user_id       = ARGV[6]

local window_start = now_ms - window_ms

-- Step 1: Remove expired entries from the sorted set
redis.call('ZREMRANGEBYSCORE', ratelimit_key, '-inf', window_start)

-- Step 2: Count current entries in the window
local current_count = redis.call('ZCARD', ratelimit_key)

-- Step 3: Check if limit would be exceeded
if current_count >= max_requests then
    -- Find the oldest entry to calculate retry-after
    local oldest = redis.call('ZRANGE', ratelimit_key, 0, 0, 'WITHSCORES')
    local oldest_ts = tonumber(oldest[2])
    local reset_at = oldest_ts + window_ms
    local retry_after = reset_at - now_ms
    if retry_after < 0 then retry_after = 0 end
    return {0, current_count, reset_at, retry_after}
end

-- Step 4: Add the new timestamp
redis.call('ZADD', ratelimit_key, now_ms, request_id)

-- Step 5: Set TTL on the rate limit key (auto-cleanup)
redis.call('PEXPIRE', ratelimit_key, window_ms + 1000)

-- Step 6: Store the request record
redis.call('SET', 'record:' .. request_id, record_json, 'PX', 86400000)
redis.call('RPUSH', records_key, request_id)
redis.call('SADD', users_key, user_id)

-- Step 7: Calculate remaining and reset time
local new_count = current_count + 1
local remaining = max_requests - new_count
local oldest_after = redis.call('ZRANGE', ratelimit_key, 0, 0, 'WITHSCORES')
local reset_at = tonumber(oldest_after[2]) + window_ms

return {1, remaining, reset_at, 0}
`

// RedisStore implements the Store interface using Redis as the backend.
// It supports distributed rate limiting across multiple API instances.
type RedisStore struct {
	client *redis.Client
	config Config
	script *redis.Script
}

// NewRedisStore creates a new Redis-backed store.
func NewRedisStore(addr, password string, db int, config Config) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		PoolSize:     20,
	})

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis at %s: %w", addr, err)
	}

	log.Printf("[STORE] Connected to Redis at %s (db=%d)", addr, db)

	return &RedisStore{
		client: client,
		config: config,
		script: redis.NewScript(luaCheckAndRecord),
	}, nil
}

// CheckAndRecord atomically checks rate limit and records the request using
// a Redis Lua script. The entire operation is atomic within Redis.
func (s *RedisStore) CheckAndRecord(userID string, payload map[string]interface{}, requestID string) (model.RateLimitResult, *model.RequestRecord) {
	ctx := context.Background()
	now := time.Now()
	nowMs := now.UnixMilli()

	record := model.RequestRecord{
		ID:        requestID,
		UserID:    userID,
		Payload:   payload,
		Timestamp: now,
	}

	recordJSON, err := json.Marshal(record)
	if err != nil {
		log.Printf("[ERROR] Failed to marshal record: %v", err)
		return model.RateLimitResult{Allowed: false, Remaining: 0}, nil
	}

	keys := []string{
		fmt.Sprintf("ratelimit:%s", userID),
		fmt.Sprintf("records:%s", userID),
		"users",
	}

	result, err := s.script.Run(ctx, s.client, keys,
		s.config.MaxRequests,
		s.config.WindowSize.Milliseconds(),
		nowMs,
		requestID,
		string(recordJSON),
		userID,
	).Int64Slice()

	if err != nil {
		log.Printf("[ERROR] Redis Lua script failed: %v", err)
		return model.RateLimitResult{Allowed: false, Remaining: 0}, nil
	}

	allowed := result[0] == 1
	remaining := int(result[1])
	resetAtMs := result[2]
	retryAfterMs := result[3]

	rateLimitResult := model.RateLimitResult{
		Allowed:      allowed,
		Remaining:    remaining,
		ResetAt:      time.UnixMilli(resetAtMs),
		RetryAfterMs: retryAfterMs,
	}

	if !allowed {
		return rateLimitResult, nil
	}

	return rateLimitResult, &record
}

// GetStats returns per-user statistics from Redis.
func (s *RedisStore) GetStats() model.StatsResponse {
	ctx := context.Background()
	now := time.Now()
	windowStartMs := now.Add(-s.config.WindowSize).UnixMilli()

	// Get all user IDs
	userIDs, err := s.client.SMembers(ctx, "users").Result()
	if err != nil {
		log.Printf("[ERROR] Failed to get users from Redis: %v", err)
		return model.StatsResponse{Users: []model.UserStats{}}
	}

	users := make([]model.UserStats, 0, len(userIDs))
	totalRequests := 0

	for _, userID := range userIDs {
		// Count active requests in current window
		rateLimitKey := fmt.Sprintf("ratelimit:%s", userID)
		activeCount, _ := s.client.ZCount(ctx, rateLimitKey,
			strconv.FormatInt(windowStartMs, 10), "+inf").Result()

		remaining := s.config.MaxRequests - int(activeCount)
		if remaining < 0 {
			remaining = 0
		}

		// Get all request IDs for this user
		recordsKey := fmt.Sprintf("records:%s", userID)
		requestIDs, _ := s.client.LRange(ctx, recordsKey, 0, -1).Result()

		// Fetch actual records to get timestamps
		var records []model.RequestRecord
		for _, reqID := range requestIDs {
			recordJSON, err := s.client.Get(ctx, "record:"+reqID).Result()
			if err != nil {
				continue // Record may have expired
			}
			var rec model.RequestRecord
			if err := json.Unmarshal([]byte(recordJSON), &rec); err == nil {
				records = append(records, rec)
			}
		}

		if len(records) == 0 {
			continue
		}

		sort.Slice(records, func(i, j int) bool {
			return records[i].Timestamp.Before(records[j].Timestamp)
		})

		var oldest, newest *string
		if len(records) > 0 {
			o := records[0].Timestamp.UTC().Format(time.RFC3339Nano)
			n := records[len(records)-1].Timestamp.UTC().Format(time.RFC3339Nano)
			oldest = &o
			newest = &n
		}

		users = append(users, model.UserStats{
			UserID:            userID,
			TotalRequests:     len(records),
			RequestsInWindow:  int(activeCount),
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
func (s *RedisStore) GetConfig() Config {
	return s.config
}

// Reset clears all rate limiting and request data from Redis.
func (s *RedisStore) Reset() {
	ctx := context.Background()

	userIDs, _ := s.client.SMembers(ctx, "users").Result()
	for _, userID := range userIDs {
		s.client.Del(ctx, fmt.Sprintf("ratelimit:%s", userID))
		recordsKey := fmt.Sprintf("records:%s", userID)
		requestIDs, _ := s.client.LRange(ctx, recordsKey, 0, -1).Result()
		for _, reqID := range requestIDs {
			s.client.Del(ctx, "record:"+reqID)
		}
		s.client.Del(ctx, recordsKey)
	}
	s.client.Del(ctx, "users")
}

// Close shuts down the Redis connection.
func (s *RedisStore) Close() error {
	return s.client.Close()
}
