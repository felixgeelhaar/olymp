# Olymp — Technical Design Document (TDD)

This document specifies the domain model, services, contracts, storage, and core flows of Olymp. It is the authoritative reference for what Olymp *is* at the code level.

> **Scope reminder.** Olymp is the **runtime layer** of the cognitive stack (Mnemos · Chronos · Nous · Praxis). It does not remember, perceive, decide, or execute. It composes the four into a single closed-loop runtime. Memory lives in Mnemos. Pattern detection lives in Chronos. Decision logic lives in Nous. Side effects live in Praxis.

---

## 1. Purpose

Olymp is the system that:

> Drives the cognitive loop — observe → understand → decide → act → learn — end-to-end, providing a single surface to submit intents, inspect runs, steer behaviour, and stream events across Mnemos / Chronos / Nous / Praxis.

It is responsible for:

- exposing a stable `OlympAPI` (`Submit`, `Inspect`, `Steer`, `Stream`).
- modelling each `Run` as a typed state machine over the cognitive loop.
- holding **session and goal state** across loop iterations.
- composing the four cognitive layers via repository-style **ports**.
- enforcing **runtime-level safety** (approvals, kill switch, rate limits) on top of Praxis policy.
- writing every loop step into a **single audit trail** spanning all four layers.
- closing the loop by ensuring every Praxis outcome lands as a Mnemos event.

## 2. Core Responsibilities

| Responsibility       | Description |
|----------------------|-------------|
| **Loop engine**      | Drives the run state machine across the four cognitive layers. |
| **Run registry**     | Tracks every active and historical run with its session + goal state. |
| **Layer adapters**   | Talks to Mnemos / Chronos / Nous / Praxis via stable ports. |
| **Intent routing**   | Maps a typed intent to the loop variant that fulfils it. |
| **Steering**         | Pauses, resumes, cancels, approves, denies in-flight runs. |
| **Audit + provenance** | Single chain (Memory → Signal → Decision → Action → Outcome) per step. |
| **Outcome closure**  | Guarantees every terminal action produces a Mnemos event. |
| **Streaming**        | Live event feed of run progress for UIs, agents, dashboards. |

---

## 3. Domain Model (DDD)

### 3.1 Aggregate: `Run`

```go
type Run struct {
    ID            string                 // stable; the idempotency key for the loop
    Intent        Intent                 // what the caller asked for
    Session       SessionRef             // the session this run belongs to
    Caller        CallerRef              // who initiated (agent, user, scheduler, plugin)
    Scope         []string               // permission scopes presented by the caller
    Status        RunStatus              // FSM state
    Iteration     int                    // current loop iteration (0-indexed)
    Goal          Goal                   // structured goal state (immutable per run)
    Provenance    Provenance             // accumulating chain across iterations
    LastError     *RunError
    StartedAt     time.Time
    UpdatedAt     time.Time
    CompletedAt   *time.Time
}
```

#### `RunStatus` (state machine)

```go
type RunStatus string

const (
    StatusPending      RunStatus = "pending"       // accepted, not yet scheduled
    StatusObserving    RunStatus = "observing"     // pulling memories from Mnemos
    StatusUnderstanding RunStatus = "understanding" // pulling signals from Chronos
    StatusDeciding     RunStatus = "deciding"      // calling Nous
    StatusAwaitingApproval RunStatus = "awaiting_approval" // human-in-the-loop gate
    StatusActing       RunStatus = "acting"        // calling Praxis
    StatusLearning     RunStatus = "learning"      // writing outcome back to Mnemos
    StatusCompleted    RunStatus = "completed"
    StatusFailed       RunStatus = "failed"
    StatusCancelled    RunStatus = "cancelled"
    StatusPaused       RunStatus = "paused"        // steered to halt; can be resumed
)
```

Allowed transitions:

