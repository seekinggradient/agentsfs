package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const lfsMediaType = "application/vnd.git-lfs+json"

// Keep the first implementation bounded until per-user quotas exist. The
// storage layer streams to disk, so this is a product guardrail rather than a
// memory-safety limit.
const maxLFSObjectSize int64 = 10 << 30 // 10 GiB

var oidRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LFSStore is the blob store behind the Git LFS Batch API. Objects are
// content-addressed by SHA-256 and immutable; the first backend stores them on
// the same persistent volume as the bare git repos.
type LFSStore interface {
	Exists(user, repo, oid string, size int64) (bool, error)
	Put(user, repo, oid string, size int64, r io.Reader) error
	Open(user, repo, oid string, size int64) (io.ReadCloser, int64, error)
	RenameRepo(user, oldRepo, newRepo string) error
}

// LocalLFSStore stores objects under Root/<user>/<repo>/<shard>/<oid>.
type LocalLFSStore struct {
	Root string
}

func NewLocalLFSStore(root string) (*LocalLFSStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &LocalLFSStore{Root: abs}, nil
}

func (s *LocalLFSStore) repoDir(user, repo string) (string, error) {
	if !nameRe.MatchString(user) || !nameRe.MatchString(repo) {
		return "", errors.New("invalid lfs object path")
	}
	return filepath.Join(s.Root, strings.ToLower(user), repo), nil
}

func (s *LocalLFSStore) objectPath(user, repo, oid string) (string, error) {
	oid = strings.ToLower(strings.TrimSpace(oid))
	if !validLFSOID(oid) {
		return "", errors.New("invalid lfs object path")
	}
	dir, err := s.repoDir(user, repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, oid[:2], oid), nil
}

func (s *LocalLFSStore) Exists(user, repo, oid string, size int64) (bool, error) {
	p, err := s.objectPath(user, repo, oid)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !info.IsDir() && (size < 0 || info.Size() == size), nil
}

