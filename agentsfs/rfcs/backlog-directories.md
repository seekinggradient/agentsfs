---
description: RFC — backlog v2. The backlog becomes a directory role — a dense spine page plus earned per-ticket detail files, an archive collection, and delegated sub-backlogs — with the owner-blocked convention, cross-instance triage, and claim/done tooling. Contract 0.11.0.
status: accepted
rfc_status: accepted
rfc_scope: project
rfc_owner_approved: true
date: 2026-08-09
sources:
  - Owner design conversation, 2026-08-09 (journal excerpts on context-switching and the pull-based operating model)
  - agentsfs/rfcs/backlog-and-tasks.md (contract 0.10.0 — extended by this RFC)
  - Buy-vs-build re-litigation vs Linear/database, 2026-08-09 (build reaffirmed; thin tools over markdown)
  - markdownto specs/backlog/SPEC.md (backlog@0.1 — the single-file grammar this structure contains)
---

# RFC: Backlog directories — tickets, archive, and the pull-based operating model

## Summary

Contract 0.10.0 made the backlog a single role-marked page. This RFC grows it into a **directory role** while keeping the page at its center: a `backlog/` directory whose spine page carries the same dense task grammar, surrounded by per-ticket detail files (earned, never default), an archive collection that absorbs unbounded growth, and optional sub-backlog directories for workstreams — reachable only by delegation from the root spine, which remains the sole priority authority.

Three workflow conventions ship with the structure: the **owner-blocked channel** (`— blocked by owner: <question>` — agents park questions and move on), **ticket log sections** (append-only dated agent notes, separated from the synthesized body), and **cross-instance triage** (`afs tasks <search-root>` aggregates ready views across every instance under a root). Two ergonomic wrappers, `afs task claim` and `afs task done`, make the common state flips one command without changing the substrate.

The motivating vision, stated so the design is judged against it: the owner's interface to a project should be *writing tickets*. Agents pull work from the backlog (the contract's rule 13 + `afs prime` already form the pull protocol); the owner triages, specs, and reviews. Dispatch automation is deliberately out of scope here — it is designed separately in [[backlog-driven-dispatch]] and implemented only after the manual loop proves the grammar.

## Motivation

1. **Backlogs grow without bound.** A good backlog is also the history of what got done, why, and when. The 0.10.0 answer — gardener prunes `## Done`, git is the archive — fails agents in practice: git archaeology is a poor query surface, and a heavily-edited spine's history is noise. Growth needs somewhere legible to go cold.
2. **Tickets accumulate state.** Agents need to leave dated notes, review findings, screenshots, and repro details on the item they're working — Linear-style comments. A single page cannot absorb that and stay dense; density is what makes `afs prime` cheap and reordering-as-reprioritization safe.
3. **Workstreams need namespacing.** Larger projects have areas whose decomposition would swamp the root page, but which are too small to deserve their own embedded instance.
4. **The owner's productivity problem is being the dispatcher.** Ideas arrive constantly; engaging each one as it arrives destroys the big picture. The structure must make *capture free and inert* (a line in a band, from anywhere, without visiting the project) and make *triage the single deliberate gesture* that releases work.
5. **Buy-vs-build was re-litigated against Linear/a database and build reaffirmed** on structural grounds: atomicity can be added to markdown (git CAS), legibility cannot be added to a database; the clone exit ramp must stay true for the most operationally important files; and the backlog is prospective memory co-located with the other memory types — the product's differentiator, not internal tooling. Ergonomics gaps close with thin tools, not a new store.

## The backlog directory role

- `agentsfs_role: backlog` moves from a page's frontmatter to a **directory's `INDEX.md`** frontmatter, joining journal/scratch/collection as a directory role. 0.10.0's page-level role is retired (see Migration); the "first page-level role" experiment ends after one version.
- Exactly one backlog directory per instance. Duplicates → doctor `duplicate-backlog`; first in sorted path order wins (unchanged policy).
- **The spine** is the directory's `INDEX.md` itself. One file is both the directory's self-description and the task page: frontmatter `description:` labels it, the body is the 0.10.0 task grammar unchanged (bands, four-state checkboxes, nesting, `^slug` anchors, blockers). This keeps "one page to read, one page to edit" literally true and means every existing tool that renders or parses the spine needs no second file discovery step.
- Template default: `backlog/INDEX.md` with the five-line legend. `afs roles` reports `backlog` (directory path), `backlog_spine` (the INDEX.md path), `backlog_source`, `duplicate_backlog`.

### Ticket detail files

