package hub

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// The store mints once, then reuses the persisted token — the load-bearing
// property: the Eve app must receive the SAME PAT on every request so durable
// (hours-later) tool calls keep working, and a restart must not orphan tokens.
func TestAgentPATStoreMintsOnceAndReusesAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".agent-pats.json")
	s := NewAgentPATStore(path)

	var mints int32
	mint := func() (string, error) {
		n := atomic.AddInt32(&mints, 1)
		return fmt.Sprintf("afs_tok-%d", n), nil
	}

	tok1, err := s.GetOrMint("alice", mint)
	if err != nil || tok1 != "afs_tok-1" {
		t.Fatalf("first GetOrMint = %q, %v", tok1, err)
	}
	tok2, err := s.GetOrMint("alice", mint)
	if err != nil || tok2 != tok1 {
		t.Fatalf("second GetOrMint = %q (want reuse of %q), %v", tok2, tok1, err)
	}
	if got := atomic.LoadInt32(&mints); got != 1 {
		t.Fatalf("minted %d times, want exactly 1", got)
	}

	// A fresh store over the same file (process restart) reuses the same token.
	s2 := NewAgentPATStore(path)
	tok3, err := s2.GetOrMint("alice", mint)
	if err != nil || tok3 != tok1 {
		t.Fatalf("post-restart GetOrMint = %q (want %q), %v", tok3, tok1, err)
	}
	if got := atomic.LoadInt32(&mints); got != 1 {
		t.Fatalf("restart re-minted (%d total), want still 1", got)
	}

	// Distinct users get distinct tokens.
	tokBob, err := s.GetOrMint("bob", mint)
	if err != nil || tokBob == tok1 {
		t.Fatalf("bob token = %q (want != alice's %q), %v", tokBob, tok1, err)
	}
}

// The backing file is 0600 so the plaintext tokens are never group/world
// readable — the whole reason a plaintext store is acceptable.
func TestAgentPATStoreFileIs0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".agent-pats.json")
	s := NewAgentPATStore(path)
	if _, err := s.GetOrMint("alice", func() (string, error) { return "afs_x", nil }); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("pat store perms = %o, want 600", perm)
	}
}

// Rotation: deleting the entry makes the next request mint fresh (the operator
// also revokes the old PAT in the account). This must work without a restart —
// the file is re-read on every GetOrMint.
func TestAgentPATStoreDeleteThenReMint(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".agent-pats.json")
	s := NewAgentPATStore(path)

	var mints int32
	mint := func() (string, error) {
		return fmt.Sprintf("afs_tok-%d", atomic.AddInt32(&mints, 1)), nil
	}
	tok1, _ := s.GetOrMint("alice", mint)
	if err := s.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("alice"); ok {
		t.Fatal("entry survived Delete")
	}
	tok2, _ := s.GetOrMint("alice", mint)
	if tok2 == tok1 {
		t.Fatalf("re-mint returned the old token %q, want a fresh one", tok1)
	}
	if got := atomic.LoadInt32(&mints); got != 2 {
		t.Fatalf("minted %d times, want 2 (initial + post-rotation)", got)
	}
}

// A nil store and an empty user are both no-ops (injection is simply skipped),
// so a misconfigured or test deployment never panics.
func TestAgentPATStoreNilAndEmptyUser(t *testing.T) {
	var s *AgentPATStore // nil
	if tok, err := s.GetOrMint("alice", func() (string, error) { return "x", nil }); tok != "" || err != nil {
		t.Fatalf("nil store GetOrMint = %q, %v; want \"\", nil", tok, err)
	}
	s2 := NewAgentPATStore(filepath.Join(t.TempDir(), "p.json"))
	if tok, err := s2.GetOrMint("", func() (string, error) { return "x", nil }); tok != "" || err != nil {
		t.Fatalf("empty-user GetOrMint = %q, %v; want \"\", nil", tok, err)
	}
}

// Concurrent first requests for the same user must mint exactly one token (the
// mutex serializes the read-modify-write), never two.
func TestAgentPATStoreConcurrentMintOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".agent-pats.json")
	s := NewAgentPATStore(path)
	var mints int32
	mint := func() (string, error) {
		return fmt.Sprintf("afs_tok-%d", atomic.AddInt32(&mints, 1)), nil
	}
	var wg sync.WaitGroup
	toks := make([]string, 20)
	for i := range toks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			toks[i], _ = s.GetOrMint("alice", mint)
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&mints); got != 1 {
		t.Fatalf("concurrent GetOrMint minted %d tokens, want 1", got)
	}
	for i, tk := range toks {
		if tk != toks[0] {
			t.Fatalf("goroutine %d got %q, want the single minted %q", i, tk, toks[0])
		}
	}
}
