# agentsFS Hub and hosted Eve UX audit — 2026-08-06

> **Point-in-time delta audit.** This review compares the hosted product and
> current Hub checkout with
> [hub-ui-audit-2026-08-03.md](hub-ui-audit-2026-08-03.md). It records what was
> observed and changed on 2026-08-06; it does not describe a deployed release.

## Scope and method

The production pass used the signed-in Chrome product where the existing
session remained usable, plus already-loaded Hub pages after that session
expired. It covered:

- the Hub dashboard, two production notes, file context/history, the mobile
  file drawer, and repository Files, Table, and Graph renderings;
- a new harmless hosted Eve conversation, its completed tool trace and
  citation, the external-link confirmation, and the full Chats drawer;
- a focused-workspace switch from `myexpert-eve-development` toward
  `agentsfs`, including the failure state caused by an expired Hub session;
- desktop (1440×900), tablet landscape (1024×768), iPad portrait (768×1024),
  phone (390×844), and small phone (320×568) layouts;
- page overflow, touch targets, focus return, Escape behavior, accessible
  names, loading/error handling, responsive navigation, and scanning friction;
- the changed Hub locally at all five sizes, with Files, Table, Graph, a long
  Markdown note, compact context navigation, and browser-route 404 recovery.

The harmless hosted conversation was thread
`ed21c842-1179-4f4c-bbb4-304f66474801`:

> In one sentence, what is this workspace mainly about? Read only; do not
> modify any files.

Boswell answered that the workspace covers modern AI, agents, and agent
memory across the technical, economic, security, evaluation, infrastructure,
and protocol layers, citing `domain.md`. The run used `bash` and `read_file`
and made no workspace change. Unlike the August 3 run, the completed
thread acquired a useful first-turn title.

### Production-session constraint

The Hub agent initially showed `myexpert-eve-development`. Selecting `agentsfs`
left the prior conversation onscreen and disabled the selector, but never
completed or showed an error. A reload then reached the Hub sign-in page,
confirming that the existing session had expired. No successful server-side
selection change was observed, so there was no confirmed changed selection to
restore; the original value is recorded here for follow-up. The audit did not
enter credentials or try to bypass the expired session. Already-loaded Hub
dashboard/note pages remained available for read-only responsive inspection.

## Executive summary

The Hub remains stable and well contained across the required sizes. Files,
Table, and Graph do not widen the page; the mobile file drawer still has the
best focus/Escape behavior in the product; and long-form reading remains calm
at 320 pixels. This run closes the two Hub follow-ups left at the end of the
August 3 report: long notes now expose a compact Context jump, and mistyped or
moved browser routes receive a branded 404 with recovery links. It also closes
a newly observed compact Graph target gap for the Labels and Fit controls.

Hosted Eve's first-turn lifecycle improved: the audit prompt stayed visible
while the agent worked, the answer arrived without flashing back to the empty
“Ask the workspace” state, and the thread auto-titled. The remaining
roughness is concentrated in context boundaries, small controls, dense history,
and dialog/drawer focus. Two newly verified accessibility failures are that
Escape does not close Chats and both Chats and citation confirmation return
focus to the document body instead of the invoking control.

## Hosted Eve findings

This table reports only changed status or fresh evidence. Unlisted open items
from August 3 remain recommendations but were not duplicated without new
evidence.

