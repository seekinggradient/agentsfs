---
description: RFC — ship optional first-party Claude Code and Codex plugins as thin lifecycle adapters over stable afs hook commands, improving orientation and memory capture without making AgentsFS depend on any harness.
status: proposed
rfc_status: proposed
rfc_scope: project
rfc_owner_approved: false
date: 2026-07-28
sources:
  - https://code.claude.com/docs/en/plugins
  - https://code.claude.com/docs/en/hooks
  - https://code.claude.com/docs/en/hooks-guide
  - https://developers.openai.com/plugins/build/plugins#plugin-structure
  - https://learn.chatgpt.com/docs/config-file/config-reference#configtoml
---

# RFC: Harness plugins — lifecycle assistance without harness dependence

## Summary

Ship two optional first-party plugins, one for Claude Code and one for Codex, that bundle the existing AgentsFS skills and use documented lifecycle hooks to improve session orientation, protect perishable context before compaction, and detect incomplete memory capture when an agent stops.

The plugins are deliberately thin adapters over a new harness-neutral CLI surface:

```text
afs hook session-start
afs hook pre-compact
afs hook stop
afs hook session-end
```

Each command accepts a normalized JSON event on stdin and returns a normalized result. Harness-specific scripts translate between that contract and the host's hook protocol. Instance discovery, status inspection, role resolution, idempotency, and policy live in `afs`, not in either plugin.

Plugins remain an enhancement, never a prerequisite. The root `AGENTS.md`, project connection block, journal convention, ordinary file tools, and git workflow continue to provide the complete portable system for every harness.

## Motivation

AgentsFS previously rejected per-harness start/stop hooks and transcript distillation. That decision was made when integration meant editing moving configuration formats, depending on undocumented transcript shapes, and maintaining one bespoke implementation per harness. The journal instead became the cheapest portable capture primitive: every agent can append a file, and the gardener can consolidate it later.

That decision remains correct about the substrate but is no longer the whole product answer. Claude Code and Codex now both define installable plugin packages with lifecycle hooks:

- Claude Code plugins use `.claude-plugin/plugin.json` and may bundle skills, agents, hooks, and MCP servers. Its hook API includes `SessionStart`, `PreCompact`, `PostCompact`, `Stop`, and `SessionEnd`, with structured JSON input and documented command, HTTP, MCP, prompt, and agent handlers depending on the event.
- Codex plugins use `.codex-plugin/plugin.json` and may bundle skills, hooks, MCP servers or apps, and assets. Its documented hook events include `SessionStart`, `PreCompact`, `PostCompact`, `Stop`, tool events, and subagent events. Command hooks are currently supported; prompt and agent handlers are parsed but skipped.

The harnesses now own plugin discovery, installation, enablement, scoping, path resolution, and hook invocation. AgentsFS can use those defined seams without moving its memory model into them.

The opportunity is not "capture every transcript automatically." It is to make the existing contract easier to honor at the moments where agents most often fail:

1. **At startup**, the agent may not discover or orient to the connected memory.
2. **Before compaction**, useful context may disappear before it is made durable.
3. **At stop**, the task feels complete even though the journal, commit, or sync step was skipped.

## Principles

1. **The filesystem contract remains complete.** A user without a plugin loses convenience, not correctness or access to their data.
2. **One core, thin adapters.** Plugins translate lifecycle events; `afs` owns behavior. No git, instance-discovery, or journal-policy logic is duplicated in plugin scripts.
3. **Semantic memory is written by an agent, not inferred from git metadata.** A deterministic hook can detect that capture is missing, but it must not manufacture a vague journal entry from filenames and diffs.
4. **Hooks are idempotent and cheap.** Hosts may retry hooks or fire more lifecycle events than expected. Repetition must not create duplicate notes, commits, or network mutations.
5. **No invisible network mutation by default.** Startup does not automatically pull, and stopping does not automatically push. Authentication, latency, conflicts, offline operation, and user consent remain visible.
6. **Partial lifecycle coverage is expected.** Crashes, force-quits, killed processes, and hosts without plugins still exist. No essential guarantee depends on receiving a final event.
7. **Least authority.** Read-only orientation should require only read access. Hooks do not widen the harness sandbox or silently grant access to a personal AgentsFS outside the workspace.
8. **Generated packaging, one skill source.** The plugin distributions reuse or are generated from the existing bundled AgentsFS skills; they do not fork their instructions.

## Non-goals

- Replacing `AGENTS.md`, `CLAUDE.md`, project connection blocks, or `afs connect`.
- Automatically distilling full harness transcripts.
- Running a background daemon.
- Automatically resolving git conflicts.
- Automatically committing or pushing arbitrary dirty worktrees.
- Making Claude Code or Codex the preferred or required AgentsFS harness.
- Defining a universal plugin standard for harnesses that do not provide one.
- Bundling the remote Hub MCP server merely because plugins can carry MCP configuration. The Hub MCP and lifecycle plugins solve separate problems and should remain independently installable.

