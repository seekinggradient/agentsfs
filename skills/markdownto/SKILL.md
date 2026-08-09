---
name: markdownto
description: Use when the user wants a todo list, kanban board, backlog, audio narration manuscript, or other structured content as a portable Markdown file (run `mdto spec` for the full current family), or when a .todo.md/.kanban.md/.backlog.md/.audio.md file needs to be read, edited, validated, or rendered.
---

# Markdown To

## 1. What this is

Markdown To is a family of plain-Markdown specs, each canonically defined at `specs/<name>/SPEC.md`
in this repo and printed in full by `mdto spec <name>`. The `.md` file **is** the state: no database,
no hidden index, nothing a plain viewer without the tool can't already show. A file's own one-line
frontmatter envelope, `markdownto: <name>@<major>.<minor>`, is the only thing that decides which spec
it's under — never the filename, never content sniffing. Read the envelope before you assume anything
else about a file.

**Discover the family, don't recite it.** Run `mdto spec` with no name for the list of specs this
build implements, or fetch https://markdownto.ai/llms.txt when working outside a checkout — both are
live and both are the source of truth, never a list memorized from this file. At the time this skill
was written the family included things like `todo@0.1` (a checklist), `kanban@0.1` (a board),
`backlog@0.1` (a prioritized queue), and `audio@0.1` (a narration manuscript) — treat those as
examples of the shape a spec takes, not as the roster.

## 2. When to reach for each spec

- **todo@0.1** — a flat or lightly-sectioned checklist; no priority tiers, no dependencies, no board.
- **kanban@0.1** — a board: work grouped into named columns, finished by dragging a card rightward.
- **backlog@0.1** — a prioritized queue with an in-progress/dropped state and "blocked by" dependencies.
- **audio@0.1** — a script meant to be *heard*: chapters and narration for TTS, not task tracking.

This skill has detailed scaffolds and repair guidance for exactly these four below. If `mdto spec` or
llms.txt lists a spec this skill doesn't cover, read its SPEC.md the same way (`mdto spec <name>`) —
the shape (envelope, five-part doc, `mdto validate`/`mdto render`) is the same for every spec in the
family, named here or not.

If unsure, ask what the user actually does with the file day to day: check things off (todo), drag
things across (kanban), decide what to pick up next (backlog), or read it aloud (audio).

## 3. Scaffold: the minimal valid file for each spec

**todo** — a bare checklist, no sections needed:

```markdown
---
markdownto: todo@0.1
---

- [ ] Renew the car registration
- [ ] Book a dentist appointment
- [x] Pay the electricity bill
```

**kanban** — `##` headings are columns; the rightmost is done by default:

```markdown
---
markdownto: kanban@0.1
---

## Backlog

- [ ] Write the conformance fixtures

## Doing

- [ ] Build the patch engine

## Done

- [x] Choose the portable envelope
```

**backlog** — `##` headings are reserved bands (`Now`, `Next`, `Later`, `Someday`, `Done`); unused
bands are simply omitted; `[/]` means in-progress:

```markdown
---
markdownto: backlog@0.1
---

## Now

- [/] Rewrite the studio's about page
- [ ] Photograph the new work for the portfolio

## Next

- [ ] Move the mailing list off the old provider
```

**audio** — no headings at all is legal: the whole document is one implicit chapter of prose:

```markdown
---
markdownto: audio@0.1
---

I keep meaning to write these down while they are still warm, so here is the first one.
```

None of these carry a block identifier (`^id`) — identifiers are lazy (conventions §3) and only
tooling pins them, never a hand-authored file.

## 4. Validate + repair loop

`mdto validate <file>` is step one, always, before editing or trusting that a file is what it looks
like. It prints one line per diagnostic — `<file>:<line>: <severity> <code> <message>` — and exits 1
only on `error`-severity codes; `warning`/`info` are advice, not failure.

- **Read the code before guessing the fix.** Every `MDTOxxx` is owned by one range (shared 001–099;
  todo 100s; kanban 200s; audio 300s; backlog 400s) and is stable forever, so looking one up is a
  permanent investment. A spec beyond these four documents its own range at the top of its SPEC.md
  (`**Owns diagnostic codes:**`).
