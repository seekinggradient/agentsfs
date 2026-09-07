---
checkpointed: "2026-09-07T21:23:44Z"
description: Build and validate episodic memory across the CLI, Hub, and hosted agent
episode_id: ad2dd57acdb2e3fdbffecfb2
started: "2026-09-07T21:23:00Z"
status: running
---
## Intent
Implement the user’s episodic journal end-to-end after the setup and connection audit.

## Progress
Built role-resolved active episodes, bounded bootstrap and retained archives; stable starts, hash-checked checkpoints, eligible-source consolidation, and recoverable local archival. Added CLI, local MCP, and atomic Hub actions. Updated setup, root contracts, gardener instructions, and hosted-agent start/checkpoint/finish behavior.

## Decisions
Keep existing journal folder names and legacy flat entries. Never archive running work by age alone. Key knowledge links to authoritative notes. The gardener writes the adaptive synthesis; deterministic tools validate revisions, budgets, and exact source preservation.

## Validation and open work
Core lifecycle and Hub API tests passed. The full hosted suite exposed an expected tool-list assertion after adding dedicated gardening tools; corrected it. Full suites and production builds are in progress. Release, deployment, and live smoke checks remain.

## Artifacts
Implementation: internal/core/journal.go, cmd/afs/journal.go, internal/hub/apiagent_journal.go; hosted integration lives in agentsfs-eve.