```
pending           → observing, cancelled
observing         → understanding, paused, failed
understanding     → deciding, paused, failed
deciding          → acting, awaiting_approval, learning, paused, failed
awaiting_approval → acting, cancelled
acting            → learning, paused, failed
learning          → completed, observing (next iteration), failed
paused            → observing | understanding | deciding | acting | learning (resume to last step)
completed         → (terminal)
failed            → (terminal)
cancelled         → (terminal)
```

The `learning → observing` edge is what makes Olymp a *loop*. Whether to iterate again is decided by the loop policy (§5.3) based on the goal state and the latest outcome.

### 3.2 Aggregate: `Intent`

```go
type Intent struct {
    Type    IntentType             // "explain" | "remediate" | "investigate" | "plan" | "act" | "verify" | ...
    Subject string                 // the entity the intent is about (e.g. "payments-latency")
    Payload map[string]any         // free-form, validated per IntentType
}
```

Intent types are registered, typed, and schema-validated. The registry is the source of truth for what Olymp can be asked to do.

### 3.3 Value Object: `Goal`

```go
type Goal struct {
    Description string                 // human-readable
    Criteria    []GoalCriterion        // structured success conditions
    MaxIterations int                  // bound on loop iterations; default 5
    Deadline    *time.Time             // optional wall-clock deadline
}

type GoalCriterion struct {
    Kind    string  // "signal_resolved" | "claim_confirmed" | "action_succeeded" | "user_accepted"
    Subject string  // signal id, claim id, action id, or user prompt
    Operator string // "eq" | "lte" | "gte" | ...
    Value   any
}
```

`Goal` is immutable per run. Re-submitting under the same `Run.ID` reuses the original goal.

### 3.4 Value Object: `Provenance`

```go
type Provenance struct {
    Steps []ProvenanceStep
}

type ProvenanceStep struct {
    Iteration   int
    Stage       RunStatus              // observing | understanding | deciding | acting | learning
    StartedAt   time.Time
    CompletedAt time.Time
    Inputs      map[string]any         // memories, signals, decisions read at this step
    Outputs     map[string]any         // claim IDs, signal IDs, action IDs produced
    LayerRef    LayerRef               // which underlying system was invoked
    Error       *RunError
}

type LayerRef struct {
    Layer string // "mnemos" | "chronos" | "nous" | "praxis" | "olymp"
    ID    string // remote object ID (claim/signal/decision/action) when applicable
}
```

`Provenance` is the audit trail. Every step records inputs, outputs, and the cross-layer reference. The full chain `Memory → Signal → Decision → Action → Outcome` is reconstructable from `Provenance.Steps` alone.

### 3.5 Value Object: `RunSnapshot`

```go
type RunSnapshot struct {
    Run        Run
    Memories   []MemoryRef    // currently surfaced from Mnemos
    Signals    []SignalRef    // currently surfaced from Chronos
    Decisions  []DecisionRef  // produced by Nous in this run
    Actions    []ActionRef    // dispatched to Praxis in this run
    Outcomes   []OutcomeRef   // events written back to Mnemos
    Timeline   []ProvenanceStep
}
```

`RunSnapshot` is what `Inspect` returns. The runtime composes it from local state plus `Get`/`List` calls into the four layers.

### 3.6 Value Object: `RunEvent`

```go
type RunEvent struct {
    RunID     string
    Iteration int
    Stage     RunStatus
    Kind      string                 // "step_started" | "step_completed" | "approval_required" | "outcome_written" | ...
    Payload   map[string]any
    Timestamp time.Time
}
```

`RunEvent` is the unit emitted by `Stream`. Every transition and every layer interaction produces an event.

### 3.7 Value Object: `SteerCommand`

```go
type SteerCommand struct {
    Kind   string  // "pause" | "resume" | "cancel" | "approve" | "deny"
    Reason string
    Caller CallerRef
}
```

### 3.8 Value Object: `CallerRef`

```go
type CallerRef struct {
    Type string  // "agent" | "user" | "scheduler" | "plugin"
    ID   string
    Name string
}
```

