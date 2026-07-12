package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestDashboardJSONIncludesSharedRepositories(t *testing.T) {
	ts, srv, accounts := newDeleteTestServer(t)
	if _, err := accounts.CreateUser("alice", "alice@example.com", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.CreateUser("bob", "bob@example.com", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Storage.EnsureRepo("alice", "shared-notes"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Storage.EnsureRepo("bob", "own-notes"); err != nil {
		t.Fatal(err)
	}
	if err := accounts.AddCollaborator("alice", "shared-notes", "bob", "write"); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/bob?format=json", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(sessionCookieFor(srv, "bob"))
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("dashboard JSON status = %d: %s", res.StatusCode, body)
	}

	var listing struct {
		User  string `json:"user"`
		Repos []struct {
			Owner  string `json:"owner"`
			Name   string `json:"name"`
			Role   string `json:"role"`
			Shared bool   `json:"shared"`
			URL    string `json:"url"`
		} `json:"repos"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		t.Fatal(err)
	}
	if listing.User != "bob" {
		t.Fatalf("listing user = %q, want bob", listing.User)
	}
	var own, shared bool
	for _, repo := range listing.Repos {
		switch {
		case repo.Owner == "bob" && repo.Name == "own-notes":
			own = true
		case repo.Owner == "alice" && repo.Name == "shared-notes":
			if !repo.Shared || repo.Role != "write" || repo.URL != ts.URL+"/alice/shared-notes" {
				t.Fatalf("shared repo metadata = %+v", repo)
			}
			shared = true
		}
	}
	if !own || !shared {
		t.Fatalf("listing missing own/shared repos: %+v", listing.Repos)
	}
}
