package checker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/aarushishahhh/linkwatch/project/internal/models"
	"github.com/aarushishahhh/linkwatch/project/internal/storage"
)

type Config struct {
	Interval       time.Duration
	MaxConcurrency int
	HTTPTimeout    time.Duration
}

type Checker struct {
	store    *storage.Storage
	config   Config
	client   *http.Client
	hostSems map[string]chan struct{} // Per-host semaphores
	hostMux  sync.RWMutex             // Protects hostSems map
}

func New(store *storage.Storage, config Config) *Checker {
	return &Checker{
		store:    store,
		config:   config,
		hostSems: make(map[string]chan struct{}),
		client: &http.Client{
			Timeout: config.HTTPTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     30 * time.Second,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("stopped after 5 redirects")
				}
				return nil
			},
		},
	}
}

func (c *Checker) Start(ctx context.Context) {
	go c.run(ctx)
}

func (c *Checker) run(ctx context.Context) {
	ticker := time.NewTicker(c.config.Interval)
	defer ticker.Stop()

	// Run initial check immediately
	c.checkAllTargets(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkAllTargets(ctx)
		}
	}
}

func (c *Checker) checkAllTargets(ctx context.Context) {
	targets, err := c.store.GetAllTargets()
	if err != nil {
		slog.Error("failed to get targets for checking", "error", err)
		return
	}

	if len(targets) == 0 {
		return
	}

	slog.Info("starting check cycle", "target_count", len(targets))

	// Use a semaphore to limit overall concurrency
	sem := make(chan struct{}, c.config.MaxConcurrency)
	var wg sync.WaitGroup

	for _, target := range targets {
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
			wg.Add(1)
			go func(t models.Target) {
				defer wg.Done()
				defer func() { <-sem }()
				c.checkTarget(ctx, t)
			}(target)
		}
	}

	wg.Wait()
	slog.Info("check cycle completed")
}

func (c *Checker) checkTarget(ctx context.Context, target models.Target) {
	parsed, err := url.Parse(target.URL)
	if err != nil {
		slog.Error("failed to parse target URL", "target_id", target.ID, "url", target.URL, "error", err)
		return
	}

	host := parsed.Host

	// Get or create per-host semaphore
	hostSem := c.getHostSemaphore(host)

	// Acquire per-host lock
	select {
	case <-ctx.Done():
		return
	case hostSem <- struct{}{}:
		defer func() { <-hostSem }()
	}

	start := time.Now()
	result := c.performCheck(ctx, target.URL)
	result.CheckedAt = start
	result.LatencyMs = int(time.Since(start).Milliseconds())

	if err := c.store.SaveCheckResult(target.ID, result); err != nil {
		slog.Error("failed to save check result", "target_id", target.ID, "error", err)
		return
	}

	slog.Debug("check completed", "target_id", target.ID, "url", target.URL,
		"status", result.StatusCode, "latency_ms", result.LatencyMs, "error", result.Error)
}

func (c *Checker) getHostSemaphore(host string) chan struct{} {
	c.hostMux.RLock()
	if sem, exists := c.hostSems[host]; exists {
		c.hostMux.RUnlock()
		return sem
	}
	c.hostMux.RUnlock()

	c.hostMux.Lock()
	defer c.hostMux.Unlock()

	// Double-check after acquiring write lock
	if sem, exists := c.hostSems[host]; exists {
		return sem
	}

	// Create new semaphore with capacity 1 (one check per host at a time)
	sem := make(chan struct{}, 1)
	c.hostSems[host] = sem
	return sem
}

func (c *Checker) performCheck(ctx context.Context, targetURL string) models.CheckResult {
	var result models.CheckResult
	var lastErr error

	// Retry logic: initial attempt + up to 2 retries on 5xx or network errors
	maxAttempts := 3
	backoff := 200 * time.Millisecond

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Apply exponential backoff
			select {
			case <-ctx.Done():
				errorMsg := "context cancelled"
				result.Error = &errorMsg
				return result
			case <-time.After(backoff):
				backoff *= 2
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
		if err != nil {
			lastErr = err
			continue
		}

		req.Header.Set("User-Agent", "Linkwatch/1.0")

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			// Retry on network errors
			if isNetworkError(err) {
				continue
			}
			break
		}

		result.StatusCode = &resp.StatusCode
		resp.Body.Close()

		// Success or 4xx - don't retry
		if resp.StatusCode < 500 {
			return result
		}

		// 5xx - retry if we have attempts left
		lastErr = fmt.Errorf("server error: %d", resp.StatusCode)
		if attempt == maxAttempts-1 {
			break
		}
	}

	// All attempts failed
	if lastErr != nil {
		errorMsg := lastErr.Error()
		result.Error = &errorMsg
	}

	return result
}

func isNetworkError(err error) bool {
	if _, ok := err.(*net.OpError); ok {
		return true
	}
	if _, ok := err.(*net.DNSError); ok {
		return true
	}
	return false
}
