---
description: Full research report (2026-07-24) on ChatGPT's requirements for third-party remote MCP servers — connector paths, exact search/fetch schemas, auth, transports, limits, pitfalls. Supporting material for [[hub-mcp-server]].
sources:
  - https://developers.openai.com/api/docs/guides/developer-mode
  - https://developers.openai.com/api/docs/mcp
  - https://developers.openai.com/api/docs/guides/deep-research
  - https://developers.openai.com/apps-sdk/build/mcp-server
---

# ChatGPT ↔ Remote MCP compatibility (research, verified July 2026)

Verbatim output of the research pass backing [[hub-mcp-server]] Appendix A. Claims carry per-item source URLs; items the researcher could not confirm against a primary source are tagged UNVERIFIED.

Scope note: ChatGPT exposes **two distinct paths** for third-party remote MCP servers, and requirements differ between them:

- **Path A — Connectors / Deep Research / "Company knowledge"**: read-only `search`+`fetch` MCP servers surfaced in the Connectors UI, Deep Research, and company-knowledge retrieval. Admin/user-manageable, no developer mode needed for read-only use.
- **Path B — Developer Mode custom connectors**: full MCP client (read **and** write tools), enabled per-user via a Developer Mode toggle.
- **Apps SDK / Plugins**: the productized, reviewable, distributable version of an MCP app, submitted to the Plugin Directory.

## 1. Adding a third-party server; plans; developer mode

- Server-side minimums (both paths): HTTPS endpoint (no local stdio); MCP over Streamable HTTP or HTTP+SSE; tools with names, descriptions, input schemas, and (for Apps SDK/company knowledge) safety annotations. Custom connectors are not verified by OpenAI. (developer-mode guide; gofastmcp.com/integrations/chatgpt)
- Plans: Developer Mode is "Available to Pro, Plus, Business, Enterprise, and Education accounts on the web" — not Free. Workspace admins can disable it or allowlist connectors. ("Team" folded into "Business" in the 2025 renaming.)
- Read-only connectors/Deep Research do not require Developer Mode.
- Developer Mode is still required for read-write as of July 2026: it "provides full Model Context Protocol (MCP) client support for all tools, both read and write."

## 2. Deep Research / company-knowledge tool contract (exact)

Must implement exactly two tools, `search` and `fetch`; both read-only; approval mode `require_approval: never` (deep-research guide).

Current authoritative result shapes (developers.openai.com/api/docs/mcp) — each tool returns `structuredContent` **and** a matching JSON string inside `content[]`:

`search`:

    {
      "structuredContent": { "results": [{ "id": "doc-1", "title": "...", "url": "..." }] },
      "content": [{ "type": "text", "text": "{\"results\":[{\"id\":\"doc-1\",\"title\":\"...\",\"url\":\"...\"}]}" }]
    }

`fetch`:

    {
      "structuredContent": { "id": "doc-1", "title": "...", "text": "full text...", "url": "https://example.com/doc", "metadata": { "source": "vector_store" } },
      "content": [{ "type": "text", "text": "{...same JSON...}" }]
    }

Field rules:
- `id` must be a **string**; the `search` id round-trips into `fetch` unchanged.
- Citations: "ChatGPT creates citation metadata only when `url` is a non-empty string" — absolute, user-openable URLs are effectively required.
- `fetch.text` carries the full document body; `metadata` is an object (may be null).

⚠ Superseded-schema trap: older guides (incl. FastMCP's) show `search` returning `{"ids": [...]}`. The current contract is the `results` array of objects. Build to `results`; it satisfies both.

## 3. Writes in general chat + UX gating

- Developer Mode chat can call arbitrary tools including writes. "Write actions by default require confirmation."
- ChatGPT honors annotations: "We respect the `readOnlyHint` tool annotation"; tools WITHOUT it are treated as write actions (⇒ confirmation prompts). Annotate every read tool `readOnlyHint: true`.
- Apps SDK requires `readOnlyHint`/`destructiveHint`/`openWorldHint` on every tool.
- UNVERIFIED: `destructiveHint`-specific UX differences; exact effect of the "I trust this application" checkbox on per-call confirmations.

## 4. Transports

- "Supported MCP protocols: SSE and streaming HTTP" (developer-mode guide). Streamable HTTP at `/mcp` is the recommended/forward path (Apps SDK docs); SSE endpoints end `/sse/`.
- UNVERIFIED: any hard session-resumability requirement.

## 5. Auth

- OAuth 2.1 Authorization Code + PKCE. PKCE mandatory.
- Discovery: ChatGPT reads `/.well-known/oauth-protected-resource` (RFC 9728) then follows to AS metadata (RFC 8414 / OIDC discovery).
- Client registration: supports "OAuth, No Authentication, and Mixed Authentication", with **Client ID Metadata Documents (CIMD)** and **DCR (RFC 7591)** "where configured". Under DCR, ChatGPT registers once per connector instance and reuses the client_id. Under CIMD it sends a document URL as the client_id (public-client `none` or `private_key_jwt`).
- DCR not strictly required (static client_id path exists) but DCR/CIMD is the smooth path; community reports the static client_id field became effectively required in some flows where DCR isn't offered.
- No-auth is allowed (normal for public read-only connectors).
- Redirect URIs observed in the wild (register exactly what the connector UI shows; these two confirmed): `https://chatgpt.com/connector_platform_oauth_redirect`, `https://chatgpt.com/backend-api/aip/connectors/links/oauth/callback`. Exhaustiveness UNVERIFIED.
- Bearer token on every call; validate expiry/audience; 401 on bad tokens.

## 6. Apps SDK status

- GA, open third-party submissions; App Directory merged into the **Plugin Directory** on 2026-07-09. `_meta[openai/visibility]` deprecated for `_meta.ui.visibility` on 2026-07-21.
- Submission requires verified identity, public production MCP URL, domain verification, accurate annotations, demo credentials, 5 positive + 3 negative test cases.
- For a workspace product: same `search`/`fetch` contract powers connectors, Deep Research, and Company Knowledge — Apps SDK adds only distribution/custom UI. No SDK needed to be usable.

## 7. Limits (mostly community-reported)

- Tool-definition size ~5,000 tokens (community-reported, unconfirmed by OpenAI).
- Tool responses truncated to an undisclosed budget — keep results compact, chunk large fetches.
- ~60 s request timeout (community-reported) — return fast.
- No published tool-count cap or MCP-specific rate limits found (UNVERIFIED).

## 8. Pitfalls checklist

1. Redirect-URI exact-match failures are the #1 OAuth issue (trailing slash counts).
2. Non-string ids break fetch.
3. No `url` ⇒ no citation.
4. Must dual-encode `structuredContent` + `content[].text` JSON copy.
5. Deep Research needs `require_approval: never` read-only search/fetch; servers lacking both are rejected outside Developer Mode.
6. `{ids}` vs `{results}` schema confusion (build `results`).
7. Truncation: lean tool descriptions, chunked bodies.
8. DCR/framework regressions; static client_id sometimes demanded.
9. 60 s timeout ⇒ paginate / async.
10. `/mcp` for streamable, `/sse/` for SSE; content-type sensitivity.
