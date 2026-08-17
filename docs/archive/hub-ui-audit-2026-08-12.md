# AgentsFS Hub and hosted Eve UX audit — 2026-08-12

> **Point-in-time delta audit.** This review compares the signed-in production
> product and the current Hub checkout with
> [hub-ui-audit-2026-08-09.md](hub-ui-audit-2026-08-09.md). Resolved findings
> are not repeated as new defects. Production observations and local changes
> are separated below. Nothing was deployed, committed, or pushed.

## Scope and method

The production pass used the real signed-in Chrome product and covered:

- the Hub dashboard and `seekinggradient/agentsfs` knowledge base;
- `AGENTS.md`, `CLAUDE.md`, and `agent-journal/INDEX.md`, including Files,
  Table, and Graph views, file-tree navigation, Context, and history/recovery
  surfaces;
- a new harmless agent conversation, its tool progress, a follow-up citation
  request, Chats history, an older cited thread, and the source-link safety
  confirmation;
- switching the audit thread from `agentsfs` to `boswell-v2`, observing the
  boundary behavior, and restoring that thread to `agentsfs`;
- desktop (1440×900), tablet landscape (1024×768), iPad portrait (768×1024),
  phone (390×844), and small phone (320×568) viewports;
- root and local overflow, touch-target size, focus and Escape behavior,
  keyboard navigation, loading and error states, accessible names,
  responsive navigation, and scanning/performance friction.

The production audit conversation was thread
`f1d712a8-d65d-4c8b-886a-1093c925950a`. It began with:

> Recurring UX audit, read-only: in one sentence, what is this knowledge base
> primarily for? Do not modify files.

The agent correctly summarized the repository as project memory for building
AgentsFS. A second read-only prompt asked for the supporting source and a
clickable citation. It read `INDEX.md` successfully, made no writes, and
answered with Markdown link syntax. The rendered UI showed
`INDEX.md [blocked]` rather than a citation or usable repository link. A
read-only trajectory inspection confirmed that the model's exact link target
was the safe relative path `INDEX.md`, so this is a rendering/resolution defect
rather than a failure to name a source.

Voice mode was not started. The live microphone path was unnecessary for these
checks and would have introduced device-permission and audio side effects. The
visible voice control was still measured at each viewport.

## Executive summary

Hub fundamentals remain strong. Every tested production Hub and Eve root stayed
contained at the required widths. The filename-first Files view works well at
320px, the Table keeps its wide content in a local scroller, Graph search and
keyboard focus/reset work, the mobile file drawer has reliable focus return,
Context remains reachable, and the styled 404 provides useful recovery.

The largest new experience problem is Chats: the drawer eagerly renders a very
large stream dominated by operational “Automatic gardening” sessions. In the
observed signed-in state it produced 2,935 DOM nodes, 483 buttons, and 158 pairs
of generic Rename/Delete controls; 62 rows were titled Automatic gardening and
38 were Untitled chat. This makes personal conversations hard to find and adds
avoidable render and accessibility-tree work every time Chats opens.

The knowledge-base/thread trust boundary remains the highest-risk functional
issue. Switching the completed audit thread to `boswell-v2` preserved its old
`agentsfs` messages and made Chats relabel the row as
`seekinggradient/boswell-v2`. The original `agentsfs` selection was restored.
An older deep-linked Boswell conversation also hydrated with the selector still
showing “Choose a knowledge base…”, so the UI did not disclose the thread's
actual source scope.

This run implements two safe Hub improvements locally: file-table filtering now
announces the visible result count through its existing live region, and the
mobile Commit history link now has a 44px target. The earlier August 9 Hub fixes
remain in the uncommitted worktree and were rechecked locally. All Hub and
repository tests pass.

## Hosted Eve findings

The following table contains new findings and persistent issues for which this
run produced materially sharper evidence. Earlier findings without new evidence
remain in the August 9 report.

