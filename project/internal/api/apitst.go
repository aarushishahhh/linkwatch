package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestCreateTarget(t *testing.T) {
	store := setupTestStore(t)
	router := NewRouter(store)

	t.Run("create valid target", func(t *testing.T) {
		reqBody := `{"url": "https://example.com"}`
		req := httptest.NewRequest("POST", "/v1/targets", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Errorf("expected status %d, got %d", http.StatusCreated, rec.Code)
		}

		var response models.CreateTargetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if response.URL != "https://example.com" {
			t.Errorf("expected URL %q, got %q", "https://example.com", response.URL)
		}

		if response.ID == "" {
			t.Error("expected non-empty ID")
		}

		if response.CreatedAt.IsZero() {
			t.Error("expected non-zero created_at")
		}
	})

	t.Run("duplicate target returns existing", func(t *testing.T) {
		// Create first target
		reqBody1 := `{"url": "https://test.com"}`
		req1 := httptest.NewRequest("POST", "/v1/targets", bytes.NewBufferString(reqBody1))
		req1.Header.Set("Content-Type", "application/json")

		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)

		if rec1.Code != http.StatusCreated {
			t.Errorf("expected status %d for first request, got %d", http.StatusCreated, rec1.Code)
		}

		var response1 models.CreateTargetResponse
		json.Unmarshal(rec1.Body.Bytes(), &response1)

		// Create duplicate target (canonical equivalent)
		reqBody2 := `{"url": "https://TEST.COM/"}`
		req2 := httptest.NewRequest("POST", "/v1/targets", bytes.NewBufferString(reqBody2))
		req2.Header.Set("Content-Type", "application/json")

		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusOK {
			t.Errorf("expected status %d for duplicate request, got %d", http.StatusOK, rec2.Code)
		}

		var response2 models.CreateTargetResponse
		json.Unmarshal(rec2.Body.Bytes(), &response2)

		if response1.ID != response2.ID {
			t.Error("expected same ID for canonical equivalent URLs")
		}
	})

	t.Run("idempotency key", func(t *testing.T) {
		idempotencyKey := "test-key-123"

		// First request
		reqBody := `{"url": "https://idempotent.com"}`
		req1 := httptest.NewRequest("POST", "/v1/targets", bytes.NewBufferString(reqBody))
		req1.Header.Set("Content-Type", "application/json")
		req1.Header.Set("Idempotency-Key", idempotencyKey)

		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)

		if rec1.Code != http.StatusCreated {
			t.Errorf("expected status %d for first request, got %d", http.StatusCreated, rec1.Code)
		}

		var response1 models.CreateTargetResponse
		json.Unmarshal(rec1.Body.Bytes(), &response1)

		// Duplicate request with same idempotency key
		req2 := httptest.NewRequest("POST", "/v1/targets", bytes.NewBufferString(reqBody))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Idempotency-Key", idempotencyKey)

		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusOK {
			t.Errorf("expected status %d for duplicate request with idempotency key, got %d", http.StatusOK, rec2.Code)
		}

		var response2 models.CreateTargetResponse
		json.Unmarshal(rec2.Body.Bytes(), &response2)

		if response1.ID != response2.ID {
			t.Error("expected same ID for requests with same idempotency key")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/targets", bytes.NewBufferString("invalid json"))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status %d for invalid JSON, got %d", http.StatusBadRequest, rec.Code)
		}
	})

	t.Run("empty URL", func(t *testing.T) {
		reqBody := `{"url": ""}`
		req := httptest.NewRequest("POST", "/v1/targets", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status %d for empty URL, got %d", http.StatusBadRequest, rec.Code)
		}
	})

	t.Run("invalid URL scheme", func(t *testing.T) {
		reqBody := `{"url": "ftp://example.com"}`
		req := httptest.NewRequest("POST", "/v1/targets", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status %d for invalid scheme, got %d", http.StatusBadRequest, rec.Code)
		}
	})
}

func TestListTargets(t *testing.T) {
	store := setupTestStore(t)
	router := NewRouter(store)

	// Create test targets
	urls := []string{
		"https://example.com",
		"https://test.com",
		"https://example.org",
	}

	for _, url := range urls {
		canonical, _ := storage.CanonicalizeURL(url)
		store.CreateTarget(url, canonical, nil)
		time.Sleep(1 * time.Millisecond) // Ensure different timestamps
	}

	t.Run("list all targets", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/targets", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
		}

		var response models.TargetList
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if len(response.Items) != 3 {
			t.Errorf("expected 3 targets, got %d", len(response.Items))
		}
	})

	t.Run("filter by host", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/targets?host=example.com", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
		}

		var response models.TargetList
		json.Unmarshal(rec.Body.Bytes(), &response)

		if len(response.Items) != 1 {
			t.Errorf("expected 1 target for host filter, got %d", len(response.Items))
		}

		if response.Items[0].URL != "https://example.com" {
			t.Errorf("expected filtered result to be https://example.com, got %s", response.Items[0].URL)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/targets?limit=2", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
		}

		var response models.TargetList
		json.Unmarshal(rec.Body.Bytes(), &response)

		if len(response.Items) != 2 {
			t.Errorf("expected 2 targets with limit=2, got %d", len(response.Items))
		}

		if response.NextPageToken == "" {
			t.Error("expected non-empty next page token")
		}

		// Get next page
		req2 := httptest.NewRequest("GET", "/v1/targets?limit=2&page_token="+response.NextPageToken, nil)
		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)

		var response2 models.TargetList
		json.Unmarshal(rec2.Body.Bytes(), &response2)

		if len(response2.Items) != 1 {
			t.Errorf("expected 1 target on second page, got %d", len(response2.Items))
		}
	})
}

func TestGetCheckResults(t *testing.T) {
	store := setupTestStore(t)
	router := NewRouter(store)

	// Create target
	target, _, _ := store.CreateTarget("https://example.com", "https://example.com", nil)

	// Create check results
	now := time.Now().UTC()
	results := []models.CheckResult{
		{
			CheckedAt:  now,
			StatusCode: intPtr(200),
			LatencyMs:  150,
		},
		{
			CheckedAt:  now.Add(-time.Minute),
			StatusCode: intPtr(404),
			LatencyMs:  100,
		},
	}

	for _, result := range results {
		store.SaveCheckResult(target.ID, result)
	}

	t.Run("get all results", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/targets/"+target.ID+"/results", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
		}

		var response models.CheckResultList
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if len(response.Items) != 2 {
			t.Errorf("expected 2 results, got %d", len(response.Items))
		}

		// Should be ordered most recent first
		if response.Items[0].CheckedAt.Before(response.Items[1].CheckedAt) {
			t.Error("results not properly ordered")
		}
	})

	t.Run("filter by since", func(t *testing.T) {
		since := now.Add(-30 * time.Second)
		req := httptest.NewRequest("GET", "/v1/targets/"+target.ID+"/results?since="+since.Format(time.RFC3339), nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
		}

		var response models.CheckResultList
		json.Unmarshal(rec.Body.Bytes(), &response)

		if len(response.Items) != 1 {
			t.Errorf("expected 1 result since %v, got %d", since, len(response.Items))
		}
	})

	t.Run("invalid since parameter", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/targets/"+target.ID+"/results?since=invalid", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status %d for invalid since, got %d", http.StatusBadRequest, rec.Code)
		}
	})
}

func TestHealth(t *testing.T) {
	store := setupTestStore(t)
	router := NewRouter(store)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if rec.Body.String() != "OK" {
		t.Errorf("expected body 'OK', got %q", rec.Body.String())
	}
}

func intPtr(i int) *int {
	return &i
}
