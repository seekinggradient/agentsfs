# agentsFS Hub and hosted Eve UX audit — 2026-08-03

> **Point-in-time audit.** This is a delta review against
> [hub-ui-audit-2026-07-27.md](hub-ui-audit-2026-07-27.md). It records the
> hosted product as observed on 2026-08-03 and the safe Hub-only improvements
> made in this checkout. It does not describe a deployed release. For current
> behavior, read [../how-the-hub-works.md](../how-the-hub-works.md).

## Scope and method

This pass used the signed-in production product in Chrome and exercised real
state and navigation instead of relying only on screenshots. It covered:

- the Hub dashboard, repository Files, Table, and Graph renderings;
- two production notes, the mobile file drawer, note context, file history,
  light/dark/automatic themes, and a deliberately missing note;
- one harmless hosted Eve conversation, its nine revision-pinned sources, a
  cited-source dialog, the Chats drawer, and focused-workspace switching;
- desktop (1728×907), tablet landscape (1024×768), iPad portrait (768×1024),
  phone (390×844), and small phone (320×568) viewports;
- page overflow, focus restoration, Escape behavior, touch-target size,
  accessible names, loading and error presentation, responsive navigation,
  and visible performance friction;
- the changed Hub locally at all five sizes, including an agent-enabled local
  rendering so owner-only note controls were not skipped.

The harmless production prompt was:

> In one sentence, what was worked on most recently in this workspace?

Eve answered that the workspace had most recently captured the 232-tweet
corpus, built its wiki and growth playbook, and opened two growth positions. It
cited `agent-journal/2026-07-27T091500Z-bootstrap-x-personal-brand.md` and eight
other sources at revision `c847179`. No write or commit was requested. The new
thread was still named **Untitled chat** after the answer completed.

The production workspace selector was temporarily changed from
`x-personal-brand` to `agentsfs` to test context behavior and then restored to
`x-personal-brand`. The production theme was cycled through automatic, light,
and dark and restored to automatic.

## Executive summary

The Hub has improved materially since the July 27 audit. The mobile note tree
is now a real drawer with correct state, focus, and Escape handling; long notes
and graphs stay within the page viewport at all five sizes; the responsive
reading layout is calm and legible; and the Files/Grid presentations are strong
mobile defaults.

The largest remaining product risk is still Eve's conversation/context model.
Changing the focused workspace leaves the previous workspace's answer
and citations onscreen without a boundary, while the selector itself is
unlabeled and becomes nearly unreadable at 320 pixels. The history and source
drawers also remain dense, action-noisy, and difficult to scan.

Six safe Hub improvements were implemented in this run:

1. Desktop and compact view preferences now use independent storage keys, so a
   desktop Table preference no longer strands a phone in the widest rendering.
2. Dashboard and repository tables show a visible mobile “Scroll for more
   columns →” instruction while remaining contained in local scrollers.
3. The Graph folder legend shows “Scroll folders →” only when it actually
   overflows, then removes the hint after the user starts scrolling.
4. The note-action row detects real overflow, fades the clipped edge, and adds
   a directional cue that disappears at the end of the row.
5. Mobile dashboard, repository, file-list, note-action, drawer, and context
   controls were brought to a 44-pixel target without widening the page.
6. Repository Settings and Talk-to-agent actions now use reusable classes and
   present as normal touch-safe actions instead of tiny metadata/inline styles.

## Findings: hosted Eve

These are intentionally numbered to match the July 27 audit. None of the Eve
findings below is duplicated from a resolved item; each was reproduced in the
current hosted build.

