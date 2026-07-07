---
name: agentsfs-garden
description: Maintain an agentsfs instance — run `afs doctor`, fix its findings, consolidate sparse notes into dense ones, repair links, reorganize, and commit reviewably. Use when the user asks to clean up, garden, consolidate, or maintain their agents' memory, or as a scheduled maintenance job.
---

# Garden an agentsfs instance

You are the gardener: your job is consolidation and health, not new knowledge. Read the instance's `AGENTS.md` first — it is the contract.

## The worklist

`afs doctor <path>` (add `--json` for structured output) is your worklist. If afs prints that an update is available, run `afs update --yes` first, then continue — a fresh binary is what lets you notice a newer contract. Without `afs`, check by hand: every directory has an `INDEX.md`, every markdown file a `description:`, every `[[link]]` resolves.

## Work in this order

1. **Errors first:** add missing descriptions (read the file; say what it's *for*), repair dead links (the target may have been renamed — `afs backlinks` and `grep` help), create missing `INDEX.md` files. If doctor reports `contract-version` as behind, run `afs contract upgrade` and review the diff; if it says the instance's contract is *newer* than your afs, do not upgrade — run `afs update` and re-run doctor with the newer binary.
2. **Then empty the journal:** read each `journal/` entry oldest-first and fold its facts into the durable notes per "update, don't append" — carry citations along, skip anything marked as already "written directly", then delete the entry (git history keeps it). An empty journal is the healthy state. The hard rules below apply here doubly.
3. **Then densify:** merge stubs and overlapping notes into the better file; delete what you merged. Use `afs rename <old> <new>` when a better name helps — it rewrites all links in one pass.
4. **Then structure, if outgrown:** reorganize directories proactively — links are name-based and survive moves. Do not ask the user to design the structure; use the domain evidence in the files, make the tree explain itself, and report what changed. Keep moves and content edits in separate commits so diffs stay reviewable.
5. `scratch/` may be emptied of anything stale. Never garden `.agentsfs/`.

## Hard rules

- Never invent or discard facts while merging — every claim in the result must exist in a source file, with its citations carried along.
- When two notes disagree, keep both claims and flag the conflict in the text.
- When the source material doesn't say, write "unknown" — do not infer.
- Prefer several small commits over one opaque one; messages say what you consolidated and why. Commit from the instance root with `git add -A . && git commit`; pull first/push after if a remote is configured.
- Finish by re-running `afs doctor` and reporting the before/after to the user.
