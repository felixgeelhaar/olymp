package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/fake"
	"github.com/felixgeelhaar/olymp/internal/audit"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/engine"
	"github.com/felixgeelhaar/olymp/internal/intent"
	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/store/memory"
)

type fixture struct {
	repos    ports.Repos
	registry *intent.Registry
	logger   *audit.Logger
	mnemos   *fake.Mnemos
	chronos  *fake.Chronos
	nous     *fake.Nous
	praxis   *fake.Praxis
	engine   *engine.Engine
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	repos := memory.New()
	registry := intent.New(repos.IntentTypes)
	if _, err := registry.RegisterBuiltins(context.Background()); err != nil {
		t.Fatalf("builtins: %v", err)
	}
	mnemos := &fake.Mnemos{Memories: []domain.MemoryRef{{ID: "m-1", Confidence: 0.8}}}
	chronos := &fake.Chronos{Signals_: []domain.SignalRef{{ID: "s-1", Pattern: "spike"}}}
	nous := &fake.Nous{}
	praxis := &fake.Praxis{Result: domain.ActionResult{Status: "succeeded"}}
	logger := audit.New(repos.Audit, nil)
	eng := engine.New(engine.Config{}, ports.Layers{
		Mnemos: mnemos, Chronos: chronos, Nous: nous, Praxis: praxis,
	}, repos, registry, logger, nil)
	return &fixture{repos, registry, logger, mnemos, chronos, nous, praxis, eng}
}