func (s *LocalLFSStore) Put(user, repo, oid string, size int64, r io.Reader) error {
	if !validLFSSize(size) {
		return fmt.Errorf("invalid lfs object size")
	}
	p, err := s.objectPath(user, repo, oid)
	if err != nil {
		return err
	}
	if ok, err := s.Exists(user, repo, oid, size); ok || err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	h := sha256.New()
	lr := io.LimitReader(r, size+1)
	n, copyErr := io.Copy(tmp, io.TeeReader(lr, h))
	closeErr := tmp.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != size {
		return fmt.Errorf("lfs object size mismatch: got %d, want %d", n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(oid) {
		return fmt.Errorf("lfs object sha256 mismatch")
	}
	if ok, err := s.Exists(user, repo, oid, size); ok || err != nil {
		return err
	}
	if err := os.Rename(tmpName, p); err != nil {
		return err
	}
	return nil
}

func (s *LocalLFSStore) Open(user, repo, oid string, size int64) (io.ReadCloser, int64, error) {
	p, err := s.objectPath(user, repo, oid)
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, 0, err
	}
	if info.IsDir() || (size >= 0 && info.Size() != size) {
		return nil, 0, os.ErrNotExist
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func (s *LocalLFSStore) RenameRepo(user, oldRepo, newRepo string) error {
	oldDir, err := s.repoDir(user, oldRepo)
	if err != nil {
		return err
	}
	newDir, err := s.repoDir(user, newRepo)
	if err != nil {
		return err
	}
	if _, err := os.Stat(oldDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
		return err
	}
	return os.Rename(oldDir, newDir)
}

func validLFSOID(oid string) bool {
	return oidRe.MatchString(strings.ToLower(strings.TrimSpace(oid)))
}

func validLFSSize(size int64) bool {
	return size >= 0 && size <= maxLFSObjectSize
}

// LFSPointer is the small text file committed into git in place of a large
// object.
type LFSPointer struct {
	OID  string
	Size int64
}

func ParseLFSPointer(content string) (LFSPointer, bool) {
	var ptr LFSPointer
	sizeSeen := false
	if len(content) > 1024 || !strings.HasPrefix(content, "version https://git-lfs.github.com/spec/v1\n") {
		return ptr, false
	}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		if oid, ok := strings.CutPrefix(line, "oid sha256:"); ok {
			oid = strings.ToLower(strings.TrimSpace(oid))
			if !validLFSOID(oid) {
				return LFSPointer{}, false
			}
			ptr.OID = oid
			continue
		}
		if size, ok := strings.CutPrefix(line, "size "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
			if err != nil || !validLFSSize(n) {
				return LFSPointer{}, false
			}
			ptr.Size = n
			sizeSeen = true
		}
	}
	return ptr, ptr.OID != "" && sizeSeen
}

func isLFSService(rest string) bool {
	return strings.HasPrefix(rest, "info/lfs/")
}

type lfsBatchRequest struct {
	Operation string      `json:"operation"`
	Transfers []string    `json:"transfers,omitempty"`
	Objects   []lfsObject `json:"objects"`
}

type lfsBatchResponse struct {
	Transfer string      `json:"transfer,omitempty"`
	Objects  []lfsObject `json:"objects"`
}

type lfsObject struct {
	OID           string               `json:"oid"`
	Size          int64                `json:"size"`
	Authenticated bool                 `json:"authenticated,omitempty"`
	Actions       map[string]lfsAction `json:"actions,omitempty"`
	Error         *lfsObjectError      `json:"error,omitempty"`
}

type lfsAction struct {
	Href   string            `json:"href"`
	Header map[string]string `json:"header,omitempty"`
}

type lfsObjectError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) serveLFS(w http.ResponseWriter, r *http.Request, user, repo, rest string) {
	if s.LFS == nil {
		http.Error(w, "lfs not configured", http.StatusServiceUnavailable)
		return
	}
	if !s.Storage.Exists(user, repo) {
		if dest, ok := s.Storage.LookupRedirect(user, repo); ok {
			target := "/" + user + "/" + dest + ".git/" + rest
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
	}

	switch {
	case rest == "info/lfs/objects/batch":
		s.handleLFSBatch(w, r, user, repo)
	case strings.HasPrefix(rest, "info/lfs/objects/"):
		s.handleLFSObject(w, r, user, repo, strings.TrimPrefix(rest, "info/lfs/objects/"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleLFSBatch(w http.ResponseWriter, r *http.Request, user, repo string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req lfsBatchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
		writeLFSJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid lfs batch request"})
		return
	}
	req.Operation = strings.ToLower(strings.TrimSpace(req.Operation))
	if req.Operation != "upload" && req.Operation != "download" {
		writeLFSJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "unsupported lfs operation"})
		return
	}
	write := req.Operation == "upload"
	if _, ok := s.authorizeLFS(r, user, repo, write); !ok {
		unauthorizedLFS(w)
		return
	}

	resp := lfsBatchResponse{Transfer: "basic"}
	for _, obj := range req.Objects {
		obj.OID = strings.ToLower(strings.TrimSpace(obj.OID))
		out := lfsObject{OID: obj.OID, Size: obj.Size, Authenticated: true}
		if !validLFSOID(obj.OID) || !validLFSSize(obj.Size) {
			out.Error = &lfsObjectError{Code: http.StatusUnprocessableEntity, Message: "invalid lfs object"}
			resp.Objects = append(resp.Objects, out)
			continue
		}

		exists, err := s.LFS.Exists(user, repo, obj.OID, obj.Size)
		if err != nil {
			out.Error = &lfsObjectError{Code: http.StatusInternalServerError, Message: "could not inspect lfs object"}
			resp.Objects = append(resp.Objects, out)
			continue
		}

		base := hubBase(r) + "/" + user + "/" + repo + ".git/info/lfs/objects/" + obj.OID
		switch req.Operation {
		case "upload":
			if !exists {
				out.Actions = map[string]lfsAction{
					"upload": {Href: base + "?size=" + strconv.FormatInt(obj.Size, 10), Header: lfsActionHeaders(r)},
					"verify": {Href: base + "/verify?size=" + strconv.FormatInt(obj.Size, 10), Header: lfsActionHeaders(r)},
				}
			}
		case "download":
			if exists {
				out.Actions = map[string]lfsAction{
					"download": {Href: base, Header: lfsActionHeaders(r)},
				}
			} else {
				out.Error = &lfsObjectError{Code: http.StatusNotFound, Message: "lfs object not found"}
			}
		}
		resp.Objects = append(resp.Objects, out)
	}
	writeLFSJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLFSObject(w http.ResponseWriter, r *http.Request, user, repo, rest string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || !validLFSOID(parts[0]) {
		http.NotFound(w, r)
		return
	}
	oid := strings.ToLower(parts[0])
	verify := len(parts) == 2 && parts[1] == "verify"
	if len(parts) > 2 || (len(parts) == 2 && !verify) {
		http.NotFound(w, r)
		return
	}

	switch {
	case verify:
		s.handleLFSVerify(w, r, user, repo, oid)
	case r.Method == http.MethodPut:
		s.handleLFSUpload(w, r, user, repo, oid)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		s.handleLFSDownload(w, r, user, repo, oid)
	default:
		w.Header().Set("Allow", "GET, HEAD, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleLFSUpload(w http.ResponseWriter, r *http.Request, user, repo, oid string) {
	size, ok := lfsSizeFromRequest(r)
	if !ok {
		writeLFSJSON(w, http.StatusBadRequest, map[string]string{"message": "missing or invalid lfs object size"})
		return
	}
	if _, ok := s.authorizeLFS(r, user, repo, true); !ok {
		unauthorizedLFS(w)
		return
	}
	if err := s.LFS.Put(user, repo, oid, size, r.Body); err != nil {
		s.Log.Printf("lfs upload %s/%s %s: %v", user, repo, oid, err)
		writeLFSJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": err.Error()})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleLFSVerify(w http.ResponseWriter, r *http.Request, user, repo, oid string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	size, ok := lfsSizeFromRequest(r)
	if !ok {
		var body struct {
			OID  string `json:"oid"`
			Size int64  `json:"size"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)
		size = body.Size
		ok = strings.EqualFold(body.OID, oid) && validLFSSize(size)
	}
	if !ok {
		writeLFSJSON(w, http.StatusBadRequest, map[string]string{"message": "missing or invalid lfs object size"})
		return
	}
	if _, ok := s.authorizeLFS(r, user, repo, true); !ok {
		unauthorizedLFS(w)
		return
	}
	exists, err := s.LFS.Exists(user, repo, oid, size)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !exists {
		writeLFSJSON(w, http.StatusNotFound, map[string]string{"message": "lfs object not found"})
		return
	}
	writeLFSJSON(w, http.StatusOK, map[string]string{})
}

func (s *Server) handleLFSDownload(w http.ResponseWriter, r *http.Request, user, repo, oid string) {
	if _, ok := s.authorizeLFS(r, user, repo, false); !ok {
		unauthorizedLFS(w)
		return
	}
	rc, size, err := s.LFS.Open(user, repo, oid, -1)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		return
	}
	io.Copy(w, rc)
}

func (s *Server) authorizeLFS(r *http.Request, user, repo string, write bool) (string, bool) {
	authUser, valid := s.userForToken(tokenFromRequest(r))
	owner := valid && authUser == user
	role := ""
	if valid && !owner {
		role = s.Accounts.CollaboratorRole(user, repo, authUser)
	}
	switch {
	case owner:
		if write {
			if err := s.Storage.EnsureRepo(user, repo); err != nil {
				s.Log.Printf("ensure repo %s/%s for lfs: %v", user, repo, err)
				return "", false
			}
		}
		return authUser, true
	case role == "write" && s.Storage.Exists(user, repo):
		return authUser, true
	case !write && s.Storage.Exists(user, repo) && (role == "read" || s.isPublic(user, repo)):
		return authUser, true
	default:
		return "", false
	}
}

func lfsActionHeaders(r *http.Request) map[string]string {
	if h := r.Header.Get("Authorization"); h != "" {
		return map[string]string{"Authorization": h}
	}
	return nil
}

func lfsSizeFromRequest(r *http.Request) (int64, bool) {
	n, err := strconv.ParseInt(r.URL.Query().Get("size"), 10, 64)
	return n, err == nil && validLFSSize(n)
}

func writeLFSJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", lfsMediaType+"; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func unauthorizedLFS(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="afs-hub-lfs"`)
	writeLFSJSON(w, http.StatusUnauthorized, map[string]string{"message": "unauthorized"})
}
