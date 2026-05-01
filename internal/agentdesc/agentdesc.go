// Package agentdesc emits an agent-go AgentDescriptor for the Olymp runtime.
//
// agent-go agents discover Olymp's capabilities by reading this descriptor
// and dispatching to the matching tools (over MCP) or HTTP endpoints. The
// descriptor mirrors the registered IntentTypes — every IntentType becomes
// one Capability with the four runtime actions (submit/inspect/steer/stream).
package agentdesc

import (
	"context"
	"strings"

	"github.com/felixgeelhaar/agent-go/domain/protocol"
	"github.com/felixgeelhaar/olymp/internal/intent"
)

// RuntimeActions are the four actions every Olymp runtime exposes,
// regardless of which IntentTypes are registered.
var RuntimeActions = []string{"submit", "inspect", "steer", "stream"}

// Tools are the MCP tool names Olymp publishes (so an agent-go agent can
// discover them via AgentDescriptor.Capabilities[].ToolNames).
var Tools = []string{
	"submit_intent",
	"inspect_run",
	"steer_run",
	"halt",
	"list_intents",
	"explain_run",
}

// Build returns an agent-go AgentDescriptor describing the runtime, with one
// Capability per registered IntentType plus a "runtime" capability covering
// the four core verbs.
func Build(ctx context.Context, registry *intent.Registry, name, description string) (protocol.AgentDescriptor, error) {
	desc := protocol.AgentDescriptor{
		Name:        nonEmpty(name, "olymp"),
		Description: nonEmpty(description, "Olymp — AI runtime for the cognitive stack."),
		TrustLevel:  protocol.TrustLimited,
		Metadata: map[string]string{
			"category": "ai-runtime",
			"loop":     "observe-understand-decide-act-learn",
		},
	}
	desc.Capabilities = []protocol.Capability{{
		Name:        "runtime",
		Description: "Core Olymp runtime verbs: drive cognitive loops end-to-end.",
		Actions:     append([]string(nil), RuntimeActions...),
		ToolNames:   append([]string(nil), Tools...),
	}}
	if registry == nil {
		return desc, nil
	}
	types, err := registry.List(ctx)
	if err != nil {
		return desc, err
	}
	for _, t := range types {
		desc.Capabilities = append(desc.Capabilities, protocol.Capability{
			Name:        "intent." + t.Name,
			Description: t.Description,
			Actions:     []string{"submit"},
			ToolNames:   []string{"submit_intent"},
		})
	}
	return desc, nil
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