func sampleRun(intentName string) domain.Run {
	return domain.Run{
		ID:        "run-1",
		Intent:    domain.Intent{Type: intentName, Subject: "x", Payload: map[string]any{"subject": "x"}},
		Session:   domain.SessionRef{ID: "s-1"},
		Caller:    domain.CallerRef{Type: "agent", ID: "a-1"},
		Status:    domain.StatusPending,
		Goal:      domain.Goal{MaxIterations: 3, Description: "test"},
		StartedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func TestEngine_ExplainHappyPath(t *testing.T) {
	f := newFixture(t)
	run := sampleRun("explain")
	if err := f.repos.Runs.Save(context.Background(), run); err != nil {
		t.Fatalf("save: %v", err)
	}
	f.nous.ScriptedDecision = domain.DecisionRef{ID: "d-1", Rationale: "explain it"}

	got, err := f.engine.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Status != domain.StatusCompleted {
		t.Fatalf("status=%s want completed", got.Status)
	}
	if len(f.praxis.Calls) != 0 {
		t.Fatalf("explain must not call Praxis; calls=%d", len(f.praxis.Calls))
	}
	if len(f.mnemos.Appended) != 0 {
		t.Fatal("explain has no actions; learning stage should have nothing to write")
	}
	// Provenance must include all five stages for one iteration.
	stages := stagesOf(got.Provenance.Steps)
	wantStages := []domain.RunStatus{
		domain.StatusObserving, domain.StatusUnderstanding,
		domain.StatusDeciding, domain.StatusActing, domain.StatusLearning,
	}
	if len(stages) < len(wantStages) {
		t.Fatalf("stages=%v want %v", stages, wantStages)
	}
	for i, s := range wantStages {
		if stages[i] != s {
			t.Errorf("stages[%d]=%s want %s", i, stages[i], s)
		}
	}
}

func TestEngine_RemediateExecutesAndWritesBack(t *testing.T) {
	f := newFixture(t)
	run := sampleRun("remediate")
	run.Goal.Criteria = []domain.GoalCriterion{{Kind: "action_succeeded"}}
	if err := f.repos.Runs.Save(context.Background(), run); err != nil {
		t.Fatalf("save: %v", err)
	}
	f.nous.ScriptedDecision = domain.DecisionRef{
		ID: "d-1",
		Actions: []domain.ActionRequest{
			{ID: "a-1", Capability: "rollout_restart"},
		},
	}

	got, err := f.engine.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Status != domain.StatusCompleted {
		t.Fatalf("status=%s", got.Status)
	}
	if len(f.praxis.Calls) != 1 {
		t.Fatalf("praxis calls=%d want 1", len(f.praxis.Calls))
	}
	call := f.praxis.Calls[0]
	if call.Metadata["run_id"] != "run-1" {
		t.Errorf("action.metadata.run_id=%v", call.Metadata["run_id"])
	}
	if call.Metadata["decision_id"] != "d-1" {
		t.Errorf("action.metadata.decision_id=%v", call.Metadata["decision_id"])
	}
	if call.IdempotencyKey != "a-1" {
		t.Errorf("idempotency_key=%s", call.IdempotencyKey)
	}
	if len(f.mnemos.Appended) != 1 {
		t.Fatalf("outcome events=%d want 1", len(f.mnemos.Appended))
	}
	ev := f.mnemos.Appended[0]
	if ev.Type != "olymp.outcome" || ev.Capability != "rollout_restart" || ev.Status != "succeeded" {
		t.Errorf("outcome=%+v", ev)
	}
}

func TestEngine_ApprovalGateAuditsButDoesNotBlock(t *testing.T) {
	f := newFixture(t)
	run := sampleRun("remediate")
	run.Goal.Criteria = []domain.GoalCriterion{{Kind: "action_succeeded"}}
	_ = f.repos.Runs.Save(context.Background(), run)
	f.nous.ScriptedDecision = domain.DecisionRef{
		ID:               "d-1",
		RequiresApproval: true,
		Actions:          []domain.ActionRequest{{ID: "a-1", Capability: "rollout_restart"}},
	}
	got, err := f.engine.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Status != domain.StatusCompleted {
		t.Fatalf("status=%s", got.Status)
	}
	// Phase 1: approval audited, but not blocking.
	events, _ := f.repos.Audit.ListForRun(context.Background(), "run-1")
	hasApproval := false
	for _, e := range events {
		if e.Kind == audit.KindApprovalRequired {
			hasApproval = true
		}
	}
	if !hasApproval {
		t.Fatal("expected approval_required audit event")
	}
}

func TestEngine_ApprovalGateBlocksWhenEnabled(t *testing.T) {
	f := newFixture(t)
	f.engine = engine.New(engine.Config{EnableApprovalGate: true}, ports.Layers{
		Mnemos: f.mnemos, Chronos: f.chronos, Nous: f.nous, Praxis: f.praxis,
	}, f.repos, f.registry, f.logger, nil)

	run := sampleRun("remediate")
	_ = f.repos.Runs.Save(context.Background(), run)
	f.nous.ScriptedDecision = domain.DecisionRef{
		ID:               "d-1",
		RequiresApproval: true,
		Actions:          []domain.ActionRequest{{ID: "a-1", Capability: "rollout_restart"}},
	}
	got, err := f.engine.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Status != domain.StatusAwaitingApproval {
		t.Fatalf("status=%s want awaiting_approval", got.Status)
	}
	if len(f.praxis.Calls) != 0 {
		t.Fatal("praxis must not be called while gated")
	}
	pending, err := f.repos.Approvals.Pending(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if pending.DecisionID != "d-1" {
		t.Fatalf("pending decision=%s", pending.DecisionID)
	}
}

func TestEngine_MaxIterationsExceeded(t *testing.T) {
	f := newFixture(t)
	run := sampleRun("remediate")
	run.Goal.MaxIterations = 1
	// Force never-satisfied criteria so the loop tries to iterate again.
	run.Goal.Criteria = []domain.GoalCriterion{{Kind: "unknown_kind"}}
	_ = f.repos.Runs.Save(context.Background(), run)
	f.nous.ScriptedDecision = domain.DecisionRef{ID: "d-1"}

	got, err := f.engine.Run(context.Background(), run)
	if err == nil {
		t.Fatal("expected error")
	}
	if got.Status != domain.StatusFailed {
		t.Fatalf("status=%s", got.Status)
	}
	if got.LastError == nil || got.LastError.Code != "max_iterations_exceeded" {
		t.Fatalf("last_error=%+v", got.LastError)
	}
}

func TestEngine_DeadlineExceeded(t *testing.T) {
	f := newFixture(t)
	past := time.Now().Add(-time.Hour)
	run := sampleRun("remediate")
	run.Goal.Deadline = &past
	_ = f.repos.Runs.Save(context.Background(), run)
	f.nous.ScriptedDecision = domain.DecisionRef{ID: "d-1"}

	got, err := f.engine.Run(context.Background(), run)
	if err == nil {
		t.Fatal("expected error")
	}
	if got.LastError == nil || got.LastError.Code != "deadline_exceeded" {
		t.Fatalf("last_error=%+v", got.LastError)
	}
}

func TestEngine_LayerFailureFailsRun(t *testing.T) {
	f := newFixture(t)
	run := sampleRun("explain")
	_ = f.repos.Runs.Save(context.Background(), run)
	f.mnemos.Err = errors.New("mnemos down")
	got, _ := f.engine.Run(context.Background(), run)
	if got.Status != domain.StatusFailed {
		t.Fatalf("status=%s", got.Status)
	}
	if got.LastError == nil || got.LastError.Layer != "mnemos" {
		t.Fatalf("last_error=%+v", got.LastError)
	}
}

func TestSatisfied(t *testing.T) {
	res := []domain.ActionResult{{ActionID: "a-1", Status: "succeeded"}}
	if !engine.Satisfied(domain.Goal{}, domain.DecisionRef{}, res) {
		t.Fatal("empty criteria → always satisfied")
	}
	if !engine.Satisfied(domain.Goal{Criteria: []domain.GoalCriterion{{Kind: "action_succeeded"}}}, domain.DecisionRef{}, res) {
		t.Fatal("action_succeeded should match")
	}
	if engine.Satisfied(domain.Goal{Criteria: []domain.GoalCriterion{{Kind: "unknown"}}}, domain.DecisionRef{}, res) {
		t.Fatal("unknown criterion fails closed")
	}
}

func stagesOf(steps []domain.ProvenanceStep) []domain.RunStatus {
	out := make([]domain.RunStatus, 0, len(steps))
	for _, s := range steps {
		out = append(out, s.Stage)
	}
	return out
}
