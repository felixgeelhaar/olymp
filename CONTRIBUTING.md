# Contributing to Olymp

Thanks for your interest in contributing. Olymp is the **runtime layer**
of the four-system cognitive stack (Mnemos · Chronos · Nous · Praxis) —
please skim [README.md](README.md), [Vision.md](Vision.md), and
[TDD.md](TDD.md) before opening anything non-trivial.

---

## Getting set up

```bash
git clone https://github.com/felixgeelhaar/olymp.git
cd olymp
make check       # format, vet, test, build
make demo        # boot with in-process fakes
```

Requirements:

- Go 1.26+
- `sqlc` for query regeneration when SQL changes
- SQLite 3.40+ (bundled, required for the local-first backend)
- Postgres 15+ for the multi-tenant backend integration tests
  (env-guarded by `OLYMP_TEST_PG_DSN`; a Docker image works fine)

Olymp uses the `felixgeelhaar/*` foundation libraries. Don't roll your
own when one of these covers the use case:

| Library | Use it for |
|---|---|
| [`bolt`](https://github.com/felixgeelhaar/bolt) | All structured logging (no `log`, no raw `slog`) |
| [`fortify`](https://github.com/felixgeelhaar/fortify) | Retries, circuit breakers, rate limiting |
| [`statekit`](https://github.com/felixgeelhaar/statekit) | The `Run` lifecycle FSM — never string-switch transitions |
| [`axi-go`](https://github.com/felixgeelhaar/axi-go) | The MCP tool kernel (`cmd/olymp/axikernel.go`) |
| [`mcp-go`](https://github.com/felixgeelhaar/mcp-go) | MCP stdio server |
| [`agent-go`](https://github.com/felixgeelhaar/agent-go) | `AgentDescriptor` + `Capability` for peer-agent discovery |
| [`sqlc`](https://sqlc.dev) | Typed queries against `sql/{sqlite,postgres}/queries.sql` |

---

## Working in the codebase

[`AGENTS.md`](AGENTS.md) is the contract: hard rules, layout, conventions,
and "what NOT to do". Read it once before your first PR.

[`CLAUDE.md`](CLAUDE.md) is the same map, optimised for AI agents landing
cold — useful even for humans navigating the load-bearing files.

### Hot paths

- `internal/domain/run.go` + `run_fsm.go` — Run aggregate + FSM. New
  status transitions land in the FSM first.
- `internal/engine/engine.go` — the loop. Single point of truth for
  "what one iteration does".
- `internal/ports/{repo,layers,errors}.go` — every backend + cognitive
  layer talks through these. Domain code never imports a concrete backend.
- `cmd/olymp/axikernel.go` — register new MCP tools here so they get
  effect gating + budget for free.

---

## Tests

```bash
make test        # race-detector, no caching
make cover       # coverage report
```

- Standard library `testing` only.
- In-memory SQLite (`:memory:`) for store integration. Postgres tests are
  env-guarded by `OLYMP_TEST_PG_DSN`.
- API tests use `httptest.NewServer`. Engine tests use
  `internal/adapters/fake/`.

---

## Pull requests

- Keep changes scoped. One concern per PR.
- Update `TDD.md` if you change a domain type, port, or storage shape.
- Update `Roadmap.md` if you ship something tracked there.
- New MCP tools also update `cmd/olymp/axikernel.go::mcpTools()`.

---

## Code of conduct

See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Be excellent to each other.

## License

By contributing, you agree your contributions are licensed under the
[MIT License](LICENSE).
