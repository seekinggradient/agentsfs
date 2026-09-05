---
description: RFC — make embedded AgentsFS instances discoverable from a host project, publish them predictably to Hub main, and give status the actionable worktree and synchronization semantics users expect from Git.
status: accepted
rfc_status: accepted
rfc_scope: project
rfc_owner_approved: true
date: 2026-08-03
sources:
  - internal/core/instance.go
  - internal/core/status.go
  - internal/hubclient/hubclient.go
  - cmd/afs/hub.go
  - cmd/afs/main.go
  - https://git-scm.com/docs/git-status
  - https://git-scm.com/docs/git-push
  - https://git-scm.com/docs/git-remote
---

# RFC: Git-grade status and predictable Hub sync for embedded AgentsFS

## Summary

Make an AgentsFS workspace embedded inside an ordinary Git project feel like a first-class, independently synchronized body of work without pretending that Git itself has directory-scoped remotes.

This RFC makes three product changes:

1. **Root-level instance discovery.** Instance-bound Hub commands may be run from the host repository root—or anywhere within it—when exactly one embedded AgentsFS instance can be resolved. Ambiguous repositories require an explicit `--instance` path; AFS never guesses.
2. **Stable publication to `main`.** `afs hub push` always publishes the selected AgentsFS projection to `refs/heads/main`, independent of the host repository's current branch. It never force-pushes and reports non-fast-forward conflicts explicitly.
3. **Git-grade status.** `afs status` continues to support fleet discovery, contract health, and duplicate detection, but its default focused view becomes an actionable status report. It distinguishes the embedded instance's scoped worktree state, the host repository's commit state, and the AgentsFS projection's Hub publication state. `afs hub status` uses the same status core rather than maintaining a second, shallow implementation.

The central model is:

```text
host worktree/repository                 Hub repository
┌──────────────────────────────┐         ┌──────────────────────┐
│ application files            │         │ AGENTS.md            │
│ agentsfs/                     │ subtree │ INDEX.md             │
│   AGENTS.md                   │ project │ rfcs/…                │
│   INDEX.md                    │ ──────► │ agent-journal/…       │
│   rfcs/…                      │         │                      │
└──────────────────────────────┘         └──────────────────────┘
       host branch: any                         branch: main
```

An embedded AgentsFS has two related but different synchronization questions:

- **Host Git:** Are its files staged, unstaged, untracked, or committed in the enclosing project?
- **Hub publication:** Has the latest committed AgentsFS projection been published to `hub/main`?

AFS must answer both. Reporting only the host branch's upstream status is not an adequate proxy for Hub synchronization.

## Decision

Implement all three changes as one coherent sync/status layer rather than as independent CLI patches.

- Introduce one instance resolver shared by `afs hub push`, `afs hub status`, and focused `afs status`.
- Treat `main` as the canonical Hub publication branch.
- Introduce an instance-scoped Git status model that separates worktree, host-repository, and publication information.
- Preserve the existing multi-instance discovery report as an explicit or automatically selected fleet view.
- Preserve local-only behavior unless `--fetch` or a mutating Hub command is explicitly requested.
- Keep `git subtree split` as the publication mechanism for embedded instances, but do not make ordinary status generate a new subtree projection merely to display status.

## Motivation

### The field failure

A real embedded instance exposed three connected failures:

1. From the host project root, `afs hub push` failed with “not inside an agentsfs,” even though `afs status` could discover the embedded instance below that root.
2. Running `afs hub push` from the instance directory reported a successful upload, but it used the host feature branch name. The Hub UI showed `main`, so the newly uploaded note appeared to be missing.
3. `afs hub status` reported only sign-in and linkage. It could not say which branch was published, which commit was on Hub, whether local committed knowledge was unpushed, whether uncommitted knowledge was excluded, or whether the comparison was based on fresh remote state.

The operator ultimately had to use native Git plumbing to diagnose the truth:

```text
remote main:      477f033
remote feature:   bef4f22  (contained the note)
```

The upload had succeeded mechanically but failed the user's product expectation: “push this workspace so it appears on Hub.”

### The current abstraction leak

Git remotes belong to the enclosing repository, not to a subdirectory. In a shared/embedded topology, both of these paths resolve to the same `.git` directory:

```text
/project
/project/agentsfs
```

