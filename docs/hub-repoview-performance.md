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

## Follow-up: 2026-07-08 — still slow after the first fix

Large KBs still felt like they froze the site. Reproduced locally with a
synthetic worst case (2,000 notes, one commit each, so freshness's early-stop
never fires): the repo page took ~0.62 s server-side on a fast laptop — several
seconds on the Fly VM — plus 1.1 MB of uncompressed HTML per view, and it all
recurred on every click (pjax shows the old page while waiting, which reads as
a hang).

Four compounding causes, four fixes:

1. **Frontmatter parsing allocated 1 MiB per call** —
   `core.FrontmatterValueFromReader` pre-allocated its scanner buffer at the
   1 MiB line cap, so snapshotting 2,000 notes churned gigabytes of
   allocations (215 ms of the 260 ms snapshot). It now starts at 4 KiB and
   grows on demand. This also speeds up the CLI.
2. **Each repo page did the snapshot work three times** — `renderRepo` called
   `RepoSnapshot`, then `repoMeta` (header) re-ran it, then `BuildRepoGraph`
   re-ran `ls-tree` + `cat-file`. The page now builds everything from one
   read: one `ls-tree`, one `cat-file --batch`, one history walk.
3. **Everything was recomputed on every request** — new `repoView` cache
   (`internal/hub/repocache.go`) keyed by HEAD's commit id: one cheap
   `rev-parse` per request; a rebuild happens only when a push/edit moves
   HEAD, and reuses the prior view so the freshness walk covers just the new
   commits (`merge-base --is-ancestor` guards force-pushes into a full walk).
   File pages reuse the same view, and backlinks now come from the cached
   wikilink graph instead of a per-page rescan. Dashboards (`repoMeta`) hit
   the same cache.
4. **1.1 MB pages went over the wire uncompressed** — `renderPage` now gzips
   (BestSpeed) when the client accepts it: 1,100 KB → 76 KB for the repo
   page, 573 KB → 23 KB for a note page.

Measured on the synthetic 2,000-note repo (same laptop, byte-identical HTML):

- Repo page: 0.62 s → 0.13 s cold (first view after a push) / 0.05 s warm.
- Note page: 0.35 s → 0.06 s.
- Cold view build itself: 0.56 s → 0.07 s.

Files touched: `internal/core/frontmatter.go`, `internal/hub/repoview.go`,
`internal/hub/repocache.go` (new), `internal/hub/web.go`,
`internal/hub/server.go`. Guarded by `TestRepoViewCache` (cache hit, push
pickup via the incremental walk, force-push fallback) and
`TestGraphBacklinksResolvesTargetPath`; `TestPerfBreakdown`
(`AFS_PERF_REPO=<bare dir>`) times the pieces against any real repo.

Deploy note: the Hub ships by `fly deploy` only — none of this (nor the
2026-07-07 fix, which landed in the same day's `bdeaec3`) reaches
hub.agentsfs.ai until someone deploys.

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
