package store

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrency_ExactlyNAllowed verifies that under heavy concurrent
// access, exactly MaxRequests are allowed and the rest are rejected.
// This is the critical concurrency correctness test.
func TestConcurrency_ExactlyNAllowed(t *testing.T) {
	s := New(Config{MaxRequests: 5, WindowSize: time.Minute})

	const totalRequests = 50
	var (
		accepted int64
		rejected int64
		wg       sync.WaitGroup
	)

	wg.Add(totalRequests)
	for i := 0; i < totalRequests; i++ {
		go func(idx int) {
			defer wg.Done()
			result, _ := s.CheckAndRecord(
				"concurrent_user",
				map[string]interface{}{"i": idx},
				fmt.Sprintf("req-%d", idx),
			)
			if result.Allowed {
				atomic.AddInt64(&accepted, 1)
			} else {
				atomic.AddInt64(&rejected, 1)
			}
		}(i)
	}

	wg.Wait()

	if accepted != 5 {
		t.Fatalf("expected exactly 5 accepted, got %d", accepted)
	}
	if rejected != 45 {
		t.Fatalf("expected exactly 45 rejected, got %d", rejected)
	}
}

// TestConcurrency_IndependentUsers verifies that concurrent requests
// from different users don't interfere with each other.
func TestConcurrency_IndependentUsers(t *testing.T) {
	s := New(Config{MaxRequests: 5, WindowSize: time.Minute})

	const usersCount = 10
	const requestsPerUser = 5
	var wg sync.WaitGroup

	results := make([][]bool, usersCount)
	for i := range results {
		results[i] = make([]bool, requestsPerUser)
	}

	wg.Add(usersCount * requestsPerUser)
	for u := 0; u < usersCount; u++ {
		for r := 0; r < requestsPerUser; r++ {
			go func(userIdx, reqIdx int) {
				defer wg.Done()
				userID := fmt.Sprintf("user_%d", userIdx)
				result, _ := s.CheckAndRecord(
					userID,
					map[string]interface{}{"r": reqIdx},
					fmt.Sprintf("req-%d-%d", userIdx, reqIdx),
				)
				results[userIdx][reqIdx] = result.Allowed
			}(u, r)
		}
	}

	wg.Wait()

	// All requests should be accepted (5 per user × 10 users = 50)
	for u := 0; u < usersCount; u++ {
		acceptedCount := 0
		for _, allowed := range results[u] {
			if allowed {
				acceptedCount++
			}
		}
		if acceptedCount != requestsPerUser {
			t.Fatalf("user_%d: expected %d accepted, got %d", u, requestsPerUser, acceptedCount)
		}
	}
}

// TestConcurrency_StatsConsistency verifies that stats queries
// during concurrent writes return consistent data.
func TestConcurrency_StatsConsistency(t *testing.T) {
	s := New(Config{MaxRequests: 5, WindowSize: time.Minute})

	var wg sync.WaitGroup
	const writers = 20
	const readers = 10

	// Start writers
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(idx int) {
			defer wg.Done()
			userID := fmt.Sprintf("user_%d", idx%4)
			s.CheckAndRecord(
				userID,
				map[string]interface{}{"i": idx},
				fmt.Sprintf("req-%d", idx),
			)
		}(i)
	}

	// Start readers concurrently
	wg.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			stats := s.GetStats()
			// Stats should always be self-consistent
			calculatedTotal := 0
			for _, u := range stats.Users {
				calculatedTotal += u.TotalRequests
				if u.TotalRequests < 0 {
					t.Errorf("negative total_requests for user %s", u.UserID)
				}
				if u.RemainingRequests < 0 {
					t.Errorf("negative remaining_requests for user %s", u.UserID)
				}
			}
			if calculatedTotal != stats.TotalRequests {
				t.Errorf("stats inconsistency: sum of user totals (%d) != total_requests (%d)",
					calculatedTotal, stats.TotalRequests)
			}
		}()
	}

	wg.Wait()
}

// TestConcurrency_RaceDetector should be run with `go test -race`
// to verify there are no data races.
func TestConcurrency_RaceDetector(t *testing.T) {
	s := New(Config{MaxRequests: 3, WindowSize: time.Minute})

	var wg sync.WaitGroup
	const goroutines = 100

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			userID := fmt.Sprintf("user_%d", idx%5)

			// Mix of writes and reads
			if idx%3 == 0 {
				s.GetStats()
			} else {
				s.CheckAndRecord(userID, map[string]interface{}{"i": idx}, fmt.Sprintf("r-%d", idx))
			}
		}(i)
	}

	wg.Wait()

	// Verify final state is consistent
	stats := s.GetStats()
	if stats.TotalUsers > 5 {
		t.Fatalf("expected at most 5 users, got %d", stats.TotalUsers)
	}
}
