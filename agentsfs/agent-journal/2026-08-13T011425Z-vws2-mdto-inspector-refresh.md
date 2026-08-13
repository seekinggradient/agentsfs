---
description: Session — refreshed Hub's pinned MarkdownTo renderer so empty backlog-workspace inspectors collapse and the ledger expands.
---

## Learned / decided

- Hub now vendors the exact `site/app/mdto.js` bytes from MarkdownTo main at `66455525bc391ae9cf1945908b0ff393e348844e`; the artifact hashes to `ffd25dec20450fa159a515b2104a437001b90b168b685fe82016cac8f35dc84e`.
- The updated workspace renderer omits the inspector entirely when no task, editor, or archive detail is selected. The live MarkdownTo production check confirmed the app drops `ws-app--inspector`, renders no empty prompt, and expands the ledger after **Close details**.
- The Hub remains renderer-ignorant: this is a byte-for-byte re-vendor plus manifest update, with no Hub-side rendering fork or runtime fetch.
- Focused MarkdownTo integration tests and the complete AgentsFS Go suite passed against the refreshed bytes.

## Open

- Design the automatic MarkdownTo-to-Hub renderer release process separately, including compatibility gates and rollback/version-retention policy.

## Written directly

- Replaced `internal/hub/assets/mdto/mdto.js` with the production MarkdownTo bundle and updated `internal/hub/assets/mdto/VERSION` with its source commit, bundle commit, SHA-256, and vendoring date.
