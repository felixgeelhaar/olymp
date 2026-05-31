# DEPRECATED

**Status:** Archived 2026-05-31
**Final release:** `v0.1.5-final`
**Authoritative successor:** None for Olymp's specific shape. Reasoning and
action have moved into agent runtimes (Claude Code, Codex, Hermes, Nomi,
OpenClaw, NanoClaw, ...); memory + timeline live in Mnemos.

## Why this was archived

1. **Zero Go importers across the organisation.** A pre-archive grep across
   every sibling repo's `go.mod`, `go.sum`, and `*.go` files returned no
   matches for `github.com/felixgeelhaar/olymp` outside this repository
   itself.
2. **Reasoning is now an agent-layer concern.** Frontier models plus tool
   use plus MCP cover the orchestration patterns Olymp implemented. A
   separate Go runtime is no longer the right shape.
3. **Olymp depended on Nous and Praxis**, both of which are also being
   archived as part of the same cognitive-stack simplification.

See [ADR 0003 in Mnemos](https://github.com/felixgeelhaar/mnemos/blob/main/docs/adr/0003-archive-olymp.md)
for the full decision record.

## What was preserved

The full Olymp source code is preserved at the `v0.1.5-final` tag.
Notably:

- The closed-loop FSM (`internal/domain/run_fsm.go`, `internal/engine/engine.go`)
- Runtime steering (pause / approve / cancel / halt)
- Federation Peer plugin (`plugin/federation.go`)
- Typed intent registry (`internal/intent/`)
- MCP tool surface (`cmd/olymp/mcp.go`: `submit_intent`, `inspect_run`,
  `steer_run`, `halt`, `explain_run`, `agent_descriptor`)
- Provenance audit (`internal/audit/`)
- Multi-backend persistence (memory, sqlite, postgres)
- HTTP + gRPC API client + server
- axi-go effect kernel wiring (`cmd/olymp/axikernel.go`)

Any of these can be lifted out and re-published if a real consumer emerges.

## What was not preserved as a separate library

None of the patterns above were extracted into a new repository. The
audit (2026-05-31) found no current consumer for any of them. Premature
library extraction grows surface to maintain without callers exercising
the API. If a real consumer materialises, the cost of resurrection from
this tag is bounded; the cost of speculative maintenance is unbounded.

## Recovery path

```bash
git clone https://github.com/felixgeelhaar/olymp.git
cd olymp
git checkout v0.1.5-final
```

The repo will remain read-only and publicly readable indefinitely. Issue
filing is disabled but the source, tags, and history are preserved.

## Replacement guidance

| If you wanted... | Use instead |
|---|---|
| Memory + claims + lessons + playbooks | [Mnemos](https://github.com/felixgeelhaar/mnemos) |
| Timelines, event sourcing, audit trails | [Chronos](https://github.com/felixgeelhaar/chronos) (or Mnemos for typical use) |
| Risk + intervention scoring (from Nous) | [decisionkit](https://github.com/felixgeelhaar/decisionkit) |
| Multi-step agent workflows | Your agent runtime's native loop + MCP tools into Mnemos |
| Multi-agent coordination | CrewAI / AutoGen / LangGraph or your runtime's subagent features |
| Action execution against vendors | Agent runtime tools + MCP servers (or direct SDK calls) |

## Related archives

- [Nous](https://github.com/felixgeelhaar/nous) — reasoning service, archived
- [Praxis](https://github.com/felixgeelhaar/praxis) — action service, archived
