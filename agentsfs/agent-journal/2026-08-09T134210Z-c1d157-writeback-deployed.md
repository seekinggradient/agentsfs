---
description: Session — Phase 3 writeback deployed to hub.agentsfs.ai (efc7a34/e3ad887, clean-worktree deploy, tests green at the deploy commit); read-only surfaces verified byte-identical in production.
---
## Learned / decided
- Live boards with writeback are IN PRODUCTION: write-access /mdto/ pages render the live board (opaque-origin srcdoc + allow-scripts, SecurityError-verified isolation) and each mutation PUTs to the same route with the session cookie — apiv1 untouched, bearer-only invariant preserved. The four-gate design (session+write, strict same-origin, same-envelope, If-Match-always) is the pattern for any future page-side mutation.
- Anonymous share link re-verified post-deploy: sandbox="allow-downloads", zero allow-scripts — the read-only byte-identity claim held through a production deploy.
- The auth-bridge lesson worth keeping: when a page needs to mutate, extending the PAGE'S OWN route (method-agnostic dispatch already in web.go) beat both a cookie fallback on the API and a page-scoped token — same credential as handleEdit's form POST, no invariant loosened, zero files outside the slice's ownership.
- Re-vendor hardening: the pin test now asserts the bridge's message-shape strings and the absence of network primitives on the vendored bundle's bytes — a renamed {mdto:'source'} key upstream now fails a Go test instead of silently severing writeback. The markdownto repo's re-vendor note should mirror this (their side owns the shape).
## Open
- Owner smoke: drag a card on an owned /mdto/ page (first stateful edit on the Hub). Conflict panel try-out (concurrent playground save → drag) worth one manual pass.
- Hub workspace projection still blocked by the concurrent session's uncommitted asset WIP (afs hub push refuses correctly); origin has everything.
- Commit-subject honesty: 'Update <basename>' is all the bridge can say (bytes, not ops) — if per-op subjects ever matter, the bridge must carry op metadata; note for the contract's phase-3 retrospective.