Mirrors `praxis.CallerRef` to keep the audit vocabulary consistent across the stack.

### 3.9 Value Object: `RunError`

```go
type RunError struct {
    Code    string  // "layer_unavailable" | "validation_failed" | "policy_denied" | "deadline_exceeded" | "max_iterations_exceeded" | "cancelled" | ...
    Message string
    Layer   string                 // "mnemos" | "chronos" | "nous" | "praxis" | "olymp"
    Cause   map[string]any
    Retryable bool
}
```

---

## 4. The Public API

```go
type OlympAPI interface {
    Submit(ctx context.Context, intent Intent) (Run, error)
    Inspect(ctx context.Context, runID string) (RunSnapshot, error)
    Steer(ctx context.Context, runID string, command SteerCommand) error
    Stream(ctx context.Context, filter RunFilter) (<-chan RunEvent, error)
}
```

Four verbs. Anything else lives in the four layers below.

---

## 5. Core Flows

### 5.1 Submit

```
1. receive Intent
2. validate Intent against registered IntentType schema (else: reject "validation_failed")
3. resolve / create Session
4. derive Run.ID (caller-supplied or generated; idempotency key for the loop)
5. check existing Run by ID:
     - terminal → return as-is
     - in-flight → return current snapshot
6. construct Run with Goal from IntentType policy
7. persist Run (status: pending)
8. enqueue Run on the loop runner
9. return Run
```

`Submit` is non-blocking by default. Synchronous mode (block until terminal) is opt-in via `RunFilter` on `Stream`.

### 5.2 Inspect

```
1. load Run by ID
2. fan-out to layer adapters in parallel:
     - Mnemos: ListMemoriesByRun(runID)
     - Chronos: ListSignalsByRun(runID)
     - Nous: ListDecisionsByRun(runID)
     - Praxis: ListActionsByRun(runID)
3. read Provenance.Steps
4. assemble RunSnapshot
5. return
```

Layer cross-references are resolved by `runID` tags applied at write time. Each layer must accept and persist a free-form `metadata.run_id` field.

### 5.3 Loop Engine

The loop runs one iteration end-to-end before deciding whether to continue:

```
for iteration = 0; ; iteration++:
  if iteration >= Goal.MaxIterations:        → fail "max_iterations_exceeded"
  if deadlineExceeded(Goal.Deadline):        → fail "deadline_exceeded"
  if cancelled or paused:                    → terminal or wait

  // OBSERVING
  transition → observing
  memories := mnemos.Recall(Run.Goal, Run.Session)
  appendProvenance(observing, memories)

  // UNDERSTANDING
  transition → understanding
  signals := chronos.Signals(Run.Goal, Run.Session, since=lastIteration)
  appendProvenance(understanding, signals)

  // DECIDING
  transition → deciding
  decision := nous.Decide(Run.Goal, memories, signals, history=Provenance)
  appendProvenance(deciding, decision)

  // APPROVAL GATE (Phase 2+)
  if decision.RequiresApproval:
    transition → awaiting_approval
    wait for SteerCommand{approve|deny|cancel}
    if denied or cancelled:                  → terminal

  // ACTING
  transition → acting
  for action in decision.Actions:
    result := praxis.Execute(action.with(metadata.run_id=Run.ID))
    appendProvenance(acting, result)
    if result.Status == failed and !action.Retryable:
                                             → fail "action_failed"

  // LEARNING
  transition → learning
  for result in actionResults:
    mnemos.Append(outcomeEventFrom(result, Run.ID))
  appendProvenance(learning, outcomes)

  // GOAL EVALUATION
  if Goal.Satisfied(memories, signals, decision, results):
    transition → completed
    return
```

The engine is intentionally linear, single-threaded per run, and fully observable. Concurrency is *across* runs, not *within* a single loop.

### 5.4 Steer

