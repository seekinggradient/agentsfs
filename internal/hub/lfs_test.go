package hub

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func lfsOID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newLFSRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("alice", "s3cret")
	return req
}

func doLFS(t *testing.T, c *http.Client, req *http.Request, want int) *http.Response {
	t.Helper()
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("%s %s status = %d, want %d\n%s", req.Method, req.URL, res.StatusCode, want, body)
	}
	return res
}

func applyLFSActionHeaders(req *http.Request, a lfsAction) {
	for k, v := range a.Header {
		req.Header.Set(k, v)
	}
}

func TestLFSBatchUploadDownload(t *testing.T) {
	ts, _ := newTestHub(t)
	data := []byte("image bytes that live outside git")
	oid := lfsOID(data)

	var uploadReq bytes.Buffer
	json.NewEncoder(&uploadReq).Encode(lfsBatchRequest{
		Operation: "upload",
		Transfers: []string{"basic"},
		Objects:   []lfsObject{{OID: oid, Size: int64(len(data))}},
	})
	req := newLFSRequest(t, http.MethodPost, ts.URL+"/alice/brain.git/info/lfs/objects/batch", &uploadReq)
	req.Header.Set("Content-Type", lfsMediaType)
	res := doLFS(t, ts.Client(), req, http.StatusOK)
	var uploadResp lfsBatchResponse
	if err := json.NewDecoder(res.Body).Decode(&uploadResp); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(uploadResp.Objects) != 1 || uploadResp.Objects[0].Actions["upload"].Href == "" {
		t.Fatalf("upload batch response missing upload action: %+v", uploadResp)
	}

	upload := uploadResp.Objects[0].Actions["upload"]
	put := newLFSRequest(t, http.MethodPut, upload.Href, bytes.NewReader(data))
	applyLFSActionHeaders(put, upload)
	doLFS(t, ts.Client(), put, http.StatusOK).Body.Close()

	verify := uploadResp.Objects[0].Actions["verify"]
	postVerify := newLFSRequest(t, http.MethodPost, verify.Href, strings.NewReader(`{}`))
	applyLFSActionHeaders(postVerify, verify)
	doLFS(t, ts.Client(), postVerify, http.StatusOK).Body.Close()

	var downloadReq bytes.Buffer
	json.NewEncoder(&downloadReq).Encode(lfsBatchRequest{
		Operation: "download",
		Transfers: []string{"basic"},
		Objects:   []lfsObject{{OID: oid, Size: int64(len(data))}},
	})
	req = newLFSRequest(t, http.MethodPost, ts.URL+"/alice/brain.git/info/lfs/objects/batch", &downloadReq)
	req.Header.Set("Content-Type", lfsMediaType)
	res = doLFS(t, ts.Client(), req, http.StatusOK)
	var downloadResp lfsBatchResponse
	if err := json.NewDecoder(res.Body).Decode(&downloadResp); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	download := downloadResp.Objects[0].Actions["download"]
	get := newLFSRequest(t, http.MethodGet, download.Href, nil)
	applyLFSActionHeaders(get, download)
	res = doLFS(t, ts.Client(), get, http.StatusOK)
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Equal(got, data) {
		t.Fatalf("downloaded LFS object = %q, want %q", got, data)
	}
}

func TestRawResolvesLFSPointer(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	runGit(t, "", "init", "-b", "main", work)

	data := []byte("\x89PNG\r\n\x1a\nnot really a png")
	oid := lfsOID(data)
	pointer := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", oid, len(data))
	writeRepoFile(t, work, "photo.png", pointer)
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "add lfs pointer")

	bare := srv.Storage.RepoDir("alice", "brain")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "--bare", work, bare)
	if err := srv.LFS.Put("alice", "brain", oid, int64(len(data)), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}

	req := newLFSRequest(t, http.MethodGet, ts.URL+"/alice/brain/raw/photo.png", nil)
	res := doLFS(t, ts.Client(), req, http.StatusOK)
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Equal(got, data) {
		t.Fatalf("raw LFS bytes = %q, want %q", got, data)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
}

func TestLocalLFSStoreRenameRepo(t *testing.T) {
	store, err := NewLocalLFSStore(filepath.Join(t.TempDir(), ".lfs"))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("rename keeps lfs bytes")
	oid := lfsOID(data)
	if err := store.Put("alice", "old", oid, int64(len(data)), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := store.RenameRepo("alice", "old", "new"); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Exists("alice", "old", oid, int64(len(data))); err != nil || ok {
		t.Fatalf("old repo LFS object exists = %v, err = %v; want false, nil", ok, err)
	}
	rc, _, err := store.Open("alice", "new", oid, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, data) {
		t.Fatalf("renamed LFS object = %q, want %q", got, data)
	}
}

func runGitExtraEnv(t *testing.T, extra []string, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(), extra...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestGitLFSClientPushPull(t *testing.T) {
	if _, err := exec.LookPath("git-lfs"); err != nil {
		t.Skip("git-lfs not installed")
	}
	_, authURL := newTestHub(t)
	tmp := t.TempDir()

	work1 := filepath.Join(tmp, "work1")
	runGit(t, "", "-c", "init.defaultBranch=main", "clone", authURL("alice", "s3cret", "media"), work1)
	runGit(t, work1, "lfs", "install", "--local")
	writeRepoFile(t, work1, ".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n")
	want := []byte("lfs client round trip bytes")
	if err := os.WriteFile(filepath.Join(work1, "asset.bin"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work1, "add", "-A")
	runGit(t, work1, "commit", "-m", "add lfs asset")
	runGit(t, work1, "push", "origin", "main")

	work2 := filepath.Join(tmp, "work2")
	runGitExtraEnv(t, []string{"GIT_LFS_SKIP_SMUDGE=1"}, "", "clone", authURL("alice", "s3cret", "media"), work2)
	runGit(t, work2, "lfs", "install", "--local")
	runGit(t, work2, "lfs", "pull")
	got, err := os.ReadFile(filepath.Join(work2, "asset.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("pulled LFS file = %q, want %q", got, want)
	}
}
