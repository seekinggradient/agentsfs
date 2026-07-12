---
description: Agent review mode — leave inline comments on a rendered note, hand them to your agent, and approve the diff it proposes before it commits.
---

# Agent review mode

Review mode is agentic co-editing on a rendered markdown note. The owner highlights passages in the note, attaches comments, and hands them to the [hosted agent](hosted-agent.md); the agent resolves each comment by editing the file(s) **without committing**, and the panel shows a **proposal** — a unified diff plus an editable commit message. The owner approves (commit + push, done server-side) or discards. It closes the loop from "this paragraph is wrong" to a reviewed commit without leaving the note.

It spans two repos: the Hub (`agentsfs`, Go) renders the note and owns the comment UI; the agent app (`agentsfs-chat`, TS) runs on the per-user sprite and does the editing. They talk over `window.postMessage` because the agent is same-origin, reverse-proxied at `/agent/*`.

## The UX flow

1. On a rendered markdown note the owner sees **Comment for agent** in the toolbar (next to **Edit**). It appears only when the viewer can write, the file is markdown, and the agent is enabled (`{{if and .CanWrite .IsMarkdown .AgentURL}}` in `internal/hub/assets/file.html`).
2. Clicking it enters comment mode. Selecting text in the `.prose` article pops an inline note input; several comments can be attached. Each renders as a highlight in the article plus an entry in a bottom-right **comment rail** (edit / delete / Clear all).
3. **Handoff to your agent (N)** hands the comments to the agent. On desktop it opens the agent side dock and posts them into the iframe; on phones (see below) it stages them in localStorage and navigates to the agent full-page.
4. The agent runs a **restricted review turn** (below). If it edited files, the panel renders a **proposal card**: the diff, per-file `+/–` counts, and a commit-message input prefilled with the **agent's own description of the change** (the `Commit:` line contract below), falling back to `Resolve review comments in <path>`.
5. **Approve & commit** commits + pushes deterministically on the sprite; the Hub note refreshes to the new commit (on phones, a **"View the updated note →"** link on the committed card routes back to `/<user>/<repo>/blob/<path>`). **Discard** reverts the working tree. The owner can iterate — typed follow-up feedback stays a review turn (each proposal replaces the previous card), or leave fresh comments after committing.

## Comment discretion: questions get answers, not forced edits

Comments carry two intents, and the review prompt tells the agent to classify each one before acting:

- **Edit directives** ("mention X", "fix this") → make the edit.
- **Questions** ("are you sure this is right?", "why is this the case?") → answer substantively in the reply — an edit only happens if the answer shows the note is actually **wrong or incomplete** (then the agent corrects it and says so). When it's unclear whether the user wants the document changed, the agent answers and **offers** the edit ("want me to fold this into the note?") instead of making it.
- Mixed batches are handled per-comment; the final reply stays a short numbered list — one item per comment: the answer, what changed, or the offer.

This needs no structural machinery: an answer-only turn leaves the working tree clean, so **no proposal event fires and no card appears — by design**. The conversation stays in review mode, so a follow-up "yes, make that change" runs as a restricted review turn and produces the proposal then.

## Mobile

Review mode works on phones too (the ≤860 px breakpoint, where the dock is a full-page navigation instead of a side iframe). Everything downstream of the handoff — focus, compose, restricted review turn, proposal card, approve/discard — is identical; only the **transport** differs:

- **Handoff = same-origin localStorage.** There is no iframe to postMessage into, so the note page writes the payload JSON to the key `afs-review-pending` and navigates to the agent URL (which already carries `?repo=`). On load — after config + `?repo=` pre-focus — the agent app **consumes the key on read** (deleted before validation, so back/reload never replays it) and validates it: must have comments, a `ts` within **15 minutes**, and a `nonce` different from the last-consumed one (tracked in `afs-review-consumed`). Stale or malformed payloads are silently discarded. The decision logic is the pure `validatePendingHandoff` in `src/web/lib.js` (unit-tested).
- **Draft clearing is agent-side.** With no hub page listening for `afs-review-committed`, the agent app itself removes the hub's draft key `afs-review:<user>/<repo>/<path>` after a pushed commit (same origin, so the key is directly reachable). It does this on desktop too — harmless double-clear alongside the postMessage path.
- **Touch selection capture.** Dragging selection handles fires no `mouseup`, so comment mode also watches `selectionchange` (debounced ~300 ms, suppressed while a mouse button is down so desktop behavior is unchanged). On phones the note-input popover spans the width and flips **above** the selection when it would fall below the viewport fold — never off-screen.
- **Bottom sheet.** The comment rail becomes a fixed full-width bottom sheet (max-height ~45 vh, internal scroll, safe-area-inset-bottom padding, full-width handoff button).

