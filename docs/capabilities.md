---
description: Everything AgentsFS can do, organized by which of the four surfaces you reach it through — the afs CLI, the local afs mcp server, the Hub web app, and the Hub's remote MCP endpoint.
---

# Capabilities

AgentsFS has four surfaces, and they are not four doors onto the same room. This page answers one question, task by task: **I want to do X — which surface do I use?** Where two surfaces both do something, the cells tell you if they actually do the same thing or only sound like they do.

The four surfaces:

| Surface | What it is | Runs where | Auth |
|---|---|---|---|
| **`afs` CLI** | The full command set — everything else is built on top of it | The user's machine | None (local filesystem access) |
| **local `afs mcp`** | A stdio MCP server exposing 12 of the CLI's read/orient/sync tools | Spawned as a subprocess by a coding harness (Claude Code, Codex, Cursor…) on the user's machine | None — inherits the harness's filesystem access |
| **Hub web app** | The browser UI at `hub.agentsfs.ai` (or a self-hosted Hub), plus its git-over-HTTP and LFS endpoints | A server; the Hub | Session cookie (browser) or access token (git, `afs hub`) |
| **Hub `/mcp`** | A remote, OAuth-protected MCP endpoint on the Hub | The Hub | OAuth 2.1, or a Hub personal access token (PAT) as a bearer token |

A **knowledge base** is the user-facing name for a body of notes. On disk, one is an **instance** — a directory with `.agentsfs/` and a versioned `AGENTS.md` contract. On the Hub, the same thing is also a git **repo**. All three words point at the same object; which one a doc uses just depends on which angle it's describing.

## Before you read the matrix

Three asymmetries account for most of the surprises below:

- **The two MCP servers are not interchangeable.** They share three tool names and no matching schemas; see [mcp.md](mcp.md) for the tool-by-tool comparison.
- **`--context` is not on either MCP server.** Neither the local nor the Hub MCP `search` tool takes a context-pack parameter. The Hub's agent API does expose the same token-budgeted pack over HTTP (`?context=` on its repo search endpoint), which is how Eve gets one — but no MCP client can ask for it.
- **Anything that touches the user's machine or credentials is CLI-only, on purpose.** `init`, `setup`, `connect`, `contract`, `embeddings`, `hub login`, `hub logout` — none of these have an MCP or Hub-web equivalent. See [Deliberately CLI-only](#deliberately-cli-only).

## Task matrix

"—" means that surface has no equivalent. A named tool or command is the thing to call.

### Create and connect

| Task | `afs` CLI | local `afs mcp` | Hub web | Hub `/mcp` |
|---|---|---|---|---|
| Create a new local instance | `afs init [dir]`, or `afs setup [dir]` (creates + connects a project in one step) | — | — | — |
| Point a project or a global harness config at an existing instance | `afs connect <instance>` | — | — | — |
| Create a knowledge base on the Hub | `afs hub push` (first push creates it) | `hub_push` | First `git push` to a new slug creates it | `write` to a repo under your own username, if it doesn't exist yet |
| Sign in to a Hub account | `afs hub login` | — (reads the config `afs hub login` already wrote) | Sign in with username/password at `/login`; create an access token at `/account` | OAuth 2.1 authorize flow, or paste a PAT as the bearer token |
| Forget a saved Hub sign-in | `afs hub logout` | — | `/logout` (clears the session cookie only) | Revoke the token/grant at `/account` |

### Orient

| Task | `afs` CLI | local `afs mcp` | Hub web | Hub `/mcp` |
|---|---|---|---|---|
| List every local instance and its contract/git/sync state | `afs status [roots...] [--json] [--doctor] [--fetch]` | `status` | — | — |
| See a file tree with descriptions and freshness | `afs tree [dir] [-d N]` | `tree` | Repo page (file listing) | `tree` (`repo`, optional `dir`, `depth`; default depth 2) |
| Find where the reserved roles (journal, scratch, collections, the backlog page) actually live | `afs roles [path] [--json]` | `roles` | — | — |
| See the backlog's ready-work view (in-progress, ready by band, blocked/parked counts) | `afs tasks [--ready] [--band <name>] [--all] [--json]` | — | The backlog page renders with status controls and per-band progress | — |
| Assemble a budgeted session orientation pack (identity, top tasks, adaptive tree, recent journal) | `afs prime [--budget N]` | — | — | — |
| Find every wikilink pointing at a file | `afs backlinks <name> [path]` | `backlinks` | Graph tab on the repo page (visual, not a lookup by name) | — |

### Retrieval

| Task | `afs` CLI | local `afs mcp` | Hub web | Hub `/mcp` |
|---|---|---|---|---|
| Lexical (full-text) search, one instance | `afs search <query> [path]` | `search` (`semantic: false`) | — | — |
| Semantic (embedding) search, one instance | `afs search <query> --semantic` | `search` (`semantic: true`) | — | — |
| Ranked search across everything the caller can see | — | — | — | `search` (fans out across every knowledge base you own or collaborate on, or scope with `repo:`) |
| A token-budgeted context pack for a query | `afs search <query> --context[=N]` | — | — | — |
| Read one file's full content | (open the file) | — | Blob view (`/user/repo/blob/path`) | `fetch` (by the id `search` returned) |

