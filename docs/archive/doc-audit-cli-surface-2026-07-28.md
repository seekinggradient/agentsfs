---
description: Audit of the documentation shipped inside the afs binary — help text, `afs docs` topics, the hand-maintained command table, skills, and diagnostic output — against v0.10.0 behavior on main.
---

# Doc audit: the CLI surface (2026-07-28)

> **Archived audit.** A review of the docs shipped inside the `afs` binary, against
> v0.10.0. Its findings have been applied, so the defects it lists are historical. For the
> current in-binary docs, run `afs docs` or start at [../README.md](../README.md).

Scope: everything a user or agent can read *without a repo checkout* — `afs help`, `afs docs <topic>`, `afs skills`, error and diagnostic strings, and the MCP `docs` tool. Binary under test: `afs 0.10.0`, built from `main` @ `4835cc8`. Every claim below was executed against that binary or read at the cited line. Synthesized from five parallel audits; contradictions were re-verified against source before inclusion.

---

## 1. Verdict

The in-binary docs are **accurate in the places that were hand-written recently and rotten in the places that are hand-maintained tables**. The prose surfaces — `docs/agent-start.md`, `docs/setup.md`, `template/AGENTS.md`, the four `SKILL.md` files, the five `prompts/*.md` — are in good shape: they describe the current CLI, they agree with each other on commit discipline and reserved roles, and they already carry the correct product positioning (Hub as a real hosted product, self-hostable, private by default, real git). The obsolete "no managed hosting" line was genuinely removed from the shipped contract. What has decayed is everything that has to be kept in sync *by hand*: the 24-row command table in `internal/docs/docs.go`, the usage strings inside it, the topic list, and the claim that MCP serves "the same capabilities." The existing tests give false comfort — `TestCommandDocsCoverDispatch` guards top-level command *names* only, and `TestTopicsRenderAndAreDocumented`'s frontmatter check passes on any file containing the substring `description:` anywhere, which is why `docs/setup.md` ships with no frontmatter at all and nothing fails.

Two things dominate everything else. **The single highest-leverage fix is `internal/docs/docs.go`**: it is the one file that feeds `afs help`, `afs docs commands`, `afs docs <topic>`, and both MCP `docs` tools, and it carries ~15 of the drift items below at once — the wrong usage strings, the missing `hub logout`, the false MCP-parity claim, the 178-character help lines, and the entire (tiny) topic list. Fixing that file corrects every reader-facing surface simultaneously. Second, at the *distribution* level rather than the docs level: `embed.go:17` is a wildcard (`docs/*.md`) that ships all 20 files in `docs/` — ~215 KB of internal planning notes, superseded decisions, and a Hub UI audit — inside every released binary, unreachable by any command but trivially extractable with `strings`. That is where the last live copies of the obsolete positioning actually are (§5).

---

## 2. Drift table

Every confirmed discrepancy between what the binary claims and what the code does, most severe first.

