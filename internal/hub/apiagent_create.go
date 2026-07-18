package hub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	afs "agentsfs.ai/afs"
	"agentsfs.ai/afs/internal/core"
)

// apiCreateRepoRequest is the body of POST /api/agent/v1/repos.
type apiCreateRepoRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// apiCreateRepo creates a new bare repo in the CALLER'S OWN namespace — user is
// always the PAT's resolved identity (handleAPIAgent), never a value read from
// the request body, so this can never create a repo in someone else's
// namespace. Today a repo only comes into being on first `git push`
// (server.go's EnsureRepo call in the git-http handler) or by hand in the web
// UI; this is the create leg the agent API was missing — an agent
// provisioning its own new knowledge base without shelling out to git.
//
// The repo is private by default (isPublic's default) and seeded with a real
// first commit of the embedded AgentsFS contract template (seedContractTemplate)
// so it is contract-complete from birth — AGENTS.md, a root INDEX.md, the
// journal/scratch dirs — instead of an empty ref the caller has to bootstrap
// by hand. description, when given, replaces the root INDEX.md's REPLACE-ME
// placeholder so the Hub's repo listing shows a real label immediately (see
// core.RootDescriptionPlaceholder, repoFilesMeta).
func (s *Server) apiCreateRepo(w http.ResponseWriter, r *http.Request, user string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req apiCreateRepoRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "bad json")
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if name == "" || !nameRe.MatchString(name) {
		apiError(w, http.StatusUnprocessableEntity, "invalid repo name")
		return
	}
	if s.Storage.Exists(user, name) {
		apiError(w, http.StatusConflict, "a repo with that name already exists")
		return
	}
	if err := s.Storage.EnsureRepo(user, name); err != nil {
		s.Log.Printf("ensure repo %s/%s: %v", user, name, err)
		apiError(w, http.StatusInternalServerError, "create repo")
		return
	}
	desc := strings.TrimSpace(req.Description)
	commit, err := seedContractTemplate(s.Storage.RepoDir(user, name), user, desc)
	if err != nil {
		s.Log.Printf("seed template %s/%s: %v", user, name, err)
		// Best-effort cleanup: an empty, unseeded bare repo left behind would
		// otherwise 409 every retry (Storage.Exists would be true) with no way
		// to recover the name — soft-delete it so the slug is free again.
		if delErr := s.Storage.DeleteRepo(user, name); delErr != nil {
			s.Log.Printf("cleanup after failed seed %s/%s: %v", user, name, delErr)
		}
		apiError(w, http.StatusInternalServerError, "seed contract template")
		return
	}
	writeJSON(w, http.StatusCreated, apiRepoJSON{
		Owner: user, Name: name, Repo: name, Description: desc,
		Head: commit, Role: "owner", Public: s.isPublic(user, name),
	})
}

