// Package engine drives the cognitive loop for one Run end-to-end.
//
// The engine is single-threaded per Run; concurrency happens *across* runs.
// Each call to Run() takes a Run from pending to a terminal state, traversing
// the FSM defined in internal/domain. Failures are wrapped per-layer in
// fortify/retry by the adapters; non-retryable failures transition the Run to
// failed with a structured RunError.
package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/felixgeelhaar/olymp/internal/audit"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/eventbus"
	"github.com/felixgeelhaar/olymp/internal/intent"
	"github.com/felixgeelhaar/olymp/internal/observability"
	"github.com/felixgeelhaar/olymp/internal/outcome"
	"github.com/felixgeelhaar/olymp/internal/ports"

	"github.com/felixgeelhaar/bolt"
	"github.com/google/uuid"
)

// Config tunes the loop engine. Zero-valued Config is valid: the engine uses
// wall-clock time and disables the approval gate.
type Config struct {
	// Now returns the current time. Tests inject a fake clock here.
	Now func() time.Time
	// EnableApprovalGate, when true, raises approvals and *suspends* the loop
	// at awaiting_approval until Steer(approve) is received. When false, the
	// gate event is recorded for auditability but the loop proceeds.
	EnableApprovalGate bool
	// Logger receives structured loop-step + error logs. May be nil.
	Logger *bolt.Logger
	// Health receives per-layer success/failure marks. May be nil.
	Health *observability.HealthRegistry
}

// Engine wires every dependency the loop needs. Construct with New.
type Engine struct {
	cfg      Config
	layers   ports.Layers
	repos    ports.Repos
	registry *intent.Registry
	auditor  *audit.Logger
	writer   *outcome.Writer
	bus      *eventbus.Bus
}

// New returns an Engine. All dependencies are required; bus may be nil.
func New(cfg Config, layers ports.Layers, repos ports.Repos, registry *intent.Registry, auditor *audit.Logger, bus *eventbus.Bus) *Engine {
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Engine{
		cfg: cfg, layers: layers, repos: repos, registry: registry, auditor: auditor, bus: bus,
		writer: outcome.New(layers.Mnemos, auditor, outcome.Config{}),
	}
}

func (e *Engine) publish(ev domain.RunEvent) {
	if e.bus == nil {
		return
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = e.cfg.Now()
	}
	e.bus.Publish(ev)
}

