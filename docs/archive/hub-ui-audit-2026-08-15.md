# AgentsFS Hub and hosted Eve UX audit — 2026-08-15

> **Point-in-time delta audit.** This review compares the signed-in production
> product and the current Hub checkout with
> [hub-ui-audit-2026-08-12.md](hub-ui-audit-2026-08-12.md). Resolved findings
> are not repeated as new defects. Production observations and local changes
> are separated below. Nothing was deployed, committed, or pushed.

## Scope and method

The production pass used the real signed-in Chrome product and covered:

- the Hub dashboard and `seekinggradient/agentsfs` knowledge base;
- the latest maintenance journal note, `AGENTS.md`, and `CLAUDE.md`, including
  Files, Table, and Graph renderings, mobile file navigation, Context, commit
  history, filtering, graph keyboard operation, and recovery pages;
- the hosted Eve new-chat path, its persistent and compact conversation
  navigation, two harmless read-only submission attempts, two older threads,
  a cited answer, and the external-link safety confirmation;
- the real dashboard flow into the Boswell v2 agent and then back to the
  original global blank agent workspace. A direct knowledge-base-selector
  switch was impossible because the deployed selector is no longer rendered;
- desktop (1440×900), tablet landscape (1024×768), iPad portrait (768×1024),
  phone (390×844), and small phone (320×568) viewports;
- root and local overflow, touch-target size, focus and Escape behavior,
  keyboard navigation, loading and error states, accessible names,
  responsive navigation, and scanning/performance friction.

The harmless prompt was submitted twice because the first failure might have
been transient:

> Recurring UX audit, read-only: in one sentence, what is this knowledge base
> primarily for? Do not modify files.

Both attempts failed before an answer with “Hub 401 for GET /thread/<id>”. The
thread IDs were `fdcd04d9-f9dc-4507-8ad2-4ad7e648ac86` and
`7c1718a3-1ff1-42d7-9f25-01b804acf18f`. A trajectory lookup for the retry
returned “Thread not found”, while the same inspector successfully returned 70
archived events for the August 12 thread. This is strong evidence that the new
turn never became durable and did not modify any file.

Voice mode was not started. The live microphone path was unnecessary for this
pass and would have introduced device-permission and audio side effects. The
visible voice control was still measured at every viewport.

## Executive summary

The highest-priority finding is a production Eve availability and scope
regression. A new conversation cannot complete because Hub returns 401 while
Eve tries to access the thread. At the same time, Eve suppresses workspace and
history load errors: the knowledge-base selector and every visible scope label
disappear, and Conversations says “No conversations yet” even while an older
conversation is visibly loaded. Entering through AgentsFS or Boswell changes
only the invisible `?repo=` query parameter, so the two workspaces are
visually indistinguishable. This makes the failure both functional and a trust
boundary problem.

Hub fundamentals remain strong. All tested Hub and Eve roots stayed contained,
Files remains filename-first, Table owns its horizontal overflow, Graph has
useful keyboard behavior and feedback, the mobile file drawer manages focus
well, and recovery surfaces remain usable. The main new Hub issue is that the
desktop layout persists through 1024px and 768px while retaining mouse-sized
controls as small as 18–35px. This is especially problematic on real iPads.

This run safely expands the existing Hub targets to 44px at tablet widths and
on coarse pointers without changing navigation mode, content hierarchy, or
visual styling. Local Chrome verified the fix across Files, Table, Graph, and
note pages at 1024×768 and 768×1024. The earlier August 9 and August 12 Hub
fixes remain in the uncommitted worktree, and all repository tests pass.

## Hosted Eve findings

