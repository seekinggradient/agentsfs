---
name: agentsfs-adopt
description: Adopt an existing vault or folder of notes into an agentsfs — orient, interview the user about active vs personal vs media areas, mark reserved roles and collections, then describe and link the active areas. Additive-only; never rewrites a note. Use when the user wants to bring an Obsidian vault, a Notes folder, or a pile of markdown under agentsfs.
---

# Adopt an existing vault into an agentsfs

You are the adopter: your job is to make an existing folder of notes healthy and self-describing **without rewriting anything the user already wrote**. Read the instance's `AGENTS.md` first — it is the contract. If the folder has none, run `afs init <path>` to lay the contract down, then continue.

## Orient

`afs tree <path>` shows the shape; `afs doctor <path>` (add `--json` for structured output) is your worklist. Skim the top-level directories and a handful of notes before touching anything — understand what this vault *is* first.

## Work in this order

1. **Interview briefly.** Ask the user — a sentence or two, not a survey — which areas are *active knowledge* (read, extended, relied on), which are *personal chronology* (a diary, daily notes), and which are *media or archive* (attachments, images, exports, old material). You are classifying their existing folders, not proposing new ones.
2. **Mark the reserved roles.** The session journal and scratch space are declared by a marker (`agentsfs_role: journal` / `agentsfs_role: scratch` in a directory's `INDEX.md`), not by name. **Never claim an existing personal directory as the journal** — its consume-and-delete semantics would destroy a diary. If the user wants a journal or scratch, create or designate one *with them* (the default `agent-journal/` is safe).
3. **Declare collections.** For each personal-chronology, media, and archive directory, add `agentsfs_role: collection` to its `INDEX.md` frontmatter — or, if it has none, create a minimal `INDEX.md` with a truthful one-line `description:` plus the marker. A collection is described *collectively* by that INDEX: doctor stops asking for per-file descriptions, per-subdirectory INDEX files, and link health beneath it, and it is never emptied or deleted. Keep descriptions of personal material short and neutral.
4. **Then describe the active areas** — the real work, additive only. For directories the user called active knowledge: add a one-line `description:` to each markdown file (what it is *for*, neutral in tone), create the missing `INDEX.md` files (each with its own `description:`, plus a line for files that can't describe themselves), and repair the dead links doctor flags there. Use `[[wikilinks]]` where a real entity is named.
5. **`afs doctor` is the progress meter.** Re-run it after each pass; the findings shrink. Target healthy — and staying healthy as the user keeps adding notes.

## Hard rules

- **Additive only.** Never modify the body of an existing note. Never rename, move, merge, or split during adoption — that comes later, once the user trusts the process. You add frontmatter and INDEX files around what exists, nothing more.
- **Respect privacy.** Descriptions of personal material stay short and neutral. A personal directory becomes a collection, never the journal.
- When you don't know what a file is *for*, ask or write "unknown" — do not guess.
- Prefer several small commits over one opaque one; messages say what you marked or described (e.g. "mark Diary/ as a collection", "describe the Projects/ notes"). Commit from the instance root with `git add -A . && git commit`; pull first/push after if a remote is configured.
- Finish by re-running `afs doctor` and handing back a short summary of what you marked, what you described, and anything you left for the user to decide.
