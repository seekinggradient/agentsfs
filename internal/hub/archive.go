package hub

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"time"
)

type repoArchiveEntry struct {
	tree repoTreeEntry
	lfs  *LFSPointer
}

// handleRepoDownload serves a ready-to-use ZIP snapshot of the repository at
// one immutable commit. Unlike git archive, it resolves Git LFS pointers so a
// knowledge worker receives the actual images and documents in the snapshot.
func (s *Server) handleRepoDownload(w http.ResponseWriter, r *http.Request, user, repo string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bare := s.Storage.RepoDir(user, repo)
	oid := headOID("git", bare, defaultRef)
	if oid == "" {
		http.Error(w, "repository has no commits", http.StatusConflict)
		return
	}
	entries, err := s.repoArchiveEntries(user, repo, bare, oid)
	if err != nil {
		s.Log.Printf("prepare repository download %s/%s: %v", user, repo, err)
		http.Error(w, "could not prepare the repository download", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	setAttachmentDisposition(w, repo+".zip")
	if r.Method == http.MethodHead {
		return
	}
	modified := time.Date(1980, time.January, 2, 0, 0, 0, 0, time.UTC)
	if commits := RepoLog("git", bare, oid, 1); len(commits) == 1 && commits[0].When > 0 {
		modified = time.Unix(commits[0].When, 0).UTC()
	}
	if err := s.writeRepoArchive(w, user, repo, bare, oid, modified, entries); err != nil {
		// Headers may already be committed, so log the interrupted transfer. The
		// browser will reject the incomplete ZIP instead of accepting bad data.
		s.Log.Printf("write repository download %s/%s: %v", user, repo, err)
	}
}

func (s *Server) repoArchiveEntries(user, repo, bare, oid string) ([]repoArchiveEntry, error) {
	tree, err := repoTreeEntries("git", bare, oid)
	if err != nil {
		return nil, err
	}
	entries := make([]repoArchiveEntry, 0, len(tree))
	for _, item := range tree {
		if !validRepoPath(item.Path) {
			return nil, fmt.Errorf("unsafe repository path %q", item.Path)
		}
		entry := repoArchiveEntry{tree: item}
		size, ok := BlobSize("git", bare, oid, item.Path)
		if !ok {
			return nil, fmt.Errorf("read size for %q", item.Path)
		}
		if size <= 1024 {
			content, ok := BlobContent("git", bare, oid, item.Path)
			if !ok {
				return nil, fmt.Errorf("read %q", item.Path)
			}
			if pointer, isPointer := ParseLFSPointer(content); isPointer {
				if s.LFS == nil {
					return nil, fmt.Errorf("Git LFS is unavailable for %q", item.Path)
				}
				rc, _, err := s.LFS.Open(user, repo, pointer.OID, pointer.Size)
				if err != nil {
					return nil, fmt.Errorf("open Git LFS object for %q: %w", item.Path, err)
				}
				_ = rc.Close()
				entry.lfs = &pointer
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *Server) writeRepoArchive(dst io.Writer, user, repo, bare, oid string, modified time.Time, entries []repoArchiveEntry) error {
	zw := zip.NewWriter(dst)
	root := repo + "/"
	for _, entry := range entries {
		header := &zip.FileHeader{Name: path.Join(root, entry.tree.Path), Method: zip.Deflate}
		header.SetModTime(modified)
		switch entry.tree.Mode {
		case "100755":
			header.SetMode(0o755)
		case "120000":
			header.SetMode(os.ModeSymlink | 0o777)
		default:
			header.SetMode(0o644)
		}
		part, err := zw.CreateHeader(header)
		if err != nil {
			_ = zw.Close()
			return err
		}
		if entry.lfs != nil {
			rc, _, err := s.LFS.Open(user, repo, entry.lfs.OID, entry.lfs.Size)
			if err != nil {
				_ = zw.Close()
				return err
			}
			_, copyErr := io.Copy(part, rc)
			closeErr := rc.Close()
			if copyErr != nil {
				_ = zw.Close()
				return copyErr
			}
			if closeErr != nil {
				_ = zw.Close()
				return closeErr
			}
			continue
		}
		if err := StreamBlob("git", bare, oid, entry.tree.Path, part); err != nil {
			_ = zw.Close()
			return err
		}
	}
	return zw.Close()
}
