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
- Template, prompts, and fixtures change as one unit: a contract change is not done until all three agree.
- Default CLI name: `afs` (short, typeable by agents and humans alike); `agentsfs` as an alias if packaging allows.

## Decision queue

1. (At Layer 3) embedding provider choice
2. (At Layer 4) packaging/distribution channels

## Parking lot

Carried over from ideation: directory-level permissions / scoped checkout; native + web apps; business model; merge handling beyond git's defaults. Plus:

- **Eval pipeline** (deferred 2026-06-12, design preserved): scripted multi-session scenarios (insurance claim, recurring stock research) run by fresh-context agents against fixture instances; scoring = deterministic checks (descriptions present, links resolve, update-over-append density) + LLM-judge rubric with probe questions answerable only from earlier sessions' notes. Build it when prompts are stable enough to be worth regression-testing.
- **SPEC.md for tool builders** — a normative contract spec separate from the agent-facing root README. Premature until a second tool builder exists.
- **Contract versioning** — how an instance declares which contract version it follows. Matters once anything ships publicly.
- **Second-harness validation** — bring a non-Claude harness (e.g., Codex CLI) into demos as soon as practical; meanwhile, review prompts for Claude-isms.
- **Local embedding model** — semantic search with no API key at all.

## Agreed log

- **2026-06-12 — Plan ratified.** Working model, build philosophy, architecture stance, four-layer map, and foundations agreed as written above.
- **2026-06-12 — Evals deferred.** Eval pipeline moved to the parking lot (design preserved there); validation per layer is a live demo with a fresh-context agent on a real instance.
- **2026-06-12 — Build mode: layer-by-layer, gated.** Claude builds each layer autonomously and demos at the gate before the next layer starts.
- **2026-06-12 — Language: Go; CLI name: `afs`.** Single static binary for distribution (the brother test), pure-Go SQLite available for Layer 3, official MCP SDK for Layer 4. Layout: `cmd/afs/` (thin CLI) over `internal/core/` (the core library), per Go norms.
