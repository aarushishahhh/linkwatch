package storage

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aarushishahhh/linkwatch/project/internal/models"
)

type Storage struct {
	db *sql.DB
}

func New(db *sql.DB) *Storage {
	return &Storage{db: db}
}

func (s *Storage) Migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS targets (
		id TEXT PRIMARY KEY,
		url TEXT NOT NULL UNIQUE,
		canonical_url TEXT NOT NULL UNIQUE,
		created_at TIMESTAMP NOT NULL
	);

	CREATE TABLE IF NOT EXISTS check_results (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		target_id TEXT NOT NULL REFERENCES targets(id),
		checked_at TIMESTAMP NOT NULL,
		status_code INTEGER,
		latency_ms INTEGER NOT NULL,
		error TEXT,
		FOREIGN KEY (target_id) REFERENCES targets(id)
	);

	CREATE TABLE IF NOT EXISTS idempotency_keys (
		key TEXT PRIMARY KEY,
		target_id TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL,
		FOREIGN KEY (target_id) REFERENCES targets(id)
	);

	CREATE INDEX IF NOT EXISTS idx_check_results_target_checked 
		ON check_results(target_id, checked_at DESC);
	CREATE INDEX IF NOT EXISTS idx_targets_created_id 
		ON targets(created_at, id);
	CREATE INDEX IF NOT EXISTS idx_idempotency_created 
		ON idempotency_keys(created_at);
	`

	_, err := s.db.Exec(schema)
	return err
}

func (s *Storage) CreateTarget(originalURL, canonicalURL string, idempotencyKey *string) (*models.Target, bool, error) {
	targetID := generateID("t_")
	now := time.Now().UTC()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	// Check for existing target by canonical URL
	var existing models.Target
	err = tx.QueryRow("SELECT id, url, created_at FROM targets WHERE canonical_url = ?", canonicalURL).
		Scan(&existing.ID, &existing.URL, &existing.CreatedAt)

	if err == nil {
		// Target exists, handle idempotency key if provided
		if idempotencyKey != nil {
			_, err = tx.Exec("INSERT OR IGNORE INTO idempotency_keys (key, target_id, created_at) VALUES (?, ?, ?)",
				*idempotencyKey, existing.ID, now)
			if err != nil {
				return nil, false, err
			}
		}
		tx.Commit()
		return &existing, false, nil
	}

	if err != sql.ErrNoRows {
		return nil, false, err
	}

	// Check idempotency key if provided
	if idempotencyKey != nil {
		var existingTargetID string
		err = tx.QueryRow("SELECT target_id FROM idempotency_keys WHERE key = ?", *idempotencyKey).
			Scan(&existingTargetID)

		if err == nil {
			// Key exists, return existing target
			err = tx.QueryRow("SELECT id, url, created_at FROM targets WHERE id = ?", existingTargetID).
				Scan(&existing.ID, &existing.URL, &existing.CreatedAt)
			if err != nil {
				return nil, false, err
			}
			tx.Commit()
			return &existing, false, nil
		}

		if err != sql.ErrNoRows {
			return nil, false, err
		}
	}

	// Create new target
	_, err = tx.Exec("INSERT INTO targets (id, url, canonical_url, created_at) VALUES (?, ?, ?, ?)",
		targetID, originalURL, canonicalURL, now)
	if err != nil {
		return nil, false, err
	}

	// Store idempotency key if provided
	if idempotencyKey != nil {
		_, err = tx.Exec("INSERT INTO idempotency_keys (key, target_id, created_at) VALUES (?, ?, ?)",
			*idempotencyKey, targetID, now)
		if err != nil {
			return nil, false, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, false, err
	}

	return &models.Target{
		ID:        targetID,
		URL:       originalURL,
		CreatedAt: now,
	}, true, nil
}

func (s *Storage) ListTargets(host *string, limit int, pageToken string) (*models.TargetList, error) {
	var query string
	var args []interface{}

	baseQuery := "SELECT id, url, created_at FROM targets"

	if host != nil {
		baseQuery += " WHERE canonical_url LIKE ?"
		args = append(args, "%://"+strings.ToLower(*host)+"/%")
	}

	if pageToken != "" {
		// Decode cursor (simplified - in production, use proper encoding)
		parts := strings.Split(pageToken, "_")
		if len(parts) == 2 {
			timestamp := parts[0]
			id := parts[1]

			if host != nil {
				baseQuery += " AND (created_at > ? OR (created_at = ? AND id > ?))"
				args = append(args, timestamp, timestamp, id)
			} else {
				baseQuery += " WHERE (created_at > ? OR (created_at = ? AND id > ?))"
				args = append(args, timestamp, timestamp, id)
			}
		}
	}

	query = baseQuery + " ORDER BY created_at, id LIMIT ?"
	args = append(args, limit+1) // Fetch one extra to determine if there's a next page

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []models.Target
	for rows.Next() {
		var target models.Target
		if err := rows.Scan(&target.ID, &target.URL, &target.CreatedAt); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}

	result := &models.TargetList{Items: targets}

	// Set next page token if there are more results
	if len(targets) > limit {
		result.Items = targets[:limit]
		last := targets[limit-1]
		result.NextPageToken = fmt.Sprintf("%s_%s",
			last.CreatedAt.Format(time.RFC3339Nano), last.ID)
	}

	return result, nil
}

func (s *Storage) GetAllTargets() ([]models.Target, error) {
	rows, err := s.db.Query("SELECT id, url, created_at FROM targets ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []models.Target
	for rows.Next() {
		var target models.Target
		if err := rows.Scan(&target.ID, &target.URL, &target.CreatedAt); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}

	return targets, nil
}

func (s *Storage) GetCheckResults(targetID string, since *time.Time, limit int) (*models.CheckResultList, error) {
	query := "SELECT checked_at, status_code, latency_ms, error FROM check_results WHERE target_id = ?"
	args := []interface{}{targetID}

	if since != nil {
		query += " AND checked_at >= ?"
		args = append(args, *since)
	}

	query += " ORDER BY checked_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []models.CheckResult
	for rows.Next() {
		var result models.CheckResult
		var errorStr sql.NullString

		if err := rows.Scan(&result.CheckedAt, &result.StatusCode, &result.LatencyMs, &errorStr); err != nil {
			return nil, err
		}

		if errorStr.Valid {
			result.Error = &errorStr.String
		}

		results = append(results, result)
	}

	return &models.CheckResultList{Items: results}, nil
}

func (s *Storage) SaveCheckResult(targetID string, result models.CheckResult) error {
	_, err := s.db.Exec(
		"INSERT INTO check_results (target_id, checked_at, status_code, latency_ms, error) VALUES (?, ?, ?, ?, ?)",
		targetID, result.CheckedAt, result.StatusCode, result.LatencyMs, result.Error,
	)
	return err
}

func (s *Storage) CleanupOldIdempotencyKeys(olderThan time.Time) error {
	_, err := s.db.Exec("DELETE FROM idempotency_keys WHERE created_at < ?", olderThan)
	return err
}

func generateID(prefix string) string {
	// Simple ID generation - in production, use UUIDs or similar
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

// CanonicalizeURL converts a URL to its canonical form
func CanonicalizeURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	// Ensure scheme is present
	if parsed.Scheme == "" {
		return "", fmt.Errorf("missing scheme")
	}

	// Lowercase scheme and host
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)

	// Remove default ports
	switch parsed.Scheme {
	case "http":
		if strings.HasSuffix(parsed.Host, ":80") {
			parsed.Host = strings.TrimSuffix(parsed.Host, ":80")
		}
	case "https":
		if strings.HasSuffix(parsed.Host, ":443") {
			parsed.Host = strings.TrimSuffix(parsed.Host, ":443")
		}
	}

	// Remove fragment
	parsed.Fragment = ""

	// Normalize path - remove trailing slash unless it's root
	if parsed.Path != "/" && strings.HasSuffix(parsed.Path, "/") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	}

	return parsed.String(), nil
}
