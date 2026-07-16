# Hub ↔ hosted-Eve integration (reverse-proxy mode)

This is the Hub-side implementation of `eve-hosting.md` (in the eve-migration
research tree) Decision 5, shape 1: **the Hub reverse-proxies a Vercel-hosted Eve
app** so users
keep landing on `hub.agentsfs.ai/agent/`, the Hub stays the identity + git home,
and the Eve deployment stays behind the Hub's session auth.

It is a **flag-gated sibling** of the existing per-user sprite proxy. Both modes
live in the same code; `HUB_EVE_AGENT_URL` selects between them. When the flag is
unset, behavior is byte-for-byte the sprite path (proven by
`TestSpritePathUnchangedWhenEveFlagUnset`).

## Environment variables

| Env | Meaning |
| --- | --- |
| `HUB_EVE_AGENT_URL` | Base URL of the hosted Eve deployment (e.g. `https://agentsfs-eve.vercel.app`). **Empty ⇒ sprite mode, unchanged.** Non-empty ⇒ Eve upstream mode: no sprite lookup/provisioning, no embedded UI, no starting page. May include a base path (a mount prefix), which is preserved ahead of the forwarded path. |
| `HUB_EVE_AGENT_SECRET` | Shared HMAC-SHA256 key for the identity handoff the Eve app verifies. If unset while `HUB_EVE_AGENT_URL` is set, the Hub logs a warning and signs with an empty key (do not run production this way). |

Wiring: `cmd/afs-hub/main.go` reads both after `NewAgentManager`. Selection logic:
`AgentManager.EveMode()` (`internal/hub/agent_eve.go`) returns `HUB_EVE_AGENT_URL != ""`.

## Path mapping

### Evidence — what the Eve browser client actually requests

Determined from the compiled eve client (`eve@0.22.6`), not guessed:

- **All browser routes are `/eve/v1/*` joined onto a configurable base URL.**
  `dist/src/protocol/routes.js` defines `EVE_ROUTE_PREFIX = "/eve/v1"` and every
  route constant (`/eve/v1/health`, `/eve/v1/info`, `/eve/v1/session`,
  `/eve/v1/session/:id`, `/eve/v1/session/:id/stream`,
  `/eve/v1/connections/:name/callback/:token`, `/eve/v1/callback/:token`).
- **The base URL (`host`) is fully configurable.** `dist/src/client/client.js`
  (`Client`) and `dist/src/client/session.js` / `open-stream.js` build every
  request URL with `createClientUrl(host, route)` (`dist/src/client/url.js`),
  which concatenates `host`'s pathname with the route. `dist/src/react/use-eve-agent.js`
  → `resolveEveAgentHost` (`dist/src/client/agent-host.js`) returns `host ?? ""`
  (default: same-origin absolute `/eve/v1/*`) or `/eve/agents/<name>` for named
  agents. **No client code hardcodes an absolute origin.** So setting
  `host = "/agent"` makes every browser request `/agent/eve/v1/*`.
- **`/.well-known/workflow/*` is never a browser path.** `WORKFLOW_ROUTE_BASE =
  "/.well-known/workflow/v1"` lives only in `@workflow/utils`, the Nitro host
  route config (`dist/src/internal/nitro/host/configure-nitro-routes.js`), and
  the workflow-world packages — never in `dist/src/client/*` or `dist/src/react/*`.
  It is the workflow-world → eve-app callback (`/.well-known/workflow/v1/flow`).

### The constraint (important)

The current `agentsfs-eve` app uses `withEve(nextConfig)` with **no `basePath`**
and `useEveAgent()` with **no `host`**, so its browser client emits **absolute**
`/eve/v1/*`, its Next.js shell is served at `/`, and its framework assets are at
`/_next/*`. **A root-served Eve app cannot be prefix-hosted under `/agent/`,
because the Hub owns `/` (the dashboard) and cannot cede `/`, `/_next/*`, or
`/eve/v1/*` at its root** (`/eve/*` would also shadow a user named `eve`, which is
not a reserved name).

Therefore the reverse-proxy shape **requires the Eve app to be prefix-aware**:

```ts
// agentsfs-eve/next.config.ts
export default withEve({ basePath: "/agent" }, { servicePrefix: "/agent/_eve_internal/eve" });
// agentsfs-eve chat component
const agent = useEveAgent({ host: "/agent" /* → requests /agent/eve/v1/* */ });
```

With that, the entire browser surface (shell, `/agent/_next/*`, `/agent/eve/v1/*`)
lives under `/agent/` and maps cleanly. **If the Eve app cannot be made
basePath-aware, use the subdomain-handoff alternative** (Decision 5 shape 2:
`agent.agentsfs.ai` with the Hub minting the same signed token as a channel
`auth` policy) — the identity handoff in this doc is designed to work unchanged
for that path.