// seedContractTemplate lays down the embedded AgentsFS contract template (the
// same template/ tree `afs init` writes — see internal/core/initialize.go) as
// bare's first commit. It materializes the template into a temp dir — so the
// file list is never duplicated here, fs.WalkDir over afs.TemplateFS stays the
// single source of truth — and, when description is non-empty, injects it
// into the root INDEX.md in place of the REPLACE-ME placeholder before
// hashing anything in. Every file ships (including .gitattributes): unlike
// the local `afs init` CLI, this doesn't gate that on the Hub host machine
// happening to have a git-lfs binary, since the Hub's own LFS store (lfs.go)
// serves the protocol regardless.
//
// The commit is built the same way every other Hub write is — a throwaway
// index against the bare repo's own object store, no real work tree (see
// CommitFile in edit.go, apiCommit in apiagent_commit.go) — with the same
// author/committer split apiCommit uses: the caller authors it, the hub
// commits it, so `git blame` on a Hub-created repo stays truthful. bare must
// already exist (via Storage.EnsureRepo) and be empty (root commit); the
// update-ref uses the zeroOID CAS apiCommit uses for a first write, so this
// can never clobber a commit that raced in ahead of it.
func seedContractTemplate(bare, authorName, description string) (commit string, err error) {
	tmp, err := os.MkdirTemp("", "afs-hub-seed-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	sub, err := fs.Sub(afs.TemplateFS, "template")
	if err != nil {
		return "", err
	}
	var paths []string
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == "." {
				return nil
			}
			return os.MkdirAll(filepath.Join(tmp, filepath.FromSlash(p)), 0o755)
		}
		data, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		if p == "INDEX.md" && description != "" {
			data = injectRootDescription(data, description)
		}
		if err := os.WriteFile(filepath.Join(tmp, filepath.FromSlash(p)), data, 0o644); err != nil {
			return err
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("embedded template is empty")
	}

	// Build the tree via a throwaway index, same idiom as CommitFile/apiCommit —
	// no real work tree, just hash-object + update-index against bare's object
	// store.
	idx, err := os.CreateTemp("", "afs-hub-seed-idx-*")
	if err != nil {
		return "", err
	}
	idx.Close()
	os.Remove(idx.Name())
	defer os.Remove(idx.Name())
	env := append(os.Environ(), "GIT_INDEX_FILE="+idx.Name())

	for _, p := range paths {
		f, err := os.Open(filepath.Join(tmp, filepath.FromSlash(p)))
		if err != nil {
			return "", err
		}
		blob, hashErr := gitCmd("git", bare, env, f, "hash-object", "-w", "--stdin")
		f.Close()
		if hashErr != nil {
			return "", fmt.Errorf("hash %s: %w", p, hashErr)
		}
		blob = strings.TrimSpace(blob)
		if _, err := gitCmd("git", bare, env, nil, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+p); err != nil {
			return "", fmt.Errorf("stage %s: %w", p, err)
		}
	}

	tree, err := gitCmd("git", bare, env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	tree = strings.TrimSpace(tree)

	branchRef, err := gitCmd("git", bare, nil, nil, "symbolic-ref", "HEAD")
	if err != nil {
		return "", err
	}
	branchRef = strings.TrimSpace(branchRef)

	commitEnv := append(env,
		"GIT_AUTHOR_NAME="+authorName, "GIT_AUTHOR_EMAIL="+authorName+"@users.agentsfs",
		"GIT_COMMITTER_NAME=agentsfs hub", "GIT_COMMITTER_EMAIL=hub@agentsfs",
	)
	out, err := gitCmd("git", bare, commitEnv, strings.NewReader("Initialize agentsfs\n"), "commit-tree", tree, "-F", "-")
	if err != nil {
		return "", err
	}
	commit = strings.TrimSpace(out)

	// Root commit: expectedOld is the zero OID, which git reads as "the ref must
	// not yet exist" — the same CAS idiom apiCommit uses for a first write into
	// an empty repo, so a concurrent racer can't be silently clobbered.
	if _, err := gitCmd("git", bare, nil, nil, "update-ref", branchRef, commit, zeroOID); err != nil {
		return "", err
	}
	return commit, nil
}

// injectRootDescription replaces the template's root INDEX.md REPLACE-ME
// placeholder (core.RootDescriptionPlaceholder — the contract 0.7.0
// root-description field read by core.DirDescription / repoFilesMeta) with
// the caller-supplied description. Left untouched if the placeholder text
// isn't found verbatim, so a future template wording change can never
// silently corrupt the file — it just skips the injection instead.
func injectRootDescription(data []byte, description string) []byte {
	placeholder := []byte(`description: "` + core.RootDescriptionPlaceholder + `"`)
	if !bytes.Contains(data, placeholder) {
		return data
	}
	esc := strings.ReplaceAll(description, "\r\n", " ")
	esc = strings.ReplaceAll(esc, "\n", " ")
	esc = strings.ReplaceAll(esc, `"`, `'`)
	return bytes.Replace(data, placeholder, []byte(`description: "`+esc+`"`), 1)
}
