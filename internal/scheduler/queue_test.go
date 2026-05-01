package scheduler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/scheduler"
)

func TestMemoryQueue_FIFOWithPriority(t *testing.T) {
	q := scheduler.NewMemoryQueue()
	ctx := context.Background()
	// low priority first, but high priority comes second yet leases first
	if err := q.Enqueue(ctx, "low", 5); err != nil {
		t.Fatalf("enqueue low: %v", err)
	}
	if err := q.Enqueue(ctx, "high", 1); err != nil {
		t.Fatalf("enqueue high: %v", err)
	}
	first, err := q.Lease(ctx, "w-1", time.Minute)
	if err != nil {
		t.Fatalf("lease 1: %v", err)
	}
	if first.RunID != "high" {
		t.Fatalf("first=%s want high", first.RunID)
	}
	second, err := q.Lease(ctx, "w-1", time.Minute)
	if err != nil {
		t.Fatalf("lease 2: %v", err)
	}
	if second.RunID != "low" {
		t.Fatalf("second=%s want low", second.RunID)
	}
	if _, err := q.Lease(ctx, "w-1", time.Minute); !errors.Is(err, scheduler.ErrNoWork) {
		t.Fatalf("err=%v want ErrNoWork", err)
	}
}

func TestMemoryQueue_EnqueueIdempotent(t *testing.T) {
	q := scheduler.NewMemoryQueue()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = q.Enqueue(ctx, "r-1", 0)
	}
	stats := q.Stats(ctx)
	if stats.Pending != 1 {
		t.Fatalf("pending=%d want 1", stats.Pending)
	}
}

func TestMemoryQueue_LeaseRelease(t *testing.T) {
	q := scheduler.NewMemoryQueue()
	ctx := context.Background()
	_ = q.Enqueue(ctx, "r-1", 0)
	lease, _ := q.Lease(ctx, "w-1", time.Minute)
	if err := q.Release(ctx, lease.RunID, "w-1", false); err != nil {
		t.Fatalf("release: %v", err)
	}
	stats := q.Stats(ctx)
	if stats.Done != 1 {
		t.Fatalf("done=%d want 1", stats.Done)
	}
}

func TestMemoryQueue_FailedReleaseReenqueues(t *testing.T) {
	q := scheduler.NewMemoryQueue()
	ctx := context.Background()
	_ = q.Enqueue(ctx, "r-1", 0)
	lease, _ := q.Lease(ctx, "w-1", time.Minute)
	if err := q.Release(ctx, lease.RunID, "w-1", true); err != nil {
		t.Fatalf("release: %v", err)
	}
	stats := q.Stats(ctx)
	if stats.Pending != 1 {
		t.Fatalf("pending=%d want 1 (re-queued)", stats.Pending)
	}
	again, err := q.Lease(ctx, "w-1", time.Minute)
	if err != nil {
		t.Fatalf("re-lease: %v", err)
	}
	if again.Attempts != 1 {
		t.Fatalf("attempts=%d want 1", again.Attempts)
	}
}

func TestMemoryQueue_RenewExtendsLease(t *testing.T) {
	q := scheduler.NewMemoryQueue()
	ctx := context.Background()
	_ = q.Enqueue(ctx, "r-1", 0)
	_, _ = q.Lease(ctx, "w-1", 50*time.Millisecond)
	if err := q.Renew(ctx, "r-1", "w-1", time.Minute); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if err := q.Renew(ctx, "r-1", "w-2", time.Minute); err == nil {
		t.Fatal("expected error on wrong-holder renew")
	}
}

func TestMemoryQueue_ExpiredLeaseRecovered(t *testing.T) {
	q := scheduler.NewMemoryQueue()
	ctx := context.Background()
	_ = q.Enqueue(ctx, "r-1", 0)
	_, _ = q.Lease(ctx, "w-crashed", time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	lease, err := q.Lease(ctx, "w-recovery", time.Minute)
	if err != nil {
		t.Fatalf("lease after expiry: %v", err)
	}
	if lease.RunID != "r-1" {
		t.Fatalf("got=%s", lease.RunID)
	}
}
