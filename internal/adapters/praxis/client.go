// Package praxis is the HTTP-client adapter for the execution layer.
package praxis

import (
	"context"

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

// Execute maps to POST {base}/v1/actions.
func (c *Client) Execute(ctx context.Context, a domain.ActionRequest) (domain.ActionResult, error) {
	var res domain.ActionResult
	if err := c.hc.Do(ctx, "POST", "/v1/actions", a, &res); err != nil {
		return domain.ActionResult{}, err
	}
	return res, nil
}

// DryRun maps to POST {base}/v1/actions/dry-run.
func (c *Client) DryRun(ctx context.Context, a domain.ActionRequest) (domain.SimulationRef, error) {
	var sim domain.SimulationRef
	if err := c.hc.Do(ctx, "POST", "/v1/actions/dry-run", a, &sim); err != nil {
		return domain.SimulationRef{}, err
	}
	return sim, nil
}

var _ ports.PraxisPort = (*Client)(nil)
