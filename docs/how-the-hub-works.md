# How the agentsfs Hub works — a walkthrough

Written for you, Akshay, to read top-to-bottom and understand the whole thing —
including the Fly.io and Cloudflare parts that were new to you. It builds up
from what agentsfs already is.

---

## 1. Start from what you already know

**agentsfs** is your memory-as-files idea: a plain **git repository** full of
markdown notes. Each note has a one-line `description:`, notes link to each
other with `[[wikilinks]]`, and the whole thing is just files + git — no
database, no lock-in. Agents read and write those files with normal tools, and
the `afs` CLI adds nice things (tree, search, doctor). `git clone` is the exit
ramp: your knowledge is never trapped.

The one thing that was missing: **your knowledge lived only on your laptop.**
There was no central place to *see* all your knowledge bases, no URL to point a
second machine (or a teammate, or a future agent) at, and browsing meant opening
files in an editor.

## 2. What we built: the Hub

The **agentsfs Hub** fixes exactly that. The one-line description:

> **A private GitHub, but for your agents' knowledge instead of code.**

It's live right now at **<https://hub.agentsfs.ai>**. It does three things,
all at the *same* web address:

1. **It's a git remote.** You `git push` your knowledge to it and `git clone` it
   back — with ordinary git, no special tools.
2. **It's a website.** Open the URL in a browser and you *see* your knowledge:
   every repo, every note rendered nicely, links you can click, history, search.
3. **It's editable.** You can edit a note right in the browser and hit Save.

Crucially, what the Hub stores is **real git** — genuine git repositories, byte
for byte. So `git clone` still works from anywhere, and if you ever wanted to
walk away, you'd lose nothing. That was the non-negotiable rule, and it's kept.

## 3. The three parts, and how they fit together

### Part A — real git storage

When you push, the Hub stores your repo as a normal **bare git repository** (a
git repo with no working copy — just the `.git` internals). Nothing invented,
nothing proprietary. This is why the "exit ramp" promise holds: it's just git.

### Part B — the web space (the "central space")

A small Go program renders your knowledge into web pages by reading straight
from those git repos:

- **Dashboard** — a card for each of your knowledge bases, with its description
  and how many notes it has.
- **Repo view** — the familiar agentsfs tree, with each note's description and
  "last touched" date, folders you can collapse, and a filter box.
- **Note view** — the markdown rendered beautifully, with `[[wikilinks]]` turned
  into real clickable links, a "**Referenced by**" section (backlinks — every
  note that points *to* this one), and the note's git history.
- **Editing** — an "Edit" button opens the raw note; Save writes a real git
  commit, authored by you.

The important detail: the website reuses the *exact same* code the `afs` CLI
uses to parse descriptions and resolve wikilinks. So the website can never
"drift" from the CLI — there's one implementation, not two.

### Part C — login

Your knowledge is private. To see it in a browser you sign in once with your
**hub access token** (a long random password), and a cookie keeps you signed in.
Agents and `git` use that same token as an HTTP password. Nobody without the
token sees anything.

## 4. Where it runs — the infrastructure, in plain terms

This is the part that was new to you, so here's the whole picture.

### Fly.io — the little always-on computer

To run "real git," you need an always-on computer with a real hard drive,
because git needs a filesystem to work on. **Fly.io** is a service that runs your
program on a small computer somewhere on the internet, reachable at a URL.

- We packaged the Hub into a **container** (a self-contained bundle with the Go
  program + git inside — the `deploy/Dockerfile`). Fly runs that container.
- Fly gives it a **persistent volume** — a small hard drive (1 GB) that survives
  restarts. **This is where your repos actually live.**
- Fly puts **HTTPS** (the padlock) in front automatically, so the connection is
  encrypted.
- To save money, the machine **suspends when nobody's using it** and wakes up in
  about a second on the next request. So it costs only a few dollars a month.

That's the whole Fly story: *a tiny rented computer that runs the git server and
holds your repos, reachable at `agentsfs-hub.fly.dev`.*

### Cloudflare — and why we ended up barely using it (for now)

You gave me Cloudflare tokens, so here's the honest status. Cloudflare is a suite
of separate products; the two names you had backwards:

