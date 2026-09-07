

<!-- agentsfs:begin ./agentsfs -->
## Persistent memory (agentsfs)

A durable, user-owned memory lives at `./agentsfs`.
Before starting work, read `./agentsfs/AGENTS.md` and orient yourself.
Consult it before re-researching anything you may already know, and record
durable knowledge there as you work, following its contract.
When `afs` is available, `afs status ./agentsfs` reports this instance's contract,
scoped worktree, and sync state; from a parent workspace, `afs status <search-root>`
discovers every local AgentsFS instance before multi-instance maintenance.
Start or resume an episode before work and checkpoint meaningful progress throughout the trajectory; follow the journal INDEX.md for lifecycle and archival rules.
When this instance has a configured remote, pull before writing and immediately
push after every completed unit: use `afs hub push` for a Hub-linked instance
and `git push` for an ordinary remote. Do not wait for a user request or batch
completed work. If another checkout pushed first, reconcile before retrying;
never force-push.
<!-- agentsfs:end ./agentsfs -->
