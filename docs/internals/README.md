---
description: Index of AgentsFS maintainer reference — architecture, the Hub/Eve contract, access model, and release procedure. Not shipped in the afs binary.
---

# Internals

Reference for people changing AgentsFS, not people using it. These files name environment
variables, Go source files, and deployment steps, and they go stale the moment the code
moves — so they are verified against source and dated rather than written to last.

None of this ships to users. `embed.go` embeds `docs/*.md`, a flat glob that does not
descend into this directory, so nothing here reaches a released binary or `afs docs`.

## Architecture

| File | Covers |
| --- | --- |
| [hosted-agent.md](hosted-agent.md) | The current Hub ↔ Eve architecture: how the Hub authenticates the agent, how it reads and writes knowledge, and how it is released. Start here. |
| [eve-hosting.md](eve-hosting.md) | Where Eve runs and what the Hub keeps authority over. |
| [eve-hub-integration.md](eve-hub-integration.md) | The wire contract between Hub and Eve: identity handoff, the agent PAT, the revision-pinned API. |
| [kb-access-and-isolation.md](kb-access-and-isolation.md) | The decision record behind remote-at-HEAD reads and compare-and-swap writes, instead of clone-and-sync. |
| [agent-review-mode.md](agent-review-mode.md) | Agentic co-editing on a rendered note: comment, draft, diff, approve one commit. |

## Operations

| File | Covers |
| --- | --- |
| [how-deployment-works.md](how-deployment-works.md) | Which surfaces release independently, what each deploy command does, and what is automatic. |
| [releasing.md](releasing.md) | The three install paths and the tag-driven release process. |
| [hub-repoview-performance.md](hub-repoview-performance.md) | Debug note for the 2026-07 repo/file page fix on large knowledge bases. |

## Keeping these honest

State what is deployed, and say when you checked. Several files open with a `Status:` line
naming the source files the claim was verified against — copy that habit. When a design
here is superseded rather than amended, move the file to
[../archive/](../archive/README.md) with a banner instead of leaving a stale one in place.
