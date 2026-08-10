package hub

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// AutoGardenRecentWindow is the default activity guard selected in account
// settings. It keeps a recurring worker focused on knowledge bases people are
// actively pushing to rather than waking up every archived repository forever.
const AutoGardenRecentWindow = 7 * 24 * time.Hour

// AutoGardenCandidate is the immutable unit a scheduler may hand to an agent.
// Head and UpdatedAt are sampled together before dispatch so the agent can pin
// its work and the Hub can later reject a stale write through normal CAS.
type AutoGardenCandidate struct {
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	Head      string `json:"head"`
	UpdatedAt int64  `json:"updatedAt"`
}

// AutoGardenCandidates returns exactly the repositories whose owner opted in,
// selected the individual repository, and (when selected in account settings)
// pushed within the last seven days. It never includes collaborator-only repos:
// maintenance is an owner decision and always commits with the owner's agent
// authority.
func (s *Server) AutoGardenCandidates(now time.Time) []AutoGardenCandidate {
	return s.autoGardenCandidates(now, "", false)
}

// ManualAutoGardenCandidates returns the selected repositories for one owner,
// bypassing the global automation switch and seven-day activity filter. A
// manual click is an explicit one-shot request; per-repository opt-outs still
// win so "Run now" means exactly the checked list in the settings pane.
func (s *Server) ManualAutoGardenCandidates(owner string, now time.Time) []AutoGardenCandidate {
	return s.autoGardenCandidates(now, strings.ToLower(strings.TrimSpace(owner)), true)
}

func (s *Server) autoGardenCandidates(now time.Time, onlyOwner string, manual bool) []AutoGardenCandidate {
	if s == nil || s.Accounts == nil || s.Storage == nil {
		return nil
	}
	var out []AutoGardenCandidate
	owners := s.Accounts.AutoGardenUsers()
	if manual {
		owners = []string{onlyOwner}
	}
	for _, owner := range owners {
		settings := s.Accounts.AutoGardenSettings(owner)
		if !manual && !settings.Enabled {
			continue
		}
		repos, err := s.Storage.ListRepos(owner)
		if err != nil {
			if s.Log != nil {
				s.Log.Printf("auto garden: list %s: %v", owner, err)
			}
			continue
		}
		for _, repo := range repos {
			if !s.autoGardenEnabled(owner, repo) {
				continue
			}
			bare := s.Storage.RepoDir(owner, repo)
			head := headOID("git", bare, defaultRef)
			if head == "" {
				continue // an empty repository has nothing to garden
			}
			updatedAt := repoPushUnixTime(bare, head)
			if !manual && settings.RecentOnly && (updatedAt == 0 || now.Sub(time.Unix(updatedAt, 0)) > AutoGardenRecentWindow) {
				continue
			}
			out = append(out, AutoGardenCandidate{Owner: owner, Repo: repo, Head: head, UpdatedAt: updatedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owner != out[j].Owner {
			return out[i].Owner < out[j].Owner
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// repoPushUnixTime prefers the receipt time recorded after a successful smart-
// HTTP ref update. Repositories that predate this feature fall back to their
// freshest HEAD commit until their next push, preserving a sensible rollout.
func repoPushUnixTime(bare, head string) int64 {
	if raw := repoConfigGet(bare, "afs-hub.last-push"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return commitUnixTime(bare, head)
}

func commitUnixTime(bare, rev string) int64 {
	out, err := gitCmd("git", bare, nil, nil, "show", "-s", "--format=%ct", rev)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