The CLI's `search` (used by both `afs search` and local MCP's `search`) is a multi-signal ranking pipeline, not raw FTS5: body full-text is one signal blended with each note's description, its one-hop wikilink neighborhood, and structural priors (e.g. `INDEX.md` and `AGENTS.md`/`CLAUDE.md` ranked differently). `--context` hydrates the top hits from that same pipeline into one budgeted pack (default budget: 4000 estimated tokens, chars ÷ 4) rather than running a separate retrieval path — which is why `--context` silently ignores `--semantic` and `-n`/`--limit`: it shares the ranking, not the output shaping, with plain search.

### Maintenance

| Task | `afs` CLI | local `afs mcp` | Hub web | Hub `/mcp` |
|---|---|---|---|---|
| Run the health check (dead links, missing descriptions, stubs, orphans) | `afs doctor [path] [--json]` (exits 1 if any finding is severity `error`) | `doctor` | — | — |
| Rebuild the derived search index | `afs reindex [path] [--embeddings]` | — | — | — |
| Check or upgrade the installed contract | `afs contract [current\|status\|diff\|upgrade] [path] [--yes] [--force]` | — | — | — |
| Rename or move a file, rewriting every wikilink to it | `afs rename <old> <new> [path]` | `rename` | — | — |

Doctor and rename are the two maintenance tasks with a local MCP tool. Reindexing and contract management have no MCP or Hub-web surface whatsoever — neither exists as a tool on either MCP server, and neither has a Hub-web control. A gardener agent doing maintenance work can reach doctor/rename over local MCP, but needs the CLI itself for reindex and contract upgrades.

### Hub sync

| Task | `afs` CLI | local `afs mcp` | Hub web | Hub `/mcp` |
|---|---|---|---|---|
| Upload (link + push) an instance | `afs hub push [name]` | `hub_push` | `git push` to the repo's clone URL | `write` (first write under your username creates the repo if it doesn't exist) |
| Download a knowledge base | `afs hub pull <name> [dir]` | `hub_pull` | `git clone`, or the ZIP download on the repo page | `fetch`/`tree` (read-only; not a filesystem checkout) |
| Fold a downloaded knowledge base into an existing instance | `afs hub pull <name> --merge` | `hub_pull` (`merge: true`) | — | — |
| List repos you own or can see | `afs hub list` | `hub_list` | Dashboard | `list_kbs` |
| Check sign-in and link status | `afs hub status` | `hub_status` | Dashboard implies it (you're signed in if you're looking at it) | — |

`--merge` never overwrites: new files are added, byte-identical files are skipped, and a file that differs is written aside under the target instance's own resolved scratch role (`agent-scratch/hub-merge-<slug>/` on a current instance; call `afs roles --json` rather than assuming the name) for you to reconcile by hand.

Two write paths reach a Hub repo, and they behave differently. `afs hub push` and plain `git push` are **ordinary git** — the CLI shells out to `git push` and the Hub runs `git-http-backend` behind an auth check. If the remote has moved on, you get git's normal non-fast-forward rejection; pull, reconcile, and push again. The Hub's `write` tool and the agent API (`POST /api/agent/v1/commit`, which is how Eve writes) are **revision-pinned** instead: the caller names the revision it based its changes on, and if HEAD has moved since, the Hub merges when the intervening commits touched no path the caller changed, and refuses with a conflict naming the new HEAD and the colliding paths when they did.

### Sharing

