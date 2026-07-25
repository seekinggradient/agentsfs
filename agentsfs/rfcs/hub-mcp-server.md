---
description: RFC — expose a remote MCP server from the AFS Hub (hub.agentsfs.ai/mcp, Streamable HTTP + OAuth 2.1) so consumer apps like ChatGPT and Claude can read and write a user's knowledge bases; one common core, thin protocol wrappers.
status: accepted
date: 2026-07-24
sources:
  - "[[hub-mcp-server-research-chatgpt]]"
  - "[[hub-mcp-server-research-claude]]"
---

# RFC: Hub MCP server — AFS for consumer AI apps

## Summary

Add a remote MCP server to the AFS Hub at `https://hub.agentsfs.ai/mcp` (Streamable HTTP, stateless) with a self-contained OAuth 2.1 authorization server, so consumer applications that cannot run local binaries — ChatGPT, claude.ai/Desktop/mobile, Claude Code, Cursor, Copilot, and other MCP hosts — can search, read, and write the knowledge bases a user owns or collaborates on. The server is a thin protocol adapter over the same repo-access core the hosted-agent JSON API uses. No new capability logic anywhere.

## Motivation

AgentsFS has two tool surfaces today, both wrappers over `internal/core`: the `afs` CLI and a **local stdio MCP server** (`afs mcp`, `internal/mcpserver/server.go` — docs, status, tree, search, doctor, roles, backlinks, rename, plus hub sync tools). Both assume a local checkout and a harness that can spawn a process. Consumer apps can do neither: they connect to remote MCP servers by URL, authenticate with OAuth, and speak Streamable HTTP. Today a ChatGPT or claude.ai user has no path to their AFS knowledge at all.

The Hub already exposes the needed capabilities as a PAT-authenticated JSON API built for hosted Eve (`/api/agent/v1/*`): list repos, resolve, file, tree, search (the real core retrieval pipeline over a server-side sparse checkout), and a revision-anchored CAS commit. The access model is decided and enforced there: **an agent's permissions are exactly its user's permissions**; public repos are readable but never discoverable unless named. The gap is protocol (MCP) and auth (OAuth for consumer clients) — not capability.

## Principles

1. **One core, thin wrappers.** CLI, local MCP, hosted-agent API, and Hub MCP all delegate to shared functions. The Hub MCP tools and `/api/agent/v1/*` handlers call the same extracted repo-access core; search runs `internal/core`'s pipeline. No logic in adapters.
2. **Reads and writes.** Full read surface plus the CAS commit write path. Writes are commits: attributed, reviewable, revertible — git is the undo button, which is also the prompt-injection containment story.
3. **Maximum client compatibility.** Follow the MCP 2025-11-25 authorization spec exactly, design stateless per the 2026-07-28 RC direction, and shape the two core tools to ChatGPT's `search`/`fetch` connector contract so one server serves ChatGPT connectors/Deep Research/Company Knowledge, Claude (all surfaces incl. research mode), and generic MCP clients.
4. **Agent ≡ user.** Tokens resolve to a hub user; every tool call re-runs the same access checks as the browser and agent API. Nothing widens.
5. **The contract stays tool-independent.** Nothing changes on disk; `git clone` remains the exit ramp.

## Current state (audit, 2026-07-24)

- **Local MCP** (`internal/mcpserver/server.go`): official `modelcontextprotocol/go-sdk` v1.6.1, stdio, instance-anchored; hub tools are sync-only. Unchanged by this RFC.
- **Hub agent API** (`internal/hub/apiagent*.go`): bearer/basic PAT auth via `userForToken` → SHA-256-hashed PATs in SQLite (`accounts.go`, `migrate()` extends schema in place). Reads: repos, resolve, rev-pinned file, depth-bounded tree, search ("search at HEAD, serve at the pin", `at_rev` snippet verification, searchcache sparse checkouts). Write: `apiCommit` CAS with disjoint-path auto-merge and structured 409s. Handler logic being extracted into HTTP-free functions (Phase A).
- **Sessions/login**: HMAC cookie sessions, `/login?next=` + `safeNext`, argon2id passwords — the substrate for the OAuth authorize endpoint.
- **SDK v1.6.1 provides**: `mcp.NewStreamableHTTPHandler` (+ `Stateless` mode), `auth.RequireBearerToken` (spec-correct 401 `WWW-Authenticate` with `resource_metadata`), `auth.ProtectedResourceMetadataHandler`, `oauthex` metadata types. **Not provided: the authorization-server endpoints** — the one genuinely new build.
- **Routing/config**: `Server.ServeHTTP` prefix dispatch accommodates `/mcp`, `/oauth/*`, `/.well-known/*`; `reservedNames` gains `mcp` + `oauth`; `HUB_PUBLIC_URL` provides the issuer/resource base.

