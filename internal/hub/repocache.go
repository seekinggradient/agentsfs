package hub

import (
	"encoding/json"
	"sync"
)

// repoView is everything the web pages derive from one commit of one repo:
// the sorted file list (tree + header metadata) and the wikilink graph with
// its JSON form. It is immutable once built — pages read it concurrently.
type repoView struct {
	OID       string // commit the view was built at; "" = empty repo
	Files     []RepoFile
	Graph     RepoGraph
	GraphJSON []byte
}

// viewCache holds the latest repoView per bare repo dir. A bare repo only
// changes when a ref moves (push or web edit), so a view stays valid until
// HEAD's commit id changes — which one cheap rev-parse per request detects.
// Without this, every page view re-read the whole repo from git.
type viewCache struct {
	mu      sync.Mutex
	entries map[string]*repoView
}

// viewCacheMax bounds resident views. Views are compact (paths, descriptions,
// graph — not note bodies), so this is belt-and-braces for a long-lived
// process on a small VM, not a tuning knob; eviction beyond it is arbitrary.
const viewCacheMax = 128

// drop evicts the cached view for a bare repo dir, e.g. after a rename or
// delete moves the dir off its old path — the entry keyed by the old path
// would otherwise sit there unreachable (never looked up again, but also
// never freed) for the life of the process. Tolerates a nil map.
func (c *viewCache) drop(bareDir string) {
	c.mu.Lock()
	delete(c.entries, bareDir)
	c.mu.Unlock()
}

// repoView returns the current view of user/repo, rebuilding it only when
// HEAD has moved since the cached one. The rebuild reads the repo in one pass
// (one ls-tree, one cat-file batch, one bounded log walk) and reuses the prior
// view so the history walk covers just the new commits.
func (s *Server) repoView(user, repo string) (*repoView, error) {
	bare := s.Storage.RepoDir(user, repo)
	oid := headOID("git", bare, defaultRef)

	s.views.mu.Lock()
	prev := s.views.entries[bare]
	s.views.mu.Unlock()
	if prev != nil && prev.OID == oid {
		return prev, nil
	}

	v, err := buildRepoView("git", bare, oid, user, repo, prev)
	if err != nil {
		return nil, err
	}

	s.views.mu.Lock()
	if s.views.entries == nil {
		s.views.entries = map[string]*repoView{}
	}
	if len(s.views.entries) >= viewCacheMax {
		for k := range s.views.entries {
			delete(s.views.entries, k)
			break
		}
	}
	s.views.entries[bare] = v
	s.views.mu.Unlock()
	return v, nil
}

// buildRepoView reads the repo once at oid and assembles the view: tree
// entries, markdown contents, last-commit times (incremental against prev),
// and the wikilink graph.
func buildRepoView(gitPath, bareDir, oid, user, repo string, prev *repoView) (*repoView, error) {
	v := &repoView{OID: oid}
	if oid == "" {
		return v, nil // no commits yet
	}
	entries, err := repoTreeEntries(gitPath, bareDir, oid)
	if err != nil {
		return nil, err
	}
	contents := markdownBlobContents(gitPath, bareDir, entries)
	v.Files = assembleFiles(gitPath, bareDir, oid, entries, contents, prev)
	v.Graph = buildRepoGraphFrom(v.Files, contents, user, repo)
	v.GraphJSON, _ = json.Marshal(v.Graph)
	return v, nil
}
