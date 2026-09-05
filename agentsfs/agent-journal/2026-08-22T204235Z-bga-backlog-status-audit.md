---
description: Session — audited every AgentsFS meta-backlog item against current code, tests, RFCs, journals, production records, and the live aa-synced-vault checkout; pruned stale work and clarified every remaining task.
---

# Backlog status audit

## Learned and decided

- Automatic Hub gardening is complete. The only unchecked acceptance step was the first daily 10:00 UTC production run; scheduled maintenance entries exist at that time on 2026-08-11 through 2026-08-15, after the manual production rollout had already verified the same executor.
- The original Markdown To integration epic is complete: Hub and public-share rendering, safe writeback, the bundled agent skill, first-party OAuth/save APIs, atomic workspace transactions, and the Markdown To playground's production Hub open/save/share loop all shipped. Its mirror in the Markdown To backlog still had an open parent despite every defined child being closed, so the parent marker—not an unimplemented deliverable—was stale.
- The parked “Kanban-style Hub view of backlog pages” was superseded by the deployed editable Markdown To backlog workspace. The current design deliberately uses a ranked priority ledger rather than Kanban columns, while supporting richer add/edit/move/archive/detail-note operations.
- The two closed ticket files for contract backlog adoption and embedded Hub projection sync were moved into `backlog/archive/`. Their active spine lines and other completed/dropped historical lines were pruned; git history and existing journal entries retain the trail.
- `aa-synced-vault` is currently on customized contract 0.9.0, not 0.10.0. Its duplicate journal-role defect is already fixed: `afs roles` resolves only `Work Logs/AgentsFS Sessions` as the session journal, while personal `Journal/` is a collection. The real remaining task is an owner-approved manual port to 0.12.0 that preserves its Obsidian/privacy adaptations and introduces the backlog role.
- Contract customization detection already checks every vendored 0.9.0 stock variant, but `ComputeContractDiff` still calls singular `StockContract`; therefore the nearest-variant display bug is real and still open. The separate 0.5.0 stock-variant audit also remains open.
- The local MCP server still registers exactly 12 tools and has no `tasks` tool. The CLI already has the requested single-instance, fleet, done, and owner-blocked views.
- Share-link Markdown rendering still passes no backlog placement/options, unlike the signed-in file handler; backlog-specific styling on ordinary public backlog shares is genuinely missing. Conforming Markdown To backlog workspaces render through their separate renderer and do not close this gap.
- The save API's exposed CORS header list still omits `WWW-Authenticate`; the response and JSON error already name insufficient scope, so this remains a small browser-client ergonomics fix.
- No Mermaid renderer or document-scoped inline-agent/diff-approval surface exists. Cross-workspace task aggregation exists in the CLI, not as a Hub account page.
- Automatic gardening solved scheduling, candidate selection, scoped authority, retries, isolation, and progress reporting, but its model-facing writes still cannot move/delete files. That limitation explains the accumulated journals and unarchived tickets and is the precise remaining scope of the continual-fleet-gardener item.
- The harness-plugin RFC has evolved beyond its old backlog summary: it now proposes optional Claude Code and Codex plugin packages over a shared `afs hook` core, covering session orientation, pre-compaction capture, and remind-once stop checks without transcript synthesis or automatic network mutation. It still needs an owner decision.

## Open

- The active backlog now contains 2 owner decisions, 7 concrete Next tasks, 6 Later tasks, and 1 demand-driven Someday importer. Every line states the missing outcome and current baseline in plain language.
- A Hub projection pull could not run because the enclosing host repository contains unrelated uncommitted Hub UI work. The AgentsFS instance itself was clean and locally reported origin-synced/published before this audit; do not stash or alter the unrelated work merely to force a projection pull.

## Written directly

- Rewrote and pruned [[backlog/INDEX]].
- Created `backlog/archive/INDEX.md`, archived the two closed ticket files without changing their contents, and mechanically retargeted historical wikilinks to their new locations.
