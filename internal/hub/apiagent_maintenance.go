package hub

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentsfs.ai/afs/internal/core"
)

// withRepoCheckout materializes an immutable repository revision in a private
// temporary checkout. Maintenance analysis never runs against the bare repo,
// and the checkout disappears before the request returns.
func (s *Server) withRepoCheckout(owner, repo, rev string, fn func(string) error) error {
	dir, err := os.MkdirTemp("", "agentsfs-maintenance-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command("git", "clone", "--quiet", "--no-checkout", s.Storage.RepoDir(owner, repo), dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return accessErr(http.StatusInternalServerError, "materialize repository: "+strings.TrimSpace(string(out)))
	}
	cmd = exec.Command("git", "-C", dir, "checkout", "--quiet", "--detach", rev)
	if out, err := cmd.CombinedOutput(); err != nil {
		return accessErr(http.StatusInternalServerError, "materialize revision: "+strings.TrimSpace(string(out)))
	}
	return fn(dir)
}

func (s *Server) apiMaintenanceDoctor(w http.ResponseWriter, owner, repo string) {
	head := s.RepoResolve(owner, repo)
	if head == "" {
		apiError(w, http.StatusNotFound, "no such revision")
		return
	}
	var findings []core.Finding
	err := s.withRepoCheckout(owner, repo, head, func(root string) error {
		var err error
		findings, err = core.Doctor(root)
		return err
	})
	if err != nil {
		if s.Log != nil {
			s.Log.Printf("maintenance doctor %s/%s: %v", owner, repo, err)
		}
		writeAccessError(w, err)
		return
	}
	if findings == nil {
		findings = []core.Finding{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rev": head, "findings": findings})
}

// apiMaintenanceContractUpgrade applies only a stock, recognized contract
// upgrade. It refuses customized, unknown, and newer contracts rather than
// letting an unattended model decide how to merge governance instructions.
func (s *Server) apiMaintenanceContractUpgrade(w http.ResponseWriter, r *http.Request, auth agentAPIAuth, owner, repo string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if auth.grant == nil {
		apiError(w, http.StatusForbidden, "automatic contract upgrades require a maintenance grant")
		return
	}
	head := s.RepoResolve(owner, repo)
	var changes []apiChange
	status := "current"
	from, to := "", core.CurrentContractVersion()
	err := s.withRepoCheckout(owner, repo, head, func(root string) error {
		from = core.ContractVersion(root)
		if core.CompareContractVersions(from, to) > 0 {
			status = "newer"
			return nil
		}
		if from == to {
			return nil
		}
		customized, known := core.ContractCustomized(root)
		if !known {
			status = "unknown"
			return nil
		}
		if customized {
			status = "customized"
			return nil
		}
		if _, err := core.UpgradeContract(root); err != nil {
			return err
		}
		out, err := exec.Command("git", "-C", root, "-c", "status.renames=false", "status", "--porcelain=v1", "-z", "--untracked-files=all").Output()
		if err != nil {
			return err
		}
		for _, entry := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
			if len(entry) < 4 {
				continue
			}
			path := filepath.ToSlash(entry[3:])
			if entry[0] == 'D' || entry[1] == 'D' {
				// This is reachable only for a deterministic core contract
				// migration (for example backlog.md → backlog/INDEX.md). The
				// model-facing gardening tools still expose no deletion.
				changes = append(changes, apiChange{Path: path, Delete: true})
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				return err
			}
			changes = append(changes, apiChange{Path: path, Content: string(data)})
		}
		status = "upgraded"
		return nil
	})
	if err != nil {
		if s.Log != nil {
			s.Log.Printf("maintenance contract upgrade %s/%s: %v", owner, repo, err)
		}
		writeAccessError(w, err)
		return
	}
	if status != "upgraded" || len(changes) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"status": status, "from": from, "to": to, "rev": head})
		return
	}
	if !s.Accounts.UseAutoGardenGrant(auth.credential, time.Now()) {
		apiError(w, http.StatusForbidden, "automatic gardening write limit reached")
		return
	}
	req := apiCommitRequest{Repo: owner + "/" + repo, BaseRev: head, Message: "Automatic gardening: upgrade AgentsFS contract"}
	req.Author.Name = "AgentsFS gardener"
	req.Author.Email = "gardener@agentsfs.ai"
	req.Changes = changes
	res, err := s.RepoCommit(auth.user, req)
	if err != nil {
		if ce, ok := err.(*conflictError); ok {
			writeConflict(w, ce.head, ce.paths)
			return
		}
		writeAccessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "from": from, "to": to, "rev": res.NewRev})
}
