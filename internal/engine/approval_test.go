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

func gateFixture(t *testing.T) (*engine.Engine, ports.Repos, *fake.Praxis, *fake.Nous) {
	t.Helper()
	repos := memory.New()
	registry := intent.New(repos.IntentTypes)
	if _, err := registry.RegisterBuiltins(context.Background()); err != nil {
		t.Fatalf("builtins: %v", err)
	}
	mnemos := &fake.Mnemos{}
	chronos := &fake.Chronos{}
	nous := &fake.Nous{}
	praxis := &fake.Praxis{Result: domain.ActionResult{Status: "succeeded"}}
	auditor := audit.New(repos.Audit, nil)
	eng := engine.New(engine.Config{EnableApprovalGate: true},
		ports.Layers{Mnemos: mnemos, Chronos: chronos, Nous: nous, Praxis: praxis},
		repos, registry, auditor, nil)
	return eng, repos, praxis, nous
}

func gateRun() domain.Run {
	return domain.Run{
		ID:        "run-gate",
		Intent:    domain.Intent{Type: "remediate", Subject: "x", Payload: map[string]any{"subject": "x"}},
		Session:   domain.SessionRef{ID: "s-1"},
		Caller:    domain.CallerRef{Type: "agent", ID: "a-1"},
		Status:    domain.StatusPending,
		Goal:      domain.Goal{MaxIterations: 1, Description: "test", Criteria: []domain.GoalCriterion{{Kind: "action_succeeded"}}},
		StartedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func TestEngine_GateSuspendsAndPersistsPendingDecision(t *testing.T) {
	eng, repos, praxis, nous := gateFixture(t)
	ctx := context.Background()
	run := gateRun()
	_ = repos.Runs.Save(ctx, run)
	nous.ScriptedDecision = domain.DecisionRef{
		ID:               "d-1",
		RequiresApproval: true,
		Actions:          []domain.ActionRequest{{ID: "a-1", Capability: "rollout_restart"}},
	}

	got, err := eng.Run(ctx, run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Status != domain.StatusAwaitingApproval {
		t.Fatalf("status=%s", got.Status)
	}
	if len(praxis.Calls) != 0 {
		t.Fatal("praxis must not be called while gated")
	}
	stored, err := repos.Runs.Get(ctx, "run-gate")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.PendingDecision == nil || stored.PendingDecision.ID != "d-1" {
		t.Fatalf("pending_decision=%+v", stored.PendingDecision)
	}
	pending, err := repos.Approvals.Pending(ctx, "run-gate")
	if err != nil {
		t.Fatalf("approvals.pending: %v", err)
	}
	if pending.DecisionID != "d-1" {
		t.Fatalf("approval decision=%s", pending.DecisionID)
	}
}

func TestEngine_ResumeRequiresApproval(t *testing.T) {
	eng, repos, _, nous := gateFixture(t)
	ctx := context.Background()
	run := gateRun()
	_ = repos.Runs.Save(ctx, run)
	nous.ScriptedDecision = domain.DecisionRef{
		ID: "d-1", RequiresApproval: true,
		Actions: []domain.ActionRequest{{ID: "a-1", Capability: "rollout_restart"}},
	}
	if _, err := eng.Run(ctx, run); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Resume without approve resolution: still awaiting_approval.
	got, err := eng.Resume(ctx, "run-gate")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got.Status != domain.StatusAwaitingApproval {
		t.Fatalf("status=%s want awaiting_approval", got.Status)
	}
}

func TestEngine_ResumeAfterApprovalRunsAct(t *testing.T) {
	eng, repos, praxis, nous := gateFixture(t)
	ctx := context.Background()
	run := gateRun()
	_ = repos.Runs.Save(ctx, run)
	nous.ScriptedDecision = domain.DecisionRef{
		ID: "d-1", RequiresApproval: true,
		Actions: []domain.ActionRequest{{ID: "a-1", Capability: "rollout_restart"}},
	}
	if _, err := eng.Run(ctx, run); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Resolve the approval, then resume.
	if err := repos.Approvals.Resolve(ctx, "run-gate", domain.ApprovalDecision{
		Decision: "approve", Resolver: domain.CallerRef{Type: "user", ID: "u-1"}, ResolvedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, err := eng.Resume(ctx, "run-gate")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got.Status != domain.StatusCompleted {
		t.Fatalf("status=%s want completed", got.Status)
	}
	if len(praxis.Calls) != 1 {
		t.Fatalf("praxis calls=%d want 1", len(praxis.Calls))
	}
}

func TestEngine_ResumeRejectsWrongState(t *testing.T) {
	eng, repos, _, _ := gateFixture(t)
	ctx := context.Background()
	run := gateRun()
	_ = repos.Runs.Save(ctx, run)
	// Run is in pending — Resume must refuse.
	_, err := eng.Resume(ctx, "run-gate")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEngine_HaltPausesInFlightRunsAndDeniesApprovals(t *testing.T) {
	eng, repos, _, nous := gateFixture(t)
	ctx := context.Background()
	r1 := gateRun()
	r1.ID = "r-a"
	r2 := gateRun()
	r2.ID = "r-b"
	r2.Status = domain.StatusObserving
	r3 := gateRun()
	r3.ID = "r-c"
	now := time.Now().UTC()
	r3.Status = domain.StatusCompleted
	r3.CompletedAt = &now
	for _, r := range []domain.Run{r1, r2, r3} {
		_ = repos.Runs.Save(ctx, r)
	}
	// drive r-a to awaiting_approval
	nous.ScriptedDecision = domain.DecisionRef{
		ID: "d-1", RequiresApproval: true,
		Actions: []domain.ActionRequest{{ID: "a-1", Capability: "rollout_restart"}},
	}
	if _, err := eng.Run(ctx, r1); err != nil {
		t.Fatalf("run r1: %v", err)
	}

	affected, err := eng.Halt(ctx, "compliance freeze", domain.CallerRef{Type: "user", ID: "ops"})
	if err != nil {
		t.Fatalf("halt: %v", err)
	}
	if len(affected) < 2 {
		t.Fatalf("affected=%v want at least 2", affected)
	}
	for _, id := range affected {
		got, _ := repos.Runs.Get(ctx, id)
		if got.Status != domain.StatusPaused {
			t.Errorf("run %s status=%s want paused", id, got.Status)
		}
	}
	// terminal r-c stays completed
	got, _ := repos.Runs.Get(ctx, "r-c")
	if got.Status != domain.StatusCompleted {
		t.Errorf("r-c status=%s want completed", got.Status)
	}
	// r-a's pending approval denied
	_, err = repos.Approvals.Pending(ctx, "r-a")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("approval still pending after halt: %v", err)
	}
}

func TestRequiresApproval_PolicyAndDecision(t *testing.T) {
	tests := map[string]struct {
		policy   bool
		decision bool
		actions  int
		want     bool
	}{
		"both false":               {false, false, 0, false},
		"policy true, no actions":  {true, false, 0, false},
		"policy true, has actions": {true, false, 1, true},
		"decision true":            {false, true, 0, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			it := domain.IntentType{Policy: domain.IntentPolicy{RequireApproval: tt.policy}}
			d := domain.DecisionRef{RequiresApproval: tt.decision}
			for i := 0; i < tt.actions; i++ {
				d.Actions = append(d.Actions, domain.ActionRequest{ID: "a"})
			}
			if got := engine.RequiresApprovalForTest(it, d); got != tt.want {
				t.Errorf("got=%v want=%v", got, tt.want)
			}
		})
	}
}
