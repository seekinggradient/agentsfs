---
description: Connect an agentsfs to a hosted Hub and upload it, from the afs CLI or over MCP.
---

# The agentsfs Hub

The Hub is a hosted (or self-hosted) home for an agentsfs: a central place to browse all of a user's knowledge in a web view, share individual repositories, and give agents a stable URL to read and update. It stores **real git** plus standard Git LFS objects for large media, so `git clone` is always the exit ramp — no lock-in. It is entirely optional; a local-only agentsfs works fully without it.

The default hosted instance is `https://hub.agentsfs.ai`. AgentsFS itself is open source, so anyone can also run their own Hub instead of using the hosted one — the self-hosting guide lives in the repo at `deploy/self-host.md` (also readable at `https://github.com/seekinggradient/agentsfs/blob/main/deploy/self-host.md`). The Hub is a *destination for `git push`*, not a new way of working: local files + git stay the source of truth.

## Connect and upload (CLI)

The user signs in once; then you can upload and list:

```sh
afs hub login              # sign in — the user creates an access token at the hub's /account page
afs hub push [name] [--instance PATH]
                            # publish committed state to Hub main; repeat to sync
afs hub pull [owner/name] --instance PATH
                            # integrate Hub commits into an embedded projection
afs hub pull --continue     # finish after resolving/staging projection conflicts
afs hub pull --abort        # restore the pre-pull host tree
afs hub pull <name> [dir]  # download a knowledge base into the current directory; run again to update
afs hub pull <name> --merge # fold a knowledge base into the current instance (combine bases)
afs hub list               # list owned repositories and knowledge bases shared with you
afs hub status [--instance PATH] [--fetch] [--json]
                            # show focused publication status
```

`afs hub push` always publishes to the Hub repository's `main` branch. The host repository may be on `main`, a feature branch, or a detached commit; that source is reported as provenance but never chooses the Hub destination. Pushes are ordinary non-forced updates. A non-fast-forward stops without changing Hub history and tells you to run the projection-aware pull rather than overwriting anything.

The saved sign-in (URL, username, token) lives in the user's config directory (`<config>/agentsfs/hub.json`, mode 0600) — never inside an agentsfs repo. Each linked instance also has credential-free, rebuildable publication metadata under its ignored `.agentsfs/` machine state. An embedded projection additionally stores an append-only correspondence record at `refs/agentsfs/projection` on the Hub. Main and that ledger advance atomically, so deleting machine state loses only a cache: pass the exact `owner/repo` and AFS recovers the last host/Hub base. It never guesses from the instance folder or the enclosing repository's `hub` remote. `afs hub login` installs an AFS-backed Git credential helper for compatible ordinary Git operations without copying the token into `.git/config`.

Uploads are scoped to the AgentsFS root. A standalone instance pushes its current commit directly. An embedded/shared instance inside an application repository publishes an AgentsFS-only snapshot commit: its tree is verified equal to the committed instance prefix, and its parent is the exact fetched Hub tip already integrated by pull. Files elsewhere in the host repository are therefore neither selected nor uploaded. The snapshot and recoverable ledger are pushed atomically. Push publishes commits, not the worktree: staged, unstaged, untracked, or conflicted files are excluded and reported explicitly. Commit them and push again when they are ready.

From a host project root—or any directory inside it—Hub commands resolve the unique embedded instance automatically. If the project contains more than one, selection is a hard error; pass `--instance PATH`. Implicit resolution stays inside the enclosing Git worktree, does not follow symlinks, and does not enter nested repositories.

For an embedded instance, use `afs hub pull` before editing and `afs hub push` after committing. Plain `git pull` and `git push hub HEAD` address repository history, not the directory translation, and can either fail with unrelated histories or send the enclosing application repository instead of the knowledge base.

`afs hub pull` has two deliberately distinct modes. With `<name> [dir]`, it clones a standalone repo into the current directory; re-running it updates that checkout with a fast-forward Git pull. With a linked embedded instance (no clone directory, selected by context or `--instance PATH`), it fetches Hub main, maps the last published Hub tree and the new Hub tree under the host prefix, and performs a real three-way content merge against the current host tree. A clean integration becomes one folded host commit carrying `agentsfs-hub-base`, `agentsfs-hub-tip`, and exact-repository trailers; overlapping edits remain ordinary Git conflicts. Resolve and stage them, then run `afs hub pull --continue`, or use `--abort` to restore the pre-pull tree. The next push creates an exact snapshot whose parent is that recorded Hub tip, so Hub-side gardening/editor/API commits remain in the connected Hub history.

