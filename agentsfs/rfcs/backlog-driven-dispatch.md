---
description: RFC draft — backlog-driven dispatch. Push-triggered per-item agent spawns, claiming by git CAS, the band dispatch gate, an implement→review pipeline with tiered merge trust. Design recorded; implementation deferred until the manual pull loop is proven.
status: draft
rfc_status: draft
rfc_scope: project
rfc_owner_approved: false
date: 2026-08-09
sources:
  - Owner vision conversation, 2026-08-09 ("my interface into my software is that I just create tickets")
  - agentsfs/rfcs/backlog-directories.md (the structure this dispatches over)
  - agentsfs/rfcs/backlog-and-tasks.md (ready semantics; "no claiming protocol" assumption this RFC retires)
  - Sprites agent integration (one sprite per user, hub-proxied model calls)
---

# RFC draft: Backlog-driven dispatch

> **Status: design only.** The owner has explicitly deferred automation: the manual loop ("work the backlog" sessions) must prove the grammar first. This document records the ratified direction and marks each open decision. Nothing here is implemented.

## Vision

The owner's interface to a project is writing tickets. A dispatchable backlog item is picked up by an agent that instantiates or reads its ticket file, works it in a worktree, and puts up a change; a second agent reviews — tests, screenshots for UI changes, findings into the ticket's `## Log` — and the change reaches a merge decision. The owner triages in the morning, reviews merges, and descends into a ticket only deliberately.

## Design (proposed, unratified except where noted)

### Claiming is a git CAS flip — ratified direction

Claiming = `[ ]`→`[/]` on the spine plus push (`afs task claim`). If the push lands, the task is claimed; a rejected push is a lost race — pull, pick the next ready item. The backlog page is its own lock table, legible in every viewer. Stale `[/]` with no recent ticket-log activity is an expired lease (gardener/doctor, not a daemon). The 0.10.0 "no claiming protocol" assumption is retired by this mechanism, not by a service. No lease TTL metadata in 0.x; staleness stays a judgment call surfaced by tooling.

### The band gate — ratified direction

Only bands at or above the gate are dispatchable; capture below the gate is inert by construction. **Open:** gate at `Now` only, or `Now`+`Next`. Everything in `Later`/`Someday` never dispatches. Agent-created items always land below the gate — only the owner's triage promotes across it. This one rule keeps capture free and prevents the pool from generating its own workload.

A second gate condition worth ratifying: an item without a ticket file (or with one the dispatcher judges spec-incomplete) may be dispatched only to a *spec-drafting* agent, never an implementing one — "if an agent would need to ask what it means, it isn't ready to implement."

### Pipeline shape — proposed

Per-item spawn on push (Hub sees every push; a stateless trigger spawns one session per newly-dispatchable item) rather than standing monitor agents. Stages:

1. **Take**: claim via CAS; create/refresh the ticket file; worktree checkout.
2. **Implement**: work the spec; log dated progress entries; put up the change (branch/PR or Hub equivalent).
3. **Review**: a separate agent runs checks, exercises the change (screenshots into the ticket dir for UI), writes findings to the log, and marks the ticket reviewed or bounces it back with reasons.
4. **Merge decision**: see trust tiers.

Concurrency bound: per-instance cap on simultaneous claims (proposed: 2) so the pool can't strip-mine a backlog and flood review.

### Tiered merge trust — proposed

Merge starts human for everything. Categories graduate to auto-merge only by explicit owner decision after observed history — plausible first graduates: docs-only, test-only, copy changes with green checks. The merge queue is the owner's quality signal on the whole pipeline; do not automate it away early.

### Escalation

An agent blocked on judgment writes `— blocked by owner: <question>` and releases or completes what it can. The owner-blocked view is the pipeline's escalation inbox. No notifications in v1 beyond the triage view (open: whether Hub pings on new owner-blocks).

### Architecture boundary — ratified direction

The dispatcher is a Hub/sprite feature that reads and writes backlogs through the same grammar as any agent — never part of the AgentsFS contract, never a requirement for an instance to make sense. Clone the repo and the backlog is complete without the orchestrator. If dispatch ever needs indexes or queues, they live under `.agentsfs/` as derived, rebuildable state (contract rule 11) — never source of truth.

## Adoption sequence

1. Manual loop: "work the backlog" sessions against 0.11.0 structure (proves grammar, claim tool, log discipline).
2. Owner-blocked triage + cross-instance view in daily use.
3. Semi-auto: owner manually spawns per-ticket sessions from the triage view (Hub button or CLI) — same pipeline, human trigger.
4. Push-triggered spawn behind an explicit per-instance opt-in.
5. Review-agent stage; merge stays human.
6. Tiered auto-merge, category by category.

## Open decisions for the owner

1. Gate: `Now` only vs `Now`+`Next`.
2. Per-instance concurrent-claim cap.
3. Spec-completeness gate: dispatcher judgment vs explicit `ready` marker on the ticket.
4. Where changes land: GitHub PRs, Hub-native review, or per-instance choice.
5. Notification policy for owner-blocked items.
