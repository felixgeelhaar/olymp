// Package praxis is the HTTP-client adapter for the execution layer.
//
// Olymp's PraxisPort verbs (`ListCapabilities`, `Execute`, `DryRun`)
// map to Praxis's three-verb HTTP surface. The only translation is
// the dry-run path: Praxis routes dry-runs as
// `POST /v1/actions/{id}/dry-run`, so we ensure each request carries
// an `id` and substitute it into the URL.
package praxis

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// Client implements ports.PraxisPort over HTTP+JSON.
type Client struct {
	hc *httpx.Client
}

// New returns a Praxis HTTP client.
func New(cfg httpx.Config) *Client { return &Client{hc: httpx.New(cfg)} }

// ListCapabilities maps to GET {base}/v1/capabilities.
func (c *Client) ListCapabilities(ctx context.Context) ([]domain.CapabilityRef, error) {
	var resp struct {
		Capabilities []domain.CapabilityRef `json:"capabilities"`
	}
	if err := c.hc.Do(ctx, "GET", "/v1/capabilities", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Capabilities, nil
}

// Execute maps to POST {base}/v1/actions. The action's idempotency
// key is auto-generated when missing so retries don't double-fire.
func (c *Client) Execute(ctx context.Context, a domain.ActionRequest) (domain.ActionResult, error) {
	a = ensureActionDefaults(a)
	var res domain.ActionResult
	if err := c.hc.Do(ctx, "POST", "/v1/actions", a, &res); err != nil {
		return domain.ActionResult{}, err
	}
	return res, nil
}

// DryRun maps to POST {base}/v1/actions/{id}/dry-run. Praxis treats
// the dry-run path as a per-action subresource, so we generate an ID
// up front when the caller didn't.
func (c *Client) DryRun(ctx context.Context, a domain.ActionRequest) (domain.SimulationRef, error) {
	a = ensureActionDefaults(a)
	if a.ID == "" {
		return domain.SimulationRef{}, errors.New("praxis: action id required for dry-run")
	}
	var sim domain.SimulationRef
	if err := c.hc.Do(ctx, "POST", "/v1/actions/"+a.ID+"/dry-run", a, &sim); err != nil {
		return domain.SimulationRef{}, err
	}
	return sim, nil
}

// ensureActionDefaults guarantees the request carries the values
// Praxis treats as required: a stable action ID and an idempotency
// key. Both are auto-generated when missing.
func ensureActionDefaults(a domain.ActionRequest) domain.ActionRequest {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.IdempotencyKey == "" {
		a.IdempotencyKey = a.ID
	}
	return a
}

var _ ports.PraxisPort = (*Client)(nil)
