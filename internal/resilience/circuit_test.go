package resilience_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/fake"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/observability"
	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/resilience"
)

func TestCircuit_OpensAfterFailures(t *testing.T) {
	mnemos := &fake.Mnemos{Err: errors.New("mnemos down")}
	wrapped := resilience.WrapMnemos(mnemos, resilience.CircuitConfig{
		MaxFailures: 3,
		Cooldown:    time.Hour, // long cooldown so probe doesn't open the gate during the test
	})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _ = wrapped.Recall(ctx, domain.MemoryQuery{})
	}
	// Next call should return ErrLayerUnavailable (circuit open).
	_, err := wrapped.Recall(ctx, domain.MemoryQuery{})
	if !errors.Is(err, resilience.ErrLayerUnavailable) {
		t.Fatalf("err=%v want ErrLayerUnavailable", err)
	}
}

func TestCircuit_HealthRegistryReceivesStateChanges(t *testing.T) {
	health := observability.NewHealthRegistry()
	mnemos := &fake.Mnemos{Err: errors.New("mnemos down")}
	wrapped := resilience.WrapMnemos(mnemos, resilience.CircuitConfig{
		MaxFailures: 2,
		Cooldown:    time.Hour,
		Health:      health,
	})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _ = wrapped.Recall(ctx, domain.MemoryQuery{})
	}
	// OnStateChange fires in a goroutine; wait briefly for it to propagate.
	deadline := time.Now().Add(500 * time.Millisecond)
	var mnemosState string
	for time.Now().Before(deadline) {
		for _, l := range health.Snapshot().Layers {
			if l.Layer == "mnemos" {
				mnemosState = l.CircuitState
			}
		}
		if mnemosState == "open" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if mnemosState != "open" {
		t.Fatalf("circuit state=%s want open", mnemosState)
	}
}

func TestWrapLayers_AllPortsCovered(t *testing.T) {
	in := ports.Layers{
		Mnemos:  &fake.Mnemos{},
		Chronos: &fake.Chronos{},
		Nous:    &fake.Nous{ScriptedDecision: domain.DecisionRef{ID: "d-1"}},
		Praxis:  &fake.Praxis{Result: domain.ActionResult{Status: "succeeded"}},
	}
	out := resilience.WrapLayers(in, resilience.CircuitConfig{MaxFailures: 5})
	ctx := context.Background()
	if _, err := out.Mnemos.Recall(ctx, domain.MemoryQuery{}); err != nil {
		t.Fatalf("mnemos: %v", err)
	}
	if _, err := out.Chronos.Signals(ctx, domain.SignalQuery{}); err != nil {
		t.Fatalf("chronos: %v", err)
	}
	if _, err := out.Nous.Decide(ctx, domain.DecisionRequest{}); err != nil {
		t.Fatalf("nous: %v", err)
	}
	if _, err := out.Praxis.Execute(ctx, domain.ActionRequest{ID: "a-1"}); err != nil {
		t.Fatalf("praxis: %v", err)
	}
}

func TestLimiter_AllowAndWait(t *testing.T) {
	lim, err := resilience.NewLimiter(resilience.RateLimitConfig{Rate: 2, Burst: 2, Interval: time.Second})
	if err != nil {
		t.Fatalf("limiter: %v", err)
	}
	caller := domain.CallerRef{Type: "user", ID: "u-1"}
	intent := domain.Intent{Type: "explain"}
	allowed := 0
	for i := 0; i < 10; i++ {
		if lim.Allow(context.Background(), intent, caller) {
			allowed++
		}
	}
	if allowed > 3 {
		t.Fatalf("allowed=%d want at most 3 within burst", allowed)
	}
}

func TestLimiter_KeysIsolateCallers(t *testing.T) {
	lim, _ := resilience.NewLimiter(resilience.RateLimitConfig{Rate: 1, Burst: 1, Interval: time.Hour})
	intent := domain.Intent{Type: "explain"}
	if !lim.Allow(context.Background(), intent, domain.CallerRef{Type: "user", ID: "alice"}) {
		t.Fatal("alice first request denied")
	}
	if !lim.Allow(context.Background(), intent, domain.CallerRef{Type: "user", ID: "bob"}) {
		t.Fatal("bob's bucket should not be drained by alice")
	}
}
