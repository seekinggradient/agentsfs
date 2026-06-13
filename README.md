# agentsfs

**A portable, user-owned memory for AI agents — files + conventions + a thin toolkit, and nothing else.**

An agentsfs instance is a plain git repo. Knowledge lives in markdown with one-line `description:` frontmatter and `[[wikilinks]]`; any file type can live alongside it. A self-describing root `AGENTS.md` teaches any agent — Claude Code, Codex, anything — how to read, write, and maintain it. Your agents do the thinking; agentsfs makes what they learn survive across sessions, tools, and machines.

No server. No account. No LLM inside. `git clone` is the exit ramp.

## Quickstart

```sh
go build -o afs ./cmd/afs     # packaged releases: scripts/release.sh (channels TBD)
afs init ~/memory             # creates the instance, offers to register it
                              # with your agents' AGENTS.md / CLAUDE.md
```

Then point any agent at it — or let the registration block do it — and work normally. See [prompts/onboarding.md](prompts/onboarding.md) for the first session and [prompts/gardening.md](prompts/gardening.md) for scheduled maintenance.

## Skills (Claude Code / Agent Skills format)

The same behaviors, packaged as installable skills — `prompts/` stays the harness-neutral canonical text, `skills/` is the skill-native wrapper:

```sh
cp -R skills/agentsfs-* ~/.claude/skills/    # personal; or a project's .claude/skills/
```

- `agentsfs-setup` — create a vault, register projects, seed the first knowledge
- `agentsfs-remember` — "remember this": save conversation knowledge per the contract
- `agentsfs-garden` — doctor-driven maintenance and consolidation

## The toolkit

The contract works with zero tooling (`ls`, `grep`, git). The CLI adds what plain tools do poorly:

```
afs tree         the whole tree with descriptions and freshness — one-call orientation
afs search       ranked full-text search; --semantic with an embedding key (optional)
afs backlinks    every [[wikilink]] pointing at a file
afs rename       move a file and rewrite all links to it
afs doctor       deterministic health check; the gardener's worklist
afs mcp          the same capabilities over MCP, for harnesses that can't shell out
```

All derived state lives in `.agentsfs/` (one SQLite file), is never committed, and rebuilds from the files with `afs reindex`.

## Docs

- [docs/agentsfs-source-of-truth.md](docs/agentsfs-source-of-truth.md) — what this is and why; the settled design decisions.
- [docs/execution-plan.md](docs/execution-plan.md) — how it's being built.
- [template/AGENTS.md](template/AGENTS.md) — the contract itself, as agents read it.

Status: pre-release, under active construction.
