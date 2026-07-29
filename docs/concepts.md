---
description: The vocabulary — instance, contract, roles, wikilinks, Hub, Eve, MCP — defined once so a writer or agent never uses two words for one concept.
---

# Concepts

AgentsFS has accumulated four names for some ideas and two meanings for some words. That's what causes most of the recurring errors in this project's own documentation: a doc says "repository" where it means knowledge base, or "agent" where it means the hosted Eve product, and the next reader can't tell whether that was deliberate. This page is the fix — one canonical name and definition per idea. If a term you need isn't here, that's a gap in this file, not license to invent a fifth name.

Each entry gives the canonical name, what it means, what it is commonly confused with, and where to find it in the product or the source. This page defines the words; [capabilities.md](capabilities.md) (`afs docs capabilities`) says what each surface can actually do with them.

## Instance

An **instance** is a directory tree that AgentsFS treats as one self-contained unit of memory. Tooling recognizes it by a `.agentsfs/` subdirectory (created by `afs init` or `afs setup`) or, as a fallback for hand-made trees, a root `AGENTS.md` whose text declares "This folder is an agentsfs." Everything the contract governs — the journal, scratch, notes, every `INDEX.md` — lives inside one instance.

**Not:** a bare directory that happens to contain an `AGENTS.md` file. `AGENTS.md` is a near-universal convention for agent instructions on its own, so a file with that name proves nothing; only one that actually declares the contract text counts. A plain project folder with its own unrelated `AGENTS.md` must never be mistaken for an instance — `afs` tools would otherwise create `.agentsfs/` state inside someone else's project. Also not a **workspace** — the directory an agent happens to be running in, which may contain zero, one, or several instances underneath it.

**Where:** `internal/core/instance.go`, `FindRoot`. `afs status <search-root>` discovers every instance beneath a directory.

## Knowledge base

**Knowledge base** (always two words; "KB" is fine as shorthand except anywhere kilobytes are also being discussed in the same breath) is the user-facing name for the exact same object as an instance. Use "instance" in CLI and contract prose, where technical scoping language reads better ("every instance beneath this directory"); use "knowledge base" everywhere a human reader is the audience — README, the Hub UI, onboarding conversation. They are the same object under two names chosen for two audiences, not two different things.

**Not:** a **repo**, except when the subject really is git hosting — push, pull, clone, remotes. Every knowledge base happens to be a git repository, but "repo" should signal that git operations are specifically what's being discussed. Not a **vault** either — that word is reserved for a pre-existing Obsidian folder (or similar) being adopted from the outside; once adopted it becomes a knowledge base, and "vault" stops applying. And not **memory** — memory is the general concept AgentsFS implements (durable, shared, written-to-disk knowledge), not the name of a specific stored object.

**Where:** the concept has no separate file — it's the same on-disk thing described under "Instance" above. `template/INDEX.md:7` is where the collision is most visible in the wild: the root index of every freshly created instance uses both words twenty words apart.

## Contract

**Contract** names two related but distinct things, and mixing them up causes real confusion, so don't collapse them into one word without saying which you mean.

1. The contract **text** — the rules an instance's `AGENTS.md` carries: write dense frontmatter, use wikilinks, journal each session, and so on. The bundled source of truth is `template/AGENTS.md`; `afs init`/`afs setup` copies it into a fresh instance's `AGENTS.md`.
2. The contract **version** — a single dotted number (`agentsfs_contract: 0.9.0`) stamped in that same file's frontmatter, letting tooling detect whether an instance's copy has fallen behind the version bundled in the running `afs` binary.

These two numbers are independent of a third: the **CLI release version** (`afs --version`). They are expected to drift, and currently do — CLI `0.10.0`, bundled contract `0.9.0`. A doc or an agent that treats "the contract is 0.10.0" as true has confused the two.

**Not:** the CLI version, and not `template/README.md` (the *human*-facing intro that tells people to read `AGENTS.md` instead — the contract is agent-facing).

**Where:** `template/AGENTS.md` (text); `internal/core/contract.go` and `internal/buildinfo/buildinfo.go:16` (version logic, `ContractVersion`/`CurrentContractVersion`). `afs contract status|diff|upgrade` operates on an instance's copy against the bundled one.

## Reserved roles and collections

