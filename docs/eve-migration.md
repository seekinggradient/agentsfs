# Eve migration research: agentsfs-chat → Eve-based agent

Status: research complete; v1 vertical-slice experiment implemented at `~/Development/agentsfs-eve` (see "The v1 experiment" below).
Date: 2026-07-12/13 (overnight autonomous run).
Prior research: `~/Development/EveExperiments/agentsfs/` — especially `deep-reads/Eve versus agentsfs-chat.md` (the full capability matrix and decision test) and `research/Eve.md`. This doc condenses that work, adds the Hub/sprite deployment analysis specific to this repo, and records the v1 experiment plan and results.

## TL;DR

Eve (Vercel's agent framework) and agentsfs-chat are not substitutes. Eve overlaps with the *runtime half* of agentsfs-chat — the model/tool loop, streaming protocol, session durability, sandboxing, approvals — not with AgentsFS itself, and not with the differentiated product layer (grounded retrieval semantics, citations UX, repo focus, voice).

The recommendation, unchanged from the prior deep-read and now backed by a working slice:

> Keep AgentsFS as the durable, user-owned knowledge substrate. Keep the citation contract and product behaviors. Evaluate Eve as the execution substrate underneath — via a narrow vertical slice, not a wholesale rewrite.

Durability is not memory: Eve makes a *session* durable (checkpointed turns, park/resume, survives redeploys); AgentsFS makes *knowledge* durable across sessions, models, and vendors. Both remain necessary.

## What Eve is (short version)

- Filesystem-first TypeScript framework: an agent is a directory (`agent/instructions.md`, `agent/agent.ts` with `defineAgent`, `agent/tools/*.ts`, plus skills/channels/connections/sandbox/subagents/schedules slots). Eve discovers and compiles these into a local dev app or a Vercel deployment.
- Framework-owned harness: built-in sandbox tools (`bash`, `read_file`, `write_file`, `glob`, `grep`), `web_fetch`/`web_search`, durable per-session `todo`, `ask_question` (parks the turn), delegation, context compaction near the model limit. Built-ins can be overridden or removed (`disableTool()`).
- Durable sessions over the default HTTP channel (`/eve/v1/session`, `.../stream` NDJSON): a session is a durable workflow; turns checkpoint at each model/tool step; approvals, questions, OAuth, and child agents park without holding compute; completed steps replay after crash/redeploy. Client keeps a cursor (`sessionId`, `continuationToken`, `streamIndex`).
- `defineState` = durable per-session state (survives crashes/redeploys within one session; not cross-session memory).
- Sandbox lifecycle: `bootstrap()` at template build (clone repos, install deps), `onSession()` per durable session; `/workspace` persists across turns within a session. Local backend is microsandbox; Vercel backend is Vercel Sandbox.
- Models route through Vercel AI Gateway by default (string ids like `openai/gpt-5.1`); direct provider SDKs are possible with the provider's key.
- Open source (Apache-2.0), public preview since 2026-06-17, very high release cadence with real API churn. Verified 2026-07-13: latest is **0.22.6** (published 2026-07-12), near-daily releases, breaking changes at almost every minor (tool renaming in 0.12, `defineTool` auth removal in 0.13, approval-status rework in 0.14, sandbox-backend contract in 0.20). Requires Node ≥ 24. Pin the exact version; read every release note; upgrade on a branch.
- Self-hosting is documented and real: `eve build` emits a standard Nitro app under `.output/`; `eve start` runs it on any Node host. Workflow durability state defaults to the filesystem (`.workflow-data/`, must be on persistent disk) with a Postgres "world" as the swap-in. Off Vercel you lose sandbox prewarming, the Agent Runs dashboard, and OIDC auth — not the framework itself.

## What agentsfs-chat is (short version)

~4,500 LOC TypeScript (plus ~2,250 LOC vanilla web UI, ~2,300 LOC tests), zero-framework `node:http` server:

- Hand-written OpenAI Responses API tool loop with streaming SSE, per-turn model fallback, iteration caps.
- Agentic AgentsFS retrieval (no RAG index): `search_wiki`/`tree`/`backlinks`/`grep`/`list_dir`/path-jailed `read_file`, capability-gated domain tools, all returning citations.
- Citations as load-bearing UX: every material claim cites the exact tool-returned path; chips + source drawer; "not in the KB → say so."
- Single-repo and multi-repo workspace modes with live per-call focus.
- Voice: OpenAI Realtime over WebRTC with a `consult_knowledge` handoff into the same grounded agent.
- Hosted mode: one Fly sprite (Firecracker VM) per user, Hub reverse proxy, Hub model proxy so no provider key lives on the VM.

Known structural weaknesses (verified in the 2026-07-09/10 review; P0s fixed): request-lifetime turns (no durability), unbounded client-replayed history (413 at 1 MB), process-global repo focus (cross-tab races, capability/focus mismatch), archive-grade conversation store with lossy concurrent writes, no approval protocol, voice state entirely ephemeral.

## Where Eve clearly wins

1. **Durable execution.** A chat turn survives crash/redeploy; approvals and questions park without compute. agentsfs-chat loses in-flight work with the HTTP request.
2. **Human-in-the-loop.** Per-tool approval policies (never/once/always/custom) with durable park/resume — the missing piece for gating writes/shell/pushes.
3. **Context management.** Harness-owned compaction and token budgets vs. our unbounded history replay.
4. **Convention over bespoke plumbing.** Tools/skills/channels/connections/hooks/schedules/subagents/evals have standard authored slots; the next agent reuses the same conventions, protocol, and deploy path. We currently hand-maintain all of it.
5. **Dynamic capabilities.** Tools/instructions/models resolvable per session/turn/step — the correct primitive for per-repo capability gating, which agentsfs-chat got wrong with its process-global registry.
6. **Observability + evals.** OpenTelemetry, workflow metadata, an eval runner that drives the real session protocol.

## Where agentsfs-chat (the product) stays ahead

1. **AgentsFS-native retrieval semantics** — tree orientation, backlinks, freshness, frontmatter/sources, domain tools. Eve gives generic file tools; the domain behavior is ours to port.
2. **The citation contract** — Eve emits tool events/traces, but cited answers bound to repo+revision+path with an end-user drawer are product work to rebuild.
3. **Realtime voice** — Eve has no full-duplex audio runtime. Voice stays an application-level layer (Realtime API or AI Gateway Realtime) with a consult-the-agent tool seam, same as today.
4. **One persistent per-user workspace** — the sprite model gives a user all their checkouts in one VM. Eve's isolation unit is a per-session sandbox; a different (per-conversation) state model with cold-start implications.

Neither system provides for free: a canonical cross-modality transcript store, thread lists/retention/search, per-user session authorization, citation identity across repo switches, rate/abuse limits. Those are application responsibilities under either runtime.

## Deployment analysis for OUR hosted model (new here)

The Hub/sprite contract (from `internal/hub/agent.go`, `agent_ui.go`, `web.go`) constrains any replacement agent app:

- Sprite service must listen on **:8080** and answer `GET /api/health` → 200.
- The Hub proxy is **deny-by-default over an exact method+path allowlist** (`classifyAgentPath`): `/api/chat` (SSE), `/api/config`, `/api/instances`, `/api/instance`, `/api/focus`, `/api/tools/call`, `/api/realtime/token`, `/api/review/*`, `/api/conversations*`, `/preview`. UI files are served from the Hub's embedded immutable bundle, never proxied. Cookies are stripped; a Sprites bearer is injected; redirects and auth headers are rejected.
- The model proxy `/v1/agent-llm` allowlists only OpenAI `responses`, `chat/completions`, and `realtime/*`, authenticates the sprite's per-user PAT, injects the Hub's OpenAI key, and meters usage into admin metrics.

Consequences for an Eve-based agent:

1. **Eve's default HTTP surface (`/eve/v1/*`) does not match the Hub allowlist.** Options: (a) extend `classifyAgentPath` to allowlist Eve's routes (Eve streams NDJSON, not SSE — `hardenAgentProxyResponse` MIME forcing needs an entry); (b) put a thin app server in front of Eve on the sprite that keeps today's `/api/*` contract and talks to Eve internally. Either is tractable; (a) is less code, (b) preserves the immutable-UI story unchanged. **Gotcha (from Eve's own deployment docs): a proxy must forward both `/eve/` and `/.well-known/workflow/` — restricting to `/eve/` alone lets sessions start but silently stalls runs forever.**
2. **Model routing.** Eve defaults to Vercel AI Gateway, but `defineAgent`'s `model` documentedly accepts any AI SDK v7 `LanguageModel` object — so `createOpenAI({ baseURL: "<hub>/v1/agent-llm", apiKey: <PAT> })(model)` is the hosted path. The Responses/chat-completions endpoints the proxy already allowlists are exactly what the AI SDK uses. No Gateway account needed on the hosted path; Hub cost metering keeps working unchanged.
3. **Sandbox on a sprite.** Eve's local sandbox backend (microsandbox) wants virtualization; nested virtualization inside a Firecracker sprite is a non-starter. For hosted, the honest configuration is: disable/replace Eve's sandbox-backed built-ins and run our path-jailed AgentsFS tools in the app runtime — the sprite itself is already the isolation boundary (one VM per user, no provider key on box). This mirrors today's model exactly. Vercel-hosted Eve (with Vercel Sandbox) is the alternative deployment that trades our per-user VM model for per-session sandboxes + Vercel services; that is a bigger strategic move and not required to benefit from Eve's runtime.
4. **Durability storage.** Self-hosted Eve persists workflow state under `.workflow-data/` on the filesystem by default (Postgres world optional). Sprites persist their filesystem across sleep/wake, so this should survive — needs a live verification pass (open question below).

