---
description: RFC and implementation record for safe two-way synchronization between an embedded AgentsFS directory in a host repository and its standalone Hub projection.
---

# Embedded Hub projection two-way sync

## Status and outcome

Protocol v2 is implemented on the release branch. It makes an embedded AgentsFS projection a genuine two-way synchronization surface without uploading the enclosing application, rewriting Hub history, or changing standalone repository behavior.

The key distinction is now explicit:

- A **standalone instance** and its Hub repository have the same paths and commit graph. The Hub is an ordinary Git remote.
- An **embedded instance** is stored below a host prefix such as `agentsfs/`, while its Hub repository stores that directory at repository root. It is a translated projection, not literally a remote of the enclosing repository.

The durable protocol has two related invariants:

1. After every successful embedded push, the Hub ledger names a host commit `H` and Hub snapshot commit `P` such that `tree(H:<prefix>) == tree(P)`.
2. Before an embedded push may append, AFS can prove that current Hub tip `R` has already been integrated into the host—through local schema-2 state, a recoverable fold trailer, or actual ancestry. The new snapshot's sole parent is exactly `R`.

This preserves all three product properties that make translation necessary:

1. the knowledge base shares history with its host application;
2. Hub never receives files outside the selected instance; and
3. Hub may create commits through gardening, agents, browser editing, boards, or application writeback.

## The verified bug

The original embedded publisher ran:

```text
git subtree split --prefix=<prefix> HEAD
git push <result>:refs/heads/main
```

That works while Hub is a one-way mirror. Once Hub writes `G1`, the next independently projected host commit `P1` is a sibling:

```text
last projection: P0

Hub:              P0 -- G1
plain host split: P0 -- P1
```

Refusing `P1` over `G1` is correct. Force-pushing would destroy Hub work.

The old recovery hint was not correct:

- plain `git subtree pull` can reject the projection as unrelated history;
- `-Xsubtree=<prefix> --allow-unrelated-histories` can fold content, but a later plain split still lacks a reliable root↔prefix correspondence;
- the hint built its URL from the instance folder basename, so `markdownto/agentsfs` incorrectly pointed at `seekinggradient/agentsfs`;
- when `.agentsfs/hub.json` was missing, embedded push could make the same unsafe basename/remote guess.

The auto-gardener exposed the bug because it was the first scheduled writer on repositories previously treated as one-way projections. Browser editing, MCP/Eve writes, board writeback, and the save API would have exposed the same missing reverse path.

## Design evolution

### Rejected: permanent subtree rejoin

`git subtree split --rejoin` records correspondence with stock Git trailers, but adds a bookkeeping merge to the host after every publication. It also requires a cleaner enclosing worktree and makes a one-way backup mutate application history. Real Hub work deserves a host fold commit; every push does not.

### Rejected after implementation test: `subtree split --onto` as the v2 projector

The first RFC selected `git subtree split --onto=<hub-tip>`. It worked against the pinned markdownto manual-fold fixture and for ordinary host-only commits.

The end-to-end test then found a stronger counterexample: after a genuine three-way folded pull, `subtree split --onto` could produce a commit that kept the host half but silently omitted the Hub half. Depending on subtree trailers and split-cache interpretation, it could also emit a result that did not descend from the requested `--onto` commit.

The tree/ancestry verifier caught both outcomes before any push. That result removes Git-subtree cache behavior from the v2 correctness boundary.

### Chosen: exact projection snapshot with an explicit Hub parent

For a non-empty v2 target, push uses Git plumbing to create one commit:

```text
tree(new snapshot) = tree(HEAD:<instance-prefix>)
parent(new snapshot) = fetched Hub main
```

If the source tree already equals Hub main's tree, the existing Hub commit is reused and no main commit is created.

This trades per-host-commit projection history for a much smaller, provable protocol:

- host history remains complete in the host repository;
- every Hub-originated commit remains in Hub ancestry;
- each publication is one labeled snapshot connected to the exact remote tip;
- deletes, renames, and binary changes need no special translation logic;
- application files cannot enter the selected tree;
- the full tree and parent are verified before upload.

An initial push to an empty target may still use `git subtree split` to preserve useful seed history. Every later v2 push uses the exact snapshot-parent rule.

## Safety invariants

The implementation mechanically enforces:

