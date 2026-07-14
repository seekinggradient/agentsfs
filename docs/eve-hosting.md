# Hosting the Eve-based agent: Hub, Vercel, and whether sprites survive

Companion to [eve-migration.md](eve-migration.md) (why Eve) and `agentsfs-eve/docs/v2-plan.md`
(feature roadmap). This doc answers one question: **where does the hosted agent run once
it's built on Eve — and do we still need Fly sprites?**

Framing honesty first: sprites were the right answer *for the pre-Eve agent*. We needed a
place to run a long-lived Node process per user, a security boundary for a shell-capable
agent, and a persistent workspace of git checkouts — and we built all of it by hand
(provisioning, bundle embedding, boot scripts, proxy hardening, wake/sleep handling).
Eve + Vercel provides managed equivalents for most of that list. The question is which
equivalents we adopt and what the Hub keeps.

## What each side actually provides

**Sprites give us today:** one Firecracker VM per user (`afs-user-<user>`) = the security
boundary for shell + all repos; a persistent filesystem (warm clones, conversation data);
a place to run the agent service on :8080; no provider key on the box (Hub LLM proxy +
per-user PAT); Fly wake/sleep economics; full control, no platform coupling. Cost: the
entire `internal/hub/agent.go` provisioning/reliability surface we keep debugging.

**Vercel gives an Eve app:** Functions (fluid compute) for the app + agent routes;
Workflows (managed durable-session state — no `.workflow-data/` to own); **Vercel
Sandbox** per durable session with template prewarming (`bootstrap`/`onSession`/
`revalidationKey`), egress network policies, and firewall-layer credential injection
(secrets usable by sandbox processes without ever entering model context); AI Gateway
(provider-neutral models + realtime voice tokens + usage dashboard); Cron for schedules;
Agent Runs observability; Connect for OAuth. One deployment serves all users —
multi-tenancy is route auth + per-session state, a pattern Eve documents.

## The decisions

### 1. Where does agent compute run?

- **(A) Self-host Eve on sprites.** `eve build` → Nitro app, `eve start` on the sprite;
  `.workflow-data/` on sprite disk; Hub proxy extended to forward `/eve/` **and**
  `/.well-known/workflow/` (documented hard requirement — missing it stalls runs
  silently). Keeps everything we have; gains Eve's runtime; keeps 100% of sprite ops and
  gains none of the managed pieces. Legitimate fallback, not the target.
- **(B) Vercel-native.** The `agentsfs-eve` Next+Eve app deploys to Vercel as one
  multi-tenant app. Sessions are durable via Workflows; shell/exec runs in per-session
  Vercel Sandboxes. Sprites retire. Most canonical, least ops, most platform coupling.
- **Recommendation: B, reached in stages (Decision 7).** Fly-because-no-Eve is exactly
  right: the VM-per-user shape existed to host a bespoke always-on process. Eve's shape
  is per-session compute + managed durability, and Vercel is its native habitat.

### 2. Where do the repos live? (the Hub question)

**The Hub stays, unambiguously.** It is the product's knowledge home: identity/accounts,
real git remotes + LFS (the exit ramp guarantee), sharing/collaborators, the web reading
and editing UI, admin. None of that moves to Vercel.

What changes: the Hub stops *hosting agent compute* and becomes the agent's **git
upstream + identity provider**. Sandbox `bootstrap`/`onSession` clones the user's repos
from the Hub over its existing PAT-authenticated git endpoints, with `revalidationKey`
tied to repo HEADs so warm templates skip re-cloning. The agent's checkpoint commits push
back to the Hub — same contract as today, minus the resident VM.

### 3. Security boundary for shell and writes

Today: per-user VM, all repos (owned + collaborator-shared) in one shell — which is why
the shared-repo prompt-injection finding (malicious shared KB → victim's private KBs +
PAT) has stayed open. Vercel-native: **the boundary becomes the per-session sandbox,
seeded with only the repos in scope for that conversation** (default: the focused repo;
opt-in widening), egress default-deny + Hub allowlisted, PAT delivered by firewall-layer
injection rather than as a readable env var. This is a *better* trust model than the
sprite gives us — isolation follows conversation scope instead of user identity. It's
also the canonical home for `run_bash` (v2-plan Phase 2 defers shell to exactly this).

### 4. Model path and cost metering

Today: Hub LLM proxy (sprite PAT in, Hub's OpenAI key out, per-user metering into
`/admin/metrics`). Options on Vercel:
- Keep the Hub proxy: Eve's `model` accepts an AI SDK `LanguageModel` object pointed at
  `<hub>/v1/agent-llm`. Metering parity for free; but voice needs Gateway anyway, and we
  stay OpenAI-coupled.
