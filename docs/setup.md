# agentsfs setup

This guide has two audiences:

- **Agents** helping a user set up agentsfs safely.
- **Humans** who want to install `afs` and connect their projects.

The short version: install `afs`, run `afs setup` from a project, then let agents read the connection block in that project's `AGENTS.md` or `CLAUDE.md`.

## Concepts

`afs setup` is the normal first-run command.

It creates or reuses a personal agentsfs at `~/agentsfs`, then connects the current project to it.

`afs setup` is local-only: it does not choose a remote, enable automatic background sync, or send data to a server. If the user later wants backup, cross-device sync, or a place to browse and share their knowledge, they can connect an ordinary git remote (private GitHub/GitLab/self-hosted) or the **agentsfs Hub** with `afs hub push`.

`afs init PATH` only creates an agentsfs at `PATH`.

It does not connect a project or write global harness config.

`afs connect PATH` points the current project, or a global harness config, at an existing agentsfs.

It writes a small marker-fenced connection block into `AGENTS.md` or `CLAUDE.md`.

`afs init ./agentsfs --shared` is for team-shared memory committed inside a code repo.

Use it only when the user explicitly wants the memory to ship with that repo.

## Agent setup

When a user asks you to set up agentsfs, follow this flow.

### 1. Check whether `afs` is installed

Run:

```sh
afs version
afs help | grep "afs setup"
```

If both commands work, continue. If `afs` is missing or too old to show `afs setup`, install or update it.

For agent-run installs, prefer `~/.local/bin`. Many harness shells inherit that directory but do not read interactive shell profiles that add `~/go/bin`.

The normal path is the packaged installer — it downloads a prebuilt release binary, no Go or git required:

```sh
export PATH="$HOME/.local/bin:$PATH"
mkdir -p "$HOME/.local/bin"
curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | AFS_INSTALL_DIR="$HOME/.local/bin" sh
hash -r 2>/dev/null || true
command -v afs
afs version
```

To install from a local development checkout instead (or when GitHub is unreachable), set `AGENTSFS_SOURCE` and install from there:

```sh
export PATH="$HOME/.local/bin:$PATH"
mkdir -p "$HOME/.local/bin"
export AGENTSFS_SOURCE=/path/to/agentsfs
(cd "$AGENTSFS_SOURCE" && GOBIN="$HOME/.local/bin" go install ./cmd/afs)
hash -r 2>/dev/null || true
command -v afs
afs version
```

If the installer cannot download a release asset and cannot build from source, ask the user to install Go and git. If you are already inside the agentsfs source repo, this also works:

```sh
export PATH="$HOME/.local/bin:$PATH"
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install ./cmd/afs
hash -r 2>/dev/null || true
command -v afs
afs version
```

Do not treat the install as complete until `command -v afs` and `afs version` work in the current agent shell. If you cannot install tools in the current environment, ask the user to run the installer.

### 1b. Adopting an existing vault or folder of notes

If the user already has an Obsidian vault or a folder of notes they want to bring in, don't seed from scratch — follow `prompts/adopting.md` (or the `agentsfs-adopt` skill): declare personal-chronology and media directories as collections (`agentsfs_role: collection`), annotate the active knowledge areas, and never rewrite existing note bodies.

### 2. Recommend the personal shape

Default recommendation:

```sh
cd /path/to/user-project
afs setup --yes
```

This keeps memory in `~/agentsfs`, outside the codebase, and connects the current project to it.

Do not create memory inside a code repo unless the user explicitly asks for team-shared memory committed with that repo.

### 3. Use the right command

Use `afs setup --yes` when the current project should use the user's personal agentsfs.

Use `afs connect <path> --yes` when the agentsfs already exists and the current project should point at it.

Use `afs init <path>` when the user only wants to create an instance and does not want to connect the current project.

Use `afs init ./agentsfs --shared` only after the user explicitly chooses shared repo memory.

Use `afs connect <path> --global` only after the user explicitly says they want every session for a global harness to know about that agentsfs.

### 4. Respect filesystem permissions

Some harnesses restrict agent file access to the current project. If the personal agentsfs lives at `~/agentsfs` and the project is elsewhere, tell the user they may need to allowlist the agentsfs path in their harness.

### 5. Seed only after reading the contract

After setup, read:

```sh
~/agentsfs/AGENTS.md
```

