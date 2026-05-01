package plugin_test

import (
	"context"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/fake"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/intent"
	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/store/memory"
	"github.com/felixgeelhaar/olymp/plugin"
)

type echoPlugin struct {
	intentType string
	override   plugin.LayerOverride
}

func (e *echoPlugin) Name() string { return "echo" }
func (e *echoPlugin) Init(ctx context.Context, h plugin.Host) error {
	if e.intentType != "" {
		if err := h.RegisterIntent(ctx, domain.IntentType{
			Name:         e.intentType,
			Policy:       domain.IntentPolicy{MaxIterations: 1},
			RegisteredAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
	}
	if e.override.Nous != nil || e.override.Praxis != nil {
		h.OverrideLayers(e.override)
	}
	return nil
}

func TestRegistry_RegistersIntents(t *testing.T) {
	repos := memory.New()
	registry := intent.New(repos.IntentTypes)
	r := plugin.New(registry, ports.Layers{})
	r.Register(&echoPlugin{intentType: "custom_intent"})
	if _, err := r.Boot(context.Background()); err != nil {
		t.Fatalf("boot: %v", err)
	}
	got, err := registry.Get(context.Background(), "custom_intent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "custom_intent" {
		t.Fatalf("got=%+v", got)
	}
}

func TestRegistry_OverridesLayers(t *testing.T) {
	repos := memory.New()
	registry := intent.New(repos.IntentTypes)
	customNous := &fake.Nous{ScriptedDecision: domain.DecisionRef{ID: "from-plugin"}}
	r := plugin.New(registry, ports.Layers{
		Mnemos:  &fake.Mnemos{},
		Chronos: &fake.Chronos{},
		Nous:    &fake.Nous{},
		Praxis:  &fake.Praxis{},
	})
	r.Register(&echoPlugin{override: plugin.LayerOverride{Nous: customNous}})
	layers, err := r.Boot(context.Background())
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	if layers.Nous != customNous {
		t.Fatal("plugin override not applied")
	}
	if layers.Mnemos == nil {
		t.Fatal("non-overridden layer cleared")
	}
}

func TestRegistry_Names(t *testing.T) {
	r := plugin.New(intent.New(memory.New().IntentTypes), ports.Layers{})
	r.Register(&echoPlugin{})
	r.Register(&echoPlugin{intentType: "x"})
	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("names=%v", names)
	}
}
