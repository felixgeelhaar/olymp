package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/felixgeelhaar/bolt"
	mcp "github.com/felixgeelhaar/mcp-go"
	"github.com/felixgeelhaar/olymp/internal/adapters/chronos"
	"github.com/felixgeelhaar/olymp/internal/adapters/fake"
	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/adapters/mnemos"
	"github.com/felixgeelhaar/olymp/internal/adapters/nous"
	"github.com/felixgeelhaar/olymp/internal/adapters/praxis"
	"github.com/felixgeelhaar/olymp/internal/agentdesc"
	"github.com/felixgeelhaar/olymp/internal/api"
	"github.com/felixgeelhaar/olymp/internal/audit"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/engine"
	"github.com/felixgeelhaar/olymp/internal/eventbus"
	"github.com/felixgeelhaar/olymp/internal/explain"
	"github.com/felixgeelhaar/olymp/internal/intent"
	"github.com/felixgeelhaar/olymp/internal/observability"
	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/store"

	agentprotocol "github.com/felixgeelhaar/agent-go/domain/protocol"
	axidomain "github.com/felixgeelhaar/axi-go/domain"
)

// MCP tool I/O types.

type mcpSubmitInput struct {
	Type    string         `json:"type" jsonschema:"required,description=Intent type, e.g. explain or remediate"`
	Subject string         `json:"subject,omitempty" jsonschema:"description=Subject of the intent"`
	Payload map[string]any `json:"payload,omitempty" jsonschema:"description=Free-form payload validated against the IntentType schema"`
}

type mcpInspectInput struct {
	RunID string `json:"run_id" jsonschema:"required,description=Run ID returned from submit_intent"`
}

type mcpSteerInput struct {
	RunID  string `json:"run_id" jsonschema:"required"`
	Kind   string `json:"kind" jsonschema:"required,description=One of: pause, resume, cancel, approve, deny"`
	Reason string `json:"reason,omitempty"`
}

type mcpHaltInput struct {
	Reason string `json:"reason,omitempty"`
}

type mcpExplainInput struct {
	RunID string `json:"run_id" jsonschema:"required"`
}

type mcpListIntentsOutput struct {
	IntentTypes []domain.IntentType `json:"intent_types"`
}

type mcpHaltOutput struct {
	Affected []string `json:"affected"`
}

func cmdMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	dbType := fs.String("db", envOr("OLYMP_DB_TYPE", "memory"), "store backend: memory|sqlite|postgres")
	dbConn := fs.String("db-conn", os.Getenv("OLYMP_DB_CONN"), "store connection string")
	demoMode := fs.Bool("demo", false, "use in-process fake cognitive layers")
	mnemosURL := fs.String("mnemos-url", envOr("OLYMP_MNEMOS_URL", "http://localhost:8081"), "mnemos base URL")
	chronosURL := fs.String("chronos-url", envOr("OLYMP_CHRONOS_URL", "http://localhost:8082"), "chronos base URL")
	nousURL := fs.String("nous-url", envOr("OLYMP_NOUS_URL", "http://localhost:8083"), "nous base URL")
	praxisURL := fs.String("praxis-url", envOr("OLYMP_PRAXIS_URL", "http://localhost:8084"), "praxis base URL")
	if err := fs.Parse(args); err != nil {
		return &OlympError{Code: "bad_input", Message: err.Error()}
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	repos, err := store.Open(rootCtx, store.Config{Type: *dbType, Conn: *dbConn})
	if err != nil {
		return &OlympError{Code: "store_open", Message: err.Error(), Cause: err}
	}
	registry := intent.New(repos.IntentTypes)
	if _, err := registry.RegisterBuiltins(rootCtx); err != nil {
		return &OlympError{Code: "builtins", Message: err.Error(), Cause: err}
	}
	auditor := audit.New(repos.Audit, nil)
	bus := eventbus.New(256)

	var layers ports.Layers
	if *demoMode {
		layers = ports.Layers{
			Mnemos:  &fake.Mnemos{},
			Chronos: &fake.Chronos{},
			Nous:    &fake.Nous{ScriptedDecision: domain.DecisionRef{ID: "demo-decision"}},
			Praxis:  &fake.Praxis{Result: domain.ActionResult{Status: "succeeded"}},
		}
	} else {
		layers = ports.Layers{
			Mnemos:  mnemos.New(httpx.Config{BaseURL: *mnemosURL}),
			Chronos: chronos.New(httpx.Config{BaseURL: *chronosURL}),
			Nous:    nous.New(httpx.Config{BaseURL: *nousURL}),
			Praxis:  praxis.New(httpx.Config{BaseURL: *praxisURL}),
		}
	}

	logger := bolt.New(bolt.NewJSONHandler(os.Stderr))
	eng := engine.New(engine.Config{
		Logger: logger, Health: observability.NewHealthRegistry(),
	}, layers, repos, registry, auditor, bus)
	svc := api.NewService(repos, registry, auditor, bus, eng)

	// Build the axi-go kernel that wraps every MCP tool with effect gating,
	// evidence recording, and execution-budget enforcement. If kernel
	// construction fails the server still starts — handlers fall back to
	// direct dispatch so a kernel bug never blocks the agent.
	executors := map[string]axidomain.ActionExecutor{
		"exec.submit_intent": axiToolExecutor[mcpSubmitInput, domain.Run]{run: func(ctx context.Context, in mcpSubmitInput) (domain.Run, error) {
			ctx = api.WithCaller(ctx, mcpCaller())
			return svc.Submit(ctx, domain.Intent{Type: in.Type, Subject: in.Subject, Payload: in.Payload})
		}},
		"exec.inspect_run": axiToolExecutor[mcpInspectInput, domain.RunSnapshot]{run: func(ctx context.Context, in mcpInspectInput) (domain.RunSnapshot, error) {
			return svc.Inspect(ctx, in.RunID)
		}},
		"exec.steer_run": axiToolExecutor[mcpSteerInput, struct {
			OK bool `json:"ok"`
		}]{run: func(ctx context.Context, in mcpSteerInput) (struct {
			OK bool `json:"ok"`
		}, error) {
			ctx = api.WithCaller(ctx, mcpCaller())
			err := svc.Steer(ctx, in.RunID, domain.SteerCommand{
				Kind: in.Kind, Reason: in.Reason, Caller: mcpCaller(),
			})
			return struct {
				OK bool `json:"ok"`
			}{OK: err == nil}, err
		}},
		"exec.halt": axiToolExecutor[mcpHaltInput, mcpHaltOutput]{run: func(ctx context.Context, in mcpHaltInput) (mcpHaltOutput, error) {
			ctx = api.WithCaller(ctx, mcpCaller())
			ids, err := svc.Halt(ctx, in.Reason)
			return mcpHaltOutput{Affected: ids}, err
		}},
		"exec.list_intents": axiToolExecutor[struct{}, mcpListIntentsOutput]{run: func(ctx context.Context, _ struct{}) (mcpListIntentsOutput, error) {
			types, err := registry.List(ctx)
			return mcpListIntentsOutput{IntentTypes: types}, err
		}},
		"exec.explain_run": axiToolExecutor[mcpExplainInput, explain.Chain]{run: func(ctx context.Context, in mcpExplainInput) (explain.Chain, error) {
			return explain.Build(ctx, repos.Runs, layers, in.RunID)
		}},
		"exec.agent_descriptor": axiToolExecutor[struct{}, agentprotocol.AgentDescriptor]{run: func(ctx context.Context, _ struct{}) (agentprotocol.AgentDescriptor, error) {
			return agentdesc.Build(ctx, registry, "olymp", "Olymp — AI runtime for the cognitive stack.")
		}},
	}
	kernel, kernelErr := buildAxiKernel(logger, executors)
	if kernelErr != nil {
		fmt.Fprintf(os.Stderr, "olymp mcp: axi-go kernel disabled: %v\n", kernelErr)
	}

	srv := mcp.NewServer(mcp.ServerInfo{
		Name:         "olymp",
		Version:      "0.1.0",
		Capabilities: mcp.Capabilities{Tools: true},
	},
		mcp.WithTitle("Olymp MCP Server"),
		mcp.WithDescription("AI runtime: drive observe → understand → decide → act → learn loops over Mnemos / Chronos / Nous / Praxis."),
		mcp.WithInstructions("Submit intents with submit_intent. Inspect a run with inspect_run. Steer with steer_run. Halt all with halt. Explain a run's full provenance with explain_run. Discover capabilities with agent_descriptor."),
	)

	srv.Tool("submit_intent").
		Description("Submit a typed intent. Drives the full cognitive loop and returns the resulting Run.").
		ValidateInput().
		Handler(func(ctx context.Context, in mcpSubmitInput) (domain.Run, error) {
			if kernel != nil {
				return dispatchAxiTool[domain.Run](ctx, kernel, "", "submit_intent", in)
			}
			ctx = api.WithCaller(ctx, mcpCaller())
			return svc.Submit(ctx, domain.Intent{Type: in.Type, Subject: in.Subject, Payload: in.Payload})
		})

	srv.Tool("inspect_run").
		Description("Return the RunSnapshot for a given Run ID — status, provenance, cross-layer references.").
		ValidateInput().
		Handler(func(ctx context.Context, in mcpInspectInput) (domain.RunSnapshot, error) {
			if kernel != nil {
				return dispatchAxiTool[domain.RunSnapshot](ctx, kernel, "", "inspect_run", in)
			}
			return svc.Inspect(ctx, in.RunID)
		})

	srv.Tool("steer_run").
		Description("Apply a runtime command to a live (or paused) run: pause / resume / cancel / approve / deny.").
		ValidateInput().
		Handler(func(ctx context.Context, in mcpSteerInput) (struct {
			OK bool `json:"ok"`
		}, error) {
			if kernel != nil {
				return dispatchAxiTool[struct {
					OK bool `json:"ok"`
				}](ctx, kernel, "", "steer_run", in)
			}
			ctx = api.WithCaller(ctx, mcpCaller())
			err := svc.Steer(ctx, in.RunID, domain.SteerCommand{
				Kind: in.Kind, Reason: in.Reason, Caller: mcpCaller(),
			})
			return struct {
				OK bool `json:"ok"`
			}{OK: err == nil}, err
		})

	srv.Tool("halt").
		Description("Kill switch: pause every non-terminal run and deny pending approvals.").
		Handler(func(ctx context.Context, in mcpHaltInput) (mcpHaltOutput, error) {
			if kernel != nil {
				return dispatchAxiTool[mcpHaltOutput](ctx, kernel, "", "halt", in)
			}
			ctx = api.WithCaller(ctx, mcpCaller())
			ids, err := svc.Halt(ctx, in.Reason)
			return mcpHaltOutput{Affected: ids}, err
		})

	srv.Tool("list_intents").
		Description("List the IntentTypes registered with the runtime.").
		Handler(func(ctx context.Context, _ struct{}) (mcpListIntentsOutput, error) {
			if kernel != nil {
				return dispatchAxiTool[mcpListIntentsOutput](ctx, kernel, "", "list_intents", struct{}{})
			}
			types, err := registry.List(ctx)
			return mcpListIntentsOutput{IntentTypes: types}, err
		})

	srv.Tool("explain_run").
		Description("Return the full Memory → Signal → Decision → Action → Outcome provenance chain for a run, with citations and confidence.").
		ValidateInput().
		Handler(func(ctx context.Context, in mcpExplainInput) (explain.Chain, error) {
			if kernel != nil {
				return dispatchAxiTool[explain.Chain](ctx, kernel, "", "explain_run", in)
			}
			return explain.Build(ctx, repos.Runs, layers, in.RunID)
		})

	srv.Tool("agent_descriptor").
		Description("Return the agent-go AgentDescriptor for this runtime — capabilities, actions, trust level. Lets agent-go agents discover what Olymp can do.").
		Handler(func(ctx context.Context, _ struct{}) (agentprotocol.AgentDescriptor, error) {
			if kernel != nil {
				return dispatchAxiTool[agentprotocol.AgentDescriptor](ctx, kernel, "", "agent_descriptor", struct{}{})
			}
			return agentdesc.Build(ctx, registry, "olymp", "Olymp — AI runtime for the cognitive stack.")
		})

	stopSignals := make(chan os.Signal, 1)
	signal.Notify(stopSignals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stopSignals
		rootCancel()
	}()

	if err := mcp.ServeStdio(rootCtx, srv,
		mcp.WithMiddleware(mcp.DefaultMiddlewareWithTimeout(mcpBoltLogger{logger: logger}, 30*time.Second)...)); err != nil && !errors.Is(err, context.Canceled) {
		return &OlympError{Code: "mcp_serve", Message: err.Error(), Cause: err}
	}

	if repos.Close != nil {
		_ = repos.Close()
	}
	return nil
}

