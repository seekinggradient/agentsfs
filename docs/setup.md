# agentsfs setup

This guide has two audiences:

- **Agents** helping a user set up agentsfs safely.
- **Humans** who want to install `afs` and connect their projects.

The short version: install `afs`, run `afs setup` from a project, then let agents read the connection block in that project's `AGENTS.md` or `CLAUDE.md`.

## Concepts

`afs setup` is the normal first-run command.

It creates or reuses a personal agentsfs at `~/agentsfs`, then connects the current project to it.

`afs setup` is local-only: it does not choose a hosted remote, enable automatic background sync, or send data to a server. Hosted sync is an explicit opt-in later with `afs login`, `afs hosted clone`, or `afs hosted connect`.

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

If you have a local checkout, set `AGENTSFS_SOURCE` and install from there:

```sh
export PATH="$HOME/.local/bin:$PATH"
mkdir -p "$HOME/.local/bin"
export AGENTSFS_SOURCE=/path/to/agentsfs
(cd "$AGENTSFS_SOURCE" && GOBIN="$HOME/.local/bin" go install ./cmd/afs)
hash -r 2>/dev/null || true
command -v afs
afs version
```

Otherwise, use the packaged installer:

```sh
export PATH="$HOME/.local/bin:$PATH"
mkdir -p "$HOME/.local/bin"
AFS_INSTALL_DIR="$HOME/.local/bin" curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh
hash -r 2>/dev/null || true
command -v afs
afs version
```

If the installer gets a 404 from GitHub, the repo or release assets are not public yet. Ask the user for a local checkout path and use the `AGENTSFS_SOURCE` flow above.

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

Then follow `prompts/onboarding.md`: interview the user briefly, create the first small structure, write dense notes, and commit from the agentsfs root:

```sh
cd ~/agentsfs
git status --short
git add -A .
git commit -m "Seed agentsfs"
```

If git identity is missing, explain the commit failure and leave the files staged or ready for the user to commit.

## Human setup

### 1. Install `afs`

The fastest path is the installer:

```sh
curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh
afs version
```

The installer downloads a released binary when one exists. If no release asset is available yet, it falls back to building from source, which requires Go and git.

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

If `git lfs version` fails, agentsfs still works. The CLI prints a note and skips LFS setup.

If you intentionally install somewhere else, add that directory to your shell profile and verify a new shell can find it. On zsh, login shells read `~/.zprofile` and interactive shells read `~/.zshrc`, so putting the PATH line in both is the safest choice.

### 2. Uninstall later

To remove the local CLI and hosted login credentials from this machine:

```sh
afs uninstall --dry-run
afs uninstall --yes
```

`afs uninstall` never deletes `~/agentsfs`, any agentsfs repo, git history, hosted filesystem, or project-local `AGENTS.md` / `CLAUDE.md` connection block. Pass `--keep-auth` to leave hosted login credentials in the OS config directory. Pass `--remove-global-connections` only when you also want to remove agentsfs blocks from known global Claude/Codex harness configs.

If `afs` is installed by Homebrew or another package manager, uninstall it with that manager. The CLI refuses to unlink package-manager or system-managed binaries unless you pass an explicit `--binary PATH`.

### 3. Connect your first project

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
- ask what this memory is for
- create a small starter structure
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

## Shared repo memory

Shared memory is for teams that want the agentsfs to ship with a codebase.

From the repo root:

```sh
afs init ./agentsfs --shared
```

This creates `./agentsfs` inside the repo and commits it with the code. It is intentionally explicit because git history is durable.

## Hosted CLI managed git

Hosted agentsfs is optional. The local filesystem and ordinary git repo remain the durable source of truth.

Managed hosted filesystems expose a real git remote. The CLI hides the GitHub details for non-technical users, but the data remains recoverable with ordinary git commands.

There is no background sync daemon in the current CLI. "Use hosted sync" means the machine has a hosted connection and can run explicit git-backed sync commands. Use `afs hosted clone` when setting up a new machine from an existing hosted filesystem, or `afs hosted connect` when attaching an existing local agentsfs to a hosted filesystem.

### 1. Create and store a CLI token

Sign in at:

```sh
https://agentsfs.ai/app/filesystems
```

Create a CLI access token in the app. The token is shown once. Store it outside any agentsfs repo:

```sh
afs login --token-stdin
```

For local development or alternate deployments:

```sh
afs login --endpoint http://127.0.0.1:4321 --token-stdin
```

The token is written to the OS config directory, such as `~/Library/Application Support/agentsfs/hosted.json` on macOS or `~/.config/agentsfs/hosted.json` on Linux. Do not commit that file.

### 2. Create or connect a hosted filesystem

Create from the CLI:

```sh
afs hosted create "Research memory"
afs hosted list
```

Connect an existing local agentsfs:

```sh
cd ~/agentsfs
afs hosted connect fs_...
```

The local connection metadata is written to `.agentsfs/hosted.json`, which is machine territory and ignored by the agentsfs template. It contains the hosted endpoint, filesystem id, display name, and git remote URL. It does not contain the hosted API token or a GitHub token.

`afs hosted connect` also adds or updates a local git remote named `agentsfs` and configures a URL-scoped local credential helper. GitHub remotes include the non-secret `x-access-token` username so ordinary git uses the hosted helper instead of an unrelated global GitHub credential. The helper asks the hosted API for a short-lived git credential only when git needs one.

### 3. Check status and sync with git

```sh
afs hosted status
afs hosted push
afs hosted pull
```

On Git-backed hosted filesystems, `afs hosted push` runs `git push agentsfs HEAD:refs/heads/main`, and `afs hosted pull` runs `git pull --ff-only agentsfs main`. You can still use ordinary git directly after `connect`:

```sh
git push agentsfs HEAD:main
git pull --ff-only agentsfs main
git clone https://github.com/<managed-owner>/<managed-repo>.git
```

For now, the user or agent decides when to push and pull. A future auto-sync layer should be built on top of this git remote, not as a separate file-copy sync protocol.

To restore a hosted filesystem into a new local directory:

```sh
afs hosted clone fs_... ~/agentsfs-restored
```

On Git-backed hosted filesystems, that command runs real `git clone`, then writes the same non-secret `.agentsfs/hosted.json` connection metadata.

### 4. File API fallback

If a self-hosted or development deployment does not expose a git remote, the CLI says so and offers explicit file-copy fallback commands:

```sh
afs hosted backup
afs hosted restore --force
```

These upload/download UTF-8 text files through the hosted API. They are recovery/backup commands, not git sync.

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

Install git-lfs if you want large media tracked through LFS. Existing text knowledge still works without it.

### Agent cannot read `~/agentsfs`

Allowlist `~/agentsfs` in the harness, or connect/global-config the harness in a way that gives it permission to read that path.
