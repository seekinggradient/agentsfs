---
description: Index of the AgentsFS documentation tree — which files ship inside the afs binary, which are maintainer reference, and which are history.
---

# AgentsFS documentation

Three tiers, and the tier a file sits in tells you how much to trust it.

| Tier | Directory | Ships in the `afs` binary | What it is |
| --- | --- | --- | --- |
| Product docs | `docs/` | yes | Evergreen. What AgentsFS is and how to use it. |
| Internals | [`docs/internals/`](internals/README.md) | no | Maintainer reference: architecture, deployment, release. |
| Archive | [`docs/archive/`](archive/README.md) | no | Historical plans, audits, and superseded docs. Every file carries a banner. |

`embed.go` embeds `docs/*.md` — a flat glob that does not match subdirectories. Moving a
file into `internals/` or `archive/` removes it from every released binary. That is the
mechanism, and it is deliberate: internal planning notes should not ship to users.

## The docs that ship

Most are reachable offline as `afs docs <topic>`, from any workspace, with no checkout.

| File | `afs docs` topic | Read it for |
| --- | --- | --- |
| [agent-start.md](agent-start.md) | `agent-start` | The primer an agent reads first in a fresh workspace. |
| [concepts.md](concepts.md) | `concepts` | The vocabulary: instance, knowledge base, contract, roles, wikilinks, Hub, Eve. |
| [capabilities.md](capabilities.md) | `capabilities` | What each of the four surfaces can do, task by task. |
| [setup.md](setup.md) | `setup` | The full setup guide, for humans and for agents. |
| [mcp.md](mcp.md) | `mcp` | Both MCP servers — the local `afs mcp` and the Hub's remote `/mcp` — tool by tool. |
| [hub.md](hub.md) | `hub` | Connecting an agentsfs to a Hub and pushing it. |
| [how-the-hub-works.md](how-the-hub-works.md) | — | A plain-language walkthrough of the Hub for newcomers. |
| [agentsfs-source-of-truth.md](agentsfs-source-of-truth.md) | — | What AgentsFS is and why; the settled design decisions. |

Two more topics come from outside this directory: `afs docs contract` renders
`template/AGENTS.md`, and `afs docs commands` is generated from the command table in
`internal/docs/docs.go`.

## Where to start

- **You are an agent, dropped into an unfamiliar repo.** `afs docs agent-start`.
- **You are setting this up for the first time.** [setup.md](setup.md).
- **You want to know whether the CLI, MCP, or the Hub can do the thing.** [capabilities.md](capabilities.md).
- **A word in the docs means nothing to you.** [concepts.md](concepts.md).
- **You are changing how the Hub or the hosted agent works.** [internals/](internals/README.md).

## Adding a doc

A new user-facing doc goes in `docs/` and gets `description:` frontmatter. To make it a
topic, add it to `topics` in `internal/docs/docs.go` and link its path from the repo-root
`README.md` — `internal/docs/docs_test.go` asserts both. Maintainer-only material goes in
`internals/`. Anything superseded moves to `archive/` with a banner pointing at what
replaced it.