1. **No force updates.** Neither main nor the ledger is force-pushed, and no diagnostic recommends force.
2. **Exact target identity.** Embedded commands require valid instance-local metadata or an explicit `owner/repo`. Folder basename and repository-wide remotes are never authoritative.
3. **Application isolation.** The published root tree must equal `HEAD:<prefix>`; a host-root commit is never selected for publication.
4. **Remote preservation.** The fetched Hub tip is the new snapshot's sole parent. If that tip is not proven integrated, push stops and says to run `afs hub pull`.
5. **Atomic publication.** Hub main and `refs/agentsfs/projection` advance together with `git push --atomic`, or neither advances.
6. **Truthful conflicts.** Overlapping edits become normal unmerged index entries under the host prefix. Metadata advances only after a resolved folded commit.
7. **Recoverable machine state.** Deleting `.agentsfs/hub.json` loses a cache, not the last successful push correspondence. Recovery still requires an explicit target.
8. **Standalone compatibility.** Standalone clone/pull/push and all standalone Hub writer surfaces retain ordinary Git behavior.
9. **Credential containment.** Remote URLs stored in instance state and the ledger are credential-free; tokens remain in the user's config/credential helper.
10. **Race safety.** A remote movement after fetch makes the ordinary atomic push fail non-fast-forward; local publication state is not advanced.

## State model

### Local schema 2

Embedded `.agentsfs/hub.json` contains credential-free, rebuildable state:

```json
{
  "schema_version": 2,
  "mode": "embedded-projection",
  "sync_version": 2,
  "ledger_ref": "refs/agentsfs/projection",
  "remote_url": "https://hub.agentsfs.ai/owner/repo.git",
  "repository": "owner/repo",
  "publish_branch": "main",
  "integrated_hub_commit": "<tip folded into this host>",
  "last_push": {
    "source_repo_head": "<host commit>",
    "projected_commit": "<Hub snapshot>",
    "verified_remote_commit": "<same verified Hub snapshot>"
  }
}
```

`last_push` is an exact same-tree correspondence. `integrated_hub_commit` may instead name the Hub base of a later folded host commit whose final tree also contains concurrent host work.

### Recoverable Hub ledger

The fixed ref is:

```text
refs/agentsfs/projection
```

Each ledger commit contains `projection.json` and points to the prior ledger commit. Its record includes:

- protocol schema and embedded-projection mode;
- exact `owner/repo`;
- source host commit id;
- resulting Hub commit id; and
- projected tree id.

The host commit id is provenance data; the Hub object database does not need the enclosing application commit or tree. The reader verifies that the named Hub commit exists and has the recorded tree.

A fixed per-repository ref is sufficient because one Hub repository represents one knowledge base. Multiple embedded instances in one host use separate Hub repositories and separate local tracking refs.

### Host fold trailers

A successful pull creates one ordinary host commit with:

```text
agentsfs-hub-base: <previous Hub base>
agentsfs-hub-tip: <fetched Hub main>
agentsfs-hub-repo: owner/repo
```

These trailers let a fresh host clone recover an integrated tip even before the next push updates the Hub ledger. A trailer is accepted only for the exact repository, and the named commit must exist after fetch.

## Exact embedded pull

`afs hub pull [owner/repo] --instance PATH` is distinct from clone pull. From inside a linked embedded instance, both the target and `--instance` may be omitted.

### 1. Preflight

- Resolve the instance, enclosing worktree, and exact prefix.
- Require a valid instance-local target or explicit `owner/repo`.
- Reject any dirty host worktree. This conservative v2 rule guarantees abort can restore exactly; a later version may safely relax it outside the prefix.
- Reject an unrelated Git merge or pending projection pull.
- Fetch Hub main and, when present, the ledger into per-instance namespaced refs.

### 2. Recover the base

Base candidates, in order, are:

1. a Hub commit already in host ancestry (legacy manual fold);
2. schema-2 `integrated_hub_commit`;
3. the newest exact-repository `agentsfs-hub-tip` trailer;
4. the last verified push from a valid local schema-1/schema-2 record or fetched ledger.

The selected base must exist and be an ancestor of fetched Hub main. A rewritten Hub history stops separately from ordinary remote-ahead work.

If no base exists, `--adopt` is accepted only when the committed host prefix tree exactly equals Hub main. Names, approximate content, or a repository-wide remote are never sufficient proof.

