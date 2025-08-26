package models

import "time"

type Target struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

type TargetList struct {
	Items         []Target `json:"items"`
	NextPageToken string   `json:"next_page_token,omitempty"`
}

type CheckResult struct {
	CheckedAt  time.Time `json:"checked_at"`
	StatusCode *int      `json:"status_code"`
	LatencyMs  int       `json:"latency_ms"`
	Error      *string   `json:"error"`
}

type CheckResultList struct {
	Items []CheckResult `json:"items"`
}

type CreateTargetRequest struct {
	URL string `json:"url"`
}

type CreateTargetResponse struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}