- **R2** = **blob storage** (like Amazon S3). It stores *files*. This is where
  git repos *could* be backed up. **R2 is the one for storing repos.**
- **D1** = a **SQL database** (rows and columns). Not needed yet.
- **Workers** = tiny programs that run at Cloudflare's edge. Great for websites,
  but they *can't run a git server* (no always-on process, can't run the `git`
  program) — which is exactly why the git server runs on Fly, not Cloudflare.

So for this first version, **Cloudflare isn't in the running system** — the Fly
volume already keeps your repos safe. R2's job comes later: a **backup** copy of
your repos and a place for large media (images, PDFs). Your R2 tokens aren't
wasted; they're for that next step. (This is the honest reason your "all
Cloudflare" instinct didn't pan out: Cloudflare is superb for storage and
websites, but not for the one thing the Hub most needs — an always-on stateful
git server.)

## 5. A single request, start to finish

**When you push knowledge:**
`git push` → travels over HTTPS to `agentsfs-hub.fly.dev` → Fly's machine wakes →
the Hub checks your token → it runs real `git` to store the push on the volume.
Done. Your knowledge now lives on the internet, still as plain git.

**When you open the website:**
Browser → the Hub checks your login cookie → it reads the git repo on the volume
→ renders the tree / note / history into HTML → sends you the page.

**When a new machine or teammate wants it:**
`git clone https://…/akshay/<repo>.git` → a complete copy, full history, on their
machine. That's the exit ramp, always available.

## 6. How to actually use it

Your access token is saved on your Mac at `~/.afs-hub/hub.env` (never in any
repo). To see it: `cat ~/.afs-hub/hub.env` and copy the part after `akshay:`.

- **Browse:** open <https://hub.agentsfs.ai>. Sign in — username `akshay`, and
  your hub token as the password (or set a real password on the **Account**
  page). New people can **Create an account** (their username becomes their
  namespace). There's a sample repo called **welcome** to explore.
- **Get a git token:** on the **Account** page, create a named access token
  (git can't do an interactive login — this is like a GitHub PAT). Then:
  ```sh
  cd ~/agentsfs                       # or any agentsfs repo
  git remote add hub "https://akshay:<token>@hub.agentsfs.ai/akshay/agentsfs.git"
  git push hub main
  ```
  Then open `https://hub.agentsfs.ai/akshay/agentsfs` to see it.
- **Edit** a note in the browser and Save — it becomes a real commit.
- **Clone it anywhere:** the repo page shows a copy-ready `git clone` command.
- **Leave anytime:** `git clone` gives you everything; nothing is trapped.
- **Make a repo public (optional):** open its **Settings** and confirm by typing the slug — then anyone with the link can read and clone it, while only you can edit. Private is always the default, and your dashboard stays private.
- **Run your own Hub:** it's open source — anyone can self-host (see [../deploy/self-host.md](../deploy/self-host.md)). Hosting is a convenience, never a lock-in.

## 7. Talk to an agent that lives in the Hub

Reading and editing notes yourself is one thing. The Hub also gives you **an
agent of your own** — a conversational assistant that knows *all* your knowledge
bases and can read, discuss, edit, and commit them for you, entirely from the
browser. Open **<https://hub.agentsfs.ai/agent/>**, or click **"Talk to an
agent"** on any repo page (which drops you in already focused on that repo).

Here's the shape of it:

- **One agent per person, spanning everything.** The first time you open it, the
  Hub quietly provisions a private computer just for you and clones *all* your
  knowledge bases into it side by side. The agent boots up "unfocused," lists
  your knowledge bases, and asks which one you'd like to work in. `list_repos`
  and `focus_repo` let you switch the active KB mid-conversation; the repo-page
  button just pre-focuses one for you.
- **It genuinely does the work.** Once focused, it searches and reads the KB the
  same way the `afs` CLI does (tree, ranked and semantic search, backlinks),
  edits notes with clean diffs, and can run ordinary shell commands — `git`,
  `rg`, `ls`, `afs hub pull` — across all your cloned repos. Every change it makes
  is a **real git commit pushed straight back to the Hub**, so git stays the
  source of truth and `git clone` / `git pull` still get you everything. The
  agent never becomes a second, drifting copy of your knowledge.

### How it actually runs (the sprite)

Behind the scenes, your agent runs in a **Fly Sprite** — a "sprite" here is a
tiny, hardware-isolated virtual machine (a *Firecracker microVM*, the same
isolation tech that powers serverless clouds). It's named `afs-user-<you>`, it's
**yours alone**, and it **auto-sleeps** when you're not using it and wakes on
your next message, so it's cheap to keep around and its state persists between
sessions.

You never see the sprite directly. The Hub **reverse-proxies** it under
`hub.agentsfs.ai/agent/*`: your browser talks only to the Hub, the Hub forwards
to your sprite (injecting the Sprites credential server-side), and the sprite
stays completely private to us. So you stay signed in on the Hub the whole time
and never touch the underlying sprite provider's login. Chat streams token by
token the whole way through.

### The key security property, in plain language

The agent can **run arbitrary shell commands** on its machine. That sounds scary,
so here's exactly why it's safe — and it comes down to two ideas.

- **Isolation is the sandbox.** Because every user gets their *own* Firecracker
  microVM, the worst an agent can do — even if a note it reads tries to hijack it
  ("prompt injection") — is mess with *that one user's own* data and sprite.
  There is no path to anyone else's knowledge. The VM boundary *is* the wall.
- **The shared model key is never on the box.** The one thing that must never
  leak is the operator's shared OpenAI key, since your agent (and anything it
  reads) can run commands and even `sudo`. So we simply **don't put that key in
  the sprite at all.** Instead, the sprite makes its model calls *back through the
  Hub*, at `/v1/agent-llm`, authenticating with **your own** per-user token. The
  Hub checks that token, then forwards the call to OpenAI with the real shared key
  swapped in — a key that lives only on the Hub. The sprite holds just your own
  token, which it already needs for `git push` and `afs hub pull` anyway, so
  handing it to your own agent is fine.

