---
description: Session — automatic Hub gardening merged and deployed with manual fleet runs, trusted cursor relays, and persisted per-repository progress.
---

## Learned / decided

- Automatic gardening is live on Hub and Eve. Both account-level switches default on, repository selection defaults on, and the recent-push gate remains seven days by default.
- Manual “Run selected repositories now” passes deliberately ignore the recent-push gate. A production run queued all 22 selected workspaces.
- Eve processes one repository per bounded invocation and relays the next cursor through Hub. The external trusted hop avoids Vercel's self-recursion limit while retaining the shared-secret boundary.
- Each pass is bounded to six successful gardening writes and a four-minute soft deadline. Failures advance to the next repository instead of stranding the fleet.
- The account pane now persists and renders per-repository `queued for gardening`, `gardening now`, `gardened … ago`, and `last attempt failed … ago` states. Production verified both a completed repository and the next running repository.
- Eve's fully drained `session.waiting` boundary is the normal ready-for-next-turn state, not a failed maintenance turn. Genuine session failures and approval parks remain failures.

## Open

- Verify the first daily Vercel cron pass at 10:00 UTC end to end; today's production proof used the manual all-repository path through the same executor.

## Written directly

- Updated [[backlog/INDEX#^automatic-hub-gardening]] with the completed deployment, manual run, and progress UI; retained scheduled-run verification as the remaining gate.
