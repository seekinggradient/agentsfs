# agentsfs Hub — Execution Plan (hosted storage layer)

Companion to [agentsfs-source-of-truth.md](agentsfs-source-of-truth.md) (the settled idea) and [execution-plan.md](execution-plan.md) (how the core was built). This document records the decision to build a **hosted storage layer** and how we build it. Sections are **PROPOSED** until discussed, **AGREED (date)** after. The standing rule carries over: simplicity first, build from first principles, actively fight the urge to overcomplicate.

## TL;DR

**A private GitHub, purpose-built for agent *knowledge* instead of code.** We host real git repositories and Git LFS media objects, with a central web space on top where a user sees all their agentsfs repos, introspects the knowledge, downloads it, copies a clone command, or points an agent at a stable URL. The current Fly deployment stores bytes on the persistent volume; Cloudflare R2 remains the planned object-storage backend.

The load-bearing constraints:

- **Local-first stays sacred.** Agents keep working against a local filesystem with their normal tools (Read/Write/Edit, `ls`/`grep`, `afs tree`/`afs search`). They finish a unit of work, `git commit -m "…"`, and `git push`. The hub is a *destination*, not a new way of working.
- **We store *real* git.** What sits in the bucket is a genuine bare git repo, so `git clone` remains a byte-for-byte exit ramp. No invented on-disk format is ever the source of truth. This makes Principle 6 true *by construction*, not by promise.
- **No read capability is ever gated behind payment.** `git clone` (and download) is always the free, complete exit. Paid, if it ever exists, buys convenience/managed-sync/teams — never access to your own knowledge.

## Status

