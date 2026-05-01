// Package resilience hardens the runtime's outer surface: per-layer circuit
// breakers, rate limits per intent + caller, and a deterministic replay
// utility for decision validation.
package resilience

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/observability"
	"github.com/felixgeelhaar/olymp/internal/ports"

	"github.com/felixgeelhaar/fortify/circuitbreaker"
	"github.com/felixgeelhaar/fortify/ferrors"
)

// CircuitConfig is the per-layer breaker configuration. Defaults are tuned
// for the cognitive stack: open after 5 consecutive failures, hold open for
// 30 s, allow 1 probe in half-open.
type CircuitConfig struct {
	MaxFailures uint32
	Cooldown    time.Duration
	MaxRequests uint32
	Health      *observability.HealthRegistry
}

func (c CircuitConfig) cb(layer string) circuitbreaker.Config {
	maxF := c.MaxFailures
	if maxF == 0 {
		maxF = 5
	}
	cd := c.Cooldown
	if cd == 0 {
		cd = 30 * time.Second
	}
	mr := c.MaxRequests
	if mr == 0 {
		mr = 1
	}
	return circuitbreaker.Config{
		MaxRequests: mr,
		Interval:    60 * time.Second,
		Timeout:     cd,
		ReadyToTrip: func(counts circuitbreaker.Counts) bool {
			return counts.ConsecutiveFailures >= maxF
		},
		OnStateChange: func(from, to circuitbreaker.State) {
			if c.Health == nil {
				return
			}
			c.Health.SetCircuitState(layer, to.String())
		},
	}
}

// ErrLayerUnavailable is returned by every wrapped port when its breaker is
// open. Engine maps it to RunError{Code: "layer_unavailable"}.
var ErrLayerUnavailable = errors.New("resilience: layer unavailable (circuit open)")

// WrapMnemos returns a ports.MnemosPort wrapped in a circuit breaker.
func WrapMnemos(p ports.MnemosPort, cfg CircuitConfig) ports.MnemosPort {
	cb := circuitbreaker.New[any](cfg.cb("mnemos"))
	return &cbMnemos{p: p, cb: cb}
}

type cbMnemos struct {
	p  ports.MnemosPort
	cb circuitbreaker.CircuitBreaker[any]
}

func (c *cbMnemos) Recall(ctx context.Context, q domain.MemoryQuery) ([]domain.MemoryRef, error) {
	out, err := c.cb.Execute(ctx, func(ctx context.Context) (any, error) {
		return c.p.Recall(ctx, q)
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return out.([]domain.MemoryRef), nil
}

func (c *cbMnemos) Append(ctx context.Context, e domain.OutcomeEvent) error {
	_, err := c.cb.Execute(ctx, func(ctx context.Context) (any, error) {
		return nil, c.p.Append(ctx, e)
	})
	return mapErr(err)
}

func (c *cbMnemos) Get(ctx context.Context, id string) (domain.MemoryRef, error) {
	out, err := c.cb.Execute(ctx, func(ctx context.Context) (any, error) {
		return c.p.Get(ctx, id)
	})
	if err != nil {
		return domain.MemoryRef{}, mapErr(err)
	}
	return out.(domain.MemoryRef), nil
}

// WrapChronos wraps a ports.ChronosPort.
func WrapChronos(p ports.ChronosPort, cfg CircuitConfig) ports.ChronosPort {
	cb := circuitbreaker.New[any](cfg.cb("chronos"))
	return &cbChronos{p: p, cb: cb}
}

type cbChronos struct {
	p  ports.ChronosPort
	cb circuitbreaker.CircuitBreaker[any]
}

func (c *cbChronos) Signals(ctx context.Context, q domain.SignalQuery) ([]domain.SignalRef, error) {
	out, err := c.cb.Execute(ctx, func(ctx context.Context) (any, error) {
		return c.p.Signals(ctx, q)
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return out.([]domain.SignalRef), nil
}

func (c *cbChronos) Get(ctx context.Context, id string) (domain.SignalRef, error) {
	out, err := c.cb.Execute(ctx, func(ctx context.Context) (any, error) {
		return c.p.Get(ctx, id)
	})
	if err != nil {
		return domain.SignalRef{}, mapErr(err)
	}
	return out.(domain.SignalRef), nil
}

// WrapNous wraps a ports.NousPort.
func WrapNous(p ports.NousPort, cfg CircuitConfig) ports.NousPort {
	cb := circuitbreaker.New[any](cfg.cb("nous"))
	return &cbNous{p: p, cb: cb}
}

type cbNous struct {
	p  ports.NousPort
	cb circuitbreaker.CircuitBreaker[any]
}

func (c *cbNous) Decide(ctx context.Context, in domain.DecisionRequest) (domain.DecisionRef, error) {
	out, err := c.cb.Execute(ctx, func(ctx context.Context) (any, error) {
		return c.p.Decide(ctx, in)
	})
	if err != nil {
		return domain.DecisionRef{}, mapErr(err)
	}
	return out.(domain.DecisionRef), nil
}

func (c *cbNous) Get(ctx context.Context, id string) (domain.DecisionRef, error) {
	out, err := c.cb.Execute(ctx, func(ctx context.Context) (any, error) {
		return c.p.Get(ctx, id)
	})
	if err != nil {
		return domain.DecisionRef{}, mapErr(err)
	}
	return out.(domain.DecisionRef), nil
}

// WrapPraxis wraps a ports.PraxisPort.
func WrapPraxis(p ports.PraxisPort, cfg CircuitConfig) ports.PraxisPort {
	cb := circuitbreaker.New[any](cfg.cb("praxis"))
	return &cbPraxis{p: p, cb: cb}
}

type cbPraxis struct {
	p  ports.PraxisPort
	cb circuitbreaker.CircuitBreaker[any]
}

func (c *cbPraxis) ListCapabilities(ctx context.Context) ([]domain.CapabilityRef, error) {
	out, err := c.cb.Execute(ctx, func(ctx context.Context) (any, error) {
		return c.p.ListCapabilities(ctx)
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return out.([]domain.CapabilityRef), nil
}

func (c *cbPraxis) Execute(ctx context.Context, a domain.ActionRequest) (domain.ActionResult, error) {
	out, err := c.cb.Execute(ctx, func(ctx context.Context) (any, error) {
		return c.p.Execute(ctx, a)
	})
	if err != nil {
		return domain.ActionResult{}, mapErr(err)
	}
	return out.(domain.ActionResult), nil
}

func (c *cbPraxis) DryRun(ctx context.Context, a domain.ActionRequest) (domain.SimulationRef, error) {
	out, err := c.cb.Execute(ctx, func(ctx context.Context) (any, error) {
		return c.p.DryRun(ctx, a)
	})
	if err != nil {
		return domain.SimulationRef{}, mapErr(err)
	}
	return out.(domain.SimulationRef), nil
}

// WrapLayers convenience-wraps every layer in one call.
func WrapLayers(l ports.Layers, cfg CircuitConfig) ports.Layers {
	return ports.Layers{
		Mnemos:  WrapMnemos(l.Mnemos, cfg),
		Chronos: WrapChronos(l.Chronos, cfg),
		Nous:    WrapNous(l.Nous, cfg),
		Praxis:  WrapPraxis(l.Praxis, cfg),
	}
}

// mapErr translates a circuit-breaker open error into ErrLayerUnavailable.
// Any other error passes through.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ferrors.ErrCircuitOpen) {
		return fmt.Errorf("%w: %v", ErrLayerUnavailable, err)
	}
	return err
}
