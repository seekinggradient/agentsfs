---
description: Scheduled maintenance pass — contract current; doctor worklist confirmed; archive and journal consolidation require unavailable move/delete capability.
---

## Learned / decided
- `garden_upgrade_contract` reported the recognized stock contract current at 0.12.0; no upgrade was needed.
- `garden_doctor` found 34 session notes pending consolidation and two closed ticket files still under `backlog/`: `contract-backlog-spec.md` and `hub-projection-pull.md`.
- Both tickets already carry `closed: 2026-08-10` and remain linked only from completed backlog tasks. The available gardening writes can create or replace files but cannot move or delete the originals, so copying them into an archive would leave duplicates and would not repair the findings.
- Reviewed the journal index, the latest maintenance note, the transaction-deployment note, and the collapsed-file-tree note. The sampled durable facts are already reflected in `backlog/INDEX.md` or the relevant tickets; no additional fact was safely foldable without a broad journal review.

## Open
- An archive-capable sweep should move the two closed ticket files into `backlog/archive/`, preserve their contents and links, and remove or update terminal spine references without leaving redundant backlog copies.
- A gardener with safe deletion/archive capability should consolidate the pending journal notes into durable notes while preserving chronology, citations, uncertainty, and disagreements.

## Written directly
- No durable note or backlog navigation was changed in this bounded pass; this append-only record captures the current worklist and the unavailable move/delete limitation.
