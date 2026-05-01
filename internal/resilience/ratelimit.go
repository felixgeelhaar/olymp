package resilience

import (
	"context"
	"errors"
	"time"

	"github.com/felixgeelhaar/fortify/ratelimit"
	"github.com/felixgeelhaar/olymp/internal/domain"
)

// RateLimitConfig configures the runtime-level limiter.
type RateLimitConfig struct {
	// Rate is the sustained tokens-per-Interval. Defaults to 100/s.
	Rate     int
	Interval time.Duration
	Burst    int
}

// ErrRateLimited is returned when an Allow check fails.
var ErrRateLimited = errors.New("resilience: rate limited")

// Limiter wraps a fortify rate limiter, keying tokens by `intent_type|caller`
// so a noisy intent or caller cannot starve the rest of the system.
type Limiter struct {
	rl ratelimit.RateLimiter
}

// NewLimiter returns a Limiter from the given config.
func NewLimiter(cfg RateLimitConfig) (*Limiter, error) {
	if cfg.Rate <= 0 {
		cfg.Rate = 100
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.Burst <= 0 {
		cfg.Burst = cfg.Rate
	}
	rl := ratelimit.New(&ratelimit.Config{
		Rate:     cfg.Rate,
		Interval: cfg.Interval,
		Burst:    cfg.Burst,
	})
	return &Limiter{rl: rl}, nil
}

// Allow returns true if a token is available for the given intent + caller.
func (l *Limiter) Allow(ctx context.Context, intent domain.Intent, caller domain.CallerRef) bool {
	return l.rl.Allow(ctx, key(intent.Type, caller))
}

// Wait blocks until a token is available or ctx is cancelled.
func (l *Limiter) Wait(ctx context.Context, intent domain.Intent, caller domain.CallerRef) error {
	return l.rl.Wait(ctx, key(intent.Type, caller))
}

func key(intentType string, c domain.CallerRef) string {
	return intentType + "|" + c.Type + "|" + c.ID
}
