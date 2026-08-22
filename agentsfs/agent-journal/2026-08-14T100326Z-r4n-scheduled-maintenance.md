---
description: Scheduled maintenance pass — stock contract current; doctor worklist unchanged; archive and journal consolidation still require unavailable move/delete support.
---

## Learned / decided
- `garden_upgrade_contract` reported the stock contract current at 0.12.0; no upgrade was needed.
- `garden_doctor` found 24 session notes pending consolidation and two closed ticket files still under `backlog/`: [[contract-backlog-spec]] and [[hub-projection-pull]].
- The two tickets remain clear archive candidates. Their `closed: 2026-08-10` frontmatter is already present, but this gardening surface cannot move or delete the originals; creating archive copies would leave duplicates and would not repair the doctor finding.
- The recent maintenance notes record that the sampled journal material has already been folded where a unique durable fact was identified, and no additional fact was safely foldable without a broader review. Older journal entries remain in place to preserve chronology and evidence.

## Open
- An archive-capable sweep should move the two closed ticket files into `backlog/archive/`, preserve their contents and links, and remove or update their terminal spine references without leaving redundant backlog copies.
- A gardener with safe deletion/archive capability should consolidate the 24 pending journal notes into durable notes, preserving chronology, citations, uncertainty, and disagreements.

## Written directly
- Added this append-only maintenance record; no durable note or backlog ticket was changed because the remaining doctor findings require file moves/deletions or broader journal review.
