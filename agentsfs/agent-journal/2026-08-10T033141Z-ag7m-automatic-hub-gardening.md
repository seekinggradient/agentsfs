---
description: Session — automatic Hub gardening implemented and tested; production secrets, deploy, and first-run verification remain.
---

## Learned / decided

- Automatic gardening defaults on for accounts and repositories that have never saved an explicit preference; “only recently pushed repositories” also defaults on with a seven-day window. Explicit account and repository opt-outs remain authoritative.
- “Recently pushed” is based on an actual successful Hub ref update recorded after `git-receive-pack`, not a commit's authored timestamp. Repositories predating the marker fall back to their freshest HEAD commit until their next push. Gardening's own API commits therefore do not keep a dormant repository active forever.
- Hub dispatch issues a random one-hour capability scoped to one owner repository and one deterministic daily maintenance thread, with at most 32 commit attempts. It cannot enumerate the account, access another thread, create a repository, access usage, delete files, write non-Markdown paths, touch `.agentsfs/`, or rewrite `AGENTS.md` through the model-facing commit path.
- Contract upgrades use a separate deterministic Hub action. It upgrades only a recognized stock contract, refuses customized, unknown, and newer contracts, and can perform contract-owned migrations such as the lossless 0.10 page backlog to 0.11 backlog-directory move.
- Eve runs daily at 10:00 UTC through Vercel cron. It creates a visible `Automatic gardening · <repo>` thread, upgrades the stock contract, works from deterministic `afs doctor` findings, uses a maintenance-only write profile, and reruns doctor. Duplicate cron delivery is idempotent per repository/day.
- Automatic model-driven deletion remains unavailable. Journal facts can be folded into durable notes, but the source journal entry stays until a human-authorized or interactive gardener removes it.
- Maintenance authority is carried only on the current signed turn. Opening or continuing the visible transcript later as an ordinary user does not inherit maintenance powers.
- Hub implementation commit: `b5b331a` on `codex/auto-garden-settings`. Eve implementation commit: `f287cf9` on `codex/auto-garden-cron`.
- Verification passed: the complete Hub package suite; Eve's complete 592-test suite before the final auth tightening; final focused scheduler/auth tests (37), TypeScript typecheck, root and `/agent` Next production builds, and the compiled Eve agent manifest.

## Open

- Configure the same strong `HUB_MAINTENANCE_SECRET` on Hub and Eve, ensure Vercel supplies `CRON_SECRET`, deploy both branches, and verify one production cron pass plus its maintenance transcript and commits.
- Decide later whether to authorize a narrowly governed journal-cleanup deletion workflow; it is intentionally not part of this unattended release.

## Written directly

- Updated [[backlog/INDEX#^automatic-hub-gardening]] with completed implementation/configuration work and the remaining deployment gate.
- Updated hosted-agent and Eve staging/README deployment documentation with the two new secrets and scoped-grant behavior.
