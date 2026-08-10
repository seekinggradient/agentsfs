---
description: Implementation plan for safe two-way synchronization between an embedded AgentsFS directory in a host repository and its standalone Hub projection.
---

# Embedded Hub projection two-way sync

## Outcome

Make an embedded AgentsFS projection a real two-way synchronization surface without uploading the enclosing application, force-pushing, or changing standalone repository behavior.

The durable invariant is:

> For every linked embedded instance, AgentsFS can identify a host commit and a Hub commit that represent the same projected knowledge-base state. Pull preserves the Hub commit in host ancestry; push projects host changes on top of that exact Hub commit.

This is the missing protocol behind the current product promise. The Hub is an ordinary Git remote for a standalone instance. For an embedded instance it is a remote for a **translated repository**: the host stores the knowledge base under a prefix such as `agentsfs/`, while the Hub stores that directory's contents at repository root. Translation is unavoidable as long as AgentsFS preserves all three properties:

1. the instance shares history with its host application;
2. Hub never receives files outside the instance; and
3. Hub may create its own commits through gardening, agents, browser editing, or application writeback.

## Verified failure

An ordinary embedded push uses `git subtree split --prefix=<prefix> HEAD`. Host-only updates work: Git deterministically extends the prior synthetic projection history. The failure begins when Hub creates a commit of its own.

```text
last shared projection: P0

Hub:                 P0 -- G1       G1 is a Hub-side write
next host projection: P0 -- P1      P1 contains host-side work
```

`G1` and `P1` are siblings. Refusing to push `P1` over `G1` is correct; a force-push would destroy Hub work.

Copying or merging `G1`'s content under the host prefix is not sufficient by itself. A plain later split does not know that root-level Hub commit `G1` corresponds to the prefixed content in the host merge, so `G1` is still absent from the projected ancestry. The current `git subtree pull` hint therefore over-promises: the first pull may reject unrelated histories, and a successful manual fold still does not make the existing push path accept the result.

The claim that every split mints an entirely unrelated history is too broad. The verified, narrower fact is that a plain split has no mapping for foreign root-level Hub commits. Git's existing `git subtree split --onto=<hub-base>` supplies that mapping. Against the pinned markdownto reproduction, plain split omitted Hub commit `e2fa739`; the same split with `--onto=e2fa739` made that commit an ancestor and produced a tree byte-identical to the host's `agentsfs/` directory. A normal host-only update also remained a fast-forward descendant with `--onto=<recorded-base>`.

## Chosen architecture

Use **projection-base tracking plus `afs hub pull`**, implemented with Git's subtree-aware merge and `split --onto` machinery.

Do not make `git subtree split --rejoin` part of every push. Rejoin would record correspondence using stock Git trailers, but it would add bookkeeping merges to the host after one-way publications, require a clean enclosing worktree, and weaken today's useful property that unrelated application work outside the instance does not prevent publication. A merge caused by actual Hub-side work is honest host history; a merge after every push is not.

Do not convert embedded instances to nested repositories or submodules as part of this fix. That would make Hub an ordinary remote but retire the current shared-host-history product contract.

## Safety invariants

The implementation must enforce all of these mechanically:

1. **No force updates.** Neither main nor projection metadata refs may be force-pushed. No error hint may suggest force.
2. **Exact target identity.** An embedded instance uses its instance-local link or an explicit `owner/repo`. Folder basename and the enclosing repository's compatibility `hub` remote are never authoritative.
3. **Application isolation.** Every published main tree is verified equal to `HOST_COMMIT:<instance-prefix>` before upload. No host-root commit is sent to Hub.
4. **Remote preservation.** The recorded Hub base must be an ancestor of the projected tip. If Hub main moved beyond the recorded base, push refuses before mutation and directs the user to `afs hub pull`.
5. **Atomic publication.** Hub main and recoverable projection metadata advance together or neither advances.
6. **Truthful conflicts.** Genuine overlapping host/Hub edits become ordinary Git conflicts under the instance prefix. Metadata does not advance until resolution is committed.
7. **Recoverable machine state.** Deleting `.agentsfs/hub.json` loses a cache, not the correspondence. Recovery still requires the exact Hub target; it never guesses one.
8. **Standalone compatibility.** Standalone instances keep their ordinary fetch/merge/push behavior and existing Hub writer surfaces.

## Authoritative state

### Local cache

Evolve instance-local `.agentsfs/hub.json` to schema 2. For an embedded instance it records at least:

