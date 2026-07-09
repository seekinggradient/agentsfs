package hub

import (
	"path/filepath"
	"testing"
)

func TestCollaborators(t *testing.T) {
	a, err := OpenAccounts(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	a.CreateUser("alice", "", "pw12345678")
	a.CreateUser("bob", "", "pw12345678")

	// No grant yet.
	if a.CollaboratorRole("alice", "kauai", "bob") != "" {
		t.Fatal("bob should have no role yet")
	}

	// Can't add a non-existent user, yourself, or a bad role.
	if err := a.AddCollaborator("alice", "kauai", "nobody", "write"); err == nil {
		t.Fatal("adding a non-account should fail")
	}
	if err := a.AddCollaborator("alice", "kauai", "alice", "write"); err == nil {
		t.Fatal("adding the owner should fail")
	}
	if err := a.AddCollaborator("alice", "kauai", "bob", "admin"); err == nil {
		t.Fatal("bad role should fail")
	}

	// Grant write (case-insensitive username), then verify.
	if err := a.AddCollaborator("alice", "kauai", "BOB", "write"); err != nil {
		t.Fatal(err)
	}
	if got := a.CollaboratorRole("alice", "kauai", "bob"); got != "write" {
		t.Fatalf("role = %q, want write", got)
	}
	// Upsert changes the role, doesn't duplicate.
	if err := a.AddCollaborator("alice", "kauai", "bob", "read"); err != nil {
		t.Fatal(err)
	}
	if got := a.CollaboratorRole("alice", "kauai", "bob"); got != "read" {
		t.Fatalf("role after downgrade = %q, want read", got)
	}
	if list := a.ListCollaborators("alice", "kauai"); len(list) != 1 || list[0].Username != "bob" || list[0].Role != "read" {
		t.Fatalf("ListCollaborators = %+v", list)
	}

	// Reverse lookup: bob sees alice/kauai.
	shared := a.ReposSharedWith("bob")
	if len(shared) != 1 || shared[0].Owner != "alice" || shared[0].Repo != "kauai" || shared[0].Role != "read" {
		t.Fatalf("ReposSharedWith(bob) = %+v", shared)
	}
	if len(a.ReposSharedWith("alice")) != 0 {
		t.Fatal("alice has nothing shared with her")
	}

	// Isolation: a grant on one repo is not another repo.
	if a.CollaboratorRole("alice", "other-repo", "bob") != "" {
		t.Fatal("grant must be per-repo")
	}

	// Revoke.
	if err := a.RemoveCollaborator("alice", "kauai", "bob"); err != nil {
		t.Fatal(err)
	}
	if a.CollaboratorRole("alice", "kauai", "bob") != "" || len(a.ReposSharedWith("bob")) != 0 {
		t.Fatal("revoke should remove all access")
	}

	// Nil store is safe.
	var nilStore *AccountStore
	if nilStore.CollaboratorRole("a", "b", "c") != "" || nilStore.ReposSharedWith("x") != nil {
		t.Fatal("nil store must be safe")
	}
}

// TestCollaboratorRepoCaseInsensitive guards the revoke-misses-stale-access bug:
// a grant added via a mixed-case repo segment must be found + revoked via any
// other casing (repo names are canonicalized in the store).
func TestCollaboratorRepoCaseInsensitive(t *testing.T) {
	a, err := OpenAccounts(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	a.CreateUser("alice", "", "pw12345678")
	a.CreateUser("bob", "", "pw12345678")

	if err := a.AddCollaborator("alice", "Kauai", "bob", "write"); err != nil {
		t.Fatal(err)
	}
	// Looked up under a different casing → must still match.
	if a.CollaboratorRole("alice", "kauai", "bob") != "write" {
		t.Fatal("grant added as Kauai must be found as kauai")
	}
	// Revoke via yet another casing → must actually remove it (the security bug).
	if err := a.RemoveCollaborator("alice", "KAUAI", "bob"); err != nil {
		t.Fatal(err)
	}
	if a.CollaboratorRole("alice", "Kauai", "bob") != "" {
		t.Fatal("revoke via different case must remove access — stale grant would be a leak")
	}
	if len(a.ReposSharedWith("bob")) != 0 {
		t.Fatal("no stale share should remain")
	}
}
