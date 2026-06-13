---
description: Self-describing root of this agentsfs. Read this first — it teaches any agent how to read, write, and maintain everything here.
---

# This folder is an agentsfs

A plain git repository serving as durable, portable memory shared by humans and AI agents. Knowledge written here outlives any single session, tool, or model: read what previous sessions learned, build on it, and leave it better than you found it.

No tooling is required. Plain `ls`, `grep`, file reads, and git are enough.

## Orient first (under a minute)

If the `afs` CLI is installed, one call orients you: `afs tree` prints the whole tree with every description and freshness date. Without it, plain tools do the same job:

1. List the root. Every directory has an `INDEX.md` whose `description:` says what the directory is for.
2. Drill in by relevance: directory `INDEX.md` → file `description:` lines → full file. Read only what your task needs.
3. `git log --oneline -15` shows what changed recently.

## The toolkit (optional, never required)

When `afs` is installed, prefer it for what plain tools do poorly — and keep using plain `ls`/`grep`/reads/writes for everything else:

- `afs tree` — the whole tree, descriptions, freshness, one call.
- `afs search "<words>"` — ranked full-text search; add `--semantic` if an embedding index exists.
- `afs backlinks <name>` — every `[[link]]` pointing at a file.
- `afs rename <old> <new>` — move/rename a file and rewrite all links to it.
- `afs doctor` — health check; fix what it flags when asked to maintain this place.

## The contract

1. **Every file describes itself.** Markdown and other text files carry YAML frontmatter with a one-line `description:` — what the file is *for*, not a summary of its contents. Files that can't hold frontmatter (PDFs, images, binaries) are described in their directory's `INDEX.md`.
2. **Every directory has an `INDEX.md`** with its own `description:`, plus one line for each file in it that can't describe itself. Create the `INDEX.md` when you create the directory.
3. **Link with `[[wikilinks]]`.** Write `[[Name]]` wherever you mention a person, company, project, or document that has — or deserves — its own file. Links resolve by file name, work for any file type, and are path-independent, so reorganizing never breaks them. Disambiguate duplicate names with a path suffix: `[[work/Apple]]`.
4. **Update, don't append.** Improve the existing note instead of adding a new one. Merge, rewrite, and delete freely — git preserves history. Many sparse files are as useless as no memory; density is the goal.
5. **Cite sources.** When a fact comes from a URL, email, or document, record where — a `sources:` list in frontmatter or an inline citation next to the claim. An optional `verified: YYYY-MM-DD` field marks a fact you confirmed on that date.
6. **Never write edit-dates by hand.** Git records when and by whom, involuntarily and truthfully; self-reported timestamps go stale.
7. **`scratch/` is ephemeral.** Drafts, working files, mess — all legal there, and anything in it may be deleted without warning. Nothing durable lives in `scratch/`.
8. **`.agentsfs/` is machine territory.** Derived indexes and tool state only. Never write knowledge there; never depend on its contents — everything in it is rebuildable from the files.
9. **Commit when you finish a unit of work.** From this folder: `git add -A . && git commit` with a one-line message saying what changed and why — the `.` pathspec matters: it keeps the commit scoped to this folder when it lives inside a larger repo. If a remote is configured: pull before working, push after committing.

## Writing knowledge

- Before writing, search for where the knowledge already lives (`grep -ri`). The default action is improving an existing file, not creating a new one.
- Give recurring entities their own page — one file per company, person, or project — and link to it everywhere as `[[Name]]`.
- Write for a reader with zero context. The next session knows nothing you don't write down: state of play, decisions made, dead ends ruled out, open questions.
- Place files where the structure suggests; if nothing fits, create a directory (with its `INDEX.md`) that fits the domain. Imperfect placement is fine — maintenance sessions reorganize safely, because links are name-based.

## Structure

Structure here is grown, not prescribed. Only `scratch/` and `.agentsfs/` are reserved. If this instance is young and needs a starting shape, this pattern works for most domains — adapt or replace it freely as the domain shows itself:

- `projects/` — active efforts with an end state (a claim, a launch, a move)
- `areas/` — ongoing concerns (health, finances, a product you run)
- `reference/` — stable facts, documents, and entity pages
