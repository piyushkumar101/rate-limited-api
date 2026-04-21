package store

import (
	"testing"
	"time"
)

func TestCheckAndRecord_AllowsFirstRequest(t *testing.T) {
	s := New(Config{MaxRequests: 5, WindowSize: time.Minute})

	result, record := s.CheckAndRecord("user1", map[string]interface{}{"data": "test"}, "req-1")

	if !result.Allowed {
		t.Fatal("expected first request to be allowed")
	}
	if result.Remaining != 4 {
		t.Fatalf("expected 4 remaining, got %d", result.Remaining)
	}
	if record == nil {
		t.Fatal("expected record to be non-nil")
	}
	if record.UserID != "user1" {
		t.Fatalf("expected user_id 'user1', got '%s'", record.UserID)
	}
}

func TestCheckAndRecord_AllowsUpToLimit(t *testing.T) {
	s := New(Config{MaxRequests: 5, WindowSize: time.Minute})

	for i := 0; i < 5; i++ {
		result, record := s.CheckAndRecord("user1", map[string]interface{}{"i": i}, "req")
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
		if result.Remaining != 4-i {
			t.Fatalf("request %d: expected %d remaining, got %d", i+1, 4-i, result.Remaining)
		}
		if record == nil {
			t.Fatalf("request %d: expected record to be non-nil", i+1)
		}
	}
}

func TestCheckAndRecord_RejectsOverLimit(t *testing.T) {
	s := New(Config{MaxRequests: 5, WindowSize: time.Minute})

	// Fill quota
	for i := 0; i < 5; i++ {
		s.CheckAndRecord("user1", map[string]interface{}{"i": i}, "req")
	}

	// 6th should be rejected
	result, record := s.CheckAndRecord("user1", map[string]interface{}{"i": 5}, "req-6")

	if result.Allowed {
		t.Fatal("expected 6th request to be rejected")
	}
	if result.Remaining != 0 {
		t.Fatalf("expected 0 remaining, got %d", result.Remaining)
	}
	if result.RetryAfterMs <= 0 {
		t.Fatal("expected positive retry_after_ms")
	}
	if record != nil {
		t.Fatal("expected record to be nil for rejected request")
	}
}

func TestCheckAndRecord_IndependentUsers(t *testing.T) {
	s := New(Config{MaxRequests: 5, WindowSize: time.Minute})

	// Fill user1 quota
	for i := 0; i < 5; i++ {
		s.CheckAndRecord("user1", map[string]interface{}{"i": i}, "req")
	}

	// user2 should still be allowed
	result, record := s.CheckAndRecord("user2", map[string]interface{}{"data": "hello"}, "req-u2")

	if !result.Allowed {
		t.Fatal("user2 should be allowed")
	}
	if result.Remaining != 4 {
		t.Fatalf("expected 4 remaining for user2, got %d", result.Remaining)
	}
	if record == nil {
		t.Fatal("expected record")
	}
}

func TestCheckAndRecord_AllowsAfterWindowExpires(t *testing.T) {
	s := New(Config{MaxRequests: 2, WindowSize: 100 * time.Millisecond})

	// Fill quota
	s.CheckAndRecord("user1", map[string]interface{}{"i": 0}, "req-1")
	s.CheckAndRecord("user1", map[string]interface{}{"i": 1}, "req-2")

	// Should be rejected
	result, _ := s.CheckAndRecord("user1", map[string]interface{}{"i": 2}, "req-3")
	if result.Allowed {
		t.Fatal("should be rejected while window is active")
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Should be allowed again
	result, record := s.CheckAndRecord("user1", map[string]interface{}{"i": 3}, "req-4")
	if !result.Allowed {
		t.Fatal("should be allowed after window expires")
	}
	if record == nil {
		t.Fatal("expected record")
	}
}

func TestGetStats_Empty(t *testing.T) {
	s := New(DefaultConfig())

	stats := s.GetStats()

	if stats.TotalUsers != 0 {
		t.Fatalf("expected 0 users, got %d", stats.TotalUsers)
	}
	if stats.TotalRequests != 0 {
		t.Fatalf("expected 0 requests, got %d", stats.TotalRequests)
	}
	if len(stats.Users) != 0 {
		t.Fatalf("expected empty users slice, got %d", len(stats.Users))
	}
}

func TestGetStats_AccuratePerUserStats(t *testing.T) {
	s := New(DefaultConfig())

	s.CheckAndRecord("alice", map[string]interface{}{"msg": "hello"}, "r1")
	s.CheckAndRecord("alice", map[string]interface{}{"msg": "world"}, "r2")
	s.CheckAndRecord("bob", map[string]interface{}{"msg": "hi"}, "r3")

	stats := s.GetStats()

	if stats.TotalUsers != 2 {
		t.Fatalf("expected 2 users, got %d", stats.TotalUsers)
	}
	if stats.TotalRequests != 3 {
		t.Fatalf("expected 3 requests, got %d", stats.TotalRequests)
	}

	// Find alice
	var alice, bob *struct {
		total, inWindow, remaining int
	}
	for _, u := range stats.Users {
		if u.UserID == "alice" {
			alice = &struct{ total, inWindow, remaining int }{u.TotalRequests, u.RequestsInWindow, u.RemainingRequests}
		}
		if u.UserID == "bob" {
			bob = &struct{ total, inWindow, remaining int }{u.TotalRequests, u.RequestsInWindow, u.RemainingRequests}
		}
	}

	if alice == nil || bob == nil {
		t.Fatal("expected both alice and bob in stats")
	}
	if alice.total != 2 {
		t.Fatalf("alice: expected 2 total, got %d", alice.total)
	}
	if alice.remaining != 3 {
		t.Fatalf("alice: expected 3 remaining, got %d", alice.remaining)
	}
	if bob.total != 1 {
		t.Fatalf("bob: expected 1 total, got %d", bob.total)
	}
	if bob.remaining != 4 {
		t.Fatalf("bob: expected 4 remaining, got %d", bob.remaining)
	}
}

func TestReset(t *testing.T) {
	s := New(DefaultConfig())

	s.CheckAndRecord("user1", map[string]interface{}{"a": 1}, "r1")
	s.CheckAndRecord("user2", map[string]interface{}{"b": 1}, "r2")

	s.Reset()

	stats := s.GetStats()
	if stats.TotalUsers != 0 || stats.TotalRequests != 0 {
		t.Fatal("expected empty store after reset")
	}

	// Should accept requests again
	result, _ := s.CheckAndRecord("user1", map[string]interface{}{"a": 1}, "r3")
	if !result.Allowed {
		t.Fatal("should allow requests after reset")
	}
	if result.Remaining != 4 {
		t.Fatalf("expected 4 remaining after reset, got %d", result.Remaining)
	}
}
