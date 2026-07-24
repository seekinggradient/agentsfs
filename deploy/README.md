# Deploying the agentsfs Hub

Option A (run real git) on **Fly.io**: the [`afs-hub`](../cmd/afs-hub) binary plus
`git-http-backend`, packaged by [`Dockerfile`](Dockerfile), with the bare repos
and Git LFS objects on a persistent Fly **volume**. Cloudflare R2 is added later
for durable backup and object-storage-backed LFS; Fly runs the always-on
stateful git compute.

The live instance is **https://agentsfs-hub.fly.dev** (app `agentsfs-hub`, region
`sjc`). `fly.toml` lives at the repo root — Fly resolves `[build] dockerfile`
relative to the config's directory, and the build context must be the module
root where `go.mod` lives.

## Prerequisites (one-time)

1. A Fly.io account: <https://fly.io/app/sign-up> (needs a card; the hub runs on
   a `shared-cpu-1x` / 256 MB machine that suspends when idle — a few $/mo).
2. `flyctl` installed and authenticated. Either:
   - `fly auth login` (browser), or
   - create a token at <https://fly.io/user/personal_access_tokens> and put it in
     `~/.afs-hub/fly.env` as `FLY_API_TOKEN=...` (kept out of the repo, like the
     Cloudflare tokens).

## Deploy

Run from the repo root (this worktree), with `FLY_API_TOKEN` in the environment
(e.g. from `~/.afs-hub/fly.env`). This is the flow that provisioned the live app:

```sh
# 1. Create the app and a persistent volume for the repos.
fly apps create agentsfs-hub --org personal
fly volumes create afs_hub_data --size 5 --region sjc --app agentsfs-hub --yes

# 2. Stage the access token(s) as a secret (never baked into the image).
fly secrets set AFS_HUB_TOKENS="akshay:$(openssl rand -hex 20)" --app agentsfs-hub --stage

# 3. Ship it (fly.toml at repo root points at deploy/Dockerfile).
fly deploy --app agentsfs-hub --remote-only
```

Redeploys after a code change are just step 3. The hub is then at
`https://agentsfs-hub.fly.dev`:

```sh
git clone https://akshay:<token>@agentsfs-hub.fly.dev/akshay/brain.git
# and the browser view of your knowledge:
open https://agentsfs-hub.fly.dev/akshay/brain
```

## Notes

- **Tokens** live only in Fly secrets (`AFS_HUB_TOKENS="user:token,user2:token2"`)
  and the OS git credential helper on clients — never in a repo.
- **Persistence**: the `afs_hub_data` volume holds the bare repos and Git LFS
  objects. Do not deploy without it, or repos reset on each release. The live
  volume starts at 5 GB and `fly.toml` grows it by 5 GB at 75% utilization, up
  to a 25 GB limit.
- **Custom domain** (later): `fly certs add hub.agentsfs.ai` and a Cloudflare DNS
  record pointing at the app.
- **Local dev** mirrors production exactly:
  `docker build -f deploy/Dockerfile -t afs-hub . && docker run -p 8080:8080 -e AFS_HUB_TOKENS=you:dev afs-hub`.
