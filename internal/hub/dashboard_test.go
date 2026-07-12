package hub

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDashboardDisplayName(t *testing.T) {
	for _, tc := range []struct {
		name, configured, slug, want string
	}{
		{"humanizes an unset display name", "insurance-claim", "insurance-claim", "Insurance claim"},
		{"keeps a configured display name", "Claims workspace", "insurance-claim", "Claims workspace"},
		{"handles a one-word slug", "notes", "notes", "Notes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dashboardDisplayName(tc.configured, tc.slug); got != tc.want {
				t.Fatalf("dashboardDisplayName(%q, %q) = %q, want %q", tc.configured, tc.slug, got, tc.want)
			}
		})
	}
}

func TestDashboardTemplateUsesRepositoryLedger(t *testing.T) {
	data := dashboardData{
		baseData: baseData{User: "alice", Viewer: "alice", Dashboard: true},
		Repos: []repoCard{{
			Name:        "insurance-claim",
			DisplayName: "Insurance claim",
			Notes:       19,
			Age:         "1h ago",
			CloneCmd:    "git clone https://hub.example/alice/insurance-claim.git",
		}},
		Shared: []sharedCard{{
			Owner:       "bob",
			Name:        "field-notes",
			DisplayName: "Field notes",
			Notes:       7,
			Age:         "2d ago",
			Role:        "write",
			RoleLabel:   "Can edit",
			CloneCmd:    "git clone https://hub.example/bob/field-notes.git",
		}},
	}

	var buf bytes.Buffer
	if err := parsePages()["dashboard"].ExecuteTemplate(&buf, "base", data); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		`class="dashboard-shell"`,
		`id="dashboard-main"`,
		`class="repo-grid"`,
		`Insurance claim`,
		`alice / insurance-claim`,
		`19`,
		`Private`,
		`Field notes`,
		`bob / field-notes`,
		`Can edit`,
		`data-copy="git clone https://hub.example/alice/insurance-claim.git"`,
		`afs hub login`,
		`afs hub push`,
		`aria-label="Theme: auto (matches your device)"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered dashboard missing %q", want)
		}
	}
	for _, obsolete := range []string{"central space", "git remote add hub", "Self-describing root"} {
		if strings.Contains(out, obsolete) {
			t.Errorf("rendered dashboard still contains obsolete copy %q", obsolete)
		}
	}
}

func TestAuthenticatedDashboardHumanizesSlugAndSetsPrivateCache(t *testing.T) {
	ts, srv, accounts := newDeleteTestServer(t)
	if _, err := accounts.CreateUser("alice", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Storage.EnsureRepo("alice", "insurance-claim"); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(sessionCookieFor(srv, "alice"))
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
		t.Fatalf("GET /alice status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", got)
	}
	page := string(body)
	for _, want := range []string{
		`class="dashboard-shell"`,
		`Insurance claim`,
		`alice / insurance-claim`,
		`Private`,
		`git clone ` + ts.URL + `/alice/insurance-claim.git`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("GET /alice missing %q", want)
		}
	}
}
