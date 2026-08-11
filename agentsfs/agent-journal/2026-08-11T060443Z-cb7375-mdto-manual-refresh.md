---
description: Session — manually refreshed Hub's vendored MarkdownTo renderer to cb737577; vertical backlog ledger and browser writeback verified.
---

## Learned / decided

- Hub now vendors the exact `site/app/mdto.js` bytes from MarkdownTo main at `cb737577e59d981acae6736e2860af987d476a0f`; the bundle itself last changed at `629e2c57bbc90ed99be567e7a03745e584347e29` and hashes to `d24fd269d52085cd7ef2b870688198c7cb29b2b10e361d2b618c81659a2b70c8`.
- The current manual pin remains deliberate: Hub consumes MarkdownTo's renderer implementation but chooses when to re-vendor it. Automatic renderer releases remain future work rather than part of this bounded refresh.
- A disposable local Hub rendered `backlog@0.1` as the new vertical workspace-style ledger. A real browser keyboard band move changed the ledger from 2/1 tasks to 1/2 and committed the expected Markdown move through the existing Hub writeback bridge (`Via: Markdown To board (agentsfs hub)`).
- `go test ./internal/hub/ -run Mdto -count=1` and the complete `go test ./...` suite passed against the refreshed bytes.

## Open

- Design the automatic MarkdownTo-to-Hub release process separately, including compatibility gates and rollback/version-retention policy.

## Written directly

- Updated `internal/hub/assets/mdto/VERSION` with the source commit, bundle commit, SHA-256, vendoring date, and expanded public surface.
- Replaced `internal/hub/assets/mdto/mdto.js` byte-for-byte with the current MarkdownTo production bundle; no Hub rendering fork or runtime network fetch was introduced.
