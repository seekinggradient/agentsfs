---
description: Episodic journal — live work, archived episodes, and a bounded workspace history for arriving agents.
agentsfs_role: journal
---

# Episodic journal

The journal role belongs to this directory regardless of its name. Resolve it with `afs roles`; do not create another journal or use a personal diary.

- `bootstrap.md` — the gardener's bounded synthesis of the workspace's lifetime. Read it at startup together with recent unconsolidated episodes (`afs prime <instance-path>`).
- `active/` — unconsolidated episodes, including work still running.
- `archive/` — immutable source episodes already incorporated into the bootstrap. Preserve them as readable files, not just Git history.
- `consolidation.json` — a temporary, recoverable consolidation intent if a gardening operation is interrupted. Run `afs journal recover <instance-path>` before further journal writes; never delete it to bypass a conflict.

## One entry per conversation

A session is the entire conversation/thread, not a turn, task, tool call, commit, or focus switch. Keep at most one episode per conversation per workspace. Reuse it across follow-up messages, resumed work, and context compaction. Parallel independent conversations use different keys; subtasks in the same conversation do not create extra entries.

Create an entry only when there is something worth remembering. Use `afs journal begin --session <stable-conversation-key> --description "What this conversation is about" <instance-path>` and keep its returned ID/path. The key must identify the conversation, never a turn or individual task. Without a harness key, save and reuse the returned ID/path rather than calling begin without a key again.

Write what a future agent should retain: lessons, discoveries, decisions and their reasons, useful unresolved context, and links to authoritative notes or artifacts. Revise the same entry when new lasting knowledge emerges or a handoff needs context. Skip routine actions, tool-call inventories, repeated status, and a full recounting of what you did. Do not record secrets, transcripts, or internal reasoning traces. There is no per-turn checkpoint requirement or fixed length target; preserve useful knowledge without duplicating it from durable notes.

Use `afs journal list <instance-path>` to read the current hash. Write the revised body to a temporary Markdown file, then run `afs journal checkpoint --id <id> --expect <hash> --body <file> <instance-path>`. A stale hash refuses the write; reread and reconcile. Keep consequential learning when revising; replace obsolete status rather than accumulating progress logs.

A final reply or completed subtask does not close the conversation. Leave its episode running for follow-ups, including when switching workspace focus. Use `afs journal finish --id <id> --expect <hash> --body <file> <instance-path>` only when the conversation is explicitly closed. Record `--status interrupted` only for a confirmed abandoned session, not a failed turn or a pause; age alone does not prove abandonment. Finished episodes are immutable. If no explicit closure is available, leave the entry running rather than guessing. Never create a replacement just to record a routine follow-up.

Without the CLI, use one collision-resistant `YYYY-MM-DDTHHMMSSZ-<unique>-<slug>.md` in active/, with frontmatter containing `description`, `episode_id`, `status` (running/complete/interrupted), `started`, and `checkpointed` (UTC event times). Organize the body around learning, decisions, and context worth preserving; a chronological action log is unnecessary. Only the owning conversation edits its running entry; never edit finished or archived entries. Avoid writes while consolidation.json exists. Use atomic replacement and reread before changing an entry another agent could have touched.

## Gardening

Only complete or explicitly interrupted episodes are eligible; preserve uncertainty and unfinished outcomes in interrupted work. Legacy flat journal entries remain readable and are treated as completed source episodes without rewriting them. Running episodes remain in active/ and available during priming. Do not mark stale work complete automatically.

## First bootstrap or missing bootstrap

Resolve any pending consolidation.json recovery before initializing or rebuilding bootstrap; never replace files to bypass a recovery conflict. If bootstrap.md is missing, distinguish first use from lost history. Inspect archive/ and Git history before declaring that no episodes were consolidated. If archives or a prior bootstrap exist, restore or rebuild the bounded synthesis from those recorded sources; leave archive files unchanged and label any incomplete coverage. For a genuinely new journal, journal preparation supplies a starter with description frontmatter and exact Overview, Chronology, and Key knowledge headings. Use the workspace root INDEX.md for its purpose, then synthesize only eligible recorded episodes; running work remains unconsolidated. On the first bounded batch, label the historical coverage as partial until remaining eligible entries have been processed. If there are no eligible episodes, keep a truthful starter with no invented phases and links to existing authoritative notes. Local prepare saves a missing starter; Hub prepare is read-only and returns bootstrap_missing. When that is true and no episodes are eligible, create the missing starter through a normal revision-checked write, after verifying it is still absent; do not call consolidation with an empty plan. Recovery of a missing synthesis without eligible episodes likewise uses a revision-checked write. When eligible episodes exist, publish the synthesis and their archives together through consolidation.

## Consolidation content

Read the existing bootstrap, eligible episodes, and relevant durable notes. Fold novel durable facts into those notes, preserving sources and disagreements. Rewrite bootstrap.md within a 2,000 estimated-token target and a 3,000-token maximum (UTF-8 bytes divided by four, rounded up):

1. **Overview:** what this file is, what the workspace is about, and when consolidation last occurred.
2. **Chronology:** continuous dated phases covering the recorded lifetime. Recent work gets detail; older routine work compresses into phases. Keep old foundational decisions, turning points, abandoned approaches, and unresolved issues at useful resolution. Retain links into the archive, consulting source episodes when revising old phases to avoid cumulative summary drift.
3. **Key knowledge:** a curated set of links to authoritative notes with a short reason to read each. Do not copy their facts into a second knowledge base.

Preserve the original historical coverage while integrating new work. Compression removes detail from the bootstrap, not from the archived sources. Never invent a history when none was captured, or infer that an open question was resolved.

With the CLI: `afs journal prepare <instance-path> > plan.json`, synthesize a complete candidate bootstrap in a separate file, then `afs journal consolidate --plan plan.json --bootstrap candidate.md <instance-path>`. Keep these temporary inputs outside the journal. The plan pins the old bootstrap and exact eligible episode hashes. If anything changed, prepare again and reconcile the candidate. Consolidation saves a recovery intent, updates bootstrap, and archives the exact sources; it never silently overwrites archive files. On interruption use `afs journal recover`. Commit bootstrap and archive changes together, then sync. Repeat bounded batches if more completed work remains.

Without CLI access, use the same eligibility, source preservation, and budget rules. In a remote tool with revision-checked atomic commits, include the revised bootstrap, archived copies, and deletion of those exact active sources in one commit based on the revision you read. With plain local tools, copy sources into archive without overwriting, verify copies and the synthesis, save bootstrap, and remove only unchanged completed active files; retain a record of incorporated source paths so a retry cannot double-count. Never move a running entry.
