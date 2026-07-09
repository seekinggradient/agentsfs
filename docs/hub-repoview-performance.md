---
description: Debug note for the 2026-07 Hub repo/file page performance fix on large knowledge bases.
---

# Hub repo-view performance investigation

Date: 2026-07-07 PT / 2026-07-08 UTC

## Context

The hosted Hub was slow, and sometimes appeared not to load, when opening the
`agentic-stocks` knowledge base on `hub.agentsfs.ai`. Small repos such as
`agent-demo` loaded normally.

The slow repo was a useful reproduction case because it had 835 Markdown notes.
That size exposed work that was harmless on tiny repos but expensive on real
knowledge bases.

## Findings

The bottleneck was in `internal/hub/repoview.go`, which feeds the Hub repo page
and file page:

- `RepoSnapshot` listed the tree, then spawned one `git show` per Markdown file
  to read frontmatter descriptions.
- `RepoSnapshot` also read commit history with a full `git log --name-only` pass
  to compute freshness timestamps.
- `renderFile` reused `RepoSnapshot` for the side tree, then `RepoBacklinks`
  scanned Markdown backlinks with more per-file Git work.
- Backlink resolution called the full `core.NameIndex.Resolve` for each link,
  which scanned every file name repeatedly even though a file page only needs to
  know whether links resolve to one target path.

In practical terms, opening one large knowledge base turned into hundreds of
small Git subprocesses plus repeated in-memory scans. The UI and network were
not the primary problem; server-side repo metadata construction was.

## Fix

The fix keeps the Hub reading directly from bare Git repos, but changes the
shape of the work:

- `repoTreeEntries` now uses one `git ls-tree -r -z` call to gather paths and
  object ids.
- Markdown frontmatter is read through one `git cat-file --batch` call instead
  of one `git show` per note.
- Freshness timestamps stream `git log --name-only` and stop once every current
  file has a timestamp.
- `RepoBacklinks` batch-reads Markdown blobs and checks links against the viewed
  target path directly, using the same suffix-style semantics as `NameIndex`.
- `TestRepoBacklinksResolvesTargetPath` guards the backlink behavior for bare
  names and path-qualified wikilinks.

Files touched for the performance fix:

- `internal/hub/repoview.go`
- `internal/hub/repoview_test.go`

## Measurements

Before the fix, live Hub timing with saved local Hub credentials:

- `agent-demo` repo page: about 0.12 seconds.
- `agentic-stocks` repo page: about 5.7 seconds, 400 KB response.

After the fix, measured against a local Hub serving a bare clone of
`agentic-stocks`:

- Repo landing page: about 0.26 seconds.
- `AGENTS.md` file page: about 0.21 seconds.

An intermediate patch fixed the repo landing page but left file pages around
13.4 seconds because backlinks were still doing expensive per-link/path work.
The final backlink-specific optimization brought file pages into the same
sub-second range.

## Future guardrails

- Avoid N+1 Git subprocess patterns in Hub render paths. Prefer `git cat-file
  --batch`, `git ls-tree`, and streaming output when reading many blobs.
- Treat large knowledge bases as normal, not edge cases. A few hundred or a few
  thousand notes should not require a different code path.
- Be careful with backlink changes: the Hub should stay behaviorally aligned
  with `core.ScanLinksIn` and `core.NameIndex` matching rules.
- When timing Hub pages, compare a small repo and a large repo. A small repo can
  hide the failure mode completely.
- During this investigation another agent was editing collaborator/hosted-agent
  code in the same worktree. A later `go test ./...` failure involving
  `Agent.EnsureUser` expecting `[]RepoRef` instead of `[]string` was unrelated
  to the repo-view performance fix.