The current implementation already protects content boundaries during push: `revisionForPush` uses `git subtree split --prefix=<instance>` so application files outside the AgentsFS root are not uploaded. But the rest of the product still reasons primarily in terms of the enclosing repository:

- `core.FindRoot` searches upward only, so Hub commands cannot resolve an embedded instance from the host root.
- `hubclient.Push` derives the destination branch from `currentBranch(root)`.
- `inspectGitStatus` reads the enclosing repository branch and upstream, then compares `HEAD...@{upstream}` for the entire host repository.
- `hubStatus` reports only account and remote linkage.

This creates misleading states. An embedded workspace may be:

- clean and committed in the host repository but not published to Hub;
- dirty locally while its last committed projection is fully published;
- on a host feature branch while the desired Hub destination remains `main`;
- synced to the host's `origin` but behind or divergent from its Hub repository;
- one of several AgentsFS instances sharing the same enclosing `.git` directory.

### Product expectation

Users and agents already understand the useful parts of `git status`:

- what repository and branch they are on;
- which files are staged, unstaged, or untracked;
- what will and will not be included in a commit;
- whether local commits are ahead of or behind a remote;
- the exact next command when action is required.

AFS should preserve those concepts and add only the AgentsFS-specific facts Git cannot know: the instance boundary, projection mapping, contract health, Hub target, and last publication state.

## Goals

1. Let `afs hub push` and `afs hub status` work from a host project containing exactly one embedded AgentsFS.
2. Make instance selection deterministic, bounded, explainable, and overridable.
3. Ensure every ordinary Hub push targets `main`, regardless of the host branch.
4. Never force-push or silently replace divergent remote history.
5. Tell the user exactly which instance, source commit, projected commit, remote repository, and remote branch were involved.
6. Show staged, unstaged, and untracked changes scoped to the AgentsFS path.
7. Distinguish host-repository synchronization from Hub publication synchronization.
8. Preserve a useful multi-instance/fleet view for discovery and maintenance.
9. Keep status local and credential-free by default; make network refresh explicit.
10. Provide stable, additive JSON suitable for agents and automation.
11. Share logic among CLI, MCP status, and Hub commands so their answers cannot drift.

## Non-goals

- Making plain `git push hub HEAD` safe for embedded instances. Git refspecs select refs, not directory projections; a future Git remote helper or guarded remote design can address this.
- Replacing Git as the commit/history substrate.
- Automatically committing dirty knowledge before push.
- Automatically merging or rebasing divergent Hub history.
- Force-pushing Hub history.
- Automatically choosing among multiple embedded instances.
- Making all AFS commands search downward. This RFC introduces a command resolver for status and Hub sync; other commands may adopt it after the behavior is proven.
- Turning `afs status` into `afs doctor`. Contract and health summaries remain visible, while full findings stay in `afs doctor` or `afs status --doctor`.
- Performing an implicit fetch on every status invocation.
- Implementing a submodule topology or custom `git-remote-afs` helper.

## Terminology

- **Instance:** One AgentsFS workspace root, identified by `.agentsfs/` or a contract-declaring `AGENTS.md`.
- **Host repository:** The enclosing Git worktree returned by `git rev-parse --show-toplevel`.
- **Standalone instance:** The AgentsFS root equals the host repository root.
- **Embedded instance:** The AgentsFS root is a proper subdirectory of the host repository. This is currently called `shared` in status; JSON should retain the old value during compatibility migration while human output may say “embedded (shared history).”
- **Instance prefix:** The slash-relative path from host repository root to instance root, such as `agentsfs`.
- **Projection:** The Git tree/history containing only the AgentsFS instance, rooted at `/`, produced for Hub publication.
- **Host upstream:** The upstream of the host repository's current branch, often `origin/main`.
- **Publication target:** The Hub repository and canonical branch `main`.
- **Cached remote state:** Locally known remote-tracking refs, which may be stale until `--fetch`.

## Design 1: root-level instance discovery

### Shared resolver

Add a resolver in `internal/core` with a result shaped approximately as follows:

```go
type InstanceResolution struct {
    InstanceRoot string
    RepoRoot     string
    Prefix       string // "." for standalone
    Mode         string // standalone | embedded
    DetectedBy   string // enclosing | project-scan | explicit
}

type ResolveInstanceOptions struct {
    ExplicitPath string
    AllowProjectScan bool
}
```

The exact names are not normative. The behavior is.

