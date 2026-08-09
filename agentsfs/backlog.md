---
description: Prioritized backlog — pending work, discovered tasks, and parked ideas for this knowledge base.
agentsfs_role: backlog
---

# Backlog

> Bands in priority order: **Now · Next · Later · Someday** (parked, never offered as ready) · **Done** (pruned by the gardener; git and the journal keep the trail).
> Top of a band is highest priority; ordering is priority, never sequencing — reordering lines is the safe way to reprioritize.
> Markers: `- [ ]` open · `- [/]` in progress · `- [x]` done · `- [-]` dropped. Nest children to decompose a task; a parent finishes only when its children do.
> Name a task with a trailing `^kebab-slug` and reference it as `[[#^slug]]` here or `[[backlog#^slug]]` from anywhere. Block with `— blocked by [[#^slug]]` (lifts when every named task closes; plain-text blockers hold until edited away).
> A task that accumulates real state graduates to its own note; link the line to it. Full conventions: rule 13 of `AGENTS.md`.

## Now
- [ ] Decide the [[harness-plugins]] RFC (owner call) — remaining scope after 2026-08-07: pre-compact capture context and stop-time compliance reminders; prime is out (stays agent-initiated) ^harness-plugins-decision
- [-] Wire `afs prime` into Claude Code and Codex session-start hooks — dropped 2026-08-07: owner decided prime is pull-based; the contract instructs agents to run it
- [x] Deploy the Hub `/mcp` OAuth build to hub.agentsfs.ai — deployed 2026-08-07, verified: OAuth discovery 200, unauthenticated `/mcp` 401, login page live

## Next
- [ ] Markdown To integration (owner directive 2026-08-08; mirror of ^hub-integration in the markdownto repo's backlog): **the Hub is Markdown To's storage and distribution layer** — shared Hub links are how people distribute the boards and content they make at markdownto.ai ^markdownto-integration
      - [ ] Render conforming files with Markdown To's real renderers: a file whose frontmatter carries `markdownto: <name>@<version>` renders on Hub pages and share links via the markdownto engine instead of plain Markdown — read-only first. Open question: the Hub is Go and the renderers are TS; the playground already ships a self-contained browser bundle (site/app/mdto.js in the markdownto repo), so client-side rendering of served file bytes is the likely shape. Relates to [[#^render-content-domain]] and the share-link backlog styling item ^markdownto-render
      - [ ] Stateful second step: board edits on a Hub page write back to the instance through Markdown To's patch engine (applyOps, source-hash conflict surface, commit to the instance) ^markdownto-writeback
      - [ ] Account + save API for the markdownto.ai playground's "Save to agentsFS Hub" flow. **The contract is now drafted** (markdownto repo: agentsfs/product/hub-contract.md — github.com/seekinggradient/markdownto/blob/main/agentsfs/product/hub-contract.md): reuse the Hub's existing /mcp OAuth server with markdownto.ai as a PKCE client (one signup flow ever), REST save API with If-Match source-hash conflicts (maps 1:1 onto the patch engine's expect), auto-created `apps` instance with a collection-role saves dir, share links render with a pinned client-side mdto.js bundle. Hub side builds first; awaiting owner review ^markdownto-save-api
- [ ] Port aa-synced-vault to contract 0.10.0 — its adaptations genuinely rewrite Orient step 3, rules 9/10, and the whole Structure section (Journal/-protection guardrails), which conflict with 0.10.0's Structure rewrite; needs owner judgment. Also fix its duplicate journal role (two dirs marked `agentsfs_role: journal`; AGENTS.md names one, `afs roles` resolves the other) ^vault-contract-port
- [ ] `afs contract diff` should baseline against the *nearest* vendored variant, not the canonical stock — pre-4ac5230 0.9.0 instances show two phantom "modified" lines inside otherwise-real adaptation diffs (seen on seekinggradient-hq, boswell-v2, ai-engineer-2026 during the 0.10.0 rollout)
- [ ] Decide the content domain for `/render` sandboxed HTML (open since the share-links ship; now also a prerequisite consideration for [[#^markdownto-render]]) ^render-content-domain
- [ ] Backlog styling on share-link views — one-line wiring in sharelink.go, flagged during the backlog build
- [ ] Add a read-only `tasks` tool to the local MCP server — touches the "12 tools" count in README, concepts, capabilities, and mcp docs ^mcp-tasks-tool

## Later
- [ ] Hub cross-KB aggregated backlog view — "my Now items across every knowledge base" ^cross-kb-backlog
- [ ] Kanban-style Hub view of backlog pages
- [ ] Contract-diff variants audit: AGENTS-0.5.0.md has the same latent pristine-text split found and fixed for 0.9.0

## Someday
- [ ] Beads issues.jsonl importer (one-shot converter, only if a user asks)

## Done
- [x] Backlog and tasks feature — grammar, `afs tasks`/`afs prime`/`afs tree --budget`, doctor findings, Hub rendering, contract 0.10.0 → [[backlog-and-tasks]]
