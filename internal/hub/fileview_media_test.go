package hub

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilePreviewKinds(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
	}{
		{"cover.png", "image"},
		{"recording.mp3", "audio"},
		{"clip.mp4", "video"},
		{"paper.pdf", "pdf"},
		{"archive.zip", ""},
	} {
		if got := filePreviewKind(tc.name); got != tc.kind {
			t.Errorf("filePreviewKind(%q) = %q, want %q", tc.name, got, tc.kind)
		}
	}
}

func TestFileViewAudioPreviewAndRawStreaming(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	work := t.TempDir()
	runGit(t, work, "init", "-q", "-b", "main")
	audio := []byte("ID3\x04\x00\x00\x00\x00\x00\x00agentsfs test audio")
	unsupported := bytes.Repeat([]byte{0x00, 0x7f, 0x01}, (1<<20)/3+10)
	if err := os.WriteFile(filepath.Join(work, "recording.mp3"), audio, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "archive.bin"), unsupported, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "recording.mp3", "archive.bin")
	runGit(t, work, "commit", "-m", "add recording")
	bare := srv.Storage.RepoDir("alice", "brain")
	runGit(t, "", "clone", "--bare", work, bare)

	pageReq, err := http.NewRequest(http.MethodGet, ts.URL+"/alice/brain/blob/recording.mp3", nil)
	if err != nil {
		t.Fatal(err)
	}
	pageReq.SetBasicAuth("alice", "s3cret")
	pageRes, err := ts.Client().Do(pageReq)
	if err != nil {
		t.Fatal(err)
	}
	pageBody, readErr := io.ReadAll(pageRes.Body)
	pageRes.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if pageRes.StatusCode != http.StatusOK {
		t.Fatalf("audio file page status = %d, want 200: %s", pageRes.StatusCode, pageBody)
	}
	page := string(pageBody)
	for _, want := range []string{"Audio preview", "<audio controls", "audio/mpeg", "Download"} {
		if !strings.Contains(page, want) {
			t.Errorf("audio page missing %q", want)
		}
	}
	if strings.Contains(page, "Binary file") {
		t.Error("audio page fell through to the binary-file fallback")
	}

	rawReq, err := http.NewRequest(http.MethodGet, ts.URL+"/alice/brain/raw/recording.mp3", nil)
	if err != nil {
		t.Fatal(err)
	}
	rawReq.SetBasicAuth("alice", "s3cret")
	rawRes, err := ts.Client().Do(rawReq)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(rawRes.Body)
	rawRes.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if rawRes.StatusCode != http.StatusOK {
		t.Fatalf("audio raw status = %d, want 200", rawRes.StatusCode)
	}
	if got := rawRes.Header.Get("Content-Type"); got != "audio/mpeg" {
		t.Fatalf("audio raw Content-Type = %q, want audio/mpeg", got)
	}
	if got := rawRes.Header.Get("Content-Disposition"); got != "" {
		t.Fatalf("audio raw Content-Disposition = %q, want inline response", got)
	}
	if !bytes.Equal(raw, audio) {
		t.Fatalf("audio raw bytes = %q, want %q", raw, audio)
	}

	unsupportedReq, err := http.NewRequest(http.MethodGet, ts.URL+"/alice/brain/blob/archive.bin", nil)
	if err != nil {
		t.Fatal(err)
	}
	unsupportedReq.SetBasicAuth("alice", "s3cret")
	unsupportedRes, err := ts.Client().Do(unsupportedReq)
	if err != nil {
		t.Fatal(err)
	}
	unsupportedPage, readErr := io.ReadAll(unsupportedRes.Body)
	unsupportedRes.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if unsupportedRes.StatusCode != http.StatusOK {
		t.Fatalf("unsupported file page status = %d, want 200", unsupportedRes.StatusCode)
	}
	if !strings.Contains(string(unsupportedPage), "too large to preview") {
		t.Error("unsupported large file did not render a graceful preview fallback")
	}
	if strings.Contains(string(unsupportedPage), "Binary file") {
		t.Error("unsupported large file rendered the old binary fallback")
	}

	unsupportedRawReq, err := http.NewRequest(http.MethodGet, ts.URL+"/alice/brain/raw/archive.bin", nil)
	if err != nil {
		t.Fatal(err)
	}
	unsupportedRawReq.SetBasicAuth("alice", "s3cret")
	unsupportedRawRes, err := ts.Client().Do(unsupportedRawReq)
	if err != nil {
		t.Fatal(err)
	}
	n, copyErr := io.Copy(io.Discard, unsupportedRawRes.Body)
	unsupportedRawRes.Body.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if unsupportedRawRes.StatusCode != http.StatusOK || n != int64(len(unsupported)) {
		t.Fatalf("unsupported raw response = status %d, %d bytes; want 200, %d bytes", unsupportedRawRes.StatusCode, n, len(unsupported))
	}
	if got := unsupportedRawRes.Header.Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Fatalf("unsupported raw Content-Disposition = %q, want attachment", got)
	}
}