- `mdto spec <name>` prints that spec's full agent-facing SPEC.md — Purpose, Grammar, Rationale,
  Examples, Verb reference — straight to the terminal. When a fix isn't obvious from the diagnostic
  message alone, read the Rationale section first: it exists specifically so an agent doesn't "fix" a
  file in the wrong direction.
- **The near-miss principle.** Some errors are telling you the file wants a *different spec*, not a
  different character. `[/]` in a `.todo.md` file is `MDTO031` ("unrecognized checkbox state"), and the
  reflex fix — map `/` to `x` or ` ` — is usually wrong. The marker is legal in `backlog@0.1`, where it
  means in-progress; if the file is really a prioritized queue, the right repair is switching the
  envelope to `backlog@0.1` **and** adding band headings (`## Now`, `## Next`, …), since backlog isn't
  todo with one marker bolted on. Treat an envelope switch as a deliberate, whole-document decision —
  never a silent coercion of one line. (See conventions §9.3 and backlog SPEC.md §3.)
- Diagnostics never delete data. A field or marker that fails validation stays in the source and in the
  IR; a repair edits it, it never drops it.

## 5. Mutate, never rewrite

Don't hand-edit a conforming file's structure in a text editor when a verb exists — freehand edits risk
exactly the reformatting, reordering, and identifier churn the whole format exists to avoid. Every spec
ships its own verb namespace under `mdto <spec> <verb>`; run `mdto <spec> --help` and
`mdto <spec> <verb> --help` for exact flags before calling one blind. A verb, by contract:

- validates first and refuses (non-zero exit, nothing written) rather than guess when the file already
  has errors — `--force` overrides that refusal only, at your own risk;
- edits the smallest possible span: one line replaced, one line inserted, or a delete-and-reinsert for
  a move, never a reformat of anything else;
- leaves the file guaranteed conforming when it's done.

**Verb vocabularies are spec-owned** — a name shared across specs does not mean the same edit. Always
read the verb reference of the spec the file actually declares, not the one that looks similar:

- `todo` owns `add`, `done`, `undone`, `rm`, `move` — `move` here never touches the checkbox.
- `kanban` owns `add`, `move`, `done`, `rm`, `edit` — `move` *does* write the checkbox when it crosses
  the done-role column (the last column, or `done-column:`); this is the one place the family
  deliberately couples position and state.
- `backlog` owns `add`, `start`, `finish`, `drop`, `block`, `unblock`, `promote`, `demote`, `reorder`
  — all landed; `mdto backlog --help` lists them — plus `graduate`, which its SPEC.md §5 specifies but
  this build deliberately does not ship: it needs a multi-file transaction the patch engine's
  one-file-one-hash contract doesn't define yet, and `mdto backlog graduate` refuses and says so rather
  than faking it. `promote`/`demote` never touch the checkbox either.
- `audio` owns `estimate`, `produce`, `preview`, `voices` — all read-only. No audio verb ever edits the
  manuscript; artifacts are written beside it.

`--dry-run` prints the unified diff and writes nothing — use it first whenever you're not certain what
a verb will do.

## 6. Render

`mdto render <file>` writes one self-contained HTML page beside the source (`<basename>.html` by
default, `-o <path>` to choose, `-o -` for stdout) — no script, no network fetch, safe on untrusted
input. A file that fails to parse still renders, as its own validation report, with exit 1 instead of a
stack trace, so `render` is also a reasonable first look at a file you're not sure about.

## 7. Pointers

- Canonical, in this repo: `specs/conventions.md` (shared rules every spec inherits) and
  `specs/<name>/SPEC.md` for every spec in the family — `mdto spec` with no name lists them, and
  `mdto spec <name>` prints any one without leaving the shell.
- Live, for an agent working outside this repo: `markdownto.ai/specs/<name>.md` and
  `markdownto.ai/llms.txt` mirror the same documents — fetch `llms.txt` first, since it names every
  spec currently published.