```
1. load Run by ID
2. validate command against current Status
3. execute:
     - pause   → set status paused, snapshot last stage
     - resume  → restore last stage, continue loop
     - cancel  → terminal cancelled, write provenance step
     - approve → unblock awaiting_approval gate
     - deny    → reject decision, terminal cancelled
4. emit RunEvent{kind: "steered", command, ...}
5. persist Run
```

Steering is the only mutation an external caller can perform on an in-flight run after submission. All other state changes come from the loop engine.

### 5.5 Stream

```
1. open subscriber channel
2. attach to event bus filtered by RunFilter
3. on each transition + each layer interaction:
     - publish RunEvent
4. close on caller cancel or runtime shutdown
```

The event bus is in-process for Phase 1; Phase 3 ships a Kafka-backed bus shared with Chronos's existing event backbone.

### 5.6 Closing the Loop

After every Praxis terminal action, Olymp ensures a Mnemos event lands, even if Praxis's own writeback succeeded. Olymp tags the event with:

```json
{
  "type":           "olymp.outcome",
  "run_id":         "...",
  "iteration":      2,
  "intent":         { "type": "remediate", "subject": "payments-latency" },
  "action_id":      "...",
  "capability":     "rollout_restart",
  "status":         "succeeded",
  "decision_id":    "...",
  "memory_refs":    [ "..." ],
  "signal_refs":    [ "..." ],
  "timestamp":      "2026-04-27T12:34:56Z"
}
```

Mnemos treats it as a normal event. The next iteration's `Recall` surfaces it and Nous's next decision is grounded in what was already done.

### 5.7 Failure & Retries

- Layer-level transient failures (Mnemos / Chronos / Nous / Praxis) are wrapped in `fortify/retry` with exponential backoff; the loop step is retried, not the whole run.
- A non-retryable failure transitions the run to `failed` with a structured `RunError`.
- Retrying a *failed* run produces a *new* run whose ID derives from the original (`<orig>-r1`), preserving the original audit trail.
- Re-submitting a *succeeded* run with the same ID is a no-op (returns the original snapshot).

---

## 6. Internal Services

| Service | Responsibility |
|---|---|
| **RunRegistry**       | In-memory + persisted catalog of runs and their state. |
| **IntentValidator**   | Validate `Intent` payloads against registered `IntentType` schemas. |
| **LoopEngine**        | Drives the FSM in §5.3 for one run at a time. |
| **MnemosAdapter**     | Talks to Mnemos via `internal/ports/MnemosPort`. |
| **ChronosAdapter**    | Talks to Chronos via `internal/ports/ChronosPort`. |
| **NousAdapter**       | Talks to Nous via `internal/ports/NousPort`. |
| **PraxisAdapter**     | Talks to Praxis via `internal/ports/PraxisPort`. |
| **ApprovalGate**      | Suspends a run on `awaiting_approval`; resumes on `Steer(approve)`. |
| **EventBus**          | Publishes `RunEvent`s; backs `Stream`. |
| **AuditLog**          | Append-only record of run lifecycle and provenance steps. |
| **Scheduler**         | Picks pending runs off the queue; in Phase 1 a single in-process worker. |

Each service is independently testable. Cross-service communication goes through the domain types in §3 — no shared mutable state.

---

## 7. Storage (multi-backend, `sqlc`-typed)

Olymp is **storage-agnostic at the domain layer** and ships three backends, mirroring Praxis & Chronos:

| Backend | Use case | Config |
|---|---|---|
| `memory`   | Tests, ephemeral runs, examples | `OLYMP_DB_TYPE=memory` |
| `sqlite`   | Local-first / single-user / embedded (default) | `OLYMP_DB_TYPE=sqlite`, default `olymp.db` |
| `postgres` | Production / team / multi-tenant | `OLYMP_DB_TYPE=postgres` |

Domain code talks only to repository **ports** in `internal/ports/`. Each backend lives in `internal/store/<backend>/` and is selected at startup.

### 7.1 Repository ports

