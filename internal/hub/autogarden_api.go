package hub

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
	if !s.maintenanceAuthorized(r) {
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method == http.MethodPost {
		s.handleAutoGardenContinuation(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.Accounts == nil {
		apiError(w, http.StatusServiceUnavailable, "automatic gardening is not configured")
		return
	}
	now := time.Now()
	candidates := s.AutoGardenCandidates(now)
	if r.URL.Query().Get("manual") == "1" {
		owner := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("user")))
		if owner == "" || !nameRe.MatchString(owner) {
			apiError(w, http.StatusBadRequest, "bad user")
			return
		}
		candidates = s.ManualAutoGardenCandidates(owner, now)
	}
	cursor := 0
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			apiError(w, http.StatusBadRequest, "bad cursor")
			return
		}
		cursor = parsed
	}
	if cursor == 0 {
		for _, candidate := range candidates {
			if err := s.setAutoGardenRunState(candidate.Owner, candidate.Repo, "queued", now); err != nil && s.Log != nil {
				s.Log.Printf("auto garden: queue %s/%s: %v", candidate.Owner, candidate.Repo, err)
			}
		}
	}
	if cursor < len(candidates) {
		candidate := candidates[cursor]
		if err := s.setAutoGardenRunState(candidate.Owner, candidate.Repo, "running", now); err != nil && s.Log != nil {
			s.Log.Printf("auto garden: start %s/%s: %v", candidate.Owner, candidate.Repo, err)
		}
	}
	jobs := make([]autoGardenJob, 0)
	for _, candidate := range candidates {
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

type autoGardenContinuation struct {
	User      string            `json:"user,omitempty"`
	Scheduled bool              `json:"scheduled,omitempty"`
	Cursor    int               `json:"cursor"`
	Result    *autoGardenResult `json:"result,omitempty"`
}

type autoGardenResult struct {
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	Status     string `json:"status"`
	FinishedAt int64  `json:"finishedAt"`
}

// handleAutoGardenContinuation relays Eve's next cursor back to Eve through
// Hub. The extra trusted hop resets Vercel's recursion depth while preserving
// the shared-secret boundary and one-repository-at-a-time execution.
func (s *Server) handleAutoGardenContinuation(w http.ResponseWriter, r *http.Request) {
	var next autoGardenContinuation
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&next); err != nil {
		apiError(w, http.StatusBadRequest, "bad json")
		return
	}
	next.User = strings.ToLower(strings.TrimSpace(next.User))
	if next.Cursor < 0 || (!next.Scheduled && (next.User == "" || !nameRe.MatchString(next.User))) {
		apiError(w, http.StatusBadRequest, "bad continuation")
		return
	}
	if next.Result != nil {
		result := next.Result
		result.Owner = strings.ToLower(strings.TrimSpace(result.Owner))
		result.Repo = strings.ToLower(strings.TrimSpace(result.Repo))
		if !nameRe.MatchString(result.Owner) || !validSlug(result.Repo) ||
			(result.Status != "completed" && result.Status != "skipped" && result.Status != "failed") ||
			(!next.Scheduled && result.Owner != next.User) {
			apiError(w, http.StatusBadRequest, "bad result")
			return
		}
		finishedAt := time.Now()
		if result.FinishedAt > 0 && result.FinishedAt <= finishedAt.Add(time.Minute).Unix() {
			finishedAt = time.Unix(result.FinishedAt, 0)
		}
		if err := s.recordAutoGardenResult(result.Owner, result.Repo, result.Status, finishedAt); err != nil {
			if s.Log != nil {
				s.Log.Printf("auto garden result %s/%s: %v", result.Owner, result.Repo, err)
			}
			apiError(w, http.StatusBadRequest, "bad result repository")
			return
		}
	}
	if err := s.dispatchAutoGarden(next); err != nil {
		if s.Log != nil {
			s.Log.Printf("auto garden continuation: %v", err)
		}
		apiError(w, http.StatusBadGateway, "Eve continuation failed")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "cursor": next.Cursor})
}

// dispatchManualAutoGarden asks Eve to start one immediate pass for the user's
// checked repositories. The shared maintenance secret authenticates both hops;
// no browser credential or account PAT leaves Hub.
func (s *Server) dispatchManualAutoGarden(user string) error {
	return s.dispatchAutoGarden(autoGardenContinuation{User: strings.ToLower(strings.TrimSpace(user))})
}

func (s *Server) dispatchAutoGarden(payload autoGardenContinuation) error {
	if s.Agent == nil || strings.TrimSpace(s.Agent.EveURL) == "" {
		return fmt.Errorf("hosted Eve is not configured")
	}
	secret := strings.TrimSpace(s.MaintenanceSecret)
	if secret == "" {
		return fmt.Errorf("automatic gardening is not configured")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint, err := url.JoinPath(strings.TrimRight(s.Agent.EveURL, "/"), "agent/api/cron/auto-garden")
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		return fmt.Errorf("Eve returned %s: %s", res.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}