### Resolution algorithm

Given a starting directory:

1. If `--instance <path>` is present, canonicalize it and require that it is an AgentsFS root or lies inside one. Return that instance. An invalid explicit path is an error; do not fall back.
2. Run the existing upward `FindRoot(start)`. If it succeeds, return the enclosing instance.
3. Find the enclosing Git worktree root. If there is no Git worktree, return the existing not-inside-an-instance error with setup/discovery guidance.
4. Search downward **only within that Git worktree** for AgentsFS roots, using the existing bounded/pruned status discovery rules. Do not scan parent workspaces, the home directory, symlink targets, dependencies, caches, or nested Git repositories.
5. If exactly one instance is found, return it with `DetectedBy: project-scan`.
6. If none are found, return an actionable error.
7. If more than one is found, return an ambiguity error listing canonical project-relative paths and require `--instance`.

Example success:

```text
$ cd /project
$ afs hub status
Using embedded AgentsFS: ./agentsfs
…
```

Example ambiguity:

```text
afs: 2 AgentsFS instances are embedded in /project:
  ./agentsfs
  ./teams/research-memory

Choose one with:
  afs hub status --instance ./agentsfs
```

### Search boundaries

Root-level discovery must be cheaper and narrower than fleet discovery:

- Search root is exactly the enclosing Git worktree.
- Never cross a nested `.git` boundary.
- Never follow symlinked directories during implicit discovery.
- Reuse the status scanner's ignored directory set.
- Stop after two valid instances when the caller needs only unique-vs-ambiguous resolution; there is no reason to scan the rest of a huge repository once ambiguity is established.
- Preserve canonical and display paths separately so diagnostics can show concise relative paths without losing identity correctness.

### CLI surface

```text
afs hub push [name] [--instance PATH]
afs hub status [--instance PATH] [--fetch] [--json]
afs status [PATH ...] [--all] [--json] [--doctor] [--fetch]
```

For `afs status`, `PATH` continues to describe an inspection scope. `--all` explicitly requests fleet/discovery presentation. For Hub commands, `--instance` selects the workspace and cannot be confused with the optional Hub repository name.

### Connection metadata

Project connection blocks may provide a future fast path, but they are not authoritative in version 1: they can be stale, absent, or point outside the project. Filesystem markers remain the source of truth. If a connection block is consulted later, its path must be verified as a real AgentsFS before use.

## Design 2: stable publication to Hub `main`

### Canonical branch

Change `hubclient.Push` so its destination ref is always:

```text
refs/heads/main
```

Do not call `currentBranch` to select the remote branch. The host branch remains useful provenance and should be reported, but it does not control publication.

Examples:

```text
host main                                  → hub/main
host codex/tool-retrieval-experiments      → hub/main
host detached HEAD                         → hub/main
```

The push remains a normal non-forced Git update. If the projected commit is not a fast-forward of remote `main`, the operation fails without changing the remote.

### Push flow

`afs hub push` performs these phases:

1. **Resolve** the instance and host repository.
2. **Inspect** scoped worktree state and identify the host `HEAD` being exported.
3. **Link** or resolve the credential-free Hub repository URL.
4. **Project** the selected instance:
   - standalone: `HEAD`;
   - embedded: `git subtree split --prefix=<prefix> HEAD`.
5. **Push** the projected commit to `refs/heads/main`, without force.
6. **Verify** that the remote `main` resolves to the projected commit.
7. **Record** local publication metadata only after verification succeeds.
8. **Report** the exact result, including excluded uncommitted work.

### Dirty worktree semantics

Match Git's central rule: push publishes commits, not uncommitted files.

Do not silently imply that dirty files were uploaded. Before projection, collect instance-scoped staged, unstaged, and untracked counts. The push may proceed with the committed snapshot, but its final output must warn that these files were excluded:

```text
Published committed AgentsFS state to production-agent-research/main.
  Host source: 4365e27 on codex/attention-tool-retrieval-experiments
  Projection:  bef4f22
  Verified:    hub/main at bef4f22
  Browse:      https://hub.agentsfs.ai/seekinggradient/production-agent-research

Not included: 2 unstaged files and 1 untracked file under ./agentsfs.
Commit them, then run `afs hub push` again.
```

This is preferable to refusing every dirty push: a user may intentionally publish the last completed commit while another work unit is in progress. Automation that requires a clean tree may add a future `--require-clean`; it is not required for this RFC.