## Design

### Transport

`POST/GET /mcp`, Streamable HTTP via the SDK handler in **stateless mode**. Rationale: survives hub restarts, load-balancer-friendly, and matches the 2026-07-28 RC that removes `Mcp-Session-Id` entirely. Both ChatGPT and Claude support Streamable HTTP (SSE legacy is deprecated on both) — **no SSE endpoint**. Server identity: `agentsfs-hub`, hub build version.

### Tools

All tools operate over the KBs the authenticated user owns or collaborates on (`RepoList` scope — never a discovery surface for strangers' public repos; those require explicit `owner/repo` naming, same as the agent API).

**ChatGPT-contract pair** (also the primary tools for every client):

- `search` — `{query, repo?, limit?}`. Default scope: all the user's KBs (per-repo fan-out through the shared search core, merged by score). Returns the exact ChatGPT shape — `{results: [{id, title, url}]}` — as `structuredContent` **plus** the same JSON as a `content[].text` string (dual-encode requirement). `id` is the string `owner/repo/path`; `url` is the canonical hub file URL (citations only render when `url` is non-empty).
- `fetch` — `{id}`. Returns `{id, title, text, url, metadata}` (dual-encoded likewise): full file content at HEAD, `metadata` carrying repo, rev, and description. Content capped at 100k characters with an explicit truncation notice (Claude caps tool results ~150k; ChatGPT truncates at an undisclosed budget).

**AFS-native tools** (same core, richer contracts):

- `list_kbs` — repos + roles + descriptions + HEAD (`RepoList`).
- `tree` — `{repo, dir?, depth?}` (`RepoTree`).
- `write` — `{repo, message?, changes: [{path, content | delete}], base_rev?}` (`RepoCommit`). `base_rev` defaults to current HEAD; a CAS conflict returns the structured conflict (current head + conflicting paths) as the tool result so the model re-reads and retries.
- `docs` — bundled AgentsFS docs (`internal/docs`), so a cold model can learn the conventions before writing.

**Annotations** (they drive both hosts' consent UX; omitting them means per-call prompts everywhere): every read tool sets `readOnlyHint: true, destructiveHint: false` (Claude's bulk-approvable read-only group; ChatGPT treats unannotated tools as writes). `write` sets `readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: false` — CAS + git history: nothing is irreversible. Tool descriptions teach the loop: search → fetch → write with the fetched rev as `base_rev`. Descriptions stay lean (ChatGPT truncates ~5k-token tool definitions).

### Auth: self-contained OAuth 2.1 AS on the hub

The hub is both resource server and authorization server — self-hosting stays one binary + one volume, no external IdP.

**Discovery**

- PRM (RFC 9728) at `/.well-known/oauth-protected-resource` **and** `/.well-known/oauth-protected-resource/mcp` (Claude probes the path form first). `resource` = `<HUB_PUBLIC_URL>/mcp` exactly (Claude requires exact match with the URL the user entered); `authorization_servers: [<HUB_PUBLIC_URL>]` (only entry 0 is honored); `scopes_supported: ["afs:read","afs:write"]`.
- AS metadata (RFC 8414) at `/.well-known/oauth-authorization-server` **and** the OIDC-discovery alias `/.well-known/openid-configuration` (2025-11-25 clients may probe either). Advertises: authorize/token/register endpoints, `grant_types: [authorization_code, refresh_token]`, `code_challenge_methods_supported: ["S256"]`, `token_endpoint_auth_methods_supported: ["none"]`, `client_id_metadata_document_supported: true`, scopes.
- The SDK's `RequireBearerToken` middleware wraps `/mcp` and emits the 401 challenge (`WWW-Authenticate: Bearer resource_metadata="…"`) — only ever on 401, which is the only place Claude honors it.

**Client registration** — both mechanisms, per the 2025-11-25 priority order:

- **CIMD** (SHOULD in spec; Claude and ChatGPT support it; avoids DCR's client-per-connection explosion): a `client_id` that is an HTTPS URL is resolved by fetching the metadata document (10 s budget, HTTPS-only, no private-network targets, cached with TTL) and validating `redirect_uris` from it. Public client, `token_endpoint_auth_method: none`.
- **DCR** (RFC 7591, MAY in spec but the path older clients and ChatGPT "where configured" take): open `POST /oauth/register` (JSON), public clients only, stores id/redirect URIs/name. Expect one registration per Claude connection; a periodic sweep prunes clients with no live grants.

**Redirect URI validation**: exact string match against registered/CIMD-declared URIs, with one spec-sanctioned exception — loopback URIs (`http://localhost/…`, `http://127.0.0.1/…`) match with the port ignored (RFC 8252; Claude Code's CIMD declares both forms and uses an ephemeral port).

**Authorize** (`GET /oauth/authorize`): browser session required (redirect through existing `/login?next=`); consent page shows client name, **redirect-URI hostname** (Claude requirement), requested scopes with a read-only downgrade choice, and what the connection reaches (the user's KBs). Issues a single-use code, TTL 10 min, bound to client_id + redirect_uri + `code_challenge` (S256 required — no challenge, no code) + scope + user + RFC 8707 `resource` when supplied (must equal the MCP resource if present). No `client_credentials`, ever — every connection is user-consented.

**Token** (`POST /oauth/token`, **form-urlencoded**): `authorization_code` grant verifies PKCE + binding, issues an opaque access token (`afsmcp_…`, TTL 2 h) and rotating refresh token (TTL 30 days, rolling); `refresh_token` grant rotates — reuse of a consumed refresh token revokes the family. Dead/unknown refresh tokens return RFC 6749 `invalid_grant`. All tokens stored **hashed** (same `tokenHash` discipline as PATs) in new SQLite tables `oauth_clients`, `oauth_codes`, `oauth_tokens` via `migrate()`. Latency budgets are trivially met (all local SQLite).

**Verification**: a `TokenVerifier` resolves, in order, OAuth access tokens (scope-bearing, expiring) and **existing PATs** (full scope — the deliberate power-user path: claude.ai "Request headers", Claude Code `--header`, the Messages-API `authorization_token`, Cursor static headers) to `TokenInfo{UserID, Scopes, Expiration}`. Audience: OAuth tokens are minted for the MCP resource only and carry it; PATs are already hub-wide credentials. `write` requires `afs:write`; insufficient scope → 403 with the step-up `WWW-Authenticate` challenge (SEP-835).

### Common-core refactor (the "no proliferation" clause)

Extracted in Phase A: `internal/hub/repoaccess.go` — `RepoList`, `RepoResolve`, `RepoReadFile`, `RepoTree`, `RepoSearch`, `RepoCommit`, `RepoCreate` as HTTP-free `*Server` methods with typed errors; `apiagent*.go` handlers shrink to decode → call → encode with byte-identical wire behavior (existing tests prove it). MCP tools call the same methods. Cross-KB `search` composes `RepoList` + per-repo `RepoSearch` in the MCP layer — composition, not duplication.

## Security considerations

- PKCE S256 mandatory; single-use short-lived codes; exact-match (or RFC 8252 loopback) redirect validation; opaque hashed tokens; rotation with family revocation; no tokens in URLs or logs.
- CIMD fetches are SSRF-guarded (HTTPS, public hosts only, size/time caps, cached).
- Every tool call re-runs `apiRepoAccess`; workspace scope is owned+shared only; public repos stay non-discoverable.
- Writes: CAS + git history + author attribution (`user@users.agentsfs`) make injected writes visible and revertible; consent page names the write scope; read-only connections are a first-class choice.
- The MCP surface never widens the agent API's reach: same user resolution, same access checks, same commit size cap (64 MiB).
- Claude's egress range and UA quirks (python-httpx on `/register`) noted for any future WAF: we run none today.

## Implementation plan

- **A — core extraction** (in flight): `repoaccess.go`; agent-API tests unchanged and green.
- **B — OAuth AS**: SQLite tables + `oauth.go` (register/authorize/token + both metadata docs + PRM) + consent template + `VerifyMCPBearer` on `*Server`; table-driven tests: DCR → authorize → token → refresh → rotate-reuse-revoke; CIMD resolution incl. SSRF guards; PKCE failures; redirect mismatches incl. loopback-port cases; form-urlencoded enforcement; invalid_grant.
- **C — MCP endpoint**: `internal/hub/mcpapi.go` (per-request user-scoped `mcp.Server`; tools wired to repoaccess with dual-encoded search/fetch), `RequireBearerToken` wiring, reservedNames += `mcp`/`oauth`, route dispatch; tests: full client handshake via the SDK's client over httptest, every tool, scope enforcement, annotation presence, truncation caps.
- **D — docs**: README, docs/hub.md, docs/how-the-hub-works.md, account-page connect instructions for ChatGPT (developer mode + connector) and Claude (custom connector), `afs docs hub`.
- **E — verification**: full suite; live local hub; scripted end-to-end OAuth (DCR and CIMD paths) + MCP session; then deploy gate (fly.io) with Akshay.

Implementation by Opus subagents; design and review by Fable (the project's standing working convention).

## Appendix A: compatibility requirements distilled (full reports: [[hub-mcp-server-research-chatgpt]], [[hub-mcp-server-research-claude]])

| Requirement | ChatGPT | Claude | Our design |
|---|---|---|---|
| Transport | Streamable HTTP at `/mcp` (SSE legacy ok) | Streamable HTTP (SSE deprecated) | Streamable HTTP `/mcp`, stateless |
| Read-only research path | `search`+`fetch` exact contract, string ids, non-empty `url` for citations, dual-encoded results | research mode auto-invokes; no name requirement | `search`/`fetch` to ChatGPT contract |
| Writes | Developer Mode (Pro/Plus/Business/Ent/Edu); confirmation unless `readOnlyHint` | per-use confirm; read-only group bulk-approval via annotations | `write` tool + full annotations |
| OAuth | code+PKCE; RFC 9728→8414 discovery; DCR "where configured" + CIMD; no-auth allowed | code+PKCE S256 always; 401-only WWW-Authenticate; PRM exact resource; CIMD out of the box; DCR explodes clients; no M2M | AS with PKCE S256, PRM both forms, RFC 8414 + OIDC alias, CIMD + DCR |
| Static-credential path | n/a (no-auth or OAuth) | "Request headers" beta; Claude Code `--header`; API `authorization_token` | PATs accepted as bearers |
| Redirect URIs | `https://chatgpt.com/connector_platform_oauth_redirect` (+ UI-shown variants) | `https://claude.ai/api/mcp/auth_callback`; Claude Code port-agnostic loopback | exact-match + RFC 8252 loopback rule; DCR/CIMD-supplied |
| Token endpoint | — | form-urlencoded required; refresh rotation; `invalid_grant`; 10/30 s budgets | all implemented |
| Limits | ~60 s timeout; response truncation; ~5k-token tool defs | 300 s timeout; ~150k-char results | fast local ops; 100k fetch cap; lean defs |

## Appendix B: decision log

- **Streamable-only, stateless, single `/mcp`** — both major hosts accept it; the 2026-07-28 RC makes stateless the spec's own direction; SSE legacy adds surface for no current client need.
- **Self-contained AS** — self-hosting stays one binary; no IdP dependency; the hub already owns identity.
- **CIMD + DCR both** — CIMD is the spec's recommended default and avoids client explosion; DCR covers older clients; static pre-registration deferred until someone needs it.
- **PATs as MCP bearers** — zero new surface (same hashes, same resolution), unlocks Claude Code/API/Cursor header paths immediately.
- **`search`/`fetch` named for ChatGPT's contract** — compatibility beats branding; Claude has no naming requirement, so one pair serves both.
- **Writes from day one** — "save this from ChatGPT/Claude into my AFS" is the point; CAS + git reversibility + consent scope is the safety story; hosts add their own confirmation layer.
- **Fetch capped at 100k chars** — under Claude's 150k and ChatGPT's budget; truncation is explicit so the model can narrow with `tree`/`search`.
- **No SSE fallback, no rate limiting in v1** — add if a real client demands it; the hub fronts no WAF today.
