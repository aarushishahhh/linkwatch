package checker

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aarushishahhh/linkwatch/project/internal/models"
	"github.com/aarushishahhh/linkwatch/project/internal/storage"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestStore(t *testing.T) *storage.Storage {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	store := storage.New(db)
	if err := store.Migrate(); err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	return store
}

func TestPerformCheck(t *testing.T) {
	store := setupTestStore(t)
	config := Config{
		Interval:       time.Second,
		MaxConcurrency: 2,
		HTTPTimeout:    time.Second,
	}
	checker := New(store, config)

	t.Run("successful check", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(50 * time.Millisecond) // Simulate some latency
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		ctx := context.Background()
		result := checker.performCheck(ctx, server.URL)

		if result.StatusCode == nil || *result.StatusCode != 200 {
			t.Errorf("expected status code 200, got %v", result.StatusCode)
		}

		if result.LatencyMs <= 0 {
			t.Errorf("expected positive latency, got %d", result.LatencyMs)
		}

		if result.Error != nil {
			t.Errorf("expected no error, got %v", result.Error)
		}
	})

	t.Run("4xx error (no retry)", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		ctx := context.Background()
		result := checker.performCheck(ctx, server.URL)

		if attempts != 1 {
			t.Errorf("expected 1 attempt for 4xx, got %d", attempts)
		}

		if result.StatusCode == nil || *result.StatusCode != 404 {
			t.Errorf("expected status code 404, got %v", result.StatusCode)
		}

		if result.Error != nil {
			t.Errorf("expected no error for 4xx, got %v", result.Error)
		}
	})

	t.Run("5xx error with retry", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts < 3 {
				w.WriteHeader(http.StatusInternalServerError)
			} else {
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		ctx := context.Background()
		result := checker.performCheck(ctx, server.URL)

		if attempts != 3 {
			t.Errorf("expected 3 attempts for 5xx with retry, got %d", attempts)
		}

		if result.StatusCode == nil || *result.StatusCode != 200 {
			t.Errorf("expected final status code 200, got %v", result.StatusCode)
		}
	})

	t.Run("persistent 5xx error", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		ctx := context.Background()
		result := checker.performCheck(ctx, server.URL)

		if attempts != 3 {
			t.Errorf("expected 3 attempts for persistent 5xx, got %d", attempts)
		}

		if result.Error == nil {
			t.Error("expected error for persistent 5xx")
		}
	})

	t.Run("network error with retry", func(t *testing.T) {
		// Use invalid URL to simulate network error
		ctx := context.Background()
		result := checker.performCheck(ctx, "http://nonexistent.invalid")

		if result.Error == nil {
			t.Error("expected error for network failure")
		}

		if result.StatusCode != nil {
			t.Errorf("expected no status code for network failure, got %v", result.StatusCode)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(time.Second) // Long delay
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		result := checker.performCheck(ctx, server.URL)

		if result.Error == nil {
			t.Error("expected error for cancelled context")
		}
	})

	t.Run("redirect handling", func(t *testing.T) {
		redirectCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if redirectCount < 2 {
				redirectCount++
				http.Redirect(w, r, "/redirect", http.StatusFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		ctx := context.Background()
		result := checker.performCheck(ctx, server.URL)

		if result.StatusCode == nil || *result.StatusCode != 200 {
			t.Errorf("expected final status code 200 after redirects, got %v", result.StatusCode)
		}

		if result.Error != nil {
			t.Errorf("expected no error for successful redirect, got %v", result.Error)
		}
	})
}

func TestConcurrencyLimits(t *testing.T) {
	store := setupTestStore(t)

	// Create targets for same host
	targets := []models.Target{}
	for i := 0; i < 5; i++ {
		target, _, err := store.CreateTarget("https://example.com/path"+string(rune('0'+i)), "https://example.com/path"+string(rune('0'+i)), nil)
		if err != nil {
			t.Fatalf("failed to create target: %v", err)
		}
		targets = append(targets, *target)
	}

	config := Config{
		Interval:       time.Hour, // Long interval to prevent automatic runs
		MaxConcurrency: 10,        // High overall limit
		HTTPTimeout:    time.Second,
	}
	checker := New(store, config)

	t.Run("per-host serialization", func(t *testing.T) {
		var activeCounts sync.Map
		var maxConcurrent int
		var mu sync.Mutex

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host

			// Increment active count for this host
			actual, _ := activeCounts.LoadOrStore(host, 0)
			count := actual.(int) + 1
			activeCounts.Store(host, count)

			// Track maximum concurrent requests for this host
			mu.Lock()
			if count > maxConcurrent {
				maxConcurrent = count
			}
			mu.Unlock()

			// Simulate work
			time.Sleep(100 * time.Millisecond)

			// Decrement active count
			activeCounts.Store(host, count-1)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		// Update all targets to point to our test server
		for i := range targets {
			targets[i].URL = server.URL + "/path" + string(rune('0'+i))
		}

		ctx := context.Background()
		var wg sync.WaitGroup

		// Start checks for all targets concurrently
		for _, target := range targets {
			wg.Add(1)
			go func(t models.Target) {
				defer wg.Done()
				checker.checkTarget(ctx, t)
			}(target)
		}

		wg.Wait()

		// Verify that no more than 1 request was active for the host at any time
		if maxConcurrent > 1 {
			t.Errorf("expected max 1 concurrent request per host, got %d", maxConcurrent)
		}
	})

	t.Run("overall concurrency limit", func(t *testing.T) {
		config := Config{
			Interval:       time.Hour,
			MaxConcurrency: 2, // Low limit to test
			HTTPTimeout:    time.Second,
		}
		checker := New(store, config)

		var activeCounts int
		var maxConcurrent int
		var mu sync.Mutex

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			activeCounts++
			if activeCounts > maxConcurrent {
				maxConcurrent = activeCounts
			}
			mu.Unlock()

			time.Sleep(100 * time.Millisecond)

			mu.Lock()
			activeCounts--
			mu.Unlock()

			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		// Create targets for different hosts
		multiHostTargets := []models.Target{}
		for i := 0; i < 5; i++ {
			hostTarget, _, err := store.CreateTarget("https://host"+string(rune('0'+i))+".com", "https://host"+string(rune('0'+i))+".com", nil)
			if err != nil {
				t.Fatalf("failed to create target: %v", err)
			}
			hostTarget.URL = server.URL // Point to test server
			multiHostTargets = append(multiHostTargets, *hostTarget)
		}

		ctx := context.Background()

		// This should respect the MaxConcurrency limit of 2
		checker.checkAllTargets(ctx)

		if maxConcurrent > 2 {
			t.Errorf("expected max 2 concurrent requests overall, got %d", maxConcurrent)
		}
	})
}

func TestBackoffTiming(t *testing.T) {
	store := setupTestStore(t)
	config := Config{
		Interval:       time.Second,
		MaxConcurrency: 1,
		HTTPTimeout:    time.Second,
	}
	checker := New(store, config)

	var requestTimes []time.Time
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestTimes = append(requestTimes, time.Now())
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError) // Trigger retry
	}))
	defer server.Close()

	ctx := context.Background()

	checker.performCheck(ctx, server.URL)

	mu.Lock()
	times := make([]time.Time, len(requestTimes))
	copy(times, requestTimes)
	mu.Unlock()

	if len(times) != 3 {
		t.Errorf("expected 3 requests (initial + 2 retries), got %d", len(times))
		return
	}

	// Check that backoff timing is approximately correct
	// First retry should be ~200ms after initial
	firstBackoff := times[1].Sub(times[0])
	if firstBackoff < 150*time.Millisecond || firstBackoff > 300*time.Millisecond {
		t.Errorf("expected first backoff ~200ms, got %v", firstBackoff)
	}

	// Second retry should be ~400ms after first retry
	secondBackoff := times[2].Sub(times[1])
	if secondBackoff < 300*time.Millisecond || secondBackoff > 600*time.Millisecond {
		t.Errorf("expected second backoff ~400ms, got %v", secondBackoff)
	}
}