```go
// internal/ports/repo.go
type RunRepo interface {
    Save(ctx context.Context, r domain.Run) error
    Get(ctx context.Context, id string) (domain.Run, error)
    UpdateStatus(ctx context.Context, id string, s domain.RunStatus) error
    AppendProvenance(ctx context.Context, id string, step domain.ProvenanceStep) error
    List(ctx context.Context, filter domain.RunFilter) ([]domain.Run, error)
}

type SessionRepo interface {
    Upsert(ctx context.Context, s domain.Session) error
    Get(ctx context.Context, id string) (domain.Session, error)
    List(ctx context.Context, filter domain.SessionFilter) ([]domain.Session, error)
}

type IntentTypeRepo interface {
    Register(ctx context.Context, t domain.IntentType) error
    Get(ctx context.Context, name string) (domain.IntentType, error)
    List(ctx context.Context) ([]domain.IntentType, error)
}

type AuditRepo interface {
    Append(ctx context.Context, e domain.AuditEvent) error
    ListForRun(ctx context.Context, runID string) ([]domain.AuditEvent, error)
    Search(ctx context.Context, q domain.AuditQuery) ([]domain.AuditEvent, error)
}

type ApprovalRepo interface { // Phase 2+
    Pending(ctx context.Context, runID string) (*domain.ApprovalRequest, error)
    Resolve(ctx context.Context, runID string, decision domain.ApprovalDecision) error
}
```

### 7.2 Layer ports (out-of-tree adapters)

The four cognitive layers are addressed via explicit ports so the runtime can be tested in isolation and the layers can evolve independently.

```go
// internal/ports/layers.go
type MnemosPort interface {
    Recall(ctx context.Context, q domain.MemoryQuery) ([]domain.MemoryRef, error)
    Append(ctx context.Context, e domain.OutcomeEvent) error
    Get(ctx context.Context, id string) (domain.MemoryRef, error)
}

type ChronosPort interface {
    Signals(ctx context.Context, q domain.SignalQuery) ([]domain.SignalRef, error)
    Get(ctx context.Context, id string) (domain.SignalRef, error)
}

type NousPort interface {
    Decide(ctx context.Context, in domain.DecisionRequest) (domain.DecisionRef, error)
    Get(ctx context.Context, id string) (domain.DecisionRef, error)
}

type PraxisPort interface {
    ListCapabilities(ctx context.Context) ([]domain.CapabilityRef, error)
    Execute(ctx context.Context, a domain.ActionRequest) (domain.ActionResult, error)
    DryRun(ctx context.Context, a domain.ActionRequest) (domain.SimulationRef, error)
}
```

In Phase 1 each port has a single implementation that calls the layer's HTTP / gRPC client. In Phase 3 multiple implementations may coexist (e.g. multiple Mnemos registries).

### 7.3 `sqlc` configuration

`sqlc.yaml` defines two engines, one per relational backend:

```yaml
version: "2"
sql:
  - engine: "sqlite"
    queries: "sql/sqlite/queries.sql"
    schema:  "internal/store/sqlite/migrations/"
    gen:
      go:
        package: "sqlcgen"
        out:     "internal/store/sqlite/sqlcgen"
        sql_package: "database/sql"
        emit_interface: true
        emit_json_tags: true
  - engine: "postgresql"
    queries: "sql/postgres/queries.sql"
    schema:  "internal/store/postgres/migrations/"
    gen:
      go:
        package: "sqlcgen"
        out:     "internal/store/postgres/sqlcgen"
        sql_package: "pgx/v5"
        emit_interface: true
        emit_json_tags: true
```

The `memory` backend is hand-written and is the canonical reference — the SQL backends round-trip every test the memory backend passes.

### 7.4 Schema — Postgres

