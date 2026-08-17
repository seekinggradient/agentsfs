---
description: Scheduled maintenance pass — stock contract current; doctor worklist reviewed; archive and journal consolidation deferred because automatic deletion/move is unavailable.
---

## Learned / decided
- `garden_upgrade_contract` reported the stock contract current at 0.12.0; no contract change was needed.
- `garden_doctor` found 22 journal notes pending consolidation and two closed ticket files still under `backlog/`: `contract-backlog-spec.md` and `hub-projection-pull.md`.
- The two ticket findings are clear archive candidates, but this pass did not create archive copies because the available gardening writes cannot remove or move the originals; copies would leave duplicate source files and would not repair the finding.
- The pending journal material was sampled only where needed for the worklist. The latest maintenance entry already records the durable projection-sync detail and the archive limitation; no additional unique fact was identified that could be safely folded without broad journal review.

## Open
- An archive-capable sweep should move the two closed ticket files into `backlog/archive/`, add their closed dates, remove their terminal spine lines, and preserve their links and contents without leaving redundant backlog copies.
- A gardener with safe deletion/archive capability should consolidate the 22 pending journal notes into durable notes, preserving chronology, citations, uncertainty, and disagreements.

## Written directly
- No durable note was changed in this bounded pass; the worklist and constraints are recorded here for the next maintenance run.
