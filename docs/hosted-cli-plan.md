# Hosted CLI Plan

Status: updated for managed GitHub-backed hosted remotes.

## Goal

The `afs` CLI must work with hosted agentsfs while preserving the core invariant: hosted is convenience, not captivity. Users can always recover ordinary files and git data.

Git-backed hosted filesystems use a real remote URL. The CLI helps non-technical users avoid learning GitHub, but it does not pretend file-copy APIs are git sync.

Automatic background sync is not part of this CLI slice. A machine opts into hosted sync by storing a hosted CLI token and either cloning or connecting a hosted filesystem. Push and pull are explicit commands today, backed by ordinary git.

## Commands

```sh
afs login [--endpoint URL] [--token TOKEN | --token-stdin]
afs hosted create [name] [--description text]
afs hosted list
afs hosted connect <filesystem-id-or-url-or-name> [path] [--remote name]
afs hosted status [path]
afs hosted push [path] [--branch main]
afs hosted pull [path] [--branch main]
afs hosted clone <filesystem-id-or-url-or-name> [dir]
afs hosted backup [path] [--dry-run]
afs hosted restore [path] [--force]
```

`push`, `pull`, and `clone` require a hosted git remote. If the API does not return a remote, they fail with a message pointing to `backup` and `restore`.

`backup` and `restore` are explicit fallback commands for deployments that only expose the hosted file API. They are not git sync.

## Auth And Config

Users create a CLI access token from the signed-in hosted web app. The CLI stores that token outside any agentsfs repo:

- macOS: `~/Library/Application Support/agentsfs/hosted.json`
- Linux: `~/.config/agentsfs/hosted.json`
- test/dev override: `AFS_CONFIG_HOME`

The config file is written with `0600` permissions. `AGENTSFS_HOSTED_TOKEN` and `AGENTSFS_HOSTED_ENDPOINT` can override the file for ephemeral automation without committing secrets.

Local hosted connection metadata is stored in `.agentsfs/hosted.json`. That file contains only endpoint, filesystem id, display name, git remote URL/name, branch, and timestamp. It never contains the hosted API token or a GitHub token.

## Git Credential Flow

`afs hosted connect` adds or updates a local git remote, defaulting to `agentsfs`, and configures a URL-scoped repo-local git credential helper:

```sh
afs hosted credential-helper --filesystem fs_... --endpoint https://agentsfs.ai
```

GitHub remotes are stored with the non-secret `x-access-token` username, which makes ordinary git use the hosted helper instead of an unrelated global GitHub credential. The URL-scoped helper starts with an empty helper entry so short-lived credentials are not stored by global helpers such as the macOS keychain.

Git calls this helper when it needs HTTPS credentials. The helper authenticates to the hosted API with the user-scoped `afs_...` token stored outside the repo, then asks the hosted API for a short-lived git credential for that filesystem. The helper emits only Git's credential protocol fields:

```text
username=x-access-token
password=<short-lived git credential>
```

The short-lived credential is never written to `.agentsfs/hosted.json`, stdout of normal commands, or docs.

## Git Semantics

`afs hosted push` runs:

```sh
git push agentsfs HEAD:refs/heads/main
```

`afs hosted pull` runs:

```sh
git pull --ff-only agentsfs main
```

`afs hosted clone` runs real `git clone`, then writes non-secret hosted connection metadata into the cloned agentsfs. Fresh clones leave both `origin` and `agentsfs` ready for ordinary `git pull` and `git push`.

Advanced users can run ordinary git commands directly after `connect`.

On a new machine, `afs hosted clone <filesystem> [dir]` is the setup path for a hosted-synced agentsfs. For an existing local agentsfs, `afs hosted connect <filesystem>` opts that repo into the hosted remote. `afs setup` remains local/project setup and does not imply hosted sync.

## Fallback File Semantics

`afs hosted backup` walks the local agentsfs with the same core walker used by `afs tree`: `.git/` and `.agentsfs/` are skipped. Ordinary dotfiles such as `.gitattributes` and `.gitignore` are allowed.

The hosted file API remains text-first, so backup rejects non-UTF-8 files with a clear error. The server enforces hosted MVP limits, including the 500-file default limit.

`afs hosted restore` fetches the hosted tree and reads each hosted file. It writes files under the local agentsfs root, creates directories as needed, and refuses to overwrite differing local files unless `--force` is passed.

Backup does not delete remote-only files. Restore does not delete local-only files. These commands are backup/import operations, not bidirectional sync.

## Verification

CLI tests cover:

- hosted help text
- login config writes outside the repo and does not print tokens
- local hosted connection metadata writes without tokens
- git remote and credential helper configuration
- credential helper output excludes the hosted API token
- `afs hosted push` pushes to a disposable bare git remote
- `afs hosted clone` clones back from that disposable remote
- backup request paths and internal-directory skipping
- restore overwrite refusal and `--force`
- path validation for ordinary dotfiles versus `.git` and `.agentsfs`

Run from the repo root:

```sh
go test ./...
```
