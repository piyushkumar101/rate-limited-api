package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rate-limited-api/internal/model"
	"github.com/rate-limited-api/internal/store"
)

func newTestStore() store.Store {
	return store.New(store.Config{MaxRequests: 5, WindowSize: time.Minute})
}

func TestRequestHandler_ValidRequest(t *testing.T) {
	s := newTestStore()
	h := &RequestHandler{Store: s}

	body := `{"user_id": "user1", "payload": {"action": "test"}}`
	req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	var resp model.APIResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if !resp.Success {
		t.Fatal("expected success: true")
	}

	// Check rate limit headers
	if rr.Header().Get("X-RateLimit-Limit") != "5" {
		t.Fatalf("expected X-RateLimit-Limit=5, got %s", rr.Header().Get("X-RateLimit-Limit"))
	}
	if rr.Header().Get("X-RateLimit-Remaining") != "4" {
		t.Fatalf("expected X-RateLimit-Remaining=4, got %s", rr.Header().Get("X-RateLimit-Remaining"))
	}
}

func TestRequestHandler_MissingUserID(t *testing.T) {
	s := newTestStore()
	h := &RequestHandler{Store: s}

	body := `{"payload": {"action": "test"}}`
	req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	var resp model.APIError
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Error.Code != "INVALID_REQUEST" {
		t.Fatalf("expected INVALID_REQUEST, got %s", resp.Error.Code)
	}
}

func TestRequestHandler_EmptyUserID(t *testing.T) {
	s := newTestStore()
	h := &RequestHandler{Store: s}

	body := `{"user_id": "", "payload": {"action": "test"}}`
	req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestRequestHandler_MissingPayload(t *testing.T) {
	s := newTestStore()
	h := &RequestHandler{Store: s}

	body := `{"user_id": "user1"}`
	req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestRequestHandler_InvalidJSON(t *testing.T) {
	s := newTestStore()
	h := &RequestHandler{Store: s}

	body := `{invalid json}`
	req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestRequestHandler_RateLimitEnforced(t *testing.T) {
	s := newTestStore()
	h := &RequestHandler{Store: s}

	// Make 5 valid requests
	for i := 0; i < 5; i++ {
		body := `{"user_id": "user1", "payload": {"i": 1}}`
		req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("request %d: expected 201, got %d", i+1, rr.Code)
		}
	}

	// 6th should be rate limited
	body := `{"user_id": "user1", "payload": {"i": 5}}`
	req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}

	var resp model.APIError
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Error.Code != "RATE_LIMIT_EXCEEDED" {
		t.Fatalf("expected RATE_LIMIT_EXCEEDED, got %s", resp.Error.Code)
	}
	if resp.Error.RetryAfterMs <= 0 {
		t.Fatal("expected positive retry_after_ms")
	}

	// Check rate limit headers
	if rr.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("expected X-RateLimit-Remaining=0, got %s", rr.Header().Get("X-RateLimit-Remaining"))
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestRequestHandler_WrongMethod(t *testing.T) {
	s := newTestStore()
	h := &RequestHandler{Store: s}

	req := httptest.NewRequest(http.MethodGet, "/request", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestRequestHandler_IndependentUsers(t *testing.T) {
	s := newTestStore()
	h := &RequestHandler{Store: s}

	// Fill user1 quota
	for i := 0; i < 5; i++ {
		body := `{"user_id": "user1", "payload": {"i": 1}}`
		req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}

	// user2 should succeed
	body := `{"user_id": "user2", "payload": {"action": "test"}}`
	req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 for user2, got %d", rr.Code)
	}
}

func TestRequestHandler_WithQueueEnabled(t *testing.T) {
	s := newTestStore()

	// Import queue package
	q := newTestQueue(s)
	defer q.Stop()

	h := &RequestHandler{Store: s, Queue: q}

	// Fill rate limit
	for i := 0; i < 5; i++ {
		body := `{"user_id": "user1", "payload": {"i": 1}}`
		req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}

	// 6th should be queued (HTTP 202), not rejected
	body := `{"user_id": "user1", "payload": {"i": 5}}`
	req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202 (Accepted/Queued), got %d. Body: %s", rr.Code, rr.Body.String())
	}

	var resp model.APIResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if !resp.Success {
		t.Fatal("expected success: true for queued request")
	}
}

func TestStatsHandler_Empty(t *testing.T) {
	s := newTestStore()
	h := &StatsHandler{Store: s}

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp model.APIResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if !resp.Success {
		t.Fatal("expected success: true")
	}
}

func TestStatsHandler_AfterRequests(t *testing.T) {
	s := newTestStore()
	reqHandler := &RequestHandler{Store: s}
	statsHandler := &StatsHandler{Store: s}

	// Make some requests
	for _, uid := range []string{"alice", "alice", "bob"} {
		body := `{"user_id": "` + uid + `", "payload": {"msg": "hi"}}`
		req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		reqHandler.ServeHTTP(rr, req)
	}

	// Check stats
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rr := httptest.NewRecorder()
	statsHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	// Parse the nested response
	var raw map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&raw)

	data := raw["data"].(map[string]interface{})
	if int(data["total_users"].(float64)) != 2 {
		t.Fatalf("expected 2 users, got %v", data["total_users"])
	}
	if int(data["total_requests"].(float64)) != 3 {
		t.Fatalf("expected 3 requests, got %v", data["total_requests"])
	}
}

func TestStatsHandler_WrongMethod(t *testing.T) {
	s := newTestStore()
	h := &StatsHandler{Store: s}

	req := httptest.NewRequest(http.MethodPost, "/stats", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestHealthHandler(t *testing.T) {
	h := &HealthHandler{StartTime: time.Now()}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp model.HealthResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Status != "healthy" {
		t.Fatalf("expected 'healthy', got '%s'", resp.Status)
	}
}