| ID | Severity | 2026-08-15 evidence | Patch-ready recommendation |
| --- | --- | --- | --- |
| E-22 | **P0 availability** | **New production regression.** Two fresh read-only submissions displayed the user message and progressed through Thinking/Working, then ended at “Needs attention / Request failed / Hub 401 for GET /thread/<id>”. The retry had no durable trajectory. The previously reported empty-home flash did not reproduce before the failure. | Repair the Hub-to-Eve identity bridge first. The Hub reverse proxy should mint or forward a fresh viewer-scoped PAT and verified identity headers on every `/agent/*` request, then validate that the PAT belongs to the signed-in viewer. Add an authenticated end-to-end canary that creates a thread, persists focus, streams one answer, and reloads it. |
| E-23 | **P1 scope/trust** | **New production regression.** The global Agent route, the AgentsFS repository route, and the Boswell v2 dashboard route rendered no knowledge-base selector or visible scope label at any tested width. There were zero `<select>` elements. AgentsFS and Boswell were distinguishable only by an invisible `?repo=` query parameter; even an older scoped conversation showed no authoritative workspace name. | Represent workspace state as loading/success/error rather than defaulting to a single empty repository. Always show an immutable owner/repository label for scoped routes and threads, including single-repository mode and while repo discovery is loading. Preserve the route scope until focus persistence succeeds. |
| E-24 | **P1 reliability/orientation** | **New production regression.** The desktop rail and compact Chats drawer both said “No conversations yet” while an older loaded conversation remained visible. The August 12 158-row history could not be rechecked because history is now falsely empty. Source inspection shows failed thread loads are caught and replaced with `[]`. | Check `response.ok`, keep loading/error/empty as distinct states, and show “Can’t load conversations” with Retry for authentication or network failures. Never translate an error into an authoritative empty-history message. Retain the earlier pagination/virtualization recommendation once loading is restored. |
| E-03 | P1/P2 touch | **Still present.** New chat measured 84×27 at compact widths; voice and Send were 34×34 at every width; textarea height was about 32px. The desktop collapse control was 30px and New chat 36px. | Apply a shared 44px coarse-pointer target token to navigation, composer, citation, source, modal, and drawer controls. Keep visual density through padding or pseudo-element hit areas where needed. |
| E-14 | P2 accessibility | **Reproduced.** Opening an older `domain.md` citation produced an external-link confirmation without `dialog`/`alertdialog` semantics. Focus remained on the citation; Escape dismissed the modal but left focus on `BODY`. | Use a labelled modal dialog, move and contain focus inside it, close on Escape, and restore the invoking citation on every close path. |
| E-06/E-17 | P1/P2 source trust | **Current cited-thread rendering regressed in disclosure.** The older Boswell answer rendered a `domain.md` citation, but no source-count disclosure was visible. Activating it led only to the external-link confirmation rather than an in-product, revision-pinned source view. The August 12 safe relative-link resolution defect remains relevant. | Resolve repository-relative links against immutable thread scope and revision. Prefer tool-derived structured citations, show source disclosure consistently, and reserve external-link warnings for origins outside the trusted Hub/Eve pair. |
| E-15 | P2 accessibility | **Still present in the compact Chats sheet.** Opening did not move focus into the sheet. Escape from its Close control did not close it, and clicking Close returned focus to `BODY`. | Give the sheet dialog semantics, move focus to its heading or Close control, support Escape, contain focus while open, and restore the Chats trigger. |

### Likely failure chain

Inspection of the sibling Eve checkout supports the following diagnosis without
requiring an edit outside this automation's writable scope:

1. Repository discovery calls `/api/repos`; failures are treated as non-fatal,
   leaving `repoInfo` in an initial `{mode: "single", repos: []}` shape.
2. The knowledge-base selector intentionally returns nothing in single mode,
   so an authentication error becomes an apparently valid but unlabelled
   single-workspace UI.
3. Conversation loading catches the same class of error and installs an empty
   array, producing the false “No conversations yet” message.
4. A fresh submission then reaches the Hub thread endpoint without a usable
   viewer credential and fails with 401 before the thread is durably archived.

The exact upstream credential break should be confirmed in deployment logs,
but the UI fallback behavior is independently unsafe: repository and history
errors must never masquerade as a valid empty state.

### Other Eve behavior rechecked

- Every tested Eve root remained horizontally contained. The persistent
  Conversations rail appears at desktop/tablet-landscape widths and switches
  to compact Chats navigation below that range.
- The helper “Shift+Enter for a new line” remains visible on touch layouts.
- The cited `domain.md` chip measured about 77×25 at 320px, below the preferred
  touch height.
- An older saved AgentsFS thread hydrated after roughly 2.6 seconds and an
  older Boswell cited thread also loaded, but neither disclosed its scope.
- The original production state was restored by returning to the Dashboard and
  opening the global Agent entry. The final route had no `repo` parameter. No
  knowledge-base or file was modified.

## Hub findings and changes

| ID | Severity | 2026-08-15 evidence | Status after this run |
| --- | --- | --- | --- |
| H-20 | P1/P2 tablet touch | **New.** Production keeps its desktop repository layout at 1024×768 and 768×1024, but Files tabs measured 26px high, Download 34px, filter 35px, disclosure carets 18px, file rows 18px, and Commit history 19px. Dashboard Agent measured 36px and sort controls 34px. These are mouse-sized targets on iPad-class touch devices. | **Fixed locally.** A max-1024/coarse-pointer media rule gives existing dashboard, repository, Files, Table, Graph, note, and history controls a 44px minimum target. It does not force the phone drawer or change the visual hierarchy. Local Chrome measured 44px targets across all four repository renderings at both tablet sizes. |
| H-15 | P2 responsive affordance | **Still not live.** A production note action strip could retain `has-overflow-right` after resizing even when client and scroll widths were equal. | The August 9 local resize refresh remains present and uncommitted. |
| H-18 | P1 accessibility | **Still not live.** A production Table no-match query displayed zero results while its live status remained empty. | The August 12 local announcement fix remains present; local Chrome announced “No files match this filter.” |
| H-19 | P2 touch | **Still not live.** Production Commit history remained about 19px high on phone and tablet. | The August 12 local 44px history target remains present and is also covered by H-20 at tablet widths. |