### Non-fast-forward behavior

Never retry with force. Return an error that identifies both commits and the safe next step:

```text
afs: hub/main has changes that are not in this AgentsFS projection.
  local projection:  18b00a1
  remote main:       72de91c

Nothing was overwritten. Run `afs hub pull <repo> --merge` or reconcile the
Hub repository in a standalone clone, commit the result, and push again.
```

The implementation may fetch `main` before pushing to improve this diagnostic, because `push` is already an explicit network mutation. Authentication errors and network errors must remain distinguishable from history divergence.

### Local publication metadata

Status needs to know what a previous successful push published without running `git subtree split` on every read-only invocation. Record rebuildable, credential-free metadata under instance machine state, for example:

```json
{
  "schema_version": 1,
  "remote_name": "hub",
  "remote_url": "https://hub.agentsfs.ai/user/repo.git",
  "publish_branch": "main",
  "last_push": {
    "source_repo_head": "4365e27…",
    "projected_commit": "bef4f22…",
    "verified_remote_commit": "bef4f22…"
  }
}
```

Requirements:

- Store no credentials or authenticated URLs.
- Treat the file as derived state, never knowledge.
- Write it atomically only after remote verification.
- Tolerate its absence and reconstruct what can be learned from Git refs/remotes.
- Do not require it for cloning or accessing the plain files.
- Key state per instance, not merely per enclosing repository; multiple embedded instances must not overwrite one another's publication state.

The implementation may choose `.agentsfs/hub.json` or an equivalent machine-state location. It should not add publication bookkeeping to human-authored Markdown.

### Hub remote compatibility

Existing instances use a Git remote named `hub`; preserve recognition of that remote. This RFC does not attempt to make ordinary `git push hub HEAD` safe for embedded instances. Focused status must therefore explain the projection and may warn:

```text
Embedded instance: use `afs hub push`; plain `git push hub HEAD` would address
the enclosing project repository, not this directory projection.
```

Supporting multiple embedded Hub-linked instances may require instance-local linkage metadata rather than relying on one repository-wide remote name. The implementation must not let selecting one embedded instance silently retarget another instance's Hub destination. If the existing `hub` remote is retained as a compatibility alias, the instance-local link remains authoritative.

## Design 3: Git-grade status

### One status core, two presentations

`afs status` serves two legitimate jobs:

1. **Focused status:** “What is the state of the AgentsFS I am working on, and what should I do next?”
2. **Fleet discovery:** “What AgentsFS instances exist beneath these roots, and which need attention?”

Keep both, but stop forcing the discovery-oriented table into every local workflow.

Presentation selection:

- `afs status` with an enclosing or uniquely resolvable project instance: focused status.
- `afs status <instance-path>` when the path resolves to one instance: focused status.
- `afs status <workspace-root>` when multiple instances are found: fleet status.
- `afs status --all [roots...]`: fleet status, even if only one instance is found.
- `afs status --json`: one additive report schema with a `presentation`/`mode` field; it does not rely on human-mode heuristics.

The existing broad scanner, completeness budgets, duplicate detection, contract version, and optional doctor summary remain. The change is primarily richer per-instance data and better default narration.

### Three status planes

Each instance status contains three explicit planes.

#### A. Worktree status, scoped to the instance

Report paths relative to the instance root:

- staged additions/modifications/deletions/renames;
- unstaged modifications/deletions;
- untracked files;
- conflicts/unmerged paths;
- clean/dirty summary.

Use porcelain output designed for parsing, with NUL delimiters for filenames. Do not infer these categories from a single dirty boolean. Never include unrelated changes elsewhere in an embedded host repository.

Human output should follow Git's useful hierarchy:

```text
Changes staged for commit:
  modified:   rfcs/INDEX.md
  new file:   rfcs/embedded-git-status-and-hub-sync.md

Changes not staged:
  modified:   agent-journal/INDEX.md

Untracked files:
  agent-scratch/status-notes.txt
```

Large lists should be bounded in human output with a count and `--json`/`--short` escape hatch. JSON returns the full bounded result and explicitly marks truncation.

#### B. Host repository status

Report:

- host repository root;
- standalone vs embedded topology;
- instance prefix;
- current host branch or detached `HEAD`;
- host upstream and cached ahead/behind counts, if configured;
- whether commits touching the instance exist after the last successful Hub publication source commit.

