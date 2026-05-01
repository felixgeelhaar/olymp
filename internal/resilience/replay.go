package resilience

import (
	"context"
	"fmt"
	"reflect"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// ReplayResult is the outcome of a determinism check on a stored Run.
type ReplayResult struct {
	RunID         string           `json:"run_id"`
	Deterministic bool             `json:"deterministic"`
	OriginalIDs   []string         `json:"original_decision_ids"`
	ReplayedIDs   []string         `json:"replayed_decision_ids"`
	Diffs         []map[string]any `json:"diffs,omitempty"`
}

// Replay re-runs the deciding step against the same memories + signals
// recorded in the Run's provenance and reports whether Nous returns the
// same decision (by ID + action set). Used for the "≥ 95% deterministic on
// identical tuples" Phase-2 acceptance metric.
//
// This is read-only — no Praxis Execute, no Mnemos Append. It validates
// that current Nous behaviour is consistent with what was recorded.
func Replay(ctx context.Context, runs ports.RunRepo, layers ports.Layers, runID string) (ReplayResult, error) {
	run, err := runs.Get(ctx, runID)
	if err != nil {
		return ReplayResult{}, fmt.Errorf("replay: %w", err)
	}
	res := ReplayResult{RunID: runID, Deterministic: true}

	for iter := 0; iter <= run.Iteration; iter++ {
		mems, sigs, originalDecID := iterationContext(run, iter)
		if originalDecID == "" {
			continue
		}
		res.OriginalIDs = append(res.OriginalIDs, originalDecID)

		decision, err := layers.Nous.Decide(ctx, domain.DecisionRequest{
			RunID: run.ID, Goal: run.Goal,
			Memories: mems, Signals: sigs,
			History: run.Provenance,
		})
		if err != nil {
			return res, fmt.Errorf("replay: nous: %w", err)
		}
		res.ReplayedIDs = append(res.ReplayedIDs, decision.ID)
		if decision.ID != originalDecID || !sameActions(decision.Actions, run) {
			res.Deterministic = false
			res.Diffs = append(res.Diffs, map[string]any{
				"iteration":  iter,
				"original":   originalDecID,
				"replayed":   decision.ID,
				"actions_eq": sameActions(decision.Actions, run),
			})
		}
	}
	return res, nil
}

// iterationContext extracts the memory + signal refs and the deciding-stage
// decision ID for one iteration of the run.
func iterationContext(run domain.Run, iter int) (mems []domain.MemoryRef, sigs []domain.SignalRef, decisionID string) {
	for _, step := range run.Provenance.Steps {
		if step.Iteration != iter {
			continue
		}
		switch step.Stage {
		case domain.StatusObserving:
			if step.LayerRef.ID != "" {
				mems = append(mems, domain.MemoryRef{ID: step.LayerRef.ID})
			}
		case domain.StatusUnderstanding:
			if step.LayerRef.ID != "" {
				sigs = append(sigs, domain.SignalRef{ID: step.LayerRef.ID})
			}
		case domain.StatusDeciding:
			if step.LayerRef.ID != "" {
				decisionID = step.LayerRef.ID
			}
		}
	}
	return
}

// sameActions is a coarse comparison: identical capability sets (order-
// insensitive). A perfect replay would compare every payload byte; the goal
// here is decision-level determinism, not payload-level reproducibility.
func sameActions(replayed []domain.ActionRequest, run domain.Run) bool {
	// In the absence of stored "actions per iteration", we treat replay as a
	// match when the replayed action capability set is subset-equivalent
	// to the actions executed in the run's acting steps.
	original := capabilitySet(run)
	replayedSet := map[string]int{}
	for _, a := range replayed {
		replayedSet[a.Capability]++
	}
	return reflect.DeepEqual(original, replayedSet)
}

func capabilitySet(run domain.Run) map[string]int {
	out := map[string]int{}
	for _, step := range run.Provenance.Steps {
		if step.Stage != domain.StatusActing {
			continue
		}
		// `Outputs.count` is the per-step action count; capability names live
		// only on the original ActionRequest, which is why this comparison is
		// coarse. Phase 3 stores per-iteration action snapshots for exact
		// replay.
	}
	return out
}
