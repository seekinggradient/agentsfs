---
description: Session — added current, stale, and missing narration artifact states to Hub file views.
---
## Learned / decided
- Hub stays synthesis-provider-ignorant. It reads only `narrate-artifacts@0.1`, validates its version-addressed paths and referenced blobs, and compares the pointer's source SHA-256 to the exact manuscript bytes being rendered.
- A current or stale recording uses the existing authenticated `/raw/` media route. The Markdown To page CSP now admits only same-origin media; no remote media or broader network access was added.
- Readers may play existing current or stale recordings. Only writers receive generation links, and a missing recording has no dead player for readers.
- The pinned Markdown To browser bundle was re-vendored so Hub recognizes the renamed `result.narrate` IR. The complete `internal/hub` package passed in 57.690 seconds.
## Open
- Merge and deploy the Hub branch before this view appears on hub.agentsfs.ai.
## Written directly
- Added and closed [[backlog/INDEX#^markdownto-narrate-artifacts]] under the Markdown To integration workstream.
