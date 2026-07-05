package hub

import (
	"os/exec"
	"testing"
)

func TestValidSlug(t *testing.T) {
	good := []string{"brain", "my-notes", "a", "stock-research-2026", "x1", "a-b-c"}
	bad := []string{"", "-x", "x-", "My-Notes", "a_b", "a..b", "a/b", "a b", "café", "----", "a--b"}
	for _, s := range good {
		if !validSlug(s) {
			t.Errorf("expected valid slug: %q", s)
		}
	}
	for _, s := range bad {
		if validSlug(s) {
			t.Errorf("expected invalid slug: %q", s)
		}
	}
}

func TestVisibilityAndSlug(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	store, err := NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRepo("alice", "brain"); err != nil {
		t.Fatal(err)
	}
	s := &Server{Storage: store}

	if s.isPublic("alice", "brain") {
		t.Fatal("a new repo must default to private")
	}
	if err := s.setVisibility("alice", "brain", visPublic); err != nil {
		t.Fatal(err)
	}
	if !s.isPublic("alice", "brain") {
		t.Fatal("repo should be public after setVisibility")
	}

	// Display name round-trips; defaults to the slug.
	if s.displayName("alice", "brain") != "brain" {
		t.Fatal("default display name should be the slug")
	}
	s.setDisplayName("alice", "brain", "My Brain")
	if s.displayName("alice", "brain") != "My Brain" {
		t.Fatal("display name did not round-trip")
	}

	// Rename carries the settings (they live in the bare repo's git config).
	if err := store.RenameRepo("alice", "brain", "mind"); err != nil {
		t.Fatal(err)
	}
	if store.Exists("alice", "brain") {
		t.Fatal("old slug should be gone after rename")
	}
	if !s.isPublic("alice", "mind") || s.displayName("alice", "mind") != "My Brain" {
		t.Fatal("visibility/display name should survive a rename")
	}

	// Duplicate slug is rejected.
	if err := store.EnsureRepo("alice", "other"); err != nil {
		t.Fatal(err)
	}
	if err := store.RenameRepo("alice", "mind", "other"); err == nil {
		t.Fatal("expected a duplicate-slug error")
	}
}
