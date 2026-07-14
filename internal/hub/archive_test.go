package hub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoDownloadIncludesTrackedFilesAndResolvesLFS(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	work := t.TempDir()
	runGit(t, "", "init", "-q", "-b", "main", work)
	writeRepoFile(t, work, "NOTE.md", "# Repository download\n")
	writeRepoFile(t, work, "scripts/check.sh", "#!/bin/sh\necho ready\n")
	if err := os.Chmod(filepath.Join(work, "scripts", "check.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	asset := []byte("actual image bytes from Git LFS")
	oid := lfsOID(asset)
	pointer := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", oid, len(asset))
	writeRepoFile(t, work, "media/photo.png", pointer)
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "seed downloadable repository")
	bare := srv.Storage.RepoDir("alice", "brain")
	runGit(t, "", "clone", "--bare", work, bare)
	if err := srv.LFS.Put("alice", "brain", oid, int64(len(asset)), bytes.NewReader(asset)); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/alice/brain/download", nil)
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
		t.Fatalf("download status = %d: %s", res.StatusCode, body)
	}
	if got := res.Header.Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", got)
	}
	if got := res.Header.Get("Content-Disposition"); !strings.Contains(got, "brain.zip") {
		t.Errorf("Content-Disposition = %q, want brain.zip", got)
	}

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("repository download is not a valid zip: %v", err)
	}
	parts := map[string][]byte{}
	modes := map[string]os.FileMode{}
	for _, file := range zr.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		parts[file.Name], err = io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		modes[file.Name] = file.Mode()
	}
	if got := string(parts["brain/NOTE.md"]); got != "# Repository download\n" {
		t.Errorf("NOTE.md = %q", got)
	}
	if !bytes.Equal(parts["brain/media/photo.png"], asset) {
		t.Errorf("LFS asset was not resolved: %q", parts["brain/media/photo.png"])
	}
	if modes["brain/scripts/check.sh"].Perm()&0o111 == 0 {
		t.Errorf("executable mode was not preserved: %v", modes["brain/scripts/check.sh"])
	}
	for name := range parts {
		if strings.Contains(name, ".git/") {
			t.Errorf("archive leaked Git internals: %s", name)
		}
	}

	post, err := http.NewRequest(http.MethodPost, ts.URL+"/alice/brain/download", nil)
	if err != nil {
		t.Fatal(err)
	}
	post.SetBasicAuth("alice", "s3cret")
	postRes, err := ts.Client().Do(post)
	if err != nil {
		t.Fatal(err)
	}
	postRes.Body.Close()
	if postRes.StatusCode != http.StatusMethodNotAllowed || postRes.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("POST status/Allow = %d %q", postRes.StatusCode, postRes.Header.Get("Allow"))
	}
}