### What the Hub proxy forwards (in Eve mode)

| Incoming (browser → Hub) | Forwarded (Hub → Eve upstream) | Auth gate |
| --- | --- | --- |
| `/agent`               | 302 → `/agent/`                | session |
| `/agent/`              | `/agent/`                      | session |
| `/agent/eve/v1/*`      | `/agent/eve/v1/*`              | session |
| `/agent/_next/*`, any `/agent/<x>` | `/agent/_next/*`, `/agent/<x>` (**un-stripped**) | session |
| `/.well-known/workflow/*` | `/.well-known/workflow/*` (un-stripped) | session |

**The `/agent` prefix is forwarded UN-stripped.** The Eve app runs with
`basePath: "/agent"`, so its shell, its `/agent/_next/*` framework assets, and its
`/agent/eve/v1/*` API/stream are all served under `/agent` **on the upstream too**.
Stripping the prefix (an earlier design) would 404 every request against the
basePath-aware app. The only rewrite the proxy still performs is the `/agent` →
`/agent/` trailing-slash redirect. Query strings are preserved.
`HUB_EVE_AGENT_URL`'s own base path (if any) is still prepended to the forwarded
path (so a mount prefix `…/mounted` yields `/mounted/agent/eve/v1/*`).

**On `/.well-known/workflow/*`:** in the Vercel-hosted topology this callback is
server-to-server (Vercel Workflow → the eve deployment's own origin) and **does
not traverse the Hub**, so this route is a no-op there. It is forwarded anyway to
honor eve's documented reverse-proxy contract ("forward **both** `/eve/` and
`/.well-known/workflow/`" — eve `docs/guides/deployment.md` §8, the run-callback
gotcha) and to keep the **self-hosted-Eve-behind-the-Hub fallback** (Decision 1 option A)
viable. Caveat: that fallback's callback is server-to-server and carries no Hub
session cookie, so if it is ever exercised the gate on this route must change from
the session cookie to the HMAC secret (see Open issues).

## What the proxy does per request (`AgentManager.EveProxy`)

1. Forwards the incoming path **un-stripped** (the basePath-aware Eve app owns the
   whole `/agent/*` surface; the top-level `/.well-known/workflow/*` callback is
   likewise forwarded unchanged). Any base path on `HUB_EVE_AGENT_URL` is joined
   ahead of it.
2. **Deletes inbound `X-AFS-User` / `X-AFS-Signature` / `X-AFS-Expiry`** so a
   crafted request can never spoof identity, then injects the Hub's own.
3. **Deletes `Cookie`** — the Hub session cookie must never leave the Hub.
4. Sets `Accept-Encoding: identity` (the response hardener rebuilds headers; an
   unpreserved encoding would corrupt the body).
5. Streams with `FlushInterval = -1` so NDJSON/SSE frames flush immediately.
6. Hardens the response (`hardenEveProxyResponse`).

### Response hardening — deliberate differences from `hardenAgentProxyResponse`

The sprite hardener defends against a **user-controlled VM** serving
agent-authored preview HTML, so it forces a per-route `Content-Type` allowlist and
rejects all 3xx. The Eve upstream is **our own trusted deployment**, so
`hardenEveProxyResponse`:

- **Preserves the upstream `Content-Type`** (the app serves `text/html`,
  `text/css`, `text/javascript`, `application/json`, and — required —
  `application/x-ndjson` streams). It still sets `X-Content-Type-Options: nosniff`.
- Rebuilds the header map from scratch, dropping `Set-Cookie`, CORS
  (`Access-Control-*`), `Location`, `Refresh`, `Clear-Site-Data`, and any other
  origin-affecting header.
- Forces `Cache-Control: no-store`, `Referrer-Policy: no-referrer`,
  `Cross-Origin-Resource-Policy: same-origin`, `X-Frame-Options: DENY`,
  `X-Accel-Buffering: no`.
- Does **not** apply the sprite CSP (that CSP is for sandboxing agent-authored
  preview docs; the Eve app ships its own CSP posture as a first-party app).

## Verification contract the Eve app MUST implement

The hosted Eve app authenticates the Hub via a signed identity handoff on **every
proxied request**. Its channel `auth` policy (an eve `AuthFn`) must:

1. Read three headers:
   - `X-AFS-User`: the Hub username (the vouched-for identity).
   - `X-AFS-Expiry`: Unix seconds (base-10 ASCII).
   - `X-AFS-Signature`: lowercase hex.
