---
description: Connect an agentsfs to a hosted Hub and upload it, from the afs CLI or over MCP.
---

# The agentsfs Hub

The Hub is a hosted (or self-hosted) home for an agentsfs: a central place to browse all of a user's knowledge in a web view, share individual repositories, and give agents a stable URL to read and update. It stores **real git**, so `git clone` is always the exit ramp — no lock-in. It is entirely optional; a local-only agentsfs works fully without it.

The default hosted instance is `https://hub.agentsfs.ai`. Anyone can also run their own (see [self-host.md](../deploy/self-host.md)). The Hub is a *destination for `git push`*, not a new way of working: local files + git stay the source of truth.

## Connect and upload (CLI)

The user signs in once; then you can upload and list:

```sh
afs hub login              # sign in — the user creates an access token at the hub's /account page
afs hub push [name]        # link this agentsfs and git push it; run again to sync updates
afs hub list               # list all of the user's repositories on the hub
afs hub status             # show sign-in and whether this folder is linked
```

`afs hub push` adds a `hub` git remote and pushes the current branch. The saved sign-in (URL, username, token) lives in the user's config directory (`<config>/agentsfs/hub.json`, mode 0600) — never inside an agentsfs repo.

## From an agent (MCP)

The MCP server exposes the same, so a harness that can't shell out still works:

- `hub_status` — is the user signed in, and is this instance linked?
- `hub_push` — link and upload this agentsfs (after the user has run `afs hub login`).
- `hub_list` — list all of the user's hub repositories.

## Visibility

Repositories are **private by default**. A repo becomes public only when the user deliberately confirms it in the repo's **Settings** on the web (typing the slug to confirm). Once public, anyone with the link can read and `git clone` it, but only the owner can push or edit — and the user's dashboard and other repos stay private. Never make a repository public on the user's behalf.

## Accounts

On the hosted Hub, a user signs in with a username and password (self-serve signup at `/signup`). Because git has no interactive login, pushing and cloning use an **access token** the user creates on the `/account` page — like a GitHub personal access token. `afs hub login` stores that token so the CLI and agents can push without prompting.
