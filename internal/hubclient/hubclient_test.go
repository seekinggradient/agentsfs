package hubclient

import "testing"

func TestParseRef(t *testing.T) {
	cases := []struct {
		name, deflt         string
		wantOwner, wantSlug string
		wantErr             bool
	}{
		{"kauai-2026", "seekinggradient", "seekinggradient", "kauai-2026", false},
		{"someone/their-notes", "seekinggradient", "someone", "their-notes", false},
		{"My Trip Notes", "seekinggradient", "seekinggradient", "my-trip-notes", false},
		{"alice/My Notes", "seekinggradient", "alice", "my-notes", false},
		{"", "seekinggradient", "", "", true},
		{"alice/", "seekinggradient", "", "", true},
	}
	for _, c := range cases {
		owner, slug, err := ParseRef(c.name, c.deflt)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseRef(%q,%q) err=%v, wantErr=%v", c.name, c.deflt, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if owner != c.wantOwner || slug != c.wantSlug {
			t.Errorf("ParseRef(%q,%q) = %q/%q, want %q/%q", c.name, c.deflt, owner, slug, c.wantOwner, c.wantSlug)
		}
	}
}