**Accepted concurrent-handoff behavior:** a re-handoff mid-review starts a new conversation (on phones each handoff navigation reloads the agent app). The working tree on the sprite is shared across conversations, so last write wins — the proposal diff always shows the true tree state, and approve/discard act on that truth.

## The working-tree lifecycle (advisory sync, checkpoints, the banner)

Three requirements drive the design: a conversation must not **start** from a stale checkout; a dirty tree must never block or brick a turn; and the agent commits at **checkpoints**, not per edit.

**Advisory sync, never a gate.** At every turn boundary (chat, focus, voice mint) `syncWorkspaceRepo` fast-forwards a **clean** checkout (pull + freshness + prompt-context invalidation) and, on a **dirty** one, skips the pull and proceeds — never clobbers, never throws. Dirty ≠ stale: the dirt is in-progress work — a pending review proposal or the agent's own batched draft. On a non-review turn the model gets a WIP note in its context ("The working tree has uncommitted changes — likely your own in-progress work… build on them, and fold them into your next checkpoint commit…"); review turns need no note (the dirt *is* the proposal). Pull failures are logged, not thrown — a turn is never failed for freshness reasons, and the voice mint never 502s on dirt.

**Checkpoint commit cadence.** The write rules treat the working tree as the **draft space across the turns of a conversation**: related edits batch there, and the agent commits **and pushes** at natural checkpoints — a coherent unit of work is done, the user confirms or wraps up ("done", "looks good"), a KB switch is imminent, or leftover uncommitted work found at turn start has been handled. One meaningful commit per checkpoint (imperative, specific), never one per micro-edit, and never ending a "saved" conversation with uncommitted work. If a push is rejected because the remote moved: `git pull --rebase`, push again; on conflicts, stop and tell the user.

**The working-tree banner.** Because turns can now legitimately end dirty, the server emits a `tree` SSE event after **every non-review chat turn**: `{files:[{path,additions,deletions}], diff, ahead}` — `files`/`diff` describe uncommitted changes, `ahead` counts committed-but-unpushed commits (a checkpoint whose push failed; no-upstream → 0), and `{files: [], diff: "", ahead: 0}` is the all-clean dismissal signal. One persistent slim banner above the composer reflects the latest event — "Uncommitted changes — N files (+A −D)" (with "· N unpushed commit(s)" appended when both apply) with **View** (expand the diff), **Commit** (message input → the shared commit endpoint), and **Discard** (two-step confirm → the shared discard endpoint). A **clean-but-ahead** tree shows "N unpushed commit(s)" with a single **Push** action — the same commit endpoint, which on a clean-but-ahead tree just retries the push (including the rebase retry) — so a silently failed push is never invisible. While a pending proposal card is showing the same dirty state, the banner is suppressed (the pure `treeBannerDecision` in `src/web/lib.js` encodes show/dismiss/suppress and is unit-tested). The review commit/discard endpoints are thus **dual-use**: proposal approval and banner checkpointing share the same deterministic git paths.

**Rebase on retry.** The commit endpoint pushes, and if the remote moved it attempts exactly one `git pull --rebase` (aborted on conflict, leaving the tree and local commits intact) and one more push, reporting `{commit, pushed}` truthfully — a persistent failure keeps the card's/banner's retry semantics.

## Quote anchoring (not offsets)

