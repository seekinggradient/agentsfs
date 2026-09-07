package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentsfs.ai/afs/internal/core"
)

type journalAPIRequest struct {
	Action      string           `json:"action"`
	BaseRev     string           `json:"baseRev"`
	Session     string           `json:"session"`
	Description string           `json:"description"`
	ID          string           `json:"id"`
	Expected    string           `json:"expected"`
	Body        string           `json:"body"`
	Status      string           `json:"status"`
	Plan        core.JournalPlan `json:"plan"`
	Bootstrap   string           `json:"bootstrap"`
}

// The journal endpoint is a narrow mutation capability: role-resolved episode
// writes or a validated consolidation, never arbitrary file deletion. Git CAS
// publishes the whole result atomically, including source archival.
func (s *Server) apiJournal(w http.ResponseWriter, r *http.Request, auth agentAPIAuth, owner, repo string) {
	head := s.RepoResolve(owner, repo)
	var req journalAPIRequest
	if r.Method == http.MethodPost {
		_, canWrite := s.apiRepoAccess(owner, repo, auth.user)
		if !canWrite {
			apiError(w, http.StatusForbidden, "journal is read-only")
			return
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
			apiError(w, 400, "bad journal request")
			return
		}
		if req.BaseRev != head {
			writeConflict(w, head, nil)
			return
		}
		if auth.grant != nil && req.Action != "consolidate" {
			apiError(w, 403, "maintenance grants may only consolidate journal episodes")
			return
		}
	} else if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		apiError(w, 405, "method not allowed")
		return
	}
	var result any
	var changes []apiChange
	err := s.withRepoCheckout(owner, repo, head, func(root string) error {
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("view") == "prime" {
				pack, err := core.Prime(root, 4000)
				if err != nil {
					return err
				}
				result = map[string]any{"prime": pack.Text}
				return nil
			}
			roles, err := core.ResolveReservedDirs(root)
			if err != nil {
				return err
			}
			_, statErr := os.Stat(filepath.Join(root, roles.Journal, "bootstrap.md"))
			missing := os.IsNotExist(statErr)
			plan, err := core.PrepareJournal(root)
			if err != nil {
				return err
			}
			episodes, err := core.JournalEpisodes(root)
			if err != nil {
				return err
			}
			bootstrap, err := os.ReadFile(filepath.Join(root, plan.Journal, "bootstrap.md"))
			if err != nil {
				return err
			}
			result = map[string]any{"plan": plan, "bootstrap": string(bootstrap), "episodes": episodes, "bootstrap_missing": missing}
			return nil
		}
		switch req.Action {
		case "begin":
			var e core.Episode
			var err error
			e, err = core.BeginEpisode(root, req.Session, req.Description)
			if err != nil {
				return err
			}
			result = e
		case "checkpoint", "finish":
			status := req.Status
			if status == "" {
				status = "running"
			}
			if req.Action == "finish" {
				status = "complete"
			}
			e, err := core.CheckpointEpisode(root, req.ID, req.Expected, req.Body, status)
			if err != nil {
				return err
			}
			result = e
		case "consolidate":
			// Ensure the same additive layout as prepare, for a legacy instance whose
			// preparation checkout did not publish its temporary layout.
			if _, err := core.EnsureJournalLayout(root); err != nil {
				return err
			}
			if err := core.ConsolidateJournal(root, req.Plan, []byte(req.Bootstrap)); err != nil {
				return err
			}
			result = map[string]any{"consolidated": len(req.Plan.Episodes)}
		default:
			return fmt.Errorf("unknown journal action")
		}
		out, err := exec.Command("git", "-C", root, "-c", "status.renames=false", "status", "--porcelain=v1", "-z", "--untracked-files=all").Output()
		if err != nil {
			return err
		}
		roles, err := core.ResolveReservedDirs(root)
		if err != nil {
			return err
		}
		for _, entry := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
			if len(entry) < 4 {
				continue
			}
			path := filepath.ToSlash(entry[3:])
			if strings.HasPrefix(path, ".agentsfs/") {
				continue
			}
			if !strings.HasPrefix(path, roles.Journal+"/") {
				return fmt.Errorf("journal operation changed an unrelated file")
			}
			if entry[0] == 'D' || entry[1] == 'D' {
				changes = append(changes, apiChange{Path: path, Delete: true})
				continue
			}
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				return err
			}
			changes = append(changes, apiChange{Path: path, Content: string(b)})
		}
		return nil
	})
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(changes) > 0 {
		if auth.grant != nil && !s.Accounts.UseAutoGardenGrant(auth.credential, time.Now()) {
			apiError(w, 403, "automatic gardening write limit reached")
			return
		}
		commit := apiCommitRequest{Repo: owner + "/" + repo, BaseRev: head, Message: "Journal: " + req.Action, Changes: changes}
		commit.Author.Name = "AgentsFS journal"
		commit.Author.Email = "journal@agentsfs.ai"
		res, err := s.RepoCommit(auth.user, commit)
		if err != nil {
			if ce, ok := err.(*conflictError); ok {
				writeConflict(w, ce.head, ce.paths)
			} else {
				writeAccessError(w, err)
			}
			return
		}
		head = res.NewRev
	}
	writeJSON(w, 200, map[string]any{"rev": head, "result": result})
}