The host upstream is contextual information, not the Hub publication state. Label it accordingly. Never render `origin/main: synced` under a generic “SYNC” heading that could be mistaken for Hub sync.

#### C. Hub publication status

Report:

- linked/unlinked;
- Hub owner/repository and credential-free URL;
- canonical publication branch (`main`);
- last successfully published host source commit;
- last projected commit;
- locally cached `hub/main` commit, if available;
- whether the committed instance tree has changed since the last successful push;
- whether remote comparison is cached or freshly fetched;
- actionable publication state.

Publication states:

| State | Meaning | Suggested action |
|---|---|---|
| `unlinked` | No Hub destination is configured for this instance | `afs hub push [name]` |
| `never-published` | Linked but no successful publication is recorded | `afs hub push` |
| `published` | Last committed instance state matches the verified publication | None |
| `commits-to-publish` | Committed instance changes exist after the last published source state | `afs hub push` |
| `remote-ahead` | Fresh/cached Hub main contains commits not represented locally | Pull/reconcile before push |
| `diverged` | Both local committed knowledge and Hub main changed | Reconcile; never force |
| `unknown` | Required metadata or refs are unavailable | `afs hub status --fetch` or diagnostic command |
| `error` | Inspection failed | Show bounded error and exact manual command |

Dirty worktree state is orthogonal. For example:

```text
Hub: published
Worktree: 2 unstaged changes (not published)
```

### Determining committed changes without creating a projection

Focused status must remain a read operation. It should not run `git subtree split` solely to answer status, because subtree splitting may create Git objects even without updating a visible branch.

Use the successful-push metadata:

- If `source_repo_head` is an ancestor of current host `HEAD`, count commits touching the instance prefix with `git rev-list <last>..HEAD -- <prefix>`.
- Compare instance tree/object IDs at the recorded source commit and current `HEAD` to distinguish unrelated host commits from actual AgentsFS changes.
- If history was rewritten and the recorded source commit is no longer an ancestor, compare the instance tree IDs. Report “committed content changed; host history rewritten” rather than inventing an ahead count.
- For standalone instances, normal Git ancestry against cached `hub/main` remains available.

Status may calculate a new projection only under an explicit diagnostic flag in the future. It is unnecessary for the initial design.

### Fetch semantics

Default status performs no network access.

```text
Remote comparison: cached; run `afs status --fetch` for current Hub state.
```

With `--fetch`:

- Fetch only remotes relevant to the selected instance in focused mode; do not blindly run `git fetch --all` when the host repository has unrelated remotes.
- Fetch `refs/heads/main` into an instance-appropriate remote-tracking ref.
- Bound network operations with a timeout.
- Sanitize credential-bearing errors.
- Mark `remote_state: fresh` only after a successful fetch in that invocation.
- A failed fetch does not erase locally useful worktree or contract status.

Fleet `--fetch` may deduplicate fetches by `(repository root, remote)` but must preserve per-instance errors.

### Focused human output

Example: embedded, dirty, with one committed change not yet published.

```text
AgentsFS: /project/agentsfs
Purpose:  Research and design memory for the project.
Mode:     embedded in /project (prefix: agentsfs/)
Contract: 0.9.0, current

Host Git
  Branch:    codex/status-rfc
  Upstream:  origin/codex/status-rfc (ahead 1)
  Knowledge commits since last Hub push: 1

Worktree
  Changes not staged: 1
    modified: rfcs/INDEX.md
  Untracked files: 1
    rfcs/embedded-git-status-and-hub-sync.md

Hub publication
  Repository: seekinggradient/agentsfs-design
  Target:     main
  State:      1 committed AgentsFS change to publish
  Last push:  source cf1ecbb → projection 8ab3d20
  Remote:     cached hub/main at 8ab3d20

Next actions
  1. Commit the 2 worktree changes in the host repository.
  2. Run `afs hub push` to publish committed AgentsFS changes to Hub main.

Note: use `afs hub push` for this embedded instance; plain `git push hub HEAD`
would address the enclosing project rather than the AgentsFS projection.
```

Example: clean and published.

```text
AgentsFS: /project/agentsfs
Mode:     embedded in /project (prefix: agentsfs/)
Contract: 0.9.0, current
Worktree: clean
Host Git: main at cf1ecbb, tracking origin/main (up to date)
Hub:      published to production-agent-research/main at bef4f22
Remote comparison: cached; use `--fetch` to refresh
```

