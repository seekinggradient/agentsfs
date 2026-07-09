---
description: Append-only session log — one note per unit of work, pending consolidation into durable notes by the gardener.
---

# journal

Episodic session notes. When you finish a unit of work, append one entry here saying what happened this session: what you learned or decided, what you ruled out, what's still open, and what you already wrote into the durable notes directly (a "Written directly" section, so the gardener doesn't re-process it).

This is the floor, not the ceiling: prefer updating durable notes directly too. The journal only guarantees nothing is lost between sessions.

Rules:

- **Append-only.** One file per session; never edit or reorganize an earlier entry. Add a new one.
- **Filename `YYYY-MM-DD-<slug>.md`**, with a one-line `description:` — the description is the entry's timeline label.
- **Consumed and deleted by the gardener.** It folds each entry's facts into the durable notes and removes the file. An empty journal is the healthy state; git history is the archive of every entry.

Example entry:

```markdown
---
description: Session — booked Kauai flights; ruled out Poipu; helicopter tour still open.
---
## Learned / decided
- Booked HA 50, SFO→LIH, Dec 14–22 ($612/person, confirmation in email).
- Ruled out Poipu: nothing decent under $500/night for those dates.
- [[Dana]] prefers the north shore.
## Open
- Doors-off vs doors-on helicopter; check [[Jack Harter]].
## Written directly
- Updated [[trip-plan]] with flight confirmations.
```