- **2026-07-04 — Direction reversed and build authorized. AGREED (owner).** The hosted layer is back in scope; owner confirmed the earlier removal was a ship-it expedient (see below). Building **Phase 0** (real-git remote) now.
- **2026-07-05 — Live in production.** Deployed at **https://agentsfs-hub.fly.dev**; well beyond Phase 0. Beginner-friendly walkthrough: [how-the-hub-works.md](how-the-hub-works.md). Shipped:
  - **Phase 0** — real-git remote (clone/push over HTTPS), token auth, private by default.
  - **Phase 1** — central web space at the same URL: session **login**, **dashboard** of repo cards, nested **tree** (descriptions + freshness), **note view** (rendered markdown, syntax highlighting, resolved `[[wikilinks]]`, **backlinks**, file/commit **history**), raw view, content-hash asset cache-busting; editorial light/dark UI, screenshot-verified.
  - **In-browser editing** (ahead of the Phase-5 plan) — web edits land real attributed git commits via a no-checkout write path with optimistic-concurrency CAS.
  - **Deployment** — Fly.io (`agentsfs-hub`, region `sjc`), real git on a persistent volume, suspend-when-idle; `deploy/Dockerfile` + root `fly.toml`.
  - **Security hardening** after a multi-agent adversarial review: path-traversal/arg-injection guards, body-size cap, render size/UTF-8 guards, EnsureRepo mutex, Content-Disposition escaping, HTTP server timeouts.
  - **Public/private per repo** — private by default; a repo goes public only after a **typed confirmation** (type the slug + a "Yes, make this public" button) behind a blunt warning. Public = anonymous read + `git clone` of that repo only; writes/edits stay owner-only; the dashboard and other repos stay private. Visibility (+ display name) live in each bare repo's git config, so they persist and survive renames.
  - **Slugs** — validated (lowercase/digits/hyphens), an optional display name, and a rename with a **duplicate check**; a repo **Settings** page; public/private badges in the UI.
  - **Self-hostable** — the Hub stays in the open-source agentsfs repo (`go install ./cmd/afs-hub`, or the Dockerfile anywhere); multi-user via `AFS_HUB_TOKENS`. Guide: [../deploy/self-host.md](../deploy/self-host.md). Positioning: self-hosting is free; the hosted instance is a convenience, never a gate.
  - **Custom domain** — live at **https://hub.agentsfs.ai** (Fly Let's Encrypt cert + Cloudflare DNS-only A/AAAA records). `*.fly.dev` still works.
  - **Accounts** — self-serve **signup** + username/**password** login + per-account git **tokens (PATs)**, argon2id hashing, in an embedded **SQLite** file on the volume (already a dependency — no new infra). Chosen over Clerk deliberately (self-hostable, no vendor lock-in). Existing `AFS_HUB_TOKENS` users are bootstrapped and keep working; `AFS_HUB_OPEN_SIGNUP=false` locks signup down.
  - **Per-repo collaborators** — owners invite by **email**. Existing accounts receive read/write access immediately; unknown addresses get a hashed, expiring invite link that can create an account and attach the repo grant on signup. Collaborators use their own account and appear under “Shared with you.”
  - **Merged to `main`** (commit `3397fdf` and after) — the Hub ships in the open-source repo.
  - **Deferred (documented, not built):** R2 durability backup (the Fly volume is already persistent), remote MCP / `afs remote` CLI helper, orgs/teams/sharing (beyond per-repo public/private), email verification + password reset, "Sign in with GitHub", rate-limiting.
- **2026-07-06 — Next Hub work, agreed direction:** (1) **Scheduled agent runs** — per-user cron wakes the sprite and runs a named routine (the gardener first: it consumes `journal/` per [execution-plan.md](execution-plan.md) Layer 5; research routines later), commits, pushes, sleeps. Prereq: per-user cost caps on the `/v1/agent-llm` proxy (metering already ships). (2) **Server-side journaling** in agentsfs-chat — the server holds full transcripts, so hosted-agent capture is deterministic, not compliance-based.
- **2026-07-07 — Large-repo Hub rendering performance fixed.** `agentic-stocks` exposed an N+1 Git subprocess pattern in repo/file rendering: 835 Markdown notes made the Hub run one `git show` per note plus expensive backlink resolution. The fix batch-reads Markdown blobs with `git cat-file --batch`, streams freshness history, and resolves backlinks directly against the viewed target path. See [hub-repoview-performance.md](hub-repoview-performance.md).
- **2026-07-09 — Git LFS support landed.** The Hub now implements the standard Git LFS Batch API for upload/download/verify, stores LFS objects on the persistent Fly volume under `.lfs/`, verifies SHA-256 and size on upload, and resolves LFS pointers in `/raw` responses. R2 remains a later backend swap, not a prerequisite for media-heavy repos.

## Why now — reversing the 2026-06-16 removal

[execution-plan.md](execution-plan.md) logged on 2026-06-16 that "managed hosting [was] removed from the product direction," and the shipped contract in [template/AGENTS.md](../template/AGENTS.md) still tells agents "Do not assume managed hosting exists." The owner confirmed on 2026-07-04 that this decision was **fully reversible** — it existed only to ship the initial system faster, because building sync would have taken more time. It was never a principled objection. So we reverse it.

**How this differs from the retired prototype.** An earlier prototype (recorded in the owner's personal `~/agentsfs/areas/agentsfs/Hosted Service.md`) used Cloudflare Workers + Clerk + D1 and stored user data in **managed private GitHub repos**. That shape had two problems this plan avoids:

1. **GitHub-in-the-middle.** Storing the canonical data in GitHub-owned repos made GitHub a hard dependency and a lock-in surface. Here the canonical data is real git in **R2 the user can own** (and can self-host); GitHub is neither required nor in the path.
2. **Managed store as the product.** The revived design keeps files-plus-git as the source of truth and treats the hub as a *pluggable git remote plus a viewer* — consistent with Principle 6, not a bypass of it.

Treat the old Workers/Clerk/D1/GitHub prototype as abandoned implementation history, not a starting point.

## Principles alignment

| Principle | How the hub honors it |
|---|---|
| 1. No intelligence inside | The hub stores and serves git; it runs no LLM. Semantic search (later) uses a user-supplied provider key, never stored in the repo. |
| 2. Files are the source of truth | Real git objects are canonical. The repo directory, search index, and rendered views in the hub are all **derived and rebuildable** from the repos. |
| 3. Information-dense | Unchanged — a storage/hosting concern, not a content concern. The web space *surfaces* density (tree, backlinks) but doesn't alter it. |
| 4. Cross-harness neutrality | One stable URL any harness can clone/pull/push. No vendor client required — plain git works. |
| 5. Ride the training distribution | The wire protocol is git; the format is the same markdown/YAML/wikilinks. Nothing new for an agent to learn. |
| 6. Git is the backbone; remote is pluggable | The hub is a *blessed* git remote. `git clone`/`git remote set-url` to anywhere else is a first-class, advertised exit. |

## The design

### Mental model — local-first

An agent (or human) has a local clone of their agentsfs. They edit plain files with whatever tools they already use, then `git commit && git push`. The hub is where the repo lives when it's not on the laptop. Pointing a brand-new agent at the URL is `git clone` → it has everything → it works locally → it pushes back. **Nothing about the local workflow changes.**

### Piece 1 — the hosted git remote (the thing you push to)

A real git server whose repos are stored as ordinary bare git repos. In the current Fly implementation, bare repos and Git LFS objects live on the persistent volume; the R2-backed version moves those bytes into S3-compatible object storage later. You `git clone` from it and `git push` to it with ordinary git — no special client. Large media (the images/PDFs the template `.gitattributes` auto-tracks via LFS) uses the standard Git LFS Batch API.

### Piece 2 — the central space (the thing you look at)

A website that lists **all** the user's knowledge repos and renders each one the agentsfs way: the progressive-disclosure tree with descriptions and git-freshness dates, markdown with resolved `[[wikilinks]]` and backlinks, commit history/diffs, a **"copy clone command"** button, and **download** (archive of the working tree, and a `.git` bundle for full history). Later: full-text/semantic search across everything. This just reads the same repos Piece 1 stores.

### One URL, three faces

`https://afs.dev/<user>/<repo>` is simultaneously:

- a **git remote** — `git clone` / `git pull` / `git push`;
- a **web space** — the same URL in a browser renders the knowledge;
- (later) a **read/write API + remote MCP** — for headless agents that don't hold a local checkout.

The human and the agent use the same address, each in their own way.

### Cloudflare product mapping (for reference — these names are opaque)

| Product | What it is | Analogy | Our use |
|---|---|---|---|
| **R2** | Object/blob storage, S3-compatible, **$0 egress** | Amazon S3 | store the git repos + LFS media blobs |
| **D1** | Serverless SQL (SQLite) | small Postgres | repo directory, search index (both derived) |
| **Workers** | Edge serverless functions (JS/WASM) | AWS Lambda | the website + APIs — **not** the git server |
| **Containers** | Run a real container/binary; can scale to zero | a small server | run actual `git` for Option A |
| **KV / Durable Objects** | Edge cache / stateful coordinator | Redis / a lock | caching; single-writer guard (later) |

Note: **R2 is the blob store; D1 is the SQL database.** Git repos are blobs → they live in R2. This is why R2 is the right home for them, and R2's zero egress makes cloning/pulling the whole knowledge base free in bandwidth (the usual git-hosting cost killer, deleted).

### Option A vs Option B (how the git server runs)

Requiring `git push` means the hub must speak git's smart-HTTP protocol (`upload-pack` for fetch, `receive-pack` for push). Two ways to provide that:

- **Option A — run real git (CHOSEN to start).** A small always-on service runs actual `git` (`git-http-backend`, the same CGI GitHub-style hosts use), with repos backed by R2. Simplest and most correct — we reimplement nothing; git does what git does. Runs as a **Cloudflare Container** (stays in-ecosystem, scales to zero with a cold-start on wake) or a small box elsewhere (Fly.io, Hetzner, Railway).
- **Option B — edge-native git (later, optional).** Reimplement git's wire protocol in a Worker over R2 objects (gitoxide→WASM / isomorphic-git / a container running `git http-backend`). Cheaper at rest, no always-on process, but real work and real risk of subtly corrupting clones.

**Because both store real git, A→B is a transparent migration — clients never know.** So we take the correct, cheap-to-build path first and keep B as a cost optimization, not a gate. The scary "reimplement git on Workers" question is deferred, not on the critical path.

### Storage layout

```
<root>/<user>/<repo>.git/       ← a genuine bare repo (objects/, refs/, packed-refs, HEAD)
<root>/.lfs/<user>/<repo>/<shard>/<oid>
                                ← Git LFS blobs, verified by SHA-256
```

For Phase 0 the authoritative repos and LFS blobs live on the container's volume. R2 remains the later durability/scaling backend; the fixed invariant is that the bytes are real git plus standard LFS objects and reconstruct a byte-complete clone.

### Auth

Private by default. A user authenticates the CLI once (`afs remote login`) which mints a **scoped, revocable token** stored in the OS git credential helper — **never** in the repo (contract rule: no secrets in the folder). The token authorizes `git push`/`pull` and API calls for the repos it grants. Public read-only repos are possible (share a URL that clones with no token). Per-repo ACLs and sharing land with the team/paid work.

### Write path & conflict model

- **Primary path (all phases): local-first `git push`.** Concurrency is handled by **plain git**: if two machines push out of sync, the second push is rejected (non-fast-forward), the agent `git pull`s and retries. Standard, boring, well understood. No custom machinery, no server-side LLM merge (Principle 1).
- **Secondary path (arrives with web editing, Phase 5): server-side commits.** A browser has no local checkout, so a human editing a note in the web app needs the server to construct a real, attributed commit and advance the ref. This is the *only* place the "no-checkout write" genuinely belongs. When both local pushes and web edits write one repo, a **single-writer guard** (a Durable Object per repo doing compare-and-swap on the ref) serializes them; a stale web edit gets a 409 and retries. Not needed while the web space is read-only.

### Human editing

- **Power users / anyone willing to clone: Obsidian already works, today, for free.** An agentsfs repo *is* markdown + `[[wikilinks]]` + YAML frontmatter — Obsidian's native format. `git clone` → open as a vault → edit with graph/backlinks → `git commit && git push`. Zero code from us, and the strongest possible proof of no-lock-in. (The owner's `~/agentsfs` is already an Obsidian vault.)
- **The web space: start read-only, add editing as a fast-follow.** Read-only ships first (browse/search/history/download/clone) and needs no write path. In-browser editing (Phase 5) turns each save into a real attributed commit via the secondary write path above — the answer for the non-technical user (the "brother test") who won't clone or install anything.

### Cost (personal scale — one user, a few GB incl. media)

- **R2 storage** (~$0.015/GB-mo) → a few GB is pennies; **egress is $0** so clones/pulls cost nothing in bandwidth.
- **D1 + Workers + KV** → effectively $0 on free tiers, or inside the $5/mo Workers paid plan if outgrown.
- **The git server (the only real line item):** Cloudflare Containers with scale-to-zero ≈ **$0–5/mo** for light use (billed per-second while awake); an always-on small VPS ≈ **$3–5/mo** flat.
- **Bottom line: a personal hub is single-digit dollars/month, plausibly under $5.** Per-user economics stay cheap at product scale precisely because R2 egress is free. Option B later removes even the container cost.

## Changes required in the existing `afs` codebase

The core is well-positioned — git usage is thin and the index is already derived/rebuildable. Concrete seams:

1. **`fingerprint()` keys FTS freshness on mtime** — [internal/core/index.go](../internal/core/index.go) hashes `info.ModTime()`, which is meaningless server-side (no working tree). The hub's index must re-key freshness to the commit-sha / root-tree-sha. Small change; silent staleness bug if missed.
2. **Connection blocks point at local paths, not URLs** — [internal/core/register.go](../internal/core/register.go) `ConnectionBlock` embeds a local path (`~/agentsfs`). The "point an agent at a stable URL" story needs this to accept a remote URL.
3. **Fresh clones have no `.agentsfs/` (it's gitignored) — already handled.** `FindRoot` ([internal/core/instance.go](../internal/core/instance.go)) falls back to detecting the contract marker in `AGENTS.md`, so a bare clone is still recognized as an instance; `afs reindex` regenerates the sidecar. No special work; note it and keep the fallback.
4. **New thin CLI surface:** `afs remote login` (mint+store a scoped token in the git credential helper) and docs for `git remote add hub <url>`. Idiomatic — the codebase already shells out to git. A full `--remote <url>` mode where `FindRoot` resolves to a remote instance is a later phase, not MVP.
5. **Web renderer must reuse the Go core, not re-implement it.** Rendering the tree + wikilink resolution in JS would be a third surface that drifts from `internal/core`. Compile `internal/core` to WASM (or run it server-side in the container) so the web space renders the *same* output `afs tree`/`afs search` produce. One implementation.
6. **Contract text update — deferred until the hub ships.** [template/AGENTS.md](../template/AGENTS.md) "Backup and sync" currently says "Do not assume managed hosting exists." That stays true until the hub is real; update it when Phase 0/1 ship, not before (don't put a false statement in the contract).

## Phased roadmap (each phase ends in a live gate demo)

- **Phase 0 — real-git remote (days). ← MVP GATE, in progress.** A server wrapping `git-http-backend` over bare repos, with token auth and on-demand repo creation. Deliverable: a working git remote you can `git push` to and vanilla-`git clone` from. **Gate demo:** create an empty repo on the hub, `git remote add hub … && git push`, then `git clone` it fresh elsewhere and read the files. Then prove the exit: `git remote set-url` to a plain GitHub repo and push — hub gone, zero data loss.
- **Phase 1 — read-only central space (1–2 wk).** Web app listing the user's repos; per-repo browser rendering tree/markdown/wikilinks/backlinks and git log/diff via the WASM-compiled core; copy-clone-command; download working-tree archive and `.git` bundle. **Gate:** a human sees and browses a real repo end-to-end; a fresh agent clones from the copied command and answers a question from the files alone.
- **Phase 2 — remote read + remote MCP (2–3 wk).** Deploy `internal/mcpserver` over an HTTP transport; a `RemoteInstance` adapter so `Tree`/`Search`/`Backlinks` can run against the hub; D1 FTS index (port the [index.go](../internal/core/index.go) schema, **re-keyed to commit-sha**). **Gate:** an agent points at one URL and reads (tree/search/backlinks) with no local checkout.
- **Phase 3 — R2-native storage + efficiency (2–3 wk).** Move authoritative storage fully into R2 (Option A hardening: repos in R2, gc/repack on a schedule), and switch LFS transfers from Hub-streamed local-volume objects to presigned R2 URLs. **Gate:** a media-heavy repo round-trips (push image → browse in web → clone with LFS) and survives a repack (`git fsck` clean).
- **Phase 4 — accounts, ACLs, sharing (2–3 wk).** D1 memberships/ACL table, public vs private repos, share links; self-hostable package (runnable on the user's own Cloudflare account, or S3+VPS) so even the managed layer has no lock-in. Unlocks the deferred teams/managed-sync business model. **Gate:** share a repo read-only via link; a second account clones it; revoke and confirm access drops.
- **Phase 5 — in-browser editing + server-side search (ongoing).** Web editing → real attributed commits via the single-writer guard; optional server-side embeddings/semantic search (user-supplied provider key, never stored in repo). **Gate (brother test):** a non-technical user edits a note in the web app, it becomes a real commit, and the change shows up on a local `git pull`.

## Open decisions

1. **Option A long-term host — Cloudflare Container (scale-to-zero, in-ecosystem) vs small always-on VPS (predictable, dead simple)?** Recommendation: prototype on a VPS/local for Phase 0 speed, evaluate Cloudflare Containers for the deployed hub; not load-bearing since A→B is transparent.
2. **A→B (edge-native git on Workers) — do we ever pursue it?** Only if container cost or cold-starts bite. Deferred; resolve with a throwaway "clone a 10k-commit repo through a Worker" spike *before* committing.
3. **Repos-on-volume-backed-by-R2 vs repos-live-in-R2 for Option A.** Phase 0 can use a volume; Phase 3 decides the durable mechanism. Fixed invariant: bytes are real git.
4. **Managed/paid posture — activate the deferred business model now (design ACLs in from Phase 4) or stay fully free?** Hard rule either way: no read behind payment; `git clone` always free and complete.
5. **Web-edit commit identity (Phase 5)** — token-id as committer with an agent/human-name trailer, or a per-actor identity, so `git blame` stays truthful and legible.

## Decision log

- **2026-07-04 — Hosted layer reinstated. AGREED (owner).** Reverses the 2026-06-16 removal (which was a ship-it expedient, not a principled objection). Direction: a "private GitHub for agent knowledge," real bare git backed by Cloudflare R2, local-first `git push`, `git clone` as the always-free exit. Distinct from the retired Workers/Clerk/D1/GitHub-managed-repos prototype (no GitHub-in-the-middle; user-ownable storage).
- **2026-07-04 — Option A first. AGREED (owner).** Run real `git` (`git-http-backend`) rather than reimplement the protocol at the edge; Option B kept as a transparent later optimization.
- **2026-07-04 — Local-first is the write path. AGREED (owner).** Agents edit files with normal tools and `git push`; git's non-fast-forward rejection is the conflict model. Server-side commits are scoped to future in-browser human editing only.
- **2026-07-04 — Human editing: Obsidian-on-a-local-clone now (zero build); web space read-only first, editable later.** PROPOSED.
