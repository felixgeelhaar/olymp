// Package scheduler decouples Submit from synchronous loop execution.
//
// In Phase 1 the API service ran the engine in-line on Submit. For
// horizontal scale a host runs N stateless workers behind one shared queue:
// Submit enqueues, workers lease + execute + release. This package defines
// the queue port and ships a memory-backed reference + a SQL-friendly
// "claim by update" abstraction that any backend can implement.
package scheduler

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrNoWork is returned by Lease when no run is currently leasable.
var ErrNoWork = errors.New("scheduler: no work")

// Lease represents a worker's claim on a run.
type Lease struct {
	RunID    string
	Holder   string
	Issued   time.Time
	Until    time.Time
	Attempts int
}

// Queue is the port every backend implements.
type Queue interface {
	// Enqueue places runID at the tail of the queue with the given priority
	// (lower = sooner). Idempotent on runID.
	Enqueue(ctx context.Context, runID string, priority int) error

	// Lease claims the next leasable run for `holder`, valid for ttl.
	// Returns ErrNoWork when nothing is available.
	Lease(ctx context.Context, holder string, ttl time.Duration) (Lease, error)

	// Renew extends an active lease. Idempotent — a stale lease (already
	// expired and reclaimed) returns an error.
	Renew(ctx context.Context, runID, holder string, ttl time.Duration) error

	// Release marks the run as completed (success) or returns it to the
	// queue (fail = true → re-enqueue with attempts+1).
	Release(ctx context.Context, runID, holder string, fail bool) error

	// Stats returns counters for /healthz and dashboards.
	Stats(ctx context.Context) Stats
}

// Stats is a snapshot of queue depth.
type Stats struct {
	Pending int
	Leased  int
	Done    int
}

// MemoryQueue is the in-process reference implementation.
type MemoryQueue struct {
	mu      sync.Mutex
	pending []entry
	leased  map[string]Lease
	now     func() time.Time
	done    int
}

type entry struct {
	runID    string
	priority int
	enqueued time.Time
	attempts int
}

// NewMemoryQueue returns an empty memory queue.
func NewMemoryQueue() *MemoryQueue {
	return &MemoryQueue{
		leased: map[string]Lease{},
		now:    func() time.Time { return time.Now().UTC() },
	}
}

func (q *MemoryQueue) Enqueue(_ context.Context, runID string, priority int) error {
	if runID == "" {
		return errors.New("scheduler: run_id is required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range q.pending {
		if e.runID == runID {
			return nil
		}
	}
	if _, leased := q.leased[runID]; leased {
		return nil
	}
	q.pending = append(q.pending, entry{runID: runID, priority: priority, enqueued: q.now()})
	sort.SliceStable(q.pending, func(i, j int) bool {
		if q.pending[i].priority != q.pending[j].priority {
			return q.pending[i].priority < q.pending[j].priority
		}
		return q.pending[i].enqueued.Before(q.pending[j].enqueued)
	})
	return nil
}

func (q *MemoryQueue) Lease(_ context.Context, holder string, ttl time.Duration) (Lease, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
	// Reclaim expired leases first.
	for id, l := range q.leased {
		if now.After(l.Until) {
			q.pending = append(q.pending, entry{
				runID:    id,
				priority: 0,
				enqueued: now,
				attempts: l.Attempts,
			})
			delete(q.leased, id)
		}
	}
	if len(q.pending) == 0 {
		return Lease{}, ErrNoWork
	}
	next := q.pending[0]
	q.pending = q.pending[1:]
	lease := Lease{
		RunID:    next.runID,
		Holder:   holder,
		Issued:   now,
		Until:    now.Add(ttl),
		Attempts: next.attempts,
	}
	q.leased[next.runID] = lease
	return lease, nil
}

func (q *MemoryQueue) Renew(_ context.Context, runID, holder string, ttl time.Duration) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	l, ok := q.leased[runID]
	if !ok {
		return errors.New("scheduler: lease not found")
	}
	if l.Holder != holder {
		return errors.New("scheduler: lease held by a different worker")
	}
	if q.now().After(l.Until) {
		// expired before renewal — caller has lost the run
		return errors.New("scheduler: lease expired")
	}
	l.Until = q.now().Add(ttl)
	q.leased[runID] = l
	return nil
}

func (q *MemoryQueue) Release(_ context.Context, runID, holder string, fail bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	l, ok := q.leased[runID]
	if !ok {
		return errors.New("scheduler: lease not found")
	}
	if l.Holder != holder {
		return errors.New("scheduler: lease held by a different worker")
	}
	delete(q.leased, runID)
	if fail {
		q.pending = append(q.pending, entry{
			runID:    runID,
			priority: 0,
			enqueued: q.now(),
			attempts: l.Attempts + 1,
		})
		return nil
	}
	q.done++
	return nil
}

func (q *MemoryQueue) Stats(_ context.Context) Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return Stats{Pending: len(q.pending), Leased: len(q.leased), Done: q.done}
}