| # | Severity | Where the binary says it | What the code does | Evidence |
|---|---|---|---|---|
| D1 | **High** | `template/INDEX.md:7` — the root index written into *every* fresh instance: the description "is what `afs tree`, `afs status`, and the Hub show as this workspace's label." | `afs status` shows no label column at all, and `afs status --json` emits `AGENTS.md`'s description — contract boilerplate, near-identical across every instance in existence. | `internal/core/status.go:391`: `Description(filepath.Join(path, "AGENTS.md"))`. The Hub does it correctly (`internal/hub/web.go:2037-2043`, INDEX→AGENTS→README with placeholder filtering). `internal/core/doctor.go:119-124` repeats the false claim in a comment. Contract 0.7.0 moved the per-instance description to `INDEX.md`; `status` was never updated. |
| D2 | **High** | `afs docs hub` (`docs/hub.md:62`): "The Hub spins up your own private agent — a hardware-isolated sandbox that clones all of your Hub repos… It boots by listing your workspaces and asking which to work in." | That describes the retired per-user Sprite. Production is the shared Vercel-hosted Eve app with no per-user VM and no persistent clone. | `docs/internals/hosted-agent.md:9`: "The current production agent is the shared `agentsfs-eve` application hosted on Vercel. It is **not** a cloned agent process or a permanently provisioned VM per user." `README.md:191` agrees with ../internals/hosted-agent.md. `docs/hub.md` is a *reachable* topic; `docs/internals/hosted-agent.md` is not. The binary's only reachable description of the hosted agent is the wrong one. |
| D3 | **High** | `internal/docs/docs.go:56` — `afs mcp [path]`, "serve the **same capabilities** over MCP". | 12 MCP tools vs 20 top-level CLI commands. No `init`, `setup`, `connect`, `reindex`, `embeddings`, `contract`, `skills`, `update`, `uninstall`, `version`, `hub login`, `hub logout`. | Live `tools/list`: `backlinks, docs, doctor, hub_list, hub_pull, hub_push, hub_status, rename, roles, search, status, tree` (`internal/mcpserver/server.go:44,64,96,123,161,183,206,233,249,271,291,337`). |
| D4 | **High** | `internal/docs/docs.go:64` markets `--context` as search's headline feature ("hydrates the top hits into a token-budgeted pack") under a claim of MCP parity. | MCP `search` has no context parameter — params are `query, semantic, limit, path`. The flagship retrieval path is CLI-only. | `internal/mcpserver/server.go:116-121`. |
| D5 | **High** | `internal/docs/docs.go:66` — `afs embeddings <status\|setup\|clear> [provider] [--yes]`. | Four errors in one string: the subcommand is *optional* (bare `afs embeddings` = status, `main.go:1225-1228`); `status` accepts no args at all (`main.go:1242-1244`); `clear` accepts no provider (`main.go:1324-1333`); `setup` **requires** a provider from the closed set `openai\|voyage` (`main.go:1282-1284`). | All four verified live. Correct shape: `afs embeddings [status] \| setup <openai\|voyage> [--yes] \| clear [--yes]`. |
| D6 | **High** | `--help` works. | `--help`/`-h` is handled on exactly three surfaces: top-level `afs` (`main.go:98-99`), `afs hub` (`hub.go:46-47`), `afs uninstall` (`uninstall.go:51-53`). Everywhere else it exits 1 with `afs: unknown flag "--help"` — no command name, no usage, no pointer. | `$ afs doctor --help` → `afs: unknown flag "--help"`, exit 1. Same for `tree`, `roles`, `status`, `reindex`, `mcp`, `search`, `hub login`. Source: the shared flag loop at `main.go:469-481`. |
| D7 | **Medium-high** | `internal/docs/docs.go:73` — `afs skills [list]`, "**list** the bundled agent skills (setup, remember, adopt, garden)". | (a) It writes: `skills.Materialize(baseDir)` runs unconditionally on every invocation (`cmd/afs/skills.go:31`), refreshing `<config>/agentsfs/skills/`. (b) The four names are wrong — the real skills are `agentsfs-setup`, `agentsfs-remember`, `agentsfs-adopt`, `agentsfs-garden`. The short names match no directory, no listing line, and no copy path. | `skills/` directory names; live `afs skills` output. |
| D8 | **Medium-high** | `internal/docs/docs.go:72` — `afs contract [current\|status\|diff\|upgrade] [path]`. | Accepts `--yes`/`-y`/`--force` (`main.go:349`), and `--force` is the documented escape hatch in three separate error messages (`main.go:360,366,369`). **The same binary prints two different usage strings for the same command**: `afs contract bogus` → `usage: afs contract [current\|status\|diff\|upgrade] [path] [--yes] [--force]` (`main.go:394`), while `afs docs commands` prints the shorter form. | Both verified live. |
| D9 | **Medium** | `afs hub logout` exists. | It does (`hub.go:43-45`) and `afs hub help` documents it (`hub.go:74`) — but it is absent from `internal/docs/docs.go` entirely, so it appears in neither `afs help` nor `afs docs commands`. Two usage texts in one binary disagree. | `afs docs commands \| grep logout` → nothing. |
| D10 | **Medium** | `internal/docs/docs.go:64` presents `--context`, `--semantic`, and `-n` as freely combinable. | `--context` silently ignores both. `main.go:1100-1102` documents the `--semantic` case in a comment; `core.SearchContext(root, query, budget)` (`main.go:1104`) never receives `limit`. Proof: `afs search q --semantic` exits 1 ("no embedding provider configured"); `afs search q --context --semantic` exits 0 with FTS results. | Verified live. |
| D11 | **Medium** | `cmd/afs/hub.go:70` and `docs/hub.md:32` — `--merge` quarantines differing files under `scratch/hub-merge-<slug>/`. | `scratch/` is the pre-0.4.0 classic name; current instances use `agent-scratch/` (`internal/core/reserved.go:25-32`). The merge code resolves the real scratch role correctly (`internal/hubclient/hubclient.go:523-530`) — only the docs are stale, so a user follows the help text to a directory that doesn't exist. | |
| D12 | **Medium** | `docs/hub.md:58` — "self-serve signup at `/signup`". | Signup is allowlist-gated; non-allowlisted emails are recorded on a waitlist. | `internal/hub/accounts.go:399-405`: "When the allowlist is non-empty it gates self-serve signup… everyone else is recorded on the waitlist." Seeded from `AFS_HUB_ALLOWLIST`. Whether production's allowlist is currently populated is **unconfirmed** from source — but the doc should describe the gate either way, since it toggles without a doc change. |
| D13 | **Medium** | `docs/hub.md:9` — "Anyone can also run their own (see [self-host.md](../../deploy/self-host.md))." | `deploy/` is not embedded (`embed.go:17` covers `README.md docs/*.md prompts/*.md template/AGENTS.md`). For anyone who installed via curl or Homebrew the link resolves to nothing, and it renders in the terminal as literal markdown brackets. No self-hosting content ships in the binary at all. | |
| D14 | **Medium** | `docs/setup.md:91` and `:130` tell the agent to "follow `prompts/adopting.md`" / "follow `prompts/onboarding.md`". `README.md:171` links both. | Those files are embedded but reachable by nothing: `Render()` only matches the five names in the `topics` table (`docs.go:120-128`). `afs docs onboarding` → `unknown docs topic "onboarding"`. An agent that installed via curl has the bytes inside its own binary and no command that will print them. | Verified live. |
| D15 | **Medium** | `internal/docs/docs.go:75` — `afs uninstall` "Never deletes any agentsfs filesystem or git data". | True but materially incomplete: it deletes the materialized skills cache (`uninstall.go:124-127,158-162`) and, with `--remove-global-connections`, rewrites `~/.claude/CLAUDE.md` / `~/.codex/AGENTS.md` (`uninstall.go:112-119,149-157`). `uninstall.go:17-26`'s own help const mentions the global connections but not the skills cache. | |
| D16 | **Medium** | `afs doctor` is a "deterministic health check" (`docs.go:67`) with no stated exit contract. | Exits 1 whenever any finding has severity `error` (`main.go:685-687`). Undocumented in the only doc surface CI and agents read. | |
| D17 | **Medium** | Doctor: `warn contract-version AGENTS.md — missing agentsfs_contract version; run \`afs contract status\``. | Fires identically whether the field is missing *or* `AGENTS.md` is gone entirely — `ContractVersion()` returns `""` for both (`internal/core/contract.go:16-18`; finding at `doctor.go:108-109`). The wording sends the user to edit a file that may not exist. `afs contract status` conflates the same two cases (`main.go:427-432`). | Reproduced by deleting `AGENTS.md` and by deleting only the field. |
| D18 | **Low-medium** | Flags and aliases accepted but documented nowhere. | `-y` on `setup`, `init`, `connect`, `update`, `contract upgrade`, `embeddings setup/clear`, `uninstall`; `afs tree -d N`; `afs search --limit N`; `afs hub link` (= push); `afs hub clone`/`get` (= pull); `afs hub repos`/`ls` (= list); `afs hub pull --vendor` (= `--merge`); `afs --version`/`-v`. | `main.go:535,919,988,165,349,1269,1327,620,1080`; `hub.go:28,30,32,146`; `uninstall.go:39`. All dispatch confirmed live. |
| D19 | **Low** | `afs status --doctor` HEALTH column. | Prints `1 warnings` — no singular form. | `cmd/afs/main.go:867-875`. |
| D20 | **Low** | Doctor's `journal-backlog` finding: "run the gardener to fold them into durable notes" (`internal/core/doctor.go:294`). | Every other finding names a runnable command (`run \`afs contract upgrade\``). "The gardener" is a persona, not a verb — it's the `agentsfs-garden` skill, which must be copied out of `afs skills` first. A reader cannot act on the message alone. | |
| D21 | **Low** | `internal/docs/docs.go:59` says "workspace"; `docs.go:60`, one line below, says "workspaces". | Two adjacent lines of the same `afs help` table spell the concept two ways. The CLI/MCP side uses the closed compound (`hub.go:65`, `hubclient.go:403,493`, `instance.go:92,122`, `mcpserver/server.go:288,292,311`); the Hub UI and docs use two words everywhere. | |
| D22 | **Low** | `afs docs [topic\|--all]` (`docs.go:71`). | Also accepts bare `all` and bare `list` (`docs.go:111-115`). Harmless under-documentation. | |