For legacy projections, a valid schema-1 local base is promoted to the recoverable ledger before pull. A Hub tip already folded into host ancestry is recognized without duplicating content. If both metadata and ledger are missing, an explicit `--adopt` is accepted only when the committed prefix tree is byte-identical to Hub main; otherwise AFS stops rather than inventing a base.

`afs hub status` and focused `afs status` share one publication model. They distinguish scoped worktree changes, host-branch upstream state, and Hub `main` publication state. Status is local by default and labels remote data as cached; pass `--fetch` for a current Hub comparison. `afs status --all <search-root>` is the complementary fleet view for discovery, contract health, and duplicate checkouts.

The Hub classifies repositories as standalone, legacy embedded projection, or protocol-v2 embedded projection. All commits originating on the Hub—automatic gardening, browser editing, hosted agent/MCP writes, board writeback, save API, and safe contract upgrades—pass through one policy gate. Standalone and v2 projection repositories remain writable. A known v1 projection is read-only on the Hub until an updated client publishes its protocol marker and performs the first projection-aware pull; Git smart-HTTP remains available for that non-forced migration.

`--merge` folds a pulled repo's files into the current instance (or into `[dir]`, if given) instead of nesting them in `./<slug>/`, and it never overwrites:

| Remote file | What happens |
|---|---|
| Doesn't exist locally | Added in place |
| Byte-identical to the local copy | Skipped |
| Differs from the local copy | Written aside into the instance's scratch role — `hub-merge-<slug>/` beneath it — and reported |
| Is a symlink | Refused; copying one would materialize its target as real content instead of a link |

The quarantine directory is `agent-scratch/hub-merge-<slug>/` on a current instance and `scratch/hub-merge-<slug>/` on an older un-upgraded one, so run `afs roles --json` for the exact path rather than assuming either name. Reconcile the two copies by hand, then delete the folder.

Nothing of the remote's own machinery comes across: its `.git` (history, and any embedded token) and its `.agentsfs` (a derived index) are both left behind, and the local instance's `.agentsfs/` is never touched. Commit the folded files and they are part of this instance — this is how you build one "mega" agentsfs out of several. Without `--merge`, a pulled repo keeps its own `.git` in a nested directory and stays independent; the parent's `afs tree`/`search`/`reindex` treat it as a separate knowledge base and don't fold it in.

## Writing in the browser

Open a note and choose **Edit** to write with familiar formatting controls, headings, checklists, tables, links, and images. Type `/` on an empty line to insert a block, use the outline to move between sections, or enter focus mode. **Markdown** opens the source editor, including document properties. Notes with advanced syntax such as HTML or math open in source mode to keep that content intact.

Typing saves a recovery draft on the current browser. A draft is not yet shared, does not create commits, and does not sync to another device. Reopening the note offers to restore it; **Download draft** exports the full Markdown at any time. If browser storage is unavailable, the editor says so.

Choose **Save version** (or press Cmd/Ctrl+S) to review the changes and describe what changed. Confirming creates one ordinary Git commit, authored by you, and makes the updated note available to everyone with access. The suggested description is editable. Existing local checkouts receive the version through `afs hub pull`.

If another writer changes a different file, both changes are preserved. If the same note changes, the editor keeps your draft and opens the latest version beside it. Combine the changes, acknowledge your review, then save a new version. Retrying a completed save does not create a duplicate commit. The editor enforces the same collaborator permissions and projection upgrade policy as other Hub writes.

## Large files and Git LFS

AgentsFS instances can hold any file type. When `git-lfs` is installed, `afs init` includes `.gitattributes` rules that route common large media through Git LFS: images, PDFs, video, audio, archives, and related binary formats. The Hub implements the standard Git LFS Batch API, so `git push`, `afs hub push`, `git clone`, and `afs hub pull` transfer those objects normally with no Hub-specific command.

On the current Fly deployment, LFS objects live on the same persistent volume as the bare git repos. Future R2/object-storage support can replace that backend without changing the git/LFS client workflow.

If `git-lfs` is missing locally, agentsfs still works; media files are just ordinary git blobs. The Hub does not rewrite already-committed blobs into LFS automatically, because that would rewrite git history.

## From an agent (MCP)

