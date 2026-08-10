---
description: Both MCP servers — the local afs mcp over stdio and the Hub's remote /mcp over OAuth 2.1 — their tools, what's deliberately not exposed, and how to wire each one up.
---

# MCP servers

AgentsFS ships **two** Model Context Protocol servers. They are not two doors onto the same room — they have different transports, different auth, different tool sets, and different jobs. If you build against one and assume the other matches, your code will break.

| | Local `afs mcp` | Hub `/mcp` |
|---|---|---|
| Runs where | On the user's machine, spawned as a subprocess | At `hub.agentsfs.ai` (or a self-hosted Hub), one shared HTTP endpoint |
| Transport | stdio | Streamable HTTP (stateless) |
| Auth | None — inherits whatever filesystem access the harness already has | OAuth 2.1, or a Hub personal access token (PAT) as a bearer token |
| Serves | One local agentsfs instance at a time | Every knowledge base the authenticated user owns or collaborates on, across the whole Hub |
| Who connects | A coding harness already running on the machine (Claude Code, Codex, Cursor, etc.) that can shell out to a subprocess | A consumer AI app that can't shell out — ChatGPT, claude.ai, Claude Desktop/mobile — or a remote client using a PAT |
| Tool count | 12 | 6 (5 read tools always; `write` only on a write-scoped connection) |
| Can write to a knowledge base | No — no tool commits or edits content | Yes — `write`, gated on the `afs:write` scope |

