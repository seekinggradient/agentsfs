---
description: Prioritized backlog — pending work, discovered tasks, and parked ideas for this knowledge base.
agentsfs_role: backlog
---

# Backlog

> Bands in priority order: **Now · Next · Later · Someday** (parked, never offered as ready) · **Done** (pruned by the gardener; git and the journal keep the trail).
> Top of a band is highest priority; ordering is priority, never sequencing — reordering lines is the safe way to reprioritize.
> Markers: `- [ ]` open · `- [/]` in progress · `- [x]` done · `- [-]` dropped. Nest children to decompose a task; a parent finishes only when its children do.
> Name a task with a trailing `^kebab-slug` and reference it as `[[#^slug]]` here or `[[backlog#^slug]]` from anywhere. Block with `— blocked by [[#^slug]]` (lifts when every named task closes; plain-text blockers hold until edited away).
> A task that accumulates real state graduates to its own note; link the line to it. Full conventions: rule 13 of `AGENTS.md`.

## Now
- [ ] Decide the [[harness-plugins]] RFC (owner call) — remaining scope after 2026-08-07: pre-compact capture context and stop-time compliance reminders; prime is out (stays agent-initiated) ^harness-plugins-decision
- [-] Wire `afs prime` into Claude Code and Codex session-start hooks — dropped 2026-08-07: owner decided prime is pull-based; the contract instructs agents to run it
- [x] Deploy the Hub `/mcp` OAuth build to hub.agentsfs.ai — deployed 2026-08-07, verified: OAuth discovery 200, unauthenticated `/mcp` 401, login page live

## Next
- [ ] Decide the content domain for `/render` sandboxed HTML (open since the share-links ship)
- [ ] Backlog styling on share-link views — one-line wiring in sharelink.go, flagged during the backlog build
- [ ] Add a read-only `tasks` tool to the local MCP server — touches the "12 tools" count in README, concepts, capabilities, and mcp docs ^mcp-tasks-tool

## Later
- [ ] Hub cross-KB aggregated backlog view — "my Now items across every knowledge base" ^cross-kb-backlog
- [ ] Kanban-style Hub view of backlog pages
- [ ] Contract-diff variants audit: AGENTS-0.5.0.md has the same latent pristine-text split found and fixed for 0.9.0

## Someday
- [ ] Beads issues.jsonl importer (one-shot converter, only if a user asks)

## Done
- [x] Backlog and tasks feature — grammar, `afs tasks`/`afs prime`/`afs tree --budget`, doctor findings, Hub rendering, contract 0.10.0 → [[backlog-and-tasks]]
