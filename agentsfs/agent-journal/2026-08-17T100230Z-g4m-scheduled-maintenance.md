---
description: Scheduled maintenance pass — stock contract current; doctor worklist reviewed; ticket archiving and journal consolidation remain unavailable in bounded mode.
---

## Learned / decided
- `garden_upgrade_contract` reported the recognized stock contract current at 0.12.0; no contract upgrade was needed.
- `garden_doctor` found 33 session notes pending consolidation and two closed ticket files still under `backlog/`: `contract-backlog-spec.md` and `hub-projection-pull.md`.
- Both tickets are clear archive candidates and already carry `closed: 2026-08-10`. The available gardening writes can create or replace files but cannot move or delete the originals, so archive copies would leave duplicates and would not repair the doctor findings.
- Reviewed the journal index, the recent transaction-deployment and collapsed-file-tree entries, and prior maintenance records. The sampled recent durable facts are already reflected in `backlog/INDEX.md` or the relevant durable tickets; no additional fact was safely foldable without broad review.

## Open
- An archive-capable sweep should move the two closed ticket files into `backlog/archive/`, preserve their contents and links, and remove or update terminal spine references without leaving redundant backlog copies.
- A gardener with safe deletion/archive capability should consolidate the 33 pending journal notes into durable notes while preserving chronology, citations, uncertainty, and disagreements.

## Written directly
- No durable note or backlog ticket was changed in this bounded pass; this append-only record captures the current worklist and the unavailable move/delete limitation.
