---
description: Connect an agentsfs to a hosted Hub and upload it, from the afs CLI or over MCP.
---

# The agentsfs Hub

The Hub is a hosted (or self-hosted) home for an agentsfs: a central place to browse all of a user's knowledge in a web view, share individual repositories, and give agents a stable URL to read and update. It stores **real git** plus standard Git LFS objects for large media, so `git clone` is always the exit ramp — no lock-in. It is entirely optional; a local-only agentsfs works fully without it.

The default hosted instance is `https://hub.agentsfs.ai`. Anyone can also run their own (see [self-host.md](../deploy/self-host.md)). The Hub is a *destination for `git push`*, not a new way of working: local files + git stay the source of truth.

## Connect and upload (CLI)

The user signs in once; then you can upload and list:

```sh
afs hub login              # sign in — the user creates an access token at the hub's /account page
afs hub push [name]        # link this agentsfs and git push it; run again to sync updates
afs hub pull <name> [dir]  # download a knowledgebase into the current directory; run again to update
afs hub pull <name> --merge # fold a knowledgebase into the current instance (combine bases)
afs hub list               # list owned repositories and knowledge bases shared with you
afs hub status             # show sign-in and whether this folder is linked
```

`afs hub push` adds a `hub` git remote and pushes the current branch. The saved sign-in (URL, username, token) lives in the user's config directory (`<config>/agentsfs/hub.json`, mode 0600) — never inside an agentsfs repo. `afs hub login` also installs an AFS-backed Git credential helper, so ordinary `git fetch`, `git pull`, and `git push` against a Hub remote authenticate using that config without requiring a Hub-specific command or storing the token in `.git/config`.

`afs hub pull` is the inverse: it clones a repo into the current directory so a knowledgebase is easy to get wherever you are. `<name>` is one of the signed-in user's repos (`<slug>`) or another account's (`<user>/<slug>`); `dir` defaults to `./<slug>`. Re-running it updates an existing checkout (a fast-forward `git pull`). It authenticates private repos with the saved token via a one-shot header, so the token is never written into the cloned repo. Pulled checkouts also get a clean `hub` remote, so shared write collaborators can run `afs hub status` and `afs hub push` safely against the owner's repo.

`afs hub list` inventories repositories owned by the signed-in user plus knowledge bases shared with them, showing the owner and access role for shared entries. `afs status <search-root>` is the complementary local view: it discovers checkouts on this machine and reports their contract, worktree, sync, health, and duplicate-checkout state.

Pass `--merge` to *combine* knowledgebases: the repo is cloned and then its `.git` is dropped, so its notes become plain files of the surrounding instance rather than a nested repo. Commit them and they become part of this instance (and push with it). This is how you build one "mega" agentsfs out of several. Without `--merge`, a pulled repo keeps its own `.git` and stays independent — the parent's `afs tree`/`search`/`reindex` treat a nested repo as a separate knowledgebase and don't fold it in.

## Large files and Git LFS

AgentsFS instances can hold any file type. When `git-lfs` is installed, `afs init` includes `.gitattributes` rules that route common large media through Git LFS: images, PDFs, video, audio, archives, and related binary formats. The Hub implements the standard Git LFS Batch API, so `git push`, `afs hub push`, `git clone`, and `afs hub pull` transfer those objects normally with no Hub-specific command.

On the current Fly deployment, LFS objects live on the same persistent volume as the bare git repos. Future R2/object-storage support can replace that backend without changing the git/LFS client workflow.

If `git-lfs` is missing locally, agentsfs still works; media files are just ordinary git blobs. The Hub does not rewrite already-committed blobs into LFS automatically, because that would rewrite git history.

## From an agent (MCP)

The MCP server exposes the same, so a harness that can't shell out still works:

- `status` — discover local AgentsFS instances beneath supplied roots and return structured scope/completeness, contract, git, sync, optional doctor, and duplicate-checkout status; local-only unless `fetch: true`. Retry partial scopes with narrower roots.
- `hub_status` — is the user signed in, and is this instance linked?
- `hub_push` — link and upload this agentsfs (after the user has run `afs hub login`).
- `hub_pull` — download a knowledgebase from the hub into the local filesystem.
- `hub_list` — list all visible hub repositories, including knowledge bases shared with the user.

## Visibility

Repositories are **private by default**. A repo becomes public only when the user deliberately confirms it in the repo's **Settings** on the web (typing the slug to confirm). Once public, anyone with the link can read and `git clone` it, but only the owner can push or edit — and the user's dashboard and other repos stay private. Never make a repository public on the user's behalf.

## Accounts

On the hosted Hub, a user signs in with a username and password (self-serve signup at `/signup`). Because git has no interactive login, pushing and cloning use an **access token** the user creates on the `/account` page — like a GitHub personal access token. `afs hub login` stores that token and configures Git to read it through AFS, so the CLI and agents can push without prompting.

## Talk to your agent

Sign in, then open `/agent/` (or click **Talk to an agent** on a repo page, which pre-focuses that repo). The Hub spins up your own private agent — a hardware-isolated sandbox that clones all of your Hub repos and can read, search, edit, and commit across them; every change is a real git commit pushed back, so `git clone`/`git pull` stay the exit ramp. It boots by listing your knowledgebases and asking which to work in; you can switch at any time.

This is **optional** — it only appears when the operator has enabled the agent feature. Model calls run *through the Hub*, so no LLM keys ever live on the agent.
