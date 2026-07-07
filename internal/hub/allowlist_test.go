package hub

import (
	"path/filepath"
	"testing"
)

func TestAllowlistAndWaitlist(t *testing.T) {
	a, err := OpenAccounts(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}

	// Empty allowlist → not active (signup open).
	if a.AllowlistActive() {
		t.Fatal("empty allowlist should be inactive")
	}

	// Add (case/space-insensitive) + idempotent.
	if err := a.AllowEmail("  SeekingGradient@Gmail.com "); err != nil {
		t.Fatal(err)
	}
	a.AllowEmail("seekinggradient@gmail.com") // dup, normalized → no second row
	if !a.AllowlistActive() {
		t.Fatal("allowlist should be active after adding")
	}
	if !a.IsAllowed("SEEKINGGRADIENT@gmail.com") {
		t.Fatal("match should be case-insensitive")
	}
	if a.IsAllowed("stranger@example.com") {
		t.Fatal("non-listed email should not be allowed")
	}
	if got := a.ListAllowlist(); len(got) != 1 || got[0] != "seekinggradient@gmail.com" {
		t.Fatalf("allowlist = %v, want one normalized entry", got)
	}

	// Waitlist a stranger (idempotent, keeps first ts, updates username).
	if err := a.AddToWaitlist("Stranger@Example.com", "stranger"); err != nil {
		t.Fatal(err)
	}
	a.AddToWaitlist("stranger@example.com", "stranger2")
	if !a.OnWaitlist("STRANGER@example.com") {
		t.Fatal("stranger should be on the waitlist")
	}
	wl := a.ListWaitlist()
	if len(wl) != 1 || wl[0].Email != "stranger@example.com" || wl[0].Username != "stranger2" {
		t.Fatalf("waitlist = %+v, want one entry with updated username", wl)
	}

	// Admit: allow + remove from waitlist.
	a.AllowEmail("stranger@example.com")
	a.RemoveFromWaitlist("stranger@example.com")
	if a.OnWaitlist("stranger@example.com") {
		t.Fatal("admitted email should leave the waitlist")
	}
	if !a.IsAllowed("stranger@example.com") {
		t.Fatal("admitted email should be allowed")
	}

	// Revoke.
	a.RemoveAllowed("stranger@example.com")
	if a.IsAllowed("stranger@example.com") {
		t.Fatal("revoked email should not be allowed")
	}

	// Blanks are rejected, not stored.
	if err := a.AllowEmail("   "); err == nil {
		t.Fatal("blank email should error")
	}
	if err := a.AddToWaitlist("", "x"); err == nil {
		t.Fatal("blank waitlist email should error")
	}
}

func TestLooksLikeEmail(t *testing.T) {
	ok := []string{"a@b.co", "seekinggradient@gmail.com", "x.y+z@sub.domain.io"}
	bad := []string{"", "nope", "@b.co", "a@b", "a@bc.", "a b@c.co", "a@ b.co"}
	for _, e := range ok {
		if !looksLikeEmail(e) {
			t.Errorf("looksLikeEmail(%q) = false, want true", e)
		}
	}
	for _, e := range bad {
		if looksLikeEmail(e) {
			t.Errorf("looksLikeEmail(%q) = true, want false", e)
		}
	}
}
