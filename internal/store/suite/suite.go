// Package suite is the shared repository contract test suite. Every backend
// MUST pass it. Each backend's test file calls Run(t, factory) where factory
// returns a fresh ports.Repos.
package suite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// Factory builds a fresh, isolated ports.Repos for one test.
type Factory func(t *testing.T) ports.Repos

// Run executes the full contract suite against the factory.
func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("Runs", func(t *testing.T) { testRuns(t, factory) })
	t.Run("Sessions", func(t *testing.T) { testSessions(t, factory) })
	t.Run("IntentTypes", func(t *testing.T) { testIntentTypes(t, factory) })
	t.Run("Audit", func(t *testing.T) { testAudit(t, factory) })
	t.Run("Approvals", func(t *testing.T) { testApprovals(t, factory) })
}

func sampleRun(id string) domain.Run {
	return domain.Run{
		ID:        id,
		Intent:    domain.Intent{Type: "explain", Subject: "payments-latency"},
		Session:   domain.SessionRef{ID: "sess-1"},
		Caller:    domain.CallerRef{Type: "agent", ID: "a-1", Name: "tester"},
		Status:    domain.StatusPending,
		Goal:      domain.Goal{MaxIterations: 5, Description: "explain"},
		StartedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func testRuns(t *testing.T, factory Factory) {
	ctx := context.Background()
	repos := factory(t)

	t.Run("save and get", func(t *testing.T) {
		r := sampleRun("run-1")
		if err := repos.Runs.Save(ctx, r); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := repos.Runs.Get(ctx, "run-1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.ID != r.ID || got.Intent.Type != r.Intent.Type {
			t.Fatalf("round-trip mismatch: %+v", got)
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		_, err := repos.Runs.Get(ctx, "does-not-exist")
		if !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("err=%v want ErrNotFound", err)
		}
	})

	t.Run("update status", func(t *testing.T) {
		if err := repos.Runs.UpdateStatus(ctx, "run-1", domain.StatusObserving); err != nil {
			t.Fatalf("update status: %v", err)
		}
		got, _ := repos.Runs.Get(ctx, "run-1")
		if got.Status != domain.StatusObserving {
			t.Fatalf("status=%s want observing", got.Status)
		}
	})

	t.Run("append provenance", func(t *testing.T) {
		step := domain.ProvenanceStep{
			Iteration:   0,
			Stage:       domain.StatusObserving,
			StartedAt:   time.Now().UTC().Add(-time.Second),
			CompletedAt: time.Now().UTC(),
			LayerRef:    domain.LayerRef{Layer: "mnemos", ID: "mem-1"},
			Inputs:      map[string]any{"q": "latency"},
			Outputs:     map[string]any{"hits": 3},
		}
		if err := repos.Runs.AppendProvenance(ctx, "run-1", step); err != nil {
			t.Fatalf("append provenance: %v", err)
		}
		got, _ := repos.Runs.Get(ctx, "run-1")
		if len(got.Provenance.Steps) != 1 {
			t.Fatalf("steps=%d want 1", len(got.Provenance.Steps))
		}
		if got.Provenance.Steps[0].LayerRef.Layer != "mnemos" {
			t.Fatalf("layer=%s want mnemos", got.Provenance.Steps[0].LayerRef.Layer)
		}
	})

	t.Run("list filters", func(t *testing.T) {
		_ = repos.Runs.Save(ctx, sampleRun("run-2"))
		all, err := repos.Runs.List(ctx, domain.RunFilter{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(all) < 2 {
			t.Fatalf("listed=%d want >=2", len(all))
		}
		filtered, _ := repos.Runs.List(ctx, domain.RunFilter{RunID: "run-2"})
		if len(filtered) != 1 || filtered[0].ID != "run-2" {
			t.Fatalf("filter by id: %+v", filtered)
		}
		byCaller, _ := repos.Runs.List(ctx, domain.RunFilter{CallerType: "agent"})
		if len(byCaller) < 2 {
			t.Fatalf("filter by caller: %d want >=2", len(byCaller))
		}
	})

	t.Run("save rejects invalid run", func(t *testing.T) {
		bad := sampleRun("bad")
		bad.Goal.MaxIterations = 0
		if err := repos.Runs.Save(ctx, bad); err == nil {
			t.Fatal("expected validate error")
		}
	})
}

func testSessions(t *testing.T, factory Factory) {
	ctx := context.Background()
	repos := factory(t)

	s := domain.Session{
		ID:        "sess-1",
		Caller:    domain.CallerRef{Type: "user", ID: "u-1"},
		Metadata:  map[string]any{"source": "cli"},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := repos.Sessions.Upsert(ctx, s); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := repos.Sessions.Get(ctx, "sess-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Caller.Type != "user" {
		t.Fatalf("caller.type=%s want user", got.Caller.Type)
	}

	if _, err := repos.Sessions.Get(ctx, "missing"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}

	all, _ := repos.Sessions.List(ctx, domain.SessionFilter{CallerType: "user"})
	if len(all) != 1 {
		t.Fatalf("list=%d want 1", len(all))
	}
}

func testIntentTypes(t *testing.T, factory Factory) {
	ctx := context.Background()
	repos := factory(t)
	it := domain.IntentType{
		Name:         "explain",
		Description:  "read-only loop",
		Schema:       map[string]any{"type": "object"},
		Policy:       domain.IntentPolicy{MaxIterations: 3, ReadOnly: true},
		RegisteredAt: time.Now().UTC(),
	}
	if err := repos.IntentTypes.Register(ctx, it); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, err := repos.IntentTypes.Get(ctx, "explain")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Policy.ReadOnly {
		t.Fatal("policy.read_only lost on round-trip")
	}
	all, _ := repos.IntentTypes.List(ctx)
	if len(all) != 1 {
		t.Fatalf("list=%d want 1", len(all))
	}
}

func testAudit(t *testing.T, factory Factory) {
	ctx := context.Background()
	repos := factory(t)
	now := time.Now().UTC()
	for i, kind := range []string{"submitted", "transitioned", "outcome_written"} {
		e := domain.AuditEvent{
			ID:        "ev-" + kind,
			RunID:     "run-1",
			Kind:      kind,
			Detail:    map[string]any{"i": i},
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		}
		if err := repos.Audit.Append(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	listed, _ := repos.Audit.ListForRun(ctx, "run-1")
	if len(listed) != 3 {
		t.Fatalf("list=%d want 3", len(listed))
	}
	if listed[0].Kind != "submitted" {
		t.Fatalf("first=%s want submitted (chronological)", listed[0].Kind)
	}
	matches, _ := repos.Audit.Search(ctx, domain.AuditQuery{Kind: "outcome_written"})
	if len(matches) != 1 {
		t.Fatalf("search by kind: %d want 1", len(matches))
	}
	since, _ := repos.Audit.Search(ctx, domain.AuditQuery{RunID: "run-1", Since: now.Add(time.Second)})
	if len(since) != 2 {
		t.Fatalf("search since: %d want 2", len(since))
	}
}

func testApprovals(t *testing.T, factory Factory) {
	ctx := context.Background()
	repos := factory(t)

	if _, err := repos.Approvals.Pending(ctx, "run-1"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
	req := domain.ApprovalRequest{
		RunID:      "run-1",
		DecisionID: "dec-1",
		RequiredAt: time.Now().UTC(),
		Reason:     "deploy gate",
	}
	if err := repos.Approvals.Raise(ctx, "run-1", req); err != nil {
		t.Fatalf("raise: %v", err)
	}
	got, err := repos.Approvals.Pending(ctx, "run-1")
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if got.DecisionID != "dec-1" {
		t.Fatalf("decision_id=%s want dec-1", got.DecisionID)
	}
	resolution := domain.ApprovalDecision{
		Decision:   "approve",
		Resolver:   domain.CallerRef{Type: "user", ID: "u-1"},
		ResolvedAt: time.Now().UTC(),
	}
	if err := repos.Approvals.Resolve(ctx, "run-1", resolution); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := repos.Approvals.Pending(ctx, "run-1"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal("pending should be empty after resolve")
	}
}
