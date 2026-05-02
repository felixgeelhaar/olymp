// Package nous is the HTTP-client adapter for the decision layer.
//
// Olymp's NousPort speaks `Decide(DecisionRequest) -> DecisionRef`.
// Real Nous's HTTP surface decides things internally during commitment
// extraction and exposes the audit trail as `GET /v1/decisions/{id}`.
// We adapt by submitting Olymp's loop context to `POST /v1/extract`
// (owner+text body) and wrapping the resulting `decision_id` as a
// DecisionRef. When extraction returns no decision (no commitments
// extracted), we synthesise a stable run-scoped reference so the
// engine can keep iterating.
package nous

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// Client implements ports.NousPort over HTTP+JSON.
type Client struct {
	hc *httpx.Client
}

// New returns a Nous HTTP client.
func New(cfg httpx.Config) *Client { return &Client{hc: httpx.New(cfg)} }

// Decide submits the loop's context to Nous's commitment extractor
// and projects the result as a DecisionRef.
func (c *Client) Decide(ctx context.Context, in domain.DecisionRequest) (domain.DecisionRef, error) {
	owner := decideOwner(in)
	body := extractRequest{
		OwnerID: owner,
		Text:    decideText(in),
	}
	var resp extractResponse
	if err := c.hc.Do(ctx, "POST", "/v1/extract", body, &resp); err != nil {
		return domain.DecisionRef{}, err
	}
	id := resp.DecisionID
	if id == "" && len(resp.SavedIDs) > 0 {
		id = resp.SavedIDs[0]
	}
	if id == "" {
		// No commitment extracted, no decision recorded. Surface a
		// stable synthetic reference so the loop's audit trail still
		// has something to point at.
		id = "olymp-noop:" + in.RunID
	}
	return domain.DecisionRef{ID: id}, nil
}

// Get fetches a recorded decision by ID.
func (c *Client) Get(ctx context.Context, id string) (domain.DecisionRef, error) {
	if id == "" {
		return domain.DecisionRef{}, errors.New("nous: empty id")
	}
	if strings.HasPrefix(id, "olymp-noop:") {
		return domain.DecisionRef{ID: id}, nil
	}
	var ref decisionDTO
	if err := c.hc.Do(ctx, "GET", "/v1/decisions/"+id, nil, &ref); err != nil {
		return domain.DecisionRef{}, err
	}
	return domain.DecisionRef{ID: ref.ID}, nil
}

func decideOwner(in domain.DecisionRequest) string {
	if in.RunID != "" {
		return "olymp:" + in.RunID
	}
	return "olymp"
}

// decideText folds the loop context into a short prose payload that
// Nous's extractor can chew on. Memories + signals are summarised by
// id only — Nous has its own knowledge layer; we don't try to ship
// full evidence over a single extract call.
func decideText(in domain.DecisionRequest) string {
	var b strings.Builder
	if d := strings.TrimSpace(in.Goal.Description); d != "" {
		b.WriteString(d)
	} else {
		b.WriteString("olymp loop iteration")
	}
	if len(in.Memories) > 0 {
		fmt.Fprintf(&b, "\n\nmemories: ")
		for i, m := range in.Memories {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(m.ID)
		}
	}
	if len(in.Signals) > 0 {
		fmt.Fprintf(&b, "\n\nsignals: ")
		for i, s := range in.Signals {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(s.ID)
		}
	}
	return b.String()
}

// --- Nous wire types (mirror nous/internal/transport/http/server.go) ---

type extractRequest struct {
	OwnerID string `json:"owner_id"`
	Text    string `json:"text"`
}

type extractResponse struct {
	Considered int      `json:"considered"`
	SavedIDs   []string `json:"saved_ids"`
	Dropped    int      `json:"dropped"`
	DecisionID string   `json:"decision_id"`
}

type decisionDTO struct {
	ID string `json:"id"`
}

var _ ports.NousPort = (*Client)(nil)
