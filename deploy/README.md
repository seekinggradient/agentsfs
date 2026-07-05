# Deploying the agentsfs Hub

Option A (run real git) on **Fly.io**: the [`afs-hub`](../cmd/afs-hub) binary plus
`git-http-backend`, packaged by [`Dockerfile`](Dockerfile), with the bare repos
on a persistent Fly **volume**. Cloudflare R2 is added later (Phase 3) for
durable backup + LFS; Fly runs the always-on stateful git compute.

## Prerequisites (one-time)

1. A Fly.io account: <https://fly.io/app/sign-up> (needs a card; the hub runs on
   a `shared-cpu-1x` / 256 MB machine that suspends when idle — a few $/mo).
2. `flyctl` installed and authenticated. Either:
   - `fly auth login` (browser), or
   - create a token at <https://fly.io/user/personal_access_tokens> and put it in
     `~/.afs-hub/fly.env` as `FLY_API_TOKEN=...` (kept out of the repo, like the
     Cloudflare tokens).

## Deploy

Run from the repo root (this worktree):

```sh
# 1. Create the app (unique name) and a persistent volume for the repos.
fly launch --no-deploy --copy-config --name afs-hub-<unique>
fly volumes create afs_hub_data --size 1 --region sjc

# 2. Set the access token(s) as a secret (never baked into the image).
fly secrets set AFS_HUB_TOKENS="akshay:$(openssl rand -hex 20)"

# 3. Ship it.
fly deploy
```

The hub is then at `https://<app>.fly.dev`:

```sh
git clone https://akshay:<token>@<app>.fly.dev/akshay/brain.git
# and the browser view of your knowledge:
open https://<app>.fly.dev/akshay/brain
```

## Notes

- **Tokens** live only in Fly secrets (`AFS_HUB_TOKENS="user:token,user2:token2"`)
  and the OS git credential helper on clients — never in a repo.
- **Persistence**: the `afs_hub_data` volume holds the bare repos. Do not deploy
  without it, or repos reset on each release.
- **Custom domain** (later): `fly certs add hub.agentsfs.ai` and a Cloudflare DNS
  record pointing at the app.
- **Local dev** mirrors production exactly:
  `docker build -f deploy/Dockerfile -t afs-hub . && docker run -p 8080:8080 -e AFS_HUB_TOKENS=you:dev afs-hub`.
