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

## Start and maintain an episode

At the start of every project trajectory, after orientation, create an episode even if the work changes no memory files. `afs journal begin --session <stable-trajectory-key> --description "User intent and intended work" <instance-path>` returns an ID, path, and content hash. Reuse the trajectory key on resume or context compaction; parallel trajectories use different keys. Omit the key only if the harness cannot supply one, then save and reuse the returned ID/path. Do not record secrets, raw transcripts, or internal reasoning traces.

Maintain a concise account of observable work at meaningful checkpoints: discoveries, decisions and their rationale, failed approaches, changes of direction, results, open questions, and links to artifacts or durable notes. Preserve earlier consequential events when rewriting the current body. Do not rely on a final update: interruptions are why checkpoints matter.

Use `afs journal list <instance-path>` to read the current hash. Write the complete revised body to a temporary Markdown file and run `afs journal checkpoint --id <id> --expect <hash> --body <file> <instance-path>`. A stale hash refuses the write; reread and reconcile. Record confirmed interruption with `--status interrupted`; age alone does not prove interruption. Resuming an interrupted episode uses a normal checkpoint. Finish with `afs journal finish --id <id> --expect <hash> --body <file> <instance-path>` once the trajectory's work has ended. Finished episodes are immutable. A new trajectory gets a new key.

Without the CLI, use a collision-resistant `YYYY-MM-DDTHHMMSSZ-<unique>-<slug>.md` in active/, frontmatter containing `description`, `episode_id`, `status` (running/complete/interrupted), `started`, and `checkpointed` (UTC event times), and sections such as Intent, Progress, Open, and Artifacts. Only the owning trajectory edits its running episode; never edit a finished or archived episode. Avoid journal writes while consolidation.json exists. Native file writes must use atomic replacement and reread before changing an entry another agent could have touched.

## Gardening

Only complete or explicitly interrupted episodes are eligible; preserve uncertainty and unfinished outcomes in interrupted work. Legacy flat journal entries remain readable and are treated as completed source episodes without rewriting them. Running episodes remain in active/ and available during priming. Do not mark stale work complete automatically.

Read the existing bootstrap, eligible episodes, and relevant durable notes. Fold novel durable facts into those notes, preserving sources and disagreements. Rewrite bootstrap.md within a 2,000 estimated-token target and a 3,000-token maximum (UTF-8 bytes divided by four, rounded up):

1. **Overview:** what this file is, what the workspace is about, and when consolidation last occurred.
2. **Chronology:** continuous dated phases covering the recorded lifetime. Recent work gets detail; older routine work compresses into phases. Keep old foundational decisions, turning points, abandoned approaches, and unresolved issues at useful resolution. Retain links into the archive, consulting source episodes when revising old phases to avoid cumulative summary drift.
3. **Key knowledge:** a curated set of links to authoritative notes with a short reason to read each. Do not copy their facts into a second knowledge base.

Preserve the original historical coverage while integrating new work. Compression removes detail from the bootstrap, not from the archived sources. Never invent a history when none was captured, or infer that an open question was resolved.

With the CLI: `afs journal prepare <instance-path> > plan.json`, synthesize a complete candidate bootstrap in a separate file, then `afs journal consolidate --plan plan.json --bootstrap candidate.md <instance-path>`. Keep these temporary inputs outside the journal. The plan pins the old bootstrap and exact eligible episode hashes. If anything changed, prepare again and reconcile the candidate. Consolidation saves a recovery intent, updates bootstrap, and archives the exact sources; it never silently overwrites archive files. On interruption use `afs journal recover`. Commit bootstrap and archive changes together, then sync. Repeat bounded batches if more completed work remains.

Without CLI access, use the same eligibility, source preservation, and budget rules. In a remote tool with revision-checked atomic commits, include the revised bootstrap, archived copies, and deletion of those exact active sources in one commit based on the revision you read. With plain local tools, copy sources into archive without overwriting, verify copies and the synthesis, save bootstrap, and remove only unchanged completed active files; retain a record of incorporated source paths so a retry cannot double-count. Never move a running entry.
