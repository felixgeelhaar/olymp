package plugin

import (
	"context"
	"fmt"

	"github.com/felixgeelhaar/olymp/client"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// Peer is a remote Olymp runtime addressed via the Go client. It satisfies
// ports.NousPort by delegating Decide to a `submit_intent` of type "decide"
// on the peer (or any IntentType the peer registers for that purpose). This
// is the simplest federation primitive: a runtime can recursively consult
// another runtime as part of its loop.
//
// Federation is intentionally narrow in v0: only the deciding stage is
// federable. Memories + Signals + Praxis stay local — those are the layers
// where data sovereignty matters most.
type Peer struct {
	c          *client.Client
	intentType string
}

// NewPeer wraps a client + the IntentType the peer expects for decision
// federation requests.
func NewPeer(c *client.Client, intentType string) *Peer {
	if intentType == "" {
		intentType = "delegate_decide"
	}
	return &Peer{c: c, intentType: intentType}
}

// Decide delegates to the peer by submitting an intent and waiting for the
// terminal Run. The Run's last decision is reconstructed from the peer
// response.
func (p *Peer) Decide(ctx context.Context, in domain.DecisionRequest) (domain.DecisionRef, error) {
	run, err := p.c.Submit(ctx, domain.Intent{
		Type: p.intentType,
		Payload: map[string]any{
			"goal":     in.Goal,
			"memories": in.Memories,
			"signals":  in.Signals,
		},
	})
	if err != nil {
		return domain.DecisionRef{}, fmt.Errorf("peer: submit: %w", err)
	}
	if run.PendingDecision != nil {
		return *run.PendingDecision, nil
	}
	return domain.DecisionRef{
		ID:        run.ID,
		Rationale: "federated to peer; see peer run " + run.ID,
	}, nil
}

// Get is unsupported in v0 federation — peers do not expose decision-by-id
// because each peer's decision IDs are local to that runtime. Inspect the
// peer's run instead.
func (p *Peer) Get(ctx context.Context, id string) (domain.DecisionRef, error) {
	return domain.DecisionRef{}, fmt.Errorf("peer: Get not supported (use peer.Inspect)")
}

// Inspect returns the peer's RunSnapshot for a delegated run. Useful for
// post-hoc auditing of federated decisions.
func (p *Peer) Inspect(ctx context.Context, runID string) (domain.RunSnapshot, error) {
	return p.c.Inspect(ctx, runID)
}

var _ ports.NousPort = (*Peer)(nil)
