# Olymp — Vision

## Vision Statement

Olymp is the AI control plane for complex systems — the runtime that drives
the cognitive loop, holds session and goal state, and turns four powerful
primitives (Mnemos · Chronos · Nous · Praxis) into one self-improving
operator of real systems.

---

## The Problem

Today's AI agents are **smart but disconnected**. They forget context,
suggest actions instead of taking them, ignore time and causality, and
never close the loop on outcomes. Each layer of the stack solves part of
the problem, but in isolation:

- **Mnemos** stores truth — but has no triggers; knowledge sits inert.
- **Chronos** sees patterns — but cannot recall context or act on them.
- **Nous** decides — but has no canonical place to source memory + signals
  + execute actions.
- **Praxis** executes — but does not know *what* to do or *when*.

Without a runtime, every consumer:

1. Wires the four systems by hand for every use case.
2. Re-implements the loop (observe → understand → decide → act → learn).
3. Stitches session, identity, and goal state across four data planes.
4. Reconstructs an audit trail across four logs.
5. Manages failure recovery when one layer is slow or down.

## The Cost of Inertia

Without a runtime, organisations pay in:

- **Open loops** — outcomes never feed back into memory; the system never
  learns what actually worked.
- **Bespoke orchestration** — every product re-builds session and goal
  glue from scratch.
- **Audit fragmentation** — no single place to ask *"what is the system
  doing right now, and why?"*
- **Steering blindness** — no way to pause, redirect, gate, or kill an
  in-flight loop without digging into four logs.
- **Trust ceilings** — agents stay on a short leash because the operator
  has no observable, governed, replayable surface to point them at.

---

## The Shift

| Traditional AI | Olymp |
|---|---|
| Responds to prompts | Operates on systems |
| Stateless | Persistent memory + session |
| No time awareness | Timeline + causality (via Chronos) |
| Suggests actions | Executes actions (via Praxis) |
| Doesn't improve | Learns from outcomes |

Olymp turns AI from passive intelligence into an active, learning system
that operates in the real world.

---

## What Olymp Is

> An AI control plane for complex systems.

Similar to how Kubernetes is a control plane for containers, Olymp is:

- **Observing** system state through Mnemos and Chronos.
- **Making decisions** through Nous, with full context and history.
- **Enforcing desired outcomes** through Praxis, under policy and approval.
- **Learning continuously** by writing every outcome back to Mnemos so the
  next iteration is strictly smarter than the last.

Each step is observable and replayable. The loop closes only when an
outcome lands in memory.

---

## Strategic Position

Olymp creates (or fits into) the category of **Autonomous Systems
Runtime** — the substrate that turns four powerful primitives into a
self-improving operator of real systems.

| Layer | Question it answers |
|---|---|
| Mnemos | "What is true and why?" |
| Chronos | "What patterns are happening?" |
| Nous | "What should be done — and when?" |
| Praxis | "What can be done? What happened when we did it?" |
| **Olymp** | **"How do we run all of this as one system?"** |

---

## Use Cases

- **AIOps** — detect incidents, analyse root cause, auto-remediate, learn.
- **Engineering productivity** — explain codebases, generate fixes, manage
  deployments, all under one provenance trail.
- **Autonomous workflows** — monitor systems, trigger actions, adapt over
  time without bespoke orchestration.
- **Regulated automation** — every action approval-gated, audit-exportable,
  rollback-able.

---

## Final mental model

```
Mnemos remembers truth.
Chronos understands time.
Nous decides what to do.
Praxis takes action.
Olymp connects them into a self-improving system.
```

The shift: from AI that *talks about* systems to AI that *operates*
systems.
