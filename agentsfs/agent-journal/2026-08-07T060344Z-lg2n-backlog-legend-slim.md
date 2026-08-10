---
description: "Session — slimmed the backlog page from manual+example to a five-line legend (owner call: page was crowding its own tasks), synced the vendored stock, and documented the backlog across concepts/capabilities/agent-start docs."
---

# Backlog page slimmed to a legend; docs sweep

## Learned / decided

- Owner review: the stock backlog page's 20-line conventions block plus fenced example crowded the working content. Decision (against moving rules into AGENTS.md or making backlog a directory): the page carries a **five-line legend blockquote**; rule 13 stays the contract summary; the full worked example lives only in [[backlog-and-tasks]]. Rationale recorded there: a `backlog/` directory is structural overhead for one page and breaks `[[backlog]]` link resolution; full grammar in AGENTS.md would overweight rule 13 and couple grammar tweaks to contract-version churn.
- Amended the 0.10.0 stock backlog text pre-release (no tagged CLI release carries it yet), so no new contract version and no variant vendoring needed for AGENTS.md — its text is untouched; `contracts/backlog-0.10.0.md` re-synced byte-identical.
- Docs now teach the backlog: `docs/concepts.md` (fourth role, page-level, new "The backlog" block), `docs/capabilities.md` (tasks/prime matrix rows, roles row), `docs/agent-start.md` (prime in orientation, backlog in the write-and-commit flow).

## Open

- Tracked in [[backlog/INDEX]] — no changes to open items this session.

## Written directly

- `template/backlog.md` + vendored stock, this instance's [[backlog/INDEX]] preamble, and the three docs above.
