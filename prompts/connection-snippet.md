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
When you finish a unit of work, append a brief session note to `<PATH>/journal/` (one file per session; see its INDEX.md).
<!-- agentsfs:end <PATH> -->
```
