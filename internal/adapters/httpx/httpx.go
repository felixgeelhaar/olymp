// Package httpx is the shared HTTP transport for cognitive-layer adapters.
//
// Every adapter (mnemos, chronos, nous, praxis) uses the same client so retry
// policy, error classification, and timeouts stay identical across layers.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/felixgeelhaar/fortify/retry"
)

// Config holds the per-adapter HTTP settings.
type Config struct {
	BaseURL    string
	HTTPClient *http.Client
	Timeout    time.Duration
	// Retry policy. Defaults match the rest of the cognitive stack: 5xx + 429
	// retried, 4xx fail-fast, exponential backoff with jitter.
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

// Client is the shared transport. Construct with New, then call Do.
type Client struct {
	base    string
	hc      *http.Client
	cfg     Config
	retrier retry.Retry[json.RawMessage]
}

// New returns a Client. The base URL must include scheme + host; trailing
// slashes are stripped.
func New(cfg Config) *Client {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultDuration(cfg.Timeout, 10*time.Second)}
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = 100 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 2 * time.Second
	}
	r := retry.New[json.RawMessage](retry.Config{
		MaxAttempts:   cfg.MaxAttempts,
		InitialDelay:  cfg.InitialDelay,
		MaxDelay:      cfg.MaxDelay,
		Multiplier:    2.0,
		BackoffPolicy: retry.BackoffExponential,
		Jitter:        true,
		IsRetryable:   isRetryable,
	})
	return &Client{
		base:    strings.TrimRight(cfg.BaseURL, "/"),
		hc:      cfg.HTTPClient,
		cfg:     cfg,
		retrier: r,
	}
}

// Do issues an HTTP request, JSON-encodes the body if non-nil, decodes the
// response into out (if non-nil), and applies retry on transient failures.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	if c.base == "" {
		return errors.New("httpx: base URL is required")
	}
	url := c.base + ensureLeadingSlash(path)

	raw, err := c.retrier.Do(ctx, func(ctx context.Context) (json.RawMessage, error) {
		var reader io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("httpx: marshal: %w", err)
			}
			reader = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, transientError{err: err}
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return respBody, nil
		}
		he := &HTTPError{Status: resp.StatusCode, Body: string(respBody), URL: url}
		if isRetryableStatus(resp.StatusCode) {
			return nil, transientError{err: he}
		}
		return nil, he
	})
	if err != nil {
		return err
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("httpx: decode: %w", err)
		}
	}
	return nil
}

// HTTPError is the structured error returned for non-2xx responses.
type HTTPError struct {
	Status int
	Body   string
	URL    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("httpx: %s -> %d: %s", e.URL, e.Status, truncate(e.Body, 200))
}

// transientError wraps a retryable failure so the retrier knows to back off.
type transientError struct{ err error }

func (e transientError) Error() string   { return e.err.Error() }
func (e transientError) Unwrap() error   { return e.err }
func (e transientError) Retryable() bool { return true }

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var t transientError
	return errors.As(err, &t)
}

func isRetryableStatus(code int) bool {
	if code == http.StatusTooManyRequests {
		return true
	}
	return code >= 500 && code <= 599
}

func ensureLeadingSlash(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		return "/" + p
	}
	return p
}

func defaultDuration(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
