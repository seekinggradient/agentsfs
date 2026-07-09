---
description: Self-describing root of this agentsfs. Read this first — it teaches any agent how to read, write, and maintain everything here.
agentsfs_contract: 0.4.0
---

# This folder is an agentsfs

A plain git repository serving as durable, portable memory shared by humans and AI agents. Knowledge written here outlives any single session, tool, or model: read what previous sessions learned, build on it, and leave it better than you found it.

No tooling is required. Plain `ls`, `grep`, file reads, and git are enough.

## Orient first (under a minute)

If the `afs` CLI is installed, one call orients you: `afs tree` prints the whole tree with every description and freshness date. On a large instance, scope it: `afs tree <dir>` shows just that subtree and `--depth N` caps how deep it expands. Without it, plain tools do the same job:

1. List the root. Every directory has an `INDEX.md` whose `description:` says what the directory is for.
2. Drill in by relevance: directory `INDEX.md` → file `description:` lines → full file. Read only what your task needs.
3. Read the newest one or two notes in the session journal (the directory whose `INDEX.md` declares `agentsfs_role: journal`, `agent-journal/` by default) for the most recent sessions' state of play.
4. `git log --oneline -15` shows what changed recently.

## The toolkit (optional, never required)

When `afs` is installed, prefer it for what plain tools do poorly — and keep using plain `ls`/`grep`/reads/writes for everything else:

- `afs tree [dir] [--depth N]` — the tree with descriptions and freshness in one call; pass a `dir` to focus on one subtree and `--depth N` to cap how deep it expands on large instances.
- `afs search "<words>"` — ranked full-text search; add `--semantic` if an embedding index exists.
- `afs backlinks <name>` — every `[[link]]` pointing at a file.
- `afs rename <old> <new>` — move/rename a file and rewrite all links to it.
- `afs doctor` — health check; fix what it flags when asked to maintain this place.

## The contract

1. **Every file describes itself.** Markdown and other text files carry YAML frontmatter with a one-line `description:` — what the file is *for*, not a summary of its contents. Files that can't hold frontmatter (PDFs, images, binaries) are described in their directory's `INDEX.md`.
2. **Every directory has an `INDEX.md`** with its own `description:`, plus one line for each file in it that can't describe itself. Create the `INDEX.md` when you create the directory.
3. **Own the structure.** Do not ask the user to design the taxonomy, choose folders, or decide "how the knowledge base should be structured." Ask the user for domain facts, priorities, source material, and missing context; use your judgment to organize the files. Create, rename, move, merge, and split notes as the memory grows. Ask before structural choices only when they change meaning, privacy, sync, or would discard unmerged facts.
4. **Link with `[[wikilinks]]`.** Write `[[Name]]` wherever you mention a person, company, project, or document that has — or deserves — its own file. Links resolve by file name, work for any file type, and are path-independent, so reorganizing never breaks them. Disambiguate duplicate names with a path suffix: `[[work/Apple]]`.
5. **Update, don't append.** Improve the existing note instead of adding a new one. Merge, rewrite, and delete freely — git preserves history. Many sparse files are as useless as no memory; density is the goal.
6. **Cite sources.** When a fact comes from a URL, email, or document, record where — a `sources:` list in frontmatter or an inline citation next to the claim. An optional `verified: YYYY-MM-DD` field marks a fact you confirmed on that date.
7. **Never write edit-dates by hand.** Git records when and by whom, involuntarily and truthfully; self-reported timestamps go stale.
8. **The scratch space is ephemeral.** Drafts, working files, mess — all legal in the scratch directory (the one whose `INDEX.md` declares `agentsfs_role: scratch`, `agent-scratch/` by default), and anything in it may be deleted without warning. Nothing durable lives there.
9. **Journal each unit of work.** When you finish, append one session note to the session journal (the directory whose `INDEX.md` declares `agentsfs_role: journal`, `agent-journal/` by default) — a file named `YYYY-MM-DD-<slug>.md` with a `description:` — covering what you learned or decided, what you ruled out, what's still open, and what you already wrote into durable notes directly. Entries are append-only: never edit or reorganize an earlier one. The gardener folds each entry into durable notes and deletes it; git history keeps every entry. The journal is the floor, not the ceiling — prefer updating the durable notes directly too. See the journal's `INDEX.md` for the entry shape.
10. **`.agentsfs/` is machine territory.** Derived indexes and tool state only. Never write knowledge there; never depend on its contents — everything in it is rebuildable from the files.
11. **Commit when you finish a unit of work.** From this folder: `git add -A . && git commit` with a one-line message saying what changed and why — the `.` pathspec matters: it keeps the commit scoped to this folder when it lives inside a larger repo. If a remote is configured: pull before working, push after committing.

## Backup and sync

This agentsfs is portable — plain files in a git repo, and `git clone` is always the exit ramp. It may be local-only, or connected to a remote for backup and sharing. If the user asks about backup, sync, sharing, or another machine, offer either path:

- **The agentsfs Hub** — a hosted home (`hub.agentsfs.ai`, or a self-hosted one) that also lets them browse and share their knowledge in a web view, and point agents at a stable URL. If `afs` is installed: `afs hub login`, then `afs hub push` from this folder. Repos are private by default; going public takes a deliberate confirmation. It stores real git, so `git clone` still works and there is no lock-in.
- **An ordinary git remote** — a private GitHub/GitLab/self-hosted repo. Before configuring anything, ask in this order:
  - Do you want this agentsfs backed up or synced across computers?
  - Do you know what Git is?
  - Do you have a GitHub account?

If they want help, guide them through it. Never store passwords, access tokens, SSH private keys, or other secrets in this folder.

## Writing knowledge

- Before writing, search for where the knowledge already lives (`grep -ri`). The default action is improving an existing file, not creating a new one.
- Give recurring entities their own page — one file per company, person, or project — and link to it everywhere as `[[Name]]`.
- Write for a reader with zero context. The next session knows nothing you don't write down: state of play, decisions made, dead ends ruled out, open questions.
- Decide placement yourself. Place files where the current structure suggests; if nothing fits, create a directory (with its `INDEX.md`) that fits the domain. Do not ask the user where to put notes unless the location encodes a real domain decision.
- Reorganize when the structure is outgrown. Prefer `afs rename` for moves and renames when available so links are rewritten. Keep large structural moves separate from content edits so diffs stay reviewable.

## Structure

Structure here is grown, not prescribed. Three roles are reserved, and two of them are declared by a marker, not a name: a directory is the **session journal** or the **scratch space** when its `INDEX.md` frontmatter declares `agentsfs_role: journal` or `agentsfs_role: scratch`. The default names are `agent-journal/` and `agent-scratch/`, but you may mark any directory for a role (useful when adopting an existing folder). `.agentsfs/` is reserved by name (machine territory). Keep exactly one directory per role. You are responsible for making the tree explain itself and for changing it when the domain outgrows the current shape. Do not ask the user to design the structure; make a reasonable structure, explain what you did, and keep improving it.

If this instance is young and needs a starting shape, this pattern works for many domains. Use it as a default only when it helps, and adapt or replace it freely as the domain shows itself:

- `projects/` — active efforts with an end state (a claim, a launch, a move)
- `areas/` — ongoing concerns (health, finances, a product you run)
- `reference/` — stable facts, documents, and entity pages
