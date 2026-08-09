---
description: The vendored Markdown To agent skill — where it lives, how an agent working against the Hub receives it, what it can and cannot do without the mdto CLI, and how to re-vendor it.
---

# The bundled Markdown To skill

Status: implemented. Verified against `skills/markdownto/`, `embed.go`,
`internal/skills/skills.go`, `internal/docs/docs.go`, `internal/hub/mcpapi.go`,
and `internal/mcpserver/server.go` on 2026-08-09.

`skills/markdownto/` is a **verbatim copy** of `skills/markdownto/` from the
markdownto repository: a `SKILL.md` in the Agent Skills format plus four
byte-normative `examples/`. It is never edited here. An agent that has read it
can author and repair `todo` / `kanban` / `backlog` / `audio` documents — the
same conforming files the Hub already renders
([markdownto-rendering.md](markdownto-rendering.md)) and the markdownto.ai
playground already saves through the API ([save-api.md](save-api.md)). This is
the third side of that integration: the Hub can display those files and store
those files, and now the agents working in it can write them.

## Why a pinned copy doesn't go stale

Vendoring instructions is usually a trap — the copy is correct on the day it is
taken and wrong on the day the upstream ships a change. This skill is written to
survive that, deliberately. It **teaches discovery instead of reciting a
roster**: it names `mdto spec` and `https://markdownto.ai/llms.txt` as the live
sources of truth for which specs exist, and marks its own examples as "the shape
a spec takes, not the roster". A fifth spec published upstream therefore does not
invalidate this copy — an agent following it goes and finds the fifth spec.

That property is what makes the pin safe, so it is a test rather than a note:
`TestMarkdownToSkillIsSpecAgnostic` fails if a future re-vendor lands a copy that
stopped pointing at the live sources.

## How an agent receives it

Two paths, one vendored copy behind both.

**Agents working against the Hub (the reason this exists).** A remote agent has
no workspace and no skills directory to load from — it has an MCP connection.
The `docs` tool is the Hub's one channel for instructional text, and it is
registered on *every* connection before any scope is consulted
(`newMCPServer` in `internal/hub/mcpapi.go`), so anything in the topic table is
something an agent has out of the box. The skill is a topic:

```
docs { "topic": "markdownto" }     → the SKILL.md, byte for byte
docs { "topic": "list" }           → names it, so it is findable without knowing it
```

Unconditional on purpose. There is no per-instance setting and no scope to hold:
it is instructions, a read-only connection gets it like any other read, and there
is nothing here that would be safer off than on.

**Local harnesses.** `afs skills` materializes the whole pack — including this
skill's `examples/` — under the afs config dir and prints where to copy it
(`~/.claude/skills/` for Claude Code, or the equivalent). afs still never writes
into a harness directory itself.

The topic table lives in `internal/docs`, which both MCP servers wrap, so
`afs docs markdownto` and the local `afs mcp` `docs` tool serve the same bytes as
the Hub. That shared table is the reason the skill is a docs topic at all: it is
the only place a single change reaches every agent surface.

## What it does not include: the mdto CLI

**The Hub does not provision tools into an agent's environment, and there is no
mechanism here that could carry the `mdto` CLI.** The current hosted agent runs
on Vercel and talks to the Hub over HTTP ([hosted-agent.md](hosted-agent.md));
remote MCP agents run wherever their host runs. The only place this repo installs
a binary for an agent is the legacy Sprite fallback, which curl-installs `afs`
(`installAfs` in `internal/hub/agent.go`) — that is a per-VM path that is not the
production architecture, and adding a second binary to it would provision only
that fallback.

So the honest position is: **assume the agent has no `mdto`.** With that
assumption the skill divides cleanly, and it is worth knowing which half you are
promising:

| Skill section | Without the CLI |
| --- | --- |
| §1 what this is, §2 which spec | Works. The envelope rule is prose, and discovery has a no-CLI route: `markdownto.ai/llms.txt`, plus `markdownto.ai/specs/<name>.md` for any single spec (§7). |
| §3 scaffolds | Works. The four scaffolds are complete, literal, minimal-valid files — byte-identical to `examples/`. An agent can author a conforming document from the skill text alone. |
| §4 validate + repair | **Needs the CLI.** `mdto validate` has no substitute here. An agent can read the diagnostic-code ranges and the near-miss reasoning, but it cannot run the check. |
| §5 mutate, never rewrite | **Needs the CLI.** The whole point of the section is that `mdto <spec> <verb>` makes the minimal edit; without it an agent is hand-editing, which is what the section warns against. |
| §6 render | **Needs the CLI** — though the Hub renders these files itself, so a Hub-stored document has a rendered view regardless. |

Note the exact wording, because it is a smaller claim than it first reads: the
skill offers the live URLs "when working outside a **checkout**" (§1) and "for an
agent working outside this **repo**" (§7). Neither sentence addresses a missing
CLI, and nothing in the skill says "if `mdto` is not installed, do this instead".
The no-CLI path is real and reachable, but it is inferred by the reader, not
stated by the author. If that gap is worth closing it should be closed upstream,
in the markdownto repo, and re-vendored — never patched here.

## Upgrade procedure (deliberate, never automatic)

Like the rendering bundle, this is a bump someone makes on purpose. There is no
auto-update path and there must not be one.

1. `cp -R <markdownto>/skills/markdownto/SKILL.md <markdownto>/skills/markdownto/examples \`
   `  skills/markdownto/` — verbatim, no edits, ever. If a change is needed, make
   it upstream and re-vendor.
2. Update `commit`, `repo-head`, `vendored`, and **every** `sha256 <path>` line in
   `skills/markdownto/VERSION`:
   ```sh
   git -C <markdownto> rev-parse HEAD
   shasum -a 256 skills/markdownto/SKILL.md skills/markdownto/examples/*.md
   ```
   A file added upstream needs its own `sha256` line — the pin test fails on an
   embedded file the manifest does not name, so an unpinned file cannot ship.
3. `go test ./internal/skills/ ./internal/hub/ -run MarkdownTo` — the manifest is
   asserted against the embedded bytes, per file, and the served copy is asserted
   byte-identical over a real MCP session.
4. Read the diff of `SKILL.md`. The two things worth checking by eye: it still
   points at `mdto spec` **and** `markdownto.ai/llms.txt` (the test checks this,
   but read *why* if it changed), and the §3 scaffolds still match `examples/`.

The `examples/` files are the byte-normative templates the markdownto
conformance suite validates. Nothing in this repo can run that suite — it needs
the CLI — so byte equality with the pinned copy **is** the conformance
guarantee. That is why the manifest pins each example individually rather than
hashing the directory.

## Where the pieces live

| File | What it holds |
| --- | --- |
| `skills/markdownto/SKILL.md` | the vendored skill, as agents read it |
| `skills/markdownto/examples/` | four byte-normative minimal-valid documents |
| `skills/markdownto/VERSION` | the manifest: source, commit, per-file sha256 |
| `embed.go` | `SkillsFS` (whole directory, so `examples/` ship) and the `DocsFS` entry for the topic |
| `internal/docs/docs.go` | the `markdownto` topic — the row both MCP servers read |
| `internal/hub/mcpapi.go` | `addDocsTool`: the unconditional Hub channel |
| `internal/skills/skills.go` | `List` / `Materialize` for `afs skills` |
| `internal/skills/markdownto_pin_test.go` | the pin, both directions, plus the discovery-property guard |
| `internal/hub/mcpskill_test.go` | that a connected agent actually receives it |
