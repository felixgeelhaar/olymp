# Olymp — Archived

> **This repository is archived as of 2026-05-31.** Final release: `v0.1.5-final`.
> The cognitive-stack architecture Olymp orchestrated has been simplified.
> See below for what to use instead.

## What changed

The cognitive stack collapsed from five primitives to three:

- **Mnemos** — memory (text + claims + relationships + lessons + playbooks,
  with temporal memory powered by Chronos internally).
  → <https://github.com/felixgeelhaar/mnemos>
- **Chronos** — timeline / event engine. Standalone-usable; bundled inside
  Mnemos for typical users.
  → <https://github.com/felixgeelhaar/chronos>
- **Agent runtimes** — Claude Code, Codex, Hermes, Nomi, OpenClaw, NanoClaw,
  and similar. Reasoning and action live here, with tools and MCP, not in
  separate Go services.

Nous (reasoning) and Praxis (action) are also being archived in the same
initiative.

The closed-loop FSM (`observe → understand → decide → act → learn`),
runtime steering (pause/approve/cancel), federation Peer, and typed intent
registry were Olymp's main primitives. They have **no current consumer**
across the org (zero Go importers verified by `grep` across all sibling
repos). Rather than maintain unused infrastructure, the patterns are
preserved at the `v0.1.5-final` tag and can be lifted out if a real
consumer ever asks.

## What if I was using Olymp?

You probably weren't. Olymp had zero Go importers when this archive was
filed. If you need any of its capabilities:

- **Memory + timeline + lessons + playbooks** → use Mnemos directly.
- **Multi-step agent workflows** → use your agent runtime's native loop
  (Claude Code, Codex, Hermes, etc.) plus MCP tool calls into Mnemos.
- **Multi-agent coordination** → CrewAI / AutoGen / LangGraph or your
  runtime's subagent / federation features.
- **The exact Olymp FSM + intent registry + MCP surface** → check out the
  `v0.1.5-final` tag of this repo. The full source is preserved.

## Rationale

See [ADR 0003: Archive Olymp](https://github.com/felixgeelhaar/mnemos/blob/main/docs/adr/0003-archive-olymp.md)
in the Mnemos repository for the full reasoning. Short version: reasoning
has moved into the agent layer (frontier models + tools + MCP), and
maintaining a separate orchestration runtime is no longer worth the cost.

## License

Unchanged from the v0.1.5-final tag. See `LICENSE`.
