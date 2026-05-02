// Package client is the Go SDK for the Olymp HTTP API.
//
// CLI commands, Claude Code / Codex plugins, and external agents call this
// package to Submit / Inspect / Steer / Stream against a running runtime.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/felixgeelhaar/olymp/internal/api"
	"github.com/felixgeelhaar/olymp/internal/domain"
)

// Config controls a Client.
type Config struct {
	BaseURL    string
	HTTPClient *http.Client
	Timeout    time.Duration
	Caller     domain.CallerRef
}

// Client is the Go SDK for the Olymp HTTP API.
type Client struct {
	cfg Config
	hc  *http.Client
}

// New returns a Client.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:8080"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: orDefault(cfg.Timeout, 30*time.Second)}
	}
	if cfg.Caller == (domain.CallerRef{}) {
		cfg.Caller = domain.CallerRef{Type: "user", ID: "anonymous"}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, hc: cfg.HTTPClient}
}

// Submit POSTs /v1/runs.
func (c *Client) Submit(ctx context.Context, in domain.Intent) (domain.Run, error) {
	req := api.SubmitRequest{Type: in.Type, Subject: in.Subject, Payload: in.Payload}
	var run domain.Run
	if err := c.do(ctx, http.MethodPost, "/v1/runs", req, &run); err != nil {
		return domain.Run{}, err
	}
	return run, nil
}

// Inspect GETs /v1/runs/:id.
func (c *Client) Inspect(ctx context.Context, runID string) (domain.RunSnapshot, error) {
	var snap domain.RunSnapshot
	if err := c.do(ctx, http.MethodGet, "/v1/runs/"+runID, nil, &snap); err != nil {
		return domain.RunSnapshot{}, err
	}
	return snap, nil
}

// Steer POSTs /v1/runs/:id/steer.
func (c *Client) Steer(ctx context.Context, runID string, cmd domain.SteerCommand) error {
	req := api.SteerRequest{Kind: cmd.Kind, Reason: cmd.Reason}
	return c.do(ctx, http.MethodPost, "/v1/runs/"+runID+"/steer", req, nil)
}

// Halt POSTs /v1/halt.
func (c *Client) Halt(ctx context.Context, reason string) ([]string, error) {
	req := api.HaltRequest{Reason: reason}
	var resp api.HaltResponse
	if err := c.do(ctx, http.MethodPost, "/v1/halt", req, &resp); err != nil {
		return nil, err
	}
	return resp.Affected, nil
}

// Stream subscribes to /v1/runs/stream and decodes SSE frames into the
// returned channel. The channel closes when ctx is cancelled or the stream
// terminates.
func (c *Client) Stream(ctx context.Context, filter domain.RunFilter) (<-chan domain.RunEvent, error) {
	q := ""
	if filter.RunID != "" {
		q = "?run_id=" + filter.RunID
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"/v1/runs/stream"+q, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	c.applyCallerHeaders(req)
	resp, err := c.hc.Do(req) //nolint:bodyclose // closed in goroutine below for the success path
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("stream: %d: %s", resp.StatusCode, body)
	}
	out := make(chan domain.RunEvent, 32)
	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()
		s := bufio.NewScanner(resp.Body)
		s.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for s.Scan() {
			line := s.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			var ev domain.RunEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- ev:
			}
		}
	}()
	return out, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	c.applyCallerHeaders(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return &Error{Status: resp.StatusCode, Body: string(respBody), URL: req.URL.String()}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("client: decode: %w", err)
		}
	}
	return nil
}

func (c *Client) applyCallerHeaders(req *http.Request) {
	req.Header.Set("X-Olymp-Caller-Type", c.cfg.Caller.Type)
	req.Header.Set("X-Olymp-Caller-Id", c.cfg.Caller.ID)
	if c.cfg.Caller.Name != "" {
		req.Header.Set("X-Olymp-Caller-Name", c.cfg.Caller.Name)
	}
}

// Error is the structured client-side error for non-2xx responses.
type Error struct {
	Status int
	Body   string
	URL    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("olymp client: %s -> %d: %s", e.URL, e.Status, truncate(e.Body, 200))
}

func orDefault(d, fb time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fb
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
