---
description: Design RFCs for major AgentsFS initiatives — one file per RFC, each carrying its motivation, design, decision log, and research appendix.
---

# RFCs

One file per RFC. An RFC here is the source of truth for its initiative's design decisions; implementation state lives in the session journal and the code itself.

- [[hub-mcp-server]] — remote MCP server on the Hub for consumer apps (ChatGPT, Claude, …).
- [[harness-plugins]] — optional Claude Code and Codex plugins for lifecycle orientation and memory-capture compliance.
- [[embedded-git-status-and-hub-sync]] — root-level discovery, stable Hub `main` publication, and Git-grade scoped status for AgentsFS embedded in ordinary repositories.
- [[backlog-and-tasks]] — structured task tracking: the backlog page role, markdown-native task grammar, `afs tasks`/`afs prime`, doctor findings, Hub rendering; contract 0.10.0.
- [[backlog-directories]] — backlog v2: the backlog as a directory role (spine + earned ticket files + archive collection + delegated sub-backlogs), owner-blocked channel, cross-instance triage, claim/done tooling; contract 0.11.0.
- [[backlog-driven-dispatch]] — **draft**: push-triggered per-item agent spawns over the backlog, claiming by git CAS, the band dispatch gate, implement→review pipeline, tiered merge trust; implementation deferred until the manual loop is proven.
