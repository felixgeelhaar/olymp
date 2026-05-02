package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/felixgeelhaar/axi-go"
	"github.com/felixgeelhaar/axi-go/domain"
	"github.com/felixgeelhaar/bolt"
)

// axiActionPrefix is the axi-go action name corresponding to each MCP tool.
// axi-go's name regex disallows underscores, so we map at the boundary:
// agents see `submit_intent` (MCP), the kernel sees `submit-intent`.
const axiActionPrefix = "olymp-mcp-"

// olympTool describes one MCP tool in axi-go terms.
type olympTool struct {
	Name        string
	Effect      domain.EffectLevel
	Idempotent  bool
	Description string
}

// mcpTools returns the descriptors for every MCP-exposed Olymp tool.
// Single source of truth; kernel registration and the MCP handler dispatch
// cannot drift.
func mcpTools() []olympTool {
	return []olympTool{
		{Name: "submit_intent", Effect: domain.EffectWriteExternal, Idempotent: false,
			Description: "Submit a typed intent. Drives the full cognitive loop and returns the resulting Run."},
		{Name: "inspect_run", Effect: domain.EffectReadLocal, Idempotent: true,
			Description: "Return the RunSnapshot for a given Run ID — status, provenance, cross-layer references."},
		{Name: "steer_run", Effect: domain.EffectWriteExternal, Idempotent: false,
			Description: "Apply a runtime command to a live (or paused) run: pause / resume / cancel / approve / deny."},
		{Name: "halt", Effect: domain.EffectWriteExternal, Idempotent: true,
			Description: "Kill switch: pause every non-terminal run and deny pending approvals."},
		{Name: "list_intents", Effect: domain.EffectReadLocal, Idempotent: true,
			Description: "List the IntentTypes registered with the runtime."},
		{Name: "explain_run", Effect: domain.EffectReadLocal, Idempotent: true,
			Description: "Return the full Memory → Signal → Decision → Action → Outcome provenance chain for a run, with citations and confidence."},
		{Name: "agent_descriptor", Effect: domain.EffectReadLocal, Idempotent: true,
			Description: "Return the agent-go AgentDescriptor for this runtime — capabilities, actions, trust level."},
	}
}

func axiActionName(mcpName string) string {
	return axiActionPrefix + strings.ReplaceAll(mcpName, "_", "-")
}

// buildAxiKernel builds an axi-go kernel pre-registered with every MCP tool.
// Executors carry runtime state (the api.Service, registry, layers) and are
// passed in by the caller.
func buildAxiKernel(logger *bolt.Logger, executors map[string]domain.ActionExecutor) (*axi.Kernel, error) {
	plugin, err := newOlympMCPPlugin()
	if err != nil {
		return nil, fmt.Errorf("build plugin: %w", err)
	}
	kernel := axi.New().
		WithLogger(boltAxiLogger{logger: logger}).
		WithDomainEventPublisher(boltAxiPublisher{logger: logger}).
		WithBudget(axiBudgetFromEnv())
	for ref, exec := range executors {
		kernel.RegisterActionExecutor(ref, exec)
	}
	if err := kernel.RegisterPlugin(plugin); err != nil {
		return nil, fmt.Errorf("register plugin: %w", err)
	}
	return kernel, nil
}

type olympMCPPlugin struct {
	actions []*domain.ActionDefinition
}

func (p olympMCPPlugin) Contribute() (*domain.PluginContribution, error) {
	return domain.NewPluginContribution("olymp.mcp", p.actions, nil)
}

func newOlympMCPPlugin() (olympMCPPlugin, error) {
	p := olympMCPPlugin{}
	for _, t := range mcpTools() {
		name, err := domain.NewActionName(axiActionName(t.Name))
		if err != nil {
			return p, fmt.Errorf("invalid axi action name for %s: %w", t.Name, err)
		}
		action, err := domain.NewActionDefinition(
			name,
			t.Description,
			domain.EmptyContract(), // typed validation happens at the MCP layer
			domain.EmptyContract(),
			nil,
			domain.EffectProfile{Level: t.Effect},
			domain.IdempotencyProfile{IsIdempotent: t.Idempotent},
		)
		if err != nil {
			return p, fmt.Errorf("define action %s: %w", t.Name, err)
		}
		if err := action.BindExecutor(domain.ActionExecutorRef("exec." + t.Name)); err != nil {
			return p, fmt.Errorf("bind executor for %s: %w", t.Name, err)
		}
		p.actions = append(p.actions, action)
	}
	return p, nil
}

