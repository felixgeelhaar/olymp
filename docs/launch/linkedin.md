# LinkedIn launch draft — Olymp + Mnemos

LinkedIn rewards opinion + concrete artifact. Lead with the position.

## Post (1300 char ceiling, sweet spot ~700)

I just shipped Olymp — an open-source runtime for AI agents whose decisions need to be defensible months later.

The market has trace tools (Langfuse, LangSmith) and durable workflow runtimes (Temporal). Neither one lets you replay why a decision came out the way it did, against the same evidence, weeks later.

For a refund-triage agent that auto-approves $4k, that gap is career-ending.

Olymp records the chain of evidence behind every decision — signal → claim → decision → action → outcome — keyed to one run id, replayable from a single HTTP call.

Two ways to use it:

1. Wrap your existing LangGraph / CrewAI / MCP agent. Drop a callback in; every node becomes part of an audit chain. There's a 4-node refund-triage example in the repo (270 LOC, raw HTTP, no SDK).

2. Run the full Olymp orchestrator end-to-end across memory, signals, decisions, and execution. Reference scenario heals two deliberately-broken services and replays the chain.

A real captured run is linked from the landing page — the JSON shows every node the agent touched, what it observed, what it decided, what it did.

Live: https://felixgeelhaar.github.io/olymp
Repo: https://github.com/felixgeelhaar/olymp

Open source, MIT, single Go binary per service. Looking for design partners running AI agents in regulated environments — DM if that's you.

#AI #LangChain #Observability #OpenSource

## Posting checklist

- [ ] Pages deploy green
- [ ] Same day as HN, not before
- [ ] Engage every comment in first 90 minutes
- [ ] Post Tuesday-Thursday, 7-9am or 12-1pm local time
- [ ] Don't post identical text on multiple platforms (LI ranks penalize copy-paste)
