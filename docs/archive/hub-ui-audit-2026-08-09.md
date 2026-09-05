# AgentsFS Hub and hosted Eve UX audit — 2026-08-09

> **Point-in-time delta audit.** This review compares the signed-in production
> product and the current Hub checkout with
> [hub-ui-audit-2026-08-06.md](hub-ui-audit-2026-08-06.md). Resolved findings
> are not repeated as new defects. The report records production behavior and
> local fixes separately; nothing in this run was deployed, committed, or
> pushed.

## Scope and method

The production pass used the real signed-in Chrome product and covered:

- the Hub dashboard and the `seekinggradient/agentsfs` repository;
- production notes `AGENTS.md`, `CLAUDE.md`, and `backlog.md`, plus the already
  open `fantasy26` draft guide and `seekinggradient-hq/2026-08-08.md` notes;
- file-tree navigation, note context/history, browser-route 404 recovery, and
  Files, Table, and Graph renderings;
- a new harmless Hub-agent conversation, a follow-up explicitly requesting a
  citation, conversation history, source expansion, and an older cited hosted
  Eve thread;
- switching the audit thread from `agentsfs` to `boswell-v2`, observing the
  boundary behavior, and restoring the thread to `agentsfs`; the overall agent
  workspace was then returned to its original blank “Choose a workspace”
  state with **New chat**;
- desktop (1440×900), tablet landscape (1024×768), iPad portrait (768×1024),
  phone (390×844), and small phone (320×568) viewports;
- root overflow, local scrollers, touch-target size, focus and Escape behavior,
  keyboard navigation, accessible names, loading and error states, responsive
  navigation, and scanning/performance friction.

The production conversation was thread
`32e288d2-abce-4747-949c-ef90551d18b0`:

> Audit check: in one sentence, what is this workspace for? Read only; do
> not modify files.

The agent answered that the workspace is project memory for building
AgentsFS, containing RFCs, design decisions, and cross-session state for the
CLI, Hub, and agent surfaces. A second prompt asked which source supported the
summary and requested a brief citation. The agent replied with the raw path
`/workspace/brain/INDEX.md`; it did not render a citation chip. The turn used
read-only `bash`, `glob`, and `grep` tools and made no workspace change.

Voice mode was not started because a live microphone session was unnecessary
for the scoped checks and would have introduced device-permission side effects.
The visible voice control and its responsive target were measured.

## Executive summary

The Hub remains structurally strong. All tested Hub and Eve pages stayed
contained at every required width; Files, Table, and Graph remained usable at
320px; Graph keyboard navigation and Escape reset worked; the mobile file
drawer still has excellent focus return; and a new agent turn no longer flashes
back to the empty “Ask the workspace” screen while waiting.

The most serious remaining issue is still Eve's workspace/thread boundary.
Switching an existing `agentsfs` conversation to `boswell-v2` left the old
messages visible and caused history to describe the thread as
`seekinggradient/boswell-v2`, retroactively misrepresenting the scope in which
the answer was produced. Source trust also regressed in the new Hub-agent turn:
an explicit citation request produced only a raw container path, while an older
direct Eve thread still had a proper source chip.

This run implements four safe Hub improvements: live-resize refresh for note
overflow cues, 44px mobile targets for file-table name/download links, a compact
repository breadcrumb on phone file pages, and programmatic names for sign-in
and sign-up inputs. All Hub tests and the full repository test suite pass, and
the responsive fixes were verified against a live local Hub in Chrome.

## Hosted Eve findings

The table includes open findings with fresh evidence or a materially sharper
recommendation. Items resolved in earlier reports are intentionally omitted.

