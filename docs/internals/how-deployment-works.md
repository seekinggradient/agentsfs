---
description: How CLI, Hub, Eve, and website changes reach production, including which deployments are independent and which contracts connect them.
---

# How deployment works

AgentsFS has several independently released surfaces. The open-source CLI and Hub live in this repository; Eve lives in the separate `agentsfs-eve` repository; the marketing site has its own project. Workspace contents move through ordinary git and are not bundled into any application deployment.

| Surface | What runs there | How code ships | Automatic after the command? |
|---|---|---|---|
| **afs CLI** (laptops, servers, CI) | the `afs` binary and bundled contract/docs | tag a release: `git tag vX.Y.Z && git push origin vX.Y.Z` | **Yes.** GitHub Actions/GoReleaser publishes binaries; the installer and `afs update` consume them. |
| **AgentsFS Hub** (`hub.agentsfs.ai`) | accounts, sessions, git/LFS storage, repo UI, permissions, thread records, metering, and `/api/agent/v1/*` | run `fly deploy` from this repository | **No.** A Hub operator initiates the Fly deployment. |
| **Eve agent** (`agentsfs-eve`) | the `/agent` UI, durable Eve workflow, tools, approvals, review mode, and realtime voice bridge | run `vercel deploy --prod` from the `agentsfs-eve` repository | **Yes.** Vercel creates an immutable deployment and moves the project's production alias. |
| **Landing site** (`agentsfs.ai`) | static marketing site | run that project's deployment command | **No.** It is a separate project. |

## Production Hub and Eve

Production Hub is configured with:

- `HUB_EVE_AGENT_URL=https://agentsfs-eve-staging.vercel.app`
- `HUB_EVE_AGENT_SECRET` set to the matching HMAC secret in the Eve project

Despite the Vercel project's historical `-staging` name, its production alias is the Eve upstream used by the production Hub. New `/agent/*` requests are reverse-proxied to whichever immutable Vercel deployment currently owns that alias.

That makes the normal Eve release path deliberately independent:

1. Test and commit in `agentsfs-eve`.
2. Push the desired commit.
3. Run `vercel deploy --prod` from that repository.
4. Smoke the immutable deployment and the stable production alias.

No `fly deploy` is required for an Eve-only UI, prompt, tool, workflow, or voice change. The Hub already points at the stable Vercel alias and starts using the new deployment as soon as Vercel promotes it. Existing browser tabs may need a refresh to load new client assets; an already-running durable turn can finish on the deployment that started it.

Run `fly deploy` when the Hub itself changes: login/session handling, the `/agent/*` reverse proxy, signed identity handoff, PAT injection, `/api/agent/v1/*`, git storage, thread storage, permissions, or Hub UI. A change spanning the Hub–Eve contract needs coordinated, backward-compatible releases: deploy the additive Hub side first, deploy Eve second, verify through `hub.agentsfs.ai/agent/`, and remove old compatibility only in a later release.

## Other release rules

- **CLI or contract change:** bump the version and tag a release. `afs update` updates the binary, but contract text is deliberately instance-scoped; use `afs contract upgrade <instance-path>`, commit that workspace change, and push it through git.
- **Hub change:** run the Hub tests, then `fly deploy` from this repository.
- **Eve change:** run the Eve typecheck, tests, and production builds, then `vercel deploy --prod` from `agentsfs-eve`.
- **Landing-site change:** deploy from the landing site's own repository.

The workspaces are the constant data plane: plain git repositories stored by the Hub and cloned or pulled by any client. Application releases never rewrite them.

## Legacy Sprite fallback

The Hub still contains the earlier per-user Fly Sprite implementation as a fallback when `HUB_EVE_AGENT_URL` is unset. It can provision an embedded `agentsfs-chat` bundle and cloned working directories, but that is **not the current production agent topology or release path**. Do not rebuild bundles or reprovision Sprites for an Eve release. Sprite-specific procedures should be treated as legacy/fallback operations unless production is intentionally rolled back by unsetting `HUB_EVE_AGENT_URL` and redeploying the Hub.
