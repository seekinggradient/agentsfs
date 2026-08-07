---
description: Session — researched Beads, accepted the backlog-and-tasks RFC, and shipped the whole feature — core grammar parser, afs tasks/prime/tree --budget, doctor findings, Hub rendering, contract 0.10.0 with this instance upgraded as first dogfood.
---

# Backlog and tasks: research → RFC → ship

## Learned / decided

- **Beads research verdict** (full report delivered to Akshay out-of-repo): its value core for us is context injection + structured status, not its Dolt storage or orchestration. Its memory feature is a bare KV; AgentsFS notes already dominate it. Direct adoption rejected — `bd prime` verbatim forbids markdown task files and MEMORY.md, a live contract conflict.
- **Owner reframed and approved**: task tracking IS in scope as prospective memory (goals/priorities), completing episodic (journal) + semantic (wiki) + procedural (skills). Design: write path = markdown editing, read path = derived views. RFC [[backlog-and-tasks]] accepted 2026-08-06 and fully implemented this session.
- **Grammar decisions locked**: `[ ]`/`[/]`/`[x]`/`[-]`; bands Now/Next/Later/Someday/Done; nesting = decomposition; `^slug` block anchors + `[[#^slug]]` refs; `blocked by` annotations (refs auto-lift, text holds); ordering is priority, never sequencing.
- **Contract 0.10.0 shipped**: rule 13, fourth reserved role (first page-level one), template `backlog.md`, upgrade lays it down when the role is unclaimed.
- **Found live in the wild**: the false-"customized" contract bug — `template/AGENTS.md` was edited post-0.9.0-bump without a version change, so two pristine 0.9.0 texts exist. Fixed via vendored variants + variant-aware detection. `AGENTS-0.5.0.md` has the same latent split → backlogged.
- Implementation split across four parallel Opus subagents (core parser / hub rendering / contract bump / CLI), all green; full suite passes on the combined state.

## Open

- See [[backlog#^harness-plugins-decision]] and the rest of the backlog — pending work now lives there, not in journal prose.

## Written directly

- [[backlog-and-tasks]] RFC (accepted, with decision log); rfcs/INDEX.md entry; this instance upgraded to 0.10.0 with its real backlog written into [[backlog]].
