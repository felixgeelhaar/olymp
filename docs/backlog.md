
## Domain Model & Run FSM

Phase 1 MVP. DDD domain types in `internal/domain/`: Run aggregate (id, intent, session, caller, scope, status, iteration, goal, provenance, errors, timestamps), RunStatus FSM via `statekit` (pending → observing → understanding → deciding → awaiting_approval → acting → learning → completed|failed|cancelled|paused), Intent + IntentType, Goal + GoalCriterion, Provenance + ProvenanceStep + LayerRef, RunSnapshot, RunEvent, SteerCommand, CallerRef, RunError. Pure domain — no I/O imports outside std + uuid + statekit.

---

## Repository Ports & Multi-Backend Storage

Phase 1 MVP. Repository ports in `internal/ports/repo.go` (RunRepo, SessionRepo, IntentTypeRepo, AuditRepo). Three backends behind one interface: `memory` (canonical reference, hand-written), `sqlite` (modernc.org/sqlite, default), `postgres` (pgx/v5). `sqlc.yaml` with two engines. Schema migrations for sessions, intent_types, runs, provenance_steps, audit_events. Shared test suite re-run across all three backends to guarantee parity. Selection at startup via `OLYMP_DB_TYPE` env.

---

## Cognitive Layer Adapters

Phase 1 MVP. Four ports in `internal/ports/layers.go`: MnemosPort (Recall/Append/Get), ChronosPort (Signals/Get), NousPort (Decide/Get), PraxisPort (ListCapabilities/Execute/DryRun). HTTP-client implementations in `internal/adapters/{mnemos,chronos,nous,praxis}/`. Each adapter wrapped in `fortify/retry` (5xx+429 retry, 4xx fail-fast, exponential backoff with jitter). In-memory fakes in `internal/adapters/fake/` for end-to-end loop tests.

---

## Loop Engine

Phase 1 MVP. `internal/engine/loop.go` drives one Run end-to-end through the FSM: observe (Mnemos.Recall) → understand (Chronos.Signals) → decide (Nous.Decide) → [awaiting_approval] → act (Praxis.Execute fan-out) → learn (Mnemos.Append outcome). Bounded by `Goal.MaxIterations` and `Goal.Deadline`. Goal-satisfaction evaluation between iterations. Single-threaded per run, concurrent across runs. Every step writes a ProvenanceStep. Failures wrapped in fortify/retry per layer; non-retryable transitions Run → failed with structured RunError.

---

## Intent Registry

Phase 1 MVP. Typed intent registration in `internal/intent/`. IntentTypeRepo persists registered types with JSON Schema for payload validation and per-intent loop policy (max iterations, default deadline, required scopes, approval rules). Two built-in IntentTypes shipped: `explain` (read-only loop, no Praxis execute) and `remediate` (full loop with approval gate by default). Intents validated on Submit; unknown types rejected with structured error.

---

## Public API (HTTP)

Phase 1 MVP. `OlympAPI` interface (Submit, Inspect, Steer, Stream) wired over `axi-go` in `cmd/olymp/axikernel.go`, mirroring Praxis's & Mnemos's pattern. Routes: POST /v1/runs (Submit), GET /v1/runs/:id (Inspect), POST /v1/runs/:id/steer (Steer), GET /v1/runs/stream (Stream via SSE). DTO conversion in `internal/api/`. Stable wire shape decoupled from `internal/domain`. Error mapping to OlympError + exit codes.

---

## Provenance & Audit Trail

Phase 1 MVP. Single audit chain spanning all four cognitive layers per run. `provenance_steps` table records inputs, outputs, layer, layer_ref, error per stage per iteration. `audit_events` table records lifecycle (submitted, transitioned, steered, approval_required, outcome_written). Reconstructable Memory → Signal → Decision → Action → Outcome chain from a single runID. Audit append on every transition + every layer call; writes are non-blocking but durable.

---

## Outcome Writeback to Mnemos

Phase 1 MVP. Loop's `learning` stage MUST write a structured `olymp.outcome` event into Mnemos for every Praxis terminal action, tagged with run_id, iteration, intent, action_id, capability, status, decision_id, memory_refs, signal_refs, timestamp. Idempotent by (run_id, action_id) so duplicate writebacks do not double-count. Writeback failures retry via fortify/retry; persistent failure transitions Run → failed (loop integrity is non-negotiable).

