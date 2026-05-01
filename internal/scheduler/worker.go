package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/engine"
	"github.com/felixgeelhaar/olymp/internal/observability"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// WorkerConfig tunes a worker.
type WorkerConfig struct {
	Holder        string        // unique per worker; defaults to a uuid-shape
	LeaseTTL      time.Duration // default 30s
	RenewInterval time.Duration // default LeaseTTL/3
	IdleBackoff   time.Duration // sleep when ErrNoWork; default 1s
	Logger        *bolt.Logger
}

// Worker drains a Queue: lease → load run → engine.Run → release.
type Worker struct {
	queue  Queue
	repos  ports.Repos
	engine *engine.Engine
	cfg    WorkerConfig
}

// NewWorker constructs a Worker.
func NewWorker(queue Queue, repos ports.Repos, eng *engine.Engine, cfg WorkerConfig) *Worker {
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 30 * time.Second
	}
	if cfg.RenewInterval <= 0 {
		cfg.RenewInterval = cfg.LeaseTTL / 3
	}
	if cfg.IdleBackoff <= 0 {
		cfg.IdleBackoff = time.Second
	}
	if cfg.Holder == "" {
		cfg.Holder = fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}
	return &Worker{queue: queue, repos: repos, engine: eng, cfg: cfg}
}

// Run blocks until ctx is cancelled. Drains one run per iteration.
func (w *Worker) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lease, err := w.queue.Lease(ctx, w.cfg.Holder, w.cfg.LeaseTTL)
		if errors.Is(err, ErrNoWork) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(w.cfg.IdleBackoff):
				continue
			}
		}
		if err != nil {
			return fmt.Errorf("worker: lease: %w", err)
		}
		w.process(ctx, lease)
	}
}

func (w *Worker) process(ctx context.Context, lease Lease) {
	run, err := w.repos.Runs.Get(ctx, lease.RunID)
	if err != nil {
		observability.LogError(w.cfg.Logger, "worker_load_run_failed", lease.RunID, err, nil)
		_ = w.queue.Release(ctx, lease.RunID, w.cfg.Holder, true)
		return
	}

	heartbeatCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go w.heartbeat(heartbeatCtx, lease.RunID)

	final, err := w.engine.Run(ctx, run)
	heartbeatStop := err
	cancel()

	switch {
	case heartbeatStop != nil && !final.Status.IsTerminal() && final.Status != domain.StatusAwaitingApproval:
		// Treat as transient; re-enqueue.
		_ = w.queue.Release(ctx, lease.RunID, w.cfg.Holder, true)
		observability.LogError(w.cfg.Logger, "worker_run_retry", lease.RunID, heartbeatStop, nil)
	default:
		_ = w.queue.Release(ctx, lease.RunID, w.cfg.Holder, false)
	}
}

func (w *Worker) heartbeat(ctx context.Context, runID string) {
	ticker := time.NewTicker(w.cfg.RenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.queue.Renew(ctx, runID, w.cfg.Holder, w.cfg.LeaseTTL); err != nil {
				observability.LogError(w.cfg.Logger, "worker_heartbeat_failed", runID, err, nil)
				return
			}
		}
	}
}
