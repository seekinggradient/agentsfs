package hub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownDownloads(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	work := t.TempDir()
	runGit(t, "", "init", "-q", "-b", "main", work)
	writeRepoFile(t, work, "NOTE.md", "---\ndescription: Export fixture.\n---\n\n# Export me\n\nA **useful** note with a [link](https://example.com).\n")
	runGit(t, work, "add", "NOTE.md")
	runGit(t, work, "commit", "-m", "add export fixture")
	bare := srv.Storage.RepoDir("alice", "brain")
	runGit(t, "", "clone", "--bare", work, bare)

	for _, tc := range []struct {
		format string
		mime   string
		ext    string
	}{
		{"original", "text/plain; charset=utf-8", ".md"},
		{"markdown", "text/markdown; charset=utf-8", ".md"},
		{"pdf", "application/pdf", ".pdf"},
		{"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"},
	} {
		url := ts.URL + "/alice/brain/download/NOTE.md?format=" + tc.format
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("alice", "s3cret")
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d: %s", tc.format, res.StatusCode, body)
		}
		if got := res.Header.Get("Content-Type"); got != tc.mime {
			t.Errorf("%s Content-Type = %q, want %q", tc.format, got, tc.mime)
		}
		if got := res.Header.Get("Content-Disposition"); !strings.Contains(got, tc.ext) {
			t.Errorf("%s Content-Disposition = %q, want %q", tc.format, got, tc.ext)
		}
		switch tc.format {
		case "original", "markdown":
			if !bytes.Contains(body, []byte("# Export me")) || !bytes.Contains(body, []byte("description: Export fixture.")) {
				t.Errorf("markdown export did not preserve the original note content: %q", body)
			}
		case "pdf":
			if !bytes.HasPrefix(body, []byte("%PDF-1.4")) || !bytes.Contains(body, []byte("xref")) {
				t.Errorf("PDF export does not look like a PDF")
			}
		case "docx":
			z, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
			if err != nil {
				t.Fatalf("DOCX export is not a zip: %v", err)
			}
			foundDocument := false
			for _, file := range z.File {
				if file.Name == "word/document.xml" {
					foundDocument = true
				}
			}
			if !foundDocument {
				t.Error("DOCX export missing word/document.xml")
			}
		}
	}
}

func TestMarkdownExportsRenderTablesAndRepositoryImages(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	work := t.TempDir()
	runGit(t, "", "init", "-q", "-b", "main", work)
	writeRepoFile(t, work, "NOTE.md", "# Inspection\n\n![Cabinet damage](media/damage.png \"Site photo\")\n\n| Area | Condition | Action |\n|---|---|---|\n| Cabinet | Swollen base panel | Replace box |\n")
	if err := os.MkdirAll(filepath.Join(work, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	var imageBody bytes.Buffer
	fixture := image.NewRGBA(image.Rect(0, 0, 32, 20))
	for y := 0; y < fixture.Bounds().Dy(); y++ {
		for x := 0; x < fixture.Bounds().Dx(); x++ {
			fixture.Set(x, y, color.RGBA{R: uint8(80 + x*3), G: uint8(60 + y*4), B: 40, A: 255})
		}
	}
	if err := png.Encode(&imageBody, fixture); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "media", "damage.png"), imageBody.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "NOTE.md", "media/damage.png")
	runGit(t, work, "commit", "-m", "add illustrated inspection")
	bare := srv.Storage.RepoDir("alice", "brain")
	runGit(t, "", "clone", "--bare", work, bare)

	download := func(format string) ([]byte, http.Header) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/alice/brain/download/NOTE.md?format="+format, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("alice", "s3cret")
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
			t.Fatalf("%s status = %d: %s", format, res.StatusCode, body)
		}
		return body, res.Header.Clone()
	}

	markdown, markdownHeaders := download("markdown")
	if got := markdownHeaders.Get("Content-Type"); got != "application/zip" {
		t.Errorf("Markdown bundle Content-Type = %q, want application/zip", got)
	}
	if got := markdownHeaders.Get("Content-Disposition"); !strings.Contains(got, "NOTE.zip") {
		t.Errorf("Markdown bundle Content-Disposition = %q, want NOTE.zip", got)
	}
	bundle, err := zip.NewReader(bytes.NewReader(markdown), int64(len(markdown)))
	if err != nil {
		t.Fatalf("Markdown export with repository assets is not a zip: %v", err)
	}
	bundleParts := map[string][]byte{}
	for _, file := range bundle.File {
		if file.Name != "NOTE.md" && file.Name != "media/damage.png" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		bundleParts[file.Name], err = io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Contains(bundleParts["NOTE.md"], []byte("media/damage.png")) || !bytes.Equal(bundleParts["media/damage.png"], imageBody.Bytes()) {
		t.Error("Markdown bundle did not preserve the note and its repository-relative image")
	}

	pdf, _ := download("pdf")
	for _, want := range [][]byte{[]byte("/Subtype /Image"), []byte("0.72 0.76 0.73 RG"), []byte("(Area) Tj"), []byte("(Condition) Tj"), []byte("(Action) Tj")} {
		if !bytes.Contains(pdf, want) {
			t.Errorf("PDF missing rich export marker %q", want)
		}
	}
	if bytes.Contains(pdf, []byte("Area  -  Condition")) {
		t.Error("PDF flattened a Markdown table into delimiter-separated prose")
	}

	docx, _ := download("docx")
	z, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string][]byte{}
	for _, file := range z.File {
		if file.Name != "word/document.xml" && file.Name != "word/_rels/document.xml.rels" && file.Name != "word/media/image1.png" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		parts[file.Name], err = io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Contains(parts["word/document.xml"], []byte("<w:tbl>")) || !bytes.Contains(parts["word/document.xml"], []byte("<w:drawing>")) || !bytes.Contains(parts["word/document.xml"], []byte(`descr="Cabinet damage"`)) {
		t.Error("DOCX did not preserve the table and accessible inline image")
	}
	if len(parts["word/media/image1.png"]) == 0 || !bytes.Contains(parts["word/_rels/document.xml.rels"], []byte(`Target="media/image1.png"`)) {
		t.Error("DOCX image media or relationship is missing")
	}
}