Then follow `prompts/onboarding.md`: interview the user briefly for domain context, choose the first small structure yourself, write dense notes, append a session note to the session journal per the contract (`agent-journal/` by default), and commit from the agentsfs root:

```sh
cd ~/agentsfs
git status --short
git add -A .
git commit -m "Seed agentsfs"
```

If git identity is missing, explain the commit failure and leave the files staged or ready for the user to commit.

### 6. Offer backup, sync, or sharing only after local setup

The folder and its git history are the product; backup and sharing are optional layers on top. When the user wants them, two paths exist:

- **The agentsfs Hub** (`hub.agentsfs.ai`, or self-hosted) — a hosted home that also lets them browse and share their knowledge in a web view. If `afs` is installed: `afs hub login`, then `afs hub push`. Repos are private by default; it stores real git plus Git LFS media objects so `git clone` is still the exit ramp.
- **An ordinary git remote** (private GitHub/GitLab/self-hosted). Ask about the user's goal before introducing Git. Use this order:

- Do you want this agentsfs backed up or synced across computers?
- Do you know what Git is?
- Do you have a GitHub account?

If the user says yes and has GitHub, guide them through creating an empty private repository and connecting the local agentsfs:

```sh
cd ~/agentsfs
git remote add origin git@github.com:<user>/<repo>.git
git branch -M main
git push -u origin main
```

If the user does not know Git, explain it briefly: Git records file history inside this folder; GitHub can store a private online copy so another machine can recover it. If the user does not have GitHub, ask whether they want help creating an account. Do not create accounts, remotes, SSH keys, or tokens without consent, and never write credentials into the agentsfs repo.

On another machine, restore with plain git, then connect projects normally:

```sh
git clone git@github.com:<user>/<repo>.git ~/agentsfs
cd ~/code/myapp
afs connect ~/agentsfs --yes
```

## Human setup

### 1. Install `afs`

The fastest path is the installer:

```sh
curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh
afs version
```

The installer downloads a prebuilt release binary — no Go or git required. Only unusual platforms (or forks without releases) fall back to building from source, which requires Go and git.

Homebrew:

```sh
brew tap seekinggradient/agentsfs https://github.com/seekinggradient/agentsfs.git
brew install --HEAD seekinggradient/agentsfs/afs
afs version
```

The Homebrew formula currently installs from `main` with `--HEAD`. After stable tagged releases and a dedicated tap are in place, this can become a plain `brew install seekinggradient/agentsfs/afs`.

Source fallback:

```sh
git clone https://github.com/seekinggradient/agentsfs.git
cd agentsfs
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install ./cmd/afs
export PATH="$HOME/.local/bin:$PATH"
command -v afs
afs version
```

Prerequisites for source builds:

- Go
- git
- git-lfs, optional but recommended if you plan to store large PDFs, images, video, or other media

Check:

```sh
go version
git --version
git lfs version
```

If `git lfs version` fails, agentsfs still works. The CLI prints a note and skips LFS setup. When it is available, new standalone instances include `.gitattributes` rules for common media types, and the Hub can store and serve the resulting LFS objects.

If you intentionally install somewhere else, add that directory to your shell profile and verify a new shell can find it. On zsh, login shells read `~/.zprofile` and interactive shells read `~/.zshrc`, so putting the PATH line in both is the safest choice.

### 2. Update later

For user-level installs from the curl installer or source flow:

```sh
afs update --check
afs update
```

`afs update` reinstalls the CLI into the same user install directory. If `afs` is managed by Homebrew or another package manager, use that manager instead.

### 3. Uninstall later

To remove the local CLI from this machine:

```sh
afs uninstall --dry-run
afs uninstall --yes
```

`afs uninstall` never deletes `~/agentsfs`, any agentsfs repo, git history, or project-local `AGENTS.md` / `CLAUDE.md` connection block. Pass `--remove-global-connections` only when you also want to remove agentsfs blocks from known global Claude/Codex harness configs.

If `afs` is installed by Homebrew or another package manager, uninstall it with that manager. The CLI refuses to unlink package-manager or system-managed binaries unless you pass an explicit `--binary PATH`.

### 4. Connect your first project

Go to a project where you want agents to remember useful context:

```sh
cd ~/code/myapp
afs setup --yes
```

