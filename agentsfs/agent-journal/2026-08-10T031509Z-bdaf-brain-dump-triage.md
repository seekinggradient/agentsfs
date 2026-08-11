---
description: Session — mapped Akshay's morning AgentsFS ideas to shipped backlog v2/save work and added the two genuinely new Hub and gardening intentions.
---

## Learned / decided

- The desired atomic curl/API write path is already implemented by the Hub save API with source-hash conflict protection; it is not a new backlog item.
- The proposed backlog directory has already shipped in contract 0.11.0: a spine, earned ticket files, delegated sub-backlogs, and an archive collection. The append-only JSONL archive was explicitly rejected in [[backlog-directories]] because it would make source history worse for humans, links, and search; yearly Markdown rollups provide the derived done view.
- A document-scoped keyboard/inline command that asks an agent to operate on the current file is genuinely new.
- A recurring, fleet-aware gardener that performs real consolidation and contract maintenance safely is genuinely new. The current contract defines gardener responsibilities but does not provide this continual cross-instance operating loop.
- Rule 13 already establishes backlog ownership, but Akshay wants a stronger adoption loop: every agent should treat owner requests, discovered future work, evolving understanding, and explicit closure as ongoing backlog synchronization duties. That reinforcement across every agent-facing surface is a distinct task.
- Eve/voice quality work belongs to the MyExpertEve host backlog rather than this project backlog.

## Open

- The inline-command ticket still needs a product design for selection/context scope, execution authority, and approve-versus-auto-apply behavior.
- The gardener needs an explicit policy boundary between safe automatic hygiene and changes that require owner review.

## Written directly

- Added `^backlog-synchronization-discipline`, `^hub-inline-agent-command`, and `^continual-fleet-gardener` to [[backlog/INDEX]].
