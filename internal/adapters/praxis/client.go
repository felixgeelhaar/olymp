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
		Capabilities []capabilityWire `json:"capabilities"`
	}
	if err := c.hc.Do(ctx, "GET", "/v1/capabilities", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.CapabilityRef, 0, len(resp.Capabilities))
	for _, cap := range resp.Capabilities {
		out = append(out, domain.CapabilityRef{Name: cap.Name, Description: cap.Description, Idempotent: cap.Idempotent, Simulatable: cap.Simulatable})
	}
	return out, nil
}

// Execute maps to POST {base}/v1/actions. The action's idempotency
// key is auto-generated when missing so retries don't double-fire.
func (c *Client) Execute(ctx context.Context, a domain.ActionRequest) (domain.ActionResult, error) {
	a = ensureActionDefaults(a)
	var res resultWire
	if err := c.hc.Do(ctx, "POST", "/v1/actions", actionToWire(a), &res); err != nil {
		return domain.ActionResult{}, err
	}
	return wireToResult(res), nil
}

// DryRun maps to POST {base}/v1/actions/{id}/dry-run. Praxis treats
// the dry-run path as a per-action subresource, so we generate an ID
// up front when the caller didn't.
func (c *Client) DryRun(ctx context.Context, a domain.ActionRequest) (domain.SimulationRef, error) {
	a = ensureActionDefaults(a)
	if a.ID == "" {
		return domain.SimulationRef{}, errors.New("praxis: action id required for dry-run")
	}
	var sim simulationWire
	if err := c.hc.Do(ctx, "POST", "/v1/actions/"+a.ID+"/dry-run", actionToWire(a), &sim); err != nil {
		return domain.SimulationRef{}, err
	}
	return domain.SimulationRef{ActionID: sim.ActionID, Reversible: sim.Reversible}, nil
}

// --- Praxis wire types (mirror praxis/internal/domain.Action JSON shape) ---
//
// Praxis decodes with `DisallowUnknownFields`, so the field names must
// match Praxis's default Go-struct casing exactly. Olymp's
// `domain.ActionRequest` uses snake_case JSON tags for its own surface;
// we translate at the adapter boundary.

type actionWire struct {
	ID             string         `json:"ID"`
	Capability     string         `json:"Capability"`
	Payload        map[string]any `json:"Payload,omitempty"`
	Caller         callerWire     `json:"Caller"`
	Scope          []string       `json:"Scope,omitempty"`
	IdempotencyKey string         `json:"IdempotencyKey"`
	Mode           string         `json:"Mode"`
}

type callerWire struct {
	Type   string `json:"Type,omitempty"`
	ID     string `json:"ID,omitempty"`
	Name   string `json:"Name,omitempty"`
	OrgID  string `json:"OrgID,omitempty"`
	TeamID string `json:"TeamID,omitempty"`
}

type capabilityWire struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Idempotent  bool   `json:"idempotent"`
	Simulatable bool   `json:"simulatable"`
}

// Praxis's domain.Result has no JSON tags, so Go's default marshal
// emits CapitalCase (`"ActionID"`). We mirror that here on the read
// side; on the write side `actionWire` already does the same for the
// request body.
type resultWire struct {
	ActionID    string         `json:"ActionID"`
	Status      string         `json:"Status"`
	Output      map[string]any `json:"Output,omitempty"`
	ExternalID  string         `json:"ExternalID,omitempty"`
	StartedAt   string         `json:"StartedAt,omitempty"`
	CompletedAt string         `json:"CompletedAt,omitempty"`
	Attempts    int            `json:"Attempts,omitempty"`
}

type simulationWire struct {
	ActionID   string `json:"ActionID"`
	Reversible bool   `json:"Reversible"`
}

func actionToWire(a domain.ActionRequest) actionWire {
	return actionWire{
		ID:             a.ID,
		Capability:     a.Capability,
		Payload:        a.Payload,
		Caller:         callerWire{Type: a.Caller.Type, ID: a.Caller.ID, Name: a.Caller.Name},
		Scope:          a.Scope,
		IdempotencyKey: a.IdempotencyKey,
		Mode:           "sync",
	}
}

func wireToResult(w resultWire) domain.ActionResult {
	return domain.ActionResult{
		ActionID:   w.ActionID,
		Status:     w.Status,
		Output:     w.Output,
		ExternalID: w.ExternalID,
		Attempts:   w.Attempts,
	}
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
