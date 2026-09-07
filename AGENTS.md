

<!-- agentsfs:begin ./agentsfs -->
## Persistent memory (agentsfs)

A durable, user-owned memory lives at `./agentsfs`.
Before starting work, read `./agentsfs/AGENTS.md` and orient yourself.
Consult it before re-researching anything you may already know, and record
durable knowledge there as you work, following its contract.
When `afs` is available, `afs status ./agentsfs` reports this instance's contract,
scoped worktree, and sync state; from a parent workspace, `afs status <search-root>`
discovers every local AgentsFS instance before multi-instance maintenance.
When you finish a unit of work, append a brief session note to `./agentsfs/agent-journal/` (one file per session; see its INDEX.md).
When this instance has a configured remote, pull before writing and immediately
push after every completed unit: use `afs hub push` for a Hub-linked instance
and `git push` for an ordinary remote. Do not wait for a user request or batch
completed work. If another checkout pushed first, reconcile before retrying;
never force-push.
<!-- agentsfs:end ./agentsfs -->
