---
description: Full research report (2026-07-24) on Claude consumer-surface requirements for remote MCP connectors and the current MCP authorization spec (2025-11-25 + 2026-07-28 RC), plus a survey of other consumer hosts. Supporting material for [[hub-mcp-server]].
sources:
  - https://claude.com/docs/connectors/building/authentication
  - https://claude.com/docs/connectors/building
  - https://claude.com/docs/connectors/custom/remote-mcp
  - https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization
  - https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/
---

# Claude + MCP auth spec compatibility (research, verified 2026-07-24)

Verbatim output of the research pass backing [[hub-mcp-server]] Appendix A. Current stable MCP spec revision is **2025-11-25**; a **2026-07-28 Release Candidate** finalizes within days.

## 1. Claude.ai custom connectors

- Available on Claude.ai web, Desktop, mobile, and Cowork for **Free (1 connector), Pro, Max, Team, Enterprise**. Team/Enterprise: Owners/Admins add; members connect individually.
- Add by URL (Settings → Connectors); Advanced settings accept an optional pre-registered client_id/secret; a beta "Request headers" section takes a static API key/bearer.
- Transports: **Streamable HTTP** (standard) and legacy HTTP+SSE (deprecated).
- URL: public HTTPS, commonly ending `/mcp`; reachable from Anthropic egress `160.79.104.0/21`.
- Limits: **300 s timeout; ~150,000-char max tool result.**

## 2. OAuth behavior

- **OAuth 2.1 authorization code; PKCE S256 on every request**; AS must advertise `code_challenge_methods_supported: ["S256"]`.
- Registration types: `oauth_dcr` (RFC 7591, out of the box), `oauth_cimd` (Client ID Metadata Documents, out of the box — selected when AS metadata advertises `client_id_metadata_document_supported: true` AND `"none"` in `token_endpoint_auth_methods_supported`), `oauth_anthropic_creds` (contact Anthropic), `custom_connection`, `static_headers` (beta), `none`.
- **DCR registers a new client on every fresh connection** (client-record explosion) — Anthropic recommends CIMD for high-traffic servers.
- **No machine-to-machine**: `client_credentials` with no user is unsupported; every connection needs user consent.
- Discovery: Claude honors `WWW-Authenticate: Bearer resource_metadata="…"` **only on 401** (never 200); if absent it probes `/.well-known/oauth-protected-resource/<mcp-path>` then the root form. PRM `resource` must match the entered MCP URL **exactly**; only `authorization_servers[0]` is used. AS must serve RFC 8414 or OIDC Discovery 1.0.
- **Redirect URIs**: hosted surfaces use `https://claude.ai/api/mcp/auth_callback`; Claude Code uses RFC 8252 loopback on an ephemeral port and declares `http://localhost/callback` + `http://127.0.0.1/callback` in its CIMD — **the AS must accept both with the port ignored**.
- Refresh: reactive on 401 + proactive 5 min before expiry; return RFC 6749 `invalid_grant` for dead refresh tokens; **rotate refresh tokens for public clients**. `/token` **must accept `application/x-www-form-urlencoded`**; `/register` takes JSON. Latency budgets: 10 s (discovery/register/token), 30 s (refresh).
- Claude accepts the 2025-03-26, 2025-06-18, and 2025-11-25 auth specs. Enterprise Managed Auth (IdP-signed assertion) exists as an Anthropic extension.

## 3. Tool-use gating

- Per-use confirmation with "Allow always"; tools can be disabled via the Search-and-tools menu; org-level `ask`/`blocked` controls exist.
- Annotations drive the UX: `readOnlyHint: true` + `destructiveHint: false` puts a tool in the bulk-approvable **Read-only group**; omitted annotations default conservative ⇒ every call prompts. Annotations are a Directory-submission requirement.
- **Research mode uses remote MCP connectors and auto-invokes their tools** without per-call approval; local servers can't participate.
- No documented required tool names for Claude (search/fetch naming is a ChatGPT contract; harmless and useful for Claude too).

## 4. Current MCP spec (2025-11-25) — resource-server normatives

- MUST implement RFC 9728 PRM (with `authorization_servers`); MUST offer 401 `WWW-Authenticate` discovery or the well-known URI path; SHOULD include `scope` in the challenge.
- MUST validate token audience per RFC 8707 §2; MUST NOT pass tokens upstream; tokens never in query strings; 401 invalid, 403 insufficient scope (+ step-up challenge SHOULD), 400 malformed. HTTPS everywhere; redirects localhost-or-HTTPS.
- Clients MUST send RFC 8707 `resource` in authorize + token requests; MUST use PKCE S256 and verify AS support first; MUST support RFC 8414 and OIDC Discovery.
- Changes vs 2025-06-18: **DCR SHOULD→MAY; CIMD now SHOULD** (priority: pre-registration → CIMD → DCR → prompt); PKCE-verification mandatory; OIDC discovery added; incremental scope consent (SEP-835); Tasks primitive, icons, sampling tool-calls.
- **2026-07-28 RC**: stateless transport (initialize handshake + `Mcp-Session-Id` removed — design for plain load balancing); client MUST validate `iss` (RFC 9207); DCR `application_type`; credentials bind to one AS; W3C Trace Context.

## 5. Claude Code / API paths

- Messages API MCP connector: beta header `mcp-client-2025-11-20`; `mcp_servers` with `authorization_token` bearer; tool calls only; caller runs OAuth itself.
- Claude Code: `claude mcp add --transport http <name> <url>` with `--header` static bearer; OAuth via `claude mcp login` (auto DCR/CIMD, PRM-first discovery, auto refresh, loopback callback); `--client-id/--client-secret` for non-DCR servers.

## 6. Other hosts (brief)

- **Gemini consumer app**: no user-pasted remote MCP URLs (UNVERIFIED negative); MCP lives in Gemini CLI/API/Cloud with OAuth+IAM.
- **Microsoft Copilot**: via Copilot Studio / M365 connectors; OAuth 2.0, DCR eases setup; **OAuth 2.1-only enforcement may fail** (as of Jan 2026); refresh-token gaps reported.
- **Perplexity**: supports remote MCPs but publicly de-emphasizing MCP since Mar 2026; lower priority.
- **Cursor / VS Code**: OAuth 2.1 + PKCE, DCR or CIMD, static bearer headers; Cursor ignores header config when OAuth discovery is advertised; Copilot CLI always does DCR.

## 7. Pitfall checklist

1. DCR client explosion → offer CIMD.
2. WAF/User-Agent filtering breaks `/register` (Claude uses `python-httpx`; egress `160.79.104.0/21`).
3. `WWW-Authenticate` only honored on 401; missing PRM = "Couldn't reach the MCP server."
4. `/token` must parse form-urlencoded (JSON-only ⇒ 415).
5. Rotate refresh tokens; `invalid_grant` on dead ones.
6. Consent screen must display the redirect-URI hostname; no M2M.
7. HTTPS everywhere.
8. Support the full 401 → PRM → AS-metadata chain for mixed-vintage clients.
9. Register both the claude.ai callback and Claude Code's port-agnostic loopback forms.
10. Entra ID: Application ID URI required (AADSTS9010010).
11. Latency budgets 10 s / 30 s.
12. Missing annotations ⇒ per-use friction and Directory rejection.