// axiBudgetFromEnv reads OLYMP_AXI_MAX_DURATION + OLYMP_AXI_MAX_INVOCATIONS.
// Defaults: 5 minutes / 1000 invocations.
func axiBudgetFromEnv() axi.Budget {
	b := axi.Budget{
		MaxDuration:              5 * time.Minute,
		MaxCapabilityInvocations: 1000,
	}
	if v := os.Getenv("OLYMP_AXI_MAX_DURATION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			b.MaxDuration = d
		}
	}
	if v := os.Getenv("OLYMP_AXI_MAX_INVOCATIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			b.MaxCapabilityInvocations = n
		}
	}
	return b
}

// boltAxiLogger adapts our *bolt.Logger to axi-go's domain.Logger.
type boltAxiLogger struct{ logger *bolt.Logger }

func (l boltAxiLogger) Debug(msg string, fields ...domain.Field) {
	l.emit(l.logger.Debug(), msg, fields)
}
func (l boltAxiLogger) Info(msg string, fields ...domain.Field) {
	l.emit(l.logger.Info(), msg, fields)
}
func (l boltAxiLogger) Warn(msg string, fields ...domain.Field) {
	l.emit(l.logger.Warn(), msg, fields)
}
func (l boltAxiLogger) Error(msg string, fields ...domain.Field) {
	l.emit(l.logger.Error(), msg, fields)
}

func (l boltAxiLogger) emit(ev *bolt.Event, msg string, fields []domain.Field) {
	for _, f := range fields {
		ev = ev.Any(f.Key, f.Value)
	}
	ev.Msg(msg)
}

type boltAxiPublisher struct{ logger *bolt.Logger }

func (p boltAxiPublisher) Publish(e domain.DomainEvent) {
	p.logger.Info().
		Str("event_type", e.EventType()).
		Str("occurred_at", e.OccurredAt().UTC().Format(time.RFC3339Nano)).
		Msg("axi_event")
}

// axiRemarshal converts between two arbitrary JSON-shaped types via a JSON
// round-trip. Each MCP call is bounded by network latency anyway; the
// round-trip is rounding error.
func axiRemarshal(in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return json.Unmarshal(b, out)
}

// dispatchAxiTool routes an MCP tool invocation through the axi-go kernel.
// Returns the typed Out from the kernel result.
func dispatchAxiTool[Out any](ctx context.Context, kernel *axi.Kernel, mcpName string, in any) (Out, error) {
	var zero Out
	inputMap, err := axiToInputMap(in)
	if err != nil {
		return zero, err
	}
	res, err := kernel.Execute(ctx, axi.Invocation{
		Action: axiActionName(mcpName),
		Input:  inputMap,
	})
	if err != nil {
		return zero, err
	}
	if res.Result.Data == nil {
		return zero, nil
	}
	var out Out
	if err := axiRemarshal(res.Result.Data, &out); err != nil {
		return zero, err
	}
	return out, nil
}

func axiToInputMap(in any) (map[string]any, error) {
	if in == nil {
		return map[string]any{}, nil
	}
	if m, ok := in.(map[string]any); ok {
		return m, nil
	}
	out := map[string]any{}
	if err := axiRemarshal(in, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// axiToolExecutor adapts a typed `func(ctx, In) (Out, error)` into a
// domain.ActionExecutor. JSON round-trips bridge axi-go's untyped Input.
type axiToolExecutor[In any, Out any] struct {
	run func(context.Context, In) (Out, error)
}

func (e axiToolExecutor[In, Out]) Execute(ctx context.Context, input any, _ domain.CapabilityInvoker) (domain.ExecutionResult, []domain.EvidenceRecord, error) {
	var typed In
	if input != nil {
		if err := axiRemarshal(input, &typed); err != nil {
			return domain.ExecutionResult{}, nil, err
		}
	}
	out, err := e.run(ctx, typed)
	if err != nil {
		return domain.ExecutionResult{}, nil, err
	}
	return domain.ExecutionResult{Data: out}, nil, nil
}
