# Olymp

> The AI control plane for complex systems.

Olymp is the **runtime layer** of the four-system cognitive stack
(**Mnemos** · **Chronos** · **Nous** · **Praxis**). It drives the closed
loop *observe → understand → decide → act → learn* end-to-end, holds
session and goal state across the four cognitive systems, and exposes one
front door (CLI · HTTP · MCP) for users and agents alike.

```
[Input streams]
    ↓
Mnemos    (events → memories, knowledge graph)
    ↓
Chronos   (time series → signals, patterns, anomalies)
    ↓
Nous      (memory + signals → goals, plans, decisions)
    ↓
Praxis    (decisions → actions, results)
    ↓
[Outcomes flow back to Mnemos]

         ┌──────────────── OLYMP ────────────────┐
         │ Submit / Inspect / Steer / Stream     │
         │   drives the loop, audits everything  │
         └───────────────────────────────────────┘
```

Olymp is to AI runtimes what Kubernetes is to containers — an orchestrator
that observes state, makes decisions, and enforces desired outcomes,
continuously.

## Why

Each of the four cognitive layers solves part of the problem, but in
isolation they remain disconnected. Without a runtime, every consumer
re-implements the loop, stitches session state across four data planes,
and reconstructs an audit trail across four logs. Olymp owns the loop so
nobody else has to.

## Quickstart

### Single-binary, in-process fakes

```bash
# Build
make build

# Run with in-process fake cognitive layers (no external services)
./bin/olymp serve --demo

# In another terminal: drive the full loop
./bin/olymp explain payments-latency

# Inspect what happened
./bin/olymp inspect <run-id>

# Stream live events
./bin/olymp stream
```

### Full stack on Docker

The [`deploy/`](deploy/) directory holds a `docker-compose.yml` that boots
Olymp alongside the **real** Mnemos · Chronos · Nous · Praxis containers
(no mocks). Each layer is built from its sibling repo.

```bash
docker compose -f deploy/docker-compose.yml up --build
./deploy/demo.sh
```

See [`deploy/README.md`](deploy/README.md) for topology + ports + production
notes.

## Surfaces

| Surface | What | Location |
|---|---|---|
| **CLI** | `serve`, `submit`, `inspect`, `steer`, `stream`, `explain`, `fix`, `halt`, `mcp` | `cmd/olymp/` |
| **HTTP API** | `POST /v1/runs`, `GET /v1/runs/{id}`, `POST /v1/runs/{id}/steer`, `GET /v1/runs/stream` (SSE), `POST /v1/halt`, `GET /v1/agent-descriptor`, `GET /healthz` | `internal/api/http.go` |
| **MCP** | 7 tools over stdio: `submit_intent`, `inspect_run`, `steer_run`, `halt`, `list_intents`, `explain_run`, `agent_descriptor` | `cmd/olymp/mcp.go` |
| **Go SDK** | Typed `client.Client` matching the HTTP API | `client/` |
| **Plugin SDK** | Register custom IntentTypes, override layer adapters, federate with peer runtimes | `plugin/` |

## Architecture

DDD / hexagonal. Layers run from inside (pure) to outside (I/O):

```
        cmd/olymp                       (CLI + serve + mcp)
            │
   ┌────────┴────────┐
   ▼                 ▼
internal/api    cmd/olymp/axikernel.go  (axi-go effect kernel for MCP)
   │
   ▼
internal/engine                          (loop engine: observe→...→learn)
   │
   ├──► internal/intent                  (typed intents + JSON Schema)
   ├──► internal/audit                   (provenance + lifecycle)
   ├──► internal/outcome                 (idempotent Mnemos writeback)
   ├──► internal/eventbus                (in-process pub/sub)
   ├──► internal/observability           (bolt logging + health)
   ├──► internal/resilience              (circuit breakers + rate limit + replay)
   ├──► internal/explain                 (Memory→Signal→Decision→Action→Outcome chain)
   ├──► internal/rollback                (compensating actions)
   ├──► internal/scheduler               (queue + workers for horizontal scale)
   ├──► internal/compliance              (NDJSON export + dashboards)
   └──► internal/ports                   (repos + cognitive-layer ports)
            │
            ▼
   internal/store/{memory,sqlite,postgres}
   internal/adapters/{mnemos,chronos,nous,praxis,httpx,fake}
```

## Foundation libraries

Every system in the cognitive stack shares one operational vocabulary.
Don't roll your own when one of these covers the use case.

