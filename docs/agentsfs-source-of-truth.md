# agentsfs.ai — Source of Truth

Co-authored by the site owner and Claude during an ideation session on 2026-06-12. This document is the canonical, self-contained description of what agentsfs is, why it exists, and the decisions that constrain it. A fresh agent (or a fresh human) should be able to read this top to bottom and fully understand the project. Ideation is complete; the next phase is execution planning.

## TL;DR

**agentsfs is a portable, user-owned filesystem contract that gives AI agents durable, compounding memory — across every harness, on the user's terms.**

It is files + conventions + tools + instructions, and nothing else. An agentsfs instance is a plain git repo holding any file types — notes, PDFs, spreadsheets, images, code, datasets — with a knowledge layer written in markdown following a few simple conventions (frontmatter descriptions, wikilinks, a self-describing root). Markdown is the lingua franca that makes everything else discoverable, not a restriction on what the substrate holds. Around it sits a thin toolkit (CLI + MCP) providing search, navigation, and health checks, plus a pack of prompts that teach any agent — Claude Code, Codex, OpenClaw, anything — how to read, write, and maintain the substrate. The intelligence lives in the user's agents; agentsfs makes their knowledge survive and compound.

The analogy that anchors everything: **git doesn't write your commits; it makes committing so structured and cheap that you do it constantly.** agentsfs doesn't compound knowledge; it makes compounding the obvious, easy thing for whatever agent shows up.

## Problem statement

An agent session is a remarkable thing: in an hour it can research a company, untangle an insurance claim, or assemble a working model of a domain. Then the session ends — and almost everything it built has nowhere durable to live. The context, the dead ends ruled out, the judgment formed: the next session rebuilds it all from scratch. Models are already capable of compounding knowledge into genuine expertise; what's missing is a place for that knowledge to accumulate. And what does get saved today is trapped inside one vendor's proprietary memory.

Two kinds of people hit this wall:

- **Builders who want agents that compound.** Products like "continual deep research" (e.g., a daily stock-research agent that gets smarter about each company over time) are blocked not on model capability but on memory. Today the only path is hand-engineering a bespoke pipeline per product — powerful, but expensive and unreusable.
- **Everyday users who need continuity.** A non-technical person working through a long-running issue (an insurance claim, a project) must re-explain everything in every new conversation. The known workaround — "tell the agent to keep a file and read it next time" — works, but nothing in today's tools encourages, structures, or rewards that behavior.

The existing substrates each miss:

- **Vendor memory** (Claude memory, ChatGPT memory) is harness-locked and opaque. Claude only remembers what happened in Claude; the user can't see, own, edit, or move it.
- **The plain filesystem** is portable and human-legible, but has no agent conventions: nothing tells an agent where to write what it learned, how to find it again, or how to leave context that another tool — or the human — can pick up.
- **Human knowledge tools** (Obsidian, Notion, Dropbox) are app-first. They are actively adding agent integrations, but the agent surface is bolted onto a product whose center of gravity is their application and their account. None of them is a neutral, portable contract that any harness — or any user, local or hosted — can adopt as its own working substrate.

**The gap:** a user-owned, portable, file-based substrate shared by humans and agents — simple enough for both to understand, that agents are actively guided to read and write, and on which knowledge can accumulate across sessions, tools, and projects.

Scope decisions baked into this framing:

