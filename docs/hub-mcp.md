---
description: The Hub's remote MCP server — connect ChatGPT, Claude, and other consumer apps to your knowledge bases at hub.agentsfs.ai/mcp.
---

# The Hub MCP server

The Hub exposes a remote MCP server at `https://<hub>/mcp` (Streamable HTTP, stateless). It is the third thin wrapper over the same core the CLI and the hosted-agent API use: consumer apps that cannot run local binaries — ChatGPT, claude.ai/Desktop/mobile, Claude Code, Cursor, and any other MCP host — get search, reads, and writes over every knowledge base the signed-in user owns or collaborates on. Design and compatibility research live in the repo's own agentsfs instance: `agentsfs/rfcs/hub-mcp-server.md`.

## Connecting

**Claude (claude.ai, Desktop, mobile)** — Settings → Connectors → Add custom connector → URL `https://hub.agentsfs.ai/mcp`. Claude discovers the Hub's OAuth server automatically (CIMD or DCR), sends you through the Hub's consent page, and refreshes tokens on its own. Works on every plan (Free allows one custom connector). Claude's research mode will use the read tools automatically once connected.

**ChatGPT** — read-only research use works as a connector; read-write tools additionally require Developer Mode (Settings → Apps & Connectors → Advanced → Developer mode; Plus/Pro/Business/Enterprise/Edu). Add the connector with URL `https://hub.agentsfs.ai/mcp` and OAuth. The `search` and `fetch` tools implement ChatGPT's exact deep-research contract, so the Hub also works as a Deep Research / company-knowledge source with real citation links back to the Hub's file views.

**Claude Code / API / power users** — skip OAuth entirely and use a PAT from the Hub's `/account` page as a bearer token:

```bash
claude mcp add --transport http afs-hub https://hub.agentsfs.ai/mcp --header "Authorization: Bearer <your PAT>"
```

The Messages API's MCP connector (`mcp_servers[].authorization_token`) and Cursor's header auth work the same way.

## Tools

| Tool | What it does |
|---|---|
| `search` | Ranked section-level search across all your KBs (or one, via `repo`) — the same core retrieval pipeline `afs search` runs locally. Results carry citation URLs to the Hub web view. |
| `fetch` | Full file content by search-result id (`owner/repo/path`), with repo/rev metadata. |
| `list_kbs` | Your knowledge bases with roles, visibility, descriptions, HEAD. |
| `tree` | Depth-bounded description-free listing of one KB. |
| `write` | One git commit per call: `{repo, message, changes:[{path, content|delete}], base_rev?}`. Revision-anchored CAS — a conflict returns the new HEAD so the model re-reads and retries. Writing to a KB that doesn't exist yet under your own username creates it, seeded with the AgentsFS contract. |
| `docs` | The bundled AgentsFS docs, so a fresh model can learn the conventions before writing. |

Read tools are annotated `readOnlyHint` (Claude groups them for one-click approval; ChatGPT skips write confirmation for them). `write` appears only on connections granted the `afs:write` scope — the consent page offers a read-only downgrade.

## Access model and safety

A connection authenticates as *you* and can do exactly what you can do in the browser: owned and shared repos, collaborator roles enforced per call, public repos readable only when named explicitly — never a discovery surface. Every write is an attributed, revertible git commit (author `<user>@users.agentsfs`, committer the Hub), so anything a connected app writes is visible in history and un-doable — that, plus per-connection scopes and the hosts' own confirmation prompts, is the injection-containment story.

OAuth details, for the curious or self-hosting: the Hub is its own OAuth 2.1 authorization server (authorization-code + PKCE S256 only; no client-credentials). Discovery via RFC 9728 protected-resource metadata (`/.well-known/oauth-protected-resource[/mcp]`) and RFC 8414 / OIDC metadata; client registration via DCR (RFC 7591) or CIMD (URL client IDs, SSRF-guarded fetch); opaque tokens stored hashed with 2 h access / 30 d rotating refresh lifetimes and reuse-revokes-family semantics.

## Self-hosting

Nothing extra to run: the MCP server and OAuth endpoints ship in `afs-hub`. Set `HUB_PUBLIC_URL` to the hub's stable public origin — it anchors the OAuth issuer and the MCP resource identifier, and consumer clients require HTTPS.