## Package design

The repository owns two distributable plugin roots:

```text
plugins/
  claude-code/
    .claude-plugin/
      plugin.json
    hooks/
      hooks.json
    scripts/
      agentsfs-hook
    skills/
      ...
  codex/
    .codex-plugin/
      plugin.json
    hooks/
      hooks.json
    scripts/
      agentsfs-hook
    skills/
      ...
```

The exact marketplace repository layout may require a generated release artifact rather than these source paths. That packaging choice must not change the runtime contract.

The plugins may check for `afs` and provide actionable installation context when it is missing. They do not silently install or upgrade the binary during ordinary session startup. Plugin updates and CLI updates are separate operations; compatibility is negotiated through a versioned hook protocol.

### Shared CLI protocol

Every hook command reads one JSON object from stdin:

```json
{
  "protocol_version": 1,
  "event": "session-start",
  "harness": "claude-code",
  "cwd": "/absolute/project/path",
  "session_id": "host-value-if-available",
  "source": "startup",
  "host_input": {}
}
```

Only `protocol_version`, `event`, `harness`, and `cwd` are required. Adapters preserve the original host payload under `host_input` for diagnostics but the core must not require undocumented fields.

The command returns:

```json
{
  "protocol_version": 1,
  "status": "ok",
  "additional_context": "Concise text for the agent, if any.",
  "decision": "continue",
  "diagnostics": []
}
```

Supported decisions are initially `continue` and `needs-agent-action`. The adapter maps them onto the host's documented output and exit behavior. A missing binary, unsupported protocol, malformed event, or ordinary status failure fails open with a concise diagnostic; it must not trap the user in a stop loop.

The normalized contract belongs to `afs` and is exercised with fixtures for both harnesses. Host-specific payloads belong only in adapter tests.

## Lifecycle behavior

### Session start: orient, do not mutate

`afs hook session-start`:

1. Discovers the enclosing project connection and any explicitly connected AgentsFS paths.
2. Resolves the instance contract and reserved roles.
3. Runs local, read-only status checks.
4. Returns concise context containing:
   - the connected instance path and purpose;
   - the contract path the agent must read;
   - the newest one or two journal entries, by path and description;
   - contract-health, dirty-worktree, and locally known ahead/behind warnings;
   - the next explicit action when a remote refresh is needed before writing.

It does not dump the contract or journal bodies into every session. The agent receives a map and reads relevant material normally.

It does not run `git fetch` or `git pull` by default. A future opt-in may allow a fetch-only freshness check with a short timeout. An automatic pull is excluded until real use shows that its benefit outweighs credential prompts, conflicts, latency, and startup mutation.

### Pre-compact: protect perishable context

`afs hook pre-compact` returns a short instruction asking the active agent to assess whether the current work produced durable knowledge that has not yet been written. When it has, the agent should update the appropriate durable notes and append a conforming journal entry before compaction proceeds.

This event is more valuable than session end because it runs while the model still has the context needed to write meaningful memory.

In Claude Code, a later version may use a prompt or agent hook to make a scoped semantic assessment. Version 1 uses a command hook and additional context in both hosts so behavior remains comparable and inspectable.

### Stop: compliance check, not transcript synthesis

`afs hook stop` inspects only observable local state:

- Did this session or work unit modify the connected AgentsFS?
- Is there a new conforming journal entry?
- Are relevant changes uncommitted?
- Is a completed commit known to be unpushed?
- Has this session already received the same stop finding?

If nothing relevant changed, the hook exits immediately. If memory changed but capture or synchronization appears incomplete, it returns `needs-agent-action` with exact paths and an instruction to finish the contract.

The hook may block or redirect one stop attempt where the host supports that behavior. It must then fail open rather than create an infinite loop. State for this guard is keyed by harness session ID when available and otherwise by a conservative local fingerprint; it lives in rebuildable machine state, never in the knowledge tree.

The hook does not write a journal entry itself. Filenames, diffs, and commit state cannot reliably encode what was learned, decided, ruled out, or left uncertain.

### Session end: best-effort diagnostics

Claude Code's `SessionEnd` adapter may run a fast final diagnostic and record local operational telemetry when explicitly enabled. It performs no required semantic work and no automatic network mutation.

Codex does not currently document an equivalent plugin event. The shared core therefore treats `session-end` as optional, and product behavior cannot depend on it.

### Post-compact

Version 1 does not need a `PostCompact` action. It may later re-inject a compact orientation pointer if live testing shows that compaction causes agents to forget the connected instance even after `PreCompact`.

