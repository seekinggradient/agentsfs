---
description: Scheduled maintenance pass — stock contract current; doctor worklist reviewed; closed-ticket archiving and journal consolidation remain unavailable in bounded mode.
---

## Learned / decided
- `garden_upgrade_contract` reported the stock contract current at 0.12.0; no upgrade was needed.
- `garden_doctor` found 26 session notes pending consolidation and two closed ticket files still under `backlog/`: [[contract-backlog-spec]] and [[hub-projection-pull]].
- Both ticket files already carry `closed: 2026-08-10`. The available gardening writes cannot move or delete originals, so archive copies would leave duplicates and would not repair the doctor finding.
- Reviewed the journal index, the five latest maintenance notes, and the production projection-sync record. The sampled operational facts are already represented in [[hub-projection-pull]]; no additional unique durable fact was safely foldable without a broad review.

## Open
- An archive-capable sweep should move the two closed ticket files into `backlog/archive/`, preserving contents and links without leaving redundant backlog copies.
- A gardener with safe deletion/archive capability should consolidate the pending journal notes into durable notes, preserving chronology, citations, uncertainty, and disagreements.

## Written directly
- No durable note or backlog ticket was changed; this append-only maintenance record captures the bounded worklist and the unavailable move/delete limitation.
