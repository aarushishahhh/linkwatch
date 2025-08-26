package storage

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/aarushishahhh/linkwatch/project/internal/models"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) *Storage {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	store := New(db)
	if err := store.Migrate(); err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	return store
}

func TestCanonicalizeURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		hasError bool
	}{
		{"https://Example.Com/path/", "https://example.com/path", false},
		{"HTTP://EXAMPLE.COM:80/", "http://example.com", false},
		{"https://example.com:443/path", "https://example.com/path", false},
		{"https://example.com/path?query=value#fragment", "https://example.com/path?query=value", false},
		{"https://example.com/", "https://example.com", false},
		{"https://example.com", "https://example.com", false},
		{"example.com", "", true}, // missing scheme
		{"ftp://example.com", "ftp://example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := CanonicalizeURL(tt.input)
			if tt.hasError {
				if err == nil {
					t.Errorf("expected error for input %q", tt.input)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error for input %q: %v", tt.input, err)
				return
			}

			if result != tt.expected {
				t.Errorf("for input %q, expected %q, got %q", tt.input, tt.expected, result)
			}
		})
	}
}

func TestCreateTarget(t *testing.T) {
	store := setupTestDB(t)

	t.Run("create new target", func(t *testing.T) {
		target, isNew, err := store.CreateTarget("https://example.com", "https://example.com", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !isNew {
			t.Error("expected isNew to be true")
		}

		if target.URL != "https://example.com" {
			t.Errorf("expected URL %q, got %q", "https://example.com", target.URL)
		}

		if target.ID == "" {
			t.Error("expected non-empty target ID")
		}
	})

	t.Run("duplicate canonical URL returns existing", func(t *testing.T) {
		// First create
		target1, isNew1, err := store.CreateTarget("https://example.com/", "https://example.com", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !isNew1 {
			t.Error("expected first create to be new")
		}

		// Second create with same canonical URL
		target2, isNew2, err := store.CreateTarget("https://EXAMPLE.COM", "https://example.com", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if isNew2 {
			t.Error("expected second create to not be new")
		}

		if target1.ID != target2.ID {
			t.Error("expected same target ID for duplicate canonical URLs")
		}
	})
}

func TestCreateTargetIdempotency(t *testing.T) {
	store := setupTestDB(t)

	idempotencyKey := "test-key-123"

	t.Run("first request with idempotency key", func(t *testing.T) {
		target, isNew, err := store.CreateTarget("https://example.com", "https://example.com", &idempotencyKey)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !isNew {
			t.Error("expected first request to be new")
		}

		if !strings.HasPrefix(target.ID, "t_") {
			t.Errorf("expected target ID to start with 't_', got %q", target.ID)
		}
	})

	t.Run("duplicate request with same idempotency key", func(t *testing.T) {
		target, isNew, err := store.CreateTarget("https://example.com", "https://example.com", &idempotencyKey)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if isNew {
			t.Error("expected duplicate request to not be new")
		}

		if target.URL != "https://example.com" {
			t.Errorf("expected URL to match original")
		}
	})

	t.Run("different URL with same idempotency key returns original", func(t *testing.T) {
		target, isNew, err := store.CreateTarget("https://different.com", "https://different.com", &idempotencyKey)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if isNew {
			t.Error("expected request with existing idempotency key to not be new")
		}

		if target.URL != "https://example.com" {
			t.Errorf("expected original URL, got %q", target.URL)
		}
	})
}

func TestListTargets(t *testing.T) {
	store := setupTestDB(t)

	// Create test targets
	urls := []string{
		"https://example.com",
		"https://test.com",
		"https://example.org",
	}

	var createdTargets []models.Target
	for _, url := range urls {
		canonical, _ := CanonicalizeURL(url)
		target, _, err := store.CreateTarget(url, canonical, nil)
		if err != nil {
			t.Fatalf("failed to create target: %v", err)
		}
		createdTargets = append(createdTargets, *target)
		time.Sleep(1 * time.Millisecond) // Ensure different created_at times
	}

	t.Run("list all targets", func(t *testing.T) {
		result, err := store.ListTargets(nil, 10, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Items) != 3 {
			t.Errorf("expected 3 targets, got %d", len(result.Items))
		}

		// Should be ordered by created_at, id
		for i := 0; i < len(result.Items)-1; i++ {
			if result.Items[i].CreatedAt.After(result.Items[i+1].CreatedAt) {
				t.Error("targets not properly ordered by created_at")
			}
		}
	})

	t.Run("filter by host", func(t *testing.T) {
		host := "example.com"
		result, err := store.ListTargets(&host, 10, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Items) != 1 {
			t.Errorf("expected 1 target for host %q, got %d", host, len(result.Items))
		}

		if result.Items[0].URL != "https://example.com" {
			t.Errorf("unexpected target URL: %q", result.Items[0].URL)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		// First page with limit 2
		result1, err := store.ListTargets(nil, 2, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result1.Items) != 2 {
			t.Errorf("expected 2 items in first page, got %d", len(result1.Items))
		}

		if result1.NextPageToken == "" {
			t.Error("expected non-empty next page token")
		}

		// Second page
		result2, err := store.ListTargets(nil, 2, result1.NextPageToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result2.Items) != 1 {
			t.Errorf("expected 1 item in second page, got %d", len(result2.Items))
		}

		if result2.NextPageToken != "" {
			t.Error("expected empty next page token for last page")
		}

		// Verify no duplicates between pages
		for _, item1 := range result1.Items {
			for _, item2 := range result2.Items {
				if item1.ID == item2.ID {
					t.Error("found duplicate target across pages")
				}
			}
		}
	})
}

func TestSaveAndGetCheckResults(t *testing.T) {
	store := setupTestDB(t)

	// Create a target first
	target, _, err := store.CreateTarget("https://example.com", "https://example.com", nil)
	if err != nil {
		t.Fatalf("failed to create target: %v", err)
	}

	now := time.Now().UTC()
	results := []models.CheckResult{
		{
			CheckedAt:  now,
			StatusCode: intPtr(200),
			LatencyMs:  150,
			Error:      nil,
		},
		{
			CheckedAt:  now.Add(-time.Minute),
			StatusCode: nil,
			LatencyMs:  0,
			Error:      stringPtr("connection timeout"),
		},
		{
			CheckedAt:  now.Add(-2 * time.Minute),
			StatusCode: intPtr(404),
			LatencyMs:  75,
			Error:      nil,
		},
	}

	// Save results
	for _, result := range results {
		if err := store.SaveCheckResult(target.ID, result); err != nil {
			t.Fatalf("failed to save check result: %v", err)
		}
	}

	t.Run("get all results", func(t *testing.T) {
		retrieved, err := store.GetCheckResults(target.ID, nil, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(retrieved.Items) != 3 {
			t.Errorf("expected 3 results, got %d", len(retrieved.Items))
		}

		// Should be ordered most recent first
		for i := 0; i < len(retrieved.Items)-1; i++ {
			if retrieved.Items[i].CheckedAt.Before(retrieved.Items[i+1].CheckedAt) {
				t.Error("results not properly ordered by checked_at DESC")
			}
		}
	})

	t.Run("get results since timestamp", func(t *testing.T) {
		since := now.Add(-90 * time.Second)
		retrieved, err := store.GetCheckResults(target.ID, &since, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(retrieved.Items) != 2 {
			t.Errorf("expected 2 results since %v, got %d", since, len(retrieved.Items))
		}
	})

	t.Run("limit results", func(t *testing.T) {
		retrieved, err := store.GetCheckResults(target.ID, nil, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(retrieved.Items) != 1 {
			t.Errorf("expected 1 result with limit, got %d", len(retrieved.Items))
		}

		// Should be the most recent
		if retrieved.Items[0].StatusCode == nil || *retrieved.Items[0].StatusCode != 200 {
			t.Error("expected most recent result (200 status)")
		}
	})
}

func intPtr(i int) *int {
	return &i
}

func stringPtr(s string) *string {
	return &s
}
