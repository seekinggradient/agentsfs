# Hub ↔ Eve integration contract

Status: deployed production behavior, verified against
`internal/hub/agent_eve.go`, `internal/hub/web.go`, and `cmd/afs-hub/main.go` on
2026-07-19.

AgentsFS Hub reverse-proxies the Vercel-hosted `agentsfs-eve` application so users
stay on `hub.agentsfs.ai/agent/`. Hub remains the identity, permissions, repository,
thread, and usage authority. Eve owns the agent application and calls back to Hub's
PAT-authenticated agent API.

Production currently sets:

```text
HUB_EVE_AGENT_URL=https://agentsfs-eve-staging.vercel.app
HUB_EVE_AGENT_SECRET=<shared secret also configured on Eve>
```

Despite the Vercel project name, that stable alias is the upstream used by the
production Hub.

## Mode selection

`cmd/afs-hub/main.go` copies the two environment variables into `AgentManager`:

| Variable | Behavior |
| --- | --- |
| `HUB_EVE_AGENT_URL` | Non-empty selects hosted-Eve mode. `/agent/*` bypasses Sprite provisioning and proxies to this trusted upstream. A URL base path, if present, is preserved. |
| `HUB_EVE_AGENT_SECRET` | HMAC-SHA256 key for the short-lived identity handoff. Production must set it; Hub only logs a warning if it is missing. |

`AgentManager.EveMode()` is exactly `EveURL != ""`. With no Eve URL, Hub retains
the old Sprite/dev behavior. `HUB_AGENT_DEV_URL` is a separate local-development
override and must not be used on a deployed Hub.

## Browser and upstream paths

The deployed Eve app has Next.js `basePath: "/agent"`, but Eve's own service routes
remain rooted at `/eve/*` on Vercel. This split is why the proxy does not use one
blanket prefix rule.

| Browser → Hub | Hub → Eve upstream |
| --- | --- |
| `/agent` | Hub sends `302 /agent/` before proxying |
| `/agent/` | `/agent` |
| `/agent/_next/*` | `/agent/_next/*` |
| `/agent/api/*` | `/agent/api/*` |
| other shell routes under `/agent/*` | unchanged under `/agent/*` |
| `/agent/eve/*` | `/eve/*` |
| `/.well-known/workflow/*` | unchanged |

Queries are preserved. If `HUB_EVE_AGENT_URL` contains a mount path, that path is
joined ahead of the mapped path.

The `/agent/eve/*` rewrite is the important deployed reality: for example,
`/agent/eve/v1/session` becomes `/eve/v1/session`. Earlier documentation claimed
that the entire prefix was forwarded unstripped; that design was disproved by the
live Vercel layout and is historical.

`/.well-known/workflow/*` is claimed only in Eve mode and is protected by the Hub
web-session gate. In the current Vercel topology, workflow callbacks go directly to
the Eve deployment and do not traverse Hub, so this Hub route is normally unused. It
exists for a possible self-hosted reverse-proxy fallback. That fallback would need a
server-to-server auth gate before it could be used; a workflow callback does not carry
a user's Hub cookie.

The routing behavior is exercised by `internal/hub/agent_eve_test.go`.

## Hub authentication handoff

Every proxied request has already passed Hub's browser-session authentication. Before
sending it upstream, `AgentManager.EveProxy`:

1. Removes `Cookie`; the Hub login session never leaves Hub.
2. Removes any inbound `X-AFS-User`, `X-AFS-Expiry`, `X-AFS-Signature`, and
   `X-AFS-PAT` values so a browser cannot spoof identity or smuggle a credential.
3. Injects a five-minute signed user identity.
4. Injects the authenticated user's persisted `agent-user` PAT when available.
5. Sets `Accept-Encoding: identity` and streams with `FlushInterval = -1`.

The signed fields are:

- `X-AFS-User`: authenticated Hub username;
- `X-AFS-Expiry`: base-10 Unix seconds;
- `X-AFS-Signature`: lowercase hexadecimal HMAC.

The exact signing contract is:

```text
hex(HMAC_SHA256(HUB_EVE_AGENT_SECRET,
                X-AFS-User + "|" + X-AFS-Expiry))
```

Eve verifies the signature with a constant-time comparison. Its current verifier also
allows at most 60 seconds of expiry skew and rejects an expiry more than 600 seconds in
the future. A valid signed identity is the authority for trusting the accompanying
PAT; Eve never uses a proxied Hub cookie as identity.

The Eve-side implementations are `lib/hub-auth.ts` for Next API requests and
`agent/channels/eve.ts` for `/eve/v1/*` session requests.

## Durable per-user PAT

The HMAC proves who the browser user is. Calls from Eve back to Hub additionally need
a normal AgentsFS PAT.

Hub lazily creates one PAT per user with the account label `agent-user` and sends its
plaintext as `X-AFS-PAT`. The plaintext must be stable across requests and restarts,
so Hub stores it at:

```text
<hub-data-root>/.agent-pats.json
```

`AgentPATStore` writes the file atomically with mode `0600` and re-reads it on each
request. The accounts database retains the PAT hash, like every other AgentsFS PAT.

Eve captures the PAT in its accepted session auth context. Later tool or hook work can
therefore call Hub after the initiating HTTP request has ended or a durable approval
has resumed. The current auth seed refreshes on a new authenticated request, allowing
a rotated PAT to take effect.

If Hub cannot provide a PAT, it omits the header and Eve can use its configured Hub PAT
fallback. That is a degradation path for tests or misconfiguration, not the desired
multi-user production mode.

### Rotate one user's agent PAT

Both sides of the stored credential must be removed:

1. Delete the user's entry from `.agent-pats.json`.
2. Revoke that user's `agent-user` PAT in the accounts store.

