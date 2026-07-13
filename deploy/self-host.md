# Run your own agentsfs Hub

The Hub is part of the open-source agentsfs project, so hosting your own is a
first-class path — a third exit ramp alongside `git clone` and an ordinary
GitHub remote. It's a single Go binary that wraps real `git`, stores plain bare
repositories and Git LFS objects on disk, and is multi-user by config. No
database, no lock-in.

## Fastest: Docker

```sh
# Build the image from the repo root
docker build -f deploy/Dockerfile -t afs-hub .

# Run it. Repos persist in the mounted volume; tokens come from the environment.
docker run -p 8080:8080 \
  -v afs-hub-data:/data \
  -e AFS_HUB_TOKENS="alice:$(openssl rand -hex 20)" \
  afs-hub
```

Then `git clone http://alice:<token>@localhost:8080/alice/notes.git` and open
`http://localhost:8080` in a browser (sign in with the token).

## From source

```sh
go install agentsfs.ai/afs/cmd/afs-hub@latest   # or: go build ./cmd/afs-hub
afs-hub --dir ~/afs-hub-data --token "alice:$(openssl rand -hex 20)"
```

It needs `git` on PATH, including `git-http-backend` (the `git` package on most
distros; on Alpine it's in `git-daemon`).

## Configuration

| Flag / env | Purpose |
|---|---|
| `--addr` | listen address (default `:8080`) |
| `--dir` | directory holding the bare repos (back this up) |
| `--token user:token` (repeatable) | grant a user a namespace |
| `AFS_HUB_TOKENS` | same, comma-separated: `alice:t1,bob:t2` |
| `--git-http-backend` | path override; auto-discovered by default |

**Multi-user:** each token maps to a `user` namespace, so `alice/notes` and
`bob/notes` are independent. Tokens are the only credential — keep them out of
any repo (clients store them in the OS git credential helper).

### Hosted agent (optional)

The Hub can offer a "Talk to an agent" button that provisions a per-user,
write-capable chat agent inside a hardware-isolated Fly Sprite. It's **off by
default** and only turns on when accounts are enabled and these Fly secrets are
set (all read from the environment):

| Env | Purpose |
|---|---|
| `SPRITES_TOKEN` | a [sprites.dev](https://sprites.dev) API token used to provision/manage the per-user sprites. **Distinct from the Fly API token.** |
| `OPENAI_API_KEY` | the shared OpenAI key. Lives **only on the hub** — it powers the `/v1/agent-llm` proxy and is never shipped to the sprites. |
| `CHAT_MODEL` | model the agent uses (default `gpt-5.6-luna`). |
| `CHAT_REASONING_EFFORT` | reasoning effort for supported models (default `high`). |
| `HUB_PUBLIC_URL` | the public base URL sprites clone from and proxy through, e.g. `https://hub.agentsfs.ai`. |

Two more per-sprite env vars are set **automatically by the sprite provisioner**
(`internal/hub/agent.go`) — not operator-configured Fly secrets:

| Env (set per sprite) | Purpose |
|---|---|
| `AGENTSFS_PREVIEW_DIR` | dir the coding agent drops a built static site into; served path-jailed at `/preview/` (sprite = `/home/sprite/workspace/.preview`). |
| `AGENTSFS_DATA_DIR` | persistent conversation-history store, one JSON file per chat, surviving cold-wakes (sprite = `/home/sprite/.agentsfs-chat`). |

The feature reports as enabled only when `SPRITES_TOKEN` + `OPENAI_API_KEY` are
present and accounts are configured; otherwise the button stays hidden and no
sprites are provisioned.

## Production notes

- **TLS.** The Hub serves plain HTTP; terminate TLS at a reverse proxy (Caddy,
  nginx, Fly, a load balancer) and forward `X-Forwarded-Proto: https`. The Hub
  uses that header for secure cookies and to render `https://` clone URLs.
- **Persistence.** Everything lives under `--dir` as ordinary git repos —
  plus a `.lfs/` object store for Git LFS media. Snapshot/back up that whole
  directory. Per-repo settings (public/private, display name) live in each bare
  repo's git config, so they travel with the data.
- **Public repos.** Work identically to the hosted instance: private by
  default; a repo goes public only after a typed confirmation in its Settings,
  after which anyone can read and `git clone` it (writes stay owner-only).
- **Fly.io** is one easy target — see [README.md](README.md); the same
  Dockerfile runs anywhere containers do.
- **Hosted agent secrets.** If you enable the optional agent (above), each
  signed-in user gets one on-demand Fly Sprite — a per-user Firecracker microVM
  that clones all their repos and runs the agent with a shell. The shared
  `OPENAI_API_KEY` stays on the hub because it's never shipped to the sprites:
  the sprite calls the hub's `/v1/agent-llm` proxy with its own per-user PAT, and
  the proxy injects the real OpenAI key before forwarding to OpenAI. That keeps
  the key safe even though each sprite grants the user an unsandboxed shell.
  Provisioning ships a linux/amd64 `afs` binary that the hub image bakes at
  `/usr/local/bin/afs-linux` (override with `AFS_LINUX_BIN`). Note: the proxy
  currently spends the hub's OpenAI quota for any valid PAT — there are no
  per-user rate or cost limits yet.

## The point

Because the Hub stores nothing but real git, running your own — or moving off it
with `git clone` — is always available. Hosting is a convenience, never a gate.
