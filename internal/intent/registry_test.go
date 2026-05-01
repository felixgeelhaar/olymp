package intent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/intent"
	"github.com/felixgeelhaar/olymp/internal/store/memory"
)

func newReg(t *testing.T) *intent.Registry {
	t.Helper()
	repos := memory.New()
	return intent.New(repos.IntentTypes)
}

func TestRegister_RejectsInvalid(t *testing.T) {
	r := newReg(t)
	if err := r.Register(context.Background(), domain.IntentType{}); err == nil {
		t.Fatal("expected error for empty name")
	}
	if err := r.Register(context.Background(), domain.IntentType{Name: "x"}); err == nil {
		t.Fatal("expected error for max_iterations <= 0")
	}
}

func TestRegisterBuiltins(t *testing.T) {
	r := newReg(t)
	names, err := r.RegisterBuiltins(context.Background())
	if err != nil {
		t.Fatalf("builtins: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("names=%v want 2", names)
	}
	all, _ := r.List(context.Background())
	if len(all) != 2 {
		t.Fatalf("listed=%d want 2", len(all))
	}
	explain, err := r.Get(context.Background(), "explain")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !explain.Policy.ReadOnly {
		t.Fatal("explain.read_only lost")
	}
	remediate, _ := r.Get(context.Background(), "remediate")
	if !remediate.Policy.RequireApproval {
		t.Fatal("remediate.require_approval lost")
	}
}

func TestValidate_UnknownType(t *testing.T) {
	r := newReg(t)
	_, err := r.Validate(context.Background(), domain.Intent{Type: "nope"})
	if !errors.Is(err, intent.ErrUnknownType) {
		t.Fatalf("err=%v want ErrUnknownType", err)
	}
}

func TestValidate_RequiredAndTypes(t *testing.T) {
	r := newReg(t)
	_, _ = r.RegisterBuiltins(context.Background())

	// Missing subject.
	if _, err := r.Validate(context.Background(), domain.Intent{Type: "explain"}); err == nil {
		t.Fatal("expected error for missing subject")
	}
	// Wrong type.
	if _, err := r.Validate(context.Background(), domain.Intent{
		Type:    "explain",
		Payload: map[string]any{"subject": 42},
	}); err == nil {
		t.Fatal("expected error for non-string subject")
	}
	// Happy path.
	t1, err := r.Validate(context.Background(), domain.Intent{
		Type:    "explain",
		Payload: map[string]any{"subject": "payments-latency"},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if t1.Name != "explain" {
		t.Fatalf("type=%s", t1.Name)
	}
}

func TestPreparePolicy_FillsDefaults(t *testing.T) {
	tType := domain.IntentType{
		Name: "x",
		Policy: domain.IntentPolicy{
			MaxIterations:   5,
			DefaultDeadline: time.Hour,
		},
	}
	got := intent.PreparePolicy(tType, domain.Goal{})
	if got.MaxIterations != 5 {
		t.Fatalf("max=%d want 5", got.MaxIterations)
	}
	if got.Deadline == nil {
		t.Fatal("deadline not filled")
	}
	// caller-supplied wins
	custom := time.Now().Add(time.Minute)
	got = intent.PreparePolicy(tType, domain.Goal{MaxIterations: 1, Deadline: &custom})
	if got.MaxIterations != 1 {
		t.Fatalf("custom max not respected: %d", got.MaxIterations)
	}
	if !got.Deadline.Equal(custom) {
		t.Fatal("custom deadline not respected")
	}
}

func TestSubjectOf(t *testing.T) {
	if got := intent.SubjectOf(domain.Intent{Subject: "x"}); got != "x" {
		t.Fatalf("got=%q want x", got)
	}
	if got := intent.SubjectOf(domain.Intent{Payload: map[string]any{"subject": " y "}}); got != "y" {
		t.Fatalf("got=%q want y", got)
	}
	if got := intent.SubjectOf(domain.Intent{}); got != "" {
		t.Fatalf("got=%q want empty", got)
	}
}