| ID | Severity | 2026-08-09 evidence | Patch-ready recommendation |
| --- | --- | --- | --- |
| E-01 | P1 trust/data boundary | **Reproduced with stronger evidence.** Switching the completed audit thread from `agentsfs` to `boswell-v2` kept the prior messages onscreen. Chats then labeled the audit row `seekinggradient/boswell-v2`, even though the answer had been produced from `agentsfs`. | Pin `owner/repo` immutably when the first turn starts. On a selector change after any message exists, create a new thread in the new workspace (or require an explicit “Start new chat in …” confirmation). Never rewrite an existing thread's scope label. Disable Send until the selected scope and thread scope agree. |
| E-02 | P2 accessibility/context | The initial selector still exposed only the generic wrapper name “Focused workspace”; the `<select>` itself had no `aria-label` or title. | Give the select an explicit “Workspace” accessible name and announce successful scope changes through a polite live region. |
| E-03 | P1/P2 touch | **Reproduced at 320px.** Conversations measured 83×29, New chat 84×27, `domain.md` 77×25, source disclosure 79×22, voice 34×34, and Send 34×34. The composer textbox remained only 32px high. | Introduce one coarse-pointer target token (`min-inline-size/min-block-size: 44px`) across the header, citations, source rows, composer, dialogs, and history. Preserve visual density with internal padding rather than shrinking hit areas. |
| E-05 / E-09 | P2 performance/scanning | Chats still loads a long ungrouped stream. After an initial `Loading…` state, every row adds generic 27×27 Rename and Delete buttons; many legacy Untitled rows remain. | Virtualize or paginate the list, add search/date grouping, migrate safe legacy titles, and replace permanent row actions with one row-labeled overflow control such as “Actions for {title}”. |
| E-06 | P2 source friction | A `domain.md` citation still opens an external-link confirmation instead of an in-conversation rendered preview. The source disclosure then exposes a separate external link. | Add a revision-aware source drawer using the Hub renderer. Keep “Open in Hub” secondary and show the external warning only for an origin outside the trusted Hub/Eve pair. |
| E-07 | P2 hierarchy | Completed `bash`, `glob`, `grep`, and older `read_file` traces remain visually prominent above short answers. | Collapse successful work into a quiet expandable summary (“Read 1 source · 3 steps”); keep failures, approvals, and live progress expanded. |
| E-10 | P3 responsive copy | `Enter to send · Shift+Enter for a new line` remains visible at 390px and 320px touch layouts. | Hide desktop-only keyboard guidance under `(hover: none) and (pointer: coarse)` and use the reclaimed space for composer breathing room. |
| E-12 | P3 responsive drawer | The 320px Chats sheet is 294px wide, leaving a 26px strip of the conversation visible. The remainder does not communicate a deliberate backdrop interaction. | Below 480px use a full-width sheet, or make the remaining strip an inert, visually explicit backdrop that closes the sheet on activation. |
| E-14 | P2 accessibility | The external-link confirmation has neither `role="dialog"` nor `role="alertdialog"`. Opening it leaves focus on the citation; closing with Escape or Close leaves focus on `BODY`. | Use a labelled modal dialog, move focus into it, trap focus while mounted, close on Escape, and restore focus to the invoking citation on every close path. |
| E-15 | P2 accessibility | Opening Chats leaves focus on the Conversations trigger rather than moving into the sheet. Escape does not dismiss it, and Close leaves focus on `BODY`. | Treat Chats as a modal drawer on compact layouts: initial focus on Close or the heading, focus containment, Escape dismissal, background inertness, and deterministic return to Conversations. |
| E-17 | P1 source trust | **New.** The new Hub-agent answer did not render a citation even after an explicit request; it returned `/workspace/brain/INDEX.md`. The older direct Eve thread still rendered `domain.md` and a source disclosure. | Generate structured citations from successful read/search tool results instead of relying on model prose. Normalize `/workspace/brain/<path>` to a revision-pinned repo-relative Hub source, attach it to the answer event, and test both Hub-proxied and direct Eve routes. |
| E-18 | P2 accessibility/touch | **New.** The expanded source row's external link is named only `↗` and measures 21×22 at 320px. The adjacent source button is 279×26. | Name the link “Open domain.md in AgentsFS Hub”, merge it with the source row where possible, and give both controls a 44px minimum target. |

### Eve lifecycle and state observations

- The submitted audit prompt stayed visible through **Thinking**, **Working…**,
  **Responding**, and **Ready**. The empty-home flash reported in earlier runs
  did not reproduce.
- The first turn auto-titled in Chats within minutes.
- The workspace select was disabled during a successful switch and became
  usable again after roughly 1.2 seconds; no stuck loading state occurred.
- Selecting the disabled blank option programmatically could not clear a
  focused thread. **New chat** correctly returned the workspace to the blank
  selector state.
- The agent's answer was concise and the tools were read-only, but the missing
  structured citation makes the “grounded answers” promise unverifiable from
  that conversation.

## Hub findings and changes

| ID | Severity | 2026-08-09 evidence | Status after this run |
| --- | --- | --- | --- |
| H-09 | P2 orientation | **Still present in production.** At 390px and 320px the file-page masthead hides all breadcrumbs, so repository identity disappears while reading. | **Fixed locally.** Compact file pages now show the repository crumb only; owner and filename crumbs stay hidden, and long repository names ellipsize into available space. At 320px the live local page exposed the `brain` link without widening the masthead. |
| H-15 | P2 responsive affordance | **New.** Loading `AGENTS.md` at desktop and resizing to 320px produced a 225px note-action viewport over 335px of content, but the class remained `note-actions`; the right-edge fade/chevron appeared only after reloading at 320px. | **Fixed locally.** The existing debounced resize handler now refreshes horizontal-overflow cues alongside workspace sizing. Live local 1440→320 resizing produced `has-overflow-right` immediately (225px client / 261px scroll). |
| H-16 | P2 touch | **New.** Production Table at 320px kept the table in its own 780px scroller, but each row's Download link measured 48×12; file-name links were also below a comfortable touch height. | **Fixed locally.** Mobile name and Download links now have a 44px minimum block size. Local Chrome measured the first three name and Download pairs at 44px high while the page root remained contained. |
| H-17 | P1 accessibility | **New.** The sign-in page showed visual Username and Password text, but neither input had a programmatic name because the labels had no `for`/`id` association. Sign-up had the same defect for Username, Email, and Password. | **Fixed locally.** Stable IDs and `for` attributes now associate all five controls. Chrome's accessibility snapshot reports Username, Password, Email (optional), and Password (8+ characters) as their textbox names. |