### Fleet human output

Keep a compact table, but separate host and publication states:

```text
PATH                 CONTRACT  MODE      WORKTREE  HOST GIT   HUB
/kb/personal         current   standalone clean    synced     published
/app/agentsfs        current   embedded   2 files  ahead 1    1 to publish
/work/team-memory    behind    standalone clean    synced     unlinked

3 instances; 1 contract behind; 1 dirty; 1 with commits to publish; 1 unlinked.
```

The verbose scan-scope narration should appear only when it adds value: explicit fleet scans, partial scans, or `--verbose`. A focused status should not begin with entry-budget statistics.

### JSON model

Evolve the existing JSON additively. A possible shape:

```json
{
  "schema_version": 2,
  "presentation": "focused",
  "search_roots": ["/project"],
  "instances": [{
    "path": "/project/agentsfs",
    "description": "…",
    "contract": {"version": "0.9.0", "state": "current"},
    "topology": {
      "mode": "embedded",
      "repository_root": "/project",
      "prefix": "agentsfs"
    },
    "worktree": {
      "clean": false,
      "staged": [],
      "unstaged": [{"path": "rfcs/INDEX.md", "status": "modified"}],
      "untracked": ["rfcs/new-rfc.md"],
      "conflicted": [],
      "truncated": false
    },
    "host_git": {
      "branch": "codex/status-rfc",
      "head": "4365e27…",
      "upstream": "origin/codex/status-rfc",
      "ahead": 1,
      "behind": 0
    },
    "publication": {
      "linked": true,
      "remote": "hub",
      "repository": "seekinggradient/agentsfs-design",
      "branch": "main",
      "state": "commits-to-publish",
      "commits_to_publish": 1,
      "last_source_commit": "cf1ecbb…",
      "last_projected_commit": "8ab3d20…",
      "cached_remote_commit": "8ab3d20…",
      "remote_state": "cached"
    },
    "next_actions": [{
      "kind": "commit",
      "command": "git add -- agentsfs && git commit",
      "reason": "2 uncommitted files"
    }]
  }]
}
```

Compatibility requirements:

- Keep existing top-level fields through at least one compatibility cycle.
- Keep the existing `git` object or provide a documented version transition; do not silently change its meaning from host upstream to Hub publication.
- Add `schema_version` before clients are expected to branch on the richer shape.
- MCP status returns the same core structure as CLI JSON.
- Never include credential-bearing remote URLs.

### `afs hub status`

Replace the current account/link-only output with the same focused status core, filtered to Hub concerns:

```text
$ afs hub status --fetch
Signed in to https://hub.agentsfs.ai as seekinggradient.
Instance:   /project/agentsfs (embedded; prefix agentsfs/)
Repository: seekinggradient/production-agent-research
Target:     main
Worktree:   clean
Committed:  2 AgentsFS commits to publish
Remote:     fresh; hub/main at bef4f22
State:      ready to push
Next:       afs hub push
```

`afs hub status --json` should include account status plus the selected instance's publication model. It must work from the host project root through the shared resolver.

## Safety and privacy

1. Root discovery never crosses the enclosing Git worktree during implicit Hub command resolution.
2. Ambiguous instance selection is a hard error.
3. Projection remains mandatory for embedded instances; application files must never enter the Hub commit.
4. Push uses a credentialed one-shot transport while stored URLs remain credential-free.
5. Status never prints URL userinfo, tokens, credential-helper output, or unbounded remote errors.
6. Push never force-updates `main`.
7. Status does not fetch without `--fetch`.
8. Worktree inspection is path-scoped to the instance and uses `GIT_OPTIONAL_LOCKS=0` or `git --no-optional-locks` where supported so the read-only claim is materially true.
9. Filenames are parsed with NUL-delimited porcelain output; spaces, Unicode, tabs, newlines, and renames must not corrupt the model.
10. Derived publication metadata is atomic, credential-free, and rebuildable.

## Backward compatibility and migration

### CLI compatibility

- `afs hub push [name]` continues to work from inside an instance.
- It additionally accepts `--instance` and resolves a unique embedded instance from the host root.
- Existing Hub repositories continue to be recognized from their clean remote URL.
- The remote destination changes from the current host branch to `main`. This is intentional and must be called out in release notes.
- `PushResult.Branch` becomes `main`; add `SourceBranch` if callers need provenance.
- `afs status <roots...>` and its discovery capabilities remain; `--all` makes fleet intent explicit.
- JSON changes are additive and versioned.