Comments anchor to the **rendered text**, never to source byte offsets. Each comment stores `{quote, prefix, suffix, occurrence}`, where `quote` is the selected text normalized to collapsed whitespace, `prefix`/`suffix` are ~30 chars of surrounding context, and `occurrence` is which match of the quote within the article (0-based). This survives reformatting and re-rendering, and it deliberately matches against the article's **normalized text content** so a quote that spans inline formatting (`**bold**`, `` `code` ``, `[links](…)`) still resolves. On the agent side the quoted passage is located in the markdown source by the same logic.

Re-anchoring on page load recomputes each quote's range against the current article. A comment whose quote no longer matches is kept in the rail flagged **"text changed"** instead of being highlighted or silently dropped. Highlights use the [CSS Custom Highlight API](https://developer.mozilla.org/en-US/docs/Web/API/CSS_Custom_Highlight_API) where available, falling back to wrapping the matched text nodes in `<mark data-comment-id>`.

Draft comments are **client-only**: they persist in `localStorage` under `afs-review:<user>/<repo>/<path>` and never hit the server until the owner hands them off. Approving a commit clears that key — via the hub page's `afs-review-committed` handler on desktop, and directly by the agent app (same origin) on phones.

## The postMessage contract (desktop transport)

Both sides use `location.origin` as the `targetOrigin` and check `event.origin === location.origin` on receipt. (The pre-existing `afs-theme` message uses `"*"`; the review messages deliberately do **not** — they are strictly same-origin.)

- **Hub → agent** `{type:"afs-review-handoff", payload:{nonce, user, repo, path, head, ts, comments:[{id, quote, prefix, suffix, occurrence, note}]}}` — the same payload shape both transports use (`ts` is epoch ms; `user` is the namespace owner, needed for the agent-side draft clear and the mobile back-link). The Hub retries every 500 ms for up to 10 s until it receives an ack (then a visible error toast). The per-click `nonce` is how the agent tells those retry duplicates (same nonce → handle once) apart from a **deliberate re-handoff** of the same unchanged comments, e.g. after discarding a proposal (new nonce → handle again).
- **Agent → hub** `{type:"afs-review-ack"}` immediately on receipt (buffered in `index.html` so no handoff is missed before `app.js` loads); `{type:"afs-review-committed", commit:"<short-hash>", files:[...]}` — posted **only after the push actually lands on the hub**, which clears the draft and refreshes the note. A commit whose push failed does not emit it (the panel shows a Retry-push state instead), so the hub never reloads to stale content with the comments gone.

The composed handoff message numbers each comment with its quote; a comment whose `occurrence > 0` also carries a disambiguation hint (`(occurrence N; appears after "…prefix")`) so duplicate passages are unambiguous.

## The structural no-commit gate

The agent **cannot** commit during a review turn — the gate is structural, not a prompt request. `POST /api/chat` gains a per-request `review: true` flag. On a review turn the agent advertises and enforces a **restricted tool allowlist**: all read tools plus `write_file` / `edit_file` only. Excluded: `run_bash`, `git_commit`, `git_push`, `git_pull`, `create_repo`, `list_repos`, `focus_repo`, and the structured stock tools. The executor also refuses any excluded tool even if the model calls one that wasn't advertised (`REVIEW_TOOLS` in `src/agent/session.ts`), and the review-mode system prompt replaces the autonomous-commit write rules (`src/agent/prompt.ts`). So committing is impossible by construction; the owner is the only path to a commit.

Before a review turn the server attempts `git pull --ff-only` on the active checkout — but only when the tree is clean, so a pending proposal is never clobbered; failures are logged and non-fatal.

## The proposal event + endpoints

After a review turn, if the working tree is dirty the server emits an SSE `proposal` event before `done`:

```
event: proposal
data: {"files":[{"path","additions","deletions"}], "diff":"<full unified diff>", "suggestedMessage":"…"?}
```

A clean tree emits no proposal (this is also how an answer-only review turn ends — no card). The diff is coloured client-side by line prefix (a pure, unit-tested `renderDiff` helper in `src/web/lib.js`; HTML is escaped).

