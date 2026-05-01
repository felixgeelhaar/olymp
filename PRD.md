# Olymp — Product Requirements Document (PRD)

## 1. Overview

Olymp is the **runtime layer** of the four-system cognitive stack (Mnemos · Chronos · Nous · Praxis). It is the orchestrator that connects memory, time, decision, and execution into a single closed-loop runtime — turning AI from passive intelligence into an active, learning system that operates on real systems.

Olymp does **not** replace the four layers. It composes them. Mnemos remembers, Chronos perceives, Nous decides, Praxis acts. Olymp wires them into the loop and runs it.

> Olymp is to AI runtimes what Kubernetes is to containers — an orchestrator that observes state, makes decisions, and enforces desired outcomes, continuously.

## 2. Problem Statement

### 2.1 The Runtime Gap

Each of the four cognitive layers solves part of the problem, but in isolation they remain disconnected:

- **Mnemos** has truth, but no triggers — knowledge sits inert.
- **Chronos** sees patterns, but cannot recall context or act.
- **Nous** decides, but has no canonical place to source memory + signals + execute.
- **Praxis** executes, but does not know *what* to execute or *when*.

Without a runtime, every consumer must:

1. Wire the four systems by hand for every use case.
2. Re-implement the loop (observe → understand → decide → act → learn) per integration.
3. Stitch session, identity, and goal state across four data planes.
4. Reconstruct the audit trail across four logs.
5. Manage failure recovery when one layer is down or slow.

### 2.2 The Cost

- AI agents and automations re-build the same orchestration glue for every product.
- Outcomes never close back to memory — the loop is open, the system never learns.
- No single place to ask *"what is the system doing right now, and why?"*
- No single place to *steer* the system at runtime (pause, redirect, gate, dry-run).
- No single audit surface for compliance.

### 2.3 Root Cause

There is no shared layer that:

1. Owns the **loop** (observe → understand → decide → act → learn) end-to-end.
2. Holds **session and goal state** across the four cognitive systems.
3. Provides a **single front door** (CLI, API, plugin, MCP) for users and agents.
4. Converts **outcomes into memories** so the next iteration is smarter.
5. Provides **safety rails** (approvals, kill switch, rate limits) at the runtime level.

## 3. Strategic Vision

> Olymp is the AI control plane for complex systems.

Position relative to peers in the cognitive stack:

| Layer    | Question                                            |
|----------|-----------------------------------------------------|
| Mnemos   | "What is true and why?"                             |
| Chronos  | "What patterns are happening?"                      |
| Nous     | "What should be done — and when?"                   |
| Praxis   | "What can be done? What happened when we did it?"   |
| **Olymp** | **"How do we run all of this as one system?"**     |

Olymp creates (or fits into) the category of **Autonomous Systems Runtime** — the substrate that turns four powerful primitives into a self-improving operator of real systems.

## 4. Public Contract

The minimal user-facing API:

```go
type OlympAPI interface {
    // Submit a goal or observation; the runtime drives it through the loop.
    Submit(ctx context.Context, intent Intent) (Run, error)

    // Inspect the live state of a run (timeline, decisions, actions, outcomes).
    Inspect(ctx context.Context, runID string) (RunSnapshot, error)

    // Steer a running loop: pause, resume, cancel, approve, deny.
    Steer(ctx context.Context, runID string, command SteerCommand) error

    // Stream live events from a run (signals, decisions, actions, outcomes).
    Stream(ctx context.Context, filter RunFilter) (<-chan RunEvent, error)
}
```

Four verbs: `Submit`, `Inspect`, `Steer`, `Stream`. Anything more lives in the four layers below.

## 5. Phased Roadmap

### Phase 1 — Runtime Primitive (MVP)

**Goal:** Prove that a thin orchestration layer over Mnemos / Chronos / Nous / Praxis is dramatically more useful than any of them alone, by closing the loop end-to-end with a single API and a working agent surface.

**Target Users:**

- Developers building AI agents who need a complete observe → act → learn loop.
- Operators of automations who want one place to steer and audit.
- Teams already running one or more of Mnemos / Chronos / Nous / Praxis and want them composed.

