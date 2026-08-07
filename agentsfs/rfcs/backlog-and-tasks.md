---
description: RFC — structured task tracking for AgentsFS. A role-marked backlog page with a markdown-native task grammar, derived ready-work queries (afs tasks), a session orientation command (afs prime), adaptive tree rendering, doctor findings, and Hub rendering. Contract 0.10.0.
status: accepted
rfc_status: accepted
rfc_scope: project
rfc_owner_approved: true
date: 2026-08-06
sources:
  - Owner design conversation, 2026-08-06 (Beads research → markdown-native tasks decision)
  - Beads (bd) 1.1.2 comparative research report, 2026-08-06
  - internal/core/reserved.go
  - internal/core/frontmatter.go
  - internal/core/links.go
  - internal/core/pipeline.go
  - internal/hub/render.go
  - template/AGENTS.md
  - agentsfs/rfcs/harness-plugins.md
---

# RFC: The backlog — structured task tracking as prospective memory

## Summary

AgentsFS holds an agent's episodic memory (journal), semantic memory (wiki notes), and procedural memory (skills, scripts). This RFC adds **prospective memory**: goals, priorities, and pending work, tracked in a way agents and humans both natively read and write.

The design principle, decided against the alternative of a Beads-style tracker after a full comparative study: **the write path is markdown editing; the read path is derived views.** No new database, no task CLI an agent must learn to *write* with, no IDs beyond human-chosen slugs. One dense, role-marked backlog page per instance carries prioritized tasks in GitHub-flavored checkbox lists; the tooling derives structure from it — a ready-work query (`afs tasks`), a session orientation pack (`afs prime`), health checks (doctor), and a styled Hub rendering.

## Motivation

1. Agents working an instance need somewhere durable to break down goals, prioritize, park ideas, and record discovered work — today that state lands ad hoc in `status.md` prose and journal "Open" sections, invisible to queries.
2. A prioritized backlog that agents maintain is what lets them work *without prompting*: orient, pull the top item, record discoveries, check off, journal.
3. The owner needs a cross-project idea parking lot with priorities; per-instance backlogs (with future Hub aggregation) provide it.
4. Beads validates the demand for agent task tracking but its architecture (a versioned SQL database as source of truth, ~85% of its 291k LOC serving storage/orchestration) contradicts this project's principles: files are the source of truth, no daemons, names as identifiers, ride the training distribution. Its injected conventions also conflict with ours in-session. We adopt its valuable concepts — ready-work, structured status, session priming — on AgentsFS primitives.

## The backlog role

- A single **page** (not directory) declares `agentsfs_role: backlog` in its own frontmatter. This extends role resolution, which today reads only directory `INDEX.md` markers; backlog is the first page-level role.
- Exactly one per instance. Multiple marked pages → doctor finding `duplicate-backlog`; `afs roles` resolves the first in sorted path order (consistent with journal/scratch duplicate handling). No classic-name fallback (the role is new; the marker is the only truth).
- Template default: `backlog.md` at the instance root, shipped with the rules and a worked example (the journal-`INDEX.md` pattern: conventions travel with the page).
- `afs roles [--json]` gains `backlog`, `backlog_source` (`marker`/`none`), `duplicate_backlog`.

## Task grammar

Parsed from the backlog page only. Everything is standard GFM plus two conventions already in the training distribution (Obsidian task-status variants and block anchors).

1. **Task line** — a list item starting with a checkbox marker:
   - `- [ ]` open · `- [/]` in progress · `- [x]` done · `- [-]` dropped
   - `x` case-insensitive; `-`/`*`/`+` bullets all accepted, `-` canonical.
