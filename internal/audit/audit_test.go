package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/audit"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/store/memory"
)

func sampleRun(id string) domain.Run {
	return domain.Run{
		ID:        id,
		Intent:    domain.Intent{Type: "explain", Subject: "x"},
		Session:   domain.SessionRef{ID: "s-1"},
		Caller:    domain.CallerRef{Type: "agent", ID: "a-1"},
		Status:    domain.StatusPending,
		Goal:      domain.Goal{MaxIterations: 3},
		StartedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func TestLogger_AllKinds(t *testing.T) {
	repos := memory.New()
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	log := audit.New(repos.Audit, func() time.Time { return clock })

	ctx := context.Background()
	run := sampleRun("run-1")

	if err := log.Submitted(ctx, run); err != nil {
		t.Fatalf("submitted: %v", err)
	}
	if err := log.Transitioned(ctx, "run-1", domain.StatusPending, domain.StatusObserving, 0); err != nil {
		t.Fatalf("transitioned: %v", err)
	}
	if err := log.LayerCalled(ctx, "run-1", "mnemos", "recall", "mem-1", 0); err != nil {
		t.Fatalf("layer: %v", err)
	}
	if err := log.ApprovalRequired(ctx, "run-1", "dec-1", "deploy gate"); err != nil {
		t.Fatalf("approval req: %v", err)
	}
	if err := log.ApprovalResolved(ctx, "run-1", domain.ApprovalDecision{Decision: "approve"}); err != nil {
		t.Fatalf("approval res: %v", err)
	}
	if err := log.Steered(ctx, "run-1", domain.SteerCommand{Kind: "pause", Caller: domain.CallerRef{Type: "user", ID: "u-1"}}); err != nil {
		t.Fatalf("steered: %v", err)
	}
	if err := log.OutcomeWritten(ctx, "run-1", domain.OutcomeEvent{
		RunID: "run-1", ActionID: "a-1", Capability: "send_message", Status: "succeeded", Iteration: 0,
	}); err != nil {
		t.Fatalf("outcome: %v", err)
	}
	if err := log.Failed(ctx, "run-1", &domain.RunError{Code: "boom", Message: "kaboom"}); err != nil {
		t.Fatalf("failed: %v", err)
	}

	events, err := repos.Audit.ListForRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 8 {
		t.Fatalf("events=%d want 8", len(events))
	}
	want := []string{
		audit.KindSubmitted, audit.KindTransitioned, audit.KindLayerCalled,
		audit.KindApprovalRequired, audit.KindApprovalResolved,
		audit.KindSteered, audit.KindOutcomeWritten, audit.KindFailed,
	}
	for i, k := range want {
		if events[i].Kind != k {
			t.Errorf("events[%d].Kind=%s want %s", i, events[i].Kind, k)
		}
	}
}

func TestLogger_RejectsEmptyRunID(t *testing.T) {
	repos := memory.New()
	log := audit.New(repos.Audit, nil)
	if err := log.Submitted(context.Background(), domain.Run{}); err == nil {
		t.Fatal("expected error for empty run_id")
	}
}

func TestReconstruct(t *testing.T) {
	repos := memory.New()
	ctx := context.Background()
	run := sampleRun("run-1")
	if err := repos.Runs.Save(ctx, run); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Append steps out of order to verify canonical ordering on read.
	now := time.Now().UTC()
	steps := []domain.ProvenanceStep{
		{Iteration: 1, Stage: domain.StatusObserving, StartedAt: now, CompletedAt: now, LayerRef: domain.LayerRef{Layer: "mnemos"}},
		{Iteration: 0, Stage: domain.StatusActing, StartedAt: now, CompletedAt: now, LayerRef: domain.LayerRef{Layer: "praxis"}},
		{Iteration: 0, Stage: domain.StatusObserving, StartedAt: now, CompletedAt: now, LayerRef: domain.LayerRef{Layer: "mnemos"}},
		{Iteration: 0, Stage: domain.StatusUnderstanding, StartedAt: now, CompletedAt: now, LayerRef: domain.LayerRef{Layer: "chronos"}},
		{Iteration: 0, Stage: domain.StatusDeciding, StartedAt: now, CompletedAt: now, LayerRef: domain.LayerRef{Layer: "nous"}},
		{Iteration: 0, Stage: domain.StatusLearning, StartedAt: now, CompletedAt: now, LayerRef: domain.LayerRef{Layer: "mnemos"}},
	}
	for _, s := range steps {
		if err := repos.Runs.AppendProvenance(ctx, "run-1", s); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	chain, err := audit.Reconstruct(ctx, repos.Runs, "run-1")
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if chain.RunID != "run-1" {
		t.Fatalf("run_id=%s", chain.RunID)
	}
	wantStages := []domain.RunStatus{
		domain.StatusObserving, domain.StatusUnderstanding, domain.StatusDeciding,
		domain.StatusActing, domain.StatusLearning, domain.StatusObserving,
	}
	if len(chain.Steps) != len(wantStages) {
		t.Fatalf("steps=%d want %d", len(chain.Steps), len(wantStages))
	}
	for i, want := range wantStages {
		if chain.Steps[i].Stage != want {
			t.Errorf("steps[%d].Stage=%s want %s", i, chain.Steps[i].Stage, want)
		}
	}
	// First five steps belong to iteration 0; the sixth to iteration 1.
	for i := 0; i < 5; i++ {
		if chain.Steps[i].Iteration != 0 {
			t.Errorf("steps[%d].Iteration=%d want 0", i, chain.Steps[i].Iteration)
		}
	}
	if chain.Steps[5].Iteration != 1 {
		t.Errorf("steps[5].Iteration=%d want 1", chain.Steps[5].Iteration)
	}
}
