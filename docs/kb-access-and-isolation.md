# KB access and task isolation: remote-at-HEAD vs clone-and-sync

Companion to [eve-hosting.md](eve-hosting.md) (which proposed "read tools via Hub API,
sandbox only for shell"). Akshay's challenge, 2026-07-15, sharpened that proposal into
this decision doc: **the tradeoff is not sandbox-or-not, it is task isolation.** An agent
calling per-file APIs against the live Hub can read the same file twice and get different
results, because another writer committed in between. It never holds a consistent view of
the KB. A clone gives it one. This is the agents-writing-code analogy: do agents commit
every edit straight to a shared HEAD and read everyone's in-flight writes, or check out a
copy, work in isolation, and submit?

This matters structurally, not hypothetically: the product's whole thesis is many agents
(hub agent, OpenClaw, laptop CLIs, collaborators, the gardener) sharing one git-backed
brain. Concurrent writers are the design premise. "Rare race" reasoning does not apply.

## The failure modes of live-at-HEAD

1. **Torn reads.** search → read → re-read within one turn spans a commit landing; the
   agent reasons over two versions of one file without knowing it.
2. **Mixed-revision answers.** File A read at rev N, file B at rev N+1 — the answer can
   be internally inconsistent even though each citation is individually "correct."
3. **Lost updates.** Read-modify-write through an API with no base revision silently
   clobbers a commit that landed mid-edit.
4. **Unstable navigation.** Agentic retrieval assumes the tree it oriented against still
   exists three tool calls later.

## The decoupling that dissolves the dilemma

Git is already an MVCC store: every commit is a free, immutable snapshot. That means
**consistency and transport are separable decisions**:

- *Consistency model* — which revision does a unit of work see? (live HEAD / pinned per
  turn / pinned per session)
- *Transport* — where do the bytes come from? (Hub API vs local clone in a sandbox)
- *Write protocol* — how do changes land? (blind commit to HEAD / revision-anchored
  compare-and-swap / branch + review)

You can have isolation **without** a clone: pin a revision at the start of the work unit
and serve every read at that revision (`git show <rev>:<path>`, `ls-tree <rev>` — the Hub
already caches views by commit, so rev-pinned reads are its native shape). And you can
have a clone **without** isolation discipline: a long-lived checkout that drifts is how
the sprite content-reconcile loops were born. The clone question is really a
*capabilities* question (shell needs a filesystem); the isolation question is answered by
revision pinning either way.

## Options

**A. Live-at-HEAD API.** Always freshest; simplest server; all four failure modes.
Acceptable only for explicitly freshness-seeking calls ("what changed just now?").

**B. Revision-pinned API (snapshot isolation, no clone).** The work unit resolves HEAD
once → `rev`; every read is served at `rev`; every citation carries it; writes are
CAS commits (see below). No sandbox, no clone latency, instant new conversations.
Cost: the Hub needs rev-parameterized read endpoints (file/tree: cheap, git-native;
**search-at-rev is the hard part** — options: lazily build the index per pinned rev;
or search at HEAD but serve result *reads* at `rev` and flag the skew; or accept
index-at-HEAD for ranking only, since cited content is what must be consistent).

**C. Clone-per-session (sandbox working copy).** A developer's checkout: perfect
isolation, shell-grade capabilities, branch workflows possible. Costs: clone/prewarm
latency and sandbox economics (eve-hosting.md), *deferred* conflicts (they surface at
push, possibly long after the divergence), and the staleness-reconcile loops any
long-lived checkout owes (the sprite lesson: a checkout without a convergence story
drifts).

**D. Hybrid (recommended).** B for chat-grade sessions — the dominant, read-mostly case;
C lazily, when a session first needs shell/bulk work, seeded *at the session's pinned
rev* so escalation doesn't change what the agent sees. One write protocol underneath
both.

## The write protocol (shared by all paths)

Every write names the revision it was reasoned against: `commit(baseRev, changes)`.
The Hub applies it iff fast-forward from `baseRev`; otherwise it attempts a
non-conflicting merge (disjoint files), else rejects with the diff — and the agent
re-reads at new HEAD and retries or asks. This is optimistic concurrency with git as
the arbiter, and it is what laptop agents (OpenClaw et al.) already do implicitly via
push/pull — the Hub API path just makes the base revision explicit per write instead of
per checkout. Approval-gated writes compose unchanged (approve → CAS commit).

For heavyweight tasks, C upgrades naturally to **branch + review**: the agent works on a
branch, the human merges — the strongest approval story we have, for the work that
deserves it.

## Choosing the pin unit

- **Pin per turn (default).** Each turn opens at then-HEAD: fresh between turns,
  consistent within one. Mirrors agentsfs-chat's reconcile-before-turn behavior; a turn
  is the natural "tiny checkout → work → commit" cycle from the engineer analogy.
- **Pin per session (opt-in).** Maximal stability for long reasoning arcs; staleness is
  explicit (a "refresh to latest" affordance, surfaced when HEAD moves — the UI can show
  "KB advanced by N commits" from the rev delta).
- Live-HEAD only as a deliberate tool argument (freshness checks), never the default.

## Costs and complexities, weighed

| | B (pinned API) | C (clone/sandbox) |
| --- | --- | --- |
| Consistency | snapshot per turn/session | snapshot for the whole checkout |
| New-conversation latency | ~zero | clone or template-restore |
| Infra | Hub read API (+search-at-rev work) | sandbox + prewarm + expiry handling |
| Conflicts | at write time, small, immediate | at push time, batched, possibly large |
| Capabilities | read + file-grain writes | full shell, builds, bulk refactors |
| Staleness handling | re-pin per turn (automatic) | pull-reconcile loops (owed explicitly) |
| Cost profile | HTTP round-trips (~tens of ms) | sandbox hours + storage (eve-hosting.md) |

## Decision

Adopt **D**: revision-pinned Hub reads (pin per turn by default, per session opt-in) +
CAS writes for the hosted chat agent; lazy escalation to a sandbox clone seeded at the
pinned rev for shell/bulk work, pushing back through the same CAS gate; branch+review
reserved for large delegated tasks. Live-HEAD reads only as an explicit freshness tool.

Consequences to build: rev-parameterized Hub read endpoints (decide the search-at-rev
variant before building); `commit(baseRev, …)` endpoint with merge-else-reject; the
Eve agent's KB tools gain a session/turn `rev` from state; citations already carry
`{repo, revision, path}` and become exact under pinning (the drawer can open the cited
revision, not HEAD's approximation).

Open questions: search-at-rev cost in practice; pin-per-turn vs per-session default for
voice (a long spoken thinking session may prefer session pinning); how "KB advanced"
surfaces mid-conversation without nagging.
