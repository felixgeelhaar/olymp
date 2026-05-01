// Package nous is the HTTP-client adapter for the decision layer.
package nous

import (
	"context"

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

// Decide maps to POST {base}/v1/decisions.
func (c *Client) Decide(ctx context.Context, in domain.DecisionRequest) (domain.DecisionRef, error) {
	var ref domain.DecisionRef
	if err := c.hc.Do(ctx, "POST", "/v1/decisions", in, &ref); err != nil {
		return domain.DecisionRef{}, err
	}
	return ref, nil
}

// Get maps to GET {base}/v1/decisions/{id}.
func (c *Client) Get(ctx context.Context, id string) (domain.DecisionRef, error) {
	var ref domain.DecisionRef
	if err := c.hc.Do(ctx, "GET", "/v1/decisions/"+id, nil, &ref); err != nil {
		return domain.DecisionRef{}, err
	}
	return ref, nil
}

var _ ports.NousPort = (*Client)(nil)
