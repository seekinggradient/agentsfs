# agentsFS Hub UI audit — 2026-07-27

> **Archived audit.** A point-in-time review of the Hub web interface. Its findings drove
> changes that have since landed, so the screens it describes no longer match production.
> For how the Hub works today, read [../how-the-hub-works.md](../how-the-hub-works.md).

## Scope and method

This audit covered the production Hub and hosted Eve agent in Chrome, plus the
patched Hub locally. The pass included real navigation and state changes, not
only screenshots:

- opened an existing knowledge base and several Markdown files;
- moved between repository home, Files, Table, and Graph renderings;
- searched and manipulated the graph;
- started a new Eve conversation and waited through the full streamed answer;
- expanded all eight citations and opened a cited source;
- opened the chat-history drawer;
- changed the focused knowledge base;
- tested light/dark production presentation where available;
- tested 1728×907 desktop, 1024×768 tablet landscape, 768×1024 iPad portrait,
  390×844 phone, and 320×568 small-phone layouts;
- repeated the file-reader checks against the patched local Hub.

The production conversation asked, “In one sentence, what is the main purpose
of this knowledge base?” It returned a grounded answer with eight
revision-pinned sources. The new thread remained named “Untitled chat.”

## Summary

The underlying information architecture is sound: Hub file navigation preserves
the selected file, the agent streams status honestly, citations are pinned to a
revision, and the narrow agent layout does not create page-level horizontal
overflow. The roughest edges are concentrated in responsive navigation,
touch-target sizing, and the relationship between an active conversation and a
newly selected knowledge base.

Four Hub improvements were implemented during this audit:

1. The mobile file tree is now an off-canvas navigation drawer instead of a
   section several screens below the note.
2. Long bare links in Markdown can no longer widen the phone viewport.
3. Tablet sidebar resizing preserves a useful reading width.
4. Primary Hub controls now meet a 44-pixel mobile target at narrow widths.

## Findings

### P0/P1 — breaks or seriously obscures the primary task

| ID | Area | Finding and observed effect | Status |
| --- | --- | --- | --- |
| H-01 | Hub file reader | Long URLs widened narrow pages: a linked URL originally expanded a 390-pixel page to 533 pixels, and production smoke testing then exposed the same problem for plain-text URLs inside list items. | **Fixed here:** wrapping is inherited by all file-view prose, including linked and plain-text URLs. |
| H-02 | Hub file reader | On mobile, “Toggle file list” did not expose navigation near the reader. The tree was laid out roughly 3,400 pixels below the current note; clicking the control could only hide that distant section. | **Fixed here:** the tree starts closed and opens as a dismissible, scrim-backed drawer. It closes on outside click, Escape, close button, and file navigation. |
| H-03 | Hub file reader | At 768×1024, a persisted 341-pixel sidebar left only about 420 pixels for reading and produced content wider than the viewport. The path/actions also became hard to reach. | **Fixed here:** compact layouts cap the tree at 280 pixels and preserve a 520-pixel minimum reading column. Local verification produced a 190-pixel tree, 571-pixel reader, and no body overflow. |
| E-01 | Eve context | Changing the focused knowledge base from `x-personal-brand` to `agentsfs` left the old answer and its `x-personal-brand` citations onscreen with no boundary or explanation. The next-turn context changes, but the visual conversation does not. This is an easy way to ask a follow-up against the wrong source set. | Eve follow-up: insert a visible context-change event or require “start new chat with this knowledge base.” Keep the current thread’s focus visibly pinned. |
| E-02 | Eve mobile | At 320 pixels, the knowledge-base selector collapses to a fragment (“age…”), so the user cannot confirm the selected source before sending. | Eve follow-up: give the selector a full-width second row or a compact labeled trigger with an ellipsized value and accessible full name. |

### P1/P2 — materially slows, confuses, or degrades the experience