func TestMarkdownExportLinesPreserveReadableStructure(t *testing.T) {
	content := "---\ndescription: Hidden metadata.\n---\n\n# Brief\n\nA [useful link](https://example.com) and ![diagram](diagram.png) for [[plan|the team]].\n\n- First point\n2. Second point\n\n```go\n  fmt.Println(\"hello\")\n```\n"
	lines := markdownExportLines(content)
	var foundBody, foundBullet, foundNumber, foundCode bool
	for _, line := range lines {
		if strings.Contains(line.Text, "Hidden metadata") || strings.Contains(line.Text, "https://") || strings.Contains(line.Text, "diagram.png") {
			t.Fatalf("generated document leaked Markdown metadata or destinations: %+v", line)
		}
		switch {
		case line.Text == "A useful link and diagram for the team.":
			foundBody = true
		case line.Kind == "bullet" && line.Marker == "*" && line.Text == "First point":
			foundBullet = true
		case line.Kind == "number" && line.Marker == "2." && line.Text == "Second point":
			foundNumber = true
		case line.Kind == "code" && line.Text == `  fmt.Println("hello")`:
			foundCode = true
		}
	}
	if !foundBody || !foundBullet || !foundNumber || !foundCode {
		t.Fatalf("export structure missing body=%v bullet=%v number=%v code=%v: %+v", foundBody, foundBullet, foundNumber, foundCode, lines)
	}

	// A Markdown horizontal rule without a closing delimiter is not frontmatter.
	if got := markdownExportLines("---\n\nBody\n"); len(got) == 0 || got[0].Text != "---" {
		t.Fatalf("horizontal rule was mistaken for frontmatter: %+v", got)
	}
	if got := markdownExportLines("| Name | State |\n|---|---|\n| Report | Ready |\n"); len(got) != 2 || got[1].Cells[0] != "Report" {
		t.Fatalf("table at end of file was dropped: %+v", got)
	}
	if got := markdownImageTargets("Before ![diagram](media/diagram.png \"Title\") after\n\n![remote](https://example.com/x.png)\n"); len(got) != 2 || got[0] != "media/diagram.png" || got[1] != "https://example.com/x.png" {
		t.Fatalf("image targets = %v", got)
	}
}