---

## Event Bus & Streaming

Phase 1 MVP. In-process EventBus publishes RunEvents on every transition + layer interaction. Stream API delivers filtered events to subscribers (RunFilter by run_id, intent type, caller, status). SSE transport on the HTTP surface. Bus is fan-out, lossless for slow subscribers (bounded buffer + drop policy on overflow with structured warning). Phase 3 swaps in Kafka-backed bus; the API stays identical.

---

## CLI & Plugin Wrappers

Phase 1 MVP. Hand-rolled CLI in `cmd/olymp/` (no Cobra), one file per subcommand, mirroring Chronos pattern. Core verbs: `olymp submit`, `olymp inspect`, `olymp steer`, `olymp stream`. Task-shaped wrappers: `olymp explain <subject>` and `olymp fix <subject>` map to typed intents. Failures return `*OlympError{Code, Message, Cause, Hint}`. `OLYMP_VERBOSE=1` reveals cause chain. Plugin surface for Claude Code / Codex calls the same OlympAPI.

---

## Observability (bolt logging)

Phase 1 MVP. Every service receives `*bolt.Logger` via constructor injection. No `log.Println`, no raw `slog`. Fixed log fields on every loop step: run_id, iteration, stage, layer, intent_type, caller_type, caller_id, duration_ms. Structured error logging with full RunError context. Health endpoint exposes per-layer adapter status (last error, last success, circuit state in Phase 2).

---

## Approval Gates & Steering Hardening

Phase 2. ApprovalRepo persists pending approvals; `awaiting_approval` Run state suspends loop until SteerCommand{approve|deny|cancel}. Per-IntentType + per-capability approval policies. Steering hardening: pause/resume restores last stage cleanly across process restarts. Kill switch (`olymp halt`) flushes pending approvals to denied + transitions in-flight runs to paused. Audit captures resolver identity + reason on every approval decision.

---

## Resilience & Circuit Breakers

Phase 2. `fortify/circuit` wraps every layer adapter (Mnemos/Chronos/Nous/Praxis). Open circuit transitions current Run → failed with `RunError.Code = "layer_unavailable"`. Per-IntentType retry policies (re-observe stale signals, re-plan rejected decisions). Rate limiting per intent type + per caller. Run replay: re-run historical loop against current memory + signals to validate decision determinism (≥95% deterministic on identical tuples).

---

## Rollback & Decision Explainability

Phase 2. Rollback primitives: when a downstream Chronos signal flags regression after an action, emit a compensating Praxis action (per-capability `compensate` handler when supported). Decision explainability surface: `olymp explain run-<id>` returns the full provenance chain Memory → Signal → Decision → Action → Outcome with citations and confidence. Powers compliance and post-mortems.

---

## MCP Runtime Surface

Phase 3. `mcp-go` exposes Submit/Inspect/Steer/Stream as MCP tools so Claude Code / Codex / any MCP client treats Olymp as native. Intent descriptors auto-published as MCP tool schemas. Phase-1 HTTP API and Phase-3 MCP surface share the same loop engine underneath. agent-go agents consume the same descriptors without bespoke glue.

---

## Multi-Tenancy & Plugin Architecture

Phase 3. Multi-tenant scopes (org/team/user) with isolated runs, sessions, audit. Plugin architecture for out-of-tree cognitive-layer adapters (multiple Mnemos registries, alternative Chronos engines). Out-of-tree IntentType handlers loaded at startup. Federation: one Olymp runtime addresses another as a peer for cross-runtime delegation.

---

## Distributed Loop Runner

Phase 3. Horizontally scalable loop workers consuming a shared run queue (Postgres-backed in v1, pluggable). Run leasing with heartbeat + automatic recovery on worker crash. Backpressure when downstream layers throttle. Replaces the Phase-1 single-process scheduler without changing the domain.

---

## Compliance Audit Export & Dashboards

Phase 3. Org-level dashboards over runs, decisions, actions, outcomes. Exportable audit trail (SOC 2 / HIPAA / GDPR friendly) with provenance chain per run. Configurable retention + redaction. Goal: pass a real compliance review on first ask.

---
