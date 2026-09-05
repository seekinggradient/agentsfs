---
description: Session note — Hub MCP server implemented, tested, and committed (65fd7e5); RFC accepted; deploy to hub.agentsfs.ai still gated on Akshay.
---

# Hub MCP server: RFC → implementation shipped

**Done this session.** Wrote and accepted [[hub-mcp-server]] (with two research appendix files), then implemented it end to end in commit `65fd7e5` on `main`:

- Phase A: `internal/hub/repoaccess.go` — agent-API logic extracted into HTTP-free `*Server` methods; handlers are envelopes; behavior byte-identical (agent-API tests untouched, green).
- Phase B: OAuth 2.1 AS (`oauth.go`, `oauth_store.go`, `assets/consent.html`) — PRM + AS metadata (both well-known aliases), DCR + CIMD (SSRF-guarded dialer with post-resolution IP check), PKCE-S256-only authorize riding `/login?next=`, consent with write-downgrade + session-HMAC CSRF, form-urlencoded token endpoint, hashed `afsmcp_*` tokens (2 h access / 30 d rotating refresh, reuse revokes the family), `VerifyMCPBearer` accepting OAuth tokens and PATs.
- Phase C: `mcpapi.go` — stateless Streamable HTTP at `/mcp`; per-request user-scoped `mcp.Server`; tools search/fetch (ChatGPT deep-research contract, dual-encoded via the SDK's typed-Out path), list_workspaces, tree, docs, write (CAS; conflict returns guidance, not an error). Write registers only on `afs:write` connections.
- My post-review fix: **owner writes auto-create a missing workspace** via `RepoCreate` (seeded with the contract), mirroring git-push semantics — found by the live E2E, covered by `TestMCPWriteAutoCreatesOwnKB`.
- Verified: full `go test ./...` green; live E2E against a locally booted hub — signup → DCR → consent → PKCE token → 401-challenge check → initialize → tools/list annotations → write(auto-create) → cross-workspace search → fetch round-trip → refresh rotation → reuse-revocation.
- `docs/hub-mcp.md` documents connecting from Claude/ChatGPT/Claude Code and self-hosting (`HUB_PUBLIC_URL` anchors issuer + resource).

**Open / next.**
- **Deploy is NOT done** — needs Akshay's go (fly.io, `fly.toml` present; HUB_PUBLIC_URL must be https://hub.agentsfs.ai in prod). After deploy: connect real ChatGPT + claude.ai accounts and exercise both OAuth paths against production.
- README.md + docs/how-the-hub-works.md wiring for the MCP feature deliberately left out: a concurrent session holds uncommitted edits to those files (its code landed as 0.10.0 `947079a`). Fold a short MCP section in once those settle.
- Watch the MCP **2026-07-28 spec release** (stateless transport final, `iss` validation): we are stateless already; check client behavior once hosts adopt it.
- Possible follow-ups: DCR client-row sweep (clients with no live grants), rate limiting if a host misbehaves, Anthropic connector-directory submission (needs annotations — already present — and a public listing decision).

**Ruled out (and why).** SSE legacy endpoint (both hosts speak Streamable HTTP; spec deprecates SSE); external IdP (self-hosting = one binary); client_credentials (Claude forbids M2M; consent always); search/fetch under afs-branded names (ChatGPT contract wins, Claude doesn't care).
