# The /api/v1 save API and first-party OAuth clients

Status: deployed production behavior in Fly release 112, verified 2026-08-13.
Implementation verified against `internal/hub/apiv1.go`,
`internal/hub/apiv1_files.go`, `internal/hub/oauth_firstparty.go`,
`internal/hub/oauth_store.go`, and `cmd/afs-hub/main.go` on 2026-08-09.

`/api/v1/*` is the Hub's surface for first-party **browser** apps. Its first
consumer is the Markdown To playground (`markdownto.ai`), which has no user table
and no file store of its own: it signs its users in through this Hub's OAuth
authorization server and saves what they make into ordinary agentsfs instances
here.

It is the fourth wrapper over the capability core in `internal/hub/repoaccess.go`,
after the git remote, the agent API (`/api/agent/v1/*`), and the MCP server
(`/mcp`). It holds no access logic of its own.

## What makes it different from the agent API

| | `/api/agent/v1` | `/api/v1` |
| --- | --- | --- |
| Caller | a hosted agent (server-side) | a web page |
| Credential | PAT | OAuth access token (PAT also accepted) |
| Conflict unit | a git revision (`baseRev`) | the file's bytes (`If-Match: <sha256>`) |
| Cross-origin | no | yes, for declared origins |
| Body | JSON with content inline | the raw file bytes |

The content-addressed conflict model is the point. An agent reasons about a
revision; an editor reasons about the bytes it has open. The Markdown To patch
engine already refuses any mutation whose `expect` hash does not match the source
it was computed from, so the same hash travels from the editor's buffer to the
git commit and back.

## Scopes

`internal/hub/oauth_store.go` defines six scopes in one canonical order. The two
MCP scopes lead, so an existing MCP grant still serializes to exactly
`afs:read afs:write`.

| Scope | Grants |
| --- | --- |
| `afs:read` / `afs:write` | the `/mcp` tool surface (unchanged) |
| `profile` | `GET /api/v1/me` |
| `instances:read` | listing instances, listing documents, reading file bytes |
| `instances:write` | saving files, bootstrapping an instance |
| `sharelinks:create` | minting a share link for one file |

The save-API scopes are deliberately disjoint from the MCP scopes: a token minted
for a browser editor is not a tool-surface token. `normalizeScope("")` still
defaults to the two MCP scopes, because an MCP client omits `scope` and expects
the full tool surface — that default must never widen as scopes are added.

A **PAT** carries `profile instances:read instances:write` on this surface and
never `sharelinks:create`. Publishing a private file at an anonymous URL is the
action a prompt-injected agent would be steered into, and PATs live on machines
that read untrusted input; the web share page has always required a human at a
browser for the same reason. An OAuth token may hold the scope because a human
read "Create share links" on the consent screen and left the box ticked.

## First-party clients

`internal/hub/oauth_firstparty.go` declares clients in code; `OpenAccounts`
reconciles them into `oauth_clients` on every start (upsert, so code is the source
of truth and a hand-edited row is put back). A static browser app cannot keep a
secret and must not re-register itself on each deploy, so its `client_id` is
stable and its redirect allow-list is declared here rather than discovered.

```text
client_id     markdownto.ai         (kind "first-party", alongside "dcr" and "cimd")
name          Markdown To
redirect_uris https://markdownto.ai/app/
              https://www.markdownto.ai/app/
              http://localhost:8080/app/     (RFC 8252: the port may vary)
              http://127.0.0.1:8080/app/
```

Everything else about it is an ordinary public client: authorization code +
PKCE-S256, `token_endpoint_auth_method: none`, exact redirect matching, the same
consent screen. Adding an app is one entry in `firstPartyClients`.

Token lifetimes are the AS-wide values in `oauth_store.go`: access 2 h, refresh
30 d, refresh rotating with family-wide revocation on reuse.

## Endpoints

Errors are the Hub's usual `{"error": "..."}` at the status named, except the two
precondition failures, which carry the current hash so a client can recover in one
step. `404` is returned in preference to `403` whenever the caller has no access
at all, so the API never confirms that an instance exists.

| Method | Path | Scope |
| --- | --- | --- |
| GET | `/api/v1/me` | `profile` |
| GET | `/api/v1/instances` | `instances:read` |
| POST | `/api/v1/instances` | `instances:write` |
| GET | `/api/v1/instances/{owner}/{instance}/files` | `instances:read` |
| GET, HEAD | `/api/v1/instances/{owner}/{instance}/files/{path}` | `instances:read` |
| PUT | `/api/v1/instances/{owner}/{instance}/files/{path}` | `instances:write` |
| POST | `/api/v1/instances/{owner}/{instance}/transactions` | `instances:write` |
| POST | `/api/v1/instances/{owner}/{instance}/sharelinks` | `sharelinks:create` |

A missing scope is `403` with `WWW-Authenticate: Bearer error="insufficient_scope",
scope="<the one required>"`.

### Reading a file

Raw bytes as `application/octet-stream` with `nosniff` — this endpoint must never
become a way to have the Hub render attacker-controlled HTML on its own origin.
`ETag: "<sha256>"`, `X-Afs-Source-Hash`, `X-Afs-Rev`, and `X-Afs-Head` ride along;
`?rev=` pins a revision. Over `maxFileBytes` (8 MiB) is `413` — that file belongs
on the git remote, which has no such limit.