### 3. Upgrade a legacy identity

When schema-1 local metadata exists but the Hub has no ledger, pull first creates a root ledger record for the verified old push and publishes only that new ref. Main does not move. This marks the repository protocol-v2 before Hub writers are re-enabled.

### 4. Three-way mapped fold

Let:

- `B` be the base Hub tree;
- `O` be current host `HEAD`; and
- `R` be fetched Hub main.

AFS builds two temporary full-host trees using a throwaway index:

- base-full = current host outside the prefix + `B` under the prefix;
- theirs-full = current host outside the prefix + `R` under the prefix.

It then asks Git's three-way index machinery to merge:

```text
base-full, HEAD tree, theirs-full
```

Only the prefix differs between those mapped trees, so Hub root paths cannot leak into the application root. Git's standard per-file merge driver handles disjoint text edits; overlapping edits remain staged conflict entries with familiar markers.

### 5. Fold, continue, or abort

A clean result is committed once in host history with the stable trailers above. The Hub commit is intentionally not a merge parent: end-to-end testing proved subtree's later split interpretation could discard the second-parent content. The explicit trailer is the durable correspondence.

On conflict, AFS leaves the index/worktree intact and writes a continuation record at the host Git directory's `AFS_HUB_PULL`. The record pins instance, target, pre-pull host head, base, fetched tip, and pending metadata.

- `afs hub pull --continue` requires no unresolved paths, creates the folded commit, verifies its exact-repository tip trailer, and then advances local metadata.
- `afs hub pull --abort` uses the pinned pre-pull head with `git reset --merge`, removes the continuation record, and leaves publication state unchanged.

## Exact embedded push

### 1. Resolve and fetch

- Resolve the instance and exact target.
- Fetch Hub main and ledger.
- Validate a fetched ledger's repository identity, Hub commit, and tree.
- Prefer ledger correspondence over stale machine-local state.

### 2. Prove current Hub main is integrated

Push accepts current Hub tip only if at least one proof holds:

- it is already a host ancestor (legacy fold);
- schema-2 local state names it as integrated; or
- the newest exact-repository fold trailer names it.

Otherwise push refuses before constructing or uploading anything and points to `afs hub pull`.

### 3. Construct and verify the snapshot

For an empty Hub target, seed a prefix-only history. For a non-empty target:

- read `HEAD:<prefix>`;
- if its tree equals Hub main, reuse Hub main;
- otherwise create one commit with that tree and Hub main as sole parent;
- verify exact tree equality; and
- verify Hub main ancestry.

The snapshot message records the source host commit and prefix for auditability.

### 4. Publish atomically

- Create the next ledger commit with the exact host/snapshot/tree correspondence.
- Push snapshot→`refs/heads/main` and ledger→`refs/agentsfs/projection` with `--atomic`.
- Re-read both remote refs and require exact ids.
- Only then update namespaced local refs and schema-2 machine state.

A crash after remote success but before local state is recovered from the ledger. A concurrent Hub writer causes ordinary non-fast-forward rejection of the atomic push.

## Hub writer eligibility

Every commit that originates on the Hub consults one central policy:

- auto-gardener, including manual “run now”;
- browser editor;
- hosted agent API and remote MCP writes;
- Markdown To board writeback;
- save API;
- contract/maintenance writes that route through the same commit core.

Repository modes are:

- `standalone`;
- `embedded-projection-v1`; and
- `embedded-projection` (protocol v2).

A known v1 projection is read-only on Hub surfaces with an explanatory conflict. Git smart-HTTP remains available so an updated client can publish the create-only protocol marker and later atomic projection updates. The server recognizes a successful v2 ledger ref after receive-pack and records the upgraded mode.

Hub-created repositories are marked standalone at creation. Unmarked legacy repositories default standalone for backward compatibility, so rollout must explicitly classify every known old projection before enabling the worker.

## Strict target semantics

For embedded instances:

- missing/corrupt `.agentsfs/hub.json` plus no explicit target is a loud error;
- the enclosing repository's `hub` remote is only a compatibility convenience, never identity;
- the instance folder basename is never converted to a Hub slug;
- explicit targets accept `slug` in the signed-in namespace or `owner/slug`;
- metadata from another target is discarded as a base candidate;
- ledger repository mismatch is a hard cross-link error.