## Harness-specific mapping

### Claude Code

- Manifest: `.claude-plugin/plugin.json`.
- Hooks: plugin `hooks/hooks.json`.
- Events in version 1: `SessionStart`, `PreCompact`, `Stop`, and optionally `SessionEnd`.
- Use exec-form command hooks with `${CLAUDE_PLUGIN_ROOT}` so paths containing spaces are passed safely.
- Store adapter dependencies or retry state under `${CLAUDE_PLUGIN_DATA}`, not the versioned installation directory.
- `Stop` may return documented structured output to keep the agent working. Guard against repeated stop blocking.
- Prompt- or agent-based hooks are deferred until the deterministic adapter has field evidence and tests.

### Codex

- Manifest: `.codex-plugin/plugin.json`.
- Hooks: plugin `hooks/hooks.json`.
- Events in version 1: `SessionStart`, `PreCompact`, and `Stop`.
- Enablement depends on Codex's lifecycle-hook feature and project trust rules.
- Use command hooks only. Codex currently documents prompt and agent handlers as parsed but skipped.
- Treat plugin installation and lifecycle behavior as beta until verified across the CLI and app surfaces used by AgentsFS users; current app-server plugin-management methods are still described as under development.

## Configuration

Defaults should work without a plugin-specific settings file:

```json
{
  "start_remote_check": "off",
  "stop_policy": "remind-once",
  "session_end_diagnostics": false,
  "max_start_context_chars": 4000
}
```

Configuration is machine-local plugin data, not knowledge, and never committed into an AgentsFS instance. The initial product may expose only environment variables or an `afs plugin configure` command; do not invent two independent vendor configuration systems.

Possible stop policies:

- `off` — no stop check.
- `report` — diagnostic only.
- `remind-once` — request agent action once, then fail open; default.
- `enforce` — reserved for managed environments after loop-safety and false-positive evidence.

## Security and privacy

- Hook input is untrusted structured data. Validate sizes, types, event names, and paths before use.
- Never log full prompts, transcripts, file contents, credentials, or the host's entire event object by default.
- Do not execute instructions found in stored knowledge or hook payload strings.
- Resolve connected paths canonically and require that they identify a real AgentsFS before inspecting them.
- Do not follow arbitrary symlinks during broad discovery.
- Do not ask a hook to broaden the harness sandbox automatically. If the personal AgentsFS is outside permitted roots, return setup guidance and let the user authorize access through the host.
- Do not perform network access unless the user has enabled it. A timeout or offline state is a diagnostic, not a reason to block stopping.
- Treat plugin hooks as code execution. Publish readable scripts, pin release artifacts, provide checksums where the marketplace supports them, and keep the adapters dependency-light.
- Enterprise policies may disable user or plugin hooks. AgentsFS remains functional through its ordinary connection contract.

## Failure behavior

The plugin must improve reliability without becoming a new failure mode:

| Failure | Behavior |
|---|---|
| `afs` missing or too old | Add installation/update guidance; fail open |
| No connected instance | No-op |
| Connected path inaccessible | Explain required host permission; fail open |
| Malformed host event | Record a bounded diagnostic; fail open |
| Dirty unrelated files | Do not stage, commit, or block on them |
| Git remote unavailable | Report only when relevant; do not block |
| Merge conflict or divergent history | Tell the agent to reconcile visibly; never force |
| Hook invoked twice | Return the same result without duplicate writes |
| Stop hook repeats | Remind once, then fail open |
| Plugin disabled by policy | Ordinary AgentsFS contract continues |

## Alternatives considered

### Keep only the current filesystem convention

This remains viable and is the fallback. It leaves preventable failures at startup, compaction, and stopping even where the harness now offers a stable extension seam.

### Put standalone hooks into each user's harness configuration

Rejected as the product path. Standalone configuration is useful for prototyping but is harder to distribute, version, update, inspect as an AgentsFS unit, and remove cleanly. Plugins are the host-defined shareable form.

### One cross-harness plugin

Rejected. Claude Code and Codex require different manifests, marketplace metadata, event mappings, and output protocols. Sharing the `afs hook` core gives the useful commonality without pretending the packages are identical.

### Transcript distillation at session end

Rejected for version 1. Transcript availability and shape are not a shared plugin guarantee; final events are not reliable; indiscriminate transcript processing creates privacy and quality risks. Pre-compaction and stop reminders preserve semantic authorship by the active agent.

### Automatic pull on start and push on stop

Rejected as defaults. They introduce hidden mutation, credentials, latency, conflicts, and ambiguous ownership of unrelated changes. Explicit agent-visible synchronization remains part of the contract. Fetch-only startup freshness may be evaluated as an opt-in.