| Task | `afs` CLI | local `afs mcp` | Hub web | Hub `/mcp` |
|---|---|---|---|---|
| Make a repo public or private | — | — | Repo **Settings** (public requires typing the slug to confirm) | — |
| Add a collaborator by email, with a read or write role | — | — | Repo **Settings** → add by email; generates an invite link (`/invite/<token>`) and a ready-to-paste agent handoff prompt | — |
| Remove a collaborator or revoke a pending invite | — | — | Repo **Settings** | — |
| Rename a repo's slug (git and LFS clone URLs redirect; browser bookmarks to the old URL do not) | — | — | Repo **Settings** | — |
| Delete a repo (soft-delete to `.trash/` on the Hub's volume, not permanent) | — | — | Repo **Settings** — requires a signed-in browser session; a PAT (which an agent could hold) cannot trigger it | — |

Collaborator role names on the Hub are exactly two: **read** and **write**. A read collaborator can pull and browse but never push, commit, or open review mode's approve step; a write collaborator can do everything the owner can except the repo's own Settings actions (visibility, collaborators, rename, delete stay owner-only). The Hub enforces this on every call — CLI push, git, the hosted agent, and the MCP `write` tool alike — not just in the web UI. One important exception: a PAT presented as a bearer credential to `/mcp` always carries full read-write scope (`afs:read` + `afs:write`) regardless of the collaborator role behind it — "read-only" as an MCP-connection property only exists through OAuth scope grants, not through PATs.

### The hosted Eve agent

| Task | `afs` CLI | local `afs mcp` | Hub web | Hub `/mcp` |
|---|---|---|---|---|
| Chat with an agent that can read/write your knowledge bases | — | — | `/agent/` (top-level, spans every KB you can reach), or **Talk to an agent** on a repo page (pre-focused) | — |
| Annotate a note and hand comments to the agent for a reviewable diff | — | — | **Comment for agent** on any Markdown note you can write to (agent review mode) | — |

Eve is **one shared, Vercel-hosted application** — not a private VM or sandbox provisioned per user, and not a clone of your repos. It writes through the same revision-pinned agent API as the Hub MCP's `write` tool, so a successful Eve write is already a real Hub git commit. This surface is Hub-web-only, and it appears only when the Hub operator has enabled it. See [internals/hosted-agent.md](internals/hosted-agent.md) for the request path and permission model.

### Self-hosting

AgentsFS is open source end to end, including the Hub. `afs-hub` is a single Go binary that wraps real `git` and stores plain bare repositories plus Git LFS objects on a local volume — no managed database, no vendor lock-in, `git clone` always works. Running one is a `docker build -f deploy/Dockerfile` away, or `go build ./cmd/afs-hub` from source. A self-hosted Hub is the same server hub.agentsfs.ai runs — same `/mcp` endpoint, same web app, same git/LFS protocol — configured through environment variables:

| Concern | Env var |
|---|---|
| Public origin (anchors OAuth issuer, MCP resource URLs, clone URLs) | `HUB_PUBLIC_URL` |
| Bootstrap access tokens (`user:token`, comma-separated) | `AFS_HUB_TOKENS` |
| Signup allowlist (non-empty flips signup to invite-only; empty allows anyone) | `AFS_HUB_ALLOWLIST` |
| Turn open signup off entirely | `AFS_HUB_OPEN_SIGNUP=false` |
| Hosted-agent upstream (Eve) and its identity-handoff signing key | `HUB_EVE_AGENT_URL`, `HUB_EVE_AGENT_SECRET` |
| Operator admin console (`/admin/metrics`, `/admin/access`) | `HUB_ADMIN_USER` |

Leave the agent env vars unset and the Hub still does everything else — sync, sharing, browsing, both MCP surfaces — with the **Talk to an agent** affordance simply hidden.

## Deliberately CLI-only

Nothing below has an MCP tool or a Hub-web equivalent, and that's a boundary, not a gap: every one of these changes the user's machine, credentials, or global configuration, and the project's position is that an agent should never do that without the human running the command themselves.

- `afs init`, `afs setup`, `afs connect` — create instances, write into global harness config (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`)
- `afs contract status\|diff\|upgrade` — rewrites `AGENTS.md` on disk
- `afs embeddings setup\|clear` — writes an API key to a local config file
- `afs hub login` / `afs hub logout` — writes a Hub token to disk and installs a git credential helper
- `afs update` / `afs uninstall` — modifies or removes the installed binary
- `afs skills` — materializes skill files to a local cache directory

The gap that surprises people most: nothing among the 12 local tools writes content into a knowledge base. `rename` moves a file and rewrites links, but leaves the change uncommitted for the human to review; no local tool stages or commits a note's content. If you need an MCP-reachable write, it exists in exactly one place: the Hub's `write` tool, gated on the `afs:write` OAuth scope.

## What's deliberately not here

- **The file walker ignores `.gitignore`.** Every surface — `afs tree`, `search`, `doctor`, the Hub's file listing — sees build artifacts and `node_modules` inside an instance if they're on disk. (Tracked as a known gap, not a silent one.)
- **No incremental indexing.** `afs reindex` rebuilds the full-text index from zero every time; there's no watch mode or partial update.
- **Only `*.md` is indexed.** Search, the context pack, and the ranking pipeline all operate on Markdown files exclusively — PDFs, images, and other attachments are invisible to search even though `afs tree` and the Hub's file browser show them.

## Related reading

- [concepts.md](concepts.md) (`afs docs concepts`) — the vocabulary this page assumes: instance, knowledge base, contract, roles, Hub, Eve
- [mcp.md](mcp.md) (`afs docs mcp`) — full parameter and schema reference for both MCP servers
- [hub.md](hub.md) (`afs docs hub`) — the Hub end to end: sync, sharing, accounts, the hosted agent
- [internals/agent-review-mode.md](internals/agent-review-mode.md) — how **Comment for agent** and the approve/discard flow work
- [internals/hosted-agent.md](internals/hosted-agent.md) — Eve's request path, permission model, and release process in detail
- `afs docs commands` — the full CLI command and flag reference
