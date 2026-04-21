# Rate-Limited API Service

A production-grade, thread-safe API service built in **Go** with per-user sliding window rate limiting. Designed to handle concurrent requests correctly using Go's native concurrency primitives.

---

## Features

- **POST `/request`** — Accept requests with `{ user_id, payload }`, rate-limited per user
- **GET `/stats`** — Return per-user request statistics
- **GET `/health`** — Service health check with uptime
- **Sliding Window Rate Limiting** — 5 requests per user per minute
- **Thread-Safe** — Mutex-protected store handles true parallel goroutine access
- **Standard Rate Limit Headers** — `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`, `Retry-After`
- **Graceful Shutdown** — Signal handling with configurable drain timeout
- **Docker Support** — Multi-stage build with non-root user
- **Comprehensive Tests** — Unit, integration, and concurrency tests (24 tests)

---

## Steps to Run

### Prerequisites

- **Go 1.21+** installed ([download](https://go.dev/dl/))

### Run Locally

```bash
# Clone the repository
git clone https://github.com/<your-username>/rate-limited-api.git
cd rate-limited-api

# Install dependencies
go mod download

# Run the server
go run main.go
```

The server starts on `http://localhost:3000`.

### Run Tests

```bash
# Run all tests
go test -v ./...

# Run with race detector (Linux/macOS)
go test -race -v ./...

# Run only concurrency tests
go test -v -run TestConcurrency ./internal/store/
```

### Run with Docker

```bash
# Build image
docker build -t rate-limited-api .

# Run container
docker run -p 3000:3000 rate-limited-api
```

### Configuration

| Variable | Default | Description                |
|----------|---------|----------------------------|
| `PORT`   | `3000`  | Server port                |
| `HOST`   | `0.0.0.0` | Bind address            |

```bash
PORT=8080 go run main.go
```

---

## API Reference

### POST /request

Submit a request for a user. Rate-limited to 5 requests per user per minute.

**Request:**
```json
{
  "user_id": "alice",
  "payload": {
    "action": "process_order",
    "order_id": 12345
  }
}
```

**Success (201):**
```json
{
  "success": true,
  "data": {
    "request_id": "a1b2c3d4-e5f6-...",
    "user_id": "alice",
    "remaining_requests": 4
  },
  "timestamp": "2024-01-15T10:30:00.123Z"
}
```

**Rate Limited (429):**
```json
{
  "success": false,
  "error": {
    "code": "RATE_LIMIT_EXCEEDED",
    "message": "Rate limit exceeded for user 'alice'. Maximum 5 requests per 1m0s.",
    "retry_after_ms": 45230
  },
  "timestamp": "2024-01-15T10:30:00.123Z"
}
```

**Response Headers:**
```
X-RateLimit-Limit: 5
X-RateLimit-Remaining: 4
X-RateLimit-Reset: 1705312260
Retry-After: 46          # Only on 429 responses
```

### GET /stats

Returns per-user request statistics.

**Response (200):**
```json
{
  "success": true,
  "data": {
    "total_users": 2,
    "total_requests": 7,
    "users": [
      {
        "user_id": "alice",
        "total_requests": 5,
        "requests_in_current_window": 3,
        "remaining_requests": 2,
        "oldest_request": "2024-01-15T10:28:00.000Z",
        "newest_request": "2024-01-15T10:30:00.000Z"
      },
      {
        "user_id": "bob",
        "total_requests": 2,
        "requests_in_current_window": 2,
        "remaining_requests": 3,
        "oldest_request": "2024-01-15T10:29:30.000Z",
        "newest_request": "2024-01-15T10:30:15.000Z"
      }
    ]
  },
  "timestamp": "2024-01-15T10:30:30.000Z"
}
```

### GET /health

```json
{
  "status": "healthy",
  "uptime_seconds": 3612.5,
  "timestamp": "2024-01-15T10:30:00.000Z"
}
```

---

## Design Decisions

### 1. Go as the Language Choice

Go was chosen specifically because this assignment emphasizes **concurrent request handling**. Unlike Node.js (single-threaded event loop), Go serves each HTTP request in its own goroutine and runs goroutines truly in parallel across CPU cores. This makes concurrency a real concern — not just a theoretical one — and demonstrates meaningful use of synchronization primitives.

### 2. Sliding Window Algorithm

The rate limiter uses a **sliding window** approach rather than fixed windows:

- **Fixed window** (e.g., "5 requests per calendar minute") has a boundary problem: a user could make 5 requests at 10:00:59 and 5 more at 10:01:01, effectively making 10 requests in 2 seconds.
- **Sliding window** tracks individual request timestamps and counts how many fall within the last 60 seconds from *now*. This provides smooth, accurate rate limiting.

### 3. Atomic Check-and-Record

The `CheckAndRecord` method performs the rate limit check and request recording under a **single mutex lock**:

```go
func (s *MemoryStore) CheckAndRecord(...) {
    s.mu.Lock()
    defer s.mu.Unlock()
    // 1. Prune expired timestamps
    // 2. Check if limit exceeded
    // 3. Record new request (if allowed)
}
```

This prevents a **TOCTOU (Time-of-Check to Time-of-Use) race condition** where two goroutines could both see "4 of 5 used" and both proceed to record their request, resulting in 6 requests passing through.

### 4. Standard Library Only (No Framework)

The service uses Go's `net/http` standard library instead of frameworks like Gin or Echo. This:
- Reduces dependencies and attack surface
- Makes the code easier to understand and audit
- Demonstrates proficiency with Go fundamentals
- The only external dependency is `github.com/google/uuid` for UUID generation

### 5. Separation of Concerns

```
internal/
├── model/       # Data structures (no logic)
├── store/       # Data storage + rate limiting (thread-safe)
├── handler/     # HTTP handlers (validation, response formatting)
└── middleware/   # Cross-cutting concerns (logging)
```

The store encapsulates both storage and rate limiting logic because they must be atomic. Handlers only deal with HTTP concerns (parsing, validation, serialization).

### 6. Production-Grade Error Handling

- All errors return structured JSON with error codes
- Input validation with specific error messages
- Standard `Retry-After` and `X-RateLimit-*` headers on rate-limited responses
- Graceful shutdown with drain timeout
- Request logging middleware

---

## Concurrency Correctness

### The Problem

When multiple goroutines handle concurrent requests for the same user, a naive implementation would have a **race condition**:

```
Goroutine A: reads count=4  (4 of 5 used)
Goroutine B: reads count=4  (4 of 5 used)
Goroutine A: writes count=5 (allows request)
Goroutine B: writes count=5 (allows request — BUG! Should be 6)
```

### The Solution

All state mutations in `MemoryStore.CheckAndRecord()` are performed under a single `sync.Mutex` lock. The entire check-prune-insert sequence executes atomically:

1. **Lock acquired** — no other goroutine can read or modify state
2. Prune expired timestamps from the sliding window
3. Check if adding a request would exceed the limit
4. If allowed, record the timestamp and request
5. **Lock released**

### Test Verification

The concurrency test (`TestConcurrency_ExactlyNAllowed`) fires **50 concurrent goroutines** making requests for the same user and verifies that **exactly 5 are accepted** and **45 are rejected** — proving the mutex prevents over-admission.

---

## What I Would Improve With More Time

### 1. Redis-Backed Store
Replace the in-memory store with Redis for:
- **Persistence** across restarts
- **Horizontal scaling** — multiple API instances sharing the same rate limit state
- Redis's `MULTI/EXEC` or Lua scripting for atomic rate limiting operations

### 2. Distributed Rate Limiting
The current in-memory approach only works for a single instance. With Redis or a distributed cache, the rate limiter would work across a cluster of API servers behind a load balancer.

### 3. Request Queue / Retry Logic
Instead of rejecting rate-limited requests outright, queue them and process when capacity is available:
- Priority queue per user
- Exponential backoff for retries
- Dead-letter queue for repeatedly failed requests

### 4. Configurable Rate Limits Per User
Instead of a global 5 req/min, support per-user tiers:
- Free tier: 5 req/min
- Pro tier: 100 req/min
- Enterprise: custom limits

### 5. Metrics & Observability
Add Prometheus metrics (`/metrics` endpoint):
- `rate_limit_requests_total` (counter by user, status)
- `rate_limit_current_window` (gauge by user)
- `request_duration_seconds` (histogram)

### 6. API Authentication
Add JWT or API key authentication so `user_id` is derived from the token rather than being a client-supplied field (which could be spoofed).

### 7. Rate Limit by IP
In addition to per-user rate limiting, add IP-based rate limiting to prevent abuse from unauthenticated clients.

### 8. Memory Management
The current store grows unboundedly as it keeps all historical request records. With more time:
- Periodic cleanup of old records beyond a retention window
- Configurable maximum history size per user
- LRU eviction for inactive users

---

## Project Structure

```
rate-limited-api/
├── main.go                          # Entry point, server setup, graceful shutdown
├── go.mod                           # Go module definition
├── go.sum                           # Dependency checksums
├── Dockerfile                       # Multi-stage Docker build
├── .gitignore
├── .dockerignore
├── README.md
└── internal/
    ├── model/
    │   └── model.go                 # Data structures and types
    ├── store/
    │   ├── memory.go                # Thread-safe in-memory store
    │   ├── memory_test.go           # Unit tests for store
    │   └── concurrency_test.go      # Concurrency correctness tests
    ├── handler/
    │   ├── request.go               # POST /request handler
    │   ├── stats.go                 # GET /stats handler
    │   ├── health.go                # GET /health handler
    │   └── handler_test.go          # Handler integration tests
    └── middleware/
        └── logger.go                # Request logging middleware
```

---

## Quick Smoke Test

```bash
# Start the server
go run main.go

# In another terminal:

# Submit requests
curl -X POST http://localhost:3000/request \
  -H "Content-Type: application/json" \
  -d '{"user_id": "alice", "payload": {"action": "test"}}'

# Check stats
curl http://localhost:3000/stats

# Health check
curl http://localhost:3000/health

# Test rate limiting (run 6 times rapidly)
for i in $(seq 1 6); do
  echo "Request $i:"
  curl -s -w "\nHTTP Status: %{http_code}\n" \
    -X POST http://localhost:3000/request \
    -H "Content-Type: application/json" \
    -d "{\"user_id\": \"bob\", \"payload\": {\"i\": $i}}"
  echo "---"
done
```

---

## License

ISC