| Library | Role |
|---|---|
| [`bolt`](https://github.com/felixgeelhaar/bolt) | Structured logging |
| [`fortify/retry`](https://github.com/felixgeelhaar/fortify) | HTTP adapter retries (5xx + 429, expo + jitter) |
| [`fortify/circuitbreaker`](https://github.com/felixgeelhaar/fortify) | Per-layer breakers with HealthRegistry integration |
| [`fortify/ratelimit`](https://github.com/felixgeelhaar/fortify) | Per-intent + per-caller rate limiting |
| [`statekit`](https://github.com/felixgeelhaar/statekit) | Run FSM (`pending → observing → ... → completed`) |
| [`axi-go`](https://github.com/felixgeelhaar/axi-go) | Action kernel wrapping every MCP tool with effect gating + budget + evidence |
| [`mcp-go`](https://github.com/felixgeelhaar/mcp-go) | MCP stdio server |
| [`agent-go`](https://github.com/felixgeelhaar/agent-go) | `AgentDescriptor` + `Capability` for peer-agent discovery |
| [`sqlc`](https://sqlc.dev) | Typed queries (Postgres + SQLite) |

## Storage

Three backends behind one repository contract; every backend re-runs the
shared suite in `internal/store/suite/`.

| Backend | Use case | Selector |
|---|---|---|
| `memory` | Tests, ephemeral runs, examples | `OLYMP_DB_TYPE=memory` |
| `sqlite` | Local-first / single-binary / embedded | `OLYMP_DB_TYPE=sqlite` |
| `postgres` | Production / multi-tenant | `OLYMP_DB_TYPE=postgres` |

Olymp persists **only** the loop state + cross-layer references + audit
trail. Memories, signals, decisions, and actions live in the four
cognitive-stack systems they belong to.

## Multi-tenancy

Org / team / user scoping via `X-Olymp-Tenant-{Org,Team,User}` HTTP
headers (or `OLYMP_TENANT_*` env vars). Every Run, Session, audit row, and
provenance step inherits the tenant; cross-tenant access on Inspect
returns `404 not_found`.

## Plugins

Hosts that embed Olymp register custom contributions before the runtime
enters its serve loop:

```go
import "github.com/felixgeelhaar/olymp/plugin"

reg := plugin.New(intentRegistry, layers)
reg.Register(myCustomPlugin{})
finalLayers, err := reg.Boot(ctx)
```

A plugin's `Init(ctx, host)` calls `host.RegisterIntent(...)` and
`host.OverrideLayers(...)` to add IntentTypes or replace cognitive-layer
adapters. Federation is a plugin in this scheme: `plugin.Peer` is a
`NousPort` backed by a remote Olymp runtime via the Go client.

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `OLYMP_DB_TYPE` | `memory` | Backend: memory / sqlite / postgres |
| `OLYMP_DB_CONN` | (empty) | DSN — `:memory:` for SQLite, full Postgres URL otherwise |
| `OLYMP_URL` | `http://localhost:8080` | CLI client target |
| `OLYMP_MNEMOS_URL` | `http://localhost:8081` | Mnemos base URL |
| `OLYMP_CHRONOS_URL` | `http://localhost:8082` | Chronos base URL |
| `OLYMP_NOUS_URL` | `http://localhost:8083` | Nous base URL |
| `OLYMP_PRAXIS_URL` | `http://localhost:8084` | Praxis base URL |
| `OLYMP_VERBOSE` | `0` | `1` reveals full cause chain on CLI errors |
| `OLYMP_AXI_MAX_DURATION` | `5m` | Per-MCP-tool budget |
| `OLYMP_AXI_MAX_INVOCATIONS` | `1000` | Per-MCP-tool capability budget |

## Development

```bash
make fmt          # gofmt / goimports
make vet          # go vet
make lint         # golangci-lint (must be installed)
make test         # race-detector tests, no caching
make cover        # coverage report
make sqlc         # regenerate typed queries
make build        # compile binary into ./bin/olymp
make demo         # build + run with in-process fakes
make release-check
```

## Project layout

```
.
├── cmd/olymp/                 CLI + HTTP + MCP entry points
├── client/                    Go SDK
├── plugin/                    extension surface for hosts
├── internal/
│   ├── domain/                pure domain types + Run FSM
│   ├── ports/                 repository + cognitive-layer ports
│   ├── engine/                loop engine
│   ├── intent/                typed intent registry
│   ├── audit/                 provenance + lifecycle audit
│   ├── outcome/               idempotent writeback
│   ├── eventbus/              in-process pub/sub
│   ├── observability/         bolt logging + health
│   ├── resilience/            circuit breakers + rate limit + replay
│   ├── explain/               provenance chain reconstruction
│   ├── rollback/              compensating actions
│   ├── scheduler/             queue + workers
│   ├── compliance/            export + dashboards
│   ├── agentdesc/             agent-go AgentDescriptor builder
│   ├── api/                   public HTTP service
│   ├── adapters/              mnemos / chronos / nous / praxis HTTP clients
│   └── store/                 memory + sqlite + postgres backends
├── sql/                       sqlc query inputs
├── PRD.md                     product requirements
├── TDD.md                     technical design
└── Makefile
```

## Status

Phase 1 MVP, Phase 2 hardening, and Phase 3 ecosystem (multi-tenancy,
plugins, MCP, distributed scheduler, compliance export) all shipped per
the [Roadmap](Roadmap.md).

## License

[MIT](LICENSE) — © 2026 Felix Geelhaar.