```json
{
  "schema_version": 2,
  "source_mode": "embedded-projection",
  "projection_id": "stable opaque id",
  "remote_url": "https://hub.agentsfs.ai/owner/repo.git",
  "repository": "owner/repo",
  "publish_branch": "main",
  "sync_base": {
    "host_commit": "host commit whose prefix was synchronized",
    "hub_commit": "corresponding Hub commit",
    "projected_tree": "root tree shared by those states"
  }
}
```

The exact field names are implementation details; the semantics are not. Write the cache atomically only after remote verification or a completed pull merge.

### Recoverable Hub ledger

Store the correspondence in an append-only, per-projection Git ref on Hub, for example:

```text
refs/agentsfs/projections/<projection-id>
```

Each ledger commit points to its predecessor and contains a small versioned record with the host commit id, Hub commit id, and projected tree id. It contains no host tree and no application content. Push the ledger and `refs/heads/main` in one ordinary atomic push. Because the ledger itself is append-only, concurrent publishers race through normal non-fast-forward semantics; no `--force-with-lease` exception is needed.

A dedicated ref is preferable to relying only on commit-message trailers: it can describe successful pushes without modifying user commit messages or adding marker commits to Hub main, and Hub can inspect it to classify the repository. Subtree trailers on pull merges remain useful host-side evidence.

### Hub repository mode

Record a server-side source mode and protocol version:

```text
source_mode: standalone | embedded-projection | unknown
projection_sync_version: 1 | 2
```

Hub-created repositories are standalone by construction. A version-2 embedded publication identifies itself through its authenticated preparation call and/or projection ledger ref. A non-empty repository may not silently change modes; migration must be explicit and audited.

All Hub-originating commit surfaces consult one policy function. That includes automatic gardening, hosted-agent and MCP commits, the browser editor, Markdown To board writeback, the save API, and safe contract upgrades. Git smart-HTTP pushes remain allowed. Until a projection advertises sync version 2, Hub-side commit creation is disabled for it with an explanatory status. Once version 2 is established, those writers are enabled: the gate is part of the final protocol, not a substitute for pull.

## Exact pull protocol

`afs hub pull` gains an embedded-instance mode selected by the normal instance resolver. Repository-clone/merge behavior for standalone downloads remains a separate code path.

### 1. Preflight

- Resolve the embedded instance, enclosing worktree, and prefix.
- Resolve the exact Hub target from valid instance metadata or an explicit `owner/repo`; fail if neither exists.
- Reject unresolved conflicts and staged/unstaged/untracked changes inside the instance. Changes outside the prefix may remain when Git can preserve them.
- Fetch Hub main and the projection ledger into per-instance namespaced refs.

### 2. Recover and validate the base

- Prefer a valid local `sync_base` whose host commit exists and Hub commit is an ancestor of fetched Hub main.
- Otherwise inspect the fetched ledger for the newest record whose host commit is reachable in this host clone and whose recorded projected tree equals that host commit's prefix tree.
- Validate that the selected host base is an ancestor of current host HEAD. A rewritten host history requires explicit adoption; never silently choose a different base.
- Validate that the base Hub tree equals the base host prefix tree. A mismatch is corrupt/stale state and stops before mutation.

### 3. Classify the pull

Let `R` be fetched Hub main.

- **No remote work:** `R == base.hub_commit`; report already current.
- **Already folded with ancestry:** `R` is already an ancestor of host HEAD. Verify content and advance/recover metadata without another merge.
- **Already folded by content only:** current host prefix tree equals `R` but `R` is not in host ancestry. Create an explicit no-content ancestry bridge merge with subtree trailers; do not pretend content equality alone is durable correspondence.
- **Remote work to integrate:** merge `R` into host HEAD using the recursive/ort subtree strategy so Hub root maps to `<prefix>/`. The merge's second parent is `R`; its message records the prefix, previous host base, and Hub tip using stable trailers.

### 4. Conflicts and continuation

On conflict, leave Git's normal index/worktree conflict state intact and save only a continuation record naming the exact target, prefix, expected Hub tip, and old base. Print standard resolution instructions followed by `afs hub pull --continue`.

`--continue` verifies that:

- no unmerged paths remain;
- the resolved commit contains the expected Hub tip in its ancestry;
- files outside the instance were not introduced from the root-level projection; and
- the continuation target still matches the instance link.

Only then update local sync state. `--abort` delegates to Git's merge abort and removes the continuation record without advancing metadata.

## Exact push protocol

Standalone push remains unchanged. Embedded push performs:

### 1. Resolve and fetch