**Why it rotted, mechanically.** `cmd/afs/main_test.go:449` extracts commands with `case "([^"]+)"`, capturing only the first literal — so aliases are structurally invisible to the guard. It also scans only the region of `main.go` between `switch os.Args[1]` and `func runDocs` (`main_test.go:431-437`), which excludes `hub.go`'s subcommand switch, `runEmbeddings`, and `runContract` — exactly where D5, D8, D9, and D18 concentrate. `internal/docs/docs_test.go:45` checks table→README→usage consistency but never CLI→table.

---

## 3. Coverage gaps

Shipped capabilities with no `afs docs` topic and no meaningful help-text presence, ranked by how often a real user or agent hits them.

1. **The MCP tool surface itself.** Twelve tools ship (`internal/mcpserver/server.go`), and `afs docs commands` — whose own description claims to be a "CLI **and MCP** command overview" (`docs.go:47`) — contains zero MCP tool names, because `commandOverview()` (`docs.go:132-144`) iterates only the CLI slice. `docs/hub.md:42-51` documents the five `hub_*` tools and nothing else; `tree`, `search`, `doctor`, `roles`, `backlinks`, `rename`, `status`, and `docs` are undocumented on the surface an agent actually calls. Highest-frequency gap by a wide margin: every MCP-connected agent hits it on first use.
2. **The onboarding / adopting / gardening prompts (D14).** Three second-person agent prompts ship in the binary, are pointed at by name from a reachable topic and from the README, and cannot be printed. Hit by every agent doing a first session or an adoption.
3. **The hosted agent.** `docs/internals/hosted-agent.md` — the correct, current architecture, permissions model, and PAT flow — is embedded and unreachable. The only reachable account of it is the stale five lines at `docs/hub.md:60-64` (D2).
4. **Self-hosting.** Zero content in the binary (D13), despite "take it and self-host it" being the center of the product pitch. `deploy/self-host.md` isn't even embedded.
5. **Contract upgrades.** Named as a concept in `docs/setup.md:87` and `template/AGENTS.md:25`, given one line in the command table, and never explained — what an upgrade changes, when it refuses, what `--force` costs. The runtime error messages (`main.go:359-370`) are the best documentation of this feature that exists, and you only see them by getting it wrong.
6. **Collaborators and sharing.** One clause in `docs/hub.md:28`. `prompts/collaborator-invite.md` is a complete, accurate invite-flow doc with read/write roles; it's embedded, used by the Hub web UI (`internal/hub/web.go:592`), and unreachable from the CLI.
7. **The Hub's own MCP surface.** `internal/hub/mcpapi.go` serves a *different* tool set — `search`, `fetch`, `list_workspaces`, `tree`, `docs`, `write` (lines 177, 297, 368, 408, 465, 498). `write` and `list_workspaces` exist only there. Nothing in the binary mentions this surface exists. Lower frequency, but it is the managed product's flagship integration and the binary undersells it to zero.
8. **`afs status` column vocabulary.** MODE (`standalone`/`shared`/`unversioned`), SYNC (six values), HEALTH (`not checked`), CONTRACT (`behind custom`) print as bare enum dumps with no legend anywhere in the command's output. `afs init --shared` explains "mode: shared" beautifully once, at creation; `afs status` never repeats it. The one doc that glosses these (`docs/agent-start.md:80`) is a topic you have to know to run.