// Run drives a Run from its current Status to a terminal (or paused-at-
// awaiting_approval) state, persisting state at every transition. When the
// returned run.Status is `awaiting_approval`, the engine has suspended at the
// gate; resume by calling Run again *after* Steer(approve) marks the
// approval resolved.
func (e *Engine) Run(ctx context.Context, run domain.Run) (domain.Run, error) {
	if err := run.Validate(); err != nil {
		return run, err
	}
	intentType, err := e.resolveIntentType(ctx, run)
	if err != nil {
		return e.fail(ctx, run, &domain.RunError{
			Code: "unknown_intent", Message: err.Error(), Layer: "olymp",
		})
	}

	for {
		if err := ctx.Err(); err != nil {
			return e.fail(ctx, run, &domain.RunError{Code: "cancelled", Message: err.Error(), Layer: "olymp"})
		}
		if run.Iteration >= run.Goal.MaxIterations {
			return e.fail(ctx, run, &domain.RunError{
				Code: "max_iterations_exceeded", Layer: "olymp",
				Message: fmt.Sprintf("reached max_iterations=%d", run.Goal.MaxIterations),
			})
		}
		if run.Goal.Deadline != nil && e.cfg.Now().After(*run.Goal.Deadline) {
			return e.fail(ctx, run, &domain.RunError{Code: "deadline_exceeded", Layer: "olymp"})
		}

		var decision domain.DecisionRef

		// Resume path: Run was previously suspended at awaiting_approval and
		// is being re-driven. Use the persisted decision; require an
		// approve resolution on the approval row.
		if run.Status == domain.StatusAwaitingApproval && run.PendingDecision != nil {
			ok, err := e.approvalGranted(ctx, run.ID)
			if err != nil {
				return e.fail(ctx, run, &domain.RunError{Code: "approval_lookup", Message: err.Error(), Layer: "olymp"})
			}
			if !ok {
				// Approval not resolved (or denied) — leave run in
				// awaiting_approval and return without progress. Caller
				// either keeps waiting or denies → cancels.
				return run, nil
			}
			decision = *run.PendingDecision
			run.PendingDecision = nil
			_ = e.repos.Runs.Save(ctx, run)
		} else {
			// Fresh pre-decision flow.
			mems, runRef, err := e.stage(ctx, &run, domain.StatusObserving, "mnemos", "recall", func(ctx context.Context, step *domain.ProvenanceStep) (any, error) {
				return e.layers.Mnemos.Recall(ctx, domain.MemoryQuery{
					RunID: run.ID, Goal: run.Goal, Session: run.Session,
				})
			})
			if err != nil {
				return e.fail(ctx, run, runRef)
			}
			memories, _ := mems.([]domain.MemoryRef)

			sigs, runRef, err := e.stage(ctx, &run, domain.StatusUnderstanding, "chronos", "signals", func(ctx context.Context, step *domain.ProvenanceStep) (any, error) {
				return e.layers.Chronos.Signals(ctx, domain.SignalQuery{
					RunID: run.ID, Goal: run.Goal, Session: run.Session,
				})
			})
			if err != nil {
				return e.fail(ctx, run, runRef)
			}
			signals, _ := sigs.([]domain.SignalRef)

			decRaw, runRef, err := e.stage(ctx, &run, domain.StatusDeciding, "nous", "decide", func(ctx context.Context, step *domain.ProvenanceStep) (any, error) {
				return e.layers.Nous.Decide(ctx, domain.DecisionRequest{
					RunID: run.ID, Goal: run.Goal,
					Memories: memories, Signals: signals,
					History: run.Provenance,
				})
			})
			if err != nil {
				return e.fail(ctx, run, runRef)
			}
			decision, _ = decRaw.(domain.DecisionRef)

			// Approval gate. Honour decision.RequiresApproval AND policy.RequireApproval.
			if requiresApproval(intentType, decision) {
				if err := e.transition(ctx, &run, domain.StatusAwaitingApproval); err != nil {
					return e.fail(ctx, run, &domain.RunError{Code: "transition", Message: err.Error()})
				}
				if e.auditor != nil {
					_ = e.auditor.ApprovalRequired(ctx, run.ID, decision.ID, intentType.Description)
				}
				if e.cfg.EnableApprovalGate {
					// Persist pending decision + raise approval. Caller
					// resumes this run after Steer(approve).
					run.PendingDecision = &decision
					if e.repos.Approvals != nil {
						_ = e.repos.Approvals.Raise(ctx, run.ID, domain.ApprovalRequest{
							RunID: run.ID, DecisionID: decision.ID, RequiredAt: e.cfg.Now(),
							Reason: intentType.Description,
						})
					}
					_ = e.repos.Runs.Save(ctx, run)
					e.publish(domain.RunEvent{
						RunID: run.ID, Iteration: run.Iteration, Stage: domain.StatusAwaitingApproval,
						Kind:    "approval_required",
						Payload: map[string]any{"decision_id": decision.ID},
					})
					return run, nil
				}
			}
		}

		// Acting (always, even when read-only or no actions — record a step).
		var results []domain.ActionResult
		var memoryIDs, signalIDs []string
		// memory/signal IDs come from the provenance steps for this iteration
		memoryIDs, signalIDs = idsFromProvenance(run, run.Iteration)

		if !intentType.Policy.ReadOnly && len(decision.Actions) > 0 {
			out, runRef, err := e.stage(ctx, &run, domain.StatusActing, "praxis", "execute", func(ctx context.Context, step *domain.ProvenanceStep) (any, error) {
				rs := make([]domain.ActionResult, 0, len(decision.Actions))
				caps := make([]map[string]any, 0, len(decision.Actions))
				for _, a := range decision.Actions {
					a = withRunMetadata(a, run, decision.ID)
					res, err := e.layers.Praxis.Execute(ctx, a)
					if err != nil {
						return nil, err
					}
					rs = append(rs, res)
					caps = append(caps, map[string]any{
						"id": res.ActionID, "capability": a.Capability, "status": res.Status,
					})
					if res.Status == "failed" && !a.Retryable {
						return rs, fmt.Errorf("action %s failed", res.ActionID)
					}
				}
				if step.Outputs == nil {
					step.Outputs = map[string]any{}
				}
				step.Outputs["actions"] = caps
				return rs, nil
			})
			if err != nil {
				return e.fail(ctx, run, runRef)
			}
			results, _ = out.([]domain.ActionResult)
		} else {
			if err := e.transition(ctx, &run, domain.StatusActing); err != nil {
				return e.fail(ctx, run, &domain.RunError{Code: "transition", Message: err.Error()})
			}
			now := e.cfg.Now()
			run.AppendProvenance(domain.ProvenanceStep{
				Iteration:   run.Iteration,
				Stage:       domain.StatusActing,
				StartedAt:   now,
				CompletedAt: now,
				LayerRef:    domain.LayerRef{Layer: "praxis"},
				Outputs:     map[string]any{"skipped": true, "reason": readOnlyOrEmpty(intentType, decision)},
			})
			_ = e.repos.Runs.Save(ctx, run)
		}

		// Learning — write outcome events back to Mnemos.
		_, runRef, err := e.stage(ctx, &run, domain.StatusLearning, "mnemos", "append", func(ctx context.Context, step *domain.ProvenanceStep) (any, error) {
			for _, r := range results {
				ev := domain.OutcomeEvent{
					Type:       "olymp.outcome",
					RunID:      run.ID,
					Iteration:  run.Iteration,
					Intent:     run.Intent,
					ActionID:   r.ActionID,
					Capability: capabilityOf(decision, r.ActionID),
					Status:     r.Status,
					DecisionID: decision.ID,
					MemoryRefs: memoryIDs,
					SignalRefs: signalIDs,
					Timestamp:  e.cfg.Now(),
				}
				if err := e.writer.Write(ctx, ev); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
		if err != nil {
			return e.fail(ctx, run, runRef)
		}

		if Satisfied(run.Goal, decision, results) {
			if err := e.transition(ctx, &run, domain.StatusCompleted); err != nil {
				return e.fail(ctx, run, &domain.RunError{Code: "transition", Message: err.Error()})
			}
			if err := run.Complete(domain.StatusCompleted, e.cfg.Now()); err == nil {
				_ = e.repos.Runs.Save(ctx, run)
			}
			return run, nil
		}

		run.Iteration++
		if err := e.transition(ctx, &run, domain.StatusObserving); err != nil {
			return e.fail(ctx, run, &domain.RunError{Code: "transition", Message: err.Error()})
		}
	}
}

// Resume re-drives a Run that was suspended at awaiting_approval. It is a
// thin wrapper around Run that loads the run from storage; callers may use
// either form.
func (e *Engine) Resume(ctx context.Context, runID string) (domain.Run, error) {
	run, err := e.repos.Runs.Get(ctx, runID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("resume: %w", err)
	}
	if run.Status != domain.StatusAwaitingApproval {
		return run, fmt.Errorf("resume: run %s is %s, not awaiting_approval", runID, run.Status)
	}
	if run.PendingDecision == nil {
		return run, errors.New("resume: pending decision is missing")
	}
	return e.Run(ctx, run)
}

// Halt is the runtime kill-switch. It transitions every non-terminal run to
// paused and denies pending approvals. Returns the IDs of affected runs.
func (e *Engine) Halt(ctx context.Context, reason string, by domain.CallerRef) ([]string, error) {
	runs, err := e.repos.Runs.List(ctx, domain.RunFilter{})
	if err != nil {
		return nil, fmt.Errorf("halt: list runs: %w", err)
	}
	var affected []string
	for _, run := range runs {
		if run.Status.IsTerminal() || run.Status == domain.StatusPaused {
			continue
		}
		if err := e.repos.Runs.UpdateStatus(ctx, run.ID, domain.StatusPaused); err != nil {
			continue
		}
		if e.auditor != nil {
			_ = e.auditor.Steered(ctx, run.ID, domain.SteerCommand{Kind: "pause", Reason: reason, Caller: by})
		}
		// Deny outstanding approvals so resume is impossible without explicit re-approval.
		if e.repos.Approvals != nil {
			if pending, err := e.repos.Approvals.Pending(ctx, run.ID); err == nil && pending != nil {
				_ = e.repos.Approvals.Resolve(ctx, run.ID, domain.ApprovalDecision{
					Decision: "deny", Reason: "halted: " + reason,
					Resolver: by, ResolvedAt: e.cfg.Now(),
				})
			}
		}
		affected = append(affected, run.ID)
	}
	return affected, nil
}

// approvalGranted returns true iff the run has no outstanding approval
// (i.e. it has been resolved with "approve").
func (e *Engine) approvalGranted(ctx context.Context, runID string) (bool, error) {
	if e.repos.Approvals == nil {
		// No approval store → treat as auto-approved (Phase 1 behaviour).
		return true, nil
	}
	_, err := e.repos.Approvals.Pending(ctx, runID)
	if err == nil {
		// Pending approval still on file → not yet granted.
		return false, nil
	}
	if errors.Is(err, ports.ErrNotFound) {
		return true, nil
	}
	return false, err
}

func (e *Engine) resolveIntentType(ctx context.Context, run domain.Run) (domain.IntentType, error) {
	if e.registry == nil {
		return domain.IntentType{}, nil
	}
	return e.registry.Get(ctx, run.Intent.Type)
}

func requiresApproval(t domain.IntentType, d domain.DecisionRef) bool {
	if d.RequiresApproval {
		return true
	}
	if t.Policy.RequireApproval && len(d.Actions) > 0 {
		return true
	}
	return false
}

// idsFromProvenance pulls memory + signal IDs out of the most recent
// observing/understanding steps for the given iteration.
func idsFromProvenance(run domain.Run, iteration int) (memoryIDs, signalIDs []string) {
	for _, step := range run.Provenance.Steps {
		if step.Iteration != iteration {
			continue
		}
		if step.Stage == domain.StatusObserving && step.LayerRef.ID != "" {
			memoryIDs = append(memoryIDs, step.LayerRef.ID)
		}
		if step.Stage == domain.StatusUnderstanding && step.LayerRef.ID != "" {
			signalIDs = append(signalIDs, step.LayerRef.ID)
		}
	}
	return
}

// stage runs one loop stage end-to-end: transition, invoke layer, append a
// provenance step, persist, and audit. Returns the layer's raw output.
func (e *Engine) stage(
	ctx context.Context,
	run *domain.Run,
	to domain.RunStatus,
	layer, op string,
	fn func(ctx context.Context, step *domain.ProvenanceStep) (any, error),
) (any, *domain.RunError, error) {
	if err := e.transition(ctx, run, to); err != nil {
		return nil, &domain.RunError{Code: "transition", Message: err.Error(), Layer: "olymp"}, err
	}
	step := domain.ProvenanceStep{
		Iteration: run.Iteration,
		Stage:     to,
		StartedAt: e.cfg.Now(),
		LayerRef:  domain.LayerRef{Layer: layer},
	}
	out, callErr := fn(ctx, &step)
	step.CompletedAt = e.cfg.Now()
	// Preserve any custom keys fn set on step.Outputs; merge log fields onto
	// it so per-stage detail (e.g. acting stage's per-action capability list)
	// survives the standard summary write.
	logFields := outputsForLog(layer, op, out)
	if step.Outputs == nil {
		step.Outputs = map[string]any{}
	}
	for k, v := range logFields {
		if _, exists := step.Outputs[k]; !exists {
			step.Outputs[k] = v
		}
	}
	if callErr != nil {
		step.Error = &domain.RunError{
			Code: "layer_call_failed", Message: callErr.Error(), Layer: layer,
		}
		run.AppendProvenance(step)
		_ = e.repos.Runs.Save(ctx, *run)
		if e.cfg.Health != nil {
			e.cfg.Health.MarkError(layer, step.CompletedAt, callErr)
		}
		observability.LogError(e.cfg.Logger, "layer_call_failed", run.ID, callErr, map[string]any{
			"layer": layer, "op": op, "iteration": run.Iteration,
		})
		runErr := &domain.RunError{Code: "layer_call_failed", Message: callErr.Error(), Layer: layer}
		return nil, runErr, callErr
	}
	if e.cfg.Health != nil {
		e.cfg.Health.MarkSuccess(layer, step.CompletedAt)
	}
	step.LayerRef.ID = primaryID(out)
	run.AppendProvenance(step)
	if err := e.repos.Runs.Save(ctx, *run); err != nil {
		return nil, &domain.RunError{Code: "persist", Message: err.Error(), Layer: "olymp"}, err
	}
	if e.auditor != nil {
		_ = e.auditor.LayerCalled(ctx, run.ID, layer, op, step.LayerRef.ID, run.Iteration)
	}
	observability.LogStep(e.cfg.Logger, "layer_call", observability.LoopFields{
		RunID: run.ID, Iteration: run.Iteration, Stage: to,
		Layer: layer, IntentType: run.Intent.Type,
		CallerType: run.Caller.Type, CallerID: run.Caller.ID,
		DurationMs: step.CompletedAt.Sub(step.StartedAt).Milliseconds(),
	})
	e.publish(domain.RunEvent{
		RunID:     run.ID,
		Iteration: run.Iteration,
		Stage:     to,
		Kind:      "layer_called",
		Payload:   map[string]any{"layer": layer, "op": op, "layer_ref": step.LayerRef.ID},
	})
	return out, nil, nil
}

// transition mutates the run to a new status, persists, and audits.
func (e *Engine) transition(ctx context.Context, run *domain.Run, to domain.RunStatus) error {
	from := run.Status
	run.Status = to
	run.UpdatedAt = e.cfg.Now()
	if err := e.repos.Runs.UpdateStatus(ctx, run.ID, to); err != nil {
		return err
	}
	if e.auditor != nil {
		_ = e.auditor.Transitioned(ctx, run.ID, from, to, run.Iteration)
	}
	e.publish(domain.RunEvent{
		RunID:     run.ID,
		Iteration: run.Iteration,
		Stage:     to,
		Kind:      "transitioned",
		Payload:   map[string]any{"from": string(from), "to": string(to)},
	})
	return nil
}

// fail finalises a Run as failed with the given error and returns it.
func (e *Engine) fail(ctx context.Context, run domain.Run, runErr *domain.RunError) (domain.Run, error) {
	run.LastError = runErr
	run.Status = domain.StatusFailed
	now := e.cfg.Now()
	run.UpdatedAt = now
	t := now
	run.CompletedAt = &t
	_ = e.repos.Runs.UpdateStatus(ctx, run.ID, domain.StatusFailed)
	_ = e.repos.Runs.Save(ctx, run)
	if e.auditor != nil {
		_ = e.auditor.Failed(ctx, run.ID, runErr)
	}
	return run, errors.New(runErr.Code + ": " + runErr.Message)
}

func withRunMetadata(a domain.ActionRequest, run domain.Run, decisionID string) domain.ActionRequest {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.IdempotencyKey == "" {
		a.IdempotencyKey = a.ID
	}
	if a.Caller == (domain.CallerRef{}) {
		a.Caller = run.Caller
	}
	if a.Metadata == nil {
		a.Metadata = map[string]any{}
	}
	a.Metadata["run_id"] = run.ID
	a.Metadata["iteration"] = run.Iteration
	a.Metadata["decision_id"] = decisionID
	return a
}

func capabilityOf(d domain.DecisionRef, actionID string) string {
	for _, a := range d.Actions {
		if a.ID == actionID {
			return a.Capability
		}
	}
	return ""
}

func primaryID(v any) string {
	switch t := v.(type) {
	case domain.DecisionRef:
		return t.ID
	case []domain.ActionResult:
		if len(t) > 0 {
			return t[0].ActionID
		}
	}
	return ""
}

func readOnlyOrEmpty(t domain.IntentType, d domain.DecisionRef) string {
	if t.Policy.ReadOnly {
		return "intent_read_only"
	}
	if len(d.Actions) == 0 {
		return "no_actions"
	}
	return "skipped"
}

func outputsForLog(layer, op string, v any) map[string]any {
	out := map[string]any{"layer": layer, "op": op}
	switch t := v.(type) {
	case []domain.MemoryRef:
		out["count"] = len(t)
	case []domain.SignalRef:
		out["count"] = len(t)
	case domain.DecisionRef:
		out["decision_id"] = t.ID
		out["actions"] = len(t.Actions)
	case []domain.ActionResult:
		out["count"] = len(t)
	}
	return out
}