func TestGeneratedDocumentsIncludePaginationAndRequiredParts(t *testing.T) {
	var content strings.Builder
	content.WriteString("# Export validation\n\nA [useful link](https://example.com).\n\n| Item | Status |\n|---|---|\n| Export | Ready |\n\n- One\n- Two\n- A deliberately long bullet item that wraps onto another line so its continuation aligns with the first line of text instead of stepping inward\n1. A deliberately long numbered item that also wraps and keeps every continuation aligned with its first line of text\n\n")
	for i := 0; i < 40; i++ {
		content.WriteString("# A deliberately long heading that must wrap without leaving the page\n")
	}

	pdf := markdownPDF(content.String())
	if pages := bytes.Count(pdf, []byte("/Type /Page ")); pages < 2 {
		t.Fatalf("heading-heavy PDF has %d pages, want multiple pages", pages)
	}
	if !bytes.Contains(pdf, []byte("Page 2 of")) || bytes.Contains(pdf, []byte("https://example.com")) {
		t.Fatal("PDF pagination/footer or link-label conversion is incorrect")
	}
	if !bytes.Contains(pdf, []byte(`(\225) Tj`)) || bytes.Contains(pdf, []byte("(* One)")) {
		t.Fatal("PDF bullet lists did not use a proper WinAnsi bullet")
	}
	if got := bytes.Count(pdf, []byte("/F1 10 Tf 70 ")); got < 6 || bytes.Contains(pdf, []byte("/F1 10 Tf 68 ")) {
		t.Fatalf("PDF list text did not share one hanging-indent column: aligned commands=%d", got)
	}

	docx, err := markdownDocx(content.String())
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatalf("open generated DOCX: %v", err)
	}
	wantParts := map[string]bool{
		"[Content_Types].xml":          false,
		"_rels/.rels":                  false,
		"word/document.xml":            false,
		"word/styles.xml":              false,
		"word/numbering.xml":           false,
		"word/_rels/document.xml.rels": false,
	}
	for _, file := range z.File {
		if _, ok := wantParts[file.Name]; !ok {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		decoder := xml.NewDecoder(bytes.NewReader(body))
		for {
			if _, err := decoder.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s is invalid XML: %v", file.Name, err)
			}
		}
		if file.Name == "word/document.xml" {
			for _, want := range [][]byte{[]byte("<w:numPr>"), []byte("<w:sectPr>"), []byte("<w:tbl>"), []byte("<w:tblGrid>"), []byte("<w:tblHeader/>"), []byte("A useful link.")} {
				if !bytes.Contains(body, want) {
					t.Errorf("document.xml missing %q", want)
				}
			}
			if bytes.Contains(body, []byte("https://example.com")) {
				t.Error("document.xml leaked a Markdown link destination")
			}
		}
		wantParts[file.Name] = true
	}
	for name, found := range wantParts {
		if !found {
			t.Errorf("DOCX missing required part %s", name)
		}
	}
}

func TestAttachmentDispositionSupportsUnicodeFilenames(t *testing.T) {
	recorder := httptest.NewRecorder()
	setAttachmentDisposition(recorder, "Résumé 2026.docx")
	setRawHeaders(recorder, "Résumé 2026.bin", true)
	mediaType, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "attachment" || params["filename"] != "Résumé 2026.docx" {
		t.Fatalf("Content-Disposition = %q", recorder.Header().Get("Content-Disposition"))
	}
}

func TestFileHistoryShowsInlineDiff(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	work := t.TempDir()
	runGit(t, "", "init", "-q", "-b", "main", work)
	writeRepoFile(t, work, "NOTE.md", "# First\n\noriginal\n")
	writeRepoFile(t, work, "OTHER.md", "other-original\n")
	runGit(t, work, "add", "NOTE.md")
	runGit(t, work, "add", "OTHER.md")
	runGit(t, work, "commit", "-m", "first note")
	writeRepoFile(t, work, "NOTE.md", "# First\n\nupdated\n")
	writeRepoFile(t, work, "OTHER.md", "other-updated-secret\n")
	runGit(t, work, "add", "NOTE.md")
	runGit(t, work, "add", "OTHER.md")
	runGit(t, work, "commit", "-m", "update note")
	bare := srv.Storage.RepoDir("alice", "brain")
	runGit(t, "", "clone", "--bare", work, bare)
	commit := RepoLogPath("git", bare, defaultRef, "NOTE.md", 1)[0]

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/alice/brain/blob/NOTE.md?commit="+commit.Hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("alice", "s3cret")
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(res.Body)
	res.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	page := string(body)
	for _, want := range []string{"context-history-diff", "Changes in this file", "-original", "&#43;updated", "#file-commit-" + commit.Hash, "aria-current=\"true\"", "View diff", "Viewing"} {
		if !strings.Contains(page, want) {
			t.Errorf("file page missing %q", want)
		}
	}
	if strings.Contains(page, "other-updated-secret") {
		t.Error("inline file diff included changes from another file in the commit")
	}
	if got := strings.Count(page, `class="download-menu"`); got != 1 {
		t.Errorf("file page has %d download menus, want one", got)
	}
	if !strings.Contains(page, `href="#downloads"`) || !strings.Contains(page, `id="downloads"`) {
		t.Error("inline file diff does not link to the toolbar download menu")
	}
}

func TestDownloadRouteDoesNotExportBinaryAsDocument(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	work := t.TempDir()
	runGit(t, "", "init", "-q", "-b", "main", work)
	if err := os.WriteFile(filepath.Join(work, "image.png"), []byte("not really an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "image.png")
	runGit(t, work, "commit", "-m", "add image")
	bare := srv.Storage.RepoDir("alice", "brain")
	runGit(t, "", "clone", "--bare", work, bare)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/alice/brain/download/image.png?format=pdf", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("alice", "s3cret")
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("binary PDF export status = %d, want %d", res.StatusCode, http.StatusUnsupportedMediaType)
	}
}
