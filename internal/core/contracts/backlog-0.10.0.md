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

## Next

## Later

## Someday

## Done