Bottom line: the hosted path is *possible* without abandoning the sprite/Hub-proxy trust model, but it is real integration work and should come only after the local vertical slice proves Eve's value.

## Migration shape: keep / replace / rebuild

Keep: AgentsFS format + CLI; retrieval semantics and domain tools; citation schema + UX; repo focus, conversations, preview as product concepts; voice UX; Hub identity/model-proxy concepts.

Replace (what Eve deletes): the hand-written Responses loop; manual tool-round orchestration and fallback plumbing; the custom chat SSE protocol; static global tool registry; ad-hoc long-turn state; (eventually) compaction/budget plumbing we never built.

Rebuild deliberately on Eve: conversation persistence as canonical (not an archive); per-session repo focus via `defineState` (kills the process-global focus bug class); citations bound to `{repo, revision, path}`; write/shell approval policy via Eve HITL; voice/text continuity around one Eve session cursor.

## The v1 experiment (this run)

Per the prior deep-read's decision test: build one narrow vertical slice on a pinned Eve version, then compare. Scope chosen for v1 (separate repo `~/Development/agentsfs-eve`, sibling of agentsfs-chat):

1. Eve agent with AgentsFS-grounded authored tools — `search_wiki`, `tree`, `read_file` (path-jailed), `backlinks`, `list_dir`, `grep` — wrapping the `afs` CLI + jailed file access, each returning `{repo, revision, path}` citations. Default sandbox-backed built-ins disabled (least privilege; tools run in app runtime against a local KB, mirroring the sprite trust model).
2. Citation contract end-to-end: tools → Eve events → UI chips + source drawer.
3. One approval-gated mutation (`write_note`) demonstrating durable park/resume.
4. Web UI on `useEveAgent` with full session-cursor persistence (`sessionId` + `continuationToken` + `streamIndex` + rendered transcript), so reloads resume the same durable session.
5. Durability probe: kill the server mid-tool-turn, restart, verify the session resumes rather than losing the turn.
6. Evals: grounded-answer-cites-file; not-in-KB-admits-absence; approval parking.
7. Out of scope for v1: voice, multi-repo workspace focus, sprite/Hub deployment, schedules/subagents/connections.

