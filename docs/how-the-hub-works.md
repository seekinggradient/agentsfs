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
the `afs` CLI adds nice things (cross-workspace status, tree, search,
doctor). `git clone` is the exit
ramp: your knowledge is never trapped.

The one thing that was missing: **your knowledge lived only on your laptop.**
There was no central place to *see* all your workspaces, no URL to point a
second machine (or a teammate, or a future agent) at, and browsing meant opening
files in an editor.

## 2. What we built: the Hub

The **agentsfs Hub** fixes exactly that. The one-line description:

> **A private GitHub, but for your agents' knowledge instead of code.**

It's live right now at **<https://hub.agentsfs.ai>**. It does three things,
all at the *same* web address:

1. **It stores and serves real Git.** A standalone workspace uses it as an
   ordinary remote. An instance embedded under a host-repository prefix uses
   AFS's projection pull/push translation; raw Git cannot express a directory
   as a repository-root remote.
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

- **Dashboard** — a card for each of your workspaces, with its description
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
volume holds the repos and Git LFS media objects. R2's job comes later: a
**backup** copy of your repos and an object-storage backend for large media
(images, PDFs). Your R2 tokens aren't wasted; they're for that next step. (This
is the honest reason your "all Cloudflare" instinct didn't pan out: Cloudflare
is superb for storage and websites, but not for the one thing the Hub most needs
— an always-on stateful git server.)

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

## 7. Talk to Eve through the Hub

Reading and editing notes yourself is one thing. The Hub also gives you **Eve**,
a conversational assistant that can work across the workspaces you can
access. Open **<https://hub.agentsfs.ai/agent/>**, or click **Talk to an agent**
on a repo page to open a conversation focused on that repo.

The important architectural split is:

- **The Hub owns identity and data.** It authenticates the browser, stores the
  git repositories, enforces permissions, makes commits, and stores thread
  records and archived conversation events.
- **Eve owns the agent experience.** The `agentsfs-eve` application on Vercel
  serves the chat UI, runs durable agent turns, applies tool approval rules,
  and bridges realtime voice into the same thread.

Your browser still talks to `hub.agentsfs.ai/agent/*`. The Hub checks your login,
then reverse-proxies that request to Eve. It strips its own login cookie before
the hop and adds a short-lived signed identity plus your agent PAT. Eve verifies
that handoff and calls back to the Hub's `/api/agent/v1/*` API with exactly your
permissions.

Eve reads each unit of work from one pinned git revision and sends writes back
as compare-and-swap commits. If another writer changed the same content, the Hub
rejects the stale write instead of silently overwriting it. The agent therefore
never becomes a second source of truth: an accepted change is a real Hub git
commit, visible to ordinary `git clone` and `git pull` immediately.

### Focus, voice, and review

Each conversation stores its active workspace in its Hub thread record.
The dropdown changes that record directly, so switching workspaces does
not require a model turn. The conversational `focus_repo` tool writes the same
field when the user asks Eve to switch in natural language.

Voice uses a realtime audio model for speaking and listening, but delegates
grounded or multi-step work to the same durable Eve thread. Typed and spoken
work therefore share the selected workspace, transcript, citations, and
conversation history.

Normal write tools use approval and Hub permission checks. Review-mode edits go
into a proposed overlay on the thread rather than the repository; only the
owner's explicit approval turns the reviewed diff into one git commit.

### Why there is no per-user Sprite in the current path

The earlier implementation provisioned one Fly Sprite per user, cloned all of
that user's repos into it, and ran an embedded `agentsfs-chat` bundle with shell
access. That implementation remains in the codebase as a fallback when
`HUB_EVE_AGENT_URL` is unset, but production sets that variable and bypasses
Sprite lookup, provisioning, clones, and the embedded UI entirely.

Current Eve knowledge access is API-based and permission-scoped. It does not
expose the legacy hosted shell tool. See [internals/hosted-agent.md](internals/hosted-agent.md) for
the detailed current contract and its explicitly labeled fallback notes.

## 8. What's deliberately not done yet

- **R2 backup of repositories.** The Fly volume is the primary git storage; an
  independent object-storage backup remains a separate resilience project.
- **Full team administration.** Accounts, collaborators, and public sharing
  exist; richer organization/team policy can be added when the product needs it.
- **A clean production-named Eve project.** Production currently points at the
  stable alias `agentsfs-eve-staging.vercel.app`. It works as production, but
  separate staging and production projects would make promotion intent clearer.

## 9. Cost and safety

- **Cost:** the always-available Hub machine and volume run on Fly; Eve's agent
  application, durable workflow, and model/realtime usage run through Vercel.
- **Data safety:** knowledge remains real git on the Hub. Revision pins, CAS
  writes, access checks, and review overlays prevent silent cross-user or
  concurrent overwrite paths.
- **Credential boundary:** the Hub cookie stays on the Hub. Eve receives a
  signed user identity and a user-scoped PAT; long-lived model credentials stay
  in the Eve deployment and never enter a workspace. Voice receives only a
  short-lived, scoped realtime client token.

## 10. Where the code lives

The Hub and CLI code are on `main` in this repository:

- `cmd/afs-hub/` — the server program and environment wiring.
- `internal/hub/` — git storage, accounts, permissions, the hosted agent API,
  thread storage, and the Hub ↔ Eve reverse proxy (`agent_eve.go`).
- `internal/hub/assets/` — the Hub website.
- `deploy/` and `fly.toml` — the Hub container and Fly deployment.

The agent application lives in the separate `agentsfs-eve` repository and is
released to Vercel. See [internals/how-deployment-works.md](internals/how-deployment-works.md) for
which repository and deployment command apply to each kind of change.
