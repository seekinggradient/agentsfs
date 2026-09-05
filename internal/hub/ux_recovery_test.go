package hub

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFileTemplateProvidesCompactContextJump(t *testing.T) {
	var out bytes.Buffer
	data := fileData{
		baseData:   baseData{User: "alice", Viewer: "alice", FileView: true},
		Repo:       "notes",
		Path:       "long-note.md",
		Name:       "long-note.md",
		IsMarkdown: true,
	}
	if err := parsePages()["file"].ExecuteTemplate(&out, "base", data); err != nil {
		t.Fatalf("render file: %v", err)
	}
	page := out.String()
	for _, want := range []string{
		`class="note-context-jump" href="#note-context"`,
		`aria-label="Jump to note context"`,
		`id="note-context" class="note-context"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("file page missing %q", want)
		}
	}
}

func TestAuthenticationFormsAssociateLabelsWithInputs(t *testing.T) {
	for _, tc := range []struct {
		asset string
		wants []string
	}{
		{"assets/login.html", []string{`for="login-user"`, `id="login-user"`, `for="login-password"`, `id="login-password"`}},
		{"assets/signup.html", []string{`for="signup-user"`, `id="signup-user"`, `for="signup-email"`, `id="signup-email"`, `for="signup-password"`, `id="signup-password"`}},
	} {
		asset, err := assetsFS.ReadFile(tc.asset)
		if err != nil {
			t.Fatalf("read %s: %v", tc.asset, err)
		}
		markup := string(asset)
		for _, want := range tc.wants {
			if !strings.Contains(markup, want) {
				t.Errorf("%s missing %q", tc.asset, want)
			}
		}
	}
}

func TestBrowserNotFoundPageOffersRecovery(t *testing.T) {
	ts, _ := newTestHub(t)
	res, err := ts.Client().Get(ts.URL + "/missing/repository")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want HTML", got)
	}
	page := string(body)
	for _, want := range []string{"This path does not lead to a note.", "Back to workspaces", `aria-label="Recovery options"`} {
		if !strings.Contains(page, want) {
			t.Errorf("404 page missing %q", want)
		}
	}
}

func TestCompactGraphControlsStayTappable(t *testing.T) {
	styleAsset, err := assetsFS.ReadFile("assets/style.css")
	if err != nil {
		t.Fatal(err)
	}
	style := string(styleAsset)
	for _, want := range []string{
		`[data-graph-labels] { width: 44px; min-width: 44px;`,
		`.graph-camera-tools .graph-tool { min-width: 44px; }`,
	} {
		if !strings.Contains(style, want) {
			t.Errorf("compact graph controls missing %q", want)
		}
	}
}