| ID | Severity | 2026-08-06 evidence | Recommendation / status |
| --- | --- | --- | --- |
| E-01 | P1 | **Still reproduced.** Changing the selector from `myexpert-eve-development` toward `agentsfs` left the previous workspace conversation visible without a boundary or reset. | Pin a thread to its original workspace. Make “Start a new chat in …” the primary switch outcome, or insert an explicit context-change event before another turn can be sent. |
| E-03 | P1/P2 | **Still reproduced.** At 320px the completed direct chat measured Conversations 83×29, New chat 84×27, the `domain.md` citation 77×25, and voice/send 34×34. | Apply a shared 44px minimum target to header, citation, composer, dialog, and history controls. |
| E-05 / E-09 | P2 | **Still reproduced.** Chats loaded a very long, ungrouped stream with many Untitled rows and repeated generic `Rename` and `Delete` accessible names. | Add search/date grouping, auto-title migration for old rows, and one row-labeled overflow menu instead of two permanently exposed generic actions. |
| E-06 | P2 | **Behavior changed, friction remains.** The `domain.md` citation now opened an “Open external link?” confirmation pointing to the Hub instead of a raw-source viewer. This is safer than silent navigation, but it adds a modal step and still provides no rendered source preview in the conversation. | Offer a rendered, revision-aware in-product preview with a secondary “Open in Hub” action. Preserve the external-link warning only when the destination is truly outside the trusted product origin. |
| E-07 | P2 | **Still reproduced.** Completed `bash` and `read_file` rows remained above a one-sentence answer after both tools were done. | Collapse successful tool activity into a quiet, expandable “Read 1 source” summary; keep failures and permission prompts prominent. |
| E-10 | P3 | **Still reproduced.** `Enter to send · Shift+Enter for a new line` remained visible at the 320px touch layout. | Hide desktop keyboard guidance for coarse pointers and reclaim the vertical space. |
| E-12 | P3 | **Still reproduced.** At 320px the Chats drawer was 294px wide and left a 26px strip of the underlying conversation visible. | Use a full-width sheet below 480px, or make the residual area an intentional inert backdrop. |
| E-13 | Resolved | **Improved.** The audit-created conversation was titled from its first prompt within minutes; it did not remain Untitled. | Keep the auto-title behavior and apply it to the existing Untitled backlog where safe. |
| E-14 | P2 accessibility | **New: citation confirmation loses focus.** Escape closed the confirmation, but `document.activeElement` became `BODY` instead of returning to the `domain.md` citation button. | Save the invoking element before opening, trap focus inside the dialog, close on Escape, and restore focus to the trigger after unmount. |
| E-15 | P2 accessibility | **New: Chats lacks keyboard dismissal and focus restoration.** Pressing Escape while the Close button was focused did not close the drawer. Clicking Close removed it but left focus on `BODY`. | Treat Chats as a modal drawer: close on Escape, keep focus within it while open, and return focus to Conversations after every close path. |
| E-16 | P2 reliability | **New: an expired session can strand the workspace selector.** The selector remained disabled with no visible failure; only reload revealed sign-in. | Detect 401s and unexpected HTML/redirect responses, re-enable the selector, preserve its last confirmed value, and show a clear “Session expired — sign in to continue” action. |

### Eve items not reclassified in this run

- E-02 and E-08 (compact selector width and programmatic name) remain open;
  the select still had no `aria-label` before the session expired.
- E-04 and E-11 (large source sets and wide-desktop evidence density) were not
  re-exercised with a fresh multi-source answer, so the August 3 evidence stands.

## Hub findings and changes

| ID | Severity | 2026-08-06 evidence | Status after this run |
| --- | --- | --- | --- |
| H-01 / H-03 | P1 | Production and local note/repository roots stayed equal to the viewport at 1440, 1024, 768, 390, and 320px. Wide table and graph internals remained contained. | **Remain resolved.** |
| H-02 / H-12 | P1 / P2 a11y | At 320px, Show file list opened the labeled drawer, focus moved to Close file list, Escape closed it, and focus returned to Show file list. | **Remain resolved and are still the interaction-quality benchmark.** |
| H-04 / H-07 | P1/P2 | The loaded production note still showed an Edit action ending beyond the 320px viewport, while root width stayed contained. The August 3 touch/overflow fix is present in the current worktree but is not yet reflected by that loaded production asset. | **Previously staged; no duplicate implementation.** Deploy the accumulated Hub work after normal review. |
| H-05 / H-06 / H-11 | P2 | Local Table remained in its own scroller with a compact hint; Files and compact view preferences stayed independent; Graph remained contained at every required width. | **Previously fixed; regression checks pass.** |
| H-08 | P2 | A long 320/390px note now shows a 92×44 compact `Context ↓` action beside note state. Activating it sets `#note-context` and brings the context region onscreen while keeping the in-flow no-JavaScript fallback. | **Fixed here.** |
| H-09 | P2 | Compact masthead breadcrumbs still collapse, leaving only file-list and truncated path context in the sticky toolbar. | **Open.** Add a compact, focusable owner/repository label without competing with the 44px file-list control. |
| H-13 | P2 | Missing browser routes now render the Hub shell, a clear explanation, and 44px recovery actions. Known-repository misses include `Open ux-audit`; unknown repositories include `Back to workspaces`. Raw/API-style misses retain terse responses. | **Fixed here.** |
| H-14 | P2 touch | **Newly found and fixed.** At 390px the compact Graph `Labels` control was 32×44 and `Fit` was 41×44 despite surrounding tools being 44×44. | Both now measure 44×44 at 390 and 320px; filter buttons remain wider. |

