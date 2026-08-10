---
description: "Session note — backlog v2 shipped end to end (contract 0.11.0): directory role, tickets, archive, sub-backlogs, owner-blocked channel, fleet triage, claim/done, Hub rendering; this instance migrated; dispatch RFC drafted."
---

# Backlog v2 shipped (contract 0.11.0)

Owner-directed redesign, ratified decision-by-decision in conversation today, then built. Source of truth: [[backlog-directories]] (accepted) and [[backlog-driven-dispatch]] (draft — five open decisions parked on the spine as ^dispatch-rfc-review).

**Shipped** (commits 9493f5a…59feb0c, all suites green, smoke-tested end to end):
- Core: backlog is a directory role — the directory's INDEX.md is the spine; sub-backlog spines parse as delegated subtrees (ready only under a non-terminal delegating root task); per-file slug namespace with cross-page `[[name#^slug]]` blocker resolution; `— blocked by owner: <q>` channel; `archive/` excluded from parsing, read by `LoadBacklogArchive`; `TasksAcrossInstances` fleet report; legacy 0.10.0 page markers still resolve read-only (+ six new doctor findings).
- Contract 0.11.0: template `backlog/INDEX.md`, rewritten rule 13/Structure/toolkit text, vendored 0.10.0 stock, `afs contract upgrade` migrates a legacy page byte-preserved with links retargeted at `backlog/INDEX`.
- CLI: `afs tasks` grows `--blocked-on-owner`, `--done`, sub-spine labeling, and fleet mode (`afs tasks <search-root>` / `--all-instances`) grouped per instance with no invented cross-project order; new `afs task claim|done` (one-byte marker flip, dated ticket-log append, commit, push; lost CAS race reported with the commit kept).
- Hub: placement-aware rendering (spine/sub-spine/ticket/archived/rollup via ancestor-INDEX lookup), waiting-on-you badges, live delegation chips, ticket `## Log` timelines, archive chips. Verified against a seeded local hub (DOM + computed styles; screenshots flaked in the hidden pane).
- This instance migrated by dogfooding `afs contract upgrade` — backlog.md → backlog/INDEX.md, 8 journal entries' links retargeted, doctor 0 errors.

**Why (operating model, owner's journal 2026-08-09):** invert dispatch — Akshay triages and specs, agents pull. Capture must stay free (bands gate dispatch); the owner-blocked channel replaces interrupts; the fleet view is the morning triage screen. Build order deliberately ran conventions → triage → structure, automation last (dispatch RFC stays draft until the manual loop proves the grammar).

**Ruled out today:** JSONL archive (derived-store-as-source antipattern; archive is a markdown collection + per-year rollups); Linear/database adoption (re-litigated at owner's prompting — atomicity can be added to markdown via git CAS, legibility can't be added to a DB; tripwire recorded: any future index is derived state under `.agentsfs/`); priority metadata across instances (the owner is the cross-project ranking function).

**Cross-repo:** markdownto grew the backlog-workspace pre-spec (workspace layer over untouched backlog@0.1, conventions §13 multi-file transaction contract) — parked in that repo's Now band for owner review; coordinate with ^contract-backlog-spec here.

**Open / discovered:** ^dispatch-rfc-review (owner), ^filetree-index-only (Hub Files view hides INDEX-only sub-backlog dirs), ^deploy-0110 (owner-blocked: Fly deploy), extending the MCP tasks tool for the new surfaces; a concurrent session's commit 4d43f40 swept the staged template/backlog.md deletion early — restored in 7f80104, no lasting damage. Pre-existing malformed frontmatter in the 2026-08-09T004836Z journal entry still stands (gardener).
