package hub

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// newEvePATManager builds an Eve-mode manager backed by a real AccountStore and a
// temp PAT store, so the full X-AFS-PAT mint / inject / persist path can run.
func newEvePATManager(t *testing.T, upstreamURL string, acc *AccountStore) *AgentManager {
	t.Helper()
	m := NewAgentManager("", "", "", "", acc, nil)
	m.EveURL = upstreamURL
	m.EveSecret = "eve-hmac-secret"
	m.PATStore = NewAgentPATStore(filepath.Join(t.TempDir(), ".agent-pats.json"))
	return m
}

// openTestAccounts opens a throwaway account store with the given users created.
func openTestAccounts(t *testing.T, users ...string) *AccountStore {
	t.Helper()
	acc, err := OpenAccounts(filepath.Join(t.TempDir(), "acc.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if _, err := acc.CreateUser(u, u+"@example.com", "pw12345678"); err != nil {
			t.Fatalf("create %s: %v", u, err)
		}
	}
	return acc
}

// Eve mode injects a long-lived per-user agent PAT as X-AFS-PAT: it is minted
// under the "agent-user" label, resolves to the viewer, and the SAME token is
// reused across requests (so durable, hours-later tool calls keep working).
func TestEveProxyInjectsPerUserPAT(t *testing.T) {
	acc := openTestAccounts(t, "alice")
	upstream, seen := captureEveUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})
	m := newEvePATManager(t, upstream.URL, acc)

	req := httptest.NewRequest(http.MethodPost, "/agent/eve/v1/session", strings.NewReader(""))
	req.Header.Set("X-AFS-PAT", "afs_smuggled") // must be stripped, then replaced
	m.EveProxy(httptest.NewRecorder(), req, "alice")

	got := <-seen
	if got.afsPAT == "" {
		t.Fatal("no X-AFS-PAT injected for authenticated viewer")
	}
	if got.afsPAT == "afs_smuggled" {
		t.Fatal("smuggled inbound X-AFS-PAT survived instead of being replaced")
	}
	if u, ok := acc.UserForToken(got.afsPAT); !ok || u != "alice" {
		t.Fatalf("injected PAT resolves to (%q,%v), want alice,true", u, ok)
	}
	// It was minted under the shared "agent-user" label.
	pats, _ := acc.ListPATs("alice")
	var labeled int
	for _, p := range pats {
		if p.Name == agentUserPATName {
			labeled++
		}
	}
	if labeled != 1 {
		t.Fatalf("account has %d %q PATs, want exactly 1", labeled, agentUserPATName)
	}

	// A second request reuses the SAME persisted token (no churn, no new PAT).
	req2 := httptest.NewRequest(http.MethodGet, "/agent/eve/v1/health", nil)
	m.EveProxy(httptest.NewRecorder(), req2, "alice")
	got2 := <-seen
	if got2.afsPAT != got.afsPAT {
		t.Fatalf("second request PAT = %q, want reuse of %q", got2.afsPAT, got.afsPAT)
	}
	pats2, _ := acc.ListPATs("alice")
	if len(pats2) != len(pats) {
		t.Fatalf("second request minted another PAT: %d -> %d", len(pats), len(pats2))
	}
}

// Two different viewers get two different PATs, each resolving to its own user —
// the whole point of the multi-tenant change.
func TestEveProxyPerUserPATsAreDistinct(t *testing.T) {
	acc := openTestAccounts(t, "alice", "bob")
	upstream, seen := captureEveUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})
	m := newEvePATManager(t, upstream.URL, acc)

	m.EveProxy(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/agent/eve/v1/health", nil), "alice")
	aliceGot := <-seen
	m.EveProxy(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/agent/eve/v1/health", nil), "bob")
	bobGot := <-seen

	if aliceGot.afsPAT == "" || bobGot.afsPAT == "" || aliceGot.afsPAT == bobGot.afsPAT {
		t.Fatalf("expected distinct non-empty PATs, got alice=%q bob=%q", aliceGot.afsPAT, bobGot.afsPAT)
	}
	if u, _ := acc.UserForToken(bobGot.afsPAT); u != "bob" {
		t.Fatalf("bob's injected PAT resolved to %q, want bob", u)
	}
}

// Deliverable 2, end-to-end: the PAT the proxy injects authenticates the
// hosted-parity agent API EXACTLY like any user PAT (the UserForToken path), so
// the Eve app can use it verbatim for every hub-client call.
func TestEveInjectedPATAuthenticatesAgentAPI(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	seedCommit(t, ts, srv, aliceTok, "alice", "brain", "", map[string]string{"NOTE.md": "hi\n"}, nil)

	// Run the Eve injection path to obtain the token the upstream would receive.
	upstream, seen := captureEveUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})
	m := newEvePATManager(t, upstream.URL, acc)
	m.EveProxy(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/agent/eve/v1/health", nil), "alice")
	injected := (<-seen).afsPAT
	if injected == "" {
		t.Fatal("no PAT injected")
	}
	if injected == aliceTok {
		t.Fatal("injected agent PAT should be a distinct token from the manually minted one")
	}

	// Use it as a Bearer against the real agent API: it must authenticate alice
	// and list her repo with owner role.
	var repos struct {
		User  string        `json:"user"`
		Repos []apiRepoJSON `json:"repos"`
	}
	code := apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repos", injected, "", &repos)
	if code != http.StatusOK {
		t.Fatalf("/repos with injected PAT = %d, want 200", code)
	}
	if repos.User != "alice" {
		t.Fatalf("injected PAT authenticated as %q, want alice", repos.User)
	}
	var sawBrain bool
	for _, r := range repos.Repos {
		if r.Owner == "alice" && r.Name == "brain" && r.Role == "owner" {
			sawBrain = true
		}
	}
	if !sawBrain {
		t.Fatalf("injected PAT did not see alice/brain as owner: %+v", repos.Repos)
	}
}
