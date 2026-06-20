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

- Dense updates in place: merge into the existing note, rewrite freely — git keeps history.
- One-line `description:` frontmatter on any new file — what it's *for*, not a summary.
- `[[Wikilinks]]` for every person, company, or project that has or deserves its own page; create entity pages for recurring ones.
- `sources:` for where facts came from (URL, email, conversation — "conversation with user, <date>" counts).
- New directory → create its `INDEX.md` in the same breath. Never write dates-of-edit by hand; never write into `.agentsfs/`; nothing durable in `scratch/`.
- Write for a reader with zero context: state of play, decisions made, dead ends ruled out, open questions.
- Own placement and structure. Do not ask the user where to put notes or how to structure the knowledge base; ask for missing domain context, then place, move, merge, or split files as needed. If the current structure is outgrown, improve it and explain what changed.

## 4. Commit and report

From the instance root: `git add -A . && git commit` with a one-line message (the `.` pathspec matters if the instance lives inside a larger repo). Pull first/push after if a remote is configured. Then tell the user exactly what was stored and where.
