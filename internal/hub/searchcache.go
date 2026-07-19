package hub

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentsfs.ai/afs/internal/core"
)

// searchCache bridges the hub's bare repos (objects only, no working tree) to
// the core retrieval pipeline, which assumes a working tree plus a
// .agentsfs/index.db. For each repo it maintains a sparse, TEXT-ONLY checkout
// of HEAD next to the bare repo and runs the real core.Search / SearchContext
// against it — so the hub and a local `afs` checkout return identical results
// (retrieval-and-voice-plan.md R4, Phase B).
//
// It is a pure derived cache: no backups, delete-safe, regenerated on demand.
// Contents per materialized commit: only the *.md files the core indexes
// (chunkInstance / fingerprint filter on isMarkdown), a warm .agentsfs/ index,
// and a marker recording the commit OID. Media and other binaries are NEVER
// materialized — media reads keep coming straight from the bare repo's blobs.
//
// Layout & swap. Each materialized commit is an IMMUTABLE directory named by
// its OID:
//
//	<root>/.searchcache/<owner>/<repo>/<oid>/
//
// The OID is the version pointer, so there is no separate "current" symlink to
// update and — crucially — no directory is ever mutated or renamed-over while a
// reader is using it. A reader resolves HEAD once, computes the OID dir name,
// and runs the whole search against that fixed snapshot; a concurrent push that
// advances HEAD builds a sibling <newoid>/ dir and never disturbs the one in
// use. This is the "versioned dirs" option from the design, minus the symlink:
// it strictly dominates a single-dir temp+rename swap, which has an
// (unavoidable) window where the stable path is briefly absent or half-swapped
// under a mid-flight reader. Builds themselves are atomic (materialize+index in
// a sibling .build- temp dir, then a single rename into place), and stale OID
// dirs are grace-GC'd (see gc) long after any sub-second read could still hold
// one.
//
// The cache root is a dot-dir at the storage root, alongside .lfs/.trash/
// .threads. Usernames can't start with "." (nameRe), so it never collides with
// a user namespace and stays invisible to repo listing and clone.
type searchCache struct {
	root    string // <storage root>/.searchcache
	gitPath string

	mu    sync.Mutex
	locks map[string]*sync.Mutex // per-repo build serialization, keyed "owner/repo"
}

// cacheMarkerName is the OID marker written inside a built version dir's
// .agentsfs/. Its presence is what marks a directory as fully built; it lives
// in the machine-territory dot-dir the core walk skips, so it never affects
// indexing or the fingerprint.
const cacheMarkerName = "commit"

// cacheGracePeriod is how long a non-current version dir is kept before GC.
// Rebuilds and searches are both sub-second at KB scale, so a dir that has been
// non-current for minutes cannot have an active reader; the grace also spares a
// concurrent builder's in-progress .build- temp dir.
const cacheGracePeriod = 5 * time.Minute

func newSearchCache(root string) *searchCache {
	return &searchCache{root: root, gitPath: "git", locks: map[string]*sync.Mutex{}}
}

// ensure returns a directory holding a text-only checkout of the bare repo at
// head, with a warm core index, building it lazily when head has advanced past
// (or never had) a cached snapshot. The returned dir is immutable for this OID
// and safe to run core.Search / SearchContext against without further locking.
func (c *searchCache) ensure(owner, repo, bareDir, head string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("search cache not configured")
	}
	if head == "" {
		return "", fmt.Errorf("empty repo")
	}
	dir := c.dirFor(owner, repo, head)
	if isBuiltCacheDir(dir) {
		return dir, nil
	}
	// Serialize builds for this repo so racing queries don't materialize the same
	// snapshot twice; unrelated repos build concurrently.
	lock := c.lockFor(owner + "/" + repo)
	lock.Lock()
	defer lock.Unlock()
	if isBuiltCacheDir(dir) { // another goroutine built it while we waited
		return dir, nil
	}
	if err := c.build(bareDir, head, dir); err != nil {
		return "", err
	}
	c.gc(owner, repo, head)
	return dir, nil
}

// refresh eagerly materializes the current HEAD snapshot. It is the post-push
// optimization: correctness never depends on it (ensure rebuilds lazily on any
// query that sees a newer HEAD), so it is best-effort and its error is ignored.
func (c *searchCache) refresh(owner, repo, bareDir string) {
	if c == nil {
		return
	}
	head := headOID(c.gitPath, bareDir, defaultRef)
	if head == "" {
		return
	}
	_, _ = c.ensure(owner, repo, bareDir, head)
}