## Loading, error, focus, and performance observations

- The new hosted conversation kept the submitted message visible throughout
  **Thinking / Working…** and returned to **Ready** after the answer. The empty
  home state did not flash between those phases.
- Hub Graph exposed a named application, search combobox, filter controls, and
  keyboard instructions. The three-note local graph settled without a stuck
  loading layer or root overflow.
- The expired-session selector failure is silent and unrecoverable in place;
  this is the weakest loading/error transition observed in the current run.
- The citation confirmation and Chats drawer both remove focus from the user's
  workflow on close. Hub's file drawer correctly restores it.
- Chats remains the largest scanning/performance friction: a large history is
  loaded as one stream and every row carries two extra controls.
- The branded Hub 404 is intentionally not used for raw downloads or API-like
  clients, preserving compact machine-readable failure behavior.

## Behaviors that worked well

- Hub Files/Table/Graph tabs retain correct tab semantics and do not widen the
  root page at any required viewport.
- Tables use local horizontal scrolling; Graph tools wrap cleanly and now keep
  44×44 compact targets.
- Hub note typography, internal Markdown tables, file history, backlinks, and
  contextual agent entry remain readable and discoverable.
- Hub mobile file navigation closes on Escape and restores focus correctly.
- Hosted Eve kept the first submitted message visible, completed the read-only
  request, cited its source, and auto-titled the conversation.
- The direct chat page itself had no page-level overflow at 390 or 320px.

## Implemented Hub changes

1. Added an in-flow, compact-only `Context ↓` link and stable `#note-context`
   target to long note pages.
2. Added a Hub-styled browser 404 with clear status, recovery copy, repository
   breadcrumbs/actions when known, and a dashboard fallback when not.
3. Kept terse 404s for non-GET/HEAD and JSON/API-style callers.
4. Raised compact Graph Labels and camera controls to 44×44.
5. Added regression tests for context wiring, browser 404 status/content, and
   compact Graph target rules.

These changes are additive to the already-dirty August 3 Hub improvements and
HTML preview work. No unrelated worktree changes were reverted or rewritten.

## Local verification

| Surface | 1440×900 | 1024×768 | 768×1024 | 390×844 | 320×568 |
| --- | ---: | ---: | ---: | ---: | ---: |
| Repository root width | 1440 | 1024 | 768 | 390 | 320 |
| Graph root width | 1440 | 1024 | 768 | 390 | 320 |
| Graph app width | contained | contained | 726 | 356 | 286 |
| Compact Graph tools | — | — | — | ≥44×44 | ≥44×44 |
| Note Context jump | hidden | hidden | hidden | 92×44 | 92×44 |
| Browser 404 root width | contained | contained | contained | contained | 320 |

Validation completed successfully:

- `git diff --check`
- focused UX/HTML-render regression tests
- `go test ./internal/hub`
- `go test ./...`
- live local Chrome verification of Files, Table, Graph, a long note, Context
  navigation, and both repository-aware and unknown-repository 404s.

## Recommended next sequence

1. Fix Eve's workspace/thread boundary and expired-session recovery
   together (E-01/E-16); both currently undermine trust in the selected scope.
2. Establish the shared 44px Eve control token and compact selector treatment
   (E-02/E-03/E-08).
3. Repair dialog/drawer keyboard behavior before expanding those surfaces
   further (E-14/E-15).
4. Consolidate citation preview and completed tool telemetry into a calmer
   evidence model (E-06/E-07).
5. Search/group/simplify Chats and migrate old Untitled rows (E-05/E-09/E-12).
6. Add a compact Hub owner/repository orientation control (H-09).

Hosted Eve recommendations remain patch-ready notes only; the sibling Eve
checkout was not edited in this run.
