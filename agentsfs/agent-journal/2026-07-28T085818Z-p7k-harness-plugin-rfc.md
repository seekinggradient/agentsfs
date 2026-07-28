---
description: Session — researched Claude Code and Codex plugin hooks and drafted the proposed harness-plugins RFC.
---

## Learned / decided

- Claude Code and Codex both now package lifecycle hooks inside installable plugins, materially improving the integration seam compared with the bespoke per-harness approach rejected in July 2026.
- Plugins should remain optional adapters over a versioned `afs hook` protocol; the filesystem contract, connection block, and journal remain the complete portable system.
- The proposed first version covers read-only startup orientation, pre-compaction capture context, and a remind-once stop compliance check.
- Transcript distillation and automatic pull, commit, or push are excluded because they remain semantically weak or operationally risky.
- Claude Code should be implemented first because its hook surface is more mature; Codex should use the same core through command hooks.

## Open

- Validate exact hook payloads and behavior in live Claude Code and Codex surfaces.
- Decide whether session attribution is reliable enough for stop checks.
- Decide how plugin artifacts are generated from the existing skill sources and distributed.
- Decide whether plugin installation belongs in `afs setup` or an explicit plugin command.

## Written directly

- Added [[harness-plugins]] with the complete proposal, alternatives, failure behavior, implementation phases, and decision request.
- Added the RFC to [[rfcs/INDEX]].