### Existing non-main Hub branches

Some repositories may contain AgentsFS commits only on a branch copied from a host feature branch. Migration behavior:

1. `afs hub status --fetch` detects when `main` lacks the last known projected commit but another known Hub branch contains it, when that information is locally available.
2. Status reports the condition without silently promoting it.
3. The next `afs hub push` targets `main`. If this is a fast-forward, it completes normally.
4. If promoting would be non-fast-forward, the command stops with reconciliation guidance.
5. Do not delete historical feature branches automatically.

The Hub web UI may later offer branch cleanup, but it is outside this CLI RFC.

### Linkage migration

On first successful push after upgrade, write the new per-instance publication metadata from the verified result. Before that point, status may derive linkage from the existing `hub` remote and report missing last-push provenance as `unknown` rather than `unpublished`.

## Implementation outline

### Core instance resolution

- `internal/core/instance.go`
  - Add bounded project-root resolution with explicit ambiguity results.
  - Reuse shared marker validation and pruning logic rather than duplicating status discovery.
- `internal/core/status.go`
  - Extract scanner primitives needed by the resolver.
  - Preserve fleet scan completeness accounting.

### Publication

- `internal/hubclient/hubclient.go`
  - Replace host-branch destination with `main`.
  - Return source branch, source commit, projected commit, verified remote commit, and scoped dirty counts.
  - Add remote verification and publication-state persistence.
  - Separate per-instance linkage from repository-global remote assumptions where necessary.
- `cmd/afs/hub.go`
  - Parse `--instance`, `--fetch`, and `--json` on relevant subcommands.
  - Use the shared resolver.
  - Print precise, Git-like outcomes and warnings.

### Status

- `internal/core/status.go`
  - Replace `Dirty bool` as the only worktree detail with structured scoped porcelain parsing.
  - Split host upstream from publication state.
  - Add cached/fresh remote-state provenance.
  - Add next-action derivation from structured states.
- `cmd/afs/main.go`
  - Add focused and fleet renderers.
  - Keep output bounded and actionable.
- `internal/mcpserver/server.go`
  - Return the versioned shared status model without a parallel implementation.

### Documentation

- Update `README.md`, `docs/cli.md`, `docs/setup.md`, `docs/hub.md`, bundled command help, and the template contract's sync guidance.
- Explain that host Git sync and Hub publication are distinct for embedded instances.
- Document the safety warning against `git push hub HEAD` from an embedded topology.

## Test plan

### Resolver tests

1. CWD inside a standalone instance resolves upward.
2. CWD inside an embedded instance resolves upward.
3. CWD at host repository root with exactly one embedded instance resolves downward.
4. CWD in an unrelated host subdirectory still resolves the unique embedded instance.
5. Two embedded instances produce an ambiguity error with both relative paths.
6. `--instance` selects one of several instances.
7. Invalid explicit path fails without fallback.
8. Implicit discovery does not follow symlinks, enter nested repositories, or scan outside the host worktree.
9. Discovery stops after proving ambiguity.
10. Paths containing spaces and symlinks canonicalize consistently.

### Push tests

1. Standalone `main` publishes to Hub `main`.
2. Standalone feature branch publishes to Hub `main`.
3. Embedded host `main` publishes only the instance subtree to Hub `main`.
4. Embedded host feature branch publishes only the instance subtree to Hub `main`.
5. Detached host `HEAD` publishes to Hub `main` and reports detached provenance.
6. Application files outside the prefix never appear in the Hub tree.
7. Dirty instance files are excluded and clearly reported.
8. Dirty host files outside the instance are neither uploaded nor reported as instance dirtiness.
9. Non-fast-forward Hub main fails without mutation or force.
10. Successful verification records per-instance metadata atomically.
11. Failed push/verification does not update last-successful-push metadata.
12. Two embedded instances retain distinct Hub linkage and cannot retarget one another.
13. Existing `hub` remote linkage migrates without creating a duplicate repository.

### Status tests

