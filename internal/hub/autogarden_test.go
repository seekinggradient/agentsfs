package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentsfs.ai/afs/internal/core"
)

func newAutoGardenServer(t *testing.T) (*Server, *AccountStore) {
	t.Helper()
	store, err := NewLocalStorage(filepath.Join(t.TempDir(), "repos"))
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := OpenAccounts(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.CreateUser("alice", "alice@example.test", "password123"); err != nil {
		t.Fatal(err)
	}
	return &Server{Storage: store, Accounts: accounts, Log: log.New(io.Discard, "", 0)}, accounts
}

func seedAutoGardenRepo(t *testing.T, s *Server, repo string) {
	t.Helper()
	if err := s.Storage.EnsureRepo("alice", repo); err != nil {
		t.Fatal(err)
	}
	_, err := s.RepoCommit("alice", apiCommitRequest{
		Repo:    "alice/" + repo,
		BaseRev: "",
		Message: "seed",
		Changes: []apiChange{{
			Path:    "AGENTS.md",
			Content: "---\ndescription: Test knowledge base.\nagentsfs_contract: 0.9.0\n---\n",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutoGardenCandidatesDefaultOnWithPerRepoOptOut(t *testing.T) {
	s, accounts := newAutoGardenServer(t)
	seedAutoGardenRepo(t, s, "selected")
	seedAutoGardenRepo(t, s, "unselected")
	if got := accounts.AutoGardenSettings("alice"); !got.Enabled || !got.RecentOnly {
		t.Fatalf("default settings = %+v, want enabled + recent-only", got)
	}
	if err := s.setAutoGardenEnabled("alice", "unselected", false); err != nil {
		t.Fatal(err)
	}
	got := s.AutoGardenCandidates(time.Now())
	if len(got) != 1 || got[0].Owner != "alice" || got[0].Repo != "selected" || got[0].Head == "" || got[0].UpdatedAt == 0 {
		t.Fatalf("candidates = %+v, want selected repo only", got)
	}
}

func TestLegacyEmbeddedProjectionBlocksEveryHubOriginatedWriter(t *testing.T) {
	s, _ := newAutoGardenServer(t)
	seedAutoGardenRepo(t, s, "projected")
	bare := s.Storage.RepoDir("alice", "projected")
	if err := repoConfigSet(bare, "afs-hub.repository-mode", repoModeEmbeddedProjectionV1); err != nil {
		t.Fatal(err)
	}
	if got := s.AutoGardenCandidates(time.Now()); len(got) != 0 {
		t.Fatalf("legacy projection was offered to auto-gardener: %+v", got)
	}
	if s.canWrite("alice", "projected", "alice") {
		t.Fatal("legacy projection still exposes Hub edit affordances")
	}
	head := headOID("git", bare, defaultRef)
	_, err := s.RepoCommit("alice", apiCommitRequest{
		Repo: "alice/projected", BaseRev: head, Message: "must be blocked",
		Changes: []apiChange{{Path: "note.md", Content: "blocked\n"}},
	})
	var access *accessError
	if !errors.As(err, &access) || access.status != http.StatusConflict {
		t.Fatalf("RepoCommit error = %T %v, want 409 projection guard", err, err)
	}
}

func TestAutoGardenJobsMintScopedExpiringGrant(t *testing.T) {
	s, accounts := newAutoGardenServer(t)
	seedAutoGardenRepo(t, s, "brain")
	s.MaintenanceSecret = "maintenance-test-secret"

	req := httptest.NewRequest(http.MethodGet, autoGardenJobsPath, nil)
	req.Header.Set("Authorization", "Bearer maintenance-test-secret")
	w := httptest.NewRecorder()
	s.handleAutoGardenJobs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("jobs status = %d, body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Jobs []autoGardenJob `json:"jobs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Jobs) != 1 || body.Jobs[0].Repo != "brain" || body.Jobs[0].Grant == "" || body.Jobs[0].ThreadID == "" {
		t.Fatalf("jobs = %+v", body.Jobs)
	}
	grant, ok := accounts.AutoGardenGrantForToken(body.Jobs[0].Grant, time.Now())
	if !ok || grant.Username != "alice" || grant.Repo != "brain" || grant.ThreadID != body.Jobs[0].ThreadID {
		t.Fatalf("resolved grant = %+v, ok=%v", grant, ok)
	}
	if _, ok := accounts.AutoGardenGrantForToken(body.Jobs[0].Grant, time.Now().Add(autoGardenGrantTTL+time.Second)); ok {
		t.Fatal("expired grant still resolves")
	}
	if progress := s.autoGardenProgress("alice", "brain"); progress.State != "running" || progress.StateAt == 0 {
		t.Fatalf("progress = %+v, want running", progress)
	}

	// The capability sees its one selected repo, not the rest of the account.
	seedAutoGardenRepo(t, s, "private-other")
	reposReq := httptest.NewRequest(http.MethodGet, apiAgentPrefix+"repos", nil)
	reposReq.Header.Set("Authorization", "Bearer "+body.Jobs[0].Grant)
	reposW := httptest.NewRecorder()
	s.handleAPIAgent(reposW, reposReq)
	if reposW.Code != http.StatusOK {
		t.Fatalf("scoped repos status = %d, body=%s", reposW.Code, reposW.Body.String())
	}
	var reposBody struct {
		Repos []apiRepoJSON `json:"repos"`
	}
	if err := json.Unmarshal(reposW.Body.Bytes(), &reposBody); err != nil {
		t.Fatal(err)
	}
	if len(reposBody.Repos) != 1 || reposBody.Repos[0].Name != "brain" {
		t.Fatalf("scoped repos = %+v", reposBody.Repos)
	}

	otherReq := httptest.NewRequest(http.MethodGet, apiAgentPrefix+"repo/alice/private-other/resolve", nil)
	otherReq.Header.Set("Authorization", "Bearer "+body.Jobs[0].Grant)
	otherW := httptest.NewRecorder()
	s.handleAPIAgent(otherW, otherReq)
	if otherW.Code != http.StatusNotFound {
		t.Fatalf("cross-repo read status = %d, want 404", otherW.Code)
	}

	threadReq := httptest.NewRequest(http.MethodGet, apiAgentPrefix+"thread/not-the-job-thread", nil)
	threadReq.Header.Set("Authorization", "Bearer "+body.Jobs[0].Grant)
	threadW := httptest.NewRecorder()
	s.handleAPIAgent(threadW, threadReq)
	if threadW.Code != http.StatusNotFound {
		t.Fatalf("cross-thread read status = %d, want 404", threadW.Code)
	}

	upgradeReq := httptest.NewRequest(http.MethodPost, apiAgentPrefix+"repo/alice/brain/contract-upgrade", nil)
	upgradeReq.Header.Set("Authorization", "Bearer "+body.Jobs[0].Grant)
	upgradeW := httptest.NewRecorder()
	s.handleAPIAgent(upgradeW, upgradeReq)
	if upgradeW.Code != http.StatusOK || !strings.Contains(upgradeW.Body.String(), `"status":"customized"`) {
		t.Fatalf("customized contract upgrade = %d %s, want safe refusal", upgradeW.Code, upgradeW.Body.String())
	}

	deleteBody := strings.NewReader(`{"repo":"alice/brain","baseRev":"` + body.Jobs[0].Head + `","message":"delete","changes":[{"path":"note.md","delete":true}]}`)
	deleteReq := httptest.NewRequest(http.MethodPost, apiAgentPrefix+"commit", deleteBody)
	deleteReq.Header.Set("Authorization", "Bearer "+body.Jobs[0].Grant)
	deleteW := httptest.NewRecorder()
	s.handleAPIAgent(deleteW, deleteReq)
	if deleteW.Code != http.StatusForbidden {
		t.Fatalf("maintenance delete status = %d, want 403", deleteW.Code)
	}

	contractBody := strings.NewReader(`{"repo":"alice/brain","baseRev":"` + body.Jobs[0].Head + `","message":"rewrite contract","changes":[{"path":"AGENTS.md","content":"unsafe"}]}`)
	contractReq := httptest.NewRequest(http.MethodPost, apiAgentPrefix+"commit", contractBody)
	contractReq.Header.Set("Authorization", "Bearer "+body.Jobs[0].Grant)
	contractW := httptest.NewRecorder()
	s.handleAPIAgent(contractW, contractReq)
	if contractW.Code != http.StatusForbidden {
		t.Fatalf("model-driven contract rewrite status = %d, want 403", contractW.Code)
	}
}

func TestAutoGardenUpgradesRecognizedStockContract(t *testing.T) {
	s, _ := newAutoGardenServer(t)
	s.Log = log.New(testWriter{t}, "", 0)
	if err := s.Storage.EnsureRepo("alice", "legacy"); err != nil {
		t.Fatal(err)
	}
	stock, ok := core.StockContract("0.10.0")
	if !ok {
		t.Fatal("missing vendored 0.10.0 contract")
	}
	backlog, ok := core.StockBacklogPage("0.10.0")
	if !ok {
		t.Fatal("missing vendored 0.10.0 backlog")
	}
	seed, err := s.RepoCommit("alice", apiCommitRequest{
		Repo: "alice/legacy", Message: "seed legacy contract",
		Changes: []apiChange{{Path: "AGENTS.md", Content: stock}, {Path: "backlog.md", Content: backlog}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.MaintenanceSecret = "maintenance-test-secret"
	jobsReq := httptest.NewRequest(http.MethodGet, autoGardenJobsPath, nil)
	jobsReq.Header.Set("Authorization", "Bearer maintenance-test-secret")
	jobsW := httptest.NewRecorder()
	s.handleAutoGardenJobs(jobsW, jobsReq)
	var body struct {
		Jobs []autoGardenJob `json:"jobs"`
	}
	if err := json.Unmarshal(jobsW.Body.Bytes(), &body); err != nil || len(body.Jobs) != 1 {
		t.Fatalf("jobs = %s, err=%v", jobsW.Body.String(), err)
	}

	upgradeReq := httptest.NewRequest(http.MethodPost, apiAgentPrefix+"repo/alice/legacy/contract-upgrade", nil)
	upgradeReq.Header.Set("Authorization", "Bearer "+body.Jobs[0].Grant)
	upgradeW := httptest.NewRecorder()
	s.handleAPIAgent(upgradeW, upgradeReq)
	if upgradeW.Code != http.StatusOK || !strings.Contains(upgradeW.Body.String(), `"status":"upgraded"`) {
		t.Fatalf("stock upgrade = %d %s", upgradeW.Code, upgradeW.Body.String())
	}
	head := s.RepoResolve("alice", "legacy")
	if head == seed.NewRev {
		t.Fatal("stock upgrade did not advance HEAD")
	}
	agents, ok := BlobContent("git", s.Storage.RepoDir("alice", "legacy"), head, "AGENTS.md")
	if !ok || !strings.Contains(agents, "agentsfs_contract: "+core.CurrentContractVersion()) {
		t.Fatalf("upgraded AGENTS.md missing current contract: ok=%v", ok)
	}
	if _, ok := BlobContent("git", s.Storage.RepoDir("alice", "legacy"), head, "backlog/INDEX.md"); !ok {
		t.Fatal("stock upgrade did not create the current backlog companion")
	}
	if _, ok := BlobContent("git", s.Storage.RepoDir("alice", "legacy"), head, "backlog.md"); ok {
		t.Fatal("stock upgrade left the retired page-level backlog in place")
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func TestAutoGardenCandidatesApplySevenDayActivityGate(t *testing.T) {
	s, accounts := newAutoGardenServer(t)
	seedAutoGardenRepo(t, s, "brain")
	if err := s.setAutoGardenEnabled("alice", "brain", true); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SetAutoGardenSettings("alice", AutoGardenSettings{Enabled: true, RecentOnly: true}); err != nil {
		t.Fatal(err)
	}
	if got := s.AutoGardenCandidates(time.Now().Add(AutoGardenRecentWindow + time.Second)); len(got) != 0 {
		t.Fatalf("stale candidates = %+v, want none", got)
	}
	if err := accounts.SetAutoGardenSettings("alice", AutoGardenSettings{Enabled: true, RecentOnly: false}); err != nil {
		t.Fatal(err)
	}
	if got := s.AutoGardenCandidates(time.Now().Add(AutoGardenRecentWindow + time.Second)); len(got) != 1 || got[0].Repo != "brain" {
		t.Fatalf("unrestricted candidates = %+v, want brain", got)
	}
}

func TestAutoGardenActivityUsesRecordedPushTime(t *testing.T) {
	s, _ := newAutoGardenServer(t)
	seedAutoGardenRepo(t, s, "brain")
	now := time.Now()
	bare := s.Storage.RepoDir("alice", "brain")
	if err := repoConfigSet(bare, "afs-hub.last-push", strconv.FormatInt(now.Add(-AutoGardenRecentWindow-time.Minute).Unix(), 10)); err != nil {
		t.Fatal(err)
	}
	if got := s.AutoGardenCandidates(now); len(got) != 0 {
		t.Fatalf("old recorded push candidates = %+v, want none", got)
	}
	if err := repoConfigSet(bare, "afs-hub.last-push", strconv.FormatInt(now.Unix(), 10)); err != nil {
		t.Fatal(err)
	}
	if got := s.AutoGardenCandidates(now); len(got) != 1 || got[0].UpdatedAt != now.Unix() {
		t.Fatalf("fresh recorded push candidates = %+v, want brain at push time", got)
	}
}

func TestManualAutoGardenBypassesGlobalAndRecentFilters(t *testing.T) {
	s, accounts := newAutoGardenServer(t)
	seedAutoGardenRepo(t, s, "selected")
	seedAutoGardenRepo(t, s, "unchecked")
	if err := s.setAutoGardenEnabled("alice", "unchecked", false); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SetAutoGardenSettings("alice", AutoGardenSettings{Enabled: false, RecentOnly: true}); err != nil {
		t.Fatal(err)
	}
	bare := s.Storage.RepoDir("alice", "selected")
	if err := repoConfigSet(bare, "afs-hub.last-push", strconv.FormatInt(time.Now().Add(-30*24*time.Hour).Unix(), 10)); err != nil {
		t.Fatal(err)
	}
	if got := s.AutoGardenCandidates(time.Now()); len(got) != 0 {
		t.Fatalf("automatic candidates = %+v, want none", got)
	}
	got := s.ManualAutoGardenCandidates("alice", time.Now())
	if len(got) != 1 || got[0].Repo != "selected" {
		t.Fatalf("manual candidates = %+v, want selected only", got)
	}
}

func TestManualAutoGardenDispatchesToEve(t *testing.T) {
	var called bool
	eve := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost || r.URL.Path != "/agent/api/cron/auto-garden" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer maintenance-test-secret" {
			t.Errorf("authorization = %q", got)
		}
		var body autoGardenContinuation
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.User != "alice" || body.Cursor != 0 {
			t.Errorf("body = %+v, err=%v", body, err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer eve.Close()
	s, _ := newAutoGardenServer(t)
	s.MaintenanceSecret = "maintenance-test-secret"
	s.Agent = &AgentManager{EveURL: eve.URL}
	if err := s.dispatchManualAutoGarden("Alice"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Eve was not called")
	}
}

func TestAutoGardenContinuationRelaysThroughHub(t *testing.T) {
	var called bool
	eve := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost || r.URL.Path != "/agent/api/cron/auto-garden" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer maintenance-test-secret" {
			t.Errorf("authorization = %q", got)
		}
		var body autoGardenContinuation
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.User != "alice" || body.Cursor != 7 || body.Scheduled {
			t.Errorf("body = %+v", body)
		}
		if body.Result == nil || body.Result.Repo != "brain" || body.Result.Status != "completed" {
			t.Errorf("result = %+v", body.Result)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer eve.Close()
	s, _ := newAutoGardenServer(t)
	seedAutoGardenRepo(t, s, "brain")
	s.MaintenanceSecret = "maintenance-test-secret"
	s.Agent = &AgentManager{EveURL: eve.URL}
	finishedAt := time.Now().Add(-time.Minute).Unix()
	body := strings.NewReader(fmt.Sprintf(`{"user":"Alice","cursor":7,"result":{"owner":"alice","repo":"brain","status":"completed","finishedAt":%d}}`, finishedAt))
	req := httptest.NewRequest(http.MethodPost, autoGardenJobsPath, body)
	req.Header.Set("Authorization", "Bearer maintenance-test-secret")
	w := httptest.NewRecorder()
	s.handleAutoGardenJobs(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatal("Eve was not called")
	}
	progress := s.autoGardenProgress("alice", "brain")
	if progress.State != "idle" || progress.LastStatus != "completed" || progress.LastGardened != finishedAt {
		t.Fatalf("progress = %+v", progress)
	}
}
