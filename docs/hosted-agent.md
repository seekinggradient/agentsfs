---
description: The hosted agent — one write-capable agent per user, in-browser at hub.agentsfs.ai/agent/, spanning all your knowledge bases.
---

# The hosted agent

The hosted agent is a single, write-capable AI agent that a signed-in user talks to in the browser at `https://hub.agentsfs.ai/agent/`. It spans **all** of the user's hub knowledge bases: it boots unfocused, lists them, and asks which one to work in. Once focused it reads and searches that KB, edits it, and commits every change back over git — so the KB stays the source of truth and `git clone` stays the exit ramp. It is owner-only and entirely optional; nothing about a local agentsfs depends on it.

There is exactly **one agent per user** (this replaced an earlier one-sprite-per-repo model). The user reaches it top-level at `/agent/`, or by clicking **Talk to an agent** on a repo page, which redirects to `/agent/?repo=<slug>` and pre-focuses that KB.

## How it runs

Each user gets one Fly Sprite (https://sprites.dev) named `afs-user-<user>` — a hardware-isolated Firecracker microVM that auto-sleeps when idle and persists across wakes. On demand the Hub provisions it once, clones **every** one of the user's hub repos as sibling checkouts under `/home/sprite/workspace/<repo>`, and runs the `agentsfs-chat` server there in **workspace mode**.

The Hub reverse-proxies the sprite at `/agent/*` (`httputil.ReverseProxy`, `FlushInterval: -1` so SSE streams), injecting the Sprites bearer token server-side. The user stays on `hub.agentsfs.ai`, never sees the `sprites.dev` login, and the sprite stays private to the org. The agent boots with `AGENTSFS_ROOT` pointing at the workspace dir itself (always present, even with zero repos), so it comes up unfocused — the browser shows a **knowledge-base picker**, and `list_repos` / `focus_repo` let the agent switch in-band. **A focus takes effect immediately:** the active root is a shared box read live on every tool call, so `focus_repo` re-scopes the reads for the rest of the same turn (a switch is not deferred to the next turn), and the server pushes an SSE `instance` event so the topbar shows the active KB live. The repo-button entry pre-focuses via `?repo=` → `POST /api/focus`.

**Freshness is a turn-boundary invariant.** In hosted workspace mode, focusing a knowledge base, starting every text-chat turn, and starting a voice session first run `git pull --ff-only` for that checkout. A successful pull clears the freshness cache and invalidates the agent's cached tree/orientation so newly added nodes are visible in the same turn. If the pull cannot complete—or the checkout contains uncommitted work—the turn fails with an explicit sync error instead of answering from possibly stale files. The cold-wake background pull remains a best-effort warm-up, not the correctness boundary.

Two ways to switch KBs: **conversationally** (the agent calls `focus_repo`), or **directly** via a **knowledge-base dropdown** in the topbar. In workspace mode the topbar shows this KB dropdown (populated from `/api/config`'s `workspace.repos`) and posting a pick to `POST /api/focus`; the generic path-based "Instance" picker is hidden. Either way the server emits the SSE `instance` event so the active KB stays in sync everywhere live.

## The tools

Once focused, the agent operates on the active KB through the `afs` toolkit and a set of write tools:

- **Read / search:** `search_wiki` (ranked search), `grep` (literal), `read_file`, `list_dir`, `backlinks`, `tree` — all wrapping the shipped `afs` binary, so search behaves exactly like the CLI.
- **Write:** `write_file` and `edit_file`, path-jailed to the active repo, emitting citations and clean diffs.
- **Workspace:** `list_repos` and `focus_repo` (choose / switch the active KB), plus `create_repo` — makes a **brand-new** knowledge base in one step: it runs `afs init <slug> --yes`, sets its `description:`, best-effort commits + `afs hub push`es it to publish, and focuses it, so "make me a new knowledge base for X" is a single tool call. `create_repo` is workspace-mode + writes only. (`src/tools/registry.ts`.)
- **Shell:** `run_bash` runs arbitrary `/bin/sh -c` commands across all workspace repos — `afs status /home/sprite/workspace`, `afs hub pull`/`afs hub list`, `git`, `rg`, `ls`, and so on. The status command gives the agent one contract/git/sync view across every cloned knowledge base before multi-repo maintenance.

Because `run_bash` can just run `git`, the old per-repo `git_pull`/`git_commit`/`git_push` tools are **removed when the shell is on** (`allowShell`). `git_status`/`git_diff` (read-only) and `write_file`/`edit_file` (which produce citations and clean diffs) are kept. Edits **batch in the working tree** across the turns of a conversation and land as real git commits pushed back to the Hub at natural **checkpoints** (a coherent unit of work done, the user wrapping up, a KB switch) — not one commit per edit; the chat panel surfaces any uncommitted state with commit/discard controls (see [agent-review-mode.md](agent-review-mode.md)).

## Build things (coding + preview)

With the shell on, the agent isn't limited to notes — its prompt grants full engineering capability. With `run_bash` (arbitrary shell, including passwordless `sudo`), Node, `git`, and the package managers, it builds, installs, and runs real software. To let long builds finish, the chat tool-iteration cap is raised from 8 to 24 when the shell is enabled (`src/agent/session.ts`, `maxToolIterations`).

**Live preview.** `cfg.previewDir` (env `AGENTSFS_PREVIEW_DIR`; on the sprite `/home/sprite/workspace/.preview`) is served path-jailed at `/preview/*` (`servePreview`, `src/server/server.ts`). The agent drops a built static site there and the user opens it at `<agent-url>/preview/`. Verified live: the agent built a coffee-shop landing page and it rendered at `/preview/`.

> Note: with arbitrary `sudo` bash and no per-command approval gate, this mode is arguably *less* constrained than Claude Code. The isolation boundary below (per-user Firecracker VM, no key in the sprite) is what makes that safe.

## Conversations

Chat history is **persistent and multi-conversation** when `cfg.dataDir` is set (env `AGENTSFS_DATA_DIR`; on the sprite `/home/sprite/.agentsfs-chat`). One JSON file per conversation is stored under `<dataDir>/conversations/` (`src/server/conversations.ts`); the sprite's disk survives cold-wakes, so chats persist across reloads and — one user per sprite — across the user's devices.

Endpoints: `GET`/`POST /api/conversations` (list / create) and `GET`/`DELETE /api/conversations/:id`. `/api/chat` takes a `conversationId` and appends each user+assistant exchange, taking the title from the first message and remembering the focused KB. `/api/config` exposes `persistConversations`.

In the UI a **Chats** drawer lists past chats (title, time, message count, KB) with delete; **New** starts a fresh one; reopening a chat rehydrates its messages *and* restores the KB it was focused in. Gated: with `AGENTSFS_DATA_DIR` unset, chat is in-memory only (the old behavior).

## Secrets & sandboxing

`run_bash` executes arbitrary commands, and that is intentional. Each user has their **own** Firecracker VM, so the blast radius of anything the agent (or a prompt injection inside it) does is that single user's own data and sprite — there is no cross-tenant reach. **Firecracker per-user isolation is the sandbox boundary.**

The hard requirement is that the operator's **shared OpenAI key must never be reachable by a user's agent.** In-sprite hiding is futile: the sprite user has passwordless `sudo` to root, so `run_bash` can `sudo cat` any file or any `/proc` entry. So the fix is architectural — **the sprite holds no OpenAI key at all.**

Model calls go through the Hub. In `agentsfs-chat`'s proxy mode, when `AGENTSFS_LLM_BASE_URL` is set the OpenAI SDK `baseURL` points at the Hub's `/v1/agent-llm` and the `apiKey` is the sprite's own per-user PAT (`AGENTSFS_LLM_KEY`); both the text Responses API and the voice ephemeral-token mint go this way (`src/reasoner/openai.ts`, `src/server/realtime.ts`; `loadConfig` no longer requires an OpenAI key in proxy mode, `src/config.ts`). The Hub's `handleAgentLLM` (`internal/hub/server.go`) authenticates the PAT, then reverse-proxies to `api.openai.com` with the Hub's real key swapped in, allow-listed to `responses` / `chat/completions` / `realtime/` only. The shared key never leaves the Hub, so this is safe even multi-tenant. The **PAT is the only credential in the sprite**; it is same-user scope (authenticates only as its own owner) and is also what the sprite uses for `git push` and `afs hub pull`, so exposing it to the user's own agent is acceptable.

Defense-in-depth layers sit on top — none of them is the boundary:

- **Env scrub:** the `run_bash` child gets a small allow-list env (`PATH`, `HOME`, `XDG_CONFIG_HOME`, `AFS_BIN`, `LANG`, …) and drops anything matching `*_KEY` / `*_TOKEN` / `*SECRET*`, so `env`/`printenv` reveal nothing (`src/agentsfs/shell.ts`; automated in `test/bash-safety.test.ts`).
- **Redaction:** every tool result passes through a redactor that masks known secret values and shapes (`sk-`, `afs_`, `Authorization` headers) before the model/UI (`src/agentsfs/redact.ts`). This is **UI hygiene, not a boundary** — a determined agent can base64/hex-encode past a substring match.
- **Env eviction:** the reasoner keys are deleted from `process.env` after config load (`src/index.ts`) to close env inheritance.
- **The gate:** `run_bash` is behind a dedicated flag `AGENTSFS_ALLOW_SHELL` (default off), **separate** from `AGENTSFS_ALLOW_WRITES`, so turning on writes never silently grants arbitrary code execution.

The July 9, 2026 source review invalidated the earlier deployed-security claim: the tracked embedded bundle contained a local `.env`, so provider credentials must be treated as exposed even though the service environment itself used the Hub proxy. The working-tree bundle is now sanitized and validator-backed, but the hosted fleet is not remediated until credentials are rotated, public history/caches are handled, the new Hub is deployed, and existing Sprites are sanitized or reprovisioned.

**Residual / follow-ups:** the LLM proxy has no per-user rate or cost limit yet — a valid PAT currently spends the Hub's OpenAI quota. Voice works in code but is not yet exercised live in the sprite. Egress-lock is not available on this Sprites version. Existing healthy Sprites are reconciled but not automatically upgraded, so deployment needs an explicit fleet migration rather than relying on `EnsureUser` alone.

## Provisioning

`AgentManager.EnsureUser` → `provisionUser` (`internal/hub/agent.go`) builds the sprite: it creates the sprite, mints one per-user PAT (`Accounts.CreatePAT`), pushes the embedded `agentsfs-chat` bundle (`//go:embed agent-bundle.tgz`) plus a linux `afs` binary (gzipped + base64-chunked with a sha256 verify, since the exec body caps around 4 MB), and clones each repo with a self-guarded `if git clone … then config … else warn` so one bad clone can't abort the whole boot under `set -e`. It seeds `/home/sprite/.config/agentsfs/hub.json` (mode 0600) plus `XDG_CONFIG_HOME` so `afs hub` authenticates, then starts the persistent `agent` service via `sprite-env services create` (survives crash and cold-wake). The current service env is workspace mode + shell + the LLM proxy and carries no OpenAI key; the legacy per-repository path now uses the same PAT-authenticated proxy. `PATH` is deliberately not overridden — the sprite default already has `/home/sprite/.local/bin` (afs) and `/.sprite/bin` (node/npm). The usernames `agent` and `user` are reserved (`internal/hub/meta.go`).

Rebuild the embedded chat source only with `go run ./scripts/build_agent_bundle.go -source ../agentsfs-chat`. The builder packages an explicit runtime allowlist (`package*.json`, `tsconfig.json`, and safe files below `src/`), normalizes archive metadata for reproducible output, and rejects credential-like content. `TestEmbeddedAgentBundleIsSafe` validates the tracked artifact in CI, so a broad tarball containing `.env`, `.git`, tests, docs, `node_modules`, symlinks, or local editor files cannot be deployed accidentally.

The Sprite is not trusted to serve active code on the Hub origin. Hub extracts the known chat UI from the compile-time bundle, injects a fresh script nonce, and serves it with a default-deny `strict-dynamic` CSP. The reverse proxy then allows only the current API method/path matrix and the exact self-contained preview index. It forces safe JSON/SSE MIME types for APIs, strips origin-affecting Sprite headers and cookies, and gives preview an opaque, networkless CSP sandbox. Hosted previews must therefore be a single `index.html`; nested preview assets intentionally return 404.

Routes (`internal/hub`): `/agent/` (owner-only, `handleUserAgent`, `web.go`); the old per-repo `/<user>/<repo>/agent/` now 302-redirects to `/agent/?repo=`; `/v1/agent-llm/*` (`handleAgentLLM`, `server.go`).

## Environment & deploy

The agent feature is enabled only when Sprites, OpenAI, and accounts are all configured (`AgentManager.Enabled()`). Fly secrets on the `agentsfs-hub` app:

- `SPRITES_TOKEN` — a `sprites.dev` token (distinct from the Fly API token).
- `OPENAI_API_KEY` — intended to live only on the Hub and used by the `/v1/agent-llm` proxy; make this claim about the deployed fleet only after the credential/bundle incident migration is complete.
- `CHAT_MODEL` — default `gpt-5.1`.
- `HUB_PUBLIC_URL` — the public URL sprites clone from.

The Hub Docker image bakes a `linux/amd64` `afs` at `/usr/local/bin/afs-linux` to ship into sprites. Per-sprite service env (set by `provisionUser`, `internal/hub/agent.go`): `AGENTSFS_MODE=workspace`, `AGENTSFS_WORKSPACE` / `AGENTSFS_ROOT` = the workspace dir, `AGENTSFS_ALLOW_WRITES=1`, `AGENTSFS_ALLOW_SHELL=1`, `AGENTSFS_PREVIEW_DIR=/home/sprite/workspace/.preview` (direct mode can serve its tree; hosted mode exposes only its self-contained index), `AGENTSFS_DATA_DIR=/home/sprite/.agentsfs-chat` (the conversation store), `AGENTSFS_LLM_BASE_URL=<hub>/v1/agent-llm`, and `AGENTSFS_LLM_KEY=<per-user PAT>`.

Voice can also switch KBs: the realtime tool set includes `list_repos` and `focus_repo` alongside `search_wiki` (`src/server/realtime.ts`).

## Review mode (agentic co-editing)

On a rendered note the owner can leave inline comments and hand them to the agent, which resolves them by editing files and proposes a diff to approve — a **structural no-commit gate** (restricted review toolset + owner-only deterministic commit endpoint). See [agent-review-mode.md](agent-review-mode.md).
