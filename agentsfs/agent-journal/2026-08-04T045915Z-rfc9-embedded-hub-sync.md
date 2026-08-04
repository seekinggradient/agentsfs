---
description: Session — implemented the embedded-instance resolver, canonical Hub main publication, and Git-grade status RFC across CLI, MCP, tests, and docs.
---

## Learned / decided

- Per-instance Hub linkage cannot be represented safely by one repository-wide Git remote when several embedded AgentsFS instances share a host repository.
- Credential-free publication metadata now lives in ignored `.agentsfs/` machine state, while instance-namespaced remote-tracking refs cache each Hub `main` independently.
- Focused status compares committed instance trees and last-push provenance without running `git subtree split`; projection objects are created only by an explicit push.
- The repository-wide `hub` remote remains a compatibility alias and is never silently retargeted when another embedded instance has a different destination.
- Hub pushes retain Git semantics: only committed state is projected, every destination is `main`, remote verification gates metadata updates, and non-fast-forward history is never forced.

## Open

- Production deployment is only applicable if this CLI/core change also changes the Hub binary artifact; the documented release path for the CLI itself is commit/push and a later tagged release.
- Historical Hub feature branches are detected only when their commits are already locally available; automatic branch cleanup remains outside scope as specified by the RFC.

## Written directly

- Updated [[embedded-git-status-and-hub-sync]] implementation code, tests, bundled command help, README, setup/Hub documentation, and the current/template contract sync guidance.
