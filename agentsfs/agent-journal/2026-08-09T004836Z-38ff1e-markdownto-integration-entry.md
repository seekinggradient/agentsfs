---
description: Session — added the Markdown To integration epic to Next (owner directive: the Hub is Markdown To's storage + distribution layer), written from the markdownto project's session.
---
## Learned / decided
- Owner directive (2026-08-08, given in the markdownto project): agentsFS Hub and Markdown To integrate in both directions — the Hub renders conforming `markdownto:`-enveloped files with Markdown To's real renderers, and markdownto.ai's playground grows a "Save to agentsFS Hub" flow; **shared Hub links are the distribution story** for boards/content people make there.
- Added ^markdownto-integration to Next with three children (render, writeback, save-API/identity contract); named the existing `/render` content-domain decision as ^render-content-domain since it's now a prerequisite consideration.
- Context for whoever picks this up: the markdownto repo (github.com/seekinggradient/markdownto) ships four specs (todo/kanban/audio/backlog), a TS engine with a browser bundle (site/app/mdto.js — the playground at markdownto.ai/app/ renders + patch-edits fully client-side), and sanitized HTML renderers incl. a backlog view. Its backlog's ^hub-integration / ^hub-contract mirror this entry. Identity leaning recorded there: the Hub account IS the Markdown To account.
- Related existing wishes this epic likely absorbs or serves: "Kanban-style Hub view of backlog pages" (Later), "Backlog styling on share-link views" (Next).
## Open
- Go-Hub × TS-renderer integration shape (client-side bundle vs sidecar) is the first technical decision of ^markdownto-render.