---

## 4. Writing quality

**The primary help screen is unreadable at 80 columns.** `CommandUsage()` (`internal/docs/docs.go:91-97`) pads the usage field to a fixed 78 characters with `%-78s %s` regardless of terminal width. The longest line is 178 characters; all 24 wrap two or three times with no hanging indent. What a user actually sees for one entry:

```
  afs hub pull <name> [dir] [--merge]
 download a workspace into the current directory; --merge folds it into the
current instance
```

This is the first thing a newcomer reads, and it reads as garbled output. The irony is that a well-organized version already exists in the same file: `commandOverview()` (`docs.go:132-144`) renders the same 24 commands under group headers — `Connect agents`, `Sync to a Hub`, `Orient`, `Configure`, `Maintain`, `Learn AgentsFS`, `Manage` — in deliberate priority order rather than alphabetically. That grouping is exactly what a newcomer needs, and it is reachable only by typing `afs docs commands`, which a newcomer will never type.

**Bare `afs` exits 2.** It prints byte-identical content to `afs help`, which exits 0. The tool's own front door reports itself as a failure to any wrapper script.

**Errors that diagnose but don't direct.** The most frequently hit error in the tool gives no next step:

```
afs: /path is not inside an agentsfs (no .agentsfs/ directory, and no AGENTS.md
declaring "This folder is an agentsfs", in any parent)
```

