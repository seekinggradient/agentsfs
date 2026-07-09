# agentsfs — Execution Plan

Companion to [agentsfs-source-of-truth.md](agentsfs-source-of-truth.md), which records the settled idea. This document records how we build it: the working model, the architecture stance, the layers, the gates between them, and the decisions made along the way. Sections are **PROPOSED** until discussed, **AGREED (date)** after.

The standing rule carries over unchanged: simplicity first, build from first principles, actively fight the urge to overcomplicate.

## Working model — AGREED (2026-06-12)

**Claude builds; the owner decides.** The entire system — template, prompts, core library, CLI, search, MCP — is built autonomously by Claude, with the owner doing nothing operationally. The owner settles decisions in this doc and reviews what lands at each gate.

**Validation is lightweight, not absent.** No eval infrastructure for now (parked). Instead, each layer ends with a live demonstration: Claude runs a fresh-context agent session against a real instance and shows the owner what the agent actually did. Real feedback still drives prompt iteration — it just comes from watching real runs, not from a pipeline.

## Build philosophy — AGREED (2026-06-12)

1. **Ship the layer, then watch a real agent use it.** A layer is done when its demo holds up — a fresh agent, a real instance, the behavior the layer promises — not when the code exists. What the demo surfaces sets the next layer's scope.
2. **The contract must never come to depend on the toolkit.** At every layer we re-verify the zero-tooling path (plain files, plain `ls`/`grep`, the self-describing root) still works.

## Architecture stance — AGREED (2026-06-12)

Four standing answers that constrain all layers:

- **Standard filesystem is the interface; the toolkit augments, never wraps.** Agents use their native fluency — `ls`, `grep`, `cat`, plain reads and writes — directly on the files. There is no `afs ls`. CLI commands exist only for what plain tools can't do cheaply: aggregated views (tree with descriptions), derived structure (backlinks, search), cross-file rewrites (rename), and health (doctor). A wrapper CLI is rejected because it would make the toolkit load-bearing (violating the contract/toolkit split) and tax the training distribution (Principle 5).
- **One core, thin surfaces.** A single core library implements every capability once; the CLI and the MCP server are thin adapters over it. This holds from the first line of code, even though MCP doesn't exist until Layer 4.
- **The index is a sidecar, never the truth.** Derived state lives in a single SQLite file under `.agentsfs/` — embedded, zero-config, gitignored, fully reproducible via `reindex` from the files alone. The inverse pattern — database as truth, filesystem as a rendered view — is explicitly rejected: it would force us to roll our own versioning and forfeit git's provenance, the zero-tooling path, and plain inspectability all at once.
- **LLM and embedding services are optional enhancers, never core dependencies.** Off-the-shelf embedding APIs (user-supplied key) are acceptable behind a pluggable provider, with graceful degradation: no key means no semantic search — everything else, including full-text search, works fully.

## The layers — AGREED (2026-06-12)

Each layer lists what we build, what we deliberately do not build yet, and its gate (a live demo, not a ceremony).

### Layer 1 — the contract exists and setup is one command

**Build:**

- **The template.** Root `AGENTS.md` / README (the self-describing root — the contract in the form agents consume), starter-structure proposal, `.gitattributes` for LFS, reserved names (`scratch/`, `.agentsfs/`) stubbed.
- **Onboarding prompt v0** and the gardening-free essentials of the prompt pack.
- **The core-library skeleton and setup CLI.**
  - `afs init <path>` lays down the template, runs `git init`, and makes the first commit at exactly the requested path.
  - `afs connect <path>` appends a marker-fenced connection block to the nearest `AGENTS.md` / `CLAUDE.md` — with user approval — so agents working in that project learn the instance exists. Load-bearing, not a nicety: harnesses bootstrap by reading these files; if the pointer isn't there, no agent ever learns the substrate exists. Markers make the block idempotent to update and clean to remove.
  - `afs setup [path]` is the friendly one-command flow: create or reuse a personal agentsfs (default `~/agentsfs`), then connect the current project to it.
  - Two connection targets, same mechanism: the instance's own root (automatic — the self-describing root *is* the connection point) and external places (another project's `AGENTS.md`, or the harness's global config).
