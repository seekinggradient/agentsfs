package hub

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestStandaloneHTMLDownload(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	page := `<!doctype html><title>Offline</title><style>body{color:green}</style><img src="../media/a.svg"><img src="../media/lfs.svg"><script>document.title='Works'</script>`
	oid := fmt.Sprintf("%x", sha256.Sum256([]byte(testDiagramSVG)))
	pointer := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", oid, len(testDiagramSVG))
	seedHTMLRepo(t, srv, "alice", "brain", map[string]string{
		"docs/page.html": page, "media/a.svg": testDiagramSVG, "media/lfs.svg": pointer,
		"docs/missing.html": `<img src="missing.svg">`, "note.md": "# Note",
		"docs/large.html":       strings.Repeat("x", maxHTMLImageDocument+1),
		"docs/large-image.html": `<img src="../media/large.svg">`, "media/large.svg": strings.Repeat("x", maxHTMLImageBytes+1),
	})
	if err := srv.LFS.Put("alice", "brain", oid, int64(len(testDiagramSVG)), bytes.NewBufferString(testDiagramSVG)); err != nil {
		t.Fatal(err)
	}
	url := ts.URL + "/alice/brain/download/docs/page.html?format=standalone"
	res, body := getNoRedirect(t, url, true)
	want := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(testDiagramSVG))
	if res.StatusCode != http.StatusOK || strings.Count(body, want) != 2 || strings.Contains(body, "../media/") {
		t.Fatalf("images not packaged: %d %s", res.StatusCode, body)
	}
	for key, want := range map[string]string{
		"Content-Type": "text/html; charset=utf-8", "Content-Length": strconv.Itoa(len(body)),
		"Content-Security-Policy": htmlRenderCSP, "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
	} {
		if got := res.Header.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if disposition := res.Header.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, "page.standalone.html") {
		t.Fatalf("incorrect download disposition: %s", disposition)
	}
	if !strings.Contains(body, "<style>body{color:green}</style>") || !strings.Contains(body, "<script>document.title='Works'</script>") {
		t.Fatal("document styles or scripts changed")
	}
	_, original := getNoRedirect(t, ts.URL+"/alice/brain/download/docs/page.html?format=original", true)
	if original != page {
		t.Fatal("original download changed")
	}
	anon, _ := getNoRedirect(t, url, false)
	if anon.StatusCode != http.StatusFound {
		t.Fatal("private download bypassed login")
	}
	for _, name := range []string{"missing", "large", "large-image"} {
		res, _ := getNoRedirect(t, ts.URL+"/alice/brain/download/docs/"+name+".html?format=standalone", true)
		if res.StatusCode != http.StatusUnprocessableEntity || res.Header.Get("Content-Disposition") != "" {
			t.Errorf("%s should fail visibly, not download incomplete HTML", name)
		}
	}
	other, _ := getNoRedirect(t, ts.URL+"/alice/brain/download/note.md?format=standalone", true)
	if other.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatal("non-HTML standalone accepted")
	}
	_, view := getNoRedirect(t, ts.URL+"/alice/brain/blob/docs/page.html", true)
	if !strings.Contains(view, "?format=standalone") || !strings.Contains(view, "Standalone HTML") {
		t.Fatal("download menu missing standalone option")
	}
	_, noteView := getNoRedirect(t, ts.URL+"/alice/brain/blob/note.md", true)
	if strings.Contains(noteView, "?format=standalone") {
		t.Fatal("standalone offered on non-HTML")
	}
}

func TestStandaloneImageFailureReporting(t *testing.T) {
	for _, src := range []string{"missing.svg", "../../outside.svg", "/private.svg", "note.md"} {
		_, complete := inlineHTMLImagesResult(`<img src="`+src+`">`, "docs/page.html", func(string) (string, bool) { return "", false })
		if complete {
			t.Errorf("%q incorrectly reported complete", src)
		}
	}
	large := strings.Repeat("x", maxHTMLImageBytes)
	_, complete := inlineHTMLImagesResult(strings.Repeat(`<img src="a.svg">`, 30), "page.html", func(string) (string, bool) { return large, true })
	if complete {
		t.Fatal("over-budget export reported complete")
	}
}