`internal/core/instance.go:44`. This one string is shared by `doctor`, `tree`, `roles`, `search`, `backlinks`, `rename`, `reindex`, and `contract status/diff/upgrade`. It never mentions `afs setup`. `afs status` in an empty directory has the same shape — precise, honest, and a dead end. The house style *should* be what `afs init` and `afs contract upgrade` already do:

```
afs: you're inside the git repo at .../afstest-shared. Choose where this agentsfs should live:
  personal, outside this repo: afs setup ~/agentsfs
  shared with this codebase: afs init ./agentsfs --shared
```

```
afs: this contract is customized — run `afs contract diff` to see your adaptations and what
0.9.0 changes, port them by hand and set agentsfs_contract: 0.9.0 yourself, or pass --force to overwrite
```

Both explain the situation, name the real choices, and hand over copy-pasteable commands. The removed-flag errors (`main.go:923-926`) do the same for deprecation. That standard exists in this codebase; it just isn't applied uniformly.

**Raw markdown and YAML in the terminal.** `Render()` is a `cat` (`docs.go:109-130`, printed by `main.go:153-157`). Three of five topics open with frontmatter:

```
$ afs docs contract
---
description: Self-describing root of this agentsfs. Read this first — it teaches any agent how to read, write, and maintain everything here.
agentsfs_contract: 0.9.0
---
```

`agentsfs_contract:` reads as debug output. Headers print as literal `#`, emphasis as literal `**`, links as literal `[text](path)`. `docs/hub.md` contains a 1,022-character paragraph and `template/AGENTS.md` a 1,162-character one, relying entirely on terminal soft-wrap; `afs docs --all` is 792 lines with no pager.

**Topic descriptions aren't parallel and don't help you choose.** Four are noun phrases naming what the topic *is*; `hub`'s is an imperative naming what to *do* ("connect an agentsfs to a hosted Hub and upload it") — it reads like a command description, not a topic description. Worse, `agent-start` ("agent-facing primer for understanding, setting up, and using AgentsFS from a fresh workspace") and `setup` ("full setup guide for humans and agents") describe the same territory in the same words. Having read both: `agent-start` is a 147-line interview-then-act script; `setup` is a 424-line exhaustive reference. Nothing in the descriptions conveys short-vs-exhaustive, so a lost reader opens both.

**Command descriptions break their own grammar mid-group.** Nineteen of 24 lead with a verb. Five don't, and they sit adjacent to verb-led siblings — the `Orient` group runs `status`: "summarize discovered…" (verb), then `tree`: "the tree with descriptions…" (noun), then `search`: "ranked search; --context hydrates…" (noun). The offenders are `tree` (`docs.go:63`), `search` (`:64`), `roles` (`:65`), `doctor` (`:67`), `backlinks` (`:68`).

