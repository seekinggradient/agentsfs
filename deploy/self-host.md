# Run your own agentsfs Hub

The Hub is part of the open-source agentsfs project, so hosting your own is a
first-class path — a third exit ramp alongside `git clone` and an ordinary
GitHub remote. It's a single Go binary that wraps real `git`, stores plain bare
repositories on disk, and is multi-user by config. No database, no lock-in.

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

## Production notes

- **TLS.** The Hub serves plain HTTP; terminate TLS at a reverse proxy (Caddy,
  nginx, Fly, a load balancer) and forward `X-Forwarded-Proto: https`. The Hub
  uses that header for secure cookies and to render `https://` clone URLs.
- **Persistence.** Everything lives under `--dir` as ordinary git repos —
  snapshot/back up that directory. Per-repo settings (public/private, display
  name) live in each bare repo's git config, so they travel with the data.
- **Public repos.** Work identically to the hosted instance: private by
  default; a repo goes public only after a typed confirmation in its Settings,
  after which anyone can read and `git clone` it (writes stay owner-only).
- **Fly.io** is one easy target — see [README.md](README.md); the same
  Dockerfile runs anywhere containers do.

## The point

Because the Hub stores nothing but real git, running your own — or moving off it
with `git clone` — is always available. Hosting is a convenience, never a gate.
