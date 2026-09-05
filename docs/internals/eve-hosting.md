# Hosting Eve with AgentsFS Hub

Status: current production architecture, verified 2026-07-19.

The hosted agent is the `agentsfs-eve` Next.js/Eve application on Vercel. AgentsFS
Hub remains the public entry point, identity provider, workspace store, and
authorization boundary. The old per-user Fly Sprite agent is retained only as a
configuration fallback.

The Vercel project is currently named `agentsfs-eve-staging`, but its stable
production alias is the upstream used by Hub:

```text
https://agentsfs-eve-staging.vercel.app
```

The name is historical. Treat that alias as production until separate staging and
production projects are introduced.

## Responsibilities

| Component | Owns |
| --- | --- |
| AgentsFS Hub on Fly | Browser login, user identity, repositories and LFS, collaborators and permissions, thread records and archives, usage records, the public `/agent/` URL, and the authenticated reverse proxy to Eve |
| `agentsfs-eve` on Vercel | Agent UI, Eve durable turns, tools, approvals, citations, workspace focus UX, and the text/voice bridge |
| Vercel AI Gateway | Text-model routing and realtime voice sessions |

Hub data stays canonical. Eve reads and writes repositories, threads, and usage
through Hub's PAT-authenticated `/api/agent/v1/*` API. Workspace focus is the
`repo` field in the Hub-backed thread record; it is not process-global agent state.

## Production request flow

1. A signed-in browser opens `https://hub.agentsfs.ai/agent/`.
2. Hub authenticates the Hub session and proxies `/agent/*` to the configured Eve
   deployment.
3. Hub removes its session cookie and any client-supplied `X-AFS-*` identity
   headers, then injects a short-lived HMAC-signed identity and that user's agent
   PAT.
4. Eve verifies the signed identity, captures the PAT for durable work, and serves
   the UI or Eve/Next API response.
5. Eve uses the PAT to call `https://hub.agentsfs.ai/api/agent/v1/*` for data that
   Hub owns.

The exact path mapping and header contract are documented in
[eve-hub-integration.md](eve-hub-integration.md).

## Deployment model

### Eve-only changes

UI, prompt, tool, workflow, and voice changes normally require only an
`agentsfs-eve` deployment:

1. Test and build the intended `agentsfs-eve` commit.
2. Deploy that commit to Vercel production.
3. Confirm the stable alias above now resolves to the new deployment.
4. Smoke-test through the Hub URL, not only the direct Vercel URL.

Hub already points at the stable alias, so new Hub requests use the promoted Eve
version immediately. No Fly deployment is needed. Existing browser tabs may need a
refresh to load new Next.js assets; an in-flight durable Eve turn may finish on the
deployment that accepted it.

Prefer deploying a clean checkout of the pushed commit. A Vercel deployment from a
dirty working tree can accidentally publish local changes that were not reviewed or
pushed.

### Hub contract changes

A Hub deployment is required when a change modifies any of these:

- reverse-proxy routing or response hardening;
- the `X-AFS-*` identity/PAT handoff;
- `/api/agent/v1/*` behavior or storage;
- Hub configuration or Fly secrets;
- Hub-owned login, permissions, repositories, threads, or usage accounting.

Coordinate Hub and Eve changes when their wire contract changes. Deploy the
backward-compatible side first, verify both versions can interoperate, and only then
remove compatibility code.

## Configuration

Hub reads these variables in `cmd/afs-hub/main.go`:

| Variable | Production meaning |
| --- | --- |
| `HUB_EVE_AGENT_URL` | Selects hosted-Eve mode and supplies the trusted upstream URL. Production currently uses `https://agentsfs-eve-staging.vercel.app`. |
| `HUB_EVE_AGENT_SECRET` | Shared HMAC key used to sign the user identity sent to Eve. It must also be configured on the Eve deployment. |

When `HUB_EVE_AGENT_URL` is set, `/agent/*` bypasses Sprite provisioning. When it
is absent, Hub falls back to the legacy Sprite path if the Sprite credentials are
still configured.

## Rollback and fallback

For an Eve-only regression, redeploy the last known-good `agentsfs-eve` commit to
Vercel production so the stable alias moves back. This is the narrowest rollback and
does not require a Hub deployment.

The infrastructure fallback is to unset `HUB_EVE_AGENT_URL` and redeploy Hub.
`AgentManager.EveMode()` then becomes false and `/agent/*` returns to the legacy
Sprite implementation. This remains valid only while the Sprite credentials, bundle,
and provisioning path are operational. Hub repositories and thread storage do not
need a data migration for this switch, although feature parity between the two agent
UIs is not guaranteed.

Self-hosting Eve behind Hub remains a theoretical platform-risk fallback, not the
deployed topology. It would require persistent workflow storage and a server-to-server
authentication design for `/.well-known/workflow/*`; the current session-gated Hub
route is not sufficient for an external workflow callback.

## Historical decision record

The 2026-07 migration initially compared three possibilities: keep the bespoke agent
on per-user Sprites, self-host Eve on Sprites, or run one multi-tenant Eve app on
Vercel. Production chose the Vercel app behind Hub's reverse proxy.

The following were useful rollout alternatives but are not current production work:

- a separate `agent.agentsfs.ai` browser origin with a token handoff;
- a prefix-unaware Eve app behind a thin compatibility server;
- model traffic routed through the legacy Hub LLM proxy;
- per-session Vercel Sandbox clones as the only way to read a workspace.

Current Eve read tools use Hub's revision-aware API directly. Keep historical cost
and platform comparisons in research notes rather than treating them as an active
deployment checklist.
