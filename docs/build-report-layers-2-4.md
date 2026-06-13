# Build report — Layers 2–4 (2026-06-12)

Built in one authorized pass after Layer 1. Each layer validated live; findings below.

## Layer 2 — tree, doctor, backlinks, rename, gardening prompt

- All four commands are deterministic file+git reads/rewrites, no persistent state. Unit tests cover link resolution (case-insensitivity, path-suffix disambiguation, non-markdown targets), doctor's rules and exemptions, and rename's link rewriting (incl. `[[target|alias]]`).
- **Gardener demo:** seeded a messy instance (9 doctor findings, 4 errors: fragmented stubs, a typo'd dead link `[[Markus Lee]]`, an undescribed PDF, missing frontmatter). A fresh-context agent given only the gardening prompt + binary took it to `doctor: healthy` in two reviewable commits — and preserved the one new fact buried in a stub (bank statement sent 2026-06-14) with citation, fixed the typo by inference, and explicitly flagged what it *couldn't* know ("photos included?") instead of assuming. The doctor → gardener loop works.
- Design call surfaced by testing: the link scanner skips fenced code blocks and inline code spans — backticked `[[links]]` are quotation, not reference. Without this, the contract's own examples are permanent false positives.

## Layer 3 — search

- SQLite (pure-Go driver) sidecar at `.agentsfs/index.db`, gitignored. FTS5 full-text with section-level chunks (split at `##`), ranked, snippets. Auto-reindexes when the file fingerprint changes; explicit `reindex` reproduces the index from zero (tested).
- Agent-typed queries are operator-safe: tokens are quoted, implicit AND. `"cat AND (deductible)"` can't error.
- Embeddings: pluggable provider by env (`VOYAGE_API_KEY` / `OPENAI_API_KEY`, `AFS_EMBED_*` overrides), explicit-only reindex (API calls cost money), staleness warning at query time, graceful no-key degradation. Semantic path verified against a fake provider in tests (no real key on this machine — untested against live APIs).

## Layer 4 — MCP + packaging

- `afs mcp` serves tree/search/doctor/backlinks/rename over stdio via the official Go SDK (v1.6.1); every tool is a thin adapter over the same `internal/core` the CLI uses. Smoke-tested with raw JSON-RPC: initialize → tools/list → tools/call all correct against the fixture.
- `scripts/release.sh` cross-compiles static binaries (darwin/linux × arm64/amd64, ~11 MB, CGO off). Distribution channels deliberately undecided — that's the queued landing-page discussion.
- Project README added for the future repo/landing page.

## Code-review fix pass (2026-06-12)

An independent review surfaced 7 findings; all addressed same day, each HIGH/MEDIUM with a regression test:

1. **HIGH, fixed** — `init` inside a host repo staged the whole tree (`git add -A` is repo-wide since git 2.0); could commit the user's unrelated work. Now pathspec-scoped (`add -A -- .`), and the contract's rule 9 teaches agents the same scoped form. Bonus: LFS hooks are no longer installed into host repos we didn't create.
2. **HIGH, fixed** — `FindRoot`'s fallback accepted *any* `AGENTS.md` as an instance root, misdetecting ordinary repos (and `search` would create `.agentsfs/` inside them). The fallback now requires the contract declaration ("This folder is an agentsfs"); `.agentsfs/` remains the definitive marker.
3. **MEDIUM, fixed** — semantic search now errors when the configured provider/model differs from what the index was built with, instead of silently returning meaningless rankings.
4. **MEDIUM, fixed** — `--yes` no longer auto-writes global harness configs; those need explicit `afs connect --global`. Project-level connection stays under `--yes`.
5. **LOW, fixed** — `rename` now rewrites with the scanner's quotation semantics (line-scoped, inline-code-masked), so backticked/fenced `[[links]]` survive; rewrite count is now actual replacements.
6. **LOW, fixed** — rename's old-path argument accepts cwd-relative paths when unambiguous; root-relative convention documented in usage.
7. **LOW, deferred** — walker ignores `.gitignore`; TODO in `ListEntries`, parked for v2.

Both review repros re-run post-fix: dirty-host-repo init commits only instance files; generic-AGENTS.md repos are rejected with no `.agentsfs/` created.

## Open items carried forward

- Live-API embedding run (needs a key) and picking the blessed default provider.
- A real second harness (Codex exists on this machine) exercising an instance end-to-end.
- MCP server connected through a real harness config (only raw-protocol tested).
- Distribution: how the website hands all this to a stranger — the next discussion.