// remove deletes a repo's entire cache subtree. Called on repo deletion; safe
// to call when no cache exists.
func (c *searchCache) remove(owner, repo string) {
	if c == nil {
		return
	}
	_ = os.RemoveAll(filepath.Join(c.root, owner, repo))
}

func (c *searchCache) dirFor(owner, repo, oid string) string {
	return filepath.Join(c.root, owner, repo, oid)
}

func (c *searchCache) lockFor(key string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locks == nil {
		c.locks = map[string]*sync.Mutex{}
	}
	l := c.locks[key]
	if l == nil {
		l = &sync.Mutex{}
		c.locks[key] = l
	}
	return l
}

// build materializes head into a sibling temp dir, warms the index, writes the
// OID marker, then atomically renames the temp dir into dest. A lost rename race
// (a concurrent builder placed dest first) is not an error — that dir is equally
// valid — so the temp build is simply discarded.
func (c *searchCache) build(bareDir, head, dest string) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".build-")
	if err != nil {
		return err
	}
	placed := false
	defer func() {
		if !placed {
			_ = os.RemoveAll(tmp)
		}
	}()

	if err := materializeText(c.gitPath, bareDir, head, tmp); err != nil {
		return err
	}
	// Warm the FTS index so the first query pays no build cost. ReindexFTS
	// creates .agentsfs/ and index.db from the materialized *.md files.
	if _, err := core.ReindexFTS(tmp); err != nil {
		return err
	}
	// Marker last: it records the OID and, together with the atomic rename below,
	// guarantees a half-built dir is never mistaken for a complete one.
	if err := os.WriteFile(filepath.Join(tmp, ".agentsfs", cacheMarkerName), []byte(head), 0o644); err != nil {
		return err
	}

	if err := os.Rename(tmp, dest); err != nil {
		if isBuiltCacheDir(dest) {
			return nil // a concurrent builder won the race; use their dir
		}
		return err
	}
	placed = true
	return nil
}

// gc prunes this repo's stale version dirs (and any orphaned .build- temp dirs),
// keeping the current head and anything younger than the grace period. Called
// after a successful build; best-effort.
func (c *searchCache) gc(owner, repo, keepOID string) {
	repoDir := filepath.Join(c.root, owner, repo)
	ents, err := os.ReadDir(repoDir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.Name() == keepOID {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < cacheGracePeriod {
			continue // spare a snapshot a sub-second reader might still hold
		}
		_ = os.RemoveAll(filepath.Join(repoDir, e.Name()))
	}
}

// isBuiltCacheDir reports whether dir is a fully materialized version dir.
func isBuiltCacheDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".agentsfs", cacheMarkerName))
	return err == nil
}

// materializeText writes every text-indexable file at head into dst, preserving
// the tree layout. Only *.md files are written — that is exactly what the core
// pipeline indexes (core.isMarkdown, enforced in chunkInstance and fingerprint)
// — so media and other binaries are never materialized.
func materializeText(gitPath, bareDir, head, dst string) error {
	out, err := exec.Command(gitPath, "-C", bareDir, "ls-tree", "-r", "-z", head).Output()
	if err != nil {
		return err
	}
	for _, rec := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rec == "" {
			continue
		}
		// Record: "<mode> <type> <oid>\t<path>".
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		meta := strings.Fields(rec[:tab])
		if len(meta) < 3 || meta[1] != "blob" {
			continue // trees are recursed by -r; submodule commits aren't files
		}
		rel := rec[tab+1:]
		if !isIndexableText(rel) {
			continue
		}
		clean, ok := safeCachePath(rel)
		if !ok {
			continue
		}
		content, ok := BlobContent(gitPath, bareDir, head, rel)
		if !ok {
			continue
		}
		target := filepath.Join(dst, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// isIndexableText mirrors core.isMarkdown: the core pipeline indexes only *.md
// files, so only they are worth materializing.
func isIndexableText(rel string) bool {
	return strings.EqualFold(filepath.Ext(rel), ".md")
}

// safeCachePath jails a git tree path before it is joined under the cache dir.
// Tree paths are already clean and relative, but a defensive check keeps a
// crafted path from escaping the materialization root.
func safeCachePath(rel string) (string, bool) {
	rel = strings.TrimSpace(rel)
	if rel == "" || strings.HasPrefix(rel, "/") || strings.ContainsRune(rel, 0) {
		return "", false
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}
