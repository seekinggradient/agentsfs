---
description: How a change travels from the repo to laptops, the Hub, sprites, and the landing site — which paths are automatic, which need a manual step.
---

# How deployment works — the four surfaces

Everything starts as a commit on `main` in the agentsfs repo. From there, four *separately shipped* surfaces run code, and one data plane (your KBs) moves independently of all of them.

| Surface | What runs there | How code ships | Automatic? |
|---|---|---|---|
| **afs CLI** (laptops, servers, CI) | the `afs` binary | `git tag vX.Y.Z && git push origin vX.Y.Z` → Actions/GoReleaser builds binaries → curl installer / `afs update` | **Yes** after the tag: every install sees the once-daily update nudge (fires on `doctor` too) and `afs update` pulls the release binary |
| **The Hub** (one Fly machine, `hub.agentsfs.ai`) | web app, git server, LLM proxy, accounts — plus the *embedded* agent bundle and a fallback `afs-linux` | `fly deploy` from a repo checkout | **No** — someone runs the deploy |
| **Sprites** (one Firecracker VM per user) | the agent (`agentsfs-chat` bundle) + `afs` + clones of the user's KBs | the Hub pushes the bundle + boot script at **provision**; the boot script then reinstalls `afs` from the latest GitHub release **on every cold-wake** | **Half** — `afs` freshens itself on wake; the *agent bundle and boot script* are frozen at provision time, so bundle changes reach a sprite only via re-provision (or a future hub-pushed refresh) |
| **Landing site** (`agentsfs.ai`) | static marketing site | `npm run deploy` (Cloudflare Workers), from its own project | **No** |

Rules of thumb:

- **CLI or contract change** → cut a release (bump `buildinfo.Version`, tag). Nothing else to do for the binary; the fleet converges on its own. Use `afs status <search-root>` (commonly `afs status ~`) to inventory local instances and identify outdated/customized contracts. Contract text then reaches each distinct *instance* via `afs contract upgrade <path>` (gardener-driven, never silent) — it rides inside the KB's git history, not any deploy. Upgrade one checkout per remote-backed knowledge base, push it, and let duplicate checkouts pull the commit.
- **Hub change** (web UI, collaborators, accounts, proxy) → `fly deploy`. Live the moment it finishes.
- **Agent behavior change** (chat tools, voice, prompts) → rebuild the bundle, embed it in the Hub, `fly deploy`, then each sprite needs a (re)provision to pick it up. Deleting a sprite wipes its disk — including saved agent conversations — the KBs themselves are safe (they live on the Hub; the sprite only holds clones).
- **Landing change** → `npm run deploy` in the landing project.

The KBs are the constant: plain git push/pull between laptop ⇄ Hub ⇄ sprite, unaffected by every row above.
