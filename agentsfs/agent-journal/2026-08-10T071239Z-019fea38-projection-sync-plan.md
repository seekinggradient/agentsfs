---
description: "Session note: clarified why embedded Hub publication is translated Git rather than an ordinary remote, verified the base-aware subtree mechanism, and graduated the projection-pull backlog item into an implementation-ready ticket."
---

# Embedded projection sync plan documented

## Learned / decided

- The user confirmed the architectural explanation that made the failure legible: an embedded Hub repository is a translated projection of one host directory, not literally a Git remote of the enclosing application repository. The design must preserve shared host history, application isolation, and Hub-side writers together.
- The failure statement was narrowed: ordinary subtree splits deterministically extend host-only projection history; the missing correspondence is specifically for foreign root-level Hub commits.
- A Git-only reproduction against markdownto verified that `git subtree split --onto=<hub-base>` preserves the Hub ancestor and produces a tree identical to the host prefix, both after the manual Hub fold and across a normal host-only update.
- Chosen direction: projection-base tracking plus first-class embedded `afs hub pull`; an actual Hub pull creates honest host merge ancestry, while push uses `split --onto`. Do not add `--rejoin` bookkeeping merges after every push and do not convert embedded instances to submodules.
- Recoverability is part of the protocol: local schema-2 cache plus an append-only Hub projection ledger advanced atomically with main. Exact repository identity is always explicit or instance-local; basename guessing is removed.
- Hub writer eligibility keys off repository mode and sync protocol version. Version-1/unknown projections cannot receive Hub-created commits; version-2 projections can. This is defense-in-depth inside the completed two-way protocol, not the final fix by itself.

## Open

- Implementation has not started. The ticket gives the ordered work, migration cases, conflict semantics, and acceptance suite.
- Rule 12 likely needs a reviewed contract version bump once embedded `afs hub pull` exists; no contract text was changed in this session.
- The live markdownto Hub progressed beyond the pinned already-folded case and now has a genuine backlog conflict with host work. Both states must remain fixtures.

## Written directly

- Graduated `^hub-projection-pull` into [[hub-projection-pull]] and replaced the oversized spine line with an implementation summary.