```sql
create table sessions (
  id           text primary key,
  caller_type  text not null,
  caller_id    text not null,
  metadata     jsonb not null default '{}',
  created_at   timestamptz not null,
  updated_at   timestamptz not null
);

create table intent_types (
  name         text primary key,
  description  text,
  schema       jsonb not null,
  policy       jsonb not null default '{}',
  registered_at timestamptz not null
);

create table runs (
  id            text primary key,
  intent_type   text not null references intent_types(name),
  intent_payload jsonb not null,
  session_id    text not null references sessions(id),
  caller_type   text not null,
  caller_id     text not null,
  scope         text[] not null default '{}',
  status        text not null,
  iteration     integer not null default 0,
  goal          jsonb not null,
  last_error    jsonb,
  started_at    timestamptz not null,
  updated_at    timestamptz not null,
  completed_at  timestamptz
);

create index idx_runs_session  on runs (session_id, started_at desc);
create index idx_runs_status   on runs (status, updated_at desc);
create index idx_runs_caller   on runs (caller_type, caller_id, started_at desc);

create table provenance_steps (
  id           uuid primary key,
  run_id       text not null references runs(id) on delete cascade,
  iteration    integer not null,
  stage        text not null,
  layer        text not null,           -- "mnemos" | "chronos" | "nous" | "praxis" | "olymp"
  layer_ref    text,                    -- remote object id
  inputs       jsonb not null default '{}',
  outputs      jsonb not null default '{}',
  error        jsonb,
  started_at   timestamptz not null,
  completed_at timestamptz
);

create index idx_prov_run_iter on provenance_steps (run_id, iteration, started_at);
create index idx_prov_layer    on provenance_steps (layer, started_at desc);

create table audit_events (
  id          uuid primary key,
  run_id      text not null references runs(id) on delete cascade,
  kind        text not null,            -- "submitted", "transitioned", "steered", "approval_required", "outcome_written", ...
  detail      jsonb not null,
  created_at  timestamptz not null
);

create index idx_audit_run   on audit_events (run_id, created_at);
create index idx_audit_kind  on audit_events (kind, created_at desc);

create table approvals (                -- Phase 2+
  run_id       text primary key references runs(id) on delete cascade,
  required_at  timestamptz not null,
  resolved_at  timestamptz,
  decision     text,                    -- "approve" | "deny"
  reason       text,
  resolver     jsonb
);
```

### 7.5 Schema — SQLite

The same logical schema, adapted to SQLite types:

```sql
create table sessions (
  id           text primary key,
  caller_type  text not null,
  caller_id    text not null,
  metadata     text not null default '{}',  -- JSON
  created_at   text not null,
  updated_at   text not null
);

create table intent_types (
  name         text primary key,
  description  text,
  schema       text not null,               -- JSON
  policy       text not null default '{}',  -- JSON
  registered_at text not null
);

create table runs (
  id             text primary key,
  intent_type    text not null references intent_types(name),
  intent_payload text not null,             -- JSON
  session_id     text not null references sessions(id),
  caller_type    text not null,
  caller_id      text not null,
  scope          text not null default '[]',  -- JSON array
  status         text not null,
  iteration      integer not null default 0,
  goal           text not null,             -- JSON
  last_error     text,                      -- JSON
  started_at     text not null,
  updated_at     text not null,
  completed_at   text
);

create index idx_runs_session  on runs (session_id, started_at desc);
create index idx_runs_status   on runs (status, updated_at desc);
create index idx_runs_caller   on runs (caller_type, caller_id, started_at desc);

create table provenance_steps (
  id           text primary key,            -- uuid as text
  run_id       text not null references runs(id) on delete cascade,
  iteration    integer not null,
  stage        text not null,
  layer        text not null,
  layer_ref    text,
  inputs       text not null default '{}',  -- JSON
  outputs      text not null default '{}',  -- JSON
  error        text,                        -- JSON
  started_at   text not null,
  completed_at text
);

create index idx_prov_run_iter on provenance_steps (run_id, iteration, started_at);
create index idx_prov_layer    on provenance_steps (layer, started_at desc);

create table audit_events (
  id         text primary key,              -- uuid as text
  run_id     text not null references runs(id) on delete cascade,
  kind       text not null,
  detail     text not null,                 -- JSON
  created_at text not null
);

create index idx_audit_run   on audit_events (run_id, created_at);
create index idx_audit_kind  on audit_events (kind, created_at desc);

create table approvals (                    -- Phase 2+
  run_id       text primary key references runs(id) on delete cascade,
  required_at  text not null,
  resolved_at  text,
  decision     text,
  reason       text,
  resolver     text                         -- JSON
);
```