**Skill descriptions: one weak trigger out of four.** `agentsfs-remember`'s is the model — it quotes literal user phrases ("remember this", "save this for next time", "add this to my memory/notes") plus a generalized fallback. `agentsfs-garden` and `agentsfs-adopt` are concrete too ("an Obsidian vault, a Notes folder, or a pile of markdown"). `agentsfs-setup`'s is circular: "Use when the user wants to set up agentsfs" restates the skill's own name back at the matcher and gives an agent nothing to match against natural language that doesn't already contain the product name. It also fails to defer to `agentsfs-adopt` — "point a project at an existing agentsfs" reads to a shallow match as covering any existing folder of notes, when it means an instance that already has the contract.

**What's already good, and should not be touched.** `template/AGENTS.md` is uniformly imperative with essentially no hedging (one benign "as needed" at `skills/agentsfs-remember/SKILL.md:24`). `prompts/connection-snippet.md` is a byte-for-byte match of `connectionBlockWithJournal()` (`internal/core/register.go:90-107`). `afs docs` with no arguments and with a bad topic both print the list *and* name a starting point rather than dumping a bare index. `afs contract diff`'s three-way structure answers the right question. Doctor's contract findings correctly refuse to let an agent downgrade a newer contract (`doctor.go:110-117`).

---

## 5. Obsolete positioning

**No reachable topic carries obsolete positioning.** Grepping every embedded doc, prompt, skill, and the shipped contract for "no managed hosting" / "never stores" / "does not store" / "not hosted" returns nothing in `docs/hub.md`, `docs/setup.md`, `docs/agent-start.md`, `template/AGENTS.md`, `README.md`, `prompts/*.md`, or `skills/*/SKILL.md`. The contract's "Backup and sync" section already reads correctly: "**The agentsfs Hub** — a hosted home (`hub.agentsfs.ai`, or a self-hosted one)… Repos are private by default… It stores real git, so `git clone` still works and there is no lock-in." The old line was removed deliberately in `02ea79f`; `docs/archive/hub-execution-plan.md:145` had tracked it as a to-do. That work is done.

**The obsolete copy still ships in the binary anyway**, as a side effect of the wildcard embed at `embed.go:17`. These strings are present in the released artifact and extractable with `strings`:

- `docs/archive/execution-plan.md:178` — "**2026-06-16 — Managed hosting removed from the product direction.** agentsfs stays simple: local files plus ordinary git. Backup and cross-device sync are handled by private GitHub/GitLab/self-hosted remotes… instead of hiding a managed hosting layer." It is marked `(SUPERSEDED 2026-07-04 — see below.)` in place, which is correct as a journal entry — but journal entries have no business inside a distributed binary.
- `docs/archive/hub-execution-plan.md:40` — quotes the shipped contract as still saying "Do not assume managed hosting exists." No longer true of the contract; the sentence ships regardless.
- `docs/archive/hub-execution-plan.md:145` — "**Contract text update — deferred until the hub ships.** `template/AGENTS.md` … currently says 'Do not assume managed hosting exists.' That stays true until the hub is real." Confirmed present in the binary: `strings afs | grep "Do not assume managed hosting exists"` returns two hits, both from this file.

Adjacent to positioning rather than contradicting it, both worth fixing in the same pass: **D12** (`docs/hub.md:58` promises frictionless "self-serve signup" while the code runs an allowlist/waitlist gate — and the canonical positioning says waitlists are open, which is a *better* thing to say than nothing) and **D2** (the only reachable description of the managed agent describes an architecture that was retired). Neither denies that Hub exists; both misdescribe how a person actually gets in and what they get.

One omission, flagged not asserted: neither `template/AGENTS.md` nor any reachable topic states that AgentsFS itself is open source and self-hostable end-to-end. `README.md:177` says it ("it's part of this open-source project"), but the contract's audience is an agent already inside an instance, so its absence there is defensible. If "take it, self-host it, make it your own" is the honest center of the pitch, it currently appears in exactly one place a CLI user will never read.

---

## 6. Concrete fixes

Sizes are rough implementation estimates, not including review.

