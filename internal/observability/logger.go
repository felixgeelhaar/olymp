// Package observability is the runtime's structured logging facade.
//
// Every service receives a *bolt.Logger via constructor injection. This
// package provides the canonical field set for loop-step logs (run_id,
// iteration, stage, layer, intent_type, caller_type, caller_id, duration_ms)
// and the health endpoint that surfaces per-layer adapter status.
//
// No service should call log.Println or build raw slog handlers — they go
// through bolt via this package so structure stays uniform.
package observability

import (
	"io"
	"os"
	"sync"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/olymp/internal/domain"
)

// NewJSON returns a *bolt.Logger that writes JSON to w. nil w defaults to
// stderr.
func NewJSON(w io.Writer) *bolt.Logger {
	if w == nil {
		w = os.Stderr
	}
	return bolt.New(bolt.NewJSONHandler(w))
}

// LoopFields is the canonical field set for a single loop-step log line.
type LoopFields struct {
	RunID      string
	Iteration  int
	Stage      domain.RunStatus
	Layer      string
	IntentType string
	CallerType string
	CallerID   string
	DurationMs int64
}

// LogStep emits a single info-level log line with the canonical loop-step
// fields. Returns the *bolt.Event so callers may chain extra fields.
func LogStep(logger *bolt.Logger, msg string, f LoopFields) {
	if logger == nil {
		return
	}
	logger.Info().
		Str("run_id", f.RunID).
		Int("iteration", f.Iteration).
		Str("stage", string(f.Stage)).
		Str("layer", f.Layer).
		Str("intent_type", f.IntentType).
		Str("caller_type", f.CallerType).
		Str("caller_id", f.CallerID).
		Int64("duration_ms", f.DurationMs).
		Msg(msg)
}

// LogError emits an error-level line with run context.
func LogError(logger *bolt.Logger, msg string, runID string, err error, extra map[string]any) {
	if logger == nil {
		return
	}
	ev := logger.Error().
		Str("run_id", runID).
		Str("error", errString(err))
	for k, v := range extra {
		ev = ev.Any(k, v)
	}
	ev.Msg(msg)
}

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

// LayerStatus is the health record for one cognitive-layer adapter.
type LayerStatus struct {
	Layer       string    `json:"layer"`
	Healthy     bool      `json:"healthy"`
	LastSuccess time.Time `json:"last_success,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitempty"`
	// CircuitState surfaces the fortify/circuit state in Phase 2; "closed"
	// is the Phase-1 default.
	CircuitState string `json:"circuit_state"`
}

// Health is the runtime health record exposed via /healthz.
type Health struct {
	Status string        `json:"status"`
	Layers []LayerStatus `json:"layers"`
}

// HealthRegistry tracks per-layer success/failure for the health endpoint.
// Adapters call MarkSuccess + MarkError; the HTTP handler reads Snapshot.
type HealthRegistry struct {
	mu     sync.RWMutex
	layers map[string]*LayerStatus
}

// NewHealthRegistry returns a registry pre-populated with the four
// cognitive-stack layers in healthy state.
func NewHealthRegistry() *HealthRegistry {
	r := &HealthRegistry{layers: map[string]*LayerStatus{}}
	for _, name := range []string{"mnemos", "chronos", "nous", "praxis"} {
		r.layers[name] = &LayerStatus{Layer: name, Healthy: true, CircuitState: "closed"}
	}
	return r
}

// MarkSuccess records a successful call against layer.
func (r *HealthRegistry) MarkSuccess(layer string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.layers[layer]
	if !ok {
		s = &LayerStatus{Layer: layer, CircuitState: "closed"}
		r.layers[layer] = s
	}
	s.Healthy = true
	s.LastSuccess = at
}

// SetCircuitState records the current circuit state for layer (closed/open/
// half-open). Used by resilience.WrapLayers to surface breaker state in
// /healthz responses.
func (r *HealthRegistry) SetCircuitState(layer, state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.layers[layer]
	if !ok {
		s = &LayerStatus{Layer: layer}
		r.layers[layer] = s
	}
	s.CircuitState = state
}

// MarkError records a failed call against layer with the given error.
func (r *HealthRegistry) MarkError(layer string, at time.Time, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.layers[layer]
	if !ok {
		s = &LayerStatus{Layer: layer, CircuitState: "closed"}
		r.layers[layer] = s
	}
	s.Healthy = false
	s.LastError = errString(err)
	s.LastErrorAt = at
}

// Snapshot returns the current Health record. Status is "ok" iff all layers
// are healthy.
func (r *HealthRegistry) Snapshot() Health {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := Health{Status: "ok"}
	for _, s := range r.layers {
		copy := *s
		out.Layers = append(out.Layers, copy)
		if !s.Healthy {
			out.Status = "degraded"
		}
	}
	return out
}
