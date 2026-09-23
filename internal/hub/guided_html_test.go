package hub

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestGuidedOriginalHTMLAssets(t *testing.T) {
	_, srv, _ := newShareTestHub(t)
	files := map[string]string{
		"docs/page.html":              `<!doctype html><html><head><style>.hero{color:red}</style><link rel="stylesheet" href="../media/page.css"></head><body><h1 id="intro">Article text</h1><img src="../media/diagram.svg"><img src="https://other.test/tracker.png"><img src="../../outside.svg"><img src="../media/code.js"></body></html>`,
		"media/diagram.svg":           `<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0L10 10"/></svg>`,
		"media/page.css":              `.hero{display:grid}`,
		"media/code.js":               "alert(1)",
		"docs/page.html.capture.json": `{"source":"./page.html","blocks":[{"id":"intro","text":"Article text"}]}`,
	}
	seedShareRepo(t, srv, "alice", "brain", files)
	bare := srv.Storage.RepoDir("alice", "brain")
	b64, ref := resolveGuidedCapture(bare, "docs/tour.md", "---\nsource: ./page.html\n---\n", "https://hub.example", "alice", "brain")
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || ref != "./page.html" {
		t.Fatal("source not resolved", err)
	}
	var payload struct {
		HTML   string            `json:"html"`
		Blocks []json.RawMessage `json:"blocks"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Blocks) != 1 {
		t.Fatal("capture changed")
	}
	for _, want := range []string{`id="intro"`, `.hero{color:red}`, `.hero{display:grid}`, `data:image/svg+xml;base64,`} {
		if !strings.Contains(payload.HTML, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(payload.HTML, "alert(1)") {
		t.Fatal("embedded executable asset")
	}
	if strings.Count(payload.HTML, "data:image/") != 1 {
		t.Fatal("embedded an unapproved asset")
	}
}
