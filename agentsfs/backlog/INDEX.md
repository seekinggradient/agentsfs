---
description: Prioritized backlog — pending work, discovered tasks, and parked ideas for this knowledge base.
agentsfs_role: backlog
markdownto: backlog@0.1
---

# Backlog

> Bands in priority order: **Now · Next · Later · Someday** (parked, never offered as ready) · **Done** (pruned by the gardener; git and the journal keep the trail).
> Top of a band is highest priority; ordering is priority, never sequencing — reordering lines is the safe way to reprioritize.
> Markers: `- [ ]` open · `- [/]` in progress · `- [x]` done · `- [-]` dropped. Nest children to decompose a task; a parent finishes only when its children do.
> Name a task with a trailing `^kebab-slug`; it is always the last token on the line. Reference it as `[[#^slug]]` here or with a path-qualified link such as `[[backlog/INDEX#^slug]]` from anywhere else. Put blockers before the id: `- [ ] Ship it — blocked by [[#^dependency]] ^ship-it` (lifts when every named task closes; plain-text blockers hold until edited away).
> A task that accumulates real state graduates to its own note; link the line to it. Full conventions: rule 13 of `AGENTS.md`.

## Now
- [ ] Decide whether AgentsFS should automatically assign ready backlog tickets to agents. The draft [[backlog-driven-dispatch]] RFC still needs five owner choices: which priority bands may auto-run, how many tickets one knowledge base may run at once, how a ticket proves it is implementation-ready, whether review happens in GitHub or the Hub, and whether owner-blocked questions send notifications — blocked by owner: ratify or amend the draft ^dispatch-rfc-review
- [ ] Decide whether to ship optional Claude Code and Codex plugins that remind agents to orient at session start, save context before compaction, and finish journal/commit/sync work before stopping. The current [[harness-plugins]] RFC proposes a shared `afs hook` core and thin host-specific plugins; it explicitly keeps `afs prime` agent-initiated and forbids automatic transcript capture or network writes — blocked by owner: approve, amend, or reject the RFC ^harness-plugins-decision

## Next
- [ ] Make “keep the backlog current” an explicit duty everywhere an agent learns AgentsFS: capture owner requests and newly discovered future work, revise active tasks when understanding changes, close finished or abandoned work, and check the backlog before handoff. Update the contract, `afs prime`, docs, skills, and hosted-agent instructions; add mechanical checks where possible, and coordinate stop reminders with [[#^harness-plugins-decision]] ^backlog-synchronization-discipline
- [ ] Upgrade Akshay's `aa-synced-vault` from its customized contract 0.9.0 to the current 0.12.0 without losing its Obsidian layout, personal-journal privacy rules, or custom session-journal location. The old duplicate-journal-role problem is already fixed; the remaining work is a careful manual merge of its custom contract text plus adoption of the backlog role introduced after 0.9.0 — blocked by owner: confirm that this personal vault should be migrated now ^vault-contract-port
- [ ] Make `afs contract diff` compare a customized contract with the closest byte-for-byte stock variant of its declared version. Customization detection already recognizes multiple 0.9.0 variants, but the displayed diff still always uses the canonical variant, so untouched historical wording can appear as fake user edits ^contract-diff-nearest-variant
- [ ] Decide whether arbitrary user-authored `.html` opened through Hub `/render` needs a separate web origin. Today it is served from the Hub origin with a restrictive CSP and intended opaque-origin iframe sandbox; Markdown To boards no longer depend on this decision because they use their own opaque `srcdoc` frame ^render-content-domain
- [ ] Give public share links the same backlog-specific task styling as signed-in file pages. The share renderer currently sends ordinary Markdown options, so an AgentsFS backlog spine loses its status controls, band progress, and task anchors when shared; wire backlog placement/options into `renderSharedMarkdown` ^share-backlog-styling
- [ ] Make `afs doctor` warn when a backlog repeats a priority heading such as two `## Now` sections. Markdown To already reports this as MDTO402, but AgentsFS silently merges both bands and can render a misleading board ^doctor-duplicate-band
- [ ] Add a read-only `tasks` tool to the local AgentsFS MCP server so MCP-only agents can list in-progress, ready, completed, and owner-blocked work across one or many knowledge bases. The CLI already supports this; the MCP server still exposes 12 tools and has no task-listing tool ^mcp-tasks-tool

## Later
- [ ] Let a person invoke an agent while viewing a Hub document—using a selection, keyboard shortcut, or `// instruction`—then show the proposed Markdown diff and require approval before writing it back. No document-scoped command or approval UI exists yet ^hub-inline-agent-command
- [ ] Extend automatic gardening from “scheduled doctor-driven edits” into a complete, governed fleet maintainer. Scheduling, repository isolation, scoped grants, retries, and status already exist; the missing capability is safe archive/move/delete authority so it can actually consolidate journal entries and sweep closed tickets, with previews and an audit trail ^continual-fleet-gardener
- [ ] Add `WWW-Authenticate` to the save API's CORS `Access-Control-Expose-Headers`. A 403 already includes the missing OAuth scope in both this header and the JSON body, but browser JavaScript cannot read the header because the expose list currently contains only ETag and AgentsFS revision/hash headers ^apiv1-expose-www-authenticate
- [ ] Render fenced Mermaid diagrams in Hub file and public-share views, starting with flowcharts. Keep the Markdown source available, run the diagram renderer in a sandbox, and fall back to readable source if rendering fails; the Hub currently treats Mermaid as an ordinary highlighted code block ^hub-mermaid-rendering
- [ ] Add a Hub page that aggregates active backlog work across all of a user's knowledge bases. `afs tasks <search-root>` already provides the local CLI version, but the Hub has no account-level “all my Now items” view ^cross-kb-backlog
- [ ] Audit contract 0.5.0's historical stock texts and vendor every byte-distinct pristine variant, as was done for 0.9.0. Without that, an untouched older 0.5.0 instance may be misclassified as customized and refused an automatic upgrade ^contract-050-variants-audit

## Someday
- [ ] Build a one-time importer from Beads' `issues.jsonl` issue-tracker export into an AgentsFS backlog, but only when a real user needs the migration ^beads-importer

## Done