### Saving a file

Body is the raw bytes. The commit subject comes from `X-Afs-Message` (or
`?message=` for a caller with no way to set a header), defaulting to
`Add|Update <basename>`, and a `Via: Markdown To (markdownto.ai)` trailer records
the app. The author is the user; the committer is the Hub, as everywhere else.

Ordinary writes retain the 8 MiB limit. One narrow media exception supports hosted
`narrate@0.1` production: an `audio/mpeg` PUT whose path is exactly
`…/narrate/<manuscript>/<generation>/<file>.mp3` may be at most 128 MiB. Hub stores those bytes
in its existing Git LFS object store and commits the standard pointer, so clone/pull and `/raw/`
retain normal LFS behavior while a browser never places an hour-long MP3 in Git history. Merely
setting the content type on another path does not opt it into the exception.

| Condition | Result |
| --- | --- |
| `If-Match: <hash>` matches the current bytes | `200`, a new commit |
| file absent, no `If-Match` | `201`, a new commit |
| `If-Match: *`, file present | `200` |
| `If-Match` does not match, or file absent | `412` `{"error":"hash mismatch","hash":<current>,"rev":<head>,"why":…}` |
| file present, no `If-Match` | `428` `{"error":"if-match required","hash":<current>,…}` |
| HEAD moved onto this path mid-save | `412`, same shape |

The `428` is stricter than HTTP requires and deliberately so: a first save has no
hash to offer, but every later one does, and an unconditional PUT is exactly the
silent overwrite the hash model exists to prevent.

Response: `{owner, instance, path, hash, rev, created, merged, instanceCreated,
collection, url}`. `hash` is the saved bytes' new hash — the client's `If-Match`
for its next save, with no round trip through `GET`.

### Saving a workspace transaction

`POST …/transactions` is the directory-shaped counterpart to file `PUT`. Its JSON body carries
an ordered `changes` manifest; every row has a repository-relative `path`, a `beforeHash` (SHA-256,
or `null` when the path must be absent), and `after` (UTF-8 text, or `null` to delete). `primary`
names the file whose canonical Hub URL the response should return. Every before-hash is checked
before the Hub writes anything, and the entire manifest becomes one attributed Git commit through
the same revision-CAS boundary as the agent API.

A stale, unexpectedly present, or unexpectedly absent member returns `412` with
`{error, why, rev, conflicts:[{path, expected, current}]}` and changes nothing. A success returns
`{owner, instance, rev, merged, instanceCreated, primary, url, files}`; each file row carries its
new hash or `deleted:true`. The endpoint accepts at most 4,096 files, 8 MiB per file, and 64 MiB per
request. It exists so a `backlog-workspace@0.1` save can never expose a new note without its spine
link, or a rewritten spine without the note it names.

### Bootstrapping an instance

`POST /api/v1/instances` (empty body allowed) ensures `apps` exists for the
caller, with `apps/` declared `agentsfs_role: collection`. It is **idempotent** —
unlike the agent API's create, which `409`s on a taken slug — because it is the
call a client makes at sign-in. A `PUT` into an instance that does not exist yet,
in the caller's own namespace, runs the same path implicitly, which is the
contract's zero-decisions default: the user chooses nothing and gets a normal
agentsfs.

The collection declaration is what keeps the result contract-clean. A saved
document carries a `markdownto:` envelope and no `description:` of its own, which
is a violation anywhere except inside a declared collection.
`TestAPIV1SavedInstancePassesDoctor` materializes a bootstrapped instance and runs
the real `core.Doctor` over it.

### Listing documents

`GET …/files` lists the **markdown** documents at HEAD: `?dir=` scopes to a
subtree, `?markdownto` narrows to files carrying an envelope, and
`?markdownto=kanban` or `?markdownto=kanban@1` narrows to a spec (name-only
matching is version-agnostic). Each entry carries `path, name, size, hash,
markdownto, title, description, url`.

Markdown-only is a scope, not an oversight: the value of the listing is what a
document says about itself and the hash needed to open it for editing, neither of
which exists for a PNG. Every blob of every type stays listable through the agent
API's tree endpoint, the web UI, and a clone. The read is one `ls-tree` plus one
batched `cat-file`, the pair the repo pages already use.

## CORS

`writeCORS` (in `apiv1.go`) covers `/api/v1/*` and `/oauth/token`. The allowed
origin is echoed, never `*`; `Vary: Origin` is always set; a preflight is answered
before authentication, since it carries no `Authorization` header.

`Access-Control-Allow-Credentials` is **never** sent. The API is bearer-only, so a
cross-site page can never ride an ambient Hub session cookie into a knowledge
base.

Allowed: the `Origins` of every first-party client, anything in `HUB_API_ORIGINS`
(comma-separated, scheme+host, for a staging deployment of a first-party app), and
any `http` loopback origin — a dev server's port is unknowable in advance, and
without credentials a localhost page still needs a bearer token it could already
use from anywhere.

## Deployment

Nothing to migrate: the client seed is an upsert inside `OpenAccounts`, and no
schema changed. `HUB_API_ORIGINS` is optional. `HUB_PUBLIC_URL` must be set, as it
already is — the `url` fields and share links are built from it.