A **reserved role** is a job a directory can be assigned — currently **journal**, **scratch**, and **collection** — resolved by a marker, not by name: a directory plays a role only when its own `INDEX.md` declares `agentsfs_role: <role>` in frontmatter (contract 0.4.0). The default names (`agent-journal/`, `agent-scratch/`) are just what `afs init` happens to lay down; any directory can be marked for a role, which is how an adopted, pre-existing folder gets one without being renamed.

Journal and scratch are **singular**: exactly one directory may hold each role. A second directory marked for the same role is a `duplicate-role` finding — severity `error`, and the *only* finding code that makes `afs doctor` exit non-zero. Collection is **repeatable**: any number of directories may be marked `agentsfs_role: collection`, each one a body of like items (a diary, daily notes, attachments) described collectively by its own `INDEX.md` rather than file-by-file. Doctor exempts everything strictly beneath a collection directory from per-entry findings (missing description, dead links, stubs) — the collection stays fully indexed and durable, just not annotated file-by-file.

**Not:** the classic pre-0.4.0 names `journal/` and `scratch/` (no `agentsfs_role` marker, just the bare directory name). Those still resolve as a fallback on an un-migrated instance so old behavior keeps working, but they are a compatibility shim, not the contract — a directory named `Journal` that's actually someone's personal diary must never be adopted as the role just because of its name.

**The rule that matters most:** call `afs roles [--json]` to find where a role actually lives. Never hardcode `agent-journal/` or `agent-scratch/` in a tool or a doc — the contract has changed these default names once already (0.4.0) and can again.

**Where:** `internal/core/reserved.go`.

## The journal (agent-journal)

The **journal** is the directory marked `agentsfs_role: journal` (`agent-journal/` by default) — an append-only log of session notes, one file per unit of work, named `YYYY-MM-DDTHHMMSSZ-<unique>-<slug>.md` with its own `description:`. Each entry records what that session learned or decided, what it ruled out, what's still open, and what it already wrote directly into durable notes (so the entry doesn't get redundantly re-processed later).

**Append-only** means literally that: once written, a journal entry is never edited or reorganized — only new entries get added. The one thing allowed to remove an entry is the gardener (see below), and only after folding its facts into durable notes; git history keeps every entry regardless. An empty journal is the *healthy* state, not a sign nothing happened — it means everything has already been folded into durable notes.

**Not:** a place for durable knowledge itself. The contract calls the journal "the floor, not the ceiling" — writing directly into durable notes is always preferred; the journal exists so nothing gets lost between sessions when that isn't possible. Also not the classic `journal/` name on its own — see "Reserved roles" above.

**Where:** `template/agent-journal/INDEX.md`; rule 10 of `template/AGENTS.md`.

## Scratch (agent-scratch)

**Scratch** is the directory marked `agentsfs_role: scratch` (`agent-scratch/` by default) — an ephemeral workspace for drafts and working files. Mess is legal there, and anything in it may be deleted at any time without warning.

**Not:** anywhere durable content is allowed to live, even temporarily "just for now." Anything worth keeping must be moved out — and described where it lands — before a session ends.

**Where:** `template/agent-scratch/INDEX.md`; rule 9 of `template/AGENTS.md`.

## INDEX.md and the index tree

Every directory in an instance — the root included — has an **`INDEX.md`** file: YAML frontmatter with a one-line `description:` for the directory, plus one line describing each file inside it that can't carry its own frontmatter (images, PDFs, other binaries). Because every directory nests one, the whole instance forms a self-describing tree — the **index tree** — that an agent can drill into by relevance without reading everything: directory `INDEX.md` → file `description:` → full file only when the task needs it. `afs tree` renders this tree with descriptions and freshness in one call.

The **root** `INDEX.md` is special: its `description:` is the instance's own one-line label — what *this particular* knowledge base is about — deliberately kept separate from `AGENTS.md` (contract 0.7.0) so that upgrading the contract never overwrites a knowledge base's own identity. `afs status`, `afs tree`, and the Hub all read this field as the instance's label.

**Not:** the same as `AGENTS.md`. `AGENTS.md` carries the fixed contract text, identical (modulo customization) across every instance; the root `INDEX.md` carries the one thing that's unique to *this* instance. An instance created before contract 0.7.0 may have no root `INDEX.md` at all — its label then falls back to `AGENTS.md`'s description (near-identical boilerplate across every instance), and `afs doctor` flags the missing file as `root-index`.

