package agentdesc_test

import (
	"context"
	"testing"

	"github.com/felixgeelhaar/agent-go/domain/protocol"
	"github.com/felixgeelhaar/olymp/internal/agentdesc"
	"github.com/felixgeelhaar/olymp/internal/intent"
	"github.com/felixgeelhaar/olymp/internal/store/memory"
)

func TestBuild_DefaultsAndIntents(t *testing.T) {
	repos := memory.New()
	r := intent.New(repos.IntentTypes)
	if _, err := r.RegisterBuiltins(context.Background()); err != nil {
		t.Fatalf("builtins: %v", err)
	}
	desc, err := agentdesc.Build(context.Background(), r, "", "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if desc.Name != "olymp" {
		t.Fatalf("name=%s", desc.Name)
	}
	// runtime capability + 2 builtins
	if len(desc.Capabilities) != 3 {
		t.Fatalf("caps=%d want 3", len(desc.Capabilities))
	}
	first := desc.Capabilities[0]
	if first.Name != "runtime" {
		t.Fatalf("first cap=%s want runtime", first.Name)
	}
	if len(first.Actions) != 4 || len(first.ToolNames) == 0 {
		t.Fatalf("runtime cap=%+v", first)
	}
	if !desc.HasCapability("intent.explain") {
		t.Fatal("explain capability missing")
	}
	if !desc.HasCapability("intent.remediate") {
		t.Fatal("remediate capability missing")
	}
}

func TestBuild_NilRegistry(t *testing.T) {
	desc, err := agentdesc.Build(context.Background(), nil, "custom", "custom desc")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if desc.Name != "custom" {
		t.Fatalf("name=%s", desc.Name)
	}
	if len(desc.Capabilities) != 1 {
		t.Fatalf("caps=%d want 1", len(desc.Capabilities))
	}
}

func TestBuild_TrustLevel(t *testing.T) {
	desc, _ := agentdesc.Build(context.Background(), nil, "", "")
	if desc.TrustLevel != protocol.TrustLimited {
		t.Fatalf("trust=%v", desc.TrustLevel)
	}
}
