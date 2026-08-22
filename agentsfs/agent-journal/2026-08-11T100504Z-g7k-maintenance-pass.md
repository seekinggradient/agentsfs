---
description: Scheduled maintenance pass — contract current; doctor findings reviewed; one durable projection-sync detail folded; archive and journal cleanup remain bounded by available tools.
---

## Learned / decided
- The stock AgentsFS contract was already current at 0.12.0; no contract upgrade was needed.
- `garden_doctor` found 21 journal notes pending consolidation and two closed ticket files still under `backlog/`: `contract-backlog-spec.md` and `hub-projection-pull.md`.
- The production projection-sync record contained one durable operational detail not present in the ticket: the byte-identical dirty state of `agentic-stocks` was retained in a named stash during reconciliation rather than discarded. That detail was folded into [[hub-projection-pull]].
- Ticket archiving was not attempted in this bounded pass because the available automatic gardening writes cannot delete or move the original ticket files; creating archive copies would leave duplicate/orphaned source files and would not be a clean repair.

## Open
- A future archive-capable sweep should move the two closed ticket files into `backlog/archive/`, add closed dates, remove their terminal spine lines, and preserve their links/contents without leaving redundant backlog copies.
- The 21 pre-existing journal notes remain for a gardener with safe deletion/archive capability; this pass intentionally left them in place.

## Written directly
- Updated [[hub-projection-pull]] with the retained `agentic-stocks` stash detail and its source path.