This creates or reuses `~/agentsfs`, then connects the current project by writing a connection block to the nearest `AGENTS.md` or `CLAUDE.md`. If neither exists, it creates `./AGENTS.md`.

### 4. Seed the filesystem

Open an agent in the connected project and ask it to run the first-session onboarding prompt from `prompts/onboarding.md`.

The agent should:

- read `~/agentsfs/AGENTS.md`
- ask what this memory is for, which recurring people/projects/organizations matter, and what future sessions should never have to ask again
- choose a small starter structure; do not ask the user how to organize the knowledge base
- write dense notes with `description:` frontmatter and `[[wikilinks]]`
- commit the first useful state

### 5. Connect more projects

Run this from each additional project:

```sh
cd ~/code/another-project
afs connect ~/agentsfs --yes
```

The project now points at the same personal agentsfs.

### 6. Optional: connect global harness config

If you want every Claude Code or Codex session on this machine to know about the same agentsfs, run:

```sh
afs connect ~/agentsfs --global
```

This writes to existing global config files only, such as `~/.claude/CLAUDE.md` or `~/.codex/AGENTS.md`. It affects every future session for that harness, so do it only when that is what you want.

### 7. Optional: enable semantic search

Full-text search works with no API key. Semantic search uses an embedding provider and stores the key in a user-local config file outside the agentsfs repo:

```sh
afs embeddings setup openai
cd ~/agentsfs
afs reindex --embeddings
afs search "what you are looking for" --semantic
```

Environment variables still work and take precedence. Set `OPENAI_API_KEY` or `VOYAGE_API_KEY` directly if you prefer managing secrets in your shell, CI, or agent harness config.

## Shared repo memory

Shared memory is for teams that want the agentsfs to ship with a codebase.

From the repo root:

```sh
afs init ./agentsfs --shared
```

This creates `./agentsfs` inside the repo and commits it with the code. It is intentionally explicit because git history is durable.

## Optional Git/GitHub backup and sync

agentsfs is just files plus git. There is no agentsfs account, hosted remote, token, or background sync service.

For backup or cross-device sync, use a normal git remote. GitHub is the friendliest default for most people, but any private git remote works.

### Existing GitHub account

Create an empty private repository on GitHub, then connect the local agentsfs:

```sh
cd ~/agentsfs
git remote add origin git@github.com:<user>/<repo>.git
git branch -M main
git push -u origin main
```

To sync later:

```sh
cd ~/agentsfs
git pull --ff-only
git push
```

To use the same memory on a new machine:

```sh
git clone git@github.com:<user>/<repo>.git ~/agentsfs
cd ~/code/myapp
afs connect ~/agentsfs --yes
```

### New to Git or GitHub

The agent should ask the user whether they know Git and whether they have a GitHub account. If the answer is no, explain the minimum:

- Git is the local history system already inside agentsfs.
- GitHub can hold a private online copy for backup and syncing.
- The user can also choose no remote and keep everything local.

Then guide the user through creating a GitHub account, creating a private empty repo, and choosing SSH or HTTPS authentication. Do not store GitHub passwords, personal access tokens, or SSH private keys inside `~/agentsfs` or any project repo.

## Troubleshooting

### `afs: command not found`

Install the command:

```sh
curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh
```

If it installs outside your current `PATH`, add the printed directory to your shell profile. For a source checkout install:

```sh
export PATH="$HOME/.local/bin:$PATH"
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install ./cmd/afs
hash -r 2>/dev/null || true
command -v afs
afs version
```

### Uninstall `afs`

Run:

```sh
afs uninstall --dry-run
afs uninstall --yes
```

If `afs` itself is not on `PATH`, remove the binary from the install directory you used, usually `~/.local/bin/afs`. This does not remove the agentsfs data directory.

### `afs init` refuses inside a git repo

That is expected. Choose one:

```sh
afs setup ~/agentsfs
afs init ./agentsfs --shared
```

### No `AGENTS.md` or `CLAUDE.md` exists

`afs setup --yes` or `afs connect <path> --yes` creates `./AGENTS.md` with the connection block.

### Git LFS is missing

Install git-lfs if you want large media tracked through LFS. Existing text knowledge still works without it. The Hub supports standard Git LFS transfers; already-committed non-LFS media is not rewritten automatically.

### Agent cannot read `~/agentsfs`

Allowlist `~/agentsfs` in the harness, or connect/global-config the harness in a way that gives it permission to read that path.
