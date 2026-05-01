# CLAUDE.md — Machine guidance for Olymp

This file is the machine-oriented complement to [`AGENTS.md`](AGENTS.md).
It exists so an AI assistant landing in this repo cold gets the load-bearing
constraints in one read, then dispatches to richer docs (`PRD.md`, `TDD.md`,
`README.md`, `Roadmap.md`) only when the change demands it.

## What Olymp is

Runtime layer of the four-system cognitive stack
(Mnemos · Chronos · Nous · Praxis). Drives `observe → understand → decide
→ act → learn` end-to-end. **Does not** itself remember, perceive, decide,
or execute.

## Where to look first

| If the task is about… | Open this first |
|---|---|
| product / scope / why | [`PRD.md`](PRD.md) |
| architecture / domain model / FSM | [`TDD.md`](TDD.md) |
| working in the codebase | [`AGENTS.md`](AGENTS.md) |
| roadmap / what's shipped | [`Roadmap.md`](Roadmap.md) |
| how to run + use | [`README.md`](README.md) |

## Load-bearing files

- `internal/domain/run.go` + `run_fsm.go` — Run aggregate + state machine.
  Every status transition goes through the FSM.
- `internal/engine/engine.go` — the loop. The single longest function, but
  also the single point of truth: changes to "what one iteration does"
  land here, nowhere else.
- `internal/ports/{repo,layers,errors}.go` — the ports the engine + API
  speak. Domain code never imports a concrete backend.
- `internal/store/store.go` — `Open(cfg) → Repos` switch.
- `cmd/olymp/axikernel.go` — axi-go kernel wraps every MCP tool with
  effect gating + budget. New MCP tools register here.
- `internal/api/{api,http}.go` — the public OlympAPI service + HTTP routes.

## Hard guarantees the codebase relies on

1. `Engine.Run(ctx, run)` returns either a terminal `Run` or a
   `Run` paused at `awaiting_approval`. Anything else is a bug.
2. Every `learning` stage MUST call `outcome.Writer.Write` for every
   action result. Failure surfaces as `Run → failed`, never silent skip.
3. Every `ProvenanceStep` carries `LayerRef.Layer ∈ {mnemos, chronos,
   nous, praxis, olymp}`. New layers extend the enum *and* the FSM.
4. `Run.PendingDecision` is written iff `Run.Status == awaiting_approval`.
   Resume reads it back; the engine then clears it before re-entering the
   loop.

## Conventions to copy from siblings

Olymp follows the same conventions as Mnemos / Chronos / Nous / Praxis:

- Hand-rolled CLI dispatch (no Cobra). One file per subcommand under
  `cmd/olymp/`.
- Constructors take dependencies; `main` wires.
- No DI container.
- No event sourcing or CQRS at the engine level — writes are simple
  aggregates, reads are direct queries.

## Common change recipes

### Add a new IntentType

1. `internal/intent/registry.go::Builtins()` if it ships in the binary,
   OR via `plugin.Host.RegisterIntent` for out-of-tree types.
2. Set `IntentPolicy.{MaxIterations, DefaultDeadline, ReadOnly,
   RequireApproval, RequiredScopes}` honestly.
3. Add the JSON Schema for the payload.
4. No engine changes — the loop already routes.

### Add a new MCP tool

1. Define I/O types at the top of `cmd/olymp/mcp.go`.
2. Add a row to `cmd/olymp/axikernel.go::mcpTools()` (effect + idempotency).
3. Register an executor in `mcp.go::executors`.
4. Register the tool with `srv.Tool(name).Handler(func ... dispatchAxiTool)`.

### Add a new HTTP route

1. `internal/api/http.go::HTTPHandler` — register the path.
2. Implement `internal/api/api.go::Service` if the route is non-trivial;
   otherwise inline the handler.
3. Add a test in `api_test.go` driving via `httptest.NewServer`.

### Add a new cognitive-layer port

Almost certainly a mistake — Olymp orchestrates four layers, not five.
But if a real fifth layer emerges:

1. Define the port in `internal/ports/layers.go`.
2. Add the field to `ports.Layers`.
3. Add a stage to the FSM + the loop engine.
4. Update the canonical layer string set in `audit.Reconstruct` +
   `explain.Build` + `compliance.Export`.

## When in doubt

- Mirror what `internal/engine/engine.go` already does.
- Check the corresponding sibling project (likely Praxis or Mnemos) for
  the equivalent pattern.
- If still unsure, write a 3-line comment explaining the constraint
  rather than guessing.