- A ticket file is an ordinary note inside `backlog/`, linked from its spine line: `- [/] Voice v3 lanes → [[backlog/voice-lanes]] ^voice-lanes`. Naming: the task's slug is the recommended filename.
- **Earned, never default.** A ticket gets a file when it accumulates real state — a spec worth writing, a decision trail, review artifacts — never at creation. The graduation trigger is mechanical: the moment a spine line wants more than its text, links, and blocker clause, it graduates. Comments never live on the spine.
- **Body vs log.** The top of a ticket file is synthesized state, updated in place per contract rule 5: current understanding, the spec, repro, decisions. A `## Log` section at the bottom is an append-only dated record of events — `- 2026-08-09 — tried X, blocked on Y` — newest last. Timestamps here are fact-dates (legal under rule 8). The split mirrors journal-vs-durable-notes: the log records what happened; the body says what is true. A ticket whose body has gone stale behind its log is gardener work.
- Non-markdown artifacts (screenshots, traces) sit beside the ticket file; the ticket links them. Rule 1's collective-description escape (directory INDEX describes what can't describe itself) already covers them — the spine IS the INDEX.

### The archive

- `backlog/archive/` is a **collection** (`agentsfs_role: collection` in its own INDEX.md), created lazily on first archive.
- When a ticket closes, the sweep moves its detail file into `archive/`, stamps `closed: YYYY-MM-DD` frontmatter (a fact-date), and deletes the spine line. Archived files are moved in and never edited — append-only as a convention on the collection's existing preservation rules.
- One-liner tasks (no detail file) roll up into per-year pages: `archive/2026.md`, one line per closed task appended by the sweep — date, terminal marker, text, slug, and a link to the journal entry or ticket file when one exists. Per-year sharding bounds any single file.
- **The gardener owns the move**, not the finishing agent. The hot path stays "check the box, journal, commit"; archiving is hygiene. Doctor flags the gap states (see findings).
- `afs tasks --done` reads the archive (rollup pages + `closed:` frontmatter) — the derived history query. `--json` emits structure; JSONL as a *source* format is rejected (a derived artifact pretending to be source; invisible to search, links, and humans).

### Sub-backlogs and priority authority

- A workstream may have `backlog/<area>/` — the same shape recursively: its INDEX.md is a spine, its tickets sit beside it, it may (rarely) have its own archive. **No role marker**: a sub-backlog is defined by being a subdirectory of the backlog directory with task grammar in its INDEX.md, not by carrying `agentsfs_role`. One backlog role per instance stays true.
- **The root spine is the sole priority authority.** Nothing in a sub-backlog is ready on its own. A root task *delegates* to it — `- [/] Voice v3 → [[backlog/voice/INDEX]] ^voice-v3` — and the sub-backlog's ready tasks surface only while a non-terminal root task links its spine. This is nesting-as-decomposition extended across files: the root line is the parent; the sub-spine's tasks are its children. Ready ordering: the delegating line's position ranks the whole subtree; within the subtree, the sub-spine's own bands and document order apply.
- Un-delegated sub-backlogs are legal (parked workstreams) — their tasks are parsed by `--all` but never ready, and doctor surfaces them as info, not error.
- Sizing guidance (contract text, not enforcement): a `##` heading before a sub-backlog; an embedded instance when the workstream outgrows shared identity.

## Workflow conventions

### The owner-blocked channel

- Grammar: an ordinary blocker whose text begins `owner:` — canonical form `— blocked by owner: <the question, one line>`. Case-insensitive on `owner`. It is a prose blocker (never auto-lifts); the annotation is edited away by the owner's answer.
- Contract meaning: an agent that hits a decision only the owner can make writes the question into the blocker, **parks the task, and pulls the next ready item** — it does not stall and does not interrupt. The owner's triage pass drains these in batch.
- `afs tasks` surfaces owner-blocked items in a dedicated section (they are questions *for the reader when the reader is the owner*); `--blocked-on-owner` filters to them, across instances too — the owner's inbox.

### Cross-instance triage

