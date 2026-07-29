---
description: The current hosted Eve agent: how the Hub authenticates it, how it reads and writes knowledge, and how it is released.
---

# The hosted Eve agent

Signed-in users talk to Eve at `https://hub.agentsfs.ai/agent/`. Eve can work across the knowledge bases the user owns or has been granted access to, while the AgentsFS Hub remains the authority for identity, repositories, permissions, git commits, and conversation records.

The current production agent is the shared `agentsfs-eve` application hosted on Vercel. It is **not** a cloned agent process or a permanently provisioned VM per user. Isolation comes from authenticated user scoping and the Hub API's permission checks, revision pins, and compare-and-swap writes.

The top-level agent starts without a focus unless its thread already has one. **Talk to an agent** on a repo page opens `/agent/?repo=<slug-or-owner/slug>` so that thread can be focused on the chosen knowledge base.

## Request path

For a normal browser request:

1. The browser requests `/agent/*` on `hub.agentsfs.ai`; the Hub authenticates its normal login session.
2. The Hub reverse-proxies the request to the configured Vercel upstream. Production currently sets `HUB_EVE_AGENT_URL=https://agentsfs-eve-staging.vercel.app`.
3. Before forwarding, the Hub removes the browser's cookie and any inbound `X-AFS-*` identity headers. It injects a short-lived HMAC-signed `X-AFS-User` handoff and that user's Hub PAT in `X-AFS-PAT`.
4. Eve verifies the signature using the matching `HUB_EVE_AGENT_SECRET`, scopes the session to that user, and captures the PAT for durable work that may continue after the HTTP request ends.
5. Eve calls the Hub's PAT-authenticated `/api/agent/v1/*` endpoints for repository, commit, thread, and usage operations. The Hub applies the same owner/collaborator/public rules it applies elsewhere.

The Hub session cookie never leaves the Hub, and a browser cannot spoof another user or inject a foreign PAT because those headers are always removed before the Hub adds its own. Long-lived model and Gateway credentials stay in the Vercel project and are never stored in a user knowledge base; voice receives only a short-lived, scoped realtime client token.

The `/agent` shell and Next.js assets remain below the Hub prefix. In the deployed path, browser requests below `/agent/eve/v1/*` are mapped to Eve's root `/eve/v1/*` service routes; other `/agent/*` paths stay prefix-aware. Streaming responses are flushed without buffering. See [eve-hub-integration.md](eve-hub-integration.md) for the proxy contract.

## Knowledge access

Eve does not need a persistent clone of every user's repositories. The Hub exposes a hosted-parity API with git semantics:

- `GET /api/agent/v1/repos` lists repositories the PAT owner owns or collaborates on. An explicitly named public repository can also be read without becoming ambient discovery.
- A turn resolves the focused repo's current `HEAD` once and pins reads to that revision. File, tree, and search results in that unit of work therefore cannot mix revisions.
- Writes name the pinned `baseRev`. The Hub fast-forwards the commit, safely merges a disjoint concurrent update where possible, or returns `409` with conflict details.
- The Hub enforces read/write roles independently of Eve. A read-only collaborator cannot write, even if an agent or client asks it to.

Successful Hub-mode writes are already real commits in the Hub repository. There is no separate clone to push afterward, so Eve's `git_push` compatibility tool explains that the write is already committed rather than performing a second push.

## Focus and conversations

Knowledge-base focus belongs to the conversation, not to a process-global agent state. `ThreadRecord.repo` in the Hub-backed thread record is the single source of truth for the UI, typed turns, and voice turns. Hub mode stores the canonical `owner/name` form so same-named owned and shared repositories remain unambiguous.

The knowledge-base dropdown updates that record through a validated `PUT /api/threads/:id/focus`; it does not ask a model to call a switching tool. Conversational switching remains available through `focus_repo`, which writes the same field. Every knowledge tool resolves the current focus from the thread record.

The Hub also stores the per-user thread index, archived events, voice entries, and review state. Eve's durable workflow event log drives active turns; the Hub record is the cross-deployment product record used to list and restore conversations.

## Tools, writes, and review

Eve exposes AgentsFS-specific tools rather than a general-purpose hosted shell:

- Read and retrieval tools include `search_wiki`, `retrieve`, `grep`, `read_file`, `list_dir`, `tree`, `backlinks`, theses tools, and past-conversation search.
- Workspace tools include `list_repos`, `focus_repo`, and Hub-only `create_kb`.
- Write tools include `write_note`, `write_file`, and `edit_file`, with Eve approval policies and Hub permission checks.

All file paths and repository targets are validated before access. Citations include repository, revision, and path so an answer can be traced to the exact content it used.

Review mode has a stronger structural gate. The Hub sends the review request into a thread; Eve writes proposed changes into `ThreadRecord.review.overlay`, not to repository `HEAD`. The review card shows the resulting diff. Only the owner-only commit endpoint can turn that overlay into one Hub CAS commit; discard clears the proposal without advancing the repository.

## Voice

Voice uses the realtime model for the microphone/speaker loop and the same durable Eve thread for knowledge work. The browser obtains a short-lived realtime token through the authenticated `/agent` surface, then connects to the realtime service. When a request needs grounded knowledge or multi-step work, `delegate_to_agent` sends it into the open durable Eve session and returns the result to the voice conversation. Typed and spoken work therefore share focus, transcript, citations, and thread history.

Changing the dropdown focus while voice is open updates the thread record directly and silently re-briefs the realtime session. It does not create a synthetic chat turn or wait for a model-generated acknowledgment.

## Environment and releases

The production connection requires matching configuration on both services:

- Hub: `HUB_EVE_AGENT_URL` and `HUB_EVE_AGENT_SECRET`.
- Eve: the same `HUB_EVE_AGENT_SECRET`, the Hub API base URL, Vercel AI Gateway credentials, and the Vercel-backed Eve/workflow configuration.

For an Eve-only release, test the `agentsfs-eve` repository and run:

```sh
vercel deploy --prod
```

Vercel promotes the new immutable deployment to the stable alias already configured in the Hub, so no Hub deployment is required. Run `fly deploy` only when Hub code or the Hub–Eve contract changes. See [how-deployment-works.md](how-deployment-works.md) for the full release matrix.

## Legacy Sprite fallback

The codebase retains the earlier per-user Fly Sprite + embedded `agentsfs-chat` path behind the absence of `HUB_EVE_AGENT_URL`. That path provisions clones, a local conversation store, and an agent service inside each Sprite. It is a rollback/fallback implementation, not the current production architecture. Sprite bundle rebuilding, VM reprovisioning, the Hub LLM proxy, and Sprite shell-security notes apply only when that legacy mode is intentionally enabled.
