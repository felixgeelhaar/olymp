package explain_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/fake"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/explain"
	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/store/memory"
)

func TestBuild_AssemblesChain(t *testing.T) {
	repos := memory.New()
	ctx := context.Background()
	now := time.Now().UTC()
	run := domain.Run{
		ID:        "r-1",
		Intent:    domain.Intent{Type: "explain", Subject: "x"},
		Session:   domain.SessionRef{ID: "s"},
		Caller:    domain.CallerRef{Type: "user", ID: "u-1"},
		Status:    domain.StatusCompleted,
		Goal:      domain.Goal{MaxIterations: 1},
		StartedAt: now, UpdatedAt: now, CompletedAt: &now,
	}
	if err := repos.Runs.Save(ctx, run); err != nil {
		t.Fatalf("save: %v", err)
	}
	steps := []domain.ProvenanceStep{
		{Iteration: 0, Stage: domain.StatusObserving, StartedAt: now, CompletedAt: now, LayerRef: domain.LayerRef{Layer: "mnemos", ID: "m-1"}},
		{Iteration: 0, Stage: domain.StatusUnderstanding, StartedAt: now, CompletedAt: now, LayerRef: domain.LayerRef{Layer: "chronos", ID: "s-1"}},
		{Iteration: 0, Stage: domain.StatusDeciding, StartedAt: now, CompletedAt: now, LayerRef: domain.LayerRef{Layer: "nous", ID: "d-1"}},
		{Iteration: 0, Stage: domain.StatusActing, StartedAt: now, CompletedAt: now, LayerRef: domain.LayerRef{Layer: "praxis", ID: "a-1"}},
		{Iteration: 0, Stage: domain.StatusLearning, StartedAt: now, CompletedAt: now, LayerRef: domain.LayerRef{Layer: "mnemos", ID: "ev-1"}},
	}
	for _, s := range steps {
		_ = repos.Runs.AppendProvenance(ctx, run.ID, s)
	}
	layers := ports.Layers{
		Mnemos:  &fake.Mnemos{Index: map[string]domain.MemoryRef{"m-1": {ID: "m-1", Kind: "claim", Confidence: 0.9}}},
		Chronos: &fake.Chronos{Index: map[string]domain.SignalRef{"s-1": {ID: "s-1", Pattern: "spike", Confidence: 0.8}}},
		Nous:    &fake.Nous{ScriptedDecision: domain.DecisionRef{ID: "d-1", Rationale: "looks bad"}},
	}
	chain, err := explain.Build(ctx, repos.Runs, layers, "r-1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if chain.RunID != "r-1" {
		t.Fatalf("run_id=%s", chain.RunID)
	}
	if len(chain.Iterations) != 1 {
		t.Fatalf("iterations=%d want 1", len(chain.Iterations))
	}
	it := chain.Iterations[0]
	if len(it.Memories) != 1 || it.Memories[0].Confidence != 0.9 {
		t.Fatalf("memories=%+v", it.Memories)
	}
	if len(it.Signals) != 1 || it.Signals[0].Kind != "spike" {
		t.Fatalf("signals=%+v", it.Signals)
	}
	if it.Decision == nil || it.Decision.Rationale != "looks bad" {
		t.Fatalf("decision=%+v", it.Decision)
	}
	md := chain.Markdown()
	for _, want := range []string{"Run r-1 — explain", "Memories", "spike", "looks bad"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}
