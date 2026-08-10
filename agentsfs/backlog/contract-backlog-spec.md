---
description: "Ticket — contract-0.12 adoption of backlog-workspace@0.1: the existing directory backlog design plus envelope, final-token, and path-qualified-reference amendments."
---

# Contract adoption of backlog-workspace@0.1

## Verdict (review of template/AGENTS.md + template/backlog/INDEX.md @ 0.11.0, 2026-08-10)

**The contract does not need to switch to anything — 0.11.0's directory backlog and
`backlog-workspace@0.1` are the same design.** The workspace spec was formalized FROM this
contract's RFC, and the review confirms the alignment held through both implementations:
spine-as-INDEX ↔ spine-by-envelope (coexist in one frontmatter block, proven), ticket files ↔
detail notes, `archive/` + per-year rollups ↔ `archive/` + rollup pages, sub-backlog delegation ↔
delegation with ready-work extension, `— blocked by owner:` ↔ a conforming prose blocker element.
The template prescribes no six-space nesting (that was one instance's habit, not contract text).

What adoption needs is a **three-line amendment**, not a redesign:

1. **The SHOULD-adopt sentence** (rule 13 or the spine template's conventions block):
   > The spine SHOULD declare `markdownto: backlog@0.1` in its frontmatter, beside
   > `agentsfs_role: backlog`. A backlog directory whose spine declares it is a conforming
   > `backlog-workspace@0.1`: `mdto` validates it mechanically, `graduate`/`archive`/`sweep`
   > perform this contract's ticket and archive motions transactionally, and the Hub renders
   > the spine as a live board. SHOULD, not MUST — an instance without the tooling loses
   > nothing but the verbs.
2. **The final-token rule** (the one real grammar gap — the contract is silent and live files
   diverge): the trailing `^slug` is the LAST token of its line; a blocker clause comes before
   it (`- [ ] Ship it — blocked by [[#^dep]] ^ship-it`). This matches conventions §3.1, which
   the spec cannot bend.
3. **Path-qualified member references** (promote the already-demonstrated style to a stated
   rule): write `[[backlog/voice/INDEX#^slug]]`, not `[[INDEX#^slug]]` — every directory has
   an INDEX.md, so bare references are ambiguous (the spec reports them as MDTO515).

## Migration pre-steps for existing instances (unchanged from prior scoping, now sharper)

- Add the envelope line to the spine; swap any `^slug — blocked by …` lines to clause-first;
  `mdto validate` the spine.
- **Renormalize six-space child indentation to two/four spaces where present** (the markdownto
  instance uses six; core parses six-space children as paragraph continuations — found during
  the workspace implementation, filed there as a possible core nesting-tolerance question).

## Sequencing

Akshay authorized the contract release on 2026-08-10 as part of the projection-sync rollout.
Contract 0.12.0 carries the three-line amendment, vendors the 0.11.0 stock contract/spine,
refreshes only a byte-pristine old spine during upgrade, and leaves task-bearing custom spines
for explicit migration. This live spine now carries the envelope and normalized task grammar.