- **Unify on AI Gateway (recommended target):** provider-neutral ids, realtime voice
  tokens from the same account, one billing surface. Restore per-user attribution in
  `/admin/metrics` with a `defineHook` that posts each turn's usage `{user, model,
  tokens, cost}` to a small Hub ingest endpoint — the hook slot replaces the proxy's
  regex metering, and the admin UI keeps working. Interim (Decision 7 stage 1) can run
  text through the Hub proxy unchanged.

### 5. Identity, auth, and the URL

Users should keep landing on `hub.agentsfs.ai/agent/`. Two workable shapes:
- Hub reverse-proxies the Vercel app (forwarding `/eve/` + `/.well-known/workflow/`,
  NDJSON streaming — needs a proxy-hardening pass like the sprite one, but against one
  trusted upstream instead of user-controlled VMs).
- Or the agent runs on its own subdomain (`agent.agentsfs.ai`) with an auth handoff: Hub
  session → short-lived signed token → Eve route auth (`defineChannel` auth policy
  verifying the Hub's signature). Cleaner streaming path, one less proxy; costs a
  cross-origin auth design.
- **Recommendation:** start with the reverse proxy (no auth redesign), measure streaming
  behavior; move to the subdomain handoff if the proxy fights NDJSON/workflow traffic.

### 6. Voice

Gateway Realtime end-to-end (v2-plan Phase 4): server-side token mint with the Gateway
key, browser `useRealtime`, `consult_knowledge` bridging into the same durable session.
Nothing sprite-shaped anywhere in the path — voice is a pure argument for Decision 4's
Gateway unification.

### 7. Migration sequence (keep both worlds green)

1. **Now:** agentsfs-chat on sprites keeps serving production untouched. agentsfs-eve
   develops locally through v2-plan Phases 1–3.
2. **First hosted deploy:** `agentsfs-eve` → Vercel behind an allowlist (Hub-signed
   token or basic auth), Hub git as clone source, text via Hub LLM proxy (metering
   parity), no shell yet. Validate: durability under redeploy, sandbox clone cold-starts,
   NDJSON through the chosen auth front, per-user session isolation.
3. **Parity build-out:** shell-in-sandbox (Decision 3), Gateway unification + hook
   metering (Decision 4), voice (Decision 6), conversations UI.
4. **Cutover:** `hub.agentsfs.ai/agent/` points at the Eve app for opted-in users, then
   everyone. Sprite fleet wound down; `internal/hub/agent.go` provisioning, the embedded
   bundle, and the sprite proxy allowlist retire (the Hub keeps only the thin proxy or
   token handoff from Decision 5).

### So: do we still need sprites?

**In the target architecture, no.** Every job the sprite does has a managed successor:
compute → Functions + Workflows; workspace → per-session sandboxes seeded from Hub git;
security boundary → sandbox scope (an improvement — see Decision 3); no-key-on-box → we
keep it (Hub proxy interim, gateway-key-server-side target); wake/sleep economics →
per-session compute that costs nothing between turns. Sprites remain the fallback if
verification (below) surfaces a blocker, and self-host-on-sprite (Option A) remains
possible forever because Eve's self-host path is real — that's our platform-risk hedge
against Vercel pricing/coupling, and it's worth keeping documented and occasionally
smoke-tested.

## Costs and latency (verified 2026-07-14, official pricing pages)

**Fly Machines (sprite-shaped):** shared-cpu-1x/1GB ≈ $0.0079/hr running; a **stopped**
machine bills only rootfs at $0.15/GB-month. A mostly-idle per-user sprite (~30 min/day
active) ≈ **$0.27/month**. Wake: suspend→resume "a few hundred ms", stop→cold-start ~2s+
(community reports up to 3–8s under load). No platform fee.

**Vercel (Eve-shaped):** Sandbox $0.128/vCPU-hr active + $0.0212/GB-hr provisioned memory
(bills only while running; default idle-stop 5 min, max 24h on Pro; **fixed 32GB disk;
iad1 only today**; persistent-by-default via auto-snapshot, $0.08/GB-mo, **snapshots
expire 30 days after last use** — dormant users re-clone from Hub, which is fine since
Hub git is canonical). Workflows: $0.02/1K events + $0.50/GB written (**stream data
counts**) + $0.50/GB-mo retained. Functions similar rates; AI Gateway is **0% markup**
pass-through. Same worked example ≈ **$3/month list** — but Pro's mandatory $20/seat fee
is itself a $20 usage credit, so low fleets ride inside the seat fee.

Read of the numbers for our fleet: raw metered cost is ~10× cheaper on Fly per idle user,
but at today's user count both are noise; the real money is model tokens either way. The
decision should be made on ops burden, isolation model, and latency — not on this delta.
One genuine cost/latency optimization for the Vercel design: **read-only tools don't need
a sandbox at all** — they can read via the Hub's raw/git API from Functions, reserving
sandboxes for shell/write sessions. That collapses both cold-start latency and sandbox
hours for the dominant read-mostly workload.

**Two retention facts that constrain design:**
- **Workflows data retention: 1 day Hobby / 7 days Pro / 30 days Enterprise.** Eve
  session event logs are NOT a long-term transcript store on Vercel. The v2-plan Phase 3
  thread design must archive events app-side (or in the Hub) rather than relying on
  replay-from-index-0 for old threads. (Self-hosted Eve keeps `.workflow-data/` as long
  as we like — a point for the sprite fallback.)
- Agent Runs observability retention is 1 day on Pro (30 days with Observability Plus at
  $1.20/1M events) — an ops surface, never the audit store, as the Vercel research note
  already said.

**Eve framework latency:** no quantified public numbers exist yet (framework is a month
old; two third-party reviews and the issue tracker have zero benchmarks). Our own
observations stand: per-step checkpoint overhead is real but model-dominated locally;
benchmark on Vercel during migration stage 2 before cutover.

## Verify before committing (cheap checks, in order)

- Vercel Sandbox limits/pricing for our shape: max session duration, idle-stop/restore
  timing, filesystem persistence guarantees across restores, prewarm template size limits
  (multi-GB KBs with LFS?). This is the make-or-break for Decision 3.
- Workflows pricing at our turn volume (stream writes bill as workflow data).
- Clone-from-Hub cold-start: seconds for a typical KB from `bootstrap` cache vs fresh.
- NDJSON + `/.well-known/workflow/` behavior through the Hub's Go reverse proxy.
- Gateway usage API granularity (can a hook read authoritative per-call cost, or do we
  keep our own price table like today).
- Eve session retention on Vercel (thread rehydration in v2-plan Phase 3 depends on old
  events staying readable; if retention is bounded, the thread store must also archive
  events — check before building Phase 3's rehydrate-only design).
