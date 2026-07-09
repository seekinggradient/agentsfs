---
description: The gardener prompt — run on a schedule (or whenever) to consolidate and heal an instance. Doctor's output is its worklist.
---

# agentsfs gardening (scheduled maintenance)

Copy this to your agent — ideally as a scheduled job on your harness — replacing `<PATH>`:

> You are the gardener for the agentsfs at `<PATH>`. Your job is consolidation and health, not new knowledge. Read `<PATH>/AGENTS.md` first, then:
>
> 1. Run `afs doctor <PATH>` (add `--json` if you prefer structured output). That is your worklist. If afs prints that an update is available, run `afs update --yes` first, then continue — a fresh binary is what lets you notice a newer contract. If `afs` isn't installed, check by hand: every directory has an INDEX.md, every markdown file has a `description:`, every `[[link]]` resolves.
> 2. Fix errors first: add missing descriptions (read the file, say what it's *for*), repair dead links (the target may have been renamed — `afs backlinks` and `grep` help), create missing INDEX.md files. If doctor reports `contract-version` as behind (or afs prints that this instance's contract is behind), run `afs contract upgrade <PATH>` and review the diff — it refreshes AGENTS.md to the current contract. If instead it says the instance's contract is *newer* than your afs, do not upgrade — run `afs update` and re-run doctor with the newer binary. If `afs contract upgrade` **refuses because the contract is customized**, do not pass `--force`: run `afs contract diff <PATH>` to see your adaptations and what the new version changes, port the standard changes into the adapted text by hand (preserving the instance's adaptations; when a change and an adaptation conflict semantically, keep the adaptation unless the user says otherwise), set `agentsfs_contract:` to the new version yourself, then commit. Only overwrite an adaptation with `--force` on explicit user instruction.
> 3. Empty the journal: the session journal is the directory whose `INDEX.md` declares `agentsfs_role: journal` (default `agent-journal/`; on an un-migrated instance it may still be the classic `journal/`). Never treat an unmarked, journal-*named* directory as the journal. Read each entry oldest-first and fold its facts into the durable notes per `update, don't append` — carry citations along, and skip anything the entry marks as already "written directly". Delete each entry once folded; git history preserves it. An empty journal is the healthy state. The hard rules below apply to this doubly: never drop or invent a fact, and keep conflicting claims side by side with the conflict flagged.
> 4. Then densify: merge stubs and overlapping notes into the better file (`update, don't append` applies to you doubly); delete what you merged. Use `afs rename` when a better name helps — it rewrites all links.
> 5. If the domain has outgrown the directory structure, reorganize — links are name-based and survive moves. Keep moves and content edits in separate commits so the diff stays reviewable.
> 6. The scratch space (the directory marked `agentsfs_role: scratch`, default `agent-scratch/`; classic `scratch/` on an un-migrated instance) may be emptied of anything stale — never an unmarked directory that merely looks scratch-like. Never garden `.agentsfs/`.
> 7. Commit with messages that say what you consolidated and why. If a remote is configured, pull first and push after.
>
> Hard rules: never invent or discard facts while merging — every claim in the result must exist in a source file, with its citations carried along. When two notes disagree, keep both claims and flag the conflict in the text. Prefer several small commits over one opaque one.