**Deliverables:**

- [ ] `OlympAPI` (Submit / Inspect / Steer / Stream) with stable `Run.ID`.
- [ ] Loop engine that drives `observe → understand → decide → act → learn`.
- [ ] Cognitive-stack adapters: Mnemos client, Chronos client, Nous client, Praxis client.
- [ ] Run state machine (`pending → observing → understanding → deciding → acting → learning → completed | failed | cancelled`).
- [ ] Session + goal state persisted across iterations of the loop.
- [ ] Outcome writeback: every Praxis action produces a Mnemos event, surfaced in the next iteration.
- [ ] CLI: `olymp submit`, `olymp inspect <run-id>`, `olymp steer <run-id> <command>`, `olymp stream`.
- [ ] Plugin surface for Claude / Codex (`olymp explain`, `olymp fix`).
- [ ] Multi-backend storage (memory / SQLite / Postgres), mirroring Praxis & Chronos.
- [ ] Single audit trail spanning all four layers per run.

**Excluded from MVP:**

- Multi-tenant runtime (a single global allow-list is enough).
- Hot-swappable layer implementations.
- Distributed scheduling / worker pool (single-process loop runner).
- Plugin / dynamic loader for new layer types.
- MCP-compatible runtime surface.

**Success Metrics:**

- Time-to-close-the-loop ("submit intent → action executed → outcome recorded in Mnemos") < 30s on the reference agent flow.
- 100% of runs are reconstructible from the audit trail alone, across all four layers.
- Re-submitting the same `Run.ID` produces zero double-effect actions (Praxis idempotency is preserved end-to-end).
- Time-to-add-a-new-cognitive-stack adapter measured in hours, not days.

---

### Phase 2 — Safety, Steering & Resilience

**Goal:** Make Olymp safe to point at production. Add approvals, rollback, retries across the loop, kill switch, and full transparency at every step.

**Target Users:** Teams running real automations and AI agents in business-critical paths.

**Deliverables:**

- [ ] Approval gates: per-capability or per-scope human-in-the-loop checkpoints before Praxis executes.
- [ ] Rollback primitives: undo / compensate via Praxis when a downstream signal flags regression.
- [ ] Retry policy across the loop (Chronos signal stale → re-observe; Nous decision rejected → re-plan).
- [ ] Kill switch: `olymp halt` instantly stops all in-flight runs and pauses new submissions.
- [ ] Rate limiting per intent type and per caller.
- [ ] Decision explainability: every action has an attached chain (`Memory → Signal → Decision → Action → Outcome`).
- [ ] Run replay: re-run a historical loop against current memory + signals to validate behaviour.

**Success Metrics:**

- Zero unauthorized actions in audit (every Praxis call has a recorded approval where required).
- ≥ 99% of transient layer failures recovered by retry.
- Replay produces deterministic decisions on identical (memory, signals, policy) tuples ≥ 95% of the time.

---

### Phase 3 — Runtime Ecosystem

**Goal:** Open the surface. Run multi-tenant. Speak MCP. Let third parties register cognitive-stack adapters and intent types. Scale to org-level governance.

**Target Users:** Platform teams, regulated environments, AI agent vendors.

**Deliverables:**

- [ ] Plugin architecture for out-of-tree cognitive-stack adapters and intent handlers.
- [ ] MCP-compatible runtime: `Submit`, `Inspect`, `Steer`, `Stream` exposed as MCP tools.
- [ ] Multi-tenant scopes: org / team / user runs with isolated memory + audit.
- [ ] Org-level dashboards and exportable audit (SOC 2 / HIPAA / GDPR friendly).
- [ ] Distributed loop runner: horizontally scalable workers consuming a shared run queue.
- [ ] Cross-runtime federation: an Olymp runtime can call into another Olymp runtime as a peer.

**Success Metrics:**

- ≥ 3 organisations running production workloads on Olymp with custom adapters.
- Zero policy bypasses in audit.
- Audit export passes a real compliance review.

## 6. Core Use Cases

### Use Case 1 — Plugin: "Why is payments latency high?" (Phase 1)

> A developer in Claude Code or Codex asks: *"Why is payments latency high?"*

