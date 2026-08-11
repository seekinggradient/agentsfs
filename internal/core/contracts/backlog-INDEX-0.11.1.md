---
description: Prioritized backlog — the spine of pending work, discovered tasks, and parked ideas for this knowledge base.
agentsfs_role: backlog
---

# Backlog

> Bands in priority order: **Now · Next · Later · Someday** (parked, never offered as ready) · **Done** (pruned by the gardener once the archive has it).
> Top of a band is highest priority; ordering is priority, never sequencing — reordering lines is the safe way to reprioritize.
> Markers: `- [ ]` open · `- [/]` in progress · `- [x]` done · `- [-]` dropped. Nest children to decompose a task; a parent finishes only when its children do.
> Name a task with a trailing `^kebab-slug` — always the LAST token of its line — and reference it as `[[#^slug]]` here or `[[backlog/INDEX#^slug]]` from anywhere (always path-qualified across files: every directory has an `INDEX.md`, so a bare `[[INDEX#…]]` is ambiguous). Block with `— blocked by [[#^slug]]`, written before the trailing `^slug` (lifts when every named task closes; plain-text blockers hold until edited away).
> Questions only the owner can answer: `— blocked by owner: <the question, one line>`. Park the task, pull the next one, never wait — it lifts only when the owner edits the answer in.
> A task that accumulates real state earns its own ticket file beside this one, linked from its line. The ticket's body is current truth, updated in place; a `## Log` section at the bottom is append-only dated events.
> Closed work moves to `archive/` — ticket files stamped `closed: YYYY-MM-DD`, one-liners rolled up into `archive/<year>.md`. The gardener owns the sweep.
> A subdirectory with its own `INDEX.md` is a sub-backlog (no marker of its own); nothing in it is ready until a task here delegates to it: `- [ ] Voice v3 → [[backlog/voice/INDEX]] ^voice-v3`.

## Now

## Next

## Later

## Someday

## Done
