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

## Open items carried forward

- Live-API embedding run (needs a key) and picking the blessed default provider.
- A real second harness (Codex exists on this machine) exercising an instance end-to-end.
- MCP server registered with a real harness config (only raw-protocol tested).
- Distribution: how the website hands all this to a stranger — the next discussion.