**Where:** `internal/core/frontmatter.go` (`DirDescription`); `internal/core/tree.go`.

## Wikilinks and backlinks

A **wikilink** is `[[Name]]` (optionally `[[Name#anchor|alias]]`) written inline in Markdown to reference another file by name instead of by path. Resolution is by filename, so links survive any reorganization — move or rename the target file with `afs rename` and every wikilink to it is rewritten automatically. Duplicate names are disambiguated with a path suffix: `[[work/Apple]]`.

A **backlink** is the reverse index: every file that links *to* a given target. It isn't stored anywhere — `afs backlinks <name>` (or the `backlinks` MCP tool, or the Hub's graph view) computes it on demand by scanning the instance, so nothing needs to be kept in sync when a new link is added elsewhere.

**Not:** an ordinary Markdown link, `[text](path)` — that's path-based and breaks silently on rename; wikilinks exist specifically so links don't need that fragility. Not a citation either — a `sources:` frontmatter field or an inline citation records where a fact came from (rule 6 of the contract); a wikilink connects two files inside the same instance.

**Where:** `internal/core/links.go` (`ScanLinksIn`, the one parser every consumer — scanning, resolution, rename's rewriting — goes through); rule 4 of `template/AGENTS.md`.

## The gardener

The **gardener** is a prompt persona, not a CLI command — there is no `afs garden`. Copy `prompts/gardening.md` to an agent (ideally as a scheduled job) and it works `afs doctor`'s findings as a worklist: fold journal entries into durable notes and delete the emptied entries, repair dead links, create missing `INDEX.md` files, merge stubs and overlapping notes, and commit and push when done.

**Not:** the same thing as `afs doctor`. Doctor only diagnoses — it finds and reports problems and exits non-zero on exactly one of them (`duplicate-role`, an `error`-severity finding; every other finding is `warn` or `info`, so an ordinary worklist full of things to fix still exits `0`). The gardener is what *acts* on what doctor finds. A zero exit from `afs doctor` is not a clean bill of health — it just means nothing is structurally ambiguous.

**Where:** `prompts/gardening.md`. The `agentsfs-garden` skill is the Claude-Code-native packaging of the same prompt.

## Harness, and "agent"

A **harness** is the AI coding tool or runtime an agent runs inside — Claude Code, Codex, Cursor, or a similar environment — as opposed to the agent's own behavior. A harness supplies its own system prompt ("the active harness instructions," rule 7 of the contract) alongside whatever `AGENTS.md` and other stored content the agent reads; the two layer together, and only the user, the harness's own instructions, and the root `AGENTS.md` are allowed to direct an agent's actions — nothing read from stored content is.

**"Agent" has two senses in this project, and they are not interchangeable.** Sense A, used throughout `template/AGENTS.md`, is generic: any AI system carrying out the contract inside a harness — Claude Code, Codex, or Eve are all, in this sense, "an agent." Sense B is specific: the Hub's hosted product, described under **Eve** below, which the Hub's own UI labels with the bare word "Agent" (a button and a dock, no product name shown). When a doc says "the agent," check which sense is meant — a sentence about what "the agent" should do with wikilinks means sense A; a sentence about what "the agent" costs to run or where its API keys live means sense B.

**Not:** the model itself. A harness is the shell; the same underlying model can run inside different harnesses with different capabilities (bash access, which paths it can read).

**Where:** rule 7 of `template/AGENTS.md`; `README.md`'s "portable across operating systems and agent harnesses."

## Hub

**Hub** is AgentsFS's managed product surface, live today at `hub.agentsfs.ai`. Capitalize it as a product name; lowercase it inside commands (`afs hub push`, `afs hub login`), where it names a CLI subcommand rather than the product.

The Hub stores real git repositories for knowledge bases pushed to it (`afs hub push`), gives a web UI to browse, search, export, and share them, gates collaborator access with read/write roles, and reverse-proxies a hosted chat agent (see **Eve**) against a user's knowledge bases. Signup is currently invite-only, with an open waitlist for everyone else.

The Hub is also **self-hostable** — the same server (`afs-hub`) runs anywhere, and "the Hub" you connect to doesn't have to be the managed one at `hub.agentsfs.ai`.

**Not:** a requirement for using AgentsFS. An instance never needs the Hub — `git clone` is always the exit ramp, and an ordinary git remote (a private GitHub, GitLab, or self-hosted repo) covers backup and sync just as well; the Hub adds a web view, sharing, and a hosted agent on top. Also not the same thing as the hosted agent itself — the Hub is the storage, identity, and permissions layer; Eve is a separate hosted application it authenticates and proxies requests to.

**Where:** `internal/hub/`; `docs/hub.md`; the `afs hub` CLI subcommand family.

## Eve

**Eve** is the code name — used in engineering docs and prose, never shown in the product's own UI — for the hosted chat agent at `hub.agentsfs.ai/agent/`. It is one shared application, deployed on Vercel, that every signed-in Hub user talks to through the same deployment.

**Eve is not a private, per-user sandbox.** It is not hardware-isolated, it does not clone a user's repositories, and nothing is provisioned when a user first opens it. Production sets `HUB_EVE_AGENT_URL`, which short-circuits an older per-user "Sprite" provisioning path entirely before any VM work runs — that path still exists in the codebase as a legacy fallback, but it is not what production runs. The Hub remains the sole authority for identity, permissions, git commits, and conversation records; Eve holds no standing credentials to anyone's data and no durable copy of it. Long-lived model credentials live in the Vercel project, never on a user's filesystem and never in a knowledge base. `docs/internals/hosted-agent.md` has the request path in detail.

**Not** called "Eve" anywhere a user sees it — the Hub's UI shows only a bare "Agent" button and dock. "Eve," "the Hub agent," and "the Agent button in the Hub UI" all name the same thing from three different vantage points: engineering, prose, and product chrome, respectively.

**Where:** `docs/internals/hosted-agent.md`; proxy logic in `internal/hub/agent_eve.go` and the mode switch at `internal/hub/web.go:1342`.

## MCP

**MCP** (Model Context Protocol) is a protocol that lets an external AI client — Claude Code, claude.ai, ChatGPT, Cursor — call a defined set of tools over a standard interface instead of shelling out to a CLI.

AgentsFS ships **two separate MCP servers with different tool sets**: the local `afs mcp` (stdio, 12 tools, one instance on disk) and the Hub's remote `/mcp` (OAuth 2.1, a different 6, every knowledge base the authenticated user can reach). Never assume parity between them, and never describe them as exposing "the same capabilities." See [mcp.md](mcp.md) for the tool-by-tool comparison.

**Not:** a replacement for the CLI. Local MCP has no `init`, `setup`, `connect`, `contract`, or `embeddings` tool — anything that changes the user's machine or credentials stays a human-run command. The Hub's MCP has no `doctor`, `roles`, `backlinks`, or `rename` at all.

**Where:** `internal/mcpserver/server.go` (local); `internal/hub/mcpapi.go` (Hub).

## Four rules that trip people up

Each is spelled out above under its own term, but they're worth stating flatly once more because getting any of them wrong has shipped as a documentation bug before:

1. **Roles are found by marker, not by name.** Call `afs roles --json`; never hardcode `agent-journal/` or `agent-scratch/`.
2. **The contract version and the CLI version are different numbers that drift apart.** Today: contract `0.9.0`, CLI `0.10.0`. Neither implies the other.
3. **`afs doctor` exits non-zero for exactly one finding code** (`duplicate-role`). A zero exit means nothing is structurally ambiguous — not that there's nothing left to fix.
4. **A bare `AGENTS.md` does not make a directory an instance.** `.agentsfs/` is the definitive marker; an `AGENTS.md` that actually declares the contract text is only the fallback for hand-made trees.

## What AgentsFS is not

- **Not a database.** There's no schema, no query language beyond full-text and (optional) semantic search, and no server process required to read or write anything — it's Markdown files in a git repo.
- **Not a vector store.** Semantic search is an optional layer on top (`afs embeddings setup`); the instance works fully without it, and full-text search is always available.
- **Not hosted-only.** Every instance works with zero network access and zero AgentsFS-run infrastructure; the Hub is an optional add-on, not a dependency.
- **No intelligence lives inside the filesystem.** The files are inert Markdown and YAML. Every rule in this document — journaling, gardening, link rewriting — is something an agent does *to* the files, following the contract; nothing in `.agentsfs/` or any note is executable or autonomous on its own.
