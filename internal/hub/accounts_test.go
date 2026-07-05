package hub

import (
	"path/filepath"
	"testing"
)

func TestAccounts(t *testing.T) {
	dir := t.TempDir()
	a, err := OpenAccounts(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}

	// Create + duplicate.
	u, err := a.CreateUser("alice", "a@x.io", "s3cretpw")
	if err != nil || !u.HasPassword {
		t.Fatalf("create alice: %v", err)
	}
	if _, err := a.CreateUser("alice", "", "x"); err != ErrUserExists {
		t.Fatalf("expected ErrUserExists, got %v", err)
	}
	if _, err := a.CreateUser("Bad Name", "", "pw"); err == nil {
		t.Fatal("invalid username should be rejected")
	}

	// Password verification.
	if _, err := a.VerifyPassword("alice", "s3cretpw"); err != nil {
		t.Fatal("correct password should verify")
	}
	if _, err := a.VerifyPassword("alice", "wrong"); err == nil {
		t.Fatal("wrong password should fail")
	}

	// Bootstrap account (no password) can't log in until a password is set.
	if _, err := a.CreateUser("bob", "", ""); err != nil {
		t.Fatal(err)
	}
	if a.HasPassword("bob") {
		t.Fatal("bob should have no password")
	}
	if _, err := a.VerifyPassword("bob", "anything"); err == nil {
		t.Fatal("a passwordless account must not verify any password")
	}
	if err := a.SetPassword("bob", "bobpassword"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.VerifyPassword("bob", "bobpassword"); err != nil {
		t.Fatal("should verify after SetPassword")
	}

	// PATs.
	tok, err := a.CreatePAT("alice", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := a.UserForToken(tok); !ok || got != "alice" {
		t.Fatalf("PAT should map to alice, got %q ok=%v", got, ok)
	}
	if _, ok := a.UserForToken("afs_bogus"); ok {
		t.Fatal("a bogus token must not resolve")
	}
	pats, _ := a.ListPATs("alice")
	if len(pats) != 1 {
		t.Fatalf("expected 1 PAT, got %d", len(pats))
	}
	a.RevokePAT("alice", pats[0].ID)
	if _, ok := a.UserForToken(tok); ok {
		t.Fatal("a revoked token must not resolve")
	}

	// Session secret persists across reopen.
	a2, err := OpenAccounts(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	if string(a.SessionSecret()) != string(a2.SessionSecret()) {
		t.Fatal("session secret must persist across opens")
	}
}
