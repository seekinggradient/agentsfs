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

## Parking lot

Carried over from ideation: directory-level permissions / scoped checkout; merge handling beyond git's defaults. (**Native + web apps** and the **business model / managed sync** are no longer parked — they are now active work under [hub-execution-plan.md](hub-execution-plan.md) as of 2026-07-04.) Plus:

- **Eval pipeline** (deferred 2026-06-12, design preserved): scripted multi-session scenarios (insurance claim, recurring stock research) run by fresh-context agents against fixture instances; scoring = deterministic checks (descriptions present, links resolve, update-over-append density) + LLM-judge rubric with probe questions answerable only from earlier sessions' notes. Build it when prompts are stable enough to be worth regression-testing.
- **SPEC.md for tool builders** — a normative contract spec separate from the agent-facing root README. Premature until a second tool builder exists.
- **Contract versioning** — how an instance declares which contract version it follows. Matters once anything ships publicly.
- **Second-harness validation** — bring a non-Claude harness (e.g., Codex CLI) into demos as soon as practical; meanwhile, review prompts for Claude-isms.
- **Local embedding model** — semantic search with no API key at all.
- **Walker honors .gitignore** (deferred from code review 2026-06-12) — build artifacts inside an instance currently get treed/indexed/doctored; fix via `git ls-files --exclude-standard` when the instance is a repo.

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
- **2026-07-04 — Hosted storage layer reinstated.** Reverses the 2026-06-16 removal, which the owner confirmed was a ship-it expedient (sync would have delayed launch), not a principled objection. We build the **agentsfs Hub**: a "private GitHub for agent knowledge" — real bare git repos backed by Cloudflare R2, a central web space to introspect/download/clone, and a stable per-repo URL. Local-first is preserved: agents edit plain files with normal tools and `git push`; `git clone` stays the always-free, byte-complete exit ramp (Principle 6 by construction). Distinct from the retired Workers/Clerk/D1/GitHub-managed-repos prototype: no GitHub-in-the-middle, storage is user-ownable. Full plan and phased roadmap in [hub-execution-plan.md](hub-execution-plan.md); build starting at Phase 0 (real-git remote).
