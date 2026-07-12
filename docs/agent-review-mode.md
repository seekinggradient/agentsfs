---
description: Agent review mode — leave inline comments on a rendered note, hand them to your agent, and approve the diff it proposes before it commits.
---

# Agent review mode

Review mode is agentic co-editing on a rendered markdown note. The owner highlights passages in the note, attaches comments, and hands them to the [hosted agent](hosted-agent.md); the agent resolves each comment by editing the file(s) **without committing**, and the panel shows a **proposal** — a unified diff plus an editable commit message. The owner approves (commit + push, done server-side) or discards. It closes the loop from "this paragraph is wrong" to a reviewed commit without leaving the note.

It spans two repos: the Hub (`agentsfs`, Go) renders the note and owns the comment UI; the agent app (`agentsfs-chat`, TS) runs on the per-user sprite and does the editing. They talk over `window.postMessage` because the agent is same-origin, reverse-proxied at `/agent/*`.

## The UX flow

1. On a rendered markdown note the owner sees **Comment for agent** in the toolbar (next to **Edit**). It appears only when the viewer can write, the file is markdown, and the agent is enabled (`{{if and .CanWrite .IsMarkdown .AgentURL}}` in `internal/hub/assets/file.html`).
2. Clicking it enters comment mode. Selecting text in the `.prose` article pops an inline note input; several comments can be attached. Each renders as a highlight in the article plus an entry in a bottom-right **comment rail** (edit / delete / Clear all).
3. **Handoff to your agent (N)** opens the agent side dock and posts the comments into it.
4. The agent runs a **restricted review turn** (below), edits the file(s), and the panel renders a **proposal card**: the diff, per-file `+/–` counts, and a commit-message input defaulting to `Resolve review comments in <path>`.
5. **Approve & commit** commits + pushes deterministically on the sprite; the Hub note refreshes to the new commit. **Discard** reverts the working tree. The owner can iterate — typed follow-up feedback stays a review turn (each proposal replaces the previous card), or leave fresh comments after committing.

Desktop-only in v1: at the phone breakpoint (`max-width: 860px`) the dock becomes full-page navigation, so the comment/handoff affordances are hidden.

## Quote anchoring (not offsets)

Comments anchor to the **rendered text**, never to source byte offsets. Each comment stores `{quote, prefix, suffix, occurrence}`, where `quote` is the selected text normalized to collapsed whitespace, `prefix`/`suffix` are ~30 chars of surrounding context, and `occurrence` is which match of the quote within the article (0-based). This survives reformatting and re-rendering, and it deliberately matches against the article's **normalized text content** so a quote that spans inline formatting (`**bold**`, `` `code` ``, `[links](…)`) still resolves. On the agent side the quoted passage is located in the markdown source by the same logic.

Re-anchoring on page load recomputes each quote's range against the current article. A comment whose quote no longer matches is kept in the rail flagged **"text changed"** instead of being highlighted or silently dropped. Highlights use the [CSS Custom Highlight API](https://developer.mozilla.org/en-US/docs/Web/API/CSS_Custom_Highlight_API) where available, falling back to wrapping the matched text nodes in `<mark data-comment-id>`.

Draft comments are **client-only**: they persist in `localStorage` under `afs-review:<user>/<repo>/<path>` and never hit the server until the owner hands them off. Approving a commit clears that key.

## The postMessage contract

Both sides use `location.origin` as the `targetOrigin` and check `event.origin === location.origin` on receipt. (The pre-existing `afs-theme` message uses `"*"`; the review messages deliberately do **not** — they are strictly same-origin.)

- **Hub → agent** `{type:"afs-review-handoff", payload:{nonce, repo, path, head, comments:[{id, quote, prefix, suffix, occurrence, note}]}}`. The Hub retries every 500 ms for up to 10 s until it receives an ack (then a visible error toast). The per-click `nonce` is how the agent tells those retry duplicates (same nonce → handle once) apart from a **deliberate re-handoff** of the same unchanged comments, e.g. after discarding a proposal (new nonce → handle again).
- **Agent → hub** `{type:"afs-review-ack"}` immediately on receipt (buffered in `index.html` so no handoff is missed before `app.js` loads); `{type:"afs-review-committed", commit:"<short-hash>", files:[...]}` — posted **only after the push actually lands on the hub**, which clears the draft and refreshes the note. A commit whose push failed does not emit it (the panel shows a Retry-push state instead), so the hub never reloads to stale content with the comments gone.

The composed handoff message numbers each comment with its quote; a comment whose `occurrence > 0` also carries a disambiguation hint (`(occurrence N; appears after "…prefix")`) so duplicate passages are unambiguous.

## The structural no-commit gate

The agent **cannot** commit during a review turn — the gate is structural, not a prompt request. `POST /api/chat` gains a per-request `review: true` flag. On a review turn the agent advertises and enforces a **restricted tool allowlist**: all read tools plus `write_file` / `edit_file` only. Excluded: `run_bash`, `git_commit`, `git_push`, `git_pull`, `create_repo`, `list_repos`, `focus_repo`, and the structured stock tools. The executor also refuses any excluded tool even if the model calls one that wasn't advertised (`REVIEW_TOOLS` in `src/agent/session.ts`), and the review-mode system prompt replaces the autonomous-commit write rules (`src/agent/prompt.ts`). So committing is impossible by construction; the owner is the only path to a commit.

Before a review turn the server attempts `git pull --ff-only` on the active checkout — but only when the tree is clean, so a pending proposal is never clobbered; failures are logged and non-fatal.

## The proposal event + endpoints

After a review turn, if the working tree is dirty the server emits an SSE `proposal` event before `done`:

```
event: proposal
data: {"files":[{"path","additions","deletions"}], "diff":"<full unified diff>"}
```

A clean tree emits no proposal. The diff is coloured client-side by line prefix (a pure, unit-tested `renderDiff` helper in `src/web/lib.js`; HTML is escaped).

Two deterministic, no-LLM endpoints do the actual git, jailed to the active root:

- `POST /api/review/commit {message?, repo?}` — if the tree is dirty: `git add -A`, commit with the agent identity, push; returns `{commit, pushed}`. Default message `Resolve review comments` when none is given (the UI sends the path-specific `Resolve review comments in <path>`). **Idempotent/retryable:** a clean tree with commits stranded ahead of the upstream (a previous approve committed but the push failed) retries the push and returns the current HEAD short sha + push status; a clean, fully-pushed tree returns `{commit:null, pushed:false}`.
- `POST /api/review/discard {repo?}` — `git checkout -- .` + `git clean -fd` jailed to the active root; returns `{ok:true}`.

Both endpoints take the optional `repo` the proposal was made against (the client always sends it). In workspace mode the server returns **409** if the focused checkout is a different repo — so switching KBs in the panel between the proposal and the approve can never commit (or discard) the wrong repo's dirty tree.

Both routes must be added to the Hub's agent-proxy allowlist (`classifyAgentPath`, `internal/hub/agent_ui.go`) or the reverse proxy 404s them.

## Fleet-migration caveat

Like the rest of the hosted agent, **existing sprites do not auto-upgrade.** The new `agentsfs-chat` bundle (with the review flag, restricted toolset, proposal event, and the two endpoints) only reaches a sprite when it is reprovisioned — a running sprite keeps its old server until then. Shipping review mode to the fleet is therefore a deploy follow-up (rebuild the embedded bundle with `go run ./scripts/build_agent_bundle.go -source ../agentsfs-chat`, deploy the Hub, and reprovision or migrate existing sprites), not something `EnsureUser` grants to a healthy existing sprite on its own.
