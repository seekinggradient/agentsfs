---
description: Canonical connection block that `afs setup` / `afs connect` appends to a harness's AGENTS.md / CLAUDE.md so agents learn the instance exists. Keep in sync with internal/core/register.go.
---

# Connection snippet

Harnesses bootstrap by reading `AGENTS.md` / `CLAUDE.md`; if the pointer isn't in one of those files, no agent ever learns the substrate exists. `afs setup` and `afs connect` append this block (with the user's approval). The markers carry the instance path, so multiple instances can connect through one file and re-running the command updates a block idempotently instead of duplicating it.

```markdown
<!-- agentsfs:begin <PATH> -->
## Persistent memory (agentsfs)

A durable, user-owned memory lives at `<PATH>`.
Before starting work, read `<PATH>/AGENTS.md` and orient yourself.
Consult it before re-researching anything you may already know, and record
durable knowledge there as you work, following its contract.
When `afs` is available, `afs status <PATH>` reports this instance's contract,
scoped worktree, and sync state; from a parent workspace, `afs status <search-root>`
discovers every local AgentsFS instance before multi-instance maintenance.
When you finish a unit of work, append a brief session note to `<PATH>/<JOURNAL>/` (one file per session; see its INDEX.md).
When this instance has a configured remote, pull before writing and immediately
push after every completed unit: use `afs hub push` for a Hub-linked instance
and `git push` for an ordinary remote. Do not wait for a user request or batch
completed work. If another checkout pushed first, reconcile before retrying;
never force-push.
<!-- agentsfs:end <PATH> -->
```

`<PATH>` is the instance root. `<JOURNAL>` is the instance's session journal directory, resolved at write time from the directory whose `INDEX.md` declares `agentsfs_role: journal` (the default is `agent-journal/`). A relocated or renamed journal is pointed at correctly because the path is resolved, not hard-coded — re-run `afs connect` after moving it to refresh the block.