Model: via AI Gateway locally (`AI_GATEWAY_API_KEY`), model id configurable, default per current Hub choice.

### Results (2026-07-13, overnight run)

v1 landed at `~/Development/agentsfs-eve` (8 commits; eve pinned 0.22.6; Next.js host via
`withEve`; ~25 offline tests + live protocol smoke + browser E2E). All four acceptance
criteria verified live, most of them twice (scripted smoke against `eve dev --no-ui`, then
browser E2E through the Next host):

1. **Grounded cited answers — PASS.** Fixture questions produce `search_wiki`/`read_file`
   tool runs whose results carry `{repo, revision, path}` citations; chips render under
   the answer at the KB's git revision; the source drawer shows the real file; a
   `?path=../../../../etc/passwd` probe on the drawer route returns 403.
2. **Honest absence — PASS.** Questions with no KB coverage produce "That isn't in the
   knowledge base" with zero citations and no fabricated facts.
3. **Approval parking — PASS.** `write_note` parks the turn durably (`input.requested`,
   no compute held, file *not* written); Approve resumes and writes; Deny resumes without
   writing.
4. **Durability — PASS (the decision-test headline).** Killed (`kill -9`) the whole dev
   tree while a turn was parked at an approval; on restart Eve re-enqueued the active
   runs from `.workflow-data/`, a browser reload restored the approval card from the
   persisted cursor, and Approve resumed the turn from checkpoints — pre-crash steps
   replayed without re-running, the write executed exactly once, post-restart. A second
   probe (kill during a live read-turn) confirmed transcript/cursor survival and
   auto-re-enqueue, though that turn had already finished computing before the kill
   landed, so parked-turn probe B is the rigorous evidence.
   Bonus, unplanned: the free-tier AI Gateway rate limit caused Eve to *park* mid-turn
   rather than fail — completed tool steps checkpointed, retry re-ran only the model
   call. The bespoke stack loses exactly this work today.