The plugin calls `Submit({intent: "explain", subject: "payments-latency"})`. Olymp drives the loop:

1. Mnemos returns recent claims and prior incidents tagged `payments`.
2. Chronos surfaces a `Spike` signal on `latency_p99` over the last hour.
3. Nous combines memory + signal, forms hypotheses, ranks them, and produces an explanation with evidence.
4. The user sees a grounded, time-aware answer with links to evidence.

If the user follows up with *"fix it"*, Olymp re-enters the loop with `intent: "remediate"`, Nous plans, Praxis executes (subject to approvals), and the outcome flows back to Mnemos.

### Use Case 2 — CLI: Closed-Loop Operations (Phase 1)

```bash
olymp explain incident-123
olymp fix payments-latency
olymp inspect run-7f3
olymp stream --intent remediate
```

Each command is a thin wrapper over `Submit` / `Inspect` / `Stream`. The CLI is the fastest path from terminal to closed-loop runtime.

### Use Case 3 — Agent Action Surface (Phase 1 → 2)

> An autonomous agent uses Olymp as its operating substrate.

The agent submits high-level intents (`investigate`, `plan`, `act`, `verify`). Olymp owns the loop, the session state, the audit trail, and the safety rails. The agent is no longer responsible for re-implementing observe / understand / decide / act / learn — it composes Olymp calls.

### Use Case 4 — Compliant Autonomous Operations (Phase 3)

> A regulated team wants every AI-driven operational change to flow through one runtime.

Olymp becomes the only path: intents are typed and scoped, decisions are explainable, actions are dry-runnable and approved, outcomes are written back to memory. Audit export satisfies the auditor on first ask.

## 7. Product Principles

1. **Single responsibility.** Olymp orchestrates. It does not remember (Mnemos), perceive (Chronos), decide (Nous), or execute (Praxis).
2. **The loop is the product.** Observe → understand → decide → act → learn, end-to-end, observable, replayable.
3. **Outcomes feed memory.** Every action produces an event in Mnemos. The next iteration is strictly smarter than the last.
4. **Steering by default.** Pause, resume, cancel, approve — at any step, on any run.
5. **Evidence everywhere.** Every action carries its full provenance chain (Memory → Signal → Decision → Action → Outcome).
6. **Local-first defaults.** A laptop with SQLite running all four layers in-process is a complete deployment.
7. **Composable, not coupled.** Each layer is replaceable behind a port; Olymp depends on contracts, not implementations.

## 8. Non-Goals

- A memory store — that's Mnemos.
- A pattern detector — that's Chronos.
- A decision engine — that's Nous.
- An action executor — that's Praxis.
- An agent framework. Agents *use* Olymp; Olymp does not impose an agent shape.
- A general workflow engine with branches, loops, and conditionals beyond the cognitive loop itself.
- A scheduler / cadence runner.

## 9. Open Questions

- [ ] What is the canonical shape of `Intent`? Open vocabulary vs. typed enum vs. registry?
- [ ] Where does session state live — in Olymp's own store, or solely as Mnemos events with an Olymp-side index?
- [ ] How do we model long-running runs that span hours/days without losing context?
- [ ] What is the right boundary between "Olymp built-in adapter" and "external plugin" for cognitive-stack layers?
- [ ] How do we test the loop end-to-end deterministically when each layer involves an LLM?
- [ ] Should `Submit` be synchronous (block until terminal) or always async with `Stream` for progress?
- [ ] How do approval gates compose with Praxis policy decisions — is one strictly upstream of the other?

## 10. Definition of Success

Olymp is successful when:

- Submitting an intent runs the full observe → understand → decide → act → learn loop without bespoke glue.
- Every run is reconstructible end-to-end from a single audit trail spanning all four layers.
- Outcomes flow back into Mnemos automatically, and the next iteration is provably smarter on identical inputs.
- AI agents and humans share one front door (`Submit` / `Inspect` / `Steer` / `Stream`) for the same operations.
- Operators stop writing per-product orchestration and trust Olymp as the runtime.
- Mnemos / Chronos / Nous / Praxis are each addressable on their own *and* compose into a self-improving system through Olymp.