**A note on the tests, verified rather than assumed.** Adding a file-backed topic to `internal/docs/topics` requires exactly two things to keep `TestTopicsRenderAndAreDocumented` green (`internal/docs/docs_test.go:30-38`): (a) the embedded file must contain the substring `description:` *anywhere* — it does not have to be real frontmatter, and `docs/setup.md` currently passes on unrelated prose at `docs/setup.md:278`; (b) `README.md` must contain the topic's `Path` as a literal substring. `README.md:171` already contains `prompts/onboarding.md` and `prompts/gardening.md`; `README.md:191,271` already contain `docs/internals/hosted-agent.md`. **`prompts/adopting.md` is not in README** and would need a link added. `deploy/self-host.md` is in `README.md:177` but is *not embedded*, so a `self-host` topic additionally requires extending `embed.go:17`. Separately, `TestCommandDocsStayInSync` (`docs_test.go:45-64`) requires that for every row in the command table, `README.md` contains the literal string `afs <verb>` — so adding `afs hub logout` to the table needs README to mention `afs hub` (it does).

1. **Rewrite the usage strings in `internal/docs/docs.go`.** Fix `embeddings` to `afs embeddings [status] | setup <openai|voyage> [--yes] | clear [--yes]` (D5); add `[--yes] [--force]` to `contract` so it matches `main.go:394` (D8); add `afs hub logout` as a new row (D9); add `-d N` to `tree`, `--limit N` to `search`, `-y` where accepted, and the `link`/`clone`/`get`/`repos`/`ls`/`--vendor` aliases — or deliberately drop the aliases from the code (D18). Change `afs mcp`'s description from "the same capabilities" to something honest, e.g. "serve the read, search, and hub tools over MCP (see `afs docs mcp`)" (D3). Fix `skills`'s description to name `agentsfs-setup/-remember/-adopt/-garden` and say it materializes them to disk (D7). Fix `uninstall`'s to mention the skills cache and global connection blocks (D15). Add doctor's exit-1-on-error contract (D16). Standardize on "workspace" (D21). **~40 lines changed, one file.**

2. **Make `afs help` readable.** Have `CommandUsage()` reuse `commandOverview()`'s group headers, and wrap descriptions to `min(terminalWidth, 100)` with a hanging indent instead of the fixed `%-78s` pad. Make bare `afs` exit 0 (`main.go:48-52`). **~30 lines, `internal/docs/docs.go` + `cmd/afs/main.go`.**

3. **Add `case "--help", "-h":` to every subcommand flag loop** (D6), printing that command's usage at exit 0 — mirroring `uninstall.go:51-53`. Cheapest version: a shared `helpFor(cmd string)` helper that looks the usage line up from the `docs.Commands()` table, so it can never drift from the table again. **~60 lines across `cmd/afs/`.**

4. **Fix `afs status`'s description (D1)** — the only outright false claim shipped into every instance. Make `internal/core/status.go:391` use the same INDEX→AGENTS→README fallback as `internal/hub/web.go:2037-2043`, including `core.IsPlaceholderRootDescription` filtering, and either add a label column to the human table or amend `template/INDEX.md:7` to stop naming `afs status`. Also fix the stale comment at `internal/core/doctor.go:119-124`. **~25 lines.**

5. **Replace `docs/hub.md`'s "Talk to your agent" section (D2)** with a summary drawn from `docs/internals/hosted-agent.md:7-11`: Eve is a shared hosted application, the Hub stays the authority for identity/permissions/commits, isolation comes from authenticated scoping and revision-pinned compare-and-swap writes rather than a per-user VM, and every accepted change is a real git commit. Add the "waitlists are open" caveat to `docs/hub.md:58` (D12). Fix `scratch/` → the instance's scratch role in `docs/hub.md:32` and `cmd/afs/hub.go:70` (D11). **~15 lines of prose.**

6. **Give the "not inside an agentsfs" error a next step (D6 companion).** `internal/core/instance.go:44` gains a second line: "Run `afs setup` to create one here, or `afs status <dir>` to find an existing instance nearby." Same nudge in `afs status`'s empty-result branch (`main.go:759-761`). Split doctor's `contract-version` finding on `os.Stat(AGENTS.md)` so a missing file says so (D17). Point `journal-backlog` at `afs skills` (D20). Fix `1 warnings` (D19). **~20 lines.**