- **One hand-built fixture**: a small reference instance in a known state, used as test data for everything that follows.

**Deliberately not built:** every other command; evals; MCP.

**Gate 1:** live demo — in a clean directory, `setup` produces a working connected instance; a fresh-context agent session orients from the root unaided, does a real task, and writes notes that honor the conventions. Owner reviews the session and the resulting files.

### Layer 2 — orientation and health: the deterministic, index-free commands

**Build:** `tree` (descriptions + git freshness), `doctor` (orphans, missing descriptions, dead/ambiguous links, fragmentation flags), `backlinks`, `rename`. The boundary that makes this one layer: every command is a deterministic read or rewrite of files + git with **no persistent index** — link scans run on the fly at v1 scale. The gardening prompt v0 lands here too: doctor's output is the gardener's worklist, so they ship as a pair.

**Deliberately not built:** search, MCP, any persistent index.

**Gate 2:** live demo — a gardener session on a deliberately messy instance consumes doctor's worklist and leaves it measurably denser and healthier, reviewable in git.

### Layer 3 — search: the first derived state

**Build:** full-text first — SQLite FTS5 in the `.agentsfs/` sidecar; `reindex` rebuilds from zero and must reproduce the index exactly (Principle 2, enforced by code). Then semantic: pluggable embedding provider (off-the-shelf API with user key to start; a local-model provider can join later), vectors in the same SQLite file, brute-force cosine at personal agentsfs scale until measurement says otherwise.

**Gate 3:** live demo — queries that grep/backlinks handled poorly on the real instances succeed via search.

### Layer 4 — surfaces: MCP and packaging

**Build:** the MCP server as a thin adapter over the same core library (no logic written twice); distribution/packaging; connection snippets for more harnesses. MCP is deferred to here deliberately: CLI-fluent agents shell out natively; MCP earns its place when a harness that can't shows up.

**Gate 4:** live demo — a harness that cannot use the CLI works against a real instance via MCP.

### Layer 5 — the journal: session capture without harness integration — AGREED (2026-07-06)

**The gap it closes.** The contract's write path is high-discipline at the worst possible moment: end of session, context full, task "done" — find the right note, merge without duplicating, relink, cite. Agents that skip it fail silently, and the session's knowledge evaporates. Per-harness fixes (hooks, transcript distillation) were considered and **rejected**: N fragile integrations, each tracking a moving config format and transcript shape, and a standing violation of Principle 5. The one interface every harness has is the filesystem itself — so capture becomes a file convention.

**The design: split capture from curation.** Capture is perishable (a session that ends unjournaled is lost forever); curation is deferrable (raw material waits in git). So capture is made the cheapest possible ask — append one file — and curation moves to the gardener. Producers append, one file per session, so concurrent agents (laptop, sprite, a collaborator's harness) never conflict; the gardener is the single consumer that folds entries into durable notes and deletes them. Git history is the archive. The memory shape this creates, explicitly: `journal/` is episodic memory (append-only, time-ordered, disposable after consolidation), the notes are semantic memory (dense, update-don't-append), and a future `skills/` layer is procedural. The gardener is the consolidation pass between layers.

**Build (contract bump 0.2.0 → 0.3.0; template, prompts, skills, and fixtures change as one unit per the foundations rule):**