**Agent-authored commit messages (`Commit:` line contract):** the review prompt asks the model, whenever it edited files that turn, to end its reply with a final line exactly `Commit: <message>` — imperative mood, specific to the substantive change, ≤72 chars, never process-speak. The server extracts it (`extractCommitMessage` in `src/server/server.ts`; the **last** matching line wins, whitespace trimmed, absent → `null`) and includes it as `suggestedMessage` in the proposal payload. The card prefills the commit-message input with it, falling back to the canned path default — the input stays editable, so the user has final say. The line is not stripped from the streamed reply (it arrives as deltas); it's transparent and harmless in the bubble.

Two deterministic, no-LLM endpoints do the actual git, jailed to the active root:

- `POST /api/review/commit {message?, repo?}` — if the tree is dirty: `git add -A`, commit with the agent identity, push; returns `{commit, pushed}`. Default message `Resolve review comments` when none is given (the UI sends the path-specific `Resolve review comments in <path>`). **Idempotent/retryable:** a clean tree with commits stranded ahead of the upstream (a previous approve committed but the push failed) retries the push and returns the current HEAD short sha + push status; a clean, fully-pushed tree returns `{commit:null, pushed:false}`.
- `POST /api/review/discard {repo?}` — `git checkout -- .` + `git clean -fd` jailed to the active root; returns `{ok:true}`.

Both endpoints take the optional `repo` the proposal was made against (the client always sends it). In workspace mode the server returns **409** if the focused checkout is a different repo — so switching KBs in the panel between the proposal and the approve can never commit (or discard) the wrong repo's dirty tree.

Both routes must be added to the Hub's agent-proxy allowlist (`classifyAgentPath`, `internal/hub/agent_ui.go`) or the reverse proxy 404s them.

## Fleet-migration caveat

Like the rest of the hosted agent, **existing sprites do not auto-upgrade.** The new `agentsfs-chat` bundle (with the review flag, restricted toolset, proposal event, and the two endpoints) only reaches a sprite when it is reprovisioned — a running sprite keeps its old server until then. Shipping review mode to the fleet is therefore a deploy follow-up (rebuild the embedded bundle with `go run ./scripts/build_agent_bundle.go -source ../agentsfs-chat`, deploy the Hub, and reprovision or migrate existing sprites), not something `EnsureUser` grants to a healthy existing sprite on its own.

## Failed focus aborts the handoff

The handoff pre-focuses the payload's repo (`POST api/focus`). In workspace mode a failed focus **aborts the handoff with a visible error** in the panel instead of proceeding — otherwise the review turn would run against the workspace root, edits would land in the wrong place, and no proposal could be built (git operations fail on the non-repo root), which reads as a silent no-op. Verified live: this exact failure occurred in local dev when instance discovery couldn't see the workspace.

## Local development (no sprite)

Set `HUB_AGENT_DEV_URL=http://127.0.0.1:8787` on the Hub to enable the agent feature without Sprites/OpenAI config: `EnsureUser` short-circuits (no provisioning), and `/agent/*` API/preview traffic is reverse-proxied to a locally running `agentsfs-chat` with **no Sprites bearer attached** (same route allowlist and response hardening as production; the UI is still served from the embedded bundle, so rebuild it to see UI changes). Never set this on a deployed hub.

Run `agentsfs-chat` locally in workspace mode over hub clones, e.g.:

```sh
PORT=8787 AGENTSFS_MODE=workspace \
  AGENTSFS_WORKSPACE=$WS AGENTSFS_ROOT=$WS AGENTSFS_SEARCH_DIR=$WS \
  AGENTSFS_ALLOW_WRITES=1 AGENTSFS_ALLOW_SHELL=1 npm start
```

where `$WS` is a directory of `git clone`s from the local hub (so approve's push lands back in it). **`AGENTSFS_SEARCH_DIR` must point at the workspace dir** — instance discovery (and thus `focus_repo`/handoff pre-focus) resolves repos against it, and a stray value (e.g. from a local `.env`) makes every focus 400. The full loop was verified this way end-to-end on 2026-07-11: comment → handoff → focused review turn → proposal card → approve → real commit pushed into the local hub's bare repo.

One local-dev gotcha: `tsx` does not hot-reload — restart the `agentsfs-chat` process after any server/prompt change, and rebuild the bundle + restart the Hub for UI changes. A stale agent process silently runs the old prompt rules, which reads as "the model is ignoring the new instructions."
