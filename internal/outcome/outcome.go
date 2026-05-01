// Package outcome owns the runtime's writeback path to Mnemos.
//
// Every Praxis terminal action MUST produce one olymp.outcome event in
// Mnemos. The engine calls Writer.Write to perform this; Writer is
// responsible for:
//
//   - Idempotency by (run_id, action_id): a duplicate write is a no-op.
//   - Retry on transient failures via fortify/retry (the underlying
//     MnemosPort adapter already retries 5xx + 429; this Writer adds a
//     bounded outer retry around the full Append call so process restarts
//     don't replay infinitely).
//   - Failure surfacing: persistent failure returns an error so the loop
//     engine transitions the Run to failed (loop integrity is non-negotiable).
package outcome

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/felixgeelhaar/olymp/internal/audit"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"

	"github.com/felixgeelhaar/fortify/retry"
)

// Config tunes the Writer.
type Config struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

// Writer is the runtime's outcome writer. Construct with New.
type Writer struct {
	mnemos  ports.MnemosPort
	auditor *audit.Logger
	cfg     Config
	retrier retry.Retry[struct{}]

	mu   sync.Mutex
	seen map[string]struct{} // (run_id|action_id)
}

// New returns a Writer.
func New(mnemos ports.MnemosPort, auditor *audit.Logger, cfg Config) *Writer {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = 100 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 2 * time.Second
	}
	r := retry.New[struct{}](retry.Config{
		MaxAttempts:   cfg.MaxAttempts,
		InitialDelay:  cfg.InitialDelay,
		MaxDelay:      cfg.MaxDelay,
		Multiplier:    2.0,
		BackoffPolicy: retry.BackoffExponential,
		Jitter:        true,
		IsRetryable:   isRetryable,
	})
	return &Writer{
		mnemos:  mnemos,
		auditor: auditor,
		cfg:     cfg,
		retrier: r,
		seen:    map[string]struct{}{},
	}
}

// Write appends the OutcomeEvent to Mnemos. Duplicate writes (same run_id +
// action_id) are silently ignored. Transient failures are retried; persistent
// failures are returned for the engine to fail the Run.
func (w *Writer) Write(ctx context.Context, ev domain.OutcomeEvent) error {
	if ev.RunID == "" {
		return errors.New("outcome: run_id is required")
	}
	if ev.ActionID == "" {
		return errors.New("outcome: action_id is required")
	}
	key := ev.RunID + "|" + ev.ActionID
	w.mu.Lock()
	if _, ok := w.seen[key]; ok {
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()

	_, err := w.retrier.Do(ctx, func(ctx context.Context) (struct{}, error) {
		if err := w.mnemos.Append(ctx, ev); err != nil {
			return struct{}{}, transient(err)
		}
		return struct{}{}, nil
	})
	if err != nil {
		return fmt.Errorf("outcome: write %s/%s: %w", ev.RunID, ev.ActionID, err)
	}

	w.mu.Lock()
	w.seen[key] = struct{}{}
	w.mu.Unlock()

	if w.auditor != nil {
		_ = w.auditor.OutcomeWritten(ctx, ev.RunID, ev)
	}
	return nil
}

// transientError wraps a retryable error.
type transientError struct{ err error }

func (e transientError) Error() string   { return e.err.Error() }
func (e transientError) Unwrap() error   { return e.err }
func (e transientError) Retryable() bool { return true }

func transient(err error) error { return transientError{err: err} }

func isRetryable(err error) bool {
	var t transientError
	return errors.As(err, &t)
}
