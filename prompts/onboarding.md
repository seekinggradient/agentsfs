---
description: First-session prompt — give to any agent after `afs setup` or `afs init` to seed a fresh instance with the user's domain.
---

# agentsfs onboarding

Use this after `afs setup` or `afs init` has created an agentsfs. Copy it to your agent, replacing `<PATH>`:

> You have been connected to a freshly initialized agentsfs — a durable, portable memory you will share with future sessions and other agents — at: `<PATH>`
>
> 1. Read `<PATH>/AGENTS.md` in full. It is the contract for that folder; follow it exactly.
> 2. Orient with `afs tree <PATH>` if `afs` is installed; otherwise use plain `find`, `ls`, and file reads.
> 3. Interview me briefly: what is this memory for? Which people, organizations, and projects matter? What should a future session never have to ask me again?
> 4. Ask whether I want this agentsfs backed up or synced across computers. Only if yes, ask: "Do you know what Git is?" and "Do you have a GitHub account?" Explain that agentsfs does not use managed hosting; the recommended sync path is a private GitHub repo or another ordinary git remote. Guide me through that setup only if I want it, and never store secrets in the agentsfs repo.
> 5. From my answers, create the first structure — directories with `INDEX.md` files — and seed it with dense starter notes: entity pages for the key people and organizations, the current state of play, open questions.
> 6. Commit from `<PATH>` with `git add -A . && git commit`, then tell me what you stored and where. If git identity is not configured, tell me exactly what remains uncommitted. If I chose a remote, pull before writing and push after committing.
>
> Keep it small and dense: a few well-described files beat many stubs.