- **Contract:** `journal/` becomes the third reserved name. New rule adjacent to `scratch/`'s: when you finish a unit of work, append a session note to `journal/` — one file, `YYYY-MM-DD-<slug>.md`, `description:` required — before committing. What happened, what was learned or decided, what was ruled out, what's open, and what was already written into durable notes directly ("written directly," so the gardener doesn't double-process). The journal is the floor, not the ceiling: direct durable writes stay preferred. Orientation gains the recall side: read the newest journal entries for recent state.
- **`template/journal/INDEX.md`:** the detail lives here, not in the root contract (the tree explains itself): entry shape and example, append-only / one-file-per-session, consumed-and-deleted semantics.
- **Connection block** ([internal/core/register.go](../internal/core/register.go) + [prompts/connection-snippet.md](../prompts/connection-snippet.md)): one added trigger line. The agent at capture time is standing in the *project*, not the instance — the block is the only agentsfs text guaranteed to be in its context, so this sentence is the highest-leverage line in the design. Old blocks still work (they already say "read AGENTS.md first," which now teaches journaling); re-running `afs connect` refreshes them.
- **Doctor:** `journal/` exempt from stub/orphan checks (episodic entries are expected to be sparse and unlinked); `description:` still required (it is the timeline label). New deterministic check `journal-backlog` — too many entries or oldest too old — so a gardener that isn't running is a visible finding instead of silent rot.
- **Gardening prompt + `agentsfs-garden` skill:** new first step — empty the journal: fold each entry into durable notes per update-don't-append (carry citations, respect "written directly"), then delete it.
- **`afs contract upgrade`:** also lays down `journal/INDEX.md` when missing, so the explicit upgrade produces the complete 0.3.0 shape as one reviewable diff. The stderr stale-contract notice needs no change.
- **Fixture** updated; MCP inherits everything through core with no server change.

**Deliberately not built:** any harness hook or transcript integration (files are the only carrier; at most a single reference Claude Code hook may exist someday as documentation, never a supported surface); the **scheduled/hosted gardener** — the consumer that makes the loop automatic — which is Hub work (see [hub-execution-plan.md](hub-execution-plan.md); prereq: per-user cost caps on the LLM proxy); server-side journaling in the hosted agent (agentsfs-chat already persists full transcripts, so its capture can be deterministic rather than compliance-based — lands with the Hub work); a generalized drop-anything `inbox/` (parked — one concept per reserved name).

**Gate 5:** (a) a fresh-context agent in a connected project finishes a real task and appends a conforming journal entry unaided; (b) a later gardening session consumes it into durable notes, deletes it, and leaves doctor clean; (c) migration: a 0.2.0 instance gets the stderr notice, doctor flags `contract-version`, and `afs contract upgrade` yields the full 0.3.0 shape including `journal/`.

### Layer 5a — reserved dirs by declaration, not by name — AGREED (2026-07-08)

**The incident that forced this.** The first real Obsidian-vault adoption (day one of 0.3.0) collided immediately: on macOS's case-insensitive filesystem, the template's `journal/INDEX.md` landed inside the user's pre-existing `Journal/` — their personal diary — pointing the contract's append rule and the gardener's *consume-and-delete* semantics at personal data. The session agent adapted the vault's AGENTS.md by hand, which exposed the second flaw: `afs contract upgrade` replaces AGENTS.md wholesale, so the adaptation would be silently clobbered by the next upgrade, re-arming the hazard. Root tension: reserved names chosen for familiarity (Principle 5) are exactly the names humans already use in their vaults, and adopting an existing vault *requires* contract adaptation while upgrade assumed stock text. `scratch/` has the same collision class with worse semantics (deletion).

**The design (contract 0.4.0):**

1. **Marker is the truth; the name is only a default.** A directory is the session journal / scratch space only if its `INDEX.md` frontmatter declares `agentsfs_role: journal` / `agentsfs_role: scratch`. All tooling — doctor exemptions, `journal-backlog`, upgrade lay-down, gardener instructions, the connection block — resolves reserved dirs by marker, never by name. An instance relocates a reserved dir by marking a different directory. Doctor: `info` when no journal is marked; `error` on duplicate markers for one role.
2. **Collision-proof default names.** Fresh templates ship `agent-journal/` and `agent-scratch/` — near-zero collision with human vault folders, still self-explanatory. Hidden dotfolders were considered and rejected: Obsidian hides them (invisible in exactly the vault-adoption case), the contract already defines dot-prefix as machine territory holding no knowledge, and journal entries are knowledge in transit that must stay visible (tree timeline, web view, human inspection).
3. **Migration marks in place, never moves.** `contract upgrade` on a 0.3.0-shaped instance recognizes its `journal/`/`scratch/` INDEX as stock template text and adds the marker in place — no renames, no data moves; old names stay valid forever because the marker is what counts. Lay-down gains a case-insensitive collision guard: an existing unmarked dir matching a reserved default name (any case) is never claimed — skipped with a warning naming the collision.
4. **Upgrades respect adaptation — and equip the agent to port it.** Stock texts of released contracts are embedded; `afs contract upgrade` refuses without `--force` when the instance's AGENTS.md does not match the stock text of its declared version, instead of silently replacing it. The refusal is not a dead end: `afs contract diff` prints two labeled diffs — the instance's adaptations (stock-of-its-version → current text) and the upgrade delta (stock-of-its-version → new stock) — and `afs contract current` prints the full new text, so the upgrading agent holds the complete three-way picture and performs the merge itself, bumping `agentsfs_contract:` when done. An *automatic* three-way merge was considered and rejected: prose merges can be textually clean yet semantically contradictory (a custom journal-location rule merges cleanly next to new stock lines pointing at the default location), and per Principle 1 the tool has no judgment to detect that — the agent doing the upgrade does. The gardener prompt encodes the porting flow and forbids `--force` over an adaptation without explicit user instruction.
5. **`afs connect` resolves the marked journal's actual path** into the connection block's trigger line at write time; re-running refreshes it.

**Gate 5a:** (a) fresh init produces marked `agent-journal/` + `agent-scratch/`, doctor clean; (b) a 0.3.0 instance upgrades: markers added in place, no renames, doctor clean; (c) a vault with pre-existing `Journal/` (case-insensitive) upgrades: the dir is not claimed, the warning names it, and marking a different dir relocates the journal for doctor/backlog/connect; (d) a customized AGENTS.md refuses to upgrade without `--force`.

## Foundations: repo and working setup — AGREED (2026-06-12)

- This directory becomes a git repo now; single project repo: `docs/`, `template/`, `prompts/`, `fixtures/` (hand-built reference instances — fixed, known test data), and `core/` / `cli/` / `mcp/` per the language's packaging norms.
- Template, prompts, skills, and fixtures change as one unit: a contract change is not done until all four agree. (`skills/` added 2026-06-12: Claude-native packaging of the prompt pack — setup, remember, garden — thin wrappers that defer to each instance's AGENTS.md as the contract.)
- Default CLI name: `afs` (short, typeable by agents and humans alike); `agentsfs` as an alias if packaging allows.

## Topology: where an instance lives relative to a codebase — AGREED (2026-06-12)

The question that forced this: if `afs init` runs inside a git repo, knowledge and code share a git history — which entangles three things that shouldn't be coupled. **Audience** (commits go wherever the codebase goes — personal agent notes don't belong in a team/public repo, and git history is forever). **Lifetime** (projects end; knowledge shouldn't die with them). **Scope** (you want an agent drawing on knowledge from outside the current project without writing into the project's history).

The decoupling already exists in the architecture: **residence vs. connection.** Knowledge needn't *live* where work happens — a connection block makes any instance discoverable from any project. So there are two shapes, and the right one depends on who the knowledge belongs to:

- **Personal agentsfs (recommended default).** One standalone instance outside any codebase (`~/agentsfs`), its own git history. Projects point at it via `afs connect`, and `afs setup` bundles create-or-reuse plus connect for the normal first-run path. Solves all three concerns at once; cross-project reuse and "promote knowledge out of a project" become ordinary gardening within one repo, not git surgery. This is deployment shape (b) from the source of truth, promoted to the recommended default.
- **Shared (merged).** Knowledge in a subdirectory of the repo, sharing its history — so it ships with the code via ordinary `git pull`. The one genuinely good reason to entangle: **team-shared memory.** A deliberate choice, never a silent default.

A third shape — a *nested* instance (its own git repo inside the host, gitignored from it) — was considered and **rejected** (2026-06-12): it takes on the friction of a second repo *and* nesting fragility while forfeiting the personal agentsfs's cross-project payoff, so it's worst-of-both. Anyone wanting personal-but-co-located memory is better served by the personal shape.

**`init` behavior (implemented):**
- `afs init <path>` is literal: it creates an agentsfs exactly where requested and does not connect projects or global configs.
- Shared memory always lives in a subdirectory (default `agentsfs/` when invoked at a repo root) — never at a code repo's root, where knowledge would mix with source.
- If the init target is inside a git repo, `init` refuses unless `--shared` is explicit. Merging is the irreversible option, and `--yes` is the flag agents pass reflexively, so `--yes` cannot choose shared by accident.
- `afs setup [path]` is the personal happy path: default to `~/agentsfs`, reject paths inside code repos, create or reuse the instance, then connect the current project.
- `afs connect <path>` is the explicit lower-level command for pointing a project or global harness config at an existing instance. `afs register` remains a deprecated compatibility alias for now.

## Decision queue

1. **Website onboarding & distribution** (owner is building the landing page in parallel): what the site links to, what users download, the agent-facing setup instructions, install channels, and the ordinary Git/GitHub backup story. Discuss after the core build lands.
2. Embedding provider default (env-based auto-detection ships in Layer 3; a blessed default + docs still need deciding).
3. **Hub scheduled agents (the loop's consumer)** — agreed direction 2026-07-06, design pending in [hub-execution-plan.md](hub-execution-plan.md): per-user cron on the Hub wakes the sprite, runs a named routine (gardener first — it empties the journal; research routines later), commits, pushes, sleeps. Prereq: per-user cost caps on the LLM proxy (metering already ships). Also agreed direction, design pending: **per-repo collaborators** (grant another account read/write on one repo — the Kauai use case; collaborator repos then clone into the grantee's sprite).

## Parking lot

Carried over from ideation: directory-level permissions / scoped checkout; merge handling beyond git's defaults. (**Native + web apps** and the **business model / managed sync** are no longer parked — they are now active work under [hub-execution-plan.md](hub-execution-plan.md) as of 2026-07-04.) Plus:

- **Eval pipeline** (deferred 2026-06-12, design preserved): scripted multi-session scenarios (insurance claim, recurring stock research) run by fresh-context agents against fixture instances; scoring = deterministic checks (descriptions present, links resolve, update-over-append density) + LLM-judge rubric with probe questions answerable only from earlier sessions' notes. Build it when prompts are stable enough to be worth regression-testing.
- **SPEC.md for tool builders** — a normative contract spec separate from the agent-facing root README. Premature until a second tool builder exists.
- **Contract versioning** — how an instance declares which contract version it follows. Matters once anything ships publicly.
- **Second-harness validation** — bring a non-Claude harness (e.g., Codex CLI) into demos as soon as practical; meanwhile, review prompts for Claude-isms.
- **Local embedding model** — semantic search with no API key at all.
- **Walker honors .gitignore** (deferred from code review 2026-06-12) — build artifacts inside an instance currently get treed/indexed/doctored; fix via `git ls-files --exclude-standard` when the instance is a repo.
- **A general `inbox/` — drop-anything-to-file queue** (2026-07-06): a human drops a PDF, email, or paste; the gardener files it, describes it, links it. Kept out of the journal deliberately — one concept per reserved name. Revisit once journal + scheduled gardener are proven.
- **`skills/` — procedural memory** (2026-07-06): a gardener behavior that notices patterns recurring across journal entries and promotes them into skill-shaped notes any harness can load. Depends on the journal producing raw material; design after the loop runs.

## Agreed log

- **2026-06-12 — Plan ratified.** Working model, build philosophy, architecture stance, four-layer map, and foundations agreed as written above.
- **2026-06-12 — Evals deferred.** Eval pipeline moved to the parking lot (design preserved there); validation per layer is a live demo with a fresh-context agent on a real instance.
- **2026-06-12 — Build mode: layer-by-layer, gated.** Claude builds each layer autonomously and demos at the gate before the next layer starts.
- **2026-06-12 — Build-through authorized.** Layers 2–4 built in one pass without pausing at gates; owner reviews the whole. Per-layer demos and the fixture are retained as Claude's own build-quality tools (owner: "if it helps you, keep it"), not ceremony owed to the owner.
- **2026-06-12 — Language: Go; CLI name: `afs`.** Single static binary for distribution (the brother test), pure-Go SQLite available for Layer 3, official MCP SDK for Layer 4. Layout: `cmd/afs/` (thin CLI) over `internal/core/` (the core library), per Go norms.
- **2026-06-12 — `afs register` added.** Point an existing project at an existing instance (the recurring personal-topology operation): writes the nearest AGENTS.md/CLAUDE.md, `--global` for harness configs, creates `./AGENTS.md` when a project has none. Refuses self-connection from inside an instance.
- **2026-06-12 — Topology decided and implemented.** Two shapes: personal agentsfs (recommended default) and shared (team memory, explicit). Nested was considered and rejected as worst-of-both. `init` asks only when inside a repo; `--yes` never silently merges. Implemented with regression tests (shared isolation, refuse-on-`--yes`, enclosing-repo detection). See the topology section above.
- **2026-06-13 — Setup vocabulary simplified.** `afs setup` becomes the friendly create-or-reuse-and-connect flow; `afs connect` replaces user-facing `afs register`; `afs init` becomes create-only and no longer has `--vault`, `--no-register`, or `--register-global`. Shared memory still requires explicit `--shared`.
- **2026-06-16 — Managed hosting removed from the product direction.** agentsfs stays simple: local files plus ordinary git. Backup and cross-device sync are handled by private GitHub/GitLab/self-hosted remotes, with agents guiding users through the minimum Git/GitHub setup questions instead of hiding a managed hosting layer. **(SUPERSEDED 2026-07-04 — see below.)**
- **2026-07-06 — Layer 5 (journal capture) agreed.** Session capture becomes a file convention, not a harness integration: agents append one session note per unit of work to a new reserved `journal/`; the gardener consumes entries into durable notes and deletes them (git history is the archive). Name settled as `journal/` over `inbox/` — session notes only; a drop-anything inbox is parked as its own concept. Delete-after-consolidation settled over an archive directory. Capture ships before the scheduled gardener because capture is perishable and curation is deferrable; the existing gardening prompt gains the consume step now, so manual gardening closes the loop until the Hub scheduler exists. Contract bumps 0.2.0 → 0.3.0.
- **2026-07-06 — The loop's division of labor agreed.** Continual learning splits edge/center: edge harnesses do the universal cheap thing (append a journal entry — any tool that can write a file complies); the center does the hard thing (scheduled consolidation on the Hub, later skill-promotion). Per-harness hooks/transcript integrations rejected as a supported surface. The hosted agent is the deterministic tier — its server holds full transcripts and can journal without relying on model compliance.
- **2026-07-08 — Layer 5a (reserved dirs by declaration) agreed.** Owner chose both options after the Obsidian-vault collision: collision-proof default names (`agent-journal/`, `agent-scratch/`) *and* `agentsfs_role:` INDEX frontmatter as the semantic truth all tooling resolves by. Dotfolder defaults (`.journal/`) rejected — hidden from Obsidian and humans, and dot-prefix already means machine-territory-no-knowledge. Upgrade gains a customized-contract guard (refuse without `--force` when AGENTS.md diverges from its version's stock text). Contract bumps 0.3.0 → 0.4.0. Prereq for the scheduled hosted gardener, which would otherwise automate the clobber-and-consume hazard.
- **2026-07-04 — Hosted storage layer reinstated.** Reverses the 2026-06-16 removal, which the owner confirmed was a ship-it expedient (sync would have delayed launch), not a principled objection. We build the **agentsfs Hub**: a "private GitHub for agent knowledge" — real bare git repos backed by Cloudflare R2, a central web space to introspect/download/clone, and a stable per-repo URL. Local-first is preserved: agents edit plain files with normal tools and `git push`; `git clone` stays the always-free, byte-complete exit ramp (Principle 6 by construction). Distinct from the retired Workers/Clerk/D1/GitHub-managed-repos prototype: no GitHub-in-the-middle, storage is user-ownable. Full plan and phased roadmap in [hub-execution-plan.md](hub-execution-plan.md); build starting at Phase 0 (real-git remote).