### 7.6 Memory backend

Pure in-process implementation in `internal/store/memory/`. Used by:

- unit tests of the loop engine and adapters
- examples in docs
- the `OLYMP_DB_TYPE=memory` runtime mode (ephemeral)

It is the canonical reference for the repository contract; the integration tests in `internal/store/sqlite/` and `internal/store/postgres/` re-run the same shared test suite to guarantee parity.

### 7.7 Selection at runtime

```go
// internal/store/store.go
func Open(ctx context.Context, cfg config.DB) (Repos, error) {
    switch cfg.Type {
    case "memory":
        return memory.New(), nil
    case "sqlite":
        return sqlite.Open(ctx, cfg.Conn)
    case "postgres":
        return postgres.Open(ctx, cfg.Conn)
    default:
        return nil, fmt.Errorf("unknown db type: %q", cfg.Type)
    }
}
```

Domain code never imports a concrete backend — only the repository ports.

---

## 8. The System Loop (cognitive stack)

Olymp owns the loop. It sits *over* the four layers:

```
         ┌──────────────────────────────────────────────────┐
         │                     OLYMP                         │
         │   Submit / Inspect / Steer / Stream               │
         │                                                   │
         │   ┌─────────────── LoopEngine ───────────────┐   │
         │   │ observe → understand → decide → act →    │   │
         │   │                                  learn   │   │
         │   └───────────────────────────────────────────┘   │
         └────────┬──────────┬──────────┬──────────┬─────────┘
                  ▼          ▼          ▼          ▼
              Mnemos     Chronos      Nous       Praxis
             (memory)   (patterns)  (decision) (execution)
                  ▲                                │
                  └────────── outcome ─────────────┘
```

Each step is observable and replayable. The loop closes only when `learning` writes the outcome back to Mnemos.

---

## 9. Design Principles

### 9.1 Single responsibility

```
Mnemos  = memory
Chronos = patterns
Nous    = decisions
Praxis  = execution
Olymp   = the loop
```

Olymp does not interpret claims, detect anomalies, choose plans, or run side effects. Those belong to the layers below.

### 9.2 Ports, not couplings

The four layers are addressed via four ports. The loop engine is testable end-to-end with in-memory fakes for all four, and production deployments swap them for real clients.

### 9.3 Loop integrity

Every iteration must traverse all five stages or terminate with a structured error. Skipping `learning` is forbidden — the loop is the product, and the outcome must reach Mnemos.

### 9.4 Steering by default

Every running loop is interruptible. Pause, resume, cancel, approve, deny. The `RunStatus` FSM admits these transitions explicitly.

### 9.5 Provenance is non-negotiable

Every loop step writes a `ProvenanceStep` with inputs, outputs, and a `LayerRef`. The full chain `Memory → Signal → Decision → Action → Outcome` is reconstructable from a single run's provenance alone.

### 9.6 Local-first defaults

A single binary, SQLite, in-process Mnemos / Chronos / Nous / Praxis (or HTTP clients to local processes), is a complete deployment. Production scales out by swapping backends and ports — not by changing the domain.

---

## 9.7 Foundation Libraries

Olymp stands on the same `felixgeelhaar/*` library set as Mnemos, Chronos, Nous, and Praxis so that every system in the cognitive stack shares one operational vocabulary. **Don't roll your own** when one of these covers the use case.

