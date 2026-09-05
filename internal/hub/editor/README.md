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

Typing saves a private working draft to the Hub after 600 ms of quiet, with a two-second maximum interval during continuous typing. “Saved · version pending” means the server has acknowledged durable storage; “All changes saved” means the draft matches its published version. The editor retains per-page localStorage recovery copies until acknowledgment, warns before leaving with unsynced edits, and retries automatically after connection failures. A new browser/device opens the writer's pending server draft automatically.

Server drafts live in `<storage-root>/.editor-drafts.git/`, a private bare Git repository outside every served user/repository namespace. Every acknowledged autosave is a real commit on a writer/document branch under `refs/heads/drafts/`. The branch tree contains the latest Markdown (`content.md`), its shared merge base (`base.md`), and session metadata (`draft.json`). These are separate Git blobs so unchanged content is deduplicated. Git object and ref fsync precede acknowledgment. Include the private repository in volume backups. A single Hub process owns the volume.

The main Hub command starts `RunEditorAutosave(ctx)`; other embedders must start it for their server lifetime. Its startup scan and five-second tick squash the cumulative draft change into the shared repository after 30 seconds of inactivity or five minutes of continuous writing. This uses the existing `RepoCommit` merge-base and compare-and-swap path, rather than importing private history or metadata into the shared repository. Closing the browser does not cancel the checkpoint. The Fly deployment keeps one machine running so the worker cannot be suspended while a draft is pending.

Completed session branches remain available for retry detection. When the writer starts another session, the previous tip is retained under `refs/heads/archive/` and the draft branch starts a fresh history. Archived sessions are retained for operator recovery, including intermediate autosave commits; they are not shown in the public Git history. There is no automatic deletion of recovery history. The worker checks Git auto-packing every minute so repeated note snapshots are delta-compressed; reachable archived commits survive ordinary garbage collection. The legacy `.editor-drafts/*.json` preview format is imported on startup and marked `.migrated` only after Git persistence succeeds.

Each checkpoint is an immutable Git commit authored by the writer, with a default description such as `Edit project-notes.md`. Existing agents, collaborators, exports, and local checkouts see the previous committed content until that checkpoint. **Name version** optionally opens a diff and description to publish the current draft immediately. Ctrl/Command-S requests an immediate draft save without opening a modal. The plain form without JavaScript retains explicit version saves.

Every POST requires a pinned full Git revision and signed viewer-bound CSRF token. Draft revision checks prevent different tabs from replacing one another's writing. Publishing rechecks permissions and uses `RepoCommit` for projection policy, path validation and Git compare-and-swap. Disjoint changes merge; same-file conflicts preserve the private draft and require explicit reconciliation. Revoked access prevents publication without discarding the saved draft. Retries, including a restart between Git commit and draft bookkeeping, recognize identical content without making duplicate commits. Published history is never amended.

## Markdown fidelity and limits

Opening, downloading, or switching modes without editing preserves the complete source. Frontmatter is separated from the visual body and retained verbatim. Unchanged top-level blocks retain their original Markdown when representable as individual editor nodes; changed blocks are serialized as GFM. Wikilinks have a native inline node so their targets and labels survive visual edits. Code, tables, task lists, and relative image destinations remain Markdown.

HTML/custom directives, math, footnotes, reference definitions, aligned tables, malformed frontmatter, non-Markdown files, and documents over 250,000 characters use source editing. The server accepts text up to 4 MiB. These limits protect constructs not faithfully represented by the visual schema. Formatting a changed block may normalize its Markdown notation; review shows the exact resulting source changes. Simultaneous cursors, live collaborative editing, image uploads, and visual editing of those advanced dialects are not provided.

Editor dependencies are served locally, only on edit pages. External images are requested only from destinations already in the note or explicitly inserted by its writer; they use no-referrer. Active URL schemes are rejected. A progressively enhanced form remains available without JavaScript. Editor navigation deliberately uses full page loads so the Hub's PJAX lifecycle cannot bypass draft guards or leave a partially initialized editor.

Undo and redo accept both Control and Command shortcuts in the visual editor, including remote-desktop keyboard mappings. Native `beforeinput` history requests are routed to ProseMirror history. Initial loading is excluded from that history, and unchanged mode switches retain it.
