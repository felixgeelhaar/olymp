package eventbus_test

import (
	"context"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/eventbus"
)

func TestBus_FanOut(t *testing.T) {
	b := eventbus.New(16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := b.Subscribe(ctx, domain.RunFilter{})
	c := b.Subscribe(ctx, domain.RunFilter{})

	want := domain.RunEvent{RunID: "r-1", Kind: "transitioned", Timestamp: time.Now()}
	b.Publish(want)

	for _, ch := range []<-chan domain.RunEvent{a, c} {
		select {
		case got := <-ch:
			if got.RunID != "r-1" {
				t.Errorf("got=%+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestBus_FilterByRunID(t *testing.T) {
	b := eventbus.New(16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := b.Subscribe(ctx, domain.RunFilter{RunID: "r-2"})
	b.Publish(domain.RunEvent{RunID: "r-1", Kind: "x"})
	b.Publish(domain.RunEvent{RunID: "r-2", Kind: "y"})
	select {
	case got := <-ch:
		if got.RunID != "r-2" {
			t.Errorf("got=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("filter blocked all events")
	}
	select {
	case got := <-ch:
		t.Fatalf("filter let through extra event: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBus_DropsOnOverflow(t *testing.T) {
	b := eventbus.New(2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = b.Subscribe(ctx, domain.RunFilter{}) // never drained
	for i := 0; i < 10; i++ {
		b.Publish(domain.RunEvent{RunID: "r", Kind: "k"})
	}
	if dropped := b.Dropped(); dropped == 0 {
		t.Fatal("expected drops on overflow; dropped=0")
	}
}

func TestBus_UnsubscribeOnCancel(t *testing.T) {
	b := eventbus.New(4)
	ctx, cancel := context.WithCancel(context.Background())
	ch := b.Subscribe(ctx, domain.RunFilter{})
	if got := b.SubscriberCount(); got != 1 {
		t.Fatalf("count=%d want 1", got)
	}
	cancel()
	// drain — channel should close
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				if got := b.SubscriberCount(); got != 0 {
					t.Fatalf("count after cancel=%d want 0", got)
				}
				return
			}
		case <-deadline:
			t.Fatal("channel not closed after cancel")
		}
	}
}