| ID | Severity | Finding and current evidence | Patch-ready recommendation |
| --- | --- | --- | --- |
| E-01 | P1 | **Conversation context remains visually inconsistent.** After switching from `x-personal-brand` to `agentsfs`, the complete `x-personal-brand` question, answer, and nine citations remained onscreen unchanged. The selector changed, but there was no context-change event or warning. Restoring the selector returned the header only; the conversation itself never established a visible boundary. | Pin a thread to its original workspace. On selector change, either insert an explicit “Workspace changed” event and make the next-turn scope unambiguous, or offer “Start a new chat in …” as the primary action. Do not silently reinterpret an existing thread. |
| E-02 | P1 | **The 320-pixel workspace selector is not confirmable.** It measured about 66×29 pixels and rendered only a fragment such as `x-p…`. At 390 pixels it was still only about 136 pixels wide. | Give the selector a labeled full-width second row below 480 pixels, or replace it with a 44-pixel trigger whose primary line is the full selected name and whose secondary line says “Workspace.” Preserve the full value in the accessible name/title. |
| E-03 | P1/P2 | **Touch targets remain systematically small.** At 1024 and 768 pixels, all 16 measured Eve controls were below 44 pixels in at least one dimension. At 390 pixels, 15 were undersized; at 320 pixels, 13 were. Examples: Conversations 29px high, New chat 27px, source chips about 26px, close 32px, rename/delete 27px, voice/send 34px, and the composer about 32px. | Introduce one shared `--control-min: 44px` token and apply it to header buttons, selector, drawer close/row actions, source chips, and composer controls. Use icon size and whitespace—not target size—to control density. |
| E-04 | P2 | **Expanded citations still dominate small screens.** Nine sources became a tall, irregular two-column/wrapped field at 320 pixels; paths wrap segment-by-segment, while the revision suffix can look detached from its path. | Keep the answer compact with a “9 sources” summary. Open sources in a full-width sheet/list with one-line filename, secondary repository/path, and a stable revision line. Allow filtering by source name. |
| E-05 | P2 | **Chat history is still a long unsearchable stream.** The loaded drawer contained many repeated **Untitled chat** rows. Every row exposed Rename and Delete controls, and the current completed thread also remained Untitled. | Auto-title after the first completed answer; add search and date grouping; move secondary actions behind a row menu; keep the active row clear. |
| E-06 | P2 | **The source dialog defaults to raw Markdown.** Opening the cited journal first showed a Loading state and then a monospaced raw source. There was no rendered preview and no “Open in Hub” path; close remained 32 pixels. | Default to rendered Markdown, retain a Raw toggle, add “Open in Hub,” expose repository/path/revision above the content, and make the close action 44×44. |
| E-07 | P2 | **Successful tool telemetry remains visually prominent.** A completed `retrieve query done` trace stayed above the answer and truncated at 320 pixels. It reads like internal status rather than durable evidence. | Collapse successful traces into a quiet “Searched this workspace” disclosure. Keep persistent/high-contrast tool UI for failures, permission requests, and user action. |
| E-08 | P2 accessibility | **The workspace combobox is still unnamed.** The native `<select>` returned no `aria-label`; its enclosing title does not provide a programmatic combobox name. | Add a visible `<label>` or `aria-label="Focused workspace"`. Include the current full workspace name in compact trigger variants. |
| E-09 | P2 accessibility | **History actions are still indistinguishable.** Rows repeat the generic names Rename and Delete, which makes screen-reader navigation noisy and ambiguous. | Name actions with their row, for example `Rename “Recent growth work”` and `Delete “Recent growth work”`. A single labeled overflow menu per row is even cleaner. |
| E-10 | P3 | **Desktop keyboard guidance is shown on touch layouts.** The “Enter to send · Shift+Enter…” helper is tiny and truncates near the bottom of the 320-pixel composer. | Hide desktop key hints on coarse pointers. Use the freed space for a brief mobile-relevant status only when necessary. |
| E-11 | P3 | **Wide desktop evidence density is still low.** The central conversation remains narrow on a 1728-pixel canvas even after nine sources are expanded, leaving a large unused field. | Preserve the readable prose measure, but promote citations/source details into a secondary evidence rail at wide breakpoints. |
| E-12 | P3 | **Mobile drawers leave a distracting page sliver.** The 320-pixel Chats drawer left roughly 15 pixels of the underlying conversation visible. | Use a true full-width sheet below 480 pixels, or make the residual area a deliberate noninteractive backdrop with no readable content. |
| E-13 | P2 | **Completed chats still remain Untitled.** The audit-created thread did not acquire a title after its answer completed. | Generate a short title from the first user turn/answer and allow immediate inline correction. |

