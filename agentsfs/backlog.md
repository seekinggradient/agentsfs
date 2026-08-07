---
description: Prioritized backlog — pending work, discovered tasks, and parked ideas for this knowledge base.
agentsfs_role: backlog
---

# Backlog

This page is the instance's **backlog** — the role is set by the `agentsfs_role: backlog` marker above, not by the file name, so a backlog can live under any name you mark. `backlog.md` is only the default, and there is exactly one per instance.

Work that is pending lives here so the next session can pick it up: pull from the top, record work you discover in the band it belongs in, check off what you finish, and park ideas rather than losing them. `afs tasks` derives the ready-work view from this page; `afs prime` puts the top of it in front of a starting session.

Conventions:

- **Bands** are the `##` headings, in priority order: **Now** (this session), **Next** (soon), **Later** (real, not soon), **Someday** (the parking lot — never offered as ready work), **Done** (the archive). Order within a band is priority, top first.
- **Markers**: `- [ ]` open · `- [/]` in progress · `- [x]` done · `- [-]` dropped.
- **Nesting is decomposition.** Indented children break their parent down; a parent is finished only when its children are, so never check one off over open children.
- **Ordering is priority, never sequencing.** Reordering lines is the reprioritization gesture and must stay safe: say "after this" by nesting or an explicit blocker, never by position.
- **Anchors.** A trailing `^kebab-slug` names a task so it can be referenced — `[[#^slug]]` from this page, `[[backlog#^slug]]` from anywhere. Journal entries cite the task they served this way. Keep slugs unique here.
- **Blockers.** Write `— blocked by [[#^other-task]]`; the block lifts by itself once every task it names is done or dropped. A plain-text blocker (`— blocked by the vendor's reply`) holds until you edit it away.
- **Graduation.** When a task accumulates real state, give it its own note and link the line to it (`- [/] Hub sync polish → [[hub-sync-status]] ^hub-sync-polish`). This page stays dense.
- **Hygiene.** The gardener prunes `## Done` and flags stale `[/]` items. Git history and the journal are the audit trail, so pruning loses nothing.

Shape — an example, not real work; the live bands are the empty headings below it:

```markdown
## Now
- [/] Embedded hub sync status polish ^hub-sync-polish
  - [x] Fix PJAX test flake
  - [ ] Update shipped-docs page
- [ ] Draft tasks RFC — blocked by [[#^prime-design]]

## Next
- [ ] Prime adaptive tree rendering ^prime-design

## Later
- [ ] Kanban view of backlog pages on Hub

## Someday
- [ ] Beads issues.jsonl importer

## Done
- [x] Beads research → [[beads-research-report]]
```

## Now
- [ ] Decide the [[harness-plugins]] RFC (owner call) — prime exists; hook packaging is its scope ^harness-plugins-decision
- [ ] Wire `afs prime` into Claude Code and Codex session-start hooks — blocked by [[#^harness-plugins-decision]]
- [ ] Deploy the Hub `/mcp` OAuth build to hub.agentsfs.ai (shipped 2026-07-25, deploy pending)

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