| ID | Area | Finding and observed effect | Status |
| --- | --- | --- | --- |
| H-04 | Hub touch UI | Touch targets were inconsistent: the file-tree toggle was 28 pixels; repo tabs about 26; clone copy about 21; graph controls 32–38; dashboard controls 34–38. These are error-prone on phones and iPads. | **Improved here:** file actions, global mobile masthead controls, repo view tabs/download/copy/sort, graph search/actions/legend items, and coarse-pointer graph controls are at least 44 pixels. |
| E-03 | Eve touch UI | Header buttons/select measured 27–29 pixels; source/chat close 32; rename/delete 27; send/mic 34. | Eve follow-up: adopt one 44-pixel interactive-control token and audit all drawers, header actions, and composer buttons. |
| E-04 | Eve citations | Citation chips wrap individual path segments into tall, centered blocks at 390 and 320 pixels. Revision text can float beside or visually overlap the wrapped path, and eight expanded sources dominate multiple screens. | Eve follow-up: render a one-line filename plus secondary repository/path line, truncate both predictably, and open an accessible source list/sheet rather than expanding every chip inline. |
| E-05 | Eve history | The Chats drawer is a long, unsearchable stream with many repeated “Untitled chat” rows. Every row exposes rename/delete icons, adding noise and identical screen-reader labels. | Eve follow-up: auto-title completed first turns; add search and date grouping; move secondary actions into a row menu; include the chat title in action labels. |
| E-06 | Eve source viewer | The source drawer presents raw monospaced Markdown rather than a rendered preview. It has no “Open in Hub” action, and the 32-pixel close control is undersized. | Eve follow-up: default to rendered Markdown, allow a Raw toggle, add “Open in Hub,” and enlarge the close control. |
| E-07 | Eve tool status | A completed internal trace such as `retrieve query done` remains prominent after the answer finishes and truncates on mobile. It reads like implementation telemetry rather than useful provenance. | Eve follow-up: collapse successful tool traces into a subdued “Searched this knowledge base” disclosure; reserve persistent emphasis for failures or action required. |
| H-05 | Hub mobile table | The repository Table rendering contains a roughly 780-pixel table inside a 358-pixel scroller. Only repository and last-updated information are initially visible, while Notes, Access, and Actions appear clipped with no horizontal-scroll cue. | Hub follow-up: default to Grid below the tablet breakpoint, or transform table rows into labeled mobile records. If horizontal scrolling remains, add a visible edge fade and instruction. |
| H-06 | Hub graph | The mobile folder legend extends horizontally beyond the visible graph. It is scrollable, but hidden scrollbars and no edge affordance make additional filters undiscoverable. | Hub follow-up: wrap legend filters or add a fade/“more” treatment. |
| H-07 | Hub note actions | The mobile note toolbar scrolls horizontally without an affordance. “Comment for agent” shortens to “Comment,” and later actions can look absent. | Hub follow-up: prioritize two primary actions and place the rest in a labeled overflow menu. |
| H-08 | Hub note context | On long notes, backlinks/context appear only after the entire article. They are technically present but difficult to discover or revisit. | Hub follow-up: add a small in-note “Context” jump or a collapsible bottom sheet on narrow screens. |
| H-09 | Hub mobile location | Header breadcrumbs disappear entirely on mobile. After deep navigation, there is no persistent user/repository/location cue outside the page content. | Hub follow-up: retain a compact repository label or make the note-path control expose the full breadcrumb. |
| H-10 | Hub repository | The Settings link in the metadata eyebrow is tiny and low contrast. It looks like supporting text rather than an actionable destination. | Hub follow-up: promote it to a normal 44-pixel action in the repo header/overflow menu. |

### P2/P3 — polish, accessibility, and information-density issues

| ID | Area | Finding and observed effect | Recommendation |
| --- | --- | --- | --- |
| E-08 | Eve accessibility | The native focused-knowledge-base `<select>` has no programmatic name. Its wrapper only has a `title`, which does not label the combobox. | Add an associated visible label or `aria-label="Focused knowledge base"`. |
| E-09 | Eve accessibility | Every history-row action is announced only as “Rename” or “Delete,” producing dozens of indistinguishable controls. | Include the thread title in each action’s accessible name. |
| E-10 | Eve mobile composer | “Enter to send · Shift+Enter…” is tiny, can truncate, and describes desktop keyboard behavior to touch users. | Hide keyboard instructions on coarse pointers; substitute only relevant mobile guidance. |
| E-11 | Eve desktop density | The conversation column remains about 780 pixels wide on a 1728-pixel screen, leaving a large unused field even for citation-heavy answers. | Keep prose measure comfortable, but use a responsive secondary evidence/source rail on wide screens. |
| E-12 | Eve drawers | On a 390-pixel screen, source and chat drawers leave a narrow strip of the underlying conversation visible. It is not functionally broken, but the busy sliver competes with the drawer and makes the layer feel accidental. | Use a full-width mobile sheet or a stronger, noninteractive backdrop. |
| E-13 | Eve naming | A completed new conversation remained “Untitled chat” for the duration of the audit. | Generate a short title after the first completed answer and allow immediate correction. |
| H-11 | Hub persisted view | A user’s desktop Table preference persists onto mobile, where it is the weakest layout. | Store view preference by breakpoint or automatically substitute Grid on narrow screens while preserving the desktop choice. |
| H-12 | Hub toolbar semantics | The tree toggle originally said only “Toggle file list,” which hid the current state in its name. | **Fixed here:** the name now changes between “Show file list” and “Hide file list”; `aria-expanded` and `aria-controls` are present, and drawer focus moves predictably. |

## Behaviors that worked well

- Eve had no page-level horizontal overflow at any tested width.
- Agent status changed from Ready to Thinking and back to Ready; send-button state
  followed the request lifecycle.
- The mobile composer remained docked and usable while the answer streamed.
- Sources were revision-pinned and the source drawer had dialog semantics.
- Hub file navigation moved to the selected file, marked it with
  `aria-current="page"`, and returned the reader to the top.
- Grid is an effective narrow-screen repository view.
- Graph and Table keep their wide content inside local scrollers instead of
  widening the entire page.
- Dark styling remained coherent across Hub and Eve.

## Verification of implemented changes

The patched Hub was rechecked at 390×844 and 768×1024:

- 390×844: the file tree starts closed, the viewport and body remain 390 pixels
  wide, the toggle is 44×44, and opening it produces a 335-pixel drawer.
- 768×1024: the body remains 768 pixels wide, the tree resolves to 190 pixels,
  and the reading column to 571 pixels.
- The drawer exposes a labeled 44×44 close button and updates the toggle’s
  name and `aria-expanded` state. Opening moves focus to the drawer close button;
  close/Escape returns focus to the toggle.

Automated regression coverage checks the first-paint mobile state, drawer CSS,
dismissal hooks, responsive width constraints, long-link wrapping, ARIA state,
and primary mobile target sizes.

## Recommended next sequence

1. Fix Eve’s knowledge-base/thread context model before adding more controls.
2. Apply the 44-pixel interaction token throughout Eve.
3. Redesign Eve citations and source preview as one coherent evidence flow.
4. Make Grid the effective mobile repository view and give Table/Graph overflow
   explicit affordances.
5. Add automated mobile checks for minimum target size, labeled form controls,
   citation-chip legibility, and drawer focus/escape behavior.