Standalone instances retain their existing folder/remote conveniences.

## Migration cases

### Hub not ahead

Use valid schema-1 `last_push` as the base. The first v2 push atomically creates the ledger; if content is unchanged, main need not move.

### Hub ahead, old local base available

Pull publishes the create-only legacy ledger marker, performs the mapped three-way fold from the old base, then the next push appends one exact snapshot to the fetched Hub tip.

### Hub already folded with ancestry

If current Hub tip is already a host ancestor—as in the pinned markdownto `eee42a2` fold of Hub `e2fa739`—pull reports already integrated and records schema-2 state. The next snapshot push fast-forwards Hub.

### Content-only equality

With an explicit `--adopt`, exact equality between current prefix tree and Hub main is sufficient to create a folded correspondence. Approximate equality or name similarity is not.

### Genuine conflict

The mapped base gives Git the real old projection content, so disjoint changes merge and overlapping edits conflict only at the affected prefixed paths. Resolve/stage/continue, then push.

### Rewritten history

If the recorded base is not an ancestor of fetched Hub main, stop. The tool never treats a rewrite as ordinary remote-ahead work and never repairs it with force.

## Test coverage

The implementation suite proves:

- an initial embedded push contains no application sentinel;
- every later snapshot descends from current Hub main and has the exact prefix tree;
- Hub write → disjoint host write → pull → push preserves both contents and connected Hub ancestry;
- a schema-v1 pull publishes the recoverable protocol marker;
- overlapping edits leave a resumable normal conflict;
- abort restores exact host head/content and a clean worktree;
- continue creates a recoverable folded commit;
- a legacy manual fold is recognized without duplicate content;
- deleting local metadata plus an explicit target recovers from the ledger;
- missing metadata with no target fails loudly;
- two embedded instances cannot reuse a repository-wide remote;
- the ledger and main update atomically;
- known v1 repositories are omitted from automatic/manual gardening;
- browser/API/MCP/save/board write paths are centrally rejected for v1;
- standalone flows retain their existing tests.

The live rollout adds a production acceptance check for `seekinggradient/markdownto`: fetch current tips, fold any post-`e2fa739` gardener work in a clean migration checkout, resolve the known backlog conflict without losing either side, push the host branch normally, then run v2 projection push and prove Hub main is a descendant of every retained gardener commit.

## Contract 0.12.0 decision

This feature changes the synchronization contract, so it is not merely CLI documentation.

Rule 12 now states:

- standalone Hub instances use ordinary Git pull semantics;
- linked embedded projections use `afs hub pull` before work and `afs hub push` after the commit; and
- raw `git pull`/`git push hub HEAD` are not substitutes for prefix translation.

The same contract release adopts the already-aligned `backlog-workspace@0.1` envelope as a SHOULD, establishes task ids as final tokens, and requires path-qualified external spine references.

## Rollout sequence

1. Merge and release the protocol-v2 CLI and contract.
2. Deploy the Hub writer gate and v2 ref recognition.
3. Before the gardener runs, explicitly mark every known hosted legacy projection `embedded-projection-v1`.
4. Inventory local `.agentsfs/hub.json` files and exact hosted repository identities.
5. For each embedded projection, use a clean host worktree, pull, resolve, test, push the host branch normally, then projection-push.
6. Verify Hub main ancestry, ledger identity/tree, host prefix equality, and clean local status.
7. Leave genuinely ambiguous or dirty checkouts unchanged and record the exact blocker rather than guessing.

## Log

- 2026-08-09/10 — Auto-gardening exposed the missing reverse path. The markdownto reproduction proved that a content fold alone could not satisfy the old publisher; target-guessing and false-hint bugs were recorded.
- 2026-08-10 — Initial design selected base tracking, a recoverable Git ledger, projection-aware pull, and Hub writer gating instead of permanent subtree rejoin merges.
- 2026-08-10 — End-to-end implementation disproved `subtree split --onto` as a general post-fold projector: verified outputs could omit Hub content or fail requested ancestry. Replaced it with one exact tree snapshot commit parented directly to fetched Hub main.
- 2026-08-10 — Integration tests passed clean disjoint merge, real conflict/continue/abort, schema-v1 marker migration, deleted-machine-state recovery, two-instance isolation, and the pinned already-folded legacy shape.
