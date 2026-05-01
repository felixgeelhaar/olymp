// Package mnemos is the HTTP-client adapter for the memory layer.
package mnemos

import (
	"context"

	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// Client implements ports.MnemosPort over HTTP+JSON.
type Client struct {
	hc *httpx.Client
}

// New returns a Mnemos HTTP client wired with retry + timeouts via httpx.
func New(cfg httpx.Config) *Client { return &Client{hc: httpx.New(cfg)} }

// Recall maps to POST {base}/v1/memories/recall.
func (c *Client) Recall(ctx context.Context, q domain.MemoryQuery) ([]domain.MemoryRef, error) {
	var resp struct {
		Memories []domain.MemoryRef `json:"memories"`
	}
	if err := c.hc.Do(ctx, "POST", "/v1/memories/recall", q, &resp); err != nil {
		return nil, err
	}
	return resp.Memories, nil
}

// Append maps to POST {base}/v1/events.
func (c *Client) Append(ctx context.Context, e domain.OutcomeEvent) error {
	return c.hc.Do(ctx, "POST", "/v1/events", e, nil)
}

// Get maps to GET {base}/v1/memories/{id}.
func (c *Client) Get(ctx context.Context, id string) (domain.MemoryRef, error) {
	var ref domain.MemoryRef
	if err := c.hc.Do(ctx, "GET", "/v1/memories/"+id, nil, &ref); err != nil {
		return domain.MemoryRef{}, err
	}
	return ref, nil
}

// Compile-time assertion the client satisfies the port.
var _ ports.MnemosPort = (*Client)(nil)
