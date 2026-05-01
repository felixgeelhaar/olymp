package rollback_test

import (
	"context"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/fake"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/rollback"
)

func completedRun() domain.Run {
	now := time.Now().UTC()
	return domain.Run{
		ID:        "r-1",
		Intent:    domain.Intent{Type: "remediate"},
		Session:   domain.SessionRef{ID: "s"},
		Caller:    domain.CallerRef{Type: "user", ID: "u-1"},
		Status:    domain.StatusCompleted,
		Goal:      domain.Goal{MaxIterations: 1},
		StartedAt: now, UpdatedAt: now, CompletedAt: &now,
		Provenance: domain.Provenance{Steps: []domain.ProvenanceStep{
			{Iteration: 0, Stage: domain.StatusActing, StartedAt: now, CompletedAt: now,
				LayerRef: domain.LayerRef{Layer: "praxis"},
				Outputs: map[string]any{
					"actions": []any{
						map[string]any{"id": "a-1", "capability": "rollout_restart", "status": "succeeded"},
					},
				}},
		}},
	}
}

func TestCompensator_RollbacksOnRegression(t *testing.T) {
	chronos := &fake.Chronos{Signals_: []domain.SignalRef{
		{ID: "s-1", Pattern: "spike", Confidence: 0.9},
	}}
	mnemos := &fake.Mnemos{}
	praxis := &fake.Praxis{Result: domain.ActionResult{Status: "succeeded"}}
	c := rollback.New(chronos, praxis, mnemos, nil, rollback.CompensateMap{
		"rollout_restart": "rollout_undo",
	})
	results, err := c.Run(context.Background(), completedRun())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) != 1 || results[0].Status != "succeeded" {
		t.Fatalf("results=%+v", results)
	}
	if len(praxis.Calls) != 1 || praxis.Calls[0].Capability != "rollout_undo" {
		t.Fatalf("praxis calls=%+v", praxis.Calls)
	}
	if len(mnemos.Appended) != 1 || mnemos.Appended[0].Type != "olymp.rollback" {
		t.Fatalf("mnemos appended=%+v", mnemos.Appended)
	}
}

func TestCompensator_NoActionWhenSignalsClean(t *testing.T) {
	chronos := &fake.Chronos{Signals_: []domain.SignalRef{
		{ID: "s-1", Pattern: "trend", Confidence: 0.9}, // not regression
	}}
	c := rollback.New(chronos, &fake.Praxis{}, &fake.Mnemos{}, nil, rollback.CompensateMap{
		"rollout_restart": "rollout_undo",
	})
	results, err := c.Run(context.Background(), completedRun())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results=%+v", results)
	}
}

func TestCompensator_SkipsNonTerminalRuns(t *testing.T) {
	run := completedRun()
	run.Status = domain.StatusActing
	c := rollback.New(&fake.Chronos{}, &fake.Praxis{}, &fake.Mnemos{}, nil, nil)
	results, err := c.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if results != nil {
		t.Fatalf("results=%+v want nil", results)
	}
}

func TestDefaultRegressionPredicate(t *testing.T) {
	tests := map[string]struct {
		s    domain.SignalRef
		want bool
	}{
		"spike high":   {domain.SignalRef{Pattern: "spike", Confidence: 0.8}, true},
		"drop high":    {domain.SignalRef{Pattern: "drop", Confidence: 0.9}, true},
		"spike low":    {domain.SignalRef{Pattern: "spike", Confidence: 0.5}, false},
		"trend":        {domain.SignalRef{Pattern: "trend", Confidence: 0.9}, false},
		"anomaly high": {domain.SignalRef{Pattern: "anomaly", Confidence: 0.95}, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := rollback.DefaultRegressionPredicate(tt.s); got != tt.want {
				t.Errorf("got=%v want=%v", got, tt.want)
			}
		})
	}
}
