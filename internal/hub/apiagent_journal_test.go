package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentsfs.ai/afs/internal/core"
)

func TestJournalAPIMaintenanceArchivesAtomicallyAndRejectsStalePlan(t *testing.T) {
	s, accounts := newAutoGardenServer(t)
	seedAutoGardenRepo(t, s, "brain")
	_, err := s.RepoCommit("alice", apiCommitRequest{Repo: "alice/brain", BaseRev: s.RepoResolve("alice", "brain"), Message: "journal", Changes: []apiChange{
		{Path: "log/INDEX.md", Content: "---\ndescription: Episodes.\nagentsfs_role: journal\n---\n"},
		{Path: "log/2025-01-01-source.md", Content: "---\ndescription: Original result.\n---\nEvidence that must survive.\n"},
		{Path: "log/active/running.md", Content: "---\ndescription: Running work.\nepisode_id: running\nstatus: running\nstarted: 2026-09-07T00:00:00Z\ncheckpointed: 2026-09-07T00:00:00Z\n---\nStill working.\n"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := accounts.MintAutoGardenGrant("alice", "brain", "journal-test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, apiAgentPrefix+"repo/alice/brain/journal", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.handleAPIAgent(w, r)
		return w
	}
	get := call(http.MethodGet, "")
	if get.Code != 200 {
		t.Fatal(get.Code, get.Body.String())
	}
	var prepared struct {
		Rev    string `json:"rev"`
		Result struct {
			Plan core.JournalPlan `json:"plan"`
		} `json:"result"`
	}
	if err = json.Unmarshal(get.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if len(prepared.Result.Plan.Episodes) != 1 {
		t.Fatalf("running work eligible: %+v", prepared)
	}
	bad, _ := json.Marshal(journalAPIRequest{Action: "begin", BaseRev: prepared.Rev, Session: "forbidden", Description: "Grant cannot start episodes"})
	if w := call(http.MethodPost, string(bad)); w.Code != 403 {
		t.Fatalf("grant began episode: %d %s", w.Code, w.Body.String())
	}
	bootstrap := "---\ndescription: Workspace history.\n---\n\n## Overview\nRecorded history.\n\n## Chronology\n2025: [[2025-01-01-source]] records the original result.\n\n## Key knowledge\nRead [[design]] before implementation.\n"
	body, _ := json.Marshal(journalAPIRequest{Action: "consolidate", BaseRev: prepared.Rev, Plan: prepared.Result.Plan, Bootstrap: bootstrap})
	post := call(http.MethodPost, string(body))
	if post.Code != 200 {
		t.Fatal(post.Code, post.Body.String())
	}
	head := s.RepoResolve("alice", "brain")
	for _, path := range []string{"log/bootstrap.md", "log/archive/2025-01-01-source.md", "log/active/running.md"} {
		if _, _, _, err := s.RepoReadFile("alice", "brain", head, path); err != nil {
			t.Fatal(path, err)
		}
	}
	if _, _, _, err := s.RepoReadFile("alice", "brain", head, "log/2025-01-01-source.md"); err == nil {
		t.Fatal("source not moved")
	}
	if w := call(http.MethodPost, string(body)); w.Code != 409 {
		t.Fatalf("stale plan accepted: %d %s", w.Code, w.Body.String())
	}
}