7. **Narrow `embed.go:17` (§5).** Replace the `docs/*.md` wildcard with an explicit list of the files that are actually reachable or linked, and add a test asserting that every file matched by the embed pattern is either wired to a topic or on an explicit allowlist. This removes ~180 workspace of internal planning notes, the superseded managed-hosting decision, and `docs/archive/hub-ui-audit-2026-07-27.md` from every released binary in one edit. **~10 lines + a ~30-line test.**

8. **Strip frontmatter and render minimal markdown in `Render()`.** Drop a leading `---…---` block; optionally bold headers and strip emphasis markers. Even frontmatter-stripping alone removes the worst of it. **~30 lines, `internal/docs/docs.go`.**

9. **Guard the table against the drift class that caused all of this.** Extend `cmd/afs/main_test.go:449`'s regex to capture every literal in a `case` clause, widen its scan window past `func runDocs`, and add scanning of `hub.go`'s subcommand switch, `runEmbeddings`, and `runContract`. Then add the reverse assertion `docs_test.go` is missing: every flag string parsed in `cmd/afs/` appears in the table's usage line for its command. **~80 lines of test.**

### Proposed new `afs docs` topics

Each brief is one paragraph. All five need a `topics` entry in `internal/docs/docs.go:24-50`, an addition to the topic enum in **both** MCP surfaces (`internal/mcpserver/server.go:41` and `internal/hub/mcpapi.go:460` — both currently read `agent-start, setup, hub, contract, commands, list, or all`), and, per the verified test requirements above, a README link where noted.

10. **`onboarding` → `prompts/onboarding.md`.** The first-session prompt an agent runs after `afs setup`. Closes D14's most-hit half; `docs/setup.md:130` already tells agents to follow it by name. Has `description:` frontmatter; `README.md:171` already links the path, so no README change. **~5 lines.**

11. **`gardening` → `prompts/gardening.md`.** The scheduled-maintenance prompt whose worklist is `afs doctor`'s output. Also gives doctor's `journal-backlog` finding (D20) something to point at. Frontmatter present; `README.md:171` already links it. **~5 lines.**

12. **`adopting` → `prompts/adopting.md`.** The additive-only flow for turning an Obsidian vault or folder of notes into an agentsfs. `docs/setup.md:91` already sends agents here by name. Frontmatter present, **but `README.md` does not contain the string `prompts/adopting.md`** — add it alongside the existing two links at `README.md:171` or the test fails. **~5 lines + one README link.**

13. **`agent` → `docs/internals/hosted-agent.md`.** The managed product's flagship surface: how the Hub authenticates Eve, the header-stripping handoff, PAT scoping, revision-pinned reads and compare-and-swap writes, and the focus/conversation model. Today the binary's only account of this is the stale paragraph at `docs/hub.md:62`. Wiring it also gives fix 5 somewhere to point. Frontmatter present; `README.md:191,271` already link the path. **~5 lines.**

14. **`mcp` → a new `docs/mcp.md`.** The largest genuine coverage gap (§3.1) and the only one needing new prose rather than new wiring. One page: the twelve local tools with a one-line purpose each, what is deliberately *not* exposed (`init`/`setup`/`connect`/`contract`/`hub login` — anything that changes the user's machine or credentials stays a human command), the `--context` asymmetry (D4) stated plainly rather than papered over, and a short section on the Hub's separate hosted MCP endpoint with its different tool set (`search`, `fetch`, `list_workspaces`, `tree`, `docs`, `write`), which is how an outside agent reaches a managed workspace at all. Needs `description:` frontmatter and a `docs/mcp.md` link in README. Also lets `docs.go:47`'s "CLI and MCP command overview" become true by pointing at it. **~120 lines of new prose + 5 lines of wiring + one README link.**

15. **`self-host` → embed `deploy/self-host.md` (D13).** Requires extending `embed.go:17` to include `deploy/self-host.md`, verifying that file has a `description:` line, and confirming `README.md:177`'s existing `deploy/self-host.md` link satisfies the path check. Given that "open source, take it and run it yourself" is the honest center of the pitch, having zero self-hosting bytes in the binary is the largest gap between what the product claims to be and what it hands you. **~8 lines of wiring, no new prose if the existing doc holds up on read.**
