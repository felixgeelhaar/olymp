package ports

import (
	"context"

	"github.com/felixgeelhaar/olymp/internal/domain"
)

// MnemosPort talks to the memory layer (Mnemos). Recall surfaces relevant
// memories for a Goal/Session; Append writes outcomes back; Get is by ID.
type MnemosPort interface {
	Recall(ctx context.Context, q domain.MemoryQuery) ([]domain.MemoryRef, error)
	Append(ctx context.Context, e domain.OutcomeEvent) error
	Get(ctx context.Context, id string) (domain.MemoryRef, error)
}

// ChronosPort talks to the time / pattern layer (Chronos). Signals returns
// the structured signals relevant to the Goal at the current iteration.
type ChronosPort interface {
	Signals(ctx context.Context, q domain.SignalQuery) ([]domain.SignalRef, error)
	Get(ctx context.Context, id string) (domain.SignalRef, error)
}

// NousPort talks to the decision layer (Nous). Decide consumes memories +
// signals + history and returns a structured decision with action requests.
type NousPort interface {
	Decide(ctx context.Context, in domain.DecisionRequest) (domain.DecisionRef, error)
	Get(ctx context.Context, id string) (domain.DecisionRef, error)
}

// PraxisPort talks to the execution layer (Praxis). Three verbs only.
type PraxisPort interface {
	ListCapabilities(ctx context.Context) ([]domain.CapabilityRef, error)
	Execute(ctx context.Context, a domain.ActionRequest) (domain.ActionResult, error)
	DryRun(ctx context.Context, a domain.ActionRequest) (domain.SimulationRef, error)
}

// Layers bundles every cognitive-layer adapter for one runtime.
type Layers struct {
	Mnemos  MnemosPort
	Chronos ChronosPort
	Nous    NousPort
	Praxis  PraxisPort
}
