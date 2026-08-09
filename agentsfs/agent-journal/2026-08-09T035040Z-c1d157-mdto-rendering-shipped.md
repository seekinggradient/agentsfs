---
description: Session — read-only Markdown To rendering shipped on share links and file pages (pinned SRI bundle, script-less sandbox, headless-Chrome verified); writeback and the live board stay behind the content-domain decision.
---
## Learned / decided
- ^markdownto-render read-only half shipped (dbca716). Shape: the Hub stays renderer-ignorant — envelope detection reuses the save API's readFileMeta, the vendored bundle is pinned by a VERSION manifest + test (a swap is a deliberate commit), SRI integrity is derived at init from the embedded bytes so attribute and bytes cannot drift.
- Sandboxing shipped is the conservative subset that needs NO content-domain decision: renderer output into srcdoc iframes with sandbox="allow-downloads" (no allow-scripts, no allow-same-origin) under default-src 'none' / connect-src 'none' CSP. renderHtml renders kanban as a static board, so read-only needs no scripts at all. The live board (MDTO.renderBoard, allow-scripts) and writeback remain blocked on ^render-content-domain — explicitly NOT decided in this slice.
- Notable pre-existing fact the agent surfaced: ordinary Hub pages carried no CSP at all before this (only /render, agent UI, previews). The mdto page now sets one; a Hub-wide CSP pass might be worth its own backlog line (not filed — owner's call on scope).
- serveAsset now sends nosniff on all /_assets/ responses (small tightening beyond the mdto path). Hub binary grows ~641 KB from the embedded bundle.
- "Open in playground" on share views is a plain link: the playground reads no hash/query yet. The deep-link half (#hub=owner/instance/path per the contract) lands with the playground Save-to-Hub work in the markdownto repo.
- The vendored bundle is @ markdownto d2bc578; the playground bundle will move again when the spec-help panel lands there. Re-vendoring is optional (the Hub uses parse/renderHtml/renderDiagnosticsHtml — a stable surface) and deliberate by design; don't chase every bundle bump.
## Open
- Deploy (with the save API, still Akshay-gated on flyctl auth) then live-verify both features end to end: PKCE walk, PUT, share link, rendered board on hub.agentsfs.ai.
- ^markdownto-writeback + live board: needs ^render-content-domain decided first.
- Provisioning Hub agents with the markdownto skill (^markdownto-agent-skill) — next slice in this repo.
