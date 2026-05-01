// Package rollback emits a compensating Praxis action when a downstream
// Chronos signal flags regression after a deploy.
//
// A Compensator wraps a Praxis client + a Chronos signal predicate. It is
// hooked up by the runtime (cmd/olymp) once the loop completes; the engine
// itself does not call into rollback so the loop stays single-purpose.
package rollback

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// RegressionPredicate decides whether a Chronos signal indicates regression
// after the action set executed.
type RegressionPredicate func(domain.SignalRef) bool

// CompensateMap maps capabilities → their compensating capability.
//
//	"rollout_restart" → "rollout_undo"
type CompensateMap map[string]string

// Compensator scans a stored Run + the latest Chronos signals; if regression
// is detected, it dispatches a compensating action via Praxis and writes a
// rollback outcome event back to Mnemos.
type Compensator struct {
	chronos       ports.ChronosPort
	praxis        ports.PraxisPort
	mnemos        ports.MnemosPort
	predicate     RegressionPredicate
	compensateMap CompensateMap
	now           func() time.Time
}

// New builds a Compensator. predicate may be nil — the default treats any
// `spike` or `drop` pattern with confidence ≥ 0.7 as regression.
func New(chronos ports.ChronosPort, praxis ports.PraxisPort, mnemos ports.MnemosPort,
	predicate RegressionPredicate, compensateMap CompensateMap) *Compensator {
	if predicate == nil {
		predicate = DefaultRegressionPredicate
	}
	return &Compensator{
		chronos: chronos, praxis: praxis, mnemos: mnemos,
		predicate: predicate, compensateMap: compensateMap,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// Run inspects the given run for actions that succeeded, fetches recent
// signals, and emits compensating actions for any flagged regression.
// Returns the action results (one per compensated action). A run with no
// completed actions is a no-op.
func (c *Compensator) Run(ctx context.Context, run domain.Run) ([]domain.ActionResult, error) {
	if run.Status != domain.StatusCompleted {
		return nil, nil
	}
	signals, err := c.chronos.Signals(ctx, domain.SignalQuery{
		RunID: run.ID, Goal: run.Goal, Session: run.Session,
		Since: run.StartedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("rollback: signals: %w", err)
	}
	if !anyRegression(signals, c.predicate) {
		return nil, nil
	}

	completedActions := capabilitiesActed(run)
	var results []domain.ActionResult
	for _, cap := range completedActions {
		comp, ok := c.compensateMap[cap]
		if !ok {
			continue
		}
		req := domain.ActionRequest{
			ID:             "rollback-" + run.ID + "-" + cap,
			Capability:     comp,
			Caller:         run.Caller,
			IdempotencyKey: "rollback-" + run.ID + "-" + cap,
			Metadata: map[string]any{
				"run_id":   run.ID,
				"original": cap,
				"reason":   "regression_detected",
			},
		}
		res, err := c.praxis.Execute(ctx, req)
		if err != nil {
			return results, fmt.Errorf("rollback: execute %s: %w", comp, err)
		}
		results = append(results, res)
		if c.mnemos != nil {
			_ = c.mnemos.Append(ctx, domain.OutcomeEvent{
				Type: "olymp.rollback", RunID: run.ID, ActionID: req.ID,
				Capability: comp, Status: res.Status, Timestamp: c.now(),
			})
		}
	}
	if len(results) == 0 {
		return nil, errors.New("rollback: regression detected but no compensating actions configured")
	}
	return results, nil
}

// DefaultRegressionPredicate returns true for spike/drop signals with
// confidence ≥ 0.7. Hosts can override with a domain-specific predicate.
func DefaultRegressionPredicate(s domain.SignalRef) bool {
	if s.Confidence < 0.7 {
		return false
	}
	switch s.Pattern {
	case "spike", "drop", "anomaly":
		return true
	}
	return false
}

func anyRegression(signals []domain.SignalRef, p RegressionPredicate) bool {
	for _, s := range signals {
		if p(s) {
			return true
		}
	}
	return false
}

// capabilitiesActed scans a Run's provenance for acting steps and returns
// every successfully executed (capability, action_id) pair. The engine
// stamps `actions: [{id, capability, status}, ...]` onto each acting step's
// outputs; this reads it back.
func capabilitiesActed(run domain.Run) []string {
	var caps []string
	seen := map[string]bool{}
	for _, step := range run.Provenance.Steps {
		if step.Stage != domain.StatusActing || step.Outputs == nil {
			continue
		}
		raw, ok := step.Outputs["actions"]
		if !ok {
			continue
		}
		entries, ok := raw.([]any)
		if !ok {
			// May be []map[string]any when not round-tripped through JSON.
			if mList, ok := raw.([]map[string]any); ok {
				for _, m := range mList {
					if c, ok := m["capability"].(string); ok && m["status"] == "succeeded" && !seen[c] {
						caps = append(caps, c)
						seen[c] = true
					}
				}
			}
			continue
		}
		for _, e := range entries {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if c, ok := m["capability"].(string); ok && m["status"] == "succeeded" && !seen[c] {
				caps = append(caps, c)
				seen[c] = true
			}
		}
	}
	return caps
}