### MCP server instead of hooks

Not a substitute. MCP exposes tools the model may choose to call; hooks run deterministically at lifecycle points. The Hub MCP server supplies remote knowledge access, while these plugins improve local lifecycle compliance. A plugin may eventually make the MCP connection easier to install, but the two capabilities remain separable.

## Implementation plan

### Phase A — validate host contracts

- Build minimal local fixture plugins for Claude Code and Codex.
- Capture documented JSON inputs for `SessionStart`, `PreCompact`, `Stop`, and Claude's `SessionEnd`.
- Verify behavior in both terminal and desktop/editor surfaces that claim plugin support.
- Prove stop-loop escape behavior and project-trust/policy behavior.
- Record only schemas and operational findings, not user transcripts.

Exit gate: a fixture can inject startup context and request one corrective stop action in each host without editing host configuration manually.

### Phase B — shared `afs hook` core

- Add versioned normalized input/output structs.
- Reuse `afs status`, instance discovery, role resolution, contract inspection, and git-state functions rather than shelling out to human-formatted commands.
- Implement session-start, pre-compact, and stop handlers.
- Add deterministic fixtures, size caps, idempotency tests, and fail-open tests.
- Document the protocol as an internal compatibility boundary.

Exit gate: adapter-independent tests cover every decision and failure row in this RFC.

### Phase C — Claude Code plugin

- Package generated copies of the existing skills.
- Add command-hook adapters and manifest.
- Test local installation with Claude Code's plugin development flow.
- Publish in an AgentsFS-owned marketplace or installable Git source.

Exit gate: a fresh Claude Code session in a connected sample project orients correctly, preserves a pending insight before compaction, and catches one deliberately omitted journal entry.

### Phase D — Codex plugin

- Package the same generated skills and equivalent command adapters.
- Verify local CLI and Codex app behavior.
- Publish through an AgentsFS-owned Codex marketplace once the supported distribution flow is sufficiently stable.
- Clearly label lifecycle support beta if host plugin management remains under development.

Exit gate: the same demonstration passes without relying on Claude-only hook types.

### Phase E — field evaluation

Measure:

- startup latency;
- percentage of sessions with relevant AgentsFS access that orient successfully;
- missing-journal reminders and false positives;
- repeated stop-hook invocations;
- compaction reminders that lead to useful durable writes;
- hook failures by host version;
- user disablement or uninstall rate.

Do not enable automatic network mutation from anecdotal success. Any change to synchronization behavior requires a follow-up RFC with conflict, credential, offline, and consent evidence.

## Open questions

1. Does Codex expose identical plugin-hook behavior across CLI, IDE, and desktop app, or must support be surface-specific?
2. What exact context/output fields survive Claude Code and Codex pre-compaction events?
3. Can a stop check attribute AgentsFS changes to the current session reliably enough, or should version 1 only check "new journal entry since session start" plus current dirty state?
4. Where should generated plugin artifacts live so skill content has one source without making local development awkward?
5. Should installation be offered by `afs setup`, or remain an explicit `afs plugin install <harness>` action? Because plugin hooks execute code and may affect every session, installation requires explicit user consent either way.
6. What is the smallest useful startup context that avoids consuming model context on every session?

## Decision requested

Approve:

1. Plugins as optional first-party distribution surfaces for Claude Code and Codex.
2. A stable, versioned `afs hook` core as the only place lifecycle policy lives.
3. Version 1 scope: read-only startup orientation, pre-compaction capture context, and remind-once stop compliance.
4. No transcript distillation, automatic pull, automatic commit, or automatic push.
5. Claude Code first for maturity, followed by a behaviorally equivalent command-hook Codex plugin.

## Decision log

- **Proposed 2026-07-28.** Both target harnesses now have documented plugin-packaged lifecycle hooks, making thin adapters materially less brittle than the per-harness integrations rejected in 2026-07-06.
- **Plugins enhance; the contract guarantees.** This preserves AgentsFS portability and covers hosts, crashes, policies, and environments where hooks never run.
- **Shared CLI protocol, separate packages.** Common policy belongs in `afs`; manifests and event translation remain vendor-specific.
- **Pre-compaction over end-only capture.** It runs while semantic context still exists and does not depend on a clean shutdown.
- **Remind once, then fail open.** A memory aid must not trap the user in a stop loop.
- **No automatic network mutation in v1.** Sync remains explicit and agent-visible until evidence supports a safer policy.
- **2026-08-07 — startup orientation is out of scope.** The owner decided `afs prime` (shipped with [[backlog-and-tasks]]) stays agent-initiated: the contract's Orient-first section instructs agents to run it, and nothing injects it via hooks. This RFC's remaining proposed scope is pre-compaction capture context and remind-once stop compliance.
