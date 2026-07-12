---
name: agentsfs-remember
description: Save knowledge from the current conversation into the user's agentsfs memory, following its contract — update dense notes, add descriptions and wikilinks, cite sources, commit. Use when the user says "remember this", "save this for next time", "add this to my memory/notes", or when a session produced durable learnings worth keeping.
---

# Save knowledge to agentsfs memory

## 1. Find the instance

Your context likely already contains a connection block ("A durable, user-owned memory lives at `<path>`") — use that path. Otherwise ask the user where their agentsfs lives. Read its `AGENTS.md` first if you haven't this session; it is the contract and overrides anything here.

## 2. Search before writing

`afs search "<words>" <path>` (or `grep -ri`) for where this knowledge already lives. **The default action is improving an existing file, not creating a new one.** Many sparse files are as useless as no memory.

## 3. Write it well

- Dense updates in place: merge into the existing synthesized note and proactively reorganize when that improves the memory. Preserve every unique fact, citation, decision, and unresolved conflict; preserve primary-source bodies, meaning, and chronology when moving source material.
- One-line `description:` frontmatter on any new file — what it's *for*, not a summary.
- `[[Wikilinks]]` for every person, company, or project that has or deserves its own page; create entity pages for recurring ones.
- `sources:` for where facts came from (URL, email, conversation — "conversation with user, <date>" counts).
- New directory → create its `INDEX.md` in the same breath. Never write dates-of-edit by hand; never write into `.agentsfs/`; nothing durable in the scratch space (the directory marked `agentsfs_role: scratch`, `agent-scratch/` by default).
- Write for a reader with zero context: state of play, decisions made, dead ends ruled out, open questions.
- Own placement and structure. Do not ask the user where to put notes or how to structure the knowledge base; ask for missing domain context, then place, move, merge, or split files as needed. If the current structure is outgrown, improve it and explain what changed.

## 4. Commit and report

Review the changes within the instance and commit every file belonging to the completed unit with a clear one-line message; do not include unrelated files outside the agentsfs. If a remote is configured, pull before writing and immediately push after committing; use `afs hub push` for the Hub and `git push` otherwise. If another checkout pushed first, reconcile before retrying and never force-push. Then tell the user exactly what was stored and where.
