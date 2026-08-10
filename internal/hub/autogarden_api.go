package hub

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

const autoGardenJobsPath = "/api/maintenance/v1/auto-garden"

type autoGardenJob struct {
	AutoGardenCandidate
	ThreadID string `json:"threadId"`
	// Grant is the user-approved short-lived capability for this exact repo and
	// maintenance transcript. It is never a reusable account credential.
	Grant string `json:"grant"`
}

func (s *Server) maintenanceAuthorized(r *http.Request) bool {
	secret := strings.TrimSpace(s.MaintenanceSecret)
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if secret == "" || got == "" || len(secret) != len(got) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(secret), []byte(got)) == 1
}

func autoGardenThreadID(candidate AutoGardenCandidate, now time.Time) string {
	key := candidate.Owner + "/" + candidate.Repo + "/" + now.UTC().Format("2006-01-02")
	sum := sha256.Sum256([]byte(key))
	return "garden-" + hex.EncodeToString(sum[:])[:24]
}

func (s *Server) handleAutoGardenJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.maintenanceAuthorized(r) {
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.Accounts == nil {
		apiError(w, http.StatusServiceUnavailable, "automatic gardening is not configured")
		return
	}
	now := time.Now()
	jobs := make([]autoGardenJob, 0)
	for _, candidate := range s.AutoGardenCandidates(now) {
		threadID := autoGardenThreadID(candidate, now)
		grant, _, err := s.Accounts.MintAutoGardenGrant(candidate.Owner, candidate.Repo, threadID, now)
		if err != nil {
			if s.Log != nil {
				s.Log.Printf("auto garden: mint %s/%s: %v", candidate.Owner, candidate.Repo, err)
			}
			continue
		}
		jobs = append(jobs, autoGardenJob{AutoGardenCandidate: candidate, ThreadID: threadID, Grant: grant})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Jobs []autoGardenJob `json:"jobs"`
	}{Jobs: jobs})
}