## Findings: Hub

| ID | Severity | Finding and current evidence | Status after this run |
| --- | --- | --- | --- |
| H-01 | P1 | Long note content can widen a phone page. | **Remains resolved.** Production and local body/root widths matched 1728, 1024, 768, 390, and 320 pixels. |
| H-02 | P1 | Mobile file navigation was previously several screens below the note. | **Remains resolved.** The drawer starts closed, opens at 335px or less, moves focus to its labeled 44×44 close button, closes with Escape, and returns focus to Show file list. |
| H-03 | P1 | Persisted sidebars previously squeezed the iPad reader beyond its viewport. | **Remains resolved.** iPad portrait and tablet landscape produced no page-level overflow; note context moved below the article at compact widths. |
| H-04 | P1/P2 | Touch targets were inconsistent. Production still had 34–36px dashboard controls, a 13px Settings link, roughly 29px repository file links, and a 40px Comment action. | **Improved here.** Local phone dashboard controls, repository Settings/filter/file links, table row links/actions, note actions, drawer links, and context links now resolve to at least 44px. The 320px drawer file links and close button measured exactly 44px. |
| H-05 | P2 | Dashboard/repository tables are wider than phones and had no cue. Production measured 680px inside a 370px dashboard scroller and 780px inside a 358px repository scroller. | **Fixed here.** Compact preferences default independently from desktop, and both table surfaces display “Scroll for more columns →” without creating root overflow. Horizontal tables remain available when explicitly chosen. |
| H-06 | P2 | The Graph folder legend was scrollable but undiscoverable. Production measured about 809px of legend content inside 332px at 390px. | **Fixed here.** The local 90-note/32-link graph showed the cue only at 390 and 320 pixels (330/260px visible against 635px content), hid it at 768px and wider, and dismissed it after horizontal movement. |
| H-07 | P2 | Note actions could continue offscreen with no affordance. With owner-only Comment enabled, the local 320px row measured 278px inside 240px. | **Fixed here.** Real overflow adds a fade and directional arrow; all History, Download, Comment, and Edit targets are at least 44px. The cue updates as the user scrolls. |
| H-08 | P2 | Long-note context is difficult to discover because it follows the complete article. On the tested production note it began around 3,302px at 390px and 3,626px at 320px. | **Open.** Add a compact Context jump near the note state or a labeled bottom sheet. Keep the current in-flow context as the no-JavaScript fallback. |
| H-09 | P2 | Mobile masthead breadcrumbs disappear; the toolbar collapses path context to the file-list control. | **Open.** Retain a compact repository/file label, or let the path control expose the full breadcrumb on tap/focus without reducing the file-list target. |
| H-10 | P2 | Repository Settings looked like tiny metadata (about 13px high in production). | **Fixed here.** It is now an explicit classed action with a 44px compact target; the agent CTA also has a 44px minimum. |
| H-11 | P2/P3 | A desktop Table preference persisted onto mobile. | **Fixed here.** Dashboard and repository preferences now use separate `:compact` keys. A mobile user can still select and remember Table without overwriting the desktop choice. Explicit `?view=table` links remain honored. |
| H-12 | P2 accessibility | The old tree toggle hid its state. | **Remains resolved.** Show/Hide labels, `aria-expanded`, `aria-controls`, focus transfer, and Escape restoration all worked. |
| H-13 | P2 | A missing note renders only the browser-default text `404 page not found`. At 320px it has no Hub shell, recovery link, repository context, or suggested next action. | **Open.** Render a Hub-styled 404 for browser routes with “Back to workspace,” “Open file list,” and the attempted path. Keep raw/API 404s unchanged. |