The two servers share exactly three tool **names** — `docs`, `tree`, `search` — and none of the three has the same schema on both sides. See [The shared-name trap](#the-shared-name-trap) before you write code that assumes otherwise.

## Local: `afs mcp`

`afs mcp [path]` starts a stdio MCP server rooted at `path` (default: the current directory). It takes one optional positional argument and no flags — `afs mcp --help` exits with an error rather than printing usage, so this document is the reference. Every tool is a thin adapter over the same `internal/core` package the CLI itself uses; no tool carries MCP annotations (no `readOnlyHint`/`destructiveHint`), so a host that gates on annotations will prompt for every call.

Most tools accept an optional `path` parameter to scope the call to an instance other than the one the server started in — useful when a harness's working directory moves around during a session but the server process doesn't restart.

| Tool | Purpose | Parameters |
|---|---|---|
| `docs` | Read bundled AgentsFS documentation and skills, including `markdownto` | `topic` (optional, default `agent-start`) |
| `status` | Discover every local agentsfs instance beneath one or more directories; JSON contract, git, sync, and duplicate-checkout state | `roots` (string list, default: server's start directory), `doctor` (bool, run health checks too), `fetch` (bool, contact git remotes — otherwise local and read-only) |
| `tree` | Orient: an indented tree with every file/directory's description and last-touched age | `path` (optional, scope to a subdirectory), `depth` (optional, cap how many levels expand) |
| `search` | Full-text (default) or semantic search over the instance, ranked, with section-level snippets | `query` (required), `semantic` (bool), `limit` (default 10), `path` (optional) |
| `doctor` | Deterministic health check — missing descriptions, dead/ambiguous wikilinks, stubs, orphans — as JSON findings | `path` (optional) |
| `roles` | Where this instance's reserved roles (journal, scratch, collections) actually live, and how each was resolved | `path` (optional) |
| `backlinks` | Every `[[wikilink]]` pointing at a given file or name | `name` (required), `path` (optional) |
| `rename` | Rename or move a file and rewrite every wikilink to it in one pass; leaves the change uncommitted for review | `old` (required), `new` (required), `path` (optional) |
| `hub_status` | Whether the user is signed in to a Hub, and whether this instance is linked to it | `path` (optional) |
| `hub_push` | Publish committed state; embedded pushes atomically append an exact prefix snapshot plus recoverable ledger | `name` (optional for linked instances; explicit `owner/repo` required for an unlinked embedded instance), `path` (optional) |
| `hub_pull` | Download a standalone repo, content-fold with quarantine, or history-sync a linked embedded projection | Clone/fold: `name`, optional `dir`, `merge`; projection: `projection`, optional exact `name`, `path`, `adopt`, `continue`, `abort` |
| `hub_list` | List every repository visible to the user on the Hub, including ones shared with them | none |

`search` has no `context` parameter. `afs search --context[=N]` — the CLI's token-budgeted context-pack feature — is CLI-only; neither MCP server exposes it.

## Hub: remote `/mcp`

The Hub runs a second, separate MCP server at `https://<hub>/mcp` (Streamable HTTP, stateless) for consumer apps that can't run a local binary at all. No tool here holds capability logic of its own: each one is a thin closure over `internal/hub/repoaccess.go`, the same list/search/read/tree/commit core the Hub's agent API (`/api/agent/v1`, which is how Eve reads and writes) goes through. Those two transports therefore cannot drift on access rules or revision semantics. The Hub's other two entry points — the browser UI and git-over-HTTP — each authorize independently and are outside that guarantee.

**Auth.** Two ways in:

- **OAuth 2.1** — the Hub is its own authorization server (authorization-code + PKCE S256, no client-credentials grant). Discovery is via RFC 9728 protected-resource metadata and RFC 8414/OIDC metadata; client registration via DCR (RFC 7591) or CIMD (URL-based client IDs, SSRF-guarded). A connection is scoped to `afs:read`, optionally plus `afs:write` — the consent screen offers a read-only downgrade, and a connection without the write scope never even sees the `write` tool in `tools/list`.
- **Personal access token (PAT)** — mint one on the Hub's `/account` page and pass it as a bearer token. A PAT is the power-user fallback and **always carries the full read-write scope**, regardless of what you intended — "read-only connection" is an OAuth-only property. If you want a connection that genuinely cannot write, use OAuth without the write scope, not a PAT.

OAuth tokens are opaque, stored hashed, and short-lived: 2 hours for an access token, 30 days for a rotating refresh token, with reuse-revokes-the-family semantics. Design and client-compatibility research lives in this repo's own agentsfs instance at `agentsfs/rfcs/hub-mcp-server.md`.

Every tool call is access-checked as the authenticated user, per call — owned and shared repos, collaborator roles enforced live, public repos readable only when named explicitly (never a discovery surface for someone else's public work). Every write lands as an attributed, revertible git commit (author `<user>@users.agentsfs`, committer the Hub), so anything a connected app writes shows up in history and can be undone — that, plus per-connection scopes and the host's own confirmation prompts, is the prompt-injection containment story.

| Tool | Purpose | Parameters |
|---|---|---|
| `search` | Search across every knowledge base the user can read (or one, via `repo`); returns ranked `{id, title, url}` hits | `query` (required), `repo` (optional `owner/repo` to scope to one KB), `limit` (default 10, max 25) |
| `fetch` | Read the full content of one file by the `id` a `search` hit returned | `id` (required, `owner/repo/path`) |
| `list_kbs` | List every knowledge base the user owns or collaborates on — role, visibility, description, current HEAD | none |
| `tree` | Indented file listing of one knowledge base at HEAD | `repo` (required), `dir` (optional), `depth` (default 2) |
| `docs` | Read bundled AgentsFS documentation and skills, including `markdownto` | `topic` (optional, default `agent-start`) |
| `write` | Commit one or more file writes/deletes to a knowledge base in a single git commit; only registered on a write-scoped connection | `repo` (required), `changes` (required list of `{path, content}` or `{path, delete:true}`), `message` (optional), `base_rev` (optional, default current HEAD) |

`write` is revision-anchored: pass the `rev` a `fetch` call returned as `base_rev`, and if HEAD has moved on a path you touched since, the call returns a conflict naming the new HEAD and the conflicting paths instead of silently overwriting or erroring — re-fetch and retry with the new `base_rev`. Writing to a knowledge base that doesn't exist yet, under the caller's own username, creates it and seeds it with the AgentsFS contract; writes into anyone else's namespace never create. Every write is a real, attributed, revertible git commit — nothing routes through a separate "publish" step.

`search` and `fetch` deliberately match ChatGPT's connector contract (`{results:[{id,title,url}]}` and `{id,title,text,url,metadata}`) so the same pair works for ChatGPT's connectors/Deep Research/Company Knowledge and for Claude. Read tools carry `readOnlyHint`, which is why Claude can bulk-approve them and ChatGPT skips a per-call confirmation for them; `write` is deliberately unannotated as read-only and prompts every time.

### Connecting

**Claude (claude.ai, Desktop, mobile).** Settings → Connectors → Add custom connector → URL `https://hub.agentsfs.ai/mcp`. Claude discovers the OAuth server on its own (CIMD or DCR), sends the user through the Hub's consent page, and refreshes tokens itself.

**ChatGPT.** Add the connector with the same URL and OAuth (Settings → Apps & Connectors). Read-only research use works as a plain connector; the write tool additionally requires Developer Mode (Settings → Apps & Connectors → Advanced → Developer mode).

**Claude Code, the Messages API, or any header-auth client** — skip OAuth and use a PAT as a bearer token:

```bash
claude mcp add --transport http afs-hub https://hub.agentsfs.ai/mcp --header "Authorization: Bearer <your PAT>"
```

The Messages API's MCP connector (`mcp_servers[].authorization_token`) and similar header-auth clients (Cursor, etc.) work the same way — swap in the client's own syntax for passing that header.

**Self-hosting.** Nothing extra to run — the MCP server and OAuth endpoints ship inside `afs-hub` itself. Set `HUB_PUBLIC_URL` to the Hub's stable public origin; it anchors the OAuth issuer and the MCP resource identifier, and consumer clients require HTTPS.

## The shared-name trap

`docs`, `tree`, and `search` exist on both servers under the same name. Do not assume a call written against one works unmodified against the other.

**`search`** is the pair with the most divergent shape:

| | local `afs mcp` | Hub `/mcp` |
|---|---|---|
| Scope | One instance (wherever the server was started, or an explicit `path`) | Every knowledge base the caller can see, or one named with `repo` |
| Input | `query`, `semantic` (bool), `limit`, `path` | `query`, `repo` (optional `owner/repo`), `limit` (max 25) |
| Output | Ranked `path § heading` section hits **with snippets** — you read the match inline | `{results: [{id, title, url}]}` — a pointer with no snippet; call `fetch` to read it |
| Ranking | The multi-signal pipeline (FTS + description + link graph + structural priors), or a pure embedding search with `semantic: true` | The same lexical pipeline at HEAD, run per repo and interleaved round-robin — no semantic option |
| Result identity | A path plus a heading | An opaque `id` (`owner/repo/path`) that round-trips into `fetch` |

**`tree`**:

| | local `afs mcp` | Hub `/mcp` |
|---|---|---|
| Input | `path` (optional, scopes to a subdirectory of the current instance), `depth` (unlimited unless set) | `repo` (**required** — there is no "current instance"), `dir`, `depth` (default 2) |
| Output | Indented tree with per-entry descriptions and last-touched age | Indented tree with file sizes; no descriptions, no ages |

**`docs`** is the one genuine near-match: both render the same bundled topics from the same embedded files, with the same default (`agent-start`), because both wrap the same `internal/docs` package. Safe to treat as interchangeable. That shared table is also how a bundled *skill* reaches a remote agent: `topic: markdownto` returns the Markdown To skill, which an agent connected over either server can read without a local skills directory to load from.

Beyond the three shared names: local MCP has no tool that writes to a knowledge base's content at all (`rename` moves and relinks a file but never edits its contents, and it leaves the change uncommitted). The Hub's MCP has no `doctor`, `roles`, `backlinks`, `status`, or `rename` — health checks, role resolution, link-graph queries, and instance discovery are local-only concepts that don't have a Hub-side equivalent. And `--context` packs, the CLI's token-budgeted retrieval feature, exist on neither MCP surface.

## What's deliberately not exposed, and why

Anything that changes the user's machine, credentials, or global configuration stays a human-run CLI command, not an MCP tool: `afs setup`/`init`/`connect` (workspace and harness wiring), `afs contract upgrade` (rewrites a file the user should review), `afs embeddings setup` (touches API keys), `afs hub login`/`logout` (mints or revokes credentials and installs a git credential helper), `afs update`/`uninstall` (modifies the installed binary). An agent that needs one of these should ask the human to run it, not be handed a tool that could run it unattended.

On the Hub side, the same principle shows up as scope rather than absence: `write` exists, but only on a connection the user explicitly granted `afs:write` to, and it can only ever produce a git commit — never delete a repository, change a collaborator's role, or touch account settings.

## See also

- [capabilities.md](capabilities.md) (`afs docs capabilities`) — the same tools placed against the CLI and Hub web, task by task, so you can pick a surface
- [concepts.md](concepts.md) (`afs docs concepts`) — what an instance, a knowledge base, and the contract are
- [hub.md](hub.md) (`afs docs hub`) — the Hub end to end: accounts, sharing, the web UI, and the hosted agent, of which this MCP endpoint is one surface among several
