# AGENTS.md — Working in this codebase

This file is a contract for AI coding agents (Claude Code, Codex, etc.) that
edit Olymp. It mirrors the conventions Mnemos / Chronos / Nous / Praxis
follow so an agent fluent in one is fluent in all.

## Hard rules

1. **Single responsibility per layer.** Olymp is the runtime. It does not
   remember (Mnemos), perceive (Chronos), decide (Nous), or execute (Praxis).
   Anything resembling those concerns gets pushed back to its layer.
2. **Domain has no I/O.** `internal/domain/` imports only stdlib + `uuid` +
   `statekit`. New imports there require a TDD update.
3. **Repos via ports, never concrete backends.** Anything outside
   `internal/store/<backend>/` imports `internal/ports`. Backend selection
   happens once, in `internal/store.Open`.
4. **Loop integrity is non-negotiable.** Every iteration must traverse all
   five stages or terminate with a structured `RunError`. Skipping
   `learning` is forbidden — the outcome must reach Mnemos.
5. **Provenance is non-negotiable.** Every loop step writes a
   `ProvenanceStep` with inputs, outputs, layer, layer_ref, and timing.
6. **Idempotency is non-negotiable.** `Engine.Run` against the same `Run.ID`
   never produces double-effect actions. `outcome.Writer` deduplicates by
   `(run_id, action_id)`.
7. **Foundation libraries.** `bolt`, `fortify/{retry,circuitbreaker,ratelimit}`,
   `statekit`, `axi-go`, `mcp-go`, `agent-go`, `sqlc`. Don't roll your own
   when one of these covers the use case.

## Layout

See [README.md § Project layout](README.md#project-layout). Brief recap:

- `cmd/olymp/` — CLI + serve + mcp + axi-go kernel wiring
- `internal/domain/` — pure types + Run FSM
- `internal/engine/` — the loop
- `internal/store/{memory,sqlite,postgres}/` — backends; memory is the
  canonical reference, SQL backends re-run `internal/store/suite/`
- `internal/adapters/{mnemos,chronos,nous,praxis,httpx,fake}/` — cognitive
  layer ports + HTTP clients + in-memory fakes for tests
- `client/` — Go SDK consumed by CLI + plugins + agents
- `plugin/` — public extension surface (intent types, layer overrides, federation)

## Working conventions

### Tests

- Standard library `testing` only. Table-driven where it improves clarity.
- In-memory SQLite (`:memory:`) for store integration so tests exercise the
  real driver. Postgres tests are env-guarded by `OLYMP_TEST_PG_DSN`.
- Domain, intent, audit, outcome, resilience packages are pure — fast unit
  tests, no fixtures heavier than a `time.Time`.
- API tests use `httptest.Server` over the in-memory store + fake layers.
- Engine tests use `internal/adapters/fake/` to drive the loop end-to-end.

### Errors

- Structured `domain.RunError{Code, Message, Layer, Cause, Retryable}`
  inside the engine.
- CLI surfaces `OlympError{Code, Message, Cause, Hint}` with stable exit
  codes. `OLYMP_VERBOSE=1` reveals the cause chain.

### Logging

- Every service receives a `*bolt.Logger` via constructor injection. Loop
  step logs use the canonical fields from `observability.LoopFields`.
  Never `log.Println`, never raw `slog`.

### State machines

- `Run` lifecycle goes through `statekit` (`internal/domain/run_fsm.go`).
  Any new transition lands in the FSM first, then wherever the engine
  drives it.

### sqlc

- Hand-written backends in `internal/store/{sqlite,postgres}/*.go` are the
  canonical implementations today.
- `sqlc.yaml` + `sql/{sqlite,postgres}/queries.sql` produce typed mirrors
  in `internal/store/{sqlite,postgres}/sqlcgen/` via `make sqlc`.
- Don't hand-edit anything inside `sqlcgen/` — it's regenerated.

### MCP tools

- Add tool registration in `cmd/olymp/mcp.go` AND its descriptor in
  `cmd/olymp/axikernel.go::mcpTools()` (effect, idempotency).
- Every tool dispatches through the axi-go kernel for effect gating +
  budget enforcement; the direct-dispatch fallback is a safety net for
  kernel construction failure, not the primary path.

## What NOT to do

- Don't add a new cognitive layer here. Add it as a port in
  `internal/ports/layers.go` and ship the adapter under
  `internal/adapters/<name>/`.
- Don't store memories / signals / decisions / actions in Olymp's tables.
  Only references (`layer`, `layer_ref`) and outcomes belong here.
- Don't bypass the engine's `Run.Validate()` + provenance writes by
  calling `repos.Runs.Save` from outside the engine.
- Don't add new HTTP routes outside `internal/api/http.go`.
- Don't rename a `domain.RunStatus` constant without updating the FSM and
  every backend's column-write paths.
