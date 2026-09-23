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

const testDiagramSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="240" height="80"><rect width="240" height="80" fill="green"/><text x="10" y="45" fill="white">Linked SVG renders</text></svg>`

func TestHTMLImagesPrivateRepo(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	page := `<!doctype html><title>Images</title><img src="../media/diagram.svg"><img src="../media/lfs.svg">`
	oid := fmt.Sprintf("%x", sha256.Sum256([]byte(testDiagramSVG)))
	pointer := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", oid, len(testDiagramSVG))
	seedHTMLRepo(t, srv, "alice", "brain", map[string]string{
		"explainers/page.html": page,
		"media/diagram.svg":    testDiagramSVG,
		"media/lfs.svg":        pointer,
	})
	if err := srv.LFS.Put("alice", "brain", oid, int64(len(testDiagramSVG)), bytes.NewBufferString(testDiagramSVG)); err != nil {
		t.Fatal(err)
	}
	res, body := getNoRedirect(t, ts.URL+"/alice/brain/render/explainers/page.html", true)
	want := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(testDiagramSVG))
	if res.StatusCode != http.StatusOK || strings.Count(body, want) != 2 {
		t.Fatalf("private images not embedded: status %d, body %s", res.StatusCode, body)
	}
	if res.Header.Get("Content-Security-Policy") != htmlRenderCSP || res.Header.Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatal("render headers changed or content length is stale")
	}
	anonymous, _ := getNoRedirect(t, ts.URL+"/alice/brain/render/explainers/page.html", false)
	if anonymous.StatusCode != http.StatusFound {
		t.Fatal("private HTML must still require login")
	}
	raw, rawBody := getNoRedirect(t, ts.URL+"/alice/brain/raw/explainers/page.html", true)
	if raw.StatusCode != http.StatusOK || rawBody != page {
		t.Fatal("stored source must remain unchanged")
	}
	// The SVG cannot be opened as an active document through /render.
	svg, _ := getNoRedirect(t, ts.URL+"/alice/brain/render/media/diagram.svg", true)
	if svg.StatusCode != http.StatusNotFound {
		t.Fatal("SVG became a live document")
	}
}

func TestHTMLImageRewritePreservesNonImages(t *testing.T) {
	untouched := `<!-- <img src="../media/no.svg"> --><script>const s = '<img src="../media/no.svg">';</script><textarea><img src="../media/no.svg"></textarea>`
	untouched += `<img src="https://example.com/x.svg"><img src="//example.com/x.svg"><img src="/other/private.svg"><img src="../../private.svg"><img src="data:image/svg+xml;base64,abc"><img src="../secret.txt"><img src="../missing.svg">`
	input := untouched + `<IMG alt="A &amp; B" SRC="../media/a%20b.SVG?version=1#view"><img src='../media/a%20b.SVG'>`
	reads := map[string]int{}
	got := inlineHTMLImages(input, "explainers/page.html", func(rel string) (string, bool) {
		reads[rel]++
		return testDiagramSVG, rel == "media/a b.SVG"
	})
	if !strings.HasPrefix(got, untouched) || strings.Count(got, "data:image/svg+xml;base64,") != 3 || !strings.Contains(got, "#view") {
		t.Fatalf("unexpected rewrite: %s", got)
	}
	if len(reads) != 2 || reads["media/a b.SVG"] != 1 || reads["missing.svg"] != 1 {
		t.Fatalf("unexpected file reads: %#v", reads)
	}
}

func TestHTMLImageBudgets(t *testing.T) {
	var body strings.Builder
	for i := 0; i < maxHTMLImageLookups+20; i++ {
		fmt.Fprintf(&body, `<img src="%d.svg">`, i)
	}
	reads := 0
	inlineHTMLImages(body.String(), "page.html", func(string) (string, bool) {
		reads++
		return "", false
	})
	if reads != maxHTMLImageLookups {
		t.Fatalf("unbounded lookups: %d", reads)
	}
	large := strings.Repeat("x", maxHTMLImageBytes)
	got := inlineHTMLImages(strings.Repeat(`<img src="a.svg">`, 30), "page.html", func(string) (string, bool) { return large, true })
	if len(got) > maxHTMLImageOutput || !strings.Contains(got, `src="a.svg"`) {
		t.Fatal("output limit not enforced")
	}
}

func TestSharedHTMLDoesNotEmbedUnsharedImages(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	page := `<img src="private.svg">`
	seedShareRepo(t, srv, "alice", "brain", map[string]string{
		"page.html": page, "private.svg": testDiagramSVG,
	})
	token := mintShareLink(t, ts, srv, "alice", "brain", "page.html", false)
	res, body := getShared(t, ts, "/s/"+token)
	if res.StatusCode != http.StatusOK || body != page {
		t.Fatal("file-scoped share must not acquire access to other repository files")
	}
}
