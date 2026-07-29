---
description: Index of superseded and historical AgentsFS docs — old plans, point-in-time audits, and replaced reference. Kept for reasoning, not for accuracy.
---

# Archive

Nothing in this directory is maintained. These files record how decisions were reached and
what things looked like on a particular day. Read them for the argument; do not read them
for current behavior. Every file opens with a banner naming what replaced it.

None of this ships to users — `embed.go` embeds `docs/*.md`, a flat glob that does not
descend into subdirectories, so archived material stays out of released binaries.

## Plans

| File | What it recorded | Read instead |
| --- | --- | --- |
| [execution-plan.md](execution-plan.md) | Layer-by-layer roadmap for the core CLI, June 2026. All layers shipped. | [../capabilities.md](../capabilities.md), [../agentsfs-source-of-truth.md](../agentsfs-source-of-truth.md) |
| [hub-execution-plan.md](hub-execution-plan.md) | The decision to build a hosted storage layer, and its original phases. The Hub has shipped well past them. | [../how-the-hub-works.md](../how-the-hub-works.md), [../internals/hosted-agent.md](../internals/hosted-agent.md) |
| [eve-migration.md](eve-migration.md) | Why Eve replaced the bespoke Sprite-hosted agent. | [../internals/eve-hosting.md](../internals/eve-hosting.md), [../internals/eve-hub-integration.md](../internals/eve-hub-integration.md) |

## Build and demo records

| File | What it recorded | Read instead |
| --- | --- | --- |
| [build-report-layers-2-4.md](build-report-layers-2-4.md) | What landed in `tree`, `doctor`, `rename`, and search, with the design calls behind each. | [../capabilities.md](../capabilities.md) |
| [gate-1-demo.md](gate-1-demo.md) | The Layer 1 gate: fresh-context agent sessions against a real instance. | [../capabilities.md](../capabilities.md), [../setup.md](../setup.md) |

## Audits

| File | What it recorded | Read instead |
| --- | --- | --- |
| [doc-audit-2026-07-28.md](doc-audit-2026-07-28.md) | The audit of every doc surface that produced the current three-tier layout. | [../README.md](../README.md) |
| [doc-audit-cli-surface-2026-07-28.md](doc-audit-cli-surface-2026-07-28.md) | The audit of the docs shipped inside the binary, against v0.10.0. | [../README.md](../README.md), `afs docs` |
| [hub-ui-audit-2026-07-27.md](hub-ui-audit-2026-07-27.md) | A point-in-time review of the Hub web interface; its findings have landed. | [../how-the-hub-works.md](../how-the-hub-works.md) |

## Superseded reference

| File | Why it moved | Read instead |
| --- | --- | --- |
| [hub-mcp.md](hub-mcp.md) | Folded into a single doc covering both MCP servers, because the common mistake is assuming they are the same server. | [../mcp.md](../mcp.md), or `afs docs mcp` |

## Archiving something

Move the file here, add a banner immediately under the H1 saying what it is, that it is not
maintained, and which live doc replaced it — with a working relative link. Then add a row
above and fix any inbound links; `git grep` the old path.
