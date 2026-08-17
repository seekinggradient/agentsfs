---
description: Scheduled maintenance pass — stock contract current; closed backlog tickets stamped; archive move and journal consolidation remain unavailable in bounded mode.
---

## Learned / decided
- `garden_upgrade_contract` reported the stock contract current at 0.12.0; no upgrade was needed.
- `garden_doctor` found 23 journal notes pending consolidation and two closed ticket files still under `backlog/`: `contract-backlog-spec.md` and `hub-projection-pull.md`.
- Both tickets are clear archive candidates. Their frontmatter is now stamped `closed: 2026-08-10`, preserving the documented production/release date, but they remain in place because the available gardening writes cannot move or delete files.
- The sampled recent journal entries did not reveal a new durable fact safe to fold without broad review; their existing projection-sync and maintenance details are already represented in durable notes.

## Open
- An archive-capable sweep should move the two closed ticket files into `backlog/archive/`, remove their terminal spine lines or update links, and preserve their contents and links without leaving redundant backlog copies.
- A gardener with safe deletion/archive capability should consolidate the 23 pending journal notes into durable notes, preserving chronology, citations, uncertainty, and disagreements.

## Written directly
- Added `closed: 2026-08-10` to `backlog/contract-backlog-spec.md` and `backlog/hub-projection-pull.md`.