What Eve deleted vs agentsfs-chat: the entire hand-written Responses tool loop, SSE
protocol, retry/fallback plumbing, and long-turn state handling. What we still wrote (and
would in any framework): the jailed AgentsFS tools + citation contract (~lib/kb.ts,
agent/tools/), the citations/drawer/approval UI, transcript+cursor persistence, and the
grounding instructions.

Friction log (details in agentsfs-eve/DECISIONS.md): eve 0.22.6's gateway catalog lacks
gpt-5.1 metadata (needed `modelContextWindowTokens` override); small models tried to
prose-confirm instead of calling the approval-gated tool until instructions said "call
directly, approval is system-handled"; citations are read from the client reducer's
`dynamic-tool` parts rather than raw NDJSON (equivalent, survives reload); the free-tier
gateway key rate-limits multi-call turns (paid key removes this).

Verdict: the slice supports adoption — Eve's runtime (durability, approvals, parking,
compaction slots) is real and deleted the plumbing we'd otherwise keep maintaining, while
the differentiated product layer (grounded retrieval, citations, honest absence) ported
cleanly as authored tools + app code. Next decisions if we proceed: paid gateway key or
Hub-proxied `LanguageModel` object; workspace/multi-repo focus via per-session state;
voice layer; and the Hub/sprite hosting work in the deployment analysis above.

## Open questions

- Workflow-state (`.workflow-data/`) persistence for self-hosted `eve start` on a sprite across sleep/wake and redeploys — believed fine, not yet live-tested.
- Eve NDJSON streaming through the Hub's hardened proxy (buffering, MIME forcing, early flush), plus the `/.well-known/workflow/` forwarding requirement.
- Cost telemetry parity if any model traffic moves to AI Gateway instead of the Hub proxy.
- Extensions (0.22.3+) as the packaging vehicle for an "AgentsFS integration" (tools+skills+instructions) once the extension API stabilizes.

## Decision guide (unchanged from deep-read, condensed)

Adopt Eve sooner if: turns need to run long or wait on humans/OAuth; multi-channel presence matters; we expect a fleet of agents sharing conventions; approvals/traces/evals would otherwise be rebuilt by hand; Vercel-managed operation is acceptable (or self-host cost is paid consciously).

Stay bespoke where: direct provider behavior and low-level voice control are central; the persistent per-user workspace is the product; Eve preview churn is too expensive this quarter.

The likely end state remains: **Eve below, AgentsFS above, an application-owned product layer around both.**