2. **Bands** — `##` headings are priority bands. Recognized (case-insensitive): `Now`, `Next`, `Later`, `Someday`, `Done`. Order within a band is priority, top first. `Someday` is the parking lot (never "ready"); `Done` is the archive section. Tasks under unrecognized headings are parsed and listed (band = that heading's text) but excluded from ready.
3. **Nesting = decomposition.** Indented child tasks break down the parent. A parent is complete only when its children are; the parser never auto-flips a parent — doctor flags a checked parent with open children.
4. **Slug** — an optional trailing block anchor makes a task referenceable: `- [ ] Ship hub sync polish ^hub-sync-polish`. Kebab-case (`^[a-z0-9][a-z0-9-]*$` after the caret), unique within the file. Referenced with standard wikilink anchors: `[[#^hub-sync-polish]]` same-file, `[[backlog#^hub-sync-polish]]` from anywhere (name-resolved like every wikilink). Journal entries citing the task they served use exactly this.
5. **Blockers** — the phrase `blocked by` (case-insensitive) in a task line marks it blocked, conventionally `— blocked by [[#^slug]]`. If the annotation contains task references, the block lifts automatically when **all** referenced tasks are done or dropped. A plain-text blocker (`— blocked by adjuster response`) holds until the annotation is edited away.
6. **Ordering is priority, never sequencing.** Reordering lines is the reprioritization gesture and must not change dependency meaning; sequencing is expressed only by nesting or explicit blockers.
7. **Graduation.** A task that accumulates real state becomes its own note; the backlog line links to it (`- [/] Hub sync polish → [[hub-sync-status]] ^hub-sync-polish`). The backlog stays dense.
8. **Hygiene.** The gardener periodically prunes `## Done` and flags stale `[/]` items. Git history plus the journal are the audit trail, so pruning loses nothing.

### Ready semantics

A task is **ready** iff: status is open (`[ ]`) ∧ its band is `Now`, `Next`, or `Later` ∧ it has no open or in-progress children ∧ neither it nor any ancestor has an active blocker ∧ no ancestor is dropped. Ready ordering: band (`Now` → `Next` → `Later`), then document order. In-progress (`[/]`) tasks are surfaced first and separately — they are resumed, not re-claimed. No claiming protocol: the agent model is one agent per user, and concurrent writers are already serialized by git / Hub CAS at commit granularity.

## Worked example (ships in the template)

```markdown
---
description: Project backlog — prioritized ideas and work items
agentsfs_role: backlog
---

## Now
- [/] Embedded hub sync status polish ^hub-sync-polish
  - [x] Fix PJAX test flake
  - [ ] Update shipped-docs page
- [ ] Draft tasks RFC — blocked by [[#^prime-design]]

## Next
- [ ] Prime adaptive tree rendering ^prime-design

## Later
- [ ] Kanban view of backlog pages on Hub

## Someday
- [ ] Beads issues.jsonl importer

## Done
- [x] Beads research → [[beads-research-report]]
```

## Tooling

### `afs tasks`

Read-only derived view; parses the backlog page on demand (no index tables — the page is small by design).

- Default: in-progress tasks, then ready tasks grouped by band, with a one-line count of blocked/parked/done.
- `--all` (full parse), `--band <name>`, `--ready` (ready only), `--json`.
- JSON per task: `file`, `line`, `text` (marker and slug stripped), `status` (`open|in_progress|done|dropped`), `band`, `slug`, `depth`, `parent_slug` (nearest sluged ancestor, empty otherwise), `blocked` `{active, reason, refs[]}`, `open_children` (count), `ready` (bool).

### `afs prime`

The session orientation pack — read-only, budgeted, built entirely from existing primitives. Sections in order:

1. **Identity** — root `INDEX.md` description, contract version, one line: read `AGENTS.md` for the contract.
2. **Tasks** — `[/]` items, then top ready items (≤10 lines total), then: `Full backlog: afs tasks`.
3. **Tree** — adaptive rendering within the remaining budget (below).
4. **Recent journal** — the newest two journal entries' filename + description.
5. **Pointers** — `afs docs agent-start`, `afs search "<words>"`.

`--budget N` (tokens, default 4000, chars÷4 estimator — same as context packs). Designed to be wired to session-start/pre-compaction hooks by the harness-plugins RFC; until then it is run manually or from hand-written hooks. Errors inside an instance are real errors; hook packaging (`--hook-json` etc.) is explicitly the harness-plugins RFC's scope, not this one's.

### `afs tree --budget N`

Degradation ladder, richest view that fits the budget: full tree with descriptions → depth-capped with descriptions (decreasing depth) → depth-1 names only → root description only. Prime uses it; agents can too.

### Doctor findings

- `duplicate-backlog` — more than one page marked `agentsfs_role: backlog`.
- `duplicate-task-slug` — a `^slug` repeated within the backlog.
- `dangling-task-ref` — a blocker reference to a slug that doesn't exist.
- `task-parent-inconsistent` — a `[x]` parent with open/in-progress children.

### Hub rendering

A file whose frontmatter declares `agentsfs_role: backlog` renders as a styled backlog: `[/]` and `[-]` markers rendered as proper status controls (in-progress and dropped states — GFM alone only knows `[ ]`/`[x]`), band headings styled with per-band progress chips (e.g. `2/5`), `^slug` anchors become link targets (slug text hidden from prose, exposed as a copy-link affordance), active `blocked by` lines get a badge. Read-only rendering; editing stays in files.

## Contract 0.10.0

- Template `AGENTS.md` frontmatter → `agentsfs_contract: 0.10.0`; vendor the 0.9.0 stock text into `internal/core/contracts/`.
- New contract rule 13 — **Track intentions in the backlog**: keep the backlog page current — pull from the top, record discovered work in the appropriate band, check off what's done; details live in the backlog page itself.
- Structure section: backlog joins journal/scratch/collection as the fourth reserved role (the first page-level one).
- Toolkit section: add `afs tasks` and `afs prime` lines.
- `afs contract upgrade` from ≤0.9.0: rewrite AGENTS.md per the existing machinery and lay down `backlog.md` **only when no page already declares the role** (never overwrite; collision handling per existing template-file rules).
- `prompts/gardening.md`: add backlog hygiene (prune `## Done`, flag stale `[/]`, verify slugs referenced from journal entries still resolve).

## Out of scope

Claiming/leases and any dispatch protocol; cross-instance backlog aggregation (a future Hub view); harness hook packaging (harness-plugins RFC); due dates and numeric priorities; automatic task extraction; Beads import/export; task state anywhere outside the backlog page.

## Decision log

- **2026-08-06** — Owner approved: markdown-native tasks over Beads adoption/integration; `[/]`/`[-]` status variants accepted; nesting-as-decomposition with explicit `blocked by` for cross-cutting edges; `^slug` block anchors for references; contract text carries the convention; Hub renders backlog pages as HTML. Prime includes top tasks + adaptive tree (count-tiered idea refined to budget-tiered).
- **2026-08-06** — Ordering-is-priority-never-sequencing locked in: reordering must stay a safe gesture.
- **2026-08-06** — Backlog is the first page-level role; exactly one per instance; no classic-name fallback.
