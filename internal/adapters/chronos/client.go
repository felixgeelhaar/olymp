// Package chronos is the HTTP-client adapter for the time / pattern layer.
package chronos

import (
	"context"

	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// Client implements ports.ChronosPort over HTTP+JSON.
type Client struct {
	hc *httpx.Client
}

// New returns a Chronos HTTP client.
func New(cfg httpx.Config) *Client { return &Client{hc: httpx.New(cfg)} }

// Signals maps to POST {base}/v1/signals/query.
func (c *Client) Signals(ctx context.Context, q domain.SignalQuery) ([]domain.SignalRef, error) {
	var resp struct {
		Signals []domain.SignalRef `json:"signals"`
	}
	if err := c.hc.Do(ctx, "POST", "/v1/signals/query", q, &resp); err != nil {
		return nil, err
	}
	return resp.Signals, nil
}

// Get maps to GET {base}/v1/signals/{id}.
func (c *Client) Get(ctx context.Context, id string) (domain.SignalRef, error) {
	var ref domain.SignalRef
	if err := c.hc.Do(ctx, "GET", "/v1/signals/"+id, nil, &ref); err != nil {
		return domain.SignalRef{}, err
	}
	return ref, nil
}

var _ ports.ChronosPort = (*Client)(nil)
