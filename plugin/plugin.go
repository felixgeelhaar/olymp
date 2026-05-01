// Package plugin is the public extension surface for out-of-tree code.
//
// Hosts that embed Olymp (cmd/olymp imports + LoadPlugins) extend the runtime
// by registering:
//
//   - custom IntentTypes (typed entry points to the loop)
//   - custom cognitive-layer adapters (Mnemos / Chronos / Nous / Praxis
//     replacements; e.g. a self-hosted Mnemos vs a managed one)
//   - federation peers (a remote Olymp runtime treated as a Nous backend, so
//     `olymp explain` can recursively delegate to a sibling runtime)
//
// Plugins are registered at process startup before `olymp serve` enters its
// accept loop. Hot-reload is intentionally not supported in v0; that's a
// distributed-runner concern.
package plugin

import (
	"context"
	"fmt"
	"sync"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/intent"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// LayerOverride lets a plugin replace one or more cognitive-layer adapters.
// Nil fields are left untouched on the host's existing ports.Layers.
type LayerOverride struct {
	Mnemos  ports.MnemosPort
	Chronos ports.ChronosPort
	Nous    ports.NousPort
	Praxis  ports.PraxisPort
}

// Plugin is what host code implements + registers via Register.
type Plugin interface {
	Name() string
	Init(ctx context.Context, host Host) error
}

// Host is the surface a plugin uses to register its contributions.
type Host interface {
	// RegisterIntent adds an IntentType to the runtime's intent registry.
	RegisterIntent(ctx context.Context, t domain.IntentType) error
	// OverrideLayers replaces zero or more cognitive-layer adapters. Plugins
	// SHOULD NOT call this after the runtime has entered its serve loop.
	OverrideLayers(o LayerOverride)
}

// Registry tracks loaded plugins and their contributions. Construct with New.
// The runtime's bootstrap (cmd/olymp/serve.go) creates one Registry per
// process, calls Init on every Plugin, then asks the Registry for the final
// Layers + IntentRegistry to wire into the engine.
type Registry struct {
	mu       sync.Mutex
	plugins  []Plugin
	intents  *intent.Registry
	layers   ports.Layers
	override LayerOverride
}

// New returns a Registry that contributes to the given intent registry +
// initial layers. Plugin Inits run in registration order.
func New(intents *intent.Registry, layers ports.Layers) *Registry {
	return &Registry{intents: intents, layers: layers}
}

// Register adds a Plugin without running it yet. Call Boot once registration
// is done to invoke every plugin's Init in order.
func (r *Registry) Register(p Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins = append(r.plugins, p)
}

// Boot calls Init on every registered Plugin. Returns the layered Layers
// (with overrides applied) for the engine to consume.
func (r *Registry) Boot(ctx context.Context) (ports.Layers, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.plugins {
		if err := p.Init(ctx, &hostImpl{r: r}); err != nil {
			return ports.Layers{}, fmt.Errorf("plugin %q: init: %w", p.Name(), err)
		}
	}
	final := r.layers
	if o := r.override; o.Mnemos != nil {
		final.Mnemos = o.Mnemos
	}
	if o := r.override; o.Chronos != nil {
		final.Chronos = o.Chronos
	}
	if o := r.override; o.Nous != nil {
		final.Nous = o.Nous
	}
	if o := r.override; o.Praxis != nil {
		final.Praxis = o.Praxis
	}
	return final, nil
}

// Names returns the names of every registered Plugin in registration order.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p.Name())
	}
	return out
}

type hostImpl struct{ r *Registry }

func (h *hostImpl) RegisterIntent(ctx context.Context, t domain.IntentType) error {
	if h.r.intents == nil {
		return fmt.Errorf("plugin host: intent registry is nil")
	}
	return h.r.intents.Register(ctx, t)
}

func (h *hostImpl) OverrideLayers(o LayerOverride) {
	if o.Mnemos != nil {
		h.r.override.Mnemos = o.Mnemos
	}
	if o.Chronos != nil {
		h.r.override.Chronos = o.Chronos
	}
	if o.Nous != nil {
		h.r.override.Nous = o.Nous
	}
	if o.Praxis != nil {
		h.r.override.Praxis = o.Praxis
	}
}