- Require valid instance-local linkage or an explicit first-link target. Remove basename fallback. Never treat a repository-wide compatibility remote as proof of an embedded instance's target.
- Fetch Hub main and the projection ledger.
- Recover and validate the sync base exactly as pull does.

### 2. Require the expected remote

- If fetched Hub main differs from `base.hub_commit`, stop before projection or push.
- If the recorded base is an ancestor of fetched main, report remote work and say `run afs hub pull`.
- If it is not an ancestor, report remote history replacement/corruption separately. Do not paper over it as an ordinary pull.

### 3. Project on top of the base

For an embedded prefix `P`, run the equivalent of:

```sh
git subtree split --prefix=P --onto=<base.hub_commit> HEAD
```

The projector may wrap this command for diagnostics, but should not reimplement commit translation unless Git proves insufficient in a covered test. `--onto` teaches the splitter that the fetched root-level Hub history is already valid projection history, including Hub commits preserved as pull-merge parents.

Before upload verify:

- `base.hub_commit` is an ancestor of the projected tip;
- the projected tip's root tree equals `HEAD:P` exactly; and
- the projected object graph contains no host-root tree chosen for publication.

### 4. Publish atomically and record

- Construct the next append-only ledger commit recording `(host HEAD, projected tip, projected tree)`.
- Atomically push projected tip to `refs/heads/main` and ledger tip to its namespaced ref, both as ordinary fast-forwards.
- Re-read both remote refs and verify exact expected ids.
- Only after verification update `.agentsfs/hub.json` and the local tracking refs.

A crash before the atomic push changes nothing remotely. A crash after the push but before the local cache write is recovered from the ledger on the next command.

## Target-link semantics

The current fallback from a missing `.agentsfs/hub.json` to `filepath.Base(instance)` is unsafe and must be removed. The enclosing Git repository may contain several embedded instances, and even a unique instance's folder name commonly differs from its Hub repository name (`markdownto/agentsfs` publishes to `seekinggradient/markdownto`, not `seekinggradient/agentsfs`).

Add an explicit linking/adoption flow, approximately:

```text
afs hub link owner/repo --instance PATH
```

Linking an empty/new target is straightforward. Linking a non-empty target fetches its mode and ledger and requires one of the migration proofs below. The command stores the exact credential-free URL only after proof succeeds. Refusal and status hints must render that exact target, never a reconstructed basename URL.

## Migration

Migration must be first-class because projections had Hub writers before protocol version 2.

### Existing standalone repositories

Mark known standalone repositories as standalone and leave their refs and write surfaces untouched. Hub-created instances can be classified from creation provenance. Repositories that cannot be classified safely remain `unknown` until an authenticated client or owner classifies them; scheduled writers skip unknown rather than guessing.

### Embedded projection, Hub not ahead

Use existing schema-1 `last_push` data as the initial base after verifying host prefix tree, projected commit tree, and current Hub main. Create the projection ledger on the first version-2 push without changing Hub main unnecessarily.

### Hub commits already folded with ancestry

If the Hub tip is a host ancestor/merge parent and the corresponding prefix tree is present, adopt that pair. The next `split --onto=<hub-tip>` produces a true fast-forward projection. This is the original markdownto `eee42a2` / `e2fa739` case.

### Hub content copied without ancestry

If trees are identical but the Hub commit is not reachable from host HEAD, create the verified no-content bridge merge described under pull. Equality permits the bridge; it does not permit metadata-only adoption because a later clone must recover the ancestry.

### Genuine divergence

Start from the last verified schema-1 base or recovered ledger entry and perform normal subtree-aware three-way pull. Identical changes already applied on the host collapse naturally; overlapping changes conflict normally. After resolution, push with `--onto=<pulled-hub-tip>`.

The live markdownto repository has progressed beyond the pinned acceptance case: after the manual fold through `e2fa739`, Hub gardening created further commits, including changes to `INDEX.md`, `backlog/INDEX.md`, and a new gardening journal entry. A current pull correctly produces a real backlog conflict against concurrent host edits. The migration must handle both the pinned already-folded fixture and the evolving live pair.

### Missing or corrupt base

Never infer a base solely from matching repository/folder names. With an explicit target, allow a deliberate `--adopt` recovery only when ancestry and tree proofs identify one unambiguous correspondence. Otherwise require explicit host and Hub commits, show the tree comparison, and obtain user confirmation. Adoption writes a new append-only ledger entry; it never replaces history.

## Implementation sequence

The feature ships as one protocol, although commits may land in this order:

1. **State model and classification:** schema-2 types, Hub repository mode/version, append-only ledger primitives, and central Hub-write eligibility policy.
2. **Strict identity and truthful diagnostics:** remove basename and compatibility-remote target guessing; replace the impossible subtree-pull hint with exact `afs hub pull` guidance; add explicit link/adopt plumbing.
3. **Base-aware projection:** parameterize embedded projection by Hub base, use `git subtree split --onto`, and add ancestry/tree/isolation verification.
4. **Embedded pull:** fetch, base recovery, subtree-aware merge, already-folded detection, conflict continuation/abort, and metadata advancement.
5. **Atomic publication:** push main plus ledger, remote verification, crash-recovery tests, and race tests.
6. **Legacy migration:** schema-1 upgrade and fixtures for every case above, including the markdownto commits.
7. **Hub writer integration:** route every Hub commit surface through mode/version eligibility; expose the state in account/repository UI and APIs.
8. **Docs and contract:** document clone pull versus embedded sync pull throughout `docs/`, the local and Hub MCP descriptions, status next actions, and error recovery. Decide and version the rule-12 contract wording rather than editing it as incidental docs.
9. **Rollout:** classify current hosted repositories, migrate known projections, deploy CLI and Hub compatibility in an order that never exposes a version-1 projection to Hub writers, then run the live convergence test.

## Acceptance tests

### Core history behavior

- Initial embedded push publishes only the prefix.
- Repeated host-only pushes are ordinary fast-forwards and preserve prior projection ancestry.
- Hub write -> embedded pull -> host write -> embedded push produces one connected Hub history containing every commit.
- Repeat the previous cycle twice to prove the base advances rather than only repairing one divergence.
- Pull with disjoint host/Hub paths merges cleanly; overlapping edits produce a normal conflict and `--continue` preserves both resolved content and Hub ancestry.
- An already-folded Hub tip is recognized without duplicating content.
- A content-only fold creates a bridge rather than silently recording a false base.

### Safety and recovery

- No code path invokes a forced update or emits force advice.
- Projected root tree always equals the selected host prefix and never contains an application-only sentinel file.
- A remote move between fetch and atomic push changes neither main nor ledger.
- A simulated crash after remote success but before local cache write recovers entirely from the ledger.
- Deleted `.agentsfs/hub.json` plus explicit target recovers the right base from the ledger.
- Deleted metadata with no explicit target fails loudly and never contacts a basename-derived repository.
- A host with two embedded instances cannot cross-link targets, tracking refs, projection ids, or ledgers.
- Rewritten host history and rewritten Hub history are distinguished from an ordinary remote-ahead state.

### Compatibility

- All existing standalone push, pull/clone, browser edit, board writeback, save API, hosted agent, MCP, and gardening tests remain behaviorally unchanged.
- Dirty application files outside the embedded prefix do not block push and are never published.
- Dirty or conflicted instance files are reported accurately and cannot be mistaken for synchronized content.
- Status and JSON report the exact base, remote-ahead state, recovery availability, and next command without generating projection commits on read-only status.

### Live fixtures

- Pinned markdownto fixture: host merge `eee42a2`, host follow-up `28f480a`, Hub tip `e2fa739`; pull recognizes the fold and push fast-forwards Hub.
- Evolved markdownto fixture: latest Hub gardening after `e2fa739` merges from the old verified base, exposes the real backlog conflict, and converges after resolution without losing either side.

## Contract decision

This likely changes the AgentsFS contract, not only command documentation. Rule 12 currently says to pull before a remote-backed work unit and names `afs hub push` for Hub publication, but it does not define the embedded two-way pull command or the obligation to use it before writing. Once the protocol exists, propose a contract version bump that states:

- standalone Hub instances use ordinary Git/Hub pull semantics;
- embedded Hub projections use `afs hub pull` before work and `afs hub push` after the commit; and
- agents never substitute raw `git pull`/`git push hub HEAD` for the embedded translation.

Do not modify stock or live `AGENTS.md` until that wording is reviewed as a contract decision.

## Log

- 2026-08-09/10 — auto-gardening exposed the missing reverse path; markdownto reproduction proved plain push remains blocked after a manual fold, and identified target-guessing/hint bugs.
- 2026-08-10 — design review clarified the architectural constraint: an embedded Hub repository is a translated projection, not literally a remote of the enclosing host repository. Verified `git subtree split --onto=<hub-base>` preserves both the Hub ancestor and the exact host-prefix tree; chose projection-base tracking plus first-class pull over per-push rejoin merges.