### Hub regression checks that remained healthy

- Production Hub and hosted Eve roots stayed contained at 1440×900, 1024×768,
  768×1024, 390×844, and 320×568. Wide tables and graphs used local scrollers
  rather than widening the page.
- The 320px production Table showed “Scroll for more columns”; sorting and
  filtering controls remained named and at least 42–44px high.
- The 320px Graph application measured 271px wide inside the content column.
  Zoom out, Fit, and Zoom in were each 44×44; Files/Table/Graph tabs were 44px
  high. Folder overflow had a visible “Scroll folders →” cue.
- Graph exposes a named `agentsfs wiki graph` application, a named Search notes
  combobox, filter pressed states, and keyboard instructions. Arrow navigation
  plus Enter focused a node; Escape restored the all-notes state.
- The mobile file drawer measured 275px at 320px, moved focus to Close file
  list, closed on Escape, and returned focus to Show file list.
- The compact Context action remained 92×44 and scrolled the in-flow context
  region into view without root overflow.
- The styled repository-aware 404 remained contained and offered two 44px
  recovery actions.
- Production file PJAX navigation between `AGENTS.md`, `CLAUDE.md`, and
  `backlog.md` avoided a whole-page flash and preserved the repository file
  list state.

## Viewport evidence

| Surface | 1440×900 | 1024×768 | 768×1024 | 390×844 | 320×568 |
| --- | --- | --- | --- | --- | --- |
| Production Hub note root | contained | contained | contained | contained | contained |
| Hosted Eve conversation root | contained | contained | contained | contained | contained |
| Eve navigation mode | persistent rail | persistent rail | compact Chats | compact Chats | compact Chats |
| Eve composer textbox | 874×32 | 556×32 | 628×32 | 240×32 | 170×32 |
| Hub compact repository crumb | production full | production full | production hidden | local fix verified | local fix verified |
| Hub Table row actions | desktop inline | desktop inline | compact | production 12px high | local fix 44px high |
| Hub Graph | contained | contained | contained | contained | 271px app; 44px camera tools |

The viewport override was reset after testing. Values at compact widths exclude
Chrome's vertical scrollbar where applicable; containment means the document's
scroll width did not exceed its client width.

## Implemented Hub changes

1. Refresh `[data-overflow-cue]` state in the existing debounced window-resize
   animation frame, so orientation changes do not leave stale note-action cues.
2. Raise mobile repository Table file-name and Download links to a 44px minimum
   target.
3. Keep the repository breadcrumb visible on compact file pages while hiding
   the owner and filename crumbs that would overcrowd the masthead.
4. Associate login and signup labels with stable input IDs.
5. Add regression coverage for live-resize cue refresh, mobile Table targets,
   compact repository orientation, and authentication-field names.

No sibling `agentsfs-eve` code was edited. The Eve changes above are deliberately
patch-ready recommendations because that checkout was outside this run's safe
writable scope.

## Verification

Completed successfully:

- `gofmt -w internal/hub/filetree_test.go internal/hub/pjax_test.go internal/hub/ux_recovery_test.go`
- `git diff --check`
- `go test ./internal/hub`
- `go test ./...`
- signed-in production Chrome audit at all five required viewports;
- live local Chrome verification at 320px of the compact repository crumb,
  44px Table row actions, and root containment;
- live local 1440→320 resize verification of immediate note-action overflow
  state; and
- local Chrome accessibility snapshots for sign-in and sign-up fields.

The checkout started clean but was already one commit ahead of `origin/main`
because of unrelated commit `c90a19e` (Mermaid-rendering backlog notes). That
commit and its files were not modified. This audit's Hub changes and this report
remain uncommitted, as required by the automation.

## Recommended next sequence

1. Fix Eve's immutable thread/workspace boundary (E-01) and add an
   end-to-end regression that switches scope after a completed answer.
2. Make source attachment tool-derived and revision-aware (E-17), then ship an
   in-product preview (E-06).
3. Apply the shared 44px coarse-pointer token and accessible source-link naming
   (E-03/E-18), including voice and Send.
4. Repair modal/drawer focus, Escape, and focus return (E-14/E-15).
5. Paginate/search Chats and replace generic Rename/Delete pairs with a single
   row-specific action menu (E-05/E-09/E-12).
6. Hide desktop key hints on touch layouts and name the workspace selector
   directly (E-10/E-02).