2. Recompute and constant-time-compare:

   ```
   signature = hex( HMAC_SHA256( key = HUB_EVE_AGENT_SECRET,
                                 message = X-AFS-User + "|" + X-AFS-Expiry ) )
   ```

   The message is the **exact ASCII** `user + "|" + expiry` (e.g. `alice|1752… `).
   Reference: `eveSignature` in `internal/hub/agent_eve.go`.
3. Reject if the signature mismatches, or if `now > X-AFS-Expiry` allowing a
   **clock-skew tolerance of ≤ 60 s** (the Hub sets expiry ~300 s out, so a
   60 s allowance is comfortable). Do not accept an expiry unboundedly far in the
   future — reject `X-AFS-Expiry - now > 600 s` as malformed.
4. On success, treat `X-AFS-User` as the authenticated principal for session
   scoping. The app must **not** trust any `Cookie` (the Hub strips it) and must
   **not** trust these headers from any source other than the Hub (lock the
   deployment to Hub-only ingress, or additionally pin a network allowlist).

Node sketch for the Eve side:

```ts
import { createHmac, timingSafeEqual } from "node:crypto";
function verifyHubHandoff(headers: Headers, secret: string): string | null {
  const user = headers.get("x-afs-user");
  const expiry = headers.get("x-afs-expiry");
  const sig = headers.get("x-afs-signature");
  if (!user || !expiry || !sig) return null;
  const exp = Number(expiry);
  const now = Math.floor(Date.now() / 1000);
  if (!Number.isFinite(exp) || now > exp + 60 || exp - now > 600) return null;
  const want = createHmac("sha256", secret).update(`${user}|${expiry}`).digest("hex");
  const a = Buffer.from(sig), b = Buffer.from(want);
  if (a.length !== b.length || !timingSafeEqual(a, b)) return null;
  return user;
}
```

## Staging rollout

1. Deploy `agentsfs-eve` to Vercel behind its own allowlist. Configure
   `basePath:"/agent"` + `useEveAgent({ host:"/agent" })` (see the constraint).
   Set the Eve channel `auth` to the handoff verifier above, keyed on
   `HUB_EVE_AGENT_SECRET`. Model routing: interim via the Hub LLM proxy (metering
   parity) or AI Gateway per eve-hosting Decision 4.
2. Generate a strong shared secret; set it on both sides
   (`HUB_EVE_AGENT_SECRET` on the Hub, the same value in the Eve deployment env).
3. On a **staging Hub**, set `HUB_EVE_AGENT_URL` to the Eve deployment URL and
   `HUB_EVE_AGENT_SECRET`. Deploy.
4. Smoke test through the Hub (authenticated browser session):
   `GET /agent/eve/v1/health` (200), a real turn via `POST /agent/eve/v1/session`,
   then attach `GET /agent/eve/v1/session/<id>/stream` and confirm NDJSON frames
   arrive incrementally (no buffering). Confirm no `Set-Cookie` from the upstream
   reaches the browser and the session cookie never appears upstream.
5. Validate durability under redeploy and per-user session isolation (two Hub
   users must not see each other's sessions — the Eve app scopes by `X-AFS-User`).
6. Roll to production for opted-in users, then everyone (eve-hosting Decision 7
   stage 4). The sprite fleet stays available as the flag-unset fallback until
   cutover completes.

## Rollback

**Unset `HUB_EVE_AGENT_URL` and redeploy the Hub.** `EveMode()` goes false,
`/agent/*` reverts to the sprite path with zero code change, and the
`/.well-known/workflow/*` route stops being claimed. No data migration.

## Open issues / follow-ups

- **Eve app must be basePath-aware.** Tracked above; the alternative is the
  subdomain handoff. Blocks the reverse-proxy shape until done on the Eve side.
- **Upstream redirects are dropped.** `Location`/`Refresh` are stripped, so if the
  Eve app relies on server-side 3xx (e.g. OAuth `connections` callbacks in a later
  phase), that flow needs explicit same-origin `Location` rewriting before Connect
  is enabled. Text chat (rollout stage 2) needs no redirects.
- **`no-store` on assets.** Framework assets under `/agent/_next/*` are forced
  `no-store`; acceptable for an SPA (one shell load) but a future optimization
  could allow immutable caching for hashed asset paths.
- **Self-host fallback auth on `/.well-known/workflow/*`.** If Decision 1 option A
  is ever used, that route's gate must switch from the session cookie to the HMAC
  secret (server-to-server callback has no cookie).
- **Metering.** In Eve mode the Hub no longer sees model calls on the sprite LLM
  proxy path; per-user cost attribution moves to the eve-hosting Decision 4 hook
  (`defineHook` posting usage to a Hub ingest endpoint). Out of scope here.
```
