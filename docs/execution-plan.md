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
- **The core-library skeleton and a CLI with one command: `init`.**
  - Lays down the template, runs `git init`, makes the first commit.
  - **Harness detection and registration.** Looks for existing `AGENTS.md` / `CLAUDE.md` (asks which harness when ambiguous or absent) and appends a marker-fenced registration block in the canonical file — with user approval. Load-bearing, not a nicety: harnesses bootstrap by reading these files; if the pointer isn't there, no agent ever learns the substrate exists. Markers make the block idempotent to update and clean to remove.
  - Two registration targets, same mechanism: the instance's own root (automatic — the self-describing root *is* the registration) and external places (another project's `AGENTS.md`, or the harness's global config).
- **One hand-built fixture**: a small reference instance in a known state, used as test data for everything that follows.

**Deliberately not built:** every other command; evals; MCP.

**Gate 1:** live demo — in a clean directory, `init` produces a working registered instance; a fresh-context agent session orients from the root unaided, does a real task, and writes notes that honor the conventions. Owner reviews the session and the resulting files.

### Layer 2 — orientation and health: the deterministic, index-free commands

**Build:** `tree` (descriptions + git freshness), `doctor` (orphans, missing descriptions, dead/ambiguous links, fragmentation flags), `backlinks`, `rename`. The boundary that makes this one layer: every command is a deterministic read or rewrite of files + git with **no persistent index** — link scans run on the fly at v1 scale. The gardening prompt v0 lands here too: doctor's output is the gardener's worklist, so they ship as a pair.

**Deliberately not built:** search, MCP, any persistent index.

**Gate 2:** live demo — a gardener session on a deliberately messy instance consumes doctor's worklist and leaves it measurably denser and healthier, reviewable in git.

### Layer 3 — search: the first derived state

**Build:** full-text first — SQLite FTS5 in the `.agentsfs/` sidecar; `reindex` rebuilds from zero and must reproduce the index exactly (Principle 2, enforced by code). Then semantic: pluggable embedding provider (off-the-shelf API with user key to start; a local-model provider can join later), vectors in the same SQLite file, brute-force cosine at personal-vault scale until measurement says otherwise.

**Gate 3:** live demo — queries that grep/backlinks handled poorly on the real instances succeed via search.

### Layer 4 — surfaces: MCP and packaging

**Build:** the MCP server as a thin adapter over the same core library (no logic written twice); distribution/packaging; registration snippets for more harnesses. MCP is deferred to here deliberately: CLI-fluent agents shell out natively; MCP earns its place when a harness that can't shows up.

**Gate 4:** live demo — a harness that cannot use the CLI works against a real instance via MCP.

## Foundations: repo and working setup — AGREED (2026-06-12)

- This directory becomes a git repo now; single project repo: `docs/`, `template/`, `prompts/`, `fixtures/` (hand-built reference instances — fixed, known test data), and `core/` / `cli/` / `mcp/` per the language's packaging norms.
- Template, prompts, skills, and fixtures change as one unit: a contract change is not done until all four agree. (`skills/` added 2026-06-12: Claude-native packaging of the prompt pack — setup, remember, garden — thin wrappers that defer to each instance's AGENTS.md as the contract.)
- Default CLI name: `afs` (short, typeable by agents and humans alike); `agentsfs` as an alias if packaging allows.

## Topology: where an instance lives relative to a codebase — AGREED (2026-06-12)

The question that forced this: if `afs init` runs inside a git repo, knowledge and code share a git history — which entangles three things that shouldn't be coupled. **Audience** (commits go wherever the codebase goes — personal agent notes don't belong in a team/public repo, and git history is forever). **Lifetime** (projects end; knowledge shouldn't die with them). **Scope** (you want an agent drawing on knowledge from outside the current project without writing into the project's history).

The decoupling already exists in the architecture: **residence vs. registration.** Knowledge needn't *live* where work happens — a registration block makes any instance discoverable from any project. So there are three shapes, and the right one depends on who the knowledge belongs to:

- **Vault (recommended default for personal use).** One standalone instance outside any codebase (`~/agentsfs`), its own git history. Projects point at it via `afs register`. Solves all three concerns at once; cross-project reuse and "promote knowledge out of a project" become ordinary gardening within one repo, not git surgery. This is deployment shape (b) from the source of truth, promoted to the recommended default.
- **Shared (merged).** Knowledge in a subdirectory of the repo, sharing its history — so it ships with the code via ordinary `git pull`. The one genuinely good reason to entangle: **team-shared memory.** A deliberate choice, never a silent default.
- **Nested.** Own git repo nested in the host, gitignored from it. The narrow case: a solo dev who wants git-tracked memory co-located with a repo they'll share, and doesn't need cross-project reuse. Demoted to an advanced `--nested` flag — it takes on the friction of a second repo *and* nesting fragility while forfeiting the vault's payoff, so it's rarely right.

**`init` behavior (implemented):**
- File location is always a subdirectory in shared/nested — never a code repo's root, where knowledge would mix with source. Default subdir is `memory/`.
- The ownership choice is made **only when the init target is inside a git repo** (one `EnclosingRepoRoot` check covers both "the dir is a repo" and "an ancestor is a repo"). Outside any repo, `init` silently creates a standalone instance (the vault case) — there's no codebase to entangle with, so no question.
- Inside a repo: interactive prompt (Vault / Shared / Nested, default Vault), or pick non-interactively with `--vault` / `--shared` / `--nested`.
- **`--yes` never picks Shared.** Merging is the irreversible option, and `--yes` is the flag agents pass reflexively — so inside a repo with no shape flag, `--yes` (or any non-interactive run) *refuses* with guidance rather than guessing. This mirrors the rule that `--yes` never writes global harness configs.

## Decision queue

1. **Website onboarding & distribution** (owner is building the landing page in parallel): what the site links to, what users download, the agent-facing setup instructions, install channels. Discuss after the core build lands.
2. Embedding provider default (env-based auto-detection ships in Layer 3; a blessed default + docs still need deciding).
3. **Init-inside-repo prompt wording** — the three-option text is the entire UX of the topology decision; refine the user-facing copy when convenient.

## Parking lot

Carried over from ideation: directory-level permissions / scoped checkout; native + web apps; business model; merge handling beyond git's defaults. Plus:

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
- **2026-06-12 — `afs register` added.** Point an existing project at an existing instance (the recurring vault-topology operation): writes the nearest AGENTS.md/CLAUDE.md, `--global` for harness configs, creates `./AGENTS.md` when a project has none. Refuses self-registration from inside an instance.
- **2026-06-12 — Topology decided and implemented.** Vault is the recommended default; shared (team memory) and nested (advanced) are explicit choices. `init` asks only when inside a repo; `--yes` never silently merges. Implemented with regression tests (shared/nested isolation, symlink-safe gitignore, refuse-on-`--yes`). See the topology section above.
