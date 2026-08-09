---
description: Session — linked the drafted Hub contract (markdownto repo) into the markdownto-save-api task; Hub-side OAuth client + save API build first.
---
## Learned / decided
- The integration contract is drafted in the markdownto repo (agentsfs/product/hub-contract.md): one account (this Hub's existing /mcp OAuth server gains markdownto.ai as a first-party PKCE client), instances as the storage layer (auto-created `apps` instance, collection-role saves dir), REST save API with If-Match source-hash conflicts, share links rendering conforming files via a pinned client-side markdownto bundle, three phases with the Hub side building first.
- Updated [[backlog#^markdownto-save-api]] to point at it; awaiting owner review before build.