In other words: the sprite can run anything, but there's simply nothing valuable
on it to steal. (There's also belt-and-suspenders tidying — the shell's
environment is scrubbed of anything that looks like a key or token, and tool
output is redacted before it reaches the model or your screen — but those are
hygiene, not the real defense. The real defense is *no key on the box* plus
*one microVM per person*.) The shell tool is also gated behind its own switch,
kept separate from the "allow edits" switch, so turning on editing never
silently grants command execution.

## 8. What's deliberately not done yet (and why)

- **R2 backup of your repos.** The Fly volume is already durable, so this is a
  safety net, not a necessity. It needs R2 storage keys I chose not to create
  unattended. One evening's work when you want it.
- **Accounts, sharing, teams** — accounts and public sharing now exist; full
  teams are for when it's more than just you.
- **Per-user cost limits on the agent's model proxy.** Today any valid token
  spends the hub's shared OpenAI quota; per-user rate/cost caps are still to
  come. Voice chat works in the code but hasn't been exercised live in a
  sprite yet.

## 9. Cost and safety

- **Cost:** a few dollars a month — one small Fly machine that mostly sleeps,
  plus a 1 GB volume. Everything else is free tier.
- **Please rotate the tokens** (both Cloudflare and the Fly one) when you get a
  chance — they passed through our chat, so treat them as exposed. And you can
  delete the broad Cloudflare "master" token; nothing needs that much power.
- **Security:** the Hub was reviewed by a multi-agent adversarial pass and
  hardened (path-traversal guards, upload caps, request timeouts, etc.). It's
  private by default — no token, nothing visible.

## 10. Where the code lives

Everything is on the **`hosted-hub`** git branch (pushed to GitHub), in a
separate working copy at `~/Development/agentsfs-hub` so it never collided with
the other agent working on `main`. When you're ready, merge `hosted-hub` into
`main`. The map:

- `cmd/afs-hub/` — the server program.
- `internal/hub/` — the guts: `server.go` (routing), `backend.go` (runs real
  git), `storage.go` (repos on disk), `auth.go` (tokens + sessions), `web.go` +
  `assets/` (the website), `render.go` (markdown + wikilinks), `repoview.go`
  (reading git), `edit.go` (browser edits → commits).
- `deploy/` — the `Dockerfile` and deploy notes; `fly.toml` (repo root).
- `docs/hub-execution-plan.md` — the technical plan and decision log.