The next proxied request mints and persists a replacement. Revoking only the hashed
account token leaves Hub injecting a dead plaintext token; deleting only the plaintext
entry leaves the old token valid but unused.

## Proxy response contract

Eve is a trusted first-party upstream, not a user-controlled Sprite. The proxy
therefore preserves the upstream `Content-Type` and any `Content-Encoding` from the
small supported set while rebuilding the rest of the response headers.

The hardener:

- drops `Set-Cookie`, CORS headers, redirects (`Location`/`Refresh`),
  `Clear-Site-Data`, and other origin-affecting headers;
- sets `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`, and same-origin framing/resource policies;
- sets `X-Accel-Buffering: no` for streaming;
- passes through `X-Eve-*` response headers because Eve's client needs its session
  cursor headers to create and resume sessions.

The Hub's agent dock embeds Eve in a same-origin iframe, so the response uses
`X-Frame-Options: SAMEORIGIN` and `frame-ancestors 'self'`, not `DENY`.

The proxy deliberately drops upstream redirects. Any future Eve OAuth or connection
flow that depends on server-side `Location` must add an explicit, safe same-origin
rewrite before that flow is enabled.

## Eve → Hub data plane

Eve uses the injected PAT as `Authorization: Bearer <afs_...>` against
`/api/agent/v1/*`. Hub resolves the PAT to a user before dispatching any route.

| Endpoint | Purpose |
| --- | --- |
| `GET /api/agent/v1/repos` | List repositories the user owns or collaborates on, including owner, role, and HEAD. |
| `POST /api/agent/v1/repos` | Create a private repository in the caller's namespace and seed the AgentsFS contract template. |
| `GET /api/agent/v1/repo/<owner>/<repo>/resolve` | Resolve and pin the current revision. |
| `GET /api/agent/v1/repo/<owner>/<repo>/file` | Read a jailed path at a revision. |
| `GET /api/agent/v1/repo/<owner>/<repo>/tree` | Read a directory tree at a revision. |
| `GET /api/agent/v1/repo/<owner>/<repo>/search` | Search and return revision-aware result metadata. |
| `POST /api/agent/v1/commit` | Apply path-jailed changes with compare-and-swap semantics. |
| `GET /api/agent/v1/threads` | List the caller's thread summaries derived by Hub. |
| `GET`, `PUT`, `DELETE /api/agent/v1/thread/<id>` | Read, persist, or delete one opaque thread record. |
| `GET`, `POST /api/agent/v1/thread/<id>/events` | Read or append the idempotent mixed Eve/voice archive. |
| `POST /api/agent/v1/usage` | Attribute Eve model usage to the PAT's user in Hub metrics. |

Repository access follows the browser user's permissions:

- the listing contains owned and explicitly shared repositories;
- an explicitly named public `owner/repo` may be read even if it is not in the
  listing;
- owners and write collaborators may commit;
- read collaborators and public-only readers cannot commit;
- inaccessible repositories return `404` to avoid leaking existence;
- there is no repository-deletion endpoint in the agent API.

Reads resolve a commit and then serve files/trees at that revision. Writes name the
`baseRev` they were reasoned against. Hub fast-forwards when HEAD is unchanged, can
merge a disjoint path change onto a moved HEAD, and returns `409` when paths overlap or
the final ref update loses a race.

Thread records are private to the PAT user and live under
`<hub-data-root>/.threads/<user>/`. Hub treats the record body as an opaque Eve-owned
JSON object but derives the thread index itself. The record's `repo` field is the
canonical knowledge-base focus shared by UI, text, and voice. The event archive stores
Eve stream events and voice entries in one chronological JSONL stream, with append
idempotency enforced server-side.

The authoritative implementations are `internal/hub/apiagent*.go` and
`internal/hub/threadstore.go`; avoid duplicating their complete JSON schemas here.

## Deployment and verification

For an Eve-only release, deploy a tested, pushed `agentsfs-eve` commit to Vercel
production. The stable Vercel alias moves to that deployment, and Hub immediately
proxies new requests to it. No Hub deployment is required while the proxy/API contract
is unchanged.

Smoke-test the release through an authenticated Hub session:

1. Load `/agent/` and confirm Next assets and app API routes succeed.
2. Create or resume an Eve session and confirm NDJSON arrives incrementally.
3. Open a thread, switch its knowledge base, reload, and confirm focus persists.
4. Perform an authorized Hub-backed read and confirm citations use the pinned revision.
5. Confirm an upstream `Set-Cookie` cannot reach the browser and the Hub session cookie
   cannot reach Eve.
6. Test with two users to confirm sessions, PATs, threads, and repository listings are
   isolated.

Changes to path mapping, authentication headers, the agent API, or Fly secrets require
a coordinated Hub deploy and the relevant Go tests.

## Rollback

For an Eve application regression, promote the last known-good Vercel deployment back
to the stable alias. This leaves Hub and its data untouched.

For a full topology rollback, unset `HUB_EVE_AGENT_URL` and redeploy Hub.
`EveMode()` becomes false and `/agent/*` uses the legacy Sprite implementation, if its
credentials and provisioning path remain available. The `/.well-known/workflow/*`
route is no longer claimed. No Hub repository or thread data migration is required.

## Historical notes

The original rollout plan proposed forwarding `/agent/eve/*` unstripped to an Eve
service mounted below `/agent`. The deployed Vercel integration instead serves Eve's
service at root, so Hub maps that one path family to `/eve/*`. The original staging
rollout, subdomain alternative, and self-host-on-Sprite evaluation are complete
architecture history, not open production blockers.

Fly volume snapshots and Litestream are Hub-wide backup concerns, not part of the
Hub/Eve wire contract. Keep their runbooks with deployment/backup operations rather
than embedding speculative, unapplied commands in this integration document.
