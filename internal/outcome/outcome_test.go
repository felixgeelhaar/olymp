package outcome_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/fake"
	"github.com/felixgeelhaar/olymp/internal/audit"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/outcome"
	"github.com/felixgeelhaar/olymp/internal/store/memory"
)

func TestWriter_AppendsAndAudits(t *testing.T) {
	repos := memory.New()
	mnemos := &fake.Mnemos{}
	w := outcome.New(mnemos, audit.New(repos.Audit, nil), outcome.Config{InitialDelay: time.Millisecond})

	ev := domain.OutcomeEvent{
		RunID: "r-1", ActionID: "a-1",
		Type: "olymp.outcome", Capability: "send_message", Status: "succeeded",
	}
	if err := w.Write(context.Background(), ev); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(mnemos.Appended) != 1 {
		t.Fatalf("appended=%d want 1", len(mnemos.Appended))
	}
	events, _ := repos.Audit.ListForRun(context.Background(), "r-1")
	if len(events) != 1 || events[0].Kind != audit.KindOutcomeWritten {
		t.Fatalf("audit=%+v", events)
	}
}

func TestWriter_IsIdempotent(t *testing.T) {
	mnemos := &fake.Mnemos{}
	w := outcome.New(mnemos, nil, outcome.Config{InitialDelay: time.Millisecond})
	ev := domain.OutcomeEvent{RunID: "r-1", ActionID: "a-1", Type: "olymp.outcome"}
	for i := 0; i < 5; i++ {
		if err := w.Write(context.Background(), ev); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if got := len(mnemos.Appended); got != 1 {
		t.Fatalf("appended=%d want 1 (idempotent)", got)
	}
}

func TestWriter_RetriesThenSucceeds(t *testing.T) {
	var calls int32
	flaky := &flakyMnemos{failuresLeft: 2, calls: &calls}
	w := outcome.New(flaky, nil, outcome.Config{MaxAttempts: 5, InitialDelay: time.Millisecond})
	if err := w.Write(context.Background(), domain.OutcomeEvent{RunID: "r-1", ActionID: "a-1"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls=%d want 3 (2 fail + 1 succeed)", got)
	}
}

func TestWriter_PersistentFailureSurfaces(t *testing.T) {
	mnemos := &fake.Mnemos{Err: errors.New("mnemos down")}
	w := outcome.New(mnemos, nil, outcome.Config{MaxAttempts: 2, InitialDelay: time.Millisecond})
	err := w.Write(context.Background(), domain.OutcomeEvent{RunID: "r-1", ActionID: "a-1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWriter_RejectsMissingFields(t *testing.T) {
	w := outcome.New(&fake.Mnemos{}, nil, outcome.Config{})
	if err := w.Write(context.Background(), domain.OutcomeEvent{ActionID: "a"}); err == nil {
		t.Fatal("expected run_id error")
	}
	if err := w.Write(context.Background(), domain.OutcomeEvent{RunID: "r"}); err == nil {
		t.Fatal("expected action_id error")
	}
}

type flakyMnemos struct {
	failuresLeft int32
	calls        *int32
	fake.Mnemos
}

func (f *flakyMnemos) Append(_ context.Context, _ domain.OutcomeEvent) error {
	atomic.AddInt32(f.calls, 1)
	if atomic.LoadInt32(&f.failuresLeft) > 0 {
		atomic.AddInt32(&f.failuresLeft, -1)
		return errors.New("transient")
	}
	return nil
}
