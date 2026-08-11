---
description: Session — inline Markdown To rendering on note pages + full-view back-nav shipped and deployed (c2b154b); bundle re-vendored @ f5ece9f so Hub live boards carry the Add fix; both owner-verified in production.
---
## Learned / decided
- The owner's two asks landed as one architecture: inline rendering is an iframe of the existing /mdto/ page with ?embed=1, because a srcdoc frame INHERITS its embedder's CSP — nesting keeps the rendered document's default-src-none policy intact three documents deep while the note page's policy stays untouched (delta: zero). One gate, one save loop, one conflict panel; conforming files open as their document by default with the markdown one visible toggle away; JS-off serves markdown and never fetches the bundle.
- The re-vendor @ markdownto f5ece9f closed the Add bug on Hub live boards (an iframe without allow-forms silently kills native submission; the board now drives submits itself — no sandbox widening anywhere). Owner pressed Add on production and confirmed.
- Landing mechanics for a shared dirty checkout, twice-proven today: cherry-pick the slice onto origin/main in a clean worktree, test at that exact commit, push from there; the local branch reconciles when the tree quiets. The gardening PR merge (other session) rode into both Hub deploys — main deploys as main.
## Open
- This repo's KB Hub projection sync still refused while the local checkout carries uncommitted WIP (correct behavior; origin has everything).
- Adoption chain for the workspace shape here: ^contract-backlog-spec (rule-13 wording + migration) once the owner sequences it with contract 0.11.0.