func mcpCaller() domain.CallerRef {
	return domain.CallerRef{
		Type: envOr("OLYMP_CALLER_TYPE", "agent"),
		ID:   envOr("OLYMP_CALLER_ID", "mcp-client"),
		Name: envOr("OLYMP_CALLER_NAME", "Claude Code via MCP"),
	}
}

type mcpBoltLogger struct{ logger *bolt.Logger }

func (l mcpBoltLogger) Info(msg string, fields ...mcp.LogField) {
	l.log(l.logger.Info(), msg, fields...)
}
func (l mcpBoltLogger) Error(msg string, fields ...mcp.LogField) {
	l.log(l.logger.Error(), msg, fields...)
}
func (l mcpBoltLogger) Debug(msg string, fields ...mcp.LogField) {
	l.log(l.logger.Debug(), msg, fields...)
}
func (l mcpBoltLogger) Warn(msg string, fields ...mcp.LogField) {
	l.log(l.logger.Warn(), msg, fields...)
}

func (l mcpBoltLogger) log(event *bolt.Event, msg string, fields ...mcp.LogField) {
	for _, f := range fields {
		event = event.Any(f.Key, f.Value)
	}
	event.Msg(msg)
}

// fmt is used by some callers that may be added later; keep the import
// intact for forward compatibility without a TODO.
var _ = fmt.Sprintf