| Library | Role in Olymp | Where it appears |
|---|---|---|
| [`bolt`](https://github.com/felixgeelhaar/bolt) | Structured logging | Every service (`internal/...`), CLI, HTTP server. Never `log.Println`, never raw `slog`. |
| [`fortify/retry`](https://github.com/felixgeelhaar/fortify) | Retry with backoff + jitter | Layer adapters (Mnemos / Chronos / Nous / Praxis clients), HTTP client in `client/` |
| [`fortify/circuit`](https://github.com/felixgeelhaar/fortify) | Circuit breakers | Per-layer protection so one slow/down layer cannot stall the runtime (Phase 2) |
| [`statekit`](https://github.com/felixgeelhaar/statekit) | State machines | `Run` lifecycle (the FSM in §3.1); intent registration FSM |
| [`axi-go`](https://github.com/felixgeelhaar/axi-go) | HTTP framework | `olymp serve` — the public HTTP surface; mirrors Mnemos's & Praxis's `axikernel` pattern in `cmd/olymp/axikernel.go` |
| [`mcp-go`](https://github.com/felixgeelhaar/mcp-go) | MCP server | Phase 3: `Submit` / `Inspect` / `Steer` / `Stream` exposed as MCP tools so Claude / Codex consume Olymp natively |
| [`agent-go`](https://github.com/felixgeelhaar/agent-go) | Agent framework | Olymp is the runtime backend an `agent-go` agent calls; intent descriptors are consumable by `agent-go` |
| [`sqlc`](https://sqlc.dev) | Typed SQL | All Postgres + SQLite queries (see §7) |

### 9.7.1 Concretely

- **Logging** — every service receives a `*bolt.Logger` via constructor injection. No package-level `log.Default()`. Run ID and iteration are fixed log fields on every loop step.
- **State machines** — `internal/domain/run_fsm.go` defines the `Run` FSM using `statekit`; transitions in §3.1 are not free-form, they go through the FSM.
- **HTTP** — `cmd/olymp/axikernel.go` wires routes, middleware, and error mapping using `axi-go`, mirroring `Praxis/cmd/praxis/axikernel.go`.
- **Retries** — `fortify/retry.Config` is the only retry primitive. Same defaults as Praxis: 5xx + 429 retry, 4xx fail fast, exponential backoff with jitter. Applied per-layer in adapters.
- **Circuit breakers** — `fortify/circuit` wraps each layer adapter in Phase 2; an open circuit transitions the run to `failed` with `RunError.Code = "layer_unavailable"`.
- **MCP** — Phase 3 wires `mcp-go` to expose the runtime as MCP tools. The Phase-1 HTTP API and the Phase-3 MCP surface share the same loop engine underneath.
- **Agents** — Olymp publishes intent descriptors compatible with `agent-go` so any `agent-go` agent can `Submit`, `Inspect`, `Steer`, `Stream` without bespoke glue.

---

## 10. MVP Scope (Phase 1)

Build only:

- `OlympAPI` (`Submit` / `Inspect` / `Steer` / `Stream`) over HTTP via `axi-go`.
- `Run` FSM and loop engine for one run at a time, single-process worker.
- Ports + adapters for Mnemos / Chronos / Nous / Praxis (HTTP clients).
- Intent registry with two built-in types: `explain`, `remediate`.
- Provenance writes at every loop step.
- Outcome writeback to Mnemos for every Praxis terminal action.
- Memory + SQLite + Postgres backends.
- CLI: `olymp submit`, `olymp inspect`, `olymp steer`, `olymp stream`, plus task-shaped wrappers (`olymp explain`, `olymp fix`).

Example:

```bash
olymp submit --intent explain --subject payments-latency
olymp inspect run-7f3
olymp stream --run-id run-7f3
olymp steer run-7f3 cancel --reason "operator override"
```

That's enough to be the runtime backend for an `agent-go` agent and a clean control-plane surface for any user.

---

## 11. Final Definition

> Olymp is the AI control plane for complex systems — the runtime that drives the cognitive loop, holds session and goal state, composes Mnemos / Chronos / Nous / Praxis through stable ports, and turns four powerful primitives into one self-improving operator of real systems.
