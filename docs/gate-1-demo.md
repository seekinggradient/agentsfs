# Gate 1 demo — 2026-06-13 (run 2026-06-12 session)

Layer 1 gate per the [execution plan](execution-plan.md): real binary, real instance, fresh-context agent sessions, owner reviews.

## Setup

- `afs init /tmp/agentsfs-demo` with the freshly built binary (git-lfs absent on this machine, so the no-LFS graceful path was exercised). Current CLI note: `init` is create-only; use `afs setup` or `afs connect` when a project should learn about the instance.
- Two agent sessions, each given **only** the connection block a harness would inject plus a user message. No coaching on conventions. Session B had zero knowledge of session A.

## What happened

**Session A** (user dumps renters-claim facts, asks to be remembered): read AGENTS.md unaided, created `projects/` and `reference/` each with `INDEX.md`, wrote a dense state-of-play file plus entity pages for the insurer and adjuster, used wikilinks throughout, gave every file a `description:`, cited sources in frontmatter, committed once with a meaningful message.

**Session B** (fresh; asks what's outstanding + relays new developments): oriented via the same flow, answered correctly from files alone (e-bike proof outstanding, deadline 2026-06-25, approved amount math right), and — the critical behavior — **updated in place**: rewrote the existing claim file and the adjuster's entity page, created nothing new, committed. Its own explanation cited the "update, don't append" rule.

**Verdict: gate passed.** Compounding across sessions with zero re-explaining, conventions honored without tooling.

## Friction log (feeds Layer 2 scope)

1. **Orientation costs ~3–4 tool calls per session** (`find`, `git log`, several reads). Worked, but `tree` with descriptions collapses it to one call. Confirms `tree` as the highest-value Layer 2 command.
2. **The contract's own examples pollute link scans.** `[[Name]]` and `[[work/Apple]]` from AGENTS.md show up when grepping for wikilinks. `backlinks`/`doctor` must exempt the root contract file (or its example spans) or they'll report dead links forever.
3. **Template bug found and fixed:** `CLAUDE.md` shipped without a `description:`, violating the contract's own rule 1. Caught by a manual frontmatter sweep — exactly a `doctor` check. Fixed in template and fixture.
4. **CLI bug found and fixed:** stdlib `flag` ignored flags placed after the directory argument; replaced with position-independent parsing.
5. No-LFS path works (warning printed, `.gitattributes` skipped). No-remote path works (agents committed, noted nothing to push).
6. Agents pulled the user's name from the machine environment unprompted and wrote it into notes. Harmless here; worth remembering that instances absorb ambient context.

## Honest limitations

- Both sessions were Claude-family agents; the connection block was simulated as their harness config. This validates the contract and prompts, not yet a second harness. (Note: a real Codex global config exists on this machine at `~/.codex/AGENTS.md`, so a Codex run is feasible later.)
- One scenario, one domain, two sessions. Good signal, small sample.
