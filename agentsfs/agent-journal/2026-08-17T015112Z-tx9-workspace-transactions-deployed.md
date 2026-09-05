---
description: Session — reviewed, merged, pushed, and deployed the Hub's atomic multi-file workspace transaction API, then reconciled the embedded Hub projection.
---

# Atomic workspace transactions deployed

- Reviewed `codex/workspace-save-api` at `19b20a7` against current `main`; the semantic merge was clean.
- Verified the transaction contract covers one-commit manifests, update-plus-delete, exact before-image conflicts with no partial writes, invalid-manifest rejection before bootstrap, and shared authorization/scope enforcement.
- Ran formatting, diff-integrity, and the complete Go test suite on both the combined review tree and the actual merge; all packages passed.
- Merged as host commit `e0028a0`, pushed `main`, and deployed Fly production version 114 (`agentsfs-hub:deployment-01M06PBMEPH1EZX33FPB7EM70K`).
- Production smoke checks returned 200 from `/healthz` and `/`, and the new transaction route returned the expected 401 without credentials. No authenticated write was attempted against a real workspace.
- Pulled the embedded `agentsfs/` Hub projection before updating durable memory. Reconciled the Hub gardener's completed-item pruning with the host tree, retained newer ticket closure metadata and six scheduled-maintenance journals, and folded Hub tip `6ce5108` into host commit `0d99ce2`.
- Updated [[backlog/INDEX#^markdownto-save-api]] so it no longer incorrectly says the APIs are undeployed.

The remaining MarkdownTo integration parent stays open for client-side adoption and other integration work; this session completed the Hub transaction backend and its production rollout.