There are two separate MCP servers here, with different tool sets — a harness built against one cannot assume the other behaves the same way.

The local `afs mcp` server (stdio, run from a workspace) bundles four Hub-aware tools among its twelve total:

- `hub_status` — is the user signed in, and is this instance linked?
- `hub_push` — link and upload this agentsfs (after the user has run `afs hub login`).
- `hub_pull` — download a standalone knowledge base, or set `projection` to integrate Hub commits into an embedded instance with `continue`/`abort` conflict handling.
- `hub_list` — list all visible hub repositories, including knowledge bases shared with the user.

Its other eight tools — `status`, `tree`, `search`, `doctor`, `roles`, `backlinks`, `rename`, `docs` — work on whatever local instance the harness points it at, whether or not that instance is Hub-linked. `status` discovers local AgentsFS instances beneath supplied roots and returns structured scope/completeness, contract, git, sync, optional doctor, and duplicate-checkout status; it stays local-only unless called with `fetch: true`.

The Hub also runs its own remote MCP server directly at `hub.agentsfs.ai/mcp`, for connecting apps that can't shell out to a local binary at all — ChatGPT, claude.ai, Claude Desktop. That endpoint is OAuth-protected and exposes a different, smaller tool set built for that use case: `search`, `fetch`, `list_kbs`, `tree`, `docs`, and (only on a connection granted write scope) `write`. The two servers are not interchangeable — see [mcp.md](mcp.md). Connection steps for Claude, ChatGPT, and header-auth clients are there too.

## Visibility

Repositories are **private by default**. A repo becomes public only when the user deliberately confirms it in the repo's **Settings** on the web (typing the slug to confirm). Once public, anyone with the link can read and `git clone` it, but only the owner can push or edit — and the user's dashboard and other repos stay private. Never make a repository public on the user's behalf.

## Collaborators

A private repo can also be shared with specific people without making it public. From the repo's **Settings** page, the owner adds a collaborator by email and picks a role, **read** or **write**. That generates an invite link (`/invite/<token>`) and a ready-to-paste agent handoff prompt that walks the invited person's agent through signing in, pulling the checkout, and orienting itself in the knowledge base. The owner can remove a collaborator or revoke a pending invite from the same page.

On the recipient's side, opening the invite link finishes creating or signing into the account for the invited email. After that, `afs hub list` shows the shared knowledge base alongside anything the recipient owns, and `afs hub pull` fetches it like any other repo. A write collaborator can `afs hub push` back to the owner's repo; a read collaborator cannot push and should hand proposed edits back to the owner instead. The Hub enforces each collaborator's role on every call — CLI, web, or the hosted agent — not just in the web UI.

## Accounts

On the hosted Hub, a user signs in with a username and password. Signup at `/signup` is currently invite-only: an email on the operator's allowlist can create an account immediately, and anyone else who signs up joins an open waitlist instead, to be admitted later. A self-hosted Hub can leave its allowlist empty, which makes signup open to anyone. Because git has no interactive login, pushing and cloning use an **access token** the user creates on the `/account` page — like a GitHub personal access token. `afs hub login` stores that token and configures Git to read it through AFS, so the CLI and agents can push without prompting.

## Talk to your agent

Sign in, then open `/agent/` (or click **Talk to an agent** on a repo page, which pre-focuses that repo). This is **optional** — it only appears when the operator has enabled the agent feature.

The hosted agent is Eve: one shared application, not a private VM or sandbox spun up per user. There is no clone step and nothing to provision, and Eve holds no durable copy of your knowledge. The Hub stays the authority for identity, permissions, commits, and conversation records throughout, so a successful write is already a real git commit in your Hub repo — no separate push step afterward, and `git clone`/`git pull` stay the exit ramp for everything Eve touches. [docs/internals/hosted-agent.md](internals/hosted-agent.md) covers the wire-level detail: the identity handoff, revision pinning, and how a concurrent change is merged or refused.

The agent starts unfocused unless its conversation already has one; you pick a knowledge base from a dropdown (or land pre-focused via **Talk to an agent** on a repo page) and can switch at any time. The Hub enforces your read/write role on every call, independently of what the agent asks for — a read-only collaborator's agent session cannot write, and review mode routes proposed edits into an overlay that only someone with write access can turn into a commit.

Model calls run inside Eve's own hosting — not on your machine, not on the Hub. No model-provider key ever lives inside a knowledge base.