| ID | Severity | 2026-08-12 evidence | Patch-ready recommendation |
| --- | --- | --- | --- |
| E-01 | P1 trust/data boundary | **Reproduced.** Switching the completed audit thread from `agentsfs` to `boswell-v2` left the original messages visible. Chats then described the row as `seekinggradient/boswell-v2`, although its answer and tools came from `agentsfs`. The selector was restored to `agentsfs`. | Bind `owner/repo` immutably when the first turn starts. If the user changes scope after a message exists, create a new thread or ask for explicit confirmation to start one. Never mutate the scope label of an existing conversation. |
| E-03 | P1/P2 touch | **Still present across the full matrix.** At 320px the knowledge-base select was 66×29, Conversations 83×29, New chat 84×27, voice and Send 34×34, and the composer 180×32. At 390px the select was 136×29 and composer 250×32. Desktop and tablet controls retained the same undersized heights. | Apply a shared coarse-pointer 44px target token to header actions, selector, composer, citations, sources, dialog controls, and drawer row actions. Keep the visible density with padding and pseudo-element hit areas where appropriate. |
| E-14 | P2 accessibility | **Reproduced with exact focus evidence.** The Streamdown link-safety confirmation mounted with no `dialog` or `alertdialog` role. Opening it left focus on the `domain.md` citation. Escape dismissed it but moved focus to `BODY`, not back to the citation. | Use a labelled modal dialog, move focus into it, contain focus while open, close on Escape, and restore the invoking citation on every close path. |
| E-17 | P1 source trust | **Cause isolated.** The model returned ``[`INDEX.md`](INDEX.md)`` after a successful read. Eve rendered `INDEX.md [blocked]`; no citation chip or source disclosure was created. The generic safe-link layer is treating a trustworthy repository-relative path as an unsafe external destination. | Resolve relative links against the thread's immutable owner/repository and revision before rendering. Prefer tool-derived structured citation events, and translate `INDEX.md` or `/workspace/brain/INDEX.md` into a revision-pinned Hub source. Only invoke the external-link warning for origins outside the trusted Hub/Eve pair. |
| E-19 | P1/P2 performance and discoverability | **New.** Opening Chats rendered 2,935 DOM nodes and 483 buttons. There were 158 Rename buttons, 158 Delete buttons, 62 Automatic gardening rows, and 38 Untitled chat rows. The collapsed drawer also leaves this large hidden subtree mounted. | Classify gardening/maintenance runs as system activity and exclude them from the default user conversation list. Add pagination or virtualization, search/date grouping, and a separate operational log. Mount drawer contents lazily and use one row-labelled overflow menu instead of two permanent generic controls. |
| E-20 | P1 scope orientation | **New.** A deep-linked older conversation containing a Boswell answer loaded while the knowledge-base selector still showed the disabled blank value “Choose a knowledge base…”. The conversation content therefore lacked an authoritative scope label. | Hydrate the selector from immutable thread metadata before showing conversation content. If legacy metadata is missing, show an explicit “Knowledge base unavailable/unknown” state rather than the new-chat placeholder. |
| E-21 | P2 loading/state clarity | **New.** The older deep-linked conversation initially presented the new-chat home state and hydrated roughly 2.8 seconds later. With the URL already naming a thread, the interim screen looks like an empty/new conversation rather than a loading state. | When `?t=` is present, render a conversation-loading skeleton or labelled status immediately. Do not mount the empty-home prompt until the thread lookup definitively returns no conversation. Virtualizing Chats should reduce the competing hydration cost. |

### Persistent Eve behavior rechecked

- The new audit prompt stayed visible through Thinking and Working until the
  final answer; the previously reported new-chat empty-home flash did not
  reproduce on this fresh submission.
- The completed thread still crossed knowledge-base boundaries incorrectly
  (E-01), but the selector disabled and re-enabled normally during the switch.
- At 320px the Chats sheet measured 294px, left a 26px content strip visible,
  kept focus on Conversations when opened, did not close on Escape, and returned
  focus to `BODY` when its Close control was used. These are the existing
  E-12/E-15 findings, not new defects.
- The knowledge-base `<select>` still has no direct accessible name or title
  (E-02). The desktop keyboard hint remains visible on touch layouts (E-10).
- Successful tool rows remain visually prominent after completion (E-07).
- No production knowledge-base or file was modified by the audit.

After restoring the audit thread to `agentsfs`, **New chat** returned the
workspace to its original blank “Choose a knowledge base…” selection. That
reset produced the empty route `8075b997-607d-4859-999f-1d36d882aef5`; no
message was sent in it.

## Hub findings and changes

| ID | Severity | 2026-08-12 evidence | Status after this run |
| --- | --- | --- | --- |
| H-15 | P2 responsive affordance | **Still not live.** Production note actions could retain `has-overflow-right` even when `clientWidth` and `scrollWidth` were equal after a viewport change. | The August 9 local resize refresh remains present and uncommitted; the local 320px note page had equal 240px widths with no stale overflow class. |
| H-16 | P2 touch | **Still not live.** Production Table Download links measured 48×12 at 320px. | The August 9 local fix remains present; local Chrome measured the visible Download link at 47.8×44. |
| H-18 | P1 accessibility | **New.** Filtering the production Table to `definitely-no-audit-match-20260812` showed a visual zero-result message and `0 files`, but the existing `role=status` live region remained empty. Screen-reader users received no result feedback. | **Fixed locally.** The live region now announces zero, singular, plural, and cleared-filter results. Local Chrome reported “No files match this filter.” for zero results and “1 file matches this filter.” for one result. |
| H-19 | P2 touch | **New.** The production Commit history link measured approximately 129×19 at 320px. | **Fixed locally.** The inline margin moved to a reusable class and its compact link uses `inline-flex` with a 44px minimum height. Local Chrome measured 128.7×44 with no root overflow. |

