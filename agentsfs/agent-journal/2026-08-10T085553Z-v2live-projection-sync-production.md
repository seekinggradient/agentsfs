---
description: Production completion record for embedded projection protocol v2, AFS 0.13.1, and the linked-checkout reconciliation.
---

# Embedded projection v2 is live

Shipped AFS 0.13.0 and contract 0.12.0 with recoverable embedded projection ledgers, mapped `afs hub pull`, exact snapshot-parent pushes, strict target identity, and Hub writer gating. Migrated the `agentsfs`, `markdownto`, and `production-agent-research` projections without force-pushing; each preserves its prior gardener tip, exactly matches its host prefix tree, and passes production pull/push acceptance.

The rollout found that a repeated byte-identical push still appended an identical ledger commit. AFS 0.13.1 reuses the ledger tip when the complete host↔Hub correspondence is unchanged. Unit, full-suite, vet, release, Fly health, and production ref checks passed; a repeat push left AgentsFS Hub main at `0ec58a8…` and the ledger at `715daa5…`.

Reconciled clean real Hub-backed checkouts with ordinary fast-forwards, preserved unrelated dirty worktrees, merged the skills GitHub/Hub histories, and retained the byte-identical agentic-stocks dirty state in a named stash before fast-forwarding. The audit caught one live basename-guess casualty: the AI Engineer 2026 checkout pointed at the unrelated `agentsfs` Hub repo. It was matched to `ai-engineer-2026`, both real histories were merged, and the remote was repaired without writing anything to the wrong repo. Detailed invariants, migration evidence, SHAs, and rejected designs live in [[backlog/hub-projection-pull]].
