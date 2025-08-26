package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	_ "strings"
	"time"

	"github.com/aarushishahhh/linkwatch/project/internal/models"
	"github.com/aarushishahhh/linkwatch/project/internal/storage"
)

type Handler struct {
	store *storage.Storage
}

func NewRouter(store *storage.Storage) http.Handler {
	h := &Handler{store: store}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/targets", h.CreateTarget)
	mux.HandleFunc("GET /v1/targets", h.ListTargets)
	mux.HandleFunc("GET /v1/targets/{target_id}/results", h.GetCheckResults)
	mux.HandleFunc("GET /healthz", h.Health)

	return withLogging(withCORS(mux))
}

func (h *Handler) CreateTarget(w http.ResponseWriter, r *http.Request) {
	var req models.CreateTargetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	// Validate and canonicalize URL
	canonicalURL, err := storage.CanonicalizeURL(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid URL: %v", err))
		return
	}

	// Parse URL to validate it's HTTP/HTTPS
	parsed, err := url.Parse(canonicalURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid URL")
		return
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		writeError(w, http.StatusBadRequest, "URL must use HTTP or HTTPS scheme")
		return
	}

	// Handle idempotency key
	var idempotencyKey *string
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		idempotencyKey = &key
	}

	target, isNew, err := h.store.CreateTarget(req.URL, canonicalURL, idempotencyKey)
	if err != nil {
		slog.Error("failed to create target", "error", err, "url", req.URL)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	statusCode := http.StatusOK
	if isNew {
		statusCode = http.StatusCreated
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(models.CreateTargetResponse{
		ID:        target.ID,
		URL:       target.URL,
		CreatedAt: target.CreatedAt,
	})
}

func (h *Handler) ListTargets(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	host := r.URL.Query().Get("host")
	var hostPtr *string
	if host != "" {
		hostPtr = &host
	}

	limit := 10 // default
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	pageToken := r.URL.Query().Get("page_token")

	targets, err := h.store.ListTargets(hostPtr, limit, pageToken)
	if err != nil {
		slog.Error("failed to list targets", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(targets)
}

func (h *Handler) GetCheckResults(w http.ResponseWriter, r *http.Request) {
	targetID := r.PathValue("target_id")
	if targetID == "" {
		writeError(w, http.StatusBadRequest, "target_id is required")
		return
	}

	// Parse query parameters
	var since *time.Time
	if s := r.URL.Query().Get("since"); s != "" {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			since = &parsed
		} else {
			writeError(w, http.StatusBadRequest, "invalid since parameter, expected RFC3339 format")
			return
		}
	}

	limit := 50 // default
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}

	results, err := h.store.GetCheckResults(targetID, since, limit)
	if err != nil {
		slog.Error("failed to get check results", "error", err, "target_id", targetID)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Generate request ID
		requestID := fmt.Sprintf("req_%d", time.Now().UnixNano())
		r = r.WithContext(r.Context())

		// Wrap response writer to capture status code
		ww := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(ww, r)

		duration := time.Since(start)

		slog.Info("request completed",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.statusCode,
			"duration_ms", duration.Milliseconds(),
		)
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
