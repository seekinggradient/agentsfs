# agentsfs setup

This guide has two audiences:

- **Humans** who want to install `afs` and connect their projects.
- **Agents** helping a user set up agentsfs safely.

The short version: install `afs`, run `afs setup` from a project, then let agents read the connection block in that project's `AGENTS.md` or `CLAUDE.md`.

## Concepts

`afs setup` is the normal first-run command.

It creates or reuses a personal agentsfs at `~/agentsfs`, then connects the current project to it.

`afs init PATH` only creates an agentsfs at `PATH`.

It does not connect a project or write global harness config.

`afs connect PATH` points the current project, or a global harness config, at an existing agentsfs.

It writes a small marker-fenced connection block into `AGENTS.md` or `CLAUDE.md`.

`afs init ./agentsfs --shared` is for team-shared memory committed inside a code repo.

Use it only when the user explicitly wants the memory to ship with that repo.

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
go install ./cmd/afs
export PATH="$(go env GOPATH)/bin:$PATH"
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

If the source fallback works only after adding Go's binary directory to `PATH`, add the `export PATH=...` line to your shell profile, such as `~/.zshrc`.

### 2. Connect your first project

Go to a project where you want agents to remember useful context:

```sh
cd ~/code/myapp
afs setup --yes
```

This creates or reuses `~/agentsfs`, then connects the current project by writing a connection block to the nearest `AGENTS.md` or `CLAUDE.md`. If neither exists, it creates `./AGENTS.md`.

### 3. Seed the filesystem

Open an agent in the connected project and ask it to run the first-session onboarding prompt from `prompts/onboarding.md`.

The agent should:

- read `~/agentsfs/AGENTS.md`
- ask what this memory is for
- create a small starter structure
- write dense notes with `description:` frontmatter and `[[wikilinks]]`
- commit the first useful state

### 4. Connect more projects

Run this from each additional project:

```sh
cd ~/code/another-project
afs connect ~/agentsfs --yes
```

The project now points at the same personal agentsfs.

### 5. Optional: connect global harness config

If you want every Claude Code or Codex session on this machine to know about the same agentsfs, run:

```sh
afs connect ~/agentsfs --global
```

This writes to existing global config files only, such as `~/.claude/CLAUDE.md` or `~/.codex/AGENTS.md`. It affects every future session for that harness, so do it only when that is what you want.

## Agent setup

When a user asks you to set up agentsfs, follow this flow.

### 1. Check whether `afs` is installed

Run:

```sh
afs version
```

If it works, continue. If it fails, install it with the packaged installer:

```sh
curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh
afs version
```

If the installer cannot download a release asset and cannot build from source, ask the user to install Go and git. If you are already inside the agentsfs source repo, this also works:

```sh
go install ./cmd/afs
export PATH="$(go env GOPATH)/bin:$PATH"
afs version
```

If you cannot install tools in the current environment, ask the user to run the installer.

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

## Shared repo memory

Shared memory is for teams that want the agentsfs to ship with a codebase.

From the repo root:

```sh
afs init ./agentsfs --shared
```

This creates `./agentsfs` inside the repo and commits it with the code. It is intentionally explicit because git history is durable.

## Troubleshooting

### `afs: command not found`

Install the command:

```sh
curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh
```

If it installs outside your current `PATH`, add the printed directory to your shell profile. For a source checkout install:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
afs version
```

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
