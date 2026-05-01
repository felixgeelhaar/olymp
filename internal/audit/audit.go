// Package audit is the runtime-level audit + provenance writer.
//
// Storage primitives live in internal/store (provenance_steps + audit_events
// tables, populated through ports.RunRepo and ports.AuditRepo). This package
// is the thin layer that:
//
//   - exposes the canonical Kind constants
//   - assembles structured audit details from domain types
//   - reconstructs the cross-layer provenance chain (Memory → Signal →
//     Decision → Action → Outcome) on demand
//
// The loop engine (internal/engine, next task) calls these helpers at every
// transition and every layer interaction so the audit trail is the product.
package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"

	"github.com/google/uuid"
)

// Canonical audit event kinds. Consumers (Inspect, dashboards, exports) match
// on these strings — never invent new kinds outside this package.
const (
	KindSubmitted        = "submitted"
	KindTransitioned     = "transitioned"
	KindSteered          = "steered"
	KindApprovalRequired = "approval_required"
	KindApprovalResolved = "approval_resolved"
	KindLayerCalled      = "layer_called"
	KindOutcomeWritten   = "outcome_written"
	KindFailed           = "failed"
)

// Now returns the current time in UTC. Tests override via Logger.Clock.
type Clock func() time.Time

// Logger appends structured audit events. Construct with New.
type Logger struct {
	repo  ports.AuditRepo
	clock Clock
}

// New returns a Logger backed by the given repository. clock may be nil — UTC
// wall-clock is used when so.
func New(repo ports.AuditRepo, clock Clock) *Logger {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Logger{repo: repo, clock: clock}
}

// Submitted records the initial Submit. Detail carries the intent shape so
// the run can be reconstructed even if the runs table is gone.
func (l *Logger) Submitted(ctx context.Context, run domain.Run) error {
	return l.append(ctx, run.ID, KindSubmitted, map[string]any{
		"intent": map[string]any{
			"type":    run.Intent.Type,
			"subject": run.Intent.Subject,
		},
		"caller": run.Caller,
		"goal":   run.Goal,
	})
}

// Transitioned records a state change.
func (l *Logger) Transitioned(ctx context.Context, runID string, from, to domain.RunStatus, iter int) error {
	return l.append(ctx, runID, KindTransitioned, map[string]any{
		"from":      string(from),
		"to":        string(to),
		"iteration": iter,
	})
}

// Steered records a SteerCommand.
func (l *Logger) Steered(ctx context.Context, runID string, cmd domain.SteerCommand) error {
	return l.append(ctx, runID, KindSteered, map[string]any{
		"kind":   cmd.Kind,
		"reason": cmd.Reason,
		"caller": cmd.Caller,
	})
}

// LayerCalled records one cognitive-layer interaction.
func (l *Logger) LayerCalled(ctx context.Context, runID string, layer string, op string, layerRef string, iter int) error {
	return l.append(ctx, runID, KindLayerCalled, map[string]any{
		"layer":     layer,
		"op":        op,
		"layer_ref": layerRef,
		"iteration": iter,
	})
}

// OutcomeWritten records the writeback to Mnemos.
func (l *Logger) OutcomeWritten(ctx context.Context, runID string, ev domain.OutcomeEvent) error {
	return l.append(ctx, runID, KindOutcomeWritten, map[string]any{
		"action_id":   ev.ActionID,
		"capability":  ev.Capability,
		"status":      ev.Status,
		"iteration":   ev.Iteration,
		"decision_id": ev.DecisionID,
	})
}

// ApprovalRequired records an awaiting_approval gate.
func (l *Logger) ApprovalRequired(ctx context.Context, runID, decisionID string, reason string) error {
	return l.append(ctx, runID, KindApprovalRequired, map[string]any{
		"decision_id": decisionID,
		"reason":      reason,
	})
}

// ApprovalResolved records resolution of a gate.
func (l *Logger) ApprovalResolved(ctx context.Context, runID string, decision domain.ApprovalDecision) error {
	return l.append(ctx, runID, KindApprovalResolved, map[string]any{
		"decision": decision.Decision,
		"reason":   decision.Reason,
		"resolver": decision.Resolver,
	})
}

// Failed records a non-recoverable failure.
func (l *Logger) Failed(ctx context.Context, runID string, err *domain.RunError) error {
	if err == nil {
		err = &domain.RunError{Code: "unknown"}
	}
	return l.append(ctx, runID, KindFailed, map[string]any{
		"code":    err.Code,
		"message": err.Message,
		"layer":   err.Layer,
	})
}

func (l *Logger) append(ctx context.Context, runID, kind string, detail map[string]any) error {
	if runID == "" {
		return fmt.Errorf("audit: run_id is required")
	}
	return l.repo.Append(ctx, domain.AuditEvent{
		ID:        uuid.NewString(),
		RunID:     runID,
		Kind:      kind,
		Detail:    detail,
		CreatedAt: l.clock(),
	})
}

// Chain is the reconstructed Memory → Signal → Decision → Action → Outcome
// provenance for one Run, ordered by iteration then stage.
type Chain struct {
	RunID string
	Steps []domain.ProvenanceStep
}

// Reconstruct loads a run, returning its provenance chain in canonical order.
// Used by Inspect, decision explainability, and post-mortems.
func Reconstruct(ctx context.Context, runs ports.RunRepo, runID string) (Chain, error) {
	run, err := runs.Get(ctx, runID)
	if err != nil {
		return Chain{}, err
	}
	steps := append([]domain.ProvenanceStep(nil), run.Provenance.Steps...)
	// stable order by iteration, then by stage in canonical loop order
	stageOrder := map[domain.RunStatus]int{
		domain.StatusObserving:        0,
		domain.StatusUnderstanding:    1,
		domain.StatusDeciding:         2,
		domain.StatusAwaitingApproval: 3,
		domain.StatusActing:           4,
		domain.StatusLearning:         5,
	}
	sortStable(steps, func(a, b domain.ProvenanceStep) bool {
		if a.Iteration != b.Iteration {
			return a.Iteration < b.Iteration
		}
		ai, ok := stageOrder[a.Stage]
		if !ok {
			ai = 99
		}
		bi, ok := stageOrder[b.Stage]
		if !ok {
			bi = 99
		}
		return ai < bi
	})
	return Chain{RunID: runID, Steps: steps}, nil
}

// sortStable is a tiny stable insertion sort. The chain is small (one step
// per stage per iteration); avoiding a sort.Slice import here makes the
// dependency surface obvious to readers.
func sortStable[T any](xs []T, less func(a, b T) bool) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && less(xs[j], xs[j-1]); j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
