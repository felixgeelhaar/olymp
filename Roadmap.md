# Olymp Roadmap

## Status

| Phase | Theme | Status |
|---|---|---|
| 1 | Runtime primitive (MVP) | ✅ shipped |
| 2 | Safety, steering & resilience | ✅ shipped |
| 3 | Runtime ecosystem | ✅ shipped |

## Phase 1 — Runtime primitive (MVP)

Goal: prove that a thin orchestration layer over Mnemos / Chronos / Nous /
Praxis is dramatically more useful than any of them alone, by closing the
loop end-to-end with a single API and a working agent surface.

- [x] `OlympAPI` (Submit / Inspect / Steer / Stream) with stable `Run.ID`
- [x] Loop engine driving observe → understand → decide → act → learn
- [x] Cognitive-stack adapters for Mnemos / Chronos / Nous / Praxis (HTTP)
- [x] Run state machine via `statekit`
- [x] Session + goal state persisted across iterations
- [x] Outcome writeback: every Praxis action lands as a Mnemos event
- [x] CLI with task-shaped wrappers (`olymp explain`, `olymp fix`)
- [x] Multi-backend storage (memory / SQLite / Postgres)
- [x] Single audit trail spanning all four layers per run

## Phase 2 — Safety, steering & resilience

- [x] Approval gates: `awaiting_approval` state with `Engine.Resume`
- [x] Kill switch (`olymp halt`) flushing pending approvals
- [x] Run replay for decision-determinism validation
- [x] Per-layer circuit breakers (`fortify/circuitbreaker`)
- [x] Rate limiting per intent + per caller (`fortify/ratelimit`)
- [x] Decision explainability surface (`olymp explain run-<id>`)
- [x] Rollback primitives: compensating Praxis actions on regression signals

## Phase 3 — Runtime ecosystem

- [x] MCP-compatible runtime surface (7 tools via `mcp-go`)
- [x] Multi-tenant scopes (org / team / user) with isolated runs + audit
- [x] Plugin architecture for out-of-tree IntentTypes + layer adapters
- [x] Federation (`plugin.Peer` — one Olymp runtime as another's `NousPort`)
- [x] Distributed loop runner: queue + leasing + heartbeat workers
- [x] Compliance audit export (NDJSON) + PII redactor + dashboards
- [x] axi-go effect kernel wrapping every MCP tool
- [x] agent-go `AgentDescriptor` for peer-agent discovery
- [x] sqlc-generated typed queries for SQLite + Postgres

## Beyond v1

Items not in scope for the launch but plausible follow-ons:

- SQL-backed `scheduler.Queue` (Postgres `SELECT FOR UPDATE SKIP LOCKED`)
- gRPC surface mirroring the HTTP API
- Web dashboard (consumes `/v1/agent-descriptor` + `/v1/runs/stream`)
- Hot-reload of plugin contributions
- LangGraph / DSPy adapter samples in `examples/`
- Token-budget enforcement at the loop engine (currently per-MCP-tool only)
