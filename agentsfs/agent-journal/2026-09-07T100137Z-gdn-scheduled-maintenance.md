---
description: Scheduled maintenance pass — contract current; folded live gardening bounds into the backlog; journal consolidation still awaits archive-capable writes.
---

## Learned / decided
- `garden_upgrade_contract` reported the recognized stock contract current at 0.12.1; no upgrade was needed.
- `garden_doctor` found 37 session notes pending consolidation and no other structural findings.
- The live Hub/Eve gardening loop already has repository isolation, scoped grants, retries, per-repository progress, and a six-successful-write/four-minute bound. Model-facing move/delete authority remains intentionally unavailable, so journal entries stay in place and closed tickets cannot be archived cleanly by copying them.
- The recent workspace-terminology release record and the harness-plugin RFC already contain their durable facts; no duplicate note was created for those entries.

## Open
- An archive-capable gardener should consolidate the pending journal entries into durable notes and move or delete consumed entries while preserving chronology, citations, uncertainty, and disagreement.
- The continual-fleet-gardener backlog item now records the live bounds and the precise missing capability.

## Written directly
- Updated [[backlog/INDEX#^continual-fleet-gardener]] with the live gardening bounds and the move/delete limitation.
