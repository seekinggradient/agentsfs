---
description: "Project memory for building AgentsFS itself — RFCs, design decisions, and cross-session state for the CLI, Hub, and agent surfaces. The meta-instance: AgentsFS remembering how AgentsFS is built."
---

# Index

Project memory for the AgentsFS codebase (the repo this instance lives inside). It carries what the code and git history cannot: why decisions were made, what was ruled out, and the standing plans that span sessions.

- `rfcs/` — design RFCs for major initiatives, one file each, with their decision logs.
- `backlog/` — prioritized work queue and task spine (reserved role), with closed ticket archive under `backlog/archive/`.
- `agent-journal/` — episodic journal (reserved role), with live episodes, bounded history, and retained archives.
- `agent-scratch/` — ephemeral scratch (reserved role).

The current human-facing product term is **workspace**; the hosted agent and compatibility surfaces retain older tool names where needed. The episodic-journal release is CLI v0.15.0 with contract 0.13.0; see [[agent-journal/active/2026-09-07T212300Z-ad2dd57acdb2e3fdbffecfb2-episode]] for its implementation and deployment record. The earlier terminology release remains recorded in [[agent-journal/2026-09-05T185002Z-wsp-workspace-terminology]].