## Loading, error, focus, and performance observations

- Eve exposed **Thinking** during the request and returned to **Ready** when the
  answer completed. The source and Chats drawers both showed Loading before
  their content appeared.
- The Hub graph exposed `aria-busy` while arranging nodes and cleared it after
  layout. A 90-note/32-link local graph settled without page overflow or a
  stuck loading layer.
- The production missing-note response is the weakest error state: it is
  accurate but unstyled and offers no recovery path.
- The mobile Hub file drawer has the strongest focus behavior in the product:
  open focuses Close file list; Escape closes; focus returns to Show file list.
- Eve's Chats stream is the most obvious performance/scanning friction. It
  loads a large ungrouped set of repeated rows, and every row pays the visual
  and accessibility cost of two always-visible actions.
- Hub PJAX note navigation remained visually stable and did not flash or widen
  the page. One early automated click closed the drawer without changing the
  route, but an immediate fresh-locator retry and subsequent file clicks worked;
  this was not reproducible and is not promoted to a finding.

## Behaviors that worked well

- Grid/Files are clear mobile renderings with strong content hierarchy.
- Hub Files/Table/Graph tabs have correct tab semantics; graph search is a named
  combobox and the SVG exposes keyboard instructions.
- Tables and graphs contain their own wide content instead of widening the page.
- Automatic, light, and dark Hub themes all retained coherent foreground and
  background contrast; automatic mode was restored after testing.
- Hub note typography remained readable from desktop through 320 pixels.
- File history, backlinks, download choices, and agent context remain available
  without competing with the reading plane on desktop.
- Eve revision-pins sources and labels the source viewer as a dialog.
- Eve's answer lifecycle and grounded source list completed successfully.

## Local verification and regression coverage

Chrome verification of the changed Hub produced:

| Surface | 1728 | 1024 | 768 | 390 | 320 |
| --- | ---: | ---: | ---: | ---: | ---: |
| Graph root/body width | 1728 | 1024 | 768 | 390 | 320 |
| Note root/body width | 1728 | 1024 | 768 | 390 | 320 |
| Graph overflow hint | hidden | hidden | hidden | shown | shown |
| File drawer target | — | — | — | 44px | 44px |

Additional checks:

- dashboard table: 680px content inside a 370px scroller, visible instruction,
  no 390px root overflow;
- repository table: 780px content inside a 358px scroller, visible instruction,
  no 390px root overflow;
- Files filter and sampled repository file links: 44px at 390px;
- note toolbar with Comment enabled: 278px content inside 240px at 320px,
  `has-overflow-right` set, visible fade/direction cue, 44px actions;
- Graph cue removed after horizontal scrolling and did not appear when the
  legend fit at iPad/tablet/desktop sizes;
- file drawer close and sampled file links: 44px at 320px; Escape restored focus.

Regression coverage now guards compact-vs-desktop preference keys, the table
instructions, dynamic overflow cue wiring, graph legend hint markup, and the
promoted repository actions. JavaScript syntax and the focused Hub tests are
run as part of this audit's verification.

## Recommended next sequence

1. Fix Eve's workspace/thread boundary (E-01) and label the selector
   (E-08) before adding new conversation features.
2. Rework the Eve selector and shared 44px target token together (E-02/E-03).
3. Consolidate citations, rendered source viewing, and successful tool traces
   into one evidence model (E-04/E-06/E-07).
4. Auto-title, search, group, and simplify Chats rows (E-05/E-09/E-13).
5. Add the Hub compact Context jump and styled browser-route 404 (H-08/H-13).

The Eve recommendations are patch-ready notes only: they belong to the sibling
hosted-Eve checkout and were not edited from this Hub repository.
