# Show HN draft — Olymp + Mnemos

## Title (80 char limit, ~70 sweet spot)

Pick one. A is the sharpest claim, B is the most concrete, C is the most contrarian.

**A.** Show HN: Deterministic replay for non-deterministic AI agents

**B.** Show HN: Mnemos – wrap any LangGraph agent for audit + replay in 4 lines

**C.** Show HN: Why agent traces aren't audit trails (and what to do about it)

## URL

https://felixgeelhaar.github.io/olymp/

## Body (HN's optional comment field — first comment of the thread)

Hey HN,

Olymp is an open-source runtime for AI agents whose decisions need to be defensible months later. Instead of "trace logs that look right," it records the chain of evidence behind every decision: signal -> claim -> decision -> action -> outcome, all keyed to one run id, all replayable from a single HTTP call.

Two ways in:

1. **Wrap your existing LangGraph / CrewAI / MCP agent.** Drop a callback into your nodes; each one emits one event into Mnemos. There's a 4-node refund-triage example in the repo (~270 LOC of Python, raw HTTP, no SDK). Real run-id from the demo: https://felixgeelhaar.github.io/olymp/assets/runs/refund-triage-cust42.json

2. **Run the full Olymp orchestrator.** It drives the four-service stack (memory · signals · decisions · execution) end-to-end. The reference scenario boots two deliberately-broken services, watches them degrade in Grafana, fires the heal action via a local Llama, and replays the whole chain.

Why I built it: Langfuse and LangSmith log spans (great for "what happened"). Temporal makes steps durable (great for "did it run"). Neither one lets you replay *why* a decision came out the way it did, against the same evidence, weeks later. That's the seam Olymp aims at.

Stack: Go (single binary per service), Postgres, MIT licensed. Five GHCR images. Works with any LLM via OpenAI-compat (default Ollama).

Repo: https://github.com/felixgeelhaar/olymp
Mnemos (the audit substrate, usable standalone): https://github.com/felixgeelhaar/mnemos

Happy to answer questions. Especially interested in: what's missing from the LangGraph-wrap story, and where the audit-vs-trace distinction lands or doesn't.

## Posting checklist

- [ ] Pages deploy green
- [ ] Real run JSON link returns 200 (curl test)
- [ ] Repo public + non-empty README
- [ ] Email at the bottom of olymp page works
- [ ] Be online for 4 hours after posting
- [ ] Don't post on Friday afternoon, Sunday, or holidays
- [ ] Best time: Tuesday-Thursday, 8-10am PT