- `afs tasks <search-root>` (and `--all-instances` from inside a checkout's parent) reuses the fleet-discovery machinery from `afs status <search-root>`: discover instances, parse each backlog, emit a grouped view — instance heading, then in-progress / owner-blocked / top ready per band. Bounded, read-only, no symlink-following: `status`'s scanning rules verbatim.
- **No global order is invented across instances.** Grouping is by instance; the owner is the cross-project root spine. The view informs the choice of which project to dispatch; it does not rank `Now` in one repo against `Now` in another.
- `--json` nests per-instance results under the existing fleet-scan envelope (`scopes[].complete` honesty rules apply).

### Claim/done ergonomics

- `afs task claim <slug>`: flips `[ ]`→`[/]` on the spine (root or sub), appends a dated claim line to the ticket's `## Log` when a ticket file exists, commits, and pushes when a remote is configured. A rejected push is reported as a lost race with the next ready item suggested; the local commit is preserved for the agent to rebase-and-retry or release. No lease state beyond the marker; stale `[/]` detection stays gardener/doctor work.
- `afs task done <slug> [--drop]`: flips to `[x]` (or `[-]`), refuses when open children exist (mirror of the parent-inconsistency finding), same log/commit/push behavior.
- Both are conveniences over edits any agent can make by hand; the substrate is unchanged and the commands are optional everywhere the contract is.

## Contract 0.11.0

- Template `agentsfs_contract: 0.11.0`; vendor the 0.10.0 stock text.
- **Rule 13 rewritten** around the directory: pending work lives in the backlog directory — the spine (its `INDEX.md`) for priorities, ticket files for accumulated state (body = current truth, log = dated events), the archive for what closed. Pull from the top; break down before starting; record discovered work in its band; park questions for the owner with `— blocked by owner: …` and move on; capture ideas from anywhere without engaging them.
- Structure section: backlog rejoins the directory roles; the page-level-role paragraph is removed; sub-backlog and archive shapes described in two sentences each.
- Toolkit section: `afs tasks` gains the search-root/`--done`/`--blocked-on-owner` forms; `afs task claim|done` added.
- `afs contract upgrade` from 0.10.0: **migrates the existing page role** — creates `backlog/`, moves the marked page's body into `backlog/INDEX.md` (preserving frontmatter, dropping the page-level marker in favor of the directory marker), rewrites links to the old page name, and reports what it did. From ≤0.9.0: lays down `backlog/INDEX.md` fresh. Never overwrites existing content; collisions follow existing template-file rules.
- `prompts/gardening.md`: the archive sweep (move closed tickets, stamp `closed:`, append rollup lines, prune spine `## Done`), stale-`[/]` flagging, body-behind-log ticket refresh, delegation-link integrity.

## Doctor findings

Kept: `duplicate-backlog`, `duplicate-task-slug`, `dangling-task-ref`, `task-parent-inconsistent`. New:

- `backlog-page-role-legacy` — a page (not directory INDEX) carries the marker: works read-only, upgrade suggested (this is the 0.10.0 shape).
- `ticket-unarchived` — a detail file in `backlog/` whose spine line is gone (closed or deleted) but which was never archived.
- `ticket-orphaned` — a detail file no spine line links (root or sub).
- `archive-live-ticket` — a file in `archive/` still referenced by an open spine line, or lacking `closed:`.
- `sub-backlog-undelegated` — info: a sub-backlog no root task delegates to.
- `delegation-terminal` — a root delegation line is terminal while its sub-spine has non-terminal tasks (the cross-file parent-inconsistency).

## Hub rendering

- The 0.10.0 styled backlog rendering moves to the directory's INDEX.md and extends: delegation links render with a live progress chip computed from the sub-spine (`voice 3/7`); ticket links render with a state dot; the directory view groups spine / tickets / archive.
- Ticket pages render with the `## Log` styled as a timeline; archive rollup pages render read-only with closed-date grouping.
- Kanban stays in the project backlog's Later band; nothing here blocks it.

## Out of scope

Dispatch automation (push-triggered spawns, agent pools, review pipeline, merge policy) — designed in [[backlog-driven-dispatch]], implemented only after the manual loop is proven. Cross-instance *ordering* (the owner is the ranking function). Leases beyond the `[/]` marker. WIP limits, due dates, numeric priorities. Beads import. MarkdownTo spec changes (tracked in that repo; the backlog@0.1 single-file grammar remains valid for any one spine, and the directory evolution is specified there).

## Decision log

- **2026-08-09** — Owner: backlog becomes a directory with spine + per-ticket files; archive absorbs unbounded growth and done-history ("a good backlog is also a history of what got done: why and when"); sub-folders for workstreams. JSONL archive proposed and **rejected** in favor of archive-as-collection + per-year markdown rollups; owner accepted.
- **2026-08-09** — Root spine is the sole cross-file priority authority; sub-backlog work is ready only via delegation from a non-terminal root line. Alternatives (band-merge across files; priority metadata) rejected as re-introducing tracker machinery.
- **2026-08-09** — Ticket files are earned, never default; body-vs-log split is contractual; comments never live on the spine.
- **2026-08-09** — Owner-blocked channel, cross-instance triage, and claim/done wrappers adopted as the pull-model enablers; build order agreed: conventions and triage before structure, structure before any automation.
- **2026-08-09** — Buy-vs-build vs Linear/database re-litigated at owner's prompting; build reaffirmed (asymmetry: markdown can gain atomicity via git CAS, a database cannot gain legibility; exit ramp; memory co-location). Tripwire recorded: real-time/cross-repo dispatch needs → derived index under `.agentsfs/`, never a source-of-truth store.
- **2026-08-09** — Spine = the directory's own INDEX.md (one file is label and task page), chosen over a separate `backlog.md` beside a thin INDEX to keep single-file reading/rendering true.
