// Package mnemos is the HTTP-client adapter for the memory layer.
//
// Olymp's MnemosPort speaks an abstract memory contract (Recall /
// Append / Get); real Mnemos exposes a concrete claim+event surface
// (`/v1/claims`, `/v1/events`). This file translates between the
// two, leaving Mnemos's standalone HTTP API untouched.
package mnemos

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// defaultRecallLimit is used when MemoryQuery.Limit is unset. Picked
// to roughly match Mnemos's listClaims default and to keep response
// payloads bounded.
const defaultRecallLimit = 50

// Client implements ports.MnemosPort over Mnemos's real HTTP API.
type Client struct {
	hc *httpx.Client
}

// New returns a Mnemos HTTP client wired with retry + timeouts via httpx.
func New(cfg httpx.Config) *Client { return &Client{hc: httpx.New(cfg)} }

// Recall surfaces relevant memories for the loop's current goal.
//
// Mapping: Mnemos has no `/v1/memories/recall` endpoint. The closest
// concept is its claims store — derived assertions over events. We
// list active claims (`GET /v1/claims?status=active&limit=N`) and
// project each claim into a `MemoryRef`. The query's `Goal`,
// `Session`, and `Filter` fields are advisory only; Mnemos doesn't
// take a free-text query at the HTTP boundary, so the engine ranks
// downstream.
func (c *Client) Recall(ctx context.Context, q domain.MemoryQuery) ([]domain.MemoryRef, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultRecallLimit
	}
	v := url.Values{}
	v.Set("status", "active")
	v.Set("limit", strconv.Itoa(limit))
	var resp claimsResponse
	if err := c.hc.Do(ctx, "GET", "/v1/claims?"+v.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.MemoryRef, 0, len(resp.Claims))
	for _, cl := range resp.Claims {
		out = append(out, claimToMemory(cl))
	}
	return out, nil
}

// Append writes the loop's outcome event back into Mnemos so future
// runs can recall what happened.
//
// Mapping: Mnemos's `/v1/events` accepts a batch wrapper (see
// `appendEventsRequest`) of typed event records. We serialise the
// `OutcomeEvent` into a single record, stash the structured fields
// in `metadata`, and use a deterministic ID so retries are
// idempotent at the audit layer.
func (c *Client) Append(ctx context.Context, e domain.OutcomeEvent) error {
	if e.RunID == "" {
		return errors.New("mnemos: outcome event missing run_id")
	}
	ts := e.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	body := appendEventsRequest{
		Events: []eventDTO{{
			ID:            outcomeEventID(e),
			RunID:         e.RunID,
			SchemaVersion: "1",
			Content:       fmt.Sprintf("olymp outcome: intent=%s capability=%s status=%s", e.Intent.Type, e.Capability, e.Status),
			SourceInputID: "olymp",
			Timestamp:     ts.UTC().Format(time.RFC3339Nano),
			Metadata: map[string]string{
				"type":        e.Type,
				"iteration":   strconv.Itoa(e.Iteration),
				"intent_type": e.Intent.Type,
				"capability":  e.Capability,
				"status":      e.Status,
				"action_id":   e.ActionID,
				"decision_id": e.DecisionID,
			},
		}},
	}
	var resp appendResponse
	return c.hc.Do(ctx, "POST", "/v1/events", body, &resp)
}

// Get resolves a memory by ID. Mnemos has no by-ID lookup endpoint
// (claims are listed, not fetched). We list claims and filter
// client-side; a missing ID returns ports.ErrNotFound.
//
// This is best-effort by design: under heavy claim volume the linear
// scan is wasteful, but Olymp's loop only calls Get for IDs returned
// from Recall in the same iteration, so the working set is bounded.
func (c *Client) Get(ctx context.Context, id string) (domain.MemoryRef, error) {
	if id == "" {
		return domain.MemoryRef{}, errors.New("mnemos: empty id")
	}
	v := url.Values{}
	v.Set("limit", strconv.Itoa(defaultRecallLimit))
	var resp claimsResponse
	if err := c.hc.Do(ctx, "GET", "/v1/claims?"+v.Encode(), nil, &resp); err != nil {
		return domain.MemoryRef{}, err
	}
	for _, cl := range resp.Claims {
		if cl.ID == id {
			return claimToMemory(cl), nil
		}
	}
	return domain.MemoryRef{}, ports.ErrNotFound
}

// outcomeEventID derives a stable event ID for retries. Falls back
// to a fresh UUID when run/iteration aren't enough to disambiguate.
func outcomeEventID(e domain.OutcomeEvent) string {
	if e.RunID != "" && e.ActionID != "" {
		return fmt.Sprintf("olymp:%s:%d:%s", e.RunID, e.Iteration, e.ActionID)
	}
	return uuid.NewString()
}

func claimToMemory(c claimDTO) domain.MemoryRef {
	md := map[string]any{}
	if c.Status != "" {
		md["status"] = c.Status
	}
	if c.Text != "" {
		md["text"] = c.Text
	}
	if c.CreatedAt != "" {
		md["created_at"] = c.CreatedAt
	}
	return domain.MemoryRef{
		ID:         c.ID,
		Kind:       c.Type,
		Confidence: c.Confidence,
		Metadata:   md,
	}
}

// --- Mnemos wire types (mirror cmd/mnemos/serve.go DTOs) ---

type claimsResponse struct {
	Claims []claimDTO `json:"claims"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

type claimDTO struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	Type       string  `json:"type"`
	Confidence float64 `json:"confidence"`
	Status     string  `json:"status"`
	CreatedAt  string  `json:"created_at"`
}

type appendEventsRequest struct {
	Events []eventDTO `json:"events"`
}

type eventDTO struct {
	ID            string            `json:"id"`
	RunID         string            `json:"run_id"`
	SchemaVersion string            `json:"schema_version"`
	Content       string            `json:"content"`
	SourceInputID string            `json:"source_input_id"`
	Timestamp     string            `json:"timestamp"`
	Metadata      map[string]string `json:"metadata"`
}

type appendResponse struct {
	Accepted int `json:"accepted"`
	Skipped  int `json:"skipped"`
}

// Compile-time assertion the client satisfies the port.
var _ ports.MnemosPort = (*Client)(nil)
