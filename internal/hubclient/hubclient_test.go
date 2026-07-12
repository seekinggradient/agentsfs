package hubclient

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestListPreservesSharedRepositoryMetadata(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bob" || r.URL.Query().Get("format") != "json" {
			t.Fatalf("listing request = %s, want /bob?format=json", r.URL.RequestURI())
		}
		user, token, ok := r.BasicAuth()
		if !ok || user != "bob" || token != "secret" {
			t.Fatalf("basic auth = %q/%q/%t, want bob/secret/true", user, token, ok)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"user":"bob","repos":[
{"owner":"bob","name":"own-notes","url":"https://hub/bob/own-notes"},
{"owner":"alice","name":"shared-notes","role":"write","shared":true,"url":"https://hub/alice/shared-notes"}
]}`)
	}))
	defer server.Close()
	if err := Save(Config{URL: server.URL, User: "bob", Token: "secret"}); err != nil {
		t.Fatal(err)
	}

	repos, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("List returned %d repos, want 2: %+v", len(repos), repos)
	}
	if repos[1].Owner != "alice" || repos[1].Name != "shared-notes" || !repos[1].Shared || repos[1].Role != "write" {
		t.Fatalf("shared repo = %+v", repos[1])
	}
}