### Hub behavior that remained healthy

- Production Hub roots were contained at 1440×900, 1024×768, 768×1024,
  390×844, and 320×568. Wide Table content stayed within its 780px local
  scroller.
- Files remained readable and filename-first at 320px. `AGENTS.md`,
  `CLAUDE.md`, and the latest maintenance journal note opened successfully.
- At 320px the file drawer measured about 275px, moved focus to Close file
  list, closed on Escape, and returned focus to Show file list.
- Table filtering visibly reached zero results. The search, direction, and
  compact actions met 44px targets; only the undeployed history/link fixes
  remained small in production.
- Graph exposed a named application and named search, kept every tested tool at
  44px on phone, announced `0 of 44` for no matches, supported ArrowRight then
  Enter to focus a node, and used Escape to restore the all-notes state.
- The compact dashboard push drawer was in-flow, focusable, and closed on
  Escape. The repository-aware 404 remained contained with two 44px recovery
  links.
- The new narration-playback work exists in remote commits and its maintenance
  note was readable, but no current production note with an actual audio
  artifact was available in this pass. Playback itself is therefore not
  claimed as verified.

## Viewport evidence

| Surface/control | 1440×900 | 1024×768 | 768×1024 | 390×844 | 320×568 |
| --- | --- | --- | --- | --- | --- |
| Production Hub root | contained | contained | contained | contained | contained |
| Hosted Eve root | contained | contained | contained | contained | contained |
| Eve visible KB selector/scope | absent | absent | absent | absent | absent |
| Eve composer textarea | 874×32 | 816×32 | 628×32 | 250×32 | 180×32 |
| Eve voice / Send | 34×34 | 34×34 | 34×34 | 34×34 | 34×34 |
| Production Files tabs | 26px high | 26px high | 26px high | 44px high | 44px high |
| Production file row/caret | 18px | 18px | 18px | 44px | 44px |
| Production Hub Table | contained | contained | contained | local scroller | 780px local scroller |
| Production Hub Graph | contained | contained | contained | contained | 286×390 application |
| Local tablet Files controls | — | 44px | 44px | — | — |
| Local tablet Table/Graph/note controls | — | 44px | 44px | — | — |

“Contained” means the document scroll width did not exceed its client width;
intentionally wide components such as Table were checked within their own
scroller. The temporary viewport override was reset after testing.

## Implemented Hub changes

1. Add a tablet/coarse-pointer interaction layer that raises the existing Hub
   dashboard, repository tabs, file controls, Table controls, Graph tools, note
   actions, and history link to a 44px minimum target.
2. Preserve the current layout breakpoints: tablet landscape and portrait keep
   the information-dense desktop hierarchy, while the existing phone drawer
   still activates at its original breakpoint.
3. Add regression coverage that locks the new media query and representative
   target selectors.

The frontend workflow's design constraints shaped this as a restrained
interaction fix rather than a redesign: the Hub remains a calm,
directory-first workbench, no sections or marketing copy were added, and no
ornamental motion was introduced.

No sibling `agentsfs-eve` code was edited. The production Eve remediation is
documented patch-ready because that checkout is outside this automation's
writable scope.

## Verification

Completed successfully:

- `gofmt -w internal/hub/filetree_test.go`
- `git diff --check`
- `go test ./internal/hub`
- `go test ./...`
- signed-in production Chrome audit at all five required viewports;
- local Chrome verification at 1024×768 and 768×1024 across Files, Table,
  Graph, and note pages, including 44px targets and root containment;
- local zero-result Table live announcement and the earlier responsive fixes.

The checkout began with uncommitted August 9 and August 12 Hub improvements and
reports. Those changes were preserved. This run augments `style.css` and
`filetree_test.go` and adds this report. `main` is four commits behind
`origin/main`; the remote renderer and narration commits were inspected but not
pulled or modified. Everything remains uncommitted and undeployed as required.

## Recommended next sequence

1. Treat E-22 as the release blocker: restore authenticated thread creation and
   add an end-to-end canary before any further hosted Eve rollout.
2. Make workspace and conversation errors explicit (E-23/E-24), then ensure
   every scoped route and loaded thread has an immutable visible knowledge-base
   label.
3. Repair trusted repository citations and source disclosure (E-06/E-17), and
   fix link-dialog and Chats-sheet focus behavior (E-14/E-15).
4. Apply Eve's shared 44px coarse-pointer target system (E-03).
5. Review and deploy the accumulated tested Hub fixes, including H-20. This
   automated run intentionally did not deploy them.