1. **Persistence + enablement of compounding.** The substrate makes synthesis into higher-level understanding possible and natural; the user's agent does the actual synthesizing (see Principle 1).
2. **Both audiences, one simplicity bar.** We design for the builder and the everyday user simultaneously. When in doubt, design for the non-technical user — the power user benefits from the same simplicity. (This is "the brother test," named for the owner's non-technical brother managing an insurance claim with an agent: if it works for him, it works for everyone.)
3. **Cross-harness portability is core, not incidental.** The payoff grows with the agent explosion: when a new tool, model, or harness appears, the user doesn't re-establish themselves — they point it at their agentsfs and it knows everything it's allowed to know.

## Principles

These were each explicitly agreed and constrain everything downstream.

### 1. No intelligence inside agentsfs

agentsfs contains no LLM, calls no LLM, and never will as a core dependency. It is files + conventions + tools + instructions. The user's own agent (Claude Code, Codex, OpenClaw, whatever) does all compounding, synthesis, and cleanup — agentsfs's job is to make that work obvious, structured, and cheap for any agent that shows up.

How the intelligence connects to the substrate:

- **Instructions shipped as product.** Out-of-the-box prompts and skills the user points their agent at ("read this to get started"), plus snippets that register agentsfs in a project's CLAUDE.md / AGENTS.md so the agent knows the substrate exists and how to use it.
- **Tools.** A CLI and MCP server exposing the same capabilities for reading, writing, and navigating the substrate.
- **Maintenance via the harness, not a daemon.** Consolidation, cleanup, and synthesis jobs run as scheduled jobs on the user's own harness (most support this), using prompts agentsfs provides. We call this recurring maintenance role "the gardener."

What this buys: no API keys, no inference cost, no model dependency, deterministic and testable behavior — and the system improves automatically as agents improve.

**Standing rule:** keep the system as simple as possible; build from first principles; actively fight the urge to overcomplicate.

### 2. Files are the source of truth

Plain files in ordinary directories are the canonical state. Any index, embedding store, cache, or derived structure must be fully rebuildable from the files alone. You can always zip an agentsfs, move it, inspect it with `ls` and a text editor, and lose nothing that matters.

### 3. Information-dense by design

The system must actively resist fragmentation into thousands of sparse files. Conventions, instructions, and maintenance prompts push toward fewer, denser, well-maintained files — updating and consolidating beats endlessly appending. A fragmented memory is as useless as no memory.

### 4. Cross-harness neutrality is the point

agentsfs belongs to the user, not to any harness or vendor. Claude Code, Codex, OpenClaw, a local script, and an agent on a Raspberry Pi should all be able to work against the same instance. Every new tool that appears is immediately useful because it inherits everything, instead of starting from zero.

### 5. Ride the training distribution

Agents are superhumanly fluent in markdown, YAML frontmatter, `[[wikilinks]]`, git, and grep-able plain text — they have seen millions of examples of each. Every novel format we invent is a tax on every agent forever; every boring convention we adopt is free fluency. Prefer existing, massively-trained-on conventions over invented ones, always. This is the tiebreaker whenever we're tempted to be clever.

### 6. Git is the backbone; the remote is pluggable

Every agentsfs instance is a local git repo — that alone provides edit logs, file history, line-level provenance ("which agent changed this, when"), "what changed since I was last here" (diff), and offline-first operation with no server or account. Remotes are deliberately decoupled: none (local-only), a self-hosted bare repo, GitHub/GitLab, or a future hosted service — all just git remotes, with push/pull as the sync protocol.

Consequences:

- **No lock-in by construction.** `git clone` is a permanent exit ramp for the user's entire substrate. Any hosted service competes on convenience (hosted search index, web viewer, auto-sync), never on captivity. This is the trust story that makes people willing to put their life context in this thing.
- **Large files** route through Git LFS via a `.gitattributes` shipped in the template (media extensions auto-tracked). Tools hide this entirely. Large media must not break the system, but knowledge — not media storage — is the product.
- **Non-technical users never see git.** Tools commit, sync, and resolve on their behalf.

## The system: contract and toolkit

The design splits cleanly along one axis. The **contract** is what works with zero tooling — just files, conventions, and git; it is never dependent on our software. The **toolkit** (CLI + MCP, same capabilities on both surfaces) makes the contract pleasant but is never load-bearing for truth.

### The contract (conventions, zero tooling required)

- **A git repo, any file types.** Per Principle 6. History, provenance, and sync come from git itself. The substrate holds anything — notes, PDFs, spreadsheets, images, audio, code, datasets. Markdown is the lingua franca of the *knowledge layer* (notes, descriptions, indexes), not a restriction on contents.
- **One-line `description:` for every file.** Agent-maintained. Powers progressive disclosure and forces the writing agent to articulate what the file is for. Files that can describe themselves do (frontmatter in markdown and other text formats); files that can't — PDFs, images, binaries — are described in their directory's `INDEX.md`, so the self-description invariant holds at the tree level, not the file-format level. (`description` was chosen over `summary` deliberately: a description says what the file is *for* — stable across edits — while a summary says what it *contains*, which churns on every edit and fights Principle 3. `description:` is also the dominant frontmatter convention in the wild, per Principle 5.) Directories carry one too (e.g., an `INDEX.md`), so disclosure works at every level: tree → folder descriptions → file descriptions → full file.
- **`[[Wikilinks]]` as the linking convention.** Entity pages (a note per company, person, or project) plus links give wiki-style "find all references" capability — directly serving the compounding-research use case. Mechanics: `[[Apple]]` is plain text; tools scan all files and resolve each link against file names, so the link graph is fully derived and rebuildable (Principle 2). Names are the identifiers — no UUIDs — because names are readable by humans and native to the training distribution. Duplicate names resolve Obsidian-style via shortest unique path suffix (e.g., `[[work/Apple]]`). Links can target any file type — `[[report.pdf]]` is as valid as `[[Apple]]`. Because links are name-based rather than path-based, reorganizing the tree never breaks them.
- **Source provenance as a writing convention.** Frontmatter `sources:` and inline citations record which URL, email, or document a claim came from. (Mechanical provenance — who wrote what, when — is git's job.)
- **Self-describing root.** A root-level README / AGENTS.md teaches the contract to any agent dropped in with zero prior instructions. The filesystem documents itself: unzip the folder, point any agent at it, it works.
- **Freshness comes from git, not frontmatter.** No required `created:`/`updated:` fields — self-reported dates go stale the moment an agent forgets to bump one, while git timestamps are involuntary and always true. The toolkit surfaces git dates in tree and disclosure views ("last touched 3 weeks ago"). An optional `verified:` field exists as a convention for research-heavy domains where "I confirmed this fact on date X" is itself knowledge. Frontmatter stays minimal: `description:` required, everything else optional.

### The toolkit (CLI + MCP)

- **`tree` with progressive disclosure** — the directory tree with each entry's one-line description and last-touched date; the agent chooses what to read fully instead of loading everything.
- **Search** — full-text and semantic (embedding-based), over the whole substrate, exposed as agent tools. The one capability that genuinely can't be contract-only (embeddings need an index); all indexes derived and rebuildable per Principle 2.
- **Backlinks / references** — "find all references to `[[X]]`," from the derived link index.
- **`rename` (link-aware refactor)** — renaming a file rewrites every `[[link]]` to it across the vault in one deterministic pass: the LSP "rename symbol" refactor applied to knowledge. Renames done outside the tool produce dead links, which `doctor` catches.
- **`doctor`** — deterministic (no LLM) health checker that flags orphan files, missing descriptions, dead or ambiguous links, stale stubs, and duplicate-looking files. This gives Principle 3 teeth: doctor's output is the worklist the gardener consumes. (Named `doctor` over `linter`: established CLI idiom for whole-installation health — `brew doctor`, `npm doctor` — and friendlier to non-technical users; lint rules live inside it.)
- **Prompts and skills** — the onboarding and gardening prompt pack, plus CLAUDE.md / AGENTS.md registration snippets.

## Directory shape

**Decision: prescribe the meta-structure, not the taxonomy.** The contract's promise is not "the tree looks like X" — it is "**the tree always explains itself.**"

The contract mandates only:

1. **The self-description invariant.** Every directory carries an index/description; every file is described — by its own frontmatter where the format allows, or by its directory's `INDEX.md` where it doesn't (PDFs, images, binaries). This is the *substitute* for a prescribed taxonomy: any shape is cheap to discover via progressive disclosure, so no shape needs to be memorized. Fixed taxonomies were rejected because domains differ too much (a stock-research instance wants entity pages per company; an insurance claim wants a timeline and correspondence) — prescribed buckets become junk drawers, which fights Principle 3.
2. **Reserved names with fixed meaning:**
   - Root `README` / `AGENTS.md` — the contract bootstrap (the self-describing root).
   - `.agentsfs/` — derived indexes and config. Machine territory; never holds knowledge.
   - `scratch/` — explicitly ephemeral; exempt from density rules and doctor strictness. Reserved because "this is disposable" is the one thing a plain filesystem cannot express, and agents need a place where mess is legal.
3. **Everything else is the agent's garden.** Structure emerges from the domain. The gardener (scheduled maintenance job) keeps it healthy: doctor flags disorder, the agent reorganizes, git makes reorganization safe, and name-based wikilinks mean reorganization never breaks references.

Prescription lives in the prompts, not the contract: the onboarding instructions *propose* a starter structure (e.g., PARA-inspired) framed as "consider using this, or adapt to whatever structure already exists or whatever structure you maintain," and the gardening prompt explicitly licenses the agent to rethink directory structure as the domain evolves. We ship a default starter template to solve the blank page for non-technical users, but it is a suggestion the agent may outgrow, and doctor never enforces it.

This mirrors git's own posture: git prescribes `.git/` and nothing else; ecosystems layer templates on top.

A consciously accepted trade: with no fixed taxonomy, shipped prompts can't say "write memories to `/memory`" — agents must orient via the tree and exercise judgment about where things go. Accepted because the gardener cleans up misfiles asynchronously, agents keep improving at exactly this judgment, and the alternative — a junk-drawer taxonomy — fails silently and permanently rather than visibly and recoverably.

## Key requirements

1. **Search as a packaged goodie.** Full-text and semantic search available to any agent as tools. Today an agent pointed at a folder can only grep; agentsfs ships proper retrieval out of the box.
2. **Multiple deployment shapes, one contract.** The same contract works as: (a) a local per-project instance; (b) a single personal root used across all projects (vault-style "monorepo for personal knowledge"); (c) a synced instance shared by agents on multiple machines (MacBook, Raspberry Pi, phone) via any git remote; (d) self-hosted or hosted sync for those who want it. Because structure is emergent, a standalone project instance and a project folder inside a personal vault are the same thing at different mount points — moving one into the other is `git mv` plus a gardener pass, not a migration.
3. **Onboarding instructions shipped as product.** Prompts, skills, and CLAUDE.md / AGENTS.md snippets that teach any agent the contract.

## How a session works (illustrative walkthrough)

1. **Setup (once):** the user runs `agentsfs init` (or clones an existing instance). They get a git repo with a self-describing root README, a starter structure proposal, `.gitattributes` for LFS, and registration snippets for their agents' CLAUDE.md / AGENTS.md.
2. **An agent arrives:** any harness, any model. Its instructions (or the root README itself) tell it: run `tree` to orient — it sees the structure with one-line descriptions and freshness dates, and reads only what's relevant.
3. **Work happens:** the agent searches (full-text or semantic), follows `[[wikilinks]]`, reads entity pages, and does its actual job. As it learns things worth keeping, it writes or — preferably — *updates* dense notes, with descriptions and source citations, linking entities as it goes. Tools commit to git automatically.
4. **The gardener runs (scheduled, on the user's harness):** it runs `doctor`, gets a worklist (orphans, dead links, missing descriptions, fragmentation), and consolidates — merging sparse notes, updating descriptions, restructuring directories if the domain has outgrown them. Git makes every change reviewable and reversible.
5. **Next session, any tool, any machine:** the knowledge is there, denser and better organized than last time. A brand-new agent installed tomorrow starts with everything.

## Parking lot

Real ideas, deliberately deferred so they don't complicate the core. Revisit after the core contract is built and proven.

- **Directory-level permissions / scoped checkout.** Give an agent access to only part of the tree ("work" vs. "personal"). Maps naturally onto git sparse checkout. Likely important for the multi-agent future; risk of overcomplicating v1.
- **Native + web apps.** Mac/iPhone/Android/web apps for humans to browse and edit their agentsfs. Powerful, but the substrate must be valuable with zero custom UI first — any editor works; that's the point of files.
- **Business model.** Open-source core + paid hosted sync (the Obsidian/Dropbox model). Plausible default; decide after the contract exists.
- **Multi-machine merge conflicts beyond git's defaults.** Git merge covers v1; anything fancier waits for real-world pain.

## Status and next step

Ideation phase complete (2026-06-12). The next session should produce an execution plan. The candidate first slice, to be debated then: `init` (template + self-describing root + git) + the onboarding prompt + `tree` with descriptions — proven end-to-end by a real agent on a real task (the insurance-claim use case is a ready-made test) *before* any search infrastructure is built.