### Hub behavior that remained healthy

- Production Hub roots were contained at 1440×900, 1024×768, 768×1024,
  390×844, and 320×568. Wide Table content stayed within its 780px local
  scroller.
- Files remained readable and filename-first at 320px. Folder disclosures and
  file rows generally met the 44px touch target; `AGENTS.md`, `CLAUDE.md`, and
  `agent-journal/INDEX.md` opened successfully.
- Graph exposed a named `agentsfs wiki graph` application, named search,
  pressed filter states, and keyboard help. ArrowRight moved the active node,
  Enter focused it, and Escape restored the all-notes state. A no-match query
  produced visible results feedback without widening the page.
- The 320px file drawer measured 275px, moved focus to Close file list, closed
  on Escape, and returned focus to Show file list.
- Compact Context navigation remained visible and scrolled its in-flow region
  into view. The repository-aware 404 remained contained with two 44px recovery
  actions.
- The August 9 compact repository crumb, overflow-resize refresh, Table target,
  and authentication-label fixes remain in the worktree. This run did not
  overwrite or revert them.

## Viewport evidence

| Surface/control | 1440×900 | 1024×768 | 768×1024 | 390×844 | 320×568 |
| --- | --- | --- | --- | --- | --- |
| Production Hub root | contained | contained | contained | contained | contained |
| Hosted Eve root | contained | contained | contained | contained | contained |
| Eve KB select | 200×25 | 200×25 | 200×29 | 136×29 | 66×29 |
| Eve composer | 874×32 | 816×32 | 628×32 | 250×32 | 180×32 |
| Eve voice / Send | 34×34 | 34×34 | 34×34 | 34×34 | 34×34 |
| Hub Table | contained | contained | contained | local scroller | 780px local scroller |
| Hub Graph | contained | contained | contained | contained | 286×390 application |
| Local Commit history | — | — | — | — | 128.7×44; root contained |

The temporary viewport override was reset after testing. “Contained” means the
document's scroll width did not exceed its client width; intentionally wide
controls such as Table were checked inside their own scroller.

## Implemented Hub changes

1. Update `[data-file-table-status]` whenever filtering changes the visible
   row count, including explicit zero, singular, plural, and cleared states.
2. Replace the inline Commit history margin with `.repo-history-link` and give
   its compact link a 44px minimum target without changing its visual position.
3. Extend regression coverage for the live-region messages, template hook,
   and compact touch target.

No sibling `agentsfs-eve` code was edited. Hosted Eve findings are patch-ready
recommendations because that checkout is outside this automation's writable
scope.

## Verification

Completed successfully:

- `gofmt -w internal/hub/repo_table_test.go internal/hub/filetree_test.go`
- `git diff --check`
- `go test ./internal/hub`
- `go test ./...`
- signed-in production Chrome audit at all five required viewports;
- local Chrome verification at 320px of the zero-result live announcement,
  singular-result announcement, 44px Commit history and Download targets,
  compact repository crumb, note-action overflow state, and root containment.

The checkout began with the uncommitted August 9 Hub improvements and report.
Those changes were preserved. This run added `repo.html` and
`repo_table_test.go` changes, augmented the existing `app.js`, `style.css`, and
`filetree_test.go` changes, and added this report. `main` is one commit behind
`origin/main`; the remote-only MarkdownTo renderer refresh was not pulled or
modified. Everything remains uncommitted and undeployed as required.

## Recommended next sequence

1. Enforce immutable Eve thread/knowledge-base binding (E-01), and hydrate the
   selector from that binding on every deep link (E-20).
2. Separate automatic gardening from user Chats, virtualize/paginate the list,
   and lazily mount the drawer (E-19). This should also improve deep-link
   hydration (E-21).
3. Resolve safe repository-relative citations and emit tool-derived structured
   citation metadata (E-17).
4. Apply a shared coarse-pointer target system throughout Eve (E-03), then
   repair dialog and drawer focus, Escape, and focus return (E-14/E-15).
5. Deploy the accumulated, tested Hub fixes after review; none of them are live
   yet.
