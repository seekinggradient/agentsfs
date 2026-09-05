# Hub note editor

A self-hosted visual Markdown editor built on Tiptap / ProseMirror, with CodeMirror for source editing. The Go Hub embeds the built assets; running or deploying the Hub needs no Node runtime and no third-party editor service.

## Build and verify

```sh
cd internal/hub/editor
npm ci
npm run build
npm test
npm run test:browser
```

Commit `package-lock.json`, source files, and the generated `../assets/editor.js` and `editor.js.LEGAL.txt` together. CSS lives in `../assets/editor.css`. The build is deterministic and dependency versions are pinned. The browser tests use installed Google Chrome, launch an opt-in Go fixture on `127.0.0.1:3348` (separate from the interactive preview), and exercise real commits in temporary repositories. The fixture is test code only. For a local interactive preview:

```sh
AFS_EDITOR_BROWSER_FIXTURE=1 go test .. -run '^TestEditorBrowserFixture$' -count=1 -timeout=30m -v
```

Open `http://127.0.0.1:3347/alice/notes/edit/note.md`. This fixture automatically authenticates as its test user and must only listen on loopback.

## Save contract

Typing writes a debounced recovery draft to localStorage, partitioned by signed-in viewer, repository, file, and loaded page. Drafts are local to the browser, not shared or synced; another tab cannot overwrite the active page's draft. Storage errors are visible and downloading the full draft remains available. Closing or navigating away from uncommitted edits triggers the browser's native warning. Recovery is offered explicitly on reopening a note.

**Save version** opens an editable description and line diff. One deliberate save becomes one real Git commit, attributed to the authenticated writer. GET reads content and revision from the same pinned snapshot. POST requires that full revision and a signed, viewer-bound CSRF token, and goes through `RepoCommit` for access, projection policy, path validation, and atomic compare-and-swap. Unrelated concurrent file changes merge; same-file changes open an explicit reconciliation dialog. Error responses never replace the draft's original revision. An identical retry returns the existing version without another commit. Local checkouts receive these commits through the normal Hub pull workflow.

## Markdown fidelity and limits

Opening, downloading, or switching modes without editing preserves the complete source. Frontmatter is separated from the visual body and retained verbatim. Unchanged top-level blocks retain their original Markdown when representable as individual editor nodes; changed blocks are serialized as GFM. Wikilinks have a native inline node so their targets and labels survive visual edits. Code, tables, task lists, and relative image destinations remain Markdown.

HTML/custom directives, math, footnotes, reference definitions, aligned tables, malformed frontmatter, non-Markdown files, and documents over 250,000 characters use source editing. The server accepts text up to 4 MiB. These limits protect constructs not faithfully represented by the visual schema. Formatting a changed block may normalize its Markdown notation; review shows the exact resulting source changes. Simultaneous cursors, cross-device draft sync, image uploads, and visual editing of those advanced dialects are not provided.

Editor dependencies are served locally, only on edit pages. External images are requested only from destinations already in the note or explicitly inserted by its writer; they use no-referrer. Active URL schemes are rejected. A progressively enhanced form remains available without JavaScript. Editor navigation deliberately uses full page loads so the Hub's PJAX lifecycle cannot bypass draft guards or leave a partially initialized editor.

Undo and redo accept both Control and Command shortcuts in the visual editor, including remote-desktop keyboard mappings. Native `beforeinput` history requests are routed to ProseMirror history. Initial loading is excluded from that history, and unchanged mode switches retain it.
