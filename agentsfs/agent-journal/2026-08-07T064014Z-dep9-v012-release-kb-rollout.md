---
description: "Session — released CLI v0.12.0 (GoReleaser, 5 assets), published this meta-instance to production Hub for the first time, and rolled contract 0.10.0 out to nine Hub KBs (three via existing checkouts incl. agentic-stocks' hand-ported routing adaptations; six via fresh clones, three of those with additive adaptations ported); aa-synced-vault skipped for owner review."
---

# v0.12.0 release + contract 0.10.0 fleet rollout

## Learned / decided

- **Released v0.12.0** (tag → GoReleaser, 1m37s, 5 assets). Note the tag trap: `git tag -l | tail` sorts lexically and hid v0.10.0/v0.11.x — v0.11.1 was already released; use `--sort=version:refname`.
- **This instance is now on production Hub** (seekinggradient/agentsfs). Its `.agentsfs/hub.json` had been captured by a local demo hub from the feature-demo session; deleting the machine-state file and re-pushing relinked it cleanly — the rebuildable-machine-territory rule doing its job.
- **Contract 0.10.0 rolled out to nine KBs** (all pushed, only AGENTS.md + backlog.md touched, no force-pushes): agentic-stocks, kauai-2026, production-agent-research, openclaw-sgbot-main, openclaw-sgbot-family-bot, seekinggradient-hq, boswell-v2, myexpert-eve-development, ai-engineer-2026. Customized ones kept their adaptations by porting additive blocks onto new stock (agentic-stocks' routing section, hq's orchestration conventions, boswell-v2's expert-factory block, ai-engineer-2026's conference framing).
- **aa-synced-vault deliberately not upgraded**: its adaptations rewrite stock prose (Orient step 3, rules 9/10, the whole Structure section — Journal/-privacy guardrails) and collide with 0.10.0's own Structure rewrite; parked as [[backlog#^vault-contract-port]] with its duplicate-journal-role problem.
- **New variant evidence**: three KBs carried the pre-4ac5230 0.9.0 text; equality checks handle it (vendored variant) but `afs contract diff` baselines against canonical stock, producing two phantom "modified" lines — backlogged as a diff-baseline fix.

## Open

- Tracked in [[backlog]] (vault port, diff baseline, harness-plugins decision).

## Written directly

- [[backlog]] items above; hub-mcp deploy checked off earlier today (verified live).
