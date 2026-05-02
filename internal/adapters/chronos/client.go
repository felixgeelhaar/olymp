// Package chronos is the HTTP-client adapter for the time / pattern layer.
//
// Olymp's ChronosPort speaks an abstract Signals(query) verb; real
// Chronos exposes scoped signal listing (`GET /v1/signals?scope_id=…`).
// This file translates between the two without changing Chronos's
// standalone HTTP surface.
package chronos

import (
	"context"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// Config carries Chronos-specific options on top of httpx.Config.
type Config struct {
	HTTP httpx.Config
	// DefaultScopeID is the Chronos scope queried when MemoryQuery
	// carries no `scope_id` filter. Empty disables Signals — the
	// adapter returns an empty list rather than failing the run.
	DefaultScopeID string
}

// Client implements ports.ChronosPort over HTTP+JSON.
type Client struct {
	hc           *httpx.Client
	defaultScope string
}

// New returns a Chronos HTTP client.
func New(cfg httpx.Config) *Client {
	return NewWithConfig(Config{HTTP: cfg})
}

// NewWithConfig wires the chronos-specific defaults alongside the
// shared httpx Config.
func NewWithConfig(cfg Config) *Client {
	return &Client{hc: httpx.New(cfg.HTTP), defaultScope: cfg.DefaultScopeID}
}

// Signals lists matching signals from Chronos.
//
// Mapping: Chronos requires `scope_id` (uuid) on every signals
// query. Olymp's SignalQuery doesn't carry one natively; the adapter
// reads `Filter["scope_id"]` first, falls back to the configured
// default, and returns an empty list when neither is set. That keeps
// runs unblocked when Chronos has no scope wired (fresh deploy,
// tests).
func (c *Client) Signals(ctx context.Context, q domain.SignalQuery) ([]domain.SignalRef, error) {
	scopeID := c.defaultScope
	if q.Filter != nil {
		if v, ok := q.Filter["scope_id"].(string); ok && v != "" {
			scopeID = v
		}
	}
	if scopeID == "" {
		return []domain.SignalRef{}, nil
	}
	v := url.Values{}
	v.Set("scope_id", scopeID)
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	var resp signalsResponse
	if err := c.hc.Do(ctx, "GET", "/v1/signals?"+v.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.SignalRef, 0, len(resp.Signals))
	for _, s := range resp.Signals {
		out = append(out, signalToRef(s))
	}
	return out, nil
}

// Get fetches a single signal by ID. Chronos enforces a uuid path
// segment; non-uuid IDs short-circuit to ErrNotFound rather than
// hitting the upstream with a guaranteed 400.
func (c *Client) Get(ctx context.Context, id string) (domain.SignalRef, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.SignalRef{}, ports.ErrNotFound
	}
	var s signalDTO
	if err := c.hc.Do(ctx, "GET", "/v1/signals/"+id, nil, &s); err != nil {
		return domain.SignalRef{}, err
	}
	return signalToRef(s), nil
}

func signalToRef(s signalDTO) domain.SignalRef {
	md := map[string]any{}
	if s.Pattern != "" {
		md["pattern"] = s.Pattern
	}
	if !s.DetectedAt.IsZero() {
		md["detected_at"] = s.DetectedAt
	}
	for k, v := range s.Metrics {
		md[k] = v
	}
	return domain.SignalRef{
		ID:         s.ID,
		Pattern:    s.Pattern,
		Strength:   s.Strength,
		Confidence: s.Confidence,
		Metadata:   md,
	}
}

// --- Chronos wire types (mirror chronos/internal/api/dto.go) ---

type signalsResponse struct {
	Signals []signalDTO `json:"signals"`
	Count   int         `json:"count"`
}

type signalDTO struct {
	ID         string             `json:"id"`
	Pattern    string             `json:"pattern"`
	DetectedAt jsonTime           `json:"detected_at"`
	Strength   float64            `json:"strength"`
	Confidence float64            `json:"confidence"`
	Metrics    map[string]float64 `json:"metrics,omitempty"`
}

var _ ports.ChronosPort = (*Client)(nil)
