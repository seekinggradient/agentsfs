---
description: First-session prompt — give to any agent after `afs setup` or `afs init` to seed a fresh instance with the user's domain.
---

# agentsfs onboarding

Use this after `afs setup` or `afs init` has created an agentsfs. Copy it to your agent, replacing `<PATH>`:

> You have been connected to a freshly initialized agentsfs — a durable, portable memory you will share with future sessions and other agents — at: `<PATH>`
>
> 1. Read `<PATH>/AGENTS.md` in full. It is the contract for that folder; follow it exactly.
> 2. Orient with `afs tree <PATH>` if `afs` is installed (on a large instance, scope with `afs tree <PATH>/<dir>` or cap breadth with `--depth N`); otherwise use plain `find`, `ls`, and file reads.
> 3. Interview me briefly for domain context, not taxonomy: what is this memory for? Which people, organizations, projects, documents, systems, and decisions matter? What should a future session never have to ask me again?
> 4. Ask whether I want this agentsfs backed up or synced across computers. Only if yes, offer the two paths from the contract's "Backup and sync" section: the agentsfs Hub (`afs hub login` then `afs hub push`; private by default, real git, no lock-in) or an ordinary private git remote (ask "Do you know what Git is?" and "Do you have a GitHub account?" first). Guide me through setup only if I want it, and never store secrets in the agentsfs repo.
> 5. From my answers, choose the first structure yourself — directories with `INDEX.md` files — and seed it with dense starter notes: entity pages for the key people and organizations, the current state of play, open questions. Do not ask me how to structure the knowledge base; make a reasonable structure, explain it briefly, and reorganize later as the memory grows.
> 6. Before committing, append your first session note to `<PATH>/journal/` — one file, `YYYY-MM-DD-<slug>.md` with a `description:` line — recording what you seeded and what's still open (see `journal/INDEX.md`). Every future session ends its units of work the same way.
> 7. Commit from `<PATH>` with `git add -A . && git commit`, then tell me what you stored and where. If git identity is not configured, tell me exactly what remains uncommitted. If I chose a remote, pull before writing and push after committing.
>
> Keep it small and dense: a few well-described files beat many stubs.
