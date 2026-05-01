package domain

import (
	"testing"
	"time"
)

func validRun() Run {
	return Run{
		ID:        "run-1",
		Intent:    Intent{Type: "explain"},
		Session:   SessionRef{ID: "sess-1"},
		Caller:    CallerRef{Type: "agent", ID: "a-1"},
		Status:    StatusPending,
		Goal:      Goal{MaxIterations: 5},
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func TestRunValidate_OK(t *testing.T) {
	if err := validRun().Validate(); err != nil {
		t.Fatalf("expected valid run, got %v", err)
	}
}

func TestRunValidate_Errors(t *testing.T) {
	cases := map[string]func(*Run){
		"missing id":         func(r *Run) { r.ID = "" },
		"missing intent":     func(r *Run) { r.Intent.Type = "" },
		"missing session":    func(r *Run) { r.Session.ID = "" },
		"missing caller":     func(r *Run) { r.Caller.ID = "" },
		"missing status":     func(r *Run) { r.Status = "" },
		"unknown status":     func(r *Run) { r.Status = "bogus" },
		"zero iterations":    func(r *Run) { r.Goal.MaxIterations = 0 },
		"negative iteration": func(r *Run) { r.Iteration = -1 },
		"terminal no completed_at": func(r *Run) {
			r.Status = StatusCompleted
			r.CompletedAt = nil
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := validRun()
			mutate(&r)
			if err := r.Validate(); err == nil {
				t.Fatalf("expected error for %s, got nil", name)
			}
		})
	}
}

func TestRunStatus_IsTerminal(t *testing.T) {
	cases := map[RunStatus]bool{
		StatusPending:          false,
		StatusObserving:        false,
		StatusUnderstanding:    false,
		StatusDeciding:         false,
		StatusAwaitingApproval: false,
		StatusActing:           false,
		StatusLearning:         false,
		StatusPaused:           false,
		StatusCompleted:        true,
		StatusFailed:           true,
		StatusCancelled:        true,
	}
	for s, want := range cases {
		if got := s.IsTerminal(); got != want {
			t.Errorf("%s.IsTerminal()=%v want %v", s, got, want)
		}
	}
}

func TestRunComplete(t *testing.T) {
	r := validRun()
	at := time.Now()
	if err := r.Complete(StatusCompleted, at); err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if r.Status != StatusCompleted {
		t.Fatalf("status=%s want completed", r.Status)
	}
	if r.CompletedAt == nil || !r.CompletedAt.Equal(at) {
		t.Fatalf("completed_at not set correctly")
	}
}

func TestRunComplete_RejectsNonTerminal(t *testing.T) {
	r := validRun()
	if err := r.Complete(StatusActing, time.Now()); err == nil {
		t.Fatal("expected error completing with non-terminal status")
	}
}

func TestAppendProvenance(t *testing.T) {
	r := validRun()
	at := time.Now()
	r.AppendProvenance(ProvenanceStep{
		Iteration:   0,
		Stage:       StatusObserving,
		StartedAt:   at.Add(-time.Second),
		CompletedAt: at,
		LayerRef:    LayerRef{Layer: "mnemos"},
	})
	if got := len(r.Provenance.Steps); got != 1 {
		t.Fatalf("steps=%d want 1", got)
	}
	if !r.UpdatedAt.Equal(at) {
		t.Fatalf("updated_at=%v want %v", r.UpdatedAt, at)
	}
}
