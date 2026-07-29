# Eve migration record: `agentsfs-chat` to `agentsfs-eve`

> **Archived decision record.** This records why Eve was adopted and what changed. It is
> not the current deployment runbook — see [../internals/eve-hosting.md](../internals/eve-hosting.md)
> for the deployment model and [../internals/eve-hub-integration.md](../internals/eve-hub-integration.md)
> for the live Hub/Eve contract.

Research began 2026-07-12, the first vertical slice was verified 2026-07-13, and the
hosted Eve application has since replaced the Sprite-hosted bespoke agent in the
configured production path.

## Decision

Keep AgentsFS as the durable, user-owned knowledge substrate and use Eve as the agent
execution substrate.

Eve replaces the runtime plumbing: model/tool orchestration, durable turns,
checkpointing, approval parking and resume, and the session streaming protocol. It
does not replace AgentsFS repositories, retrieval semantics, citations, knowledge-base
focus, voice UX, permissions, or the product's thread model.

The distinction remains useful:

- Eve makes an in-flight agent session durable.
- AgentsFS makes the user's knowledge durable across sessions, models, and vendors.

## What was retained

- AgentsFS repository format, git/LFS storage, CLI compatibility, and sharing model.
- AgentsFS-native retrieval, including revision-pinned reads and grounded citations.
- The rule that missing knowledge is reported as missing rather than invented.
- Citation chips and source inspection in the product UI.
- Voice as an application layer that consults the same grounded agent.
- Hub identity, authorization, thread storage, and usage accounting.

## What Eve replaced

- The hand-written Responses API model/tool loop.
- The custom chat SSE protocol and request-lifetime turn state.
- Ad hoc retry, resume, approval, and long-running-turn plumbing.
- The process-global capability/tool registry.
- Client replay of the entire conversation as the only continuity mechanism.

## What was rebuilt as product code

- Authored knowledge-base tools and their path/revision safety rules.
- Citation extraction and rendering.
- Thread persistence and transcript archiving across text and voice.
- Write/review policy and approval UI.
- Realtime voice and its consult bridge into Eve.
- Multi-repository selection and permissions.

Knowledge-base focus deserves one explicit correction to the original plan. Early
research proposed keeping focus in Eve `defineState`. The current design instead uses
the Hub-backed thread record's `repo` field as the canonical value. UI changes update
that record deterministically; the agent tool writes the same record; text and voice
read it. A model call is not needed to switch focus.

## The original vertical slice

The experiment lived in the sibling `agentsfs-eve` repository and used a pinned Eve
version. It intentionally tested the smallest set that could invalidate or support the
migration:

1. AgentsFS-grounded read tools with jailed paths and revision-aware citations.
2. Citation chips and a source drawer from real Eve tool events.
3. An approval-gated write that parks and resumes durably.
4. Browser session-cursor persistence across reload.
5. Process-kill/restart behavior during a parked turn.
6. Evals for cited answers, honest absence, and approval parking.

### Results recorded on 2026-07-13

- Grounded cited answers: passed.
- Honest absence with no fabricated citations: passed.
- Durable approval parking, approve, and deny: passed.
- Restart during a parked turn: passed; completed steps replayed without being
  executed twice and the approved write ran once.

The experiment showed that Eve deleted substantial orchestration code while leaving
the differentiated AgentsFS behavior as ordinary authored tools and UI code. That was
enough evidence to proceed with the hosted implementation.

## Deployment decision outcome

The research considered self-hosting Eve on Sprites as a conservative transition. It
was not selected for production. The current topology is:

- one multi-tenant `agentsfs-eve` application on Vercel;
- Hub as the authenticated public front door at `/agent/`;
- a signed Hub identity plus per-user PAT on proxied requests;
- Hub's `/api/agent/v1/*` as Eve's repository/thread/usage data plane;
- Vercel AI Gateway for model and realtime voice traffic;
- the Sprite implementation retained as a configuration rollback path.

The old questions about whether NDJSON can stream through Hub, whether Hub can hand Eve
a stable user credential, and whether the reverse proxy can serve the application are
closed by the deployed implementation and its tests. They should not be carried
forward as open migration blockers.

## Historical limitations and lessons

The initial experiment used Eve `0.22.6` and documented rapid preview-era API churn.
That version number and its contemporaneous Gateway catalog are historical facts, not
upgrade guidance. The `agentsfs-eve` lockfile and release notes are the source of truth
for the version currently deployed.

The bespoke agent's limitations remain useful context for why the migration happened:
request-bound turns were lost on restart, large client-replayed histories could exceed
request limits, process-global focus could race across tabs, and writes lacked a
durable approval protocol. They do not describe the current Eve implementation.

Self-hosted Eve and a standalone agent subdomain remain possible architectural escape
hatches, but neither is an active rollout stage. Any future change should start from
the current contract, not resume the 2026-07 research checklist.
