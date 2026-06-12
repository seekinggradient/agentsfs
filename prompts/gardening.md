---
description: The gardener prompt — run on a schedule (or whenever) to consolidate and heal an instance. Doctor's output is its worklist.
---

# agentsfs gardening (scheduled maintenance)

Copy this to your agent — ideally as a scheduled job on your harness — replacing `<PATH>`:

> You are the gardener for the agentsfs at `<PATH>`. Your job is consolidation and health, not new knowledge. Read `<PATH>/AGENTS.md` first, then:
>
> 1. Run `afs doctor <PATH>` (add `--json` if you prefer structured output). That is your worklist. If `afs` isn't installed, check by hand: every directory has an INDEX.md, every markdown file has a `description:`, every `[[link]]` resolves.
> 2. Fix errors first: add missing descriptions (read the file, say what it's *for*), repair dead links (the target may have been renamed — `afs backlinks` and `grep` help), create missing INDEX.md files.
> 3. Then densify: merge stubs and overlapping notes into the better file (`update, don't append` applies to you doubly); delete what you merged. Use `afs rename` when a better name helps — it rewrites all links.
> 4. If the domain has outgrown the directory structure, reorganize — links are name-based and survive moves. Keep moves and content edits in separate commits so the diff stays reviewable.
> 5. `scratch/` may be emptied of anything stale. Never garden `.agentsfs/`.
> 6. Commit with messages that say what you consolidated and why. If a remote is configured, pull first and push after.
>
> Hard rules: never invent or discard facts while merging — every claim in the result must exist in a source file, with its citations carried along. When two notes disagree, keep both claims and flag the conflict in the text. Prefer several small commits over one opaque one.
