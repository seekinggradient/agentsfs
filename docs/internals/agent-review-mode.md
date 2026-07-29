---
description: Agent review mode — annotate a Hub note, let Eve draft changes in a thread-scoped overlay, and approve one compare-and-swap commit.
---

# Agent review mode

Review mode is agentic co-editing on a rendered Markdown note. The owner highlights passages, adds comments, and hands them to [Eve](hosted-agent.md). Eve can answer questions or draft edits, but it cannot commit those edits during the review turn. If files changed, the agent panel shows a unified-diff proposal with an editable commit message; the owner approves one commit or discards the draft.

It spans two repositories:

- The Hub (`agentsfs`, Go) renders the note, owns the comment UI, authenticates the owner, and provides git/thread APIs.
- Eve (`agentsfs-eve`, TypeScript) runs the review turn, stores its draft in the Hub-backed thread record, renders the proposal, and calls the deterministic commit/discard endpoints.

Both are presented on the same Hub origin because `/agent/*` is reverse-proxied to the Vercel-hosted Eve app. Desktop handoff uses same-origin `window.postMessage`; mobile handoff uses a short-lived same-origin `localStorage` payload followed by navigation.

## UX flow

1. **Comment for agent** appears on a Markdown note when the signed-in viewer can write it.
2. The owner selects rendered text and adds one or more comments. Draft comments stay client-side under `afs-review:<owner>/<repo>/<path>` until handoff.
3. **Handoff to your agent** sends `{nonce, user, repo, path, head, ts, comments}` to Eve. The repo owner is included so a shared repo cannot be confused with an owned repo of the same name.
4. Eve validates freshness and nonce, resolves the exact accessible repo and base revision, and stores an active review in the selected thread.
5. Eve runs a review turn. Questions can receive answers without an edit; directives can produce changes in the review overlay.
6. When the overlay differs from the base, the panel shows the proposal diff and an editable suggested commit message.
7. **Approve & commit** turns the complete overlay into one Hub commit. **Discard** clears the overlay without changing repository `HEAD` and leaves review mode active for another iteration.

An answer-only turn produces no proposal card. Follow-up feedback in the same thread remains review-scoped until a proposal is committed.

## Quote anchoring

Comments anchor to rendered text rather than fragile source offsets. Each comment stores `{quote, prefix, suffix, occurrence}`. This lets a selection survive Markdown formatting changes and disambiguates repeated passages. When a quote no longer matches, the comment remains visible as **text changed** instead of being silently moved or dropped.

Highlights use the CSS Custom Highlight API where available, with a `<mark>` fallback. Touch selection also listens to debounced `selectionchange`; on narrow screens the comment rail becomes a bottom sheet.

## Handoff transports

Desktop uses a strict same-origin message contract:

- Hub → Eve: `{type:"afs-review-handoff", payload:{...}}`
- Eve → Hub acknowledgment: `{type:"afs-review-ack"}`
- Eve → Hub after a successful commit: `{type:"afs-review-committed", commit, files}`

Both sender and receiver check `event.origin === location.origin`. The Hub retries the handoff briefly until acknowledged; the nonce makes retries idempotent while allowing an intentional later re-handoff.

On mobile there is no dock iframe. The Hub writes the same payload to `afs-review-pending` and navigates to Eve. Eve deletes it before validation, rejects malformed or older-than-15-minute payloads, and remembers the consumed nonce so reload cannot replay the review. After commit, Eve clears the original note-draft key and offers a link back to the updated note.

## Thread-record state is the gate

The current design has no shared Sprite working tree. Review state lives in `ThreadRecord.review`:

```text
review.active
review.base     = {owner, name, head}
review.path
review.nonce
review.comments
review.overlay  = {path -> next content or deletion}
```

`POST /api/review/start` validates the handoff, resolves the owner-qualified repo, pins its current revision, and writes this state before the review turn begins. A missing, inaccessible, or ambiguous repo returns a visible error and no turn runs.

During an active review, Eve dynamically removes tools that could escape the review's scope: `focus_repo`, `write_note`, `git_push`, and repo creation are unavailable. `write_file` and `edit_file` remain, but `getKbBackend` returns a `ReviewOverlayBackend`; file reads see the pinned base plus the thread overlay, and writes can only update that overlay. The normal Hub CAS commit path is unreachable from an agent tool during the turn. This is the structural no-commit guarantee.

## Proposal and deterministic endpoints

- `GET /api/review/proposal?t=<threadId>` compares the overlay with the pinned base. It returns `204` for no effective edits, or `{files, diff, suggestedMessage}`.
- `POST /api/review/commit {threadId, message?, repo?}` submits all overlay changes as **one** Hub compare-and-swap commit against `review.base.head`. On success it clears review mode and returns `{commit, pushed:true}`.
- `POST /api/review/discard {threadId, repo?}` clears the overlay but keeps the review active, allowing another round without touching the repo.

Both mutations verify that the client-supplied repo still matches the review base. A read-only user receives `403`. If repository `HEAD` advanced during review, commit returns `409`; the draft is not applied, and the user must reopen the note and review against the current revision. No automatic merge can silently change what the owner approved.

Eve asks the model to finish an editing reply with `Commit: <message>`. The proposal extracts the last such line and uses it only as an editable suggestion; the owner has final control over the commit message.

## Deployment

Review UI/tool/endpoint changes live in `agentsfs-eve` and ship with `vercel deploy --prod`. Hub note-rail or handoff-contract changes live in this repository and ship with `fly deploy`. When changing the cross-app payload, deploy backward-compatible Hub support first, then Eve, verify the full handoff through `hub.agentsfs.ai`, and remove compatibility later.

## Legacy implementation

The original review flow used an `agentsfs-chat` process and a dirty git working tree inside each user's Fly Sprite. Its proposal endpoints committed and pushed that checkout, so delivery required rebuilding the embedded bundle and reprovisioning Sprites. That design remains relevant only to the Hub's legacy fallback when `HUB_EVE_AGENT_URL` is unset; it is not the current production review path.