1. Parse staged, unstaged, untracked, deleted, renamed, and conflicted paths under the instance.
2. Ignore unrelated host worktree changes outside an embedded instance.
3. Distinguish host-upstream ahead/behind from Hub-publication ahead/behind.
4. Detect committed instance changes after the last successful push.
5. Ignore host commits that do not change the instance tree.
6. Handle rewritten host history without inventing an ahead count.
7. Report dirty-but-published and clean-but-unpublished as distinct states.
8. Default status performs no network access and labels remote state cached.
9. `--fetch` refreshes only relevant remotes and labels the result fresh.
10. Fetch failure preserves local worktree/contract results.
11. Focused status avoids scan-budget narration.
12. Fleet status preserves incomplete-scan warnings and duplicate detection.
13. JSON is valid, credential-free, versioned, additive, and stable across focused/fleet presentation.
14. `afs hub status` and `afs status --json` agree on publication facts.
15. Filenames with whitespace, Unicode, tabs, newlines, and rename pairs parse correctly.
16. Status does not mutate the Git index or create projection refs/commits.

## Acceptance criteria

The initiative is complete when all of the following are true:

- From a host repository with one embedded instance, `afs hub push` works without changing directories.
- Every ordinary Hub push updates `main`, never a branch derived from the host branch name.
- The push output names the instance, host source, projected commit, destination repository/branch, verification result, and any excluded dirty files.
- From the same host root, `afs status` immediately answers whether there are uncommitted knowledge changes and whether committed knowledge remains to be published.
- `afs status --fetch` can distinguish published, remote-ahead, and divergent Hub states.
- `afs hub status` provides the same publication truth as `afs status`, plus account/link information.
- Multiple embedded instances are never guessed or conflated.
- No application file outside the selected instance can appear in the Hub projection.
- No non-fast-forward condition triggers a force push.
- Existing fleet discovery, contract, doctor, duplicate, and JSON consumers have a documented compatibility path.

## Rejected alternatives

### Continue mirroring the host branch name

Rejected. The Hub workspace has a stable browsing/default branch expectation. Host feature branches are implementation provenance, not the publication contract. Mirroring them caused a successful upload to appear missing.

### Automatically select the first discovered instance

Rejected. Filesystem order is not user intent. A wrong selection can publish private or unrelated knowledge to the wrong Hub repository.

### Treat host `origin` sync as AgentsFS Hub sync

Rejected. They are independent remotes containing different trees. An embedded instance can be synced to one and stale on the other.

### Run `git subtree split` on every status

Rejected for the default path. Status should be cheap and materially read-only. Last-push metadata plus scoped tree/commit comparison answers the actionable question without manufacturing projection objects.

### Make `afs status` only a Git-style single-instance command

Rejected. Cross-instance discovery, contract maintenance, duplicate detection, and bounded fleet inspection are valuable existing capabilities. The product needs focused and fleet presentations, not one at the expense of the other.

### Automatically commit before pushing

Rejected. Commit boundaries and messages are user/agent decisions. AFS reports exactly what is excluded and pushes only committed state.

### Force-update Hub `main` when histories diverge

Rejected. AgentsFS treats Git history as a recovery and accountability mechanism. Divergence requires visible reconciliation.

## Follow-on opportunities

These are deliberately outside the implementation scope but become easier after the shared resolver and publication model exist:

- `git afs status` / `git afs push` subcommands.
- A `git-remote-afs` helper that safely transforms embedded-directory pushes.
- A pre-push guard against accidentally sending a host repository to an AgentsFS Hub remote.
- Optional submodule/standalone topology for users who prefer completely native Git semantics.
- Hub UI branch migration and cleanup assistance.
- Status/watch mode for long-running agents.

## Decision log

- **2026-08-03 — Owner approved the initiative.** Scope: root-level discovery for embedded instances, stable publication to Hub `main`, and a Git-grade status model that preserves AgentsFS fleet/contract capabilities while adding actionable worktree and publication truth.
- **2026-08-03 — Host and publication status separated.** The host repository upstream cannot represent Hub sync for a projected subtree; both planes are first-class.
- **2026-08-03 — Dirty pushes follow Git semantics.** Push committed state, explicitly report scoped uncommitted files as excluded, and never imply they were uploaded.
- **2026-08-03 — Status remains network-local by default.** Remote truth becomes fresh only with explicit `--fetch`; output always labels freshness.
- **2026-08-03 — Hub `main` is canonical.** Host branch names remain provenance only and never choose the Hub destination.
