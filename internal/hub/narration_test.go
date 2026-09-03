package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newNarrationHub(t *testing.T, upstreamURL string) (*httptest.Server, *Server, *AccountStore) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewLocalStorage(filepath.Join(dir, "repos"))
	if err != nil {
		t.Fatal(err)
	}
	acc, err := OpenAccounts(filepath.Join(dir, "acc.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(store, NewTokenStore(), "git-http-backend-placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.Accounts = acc
	srv.Agent = NewAgentManager("", "", "", "", acc, nil)
	srv.Agent.EveURL = upstreamURL
	srv.Agent.EveSecret = "narration-test-secret"
	srv.Agent.PATStore = NewAgentPATStore(filepath.Join(dir, "agent-pats.json"))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	srv.PublicBaseURL = ts.URL
	return ts, srv, acc
}

func TestNarrationResearchExchangesOAuthForEveIdentity(t *testing.T) {
	seen := make(chan eveProxiedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen <- eveProxiedRequest{
			path: r.URL.Path, cookie: r.Header.Get("Cookie"),
			afsUser: r.Header.Get("X-AFS-User"), afsSignature: r.Header.Get("X-AFS-Signature"),
			afsExpiry: r.Header.Get("X-AFS-Expiry"), afsPAT: r.Header.Get("X-AFS-PAT"),
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("browser Authorization header leaked to Eve")
		}
		if string(body) != `{"page":{"title":"Test"}}` {
			t.Errorf("body = %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"manifest":{"beats":[]}}`)
	}))
	t.Cleanup(upstream.Close)

	ts, _, acc := newNarrationHub(t, upstream.URL)
	mkAccount(t, acc, "alice")
	token := mkOAuthToken(t, acc, narratedPageClientID, "alice", "profile narration:run")
	req, _ := http.NewRequest(http.MethodPost, ts.URL+narrationResearchPath, strings.NewReader(`{"page":{"title":"Test"}}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d: %s", res.StatusCode, body)
	}
	got := <-seen
	if got.path != "/agent/api/narration/research" || got.afsUser != "alice" {
		t.Fatalf("upstream request = %+v", got)
	}
	if got.afsSignature == "" || got.afsExpiry == "" || got.afsPAT == "" {
		t.Fatalf("missing Eve identity handoff: %+v", got)
	}
	if user, ok := acc.UserForToken(got.afsPAT); !ok || user != "alice" {
		t.Fatalf("injected PAT resolves to %q, %v", user, ok)
	}
}

func TestNarrationResearchRequiresOAuthAndScope(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unauthorized request reached Eve")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	tsrv, _, acc := newNarrationHub(t, upstream.URL)
	mkAccount(t, acc, "alice")
	pat, err := acc.CreatePAT("alice", "test")
	if err != nil {
		t.Fatal(err)
	}
	narrow := mkOAuthToken(t, acc, narratedPageClientID, "alice", "profile")

	for _, tc := range []struct {
		name, token string
		want        int
	}{
		{"missing token", "", http.StatusUnauthorized},
		{"PAT", pat, http.StatusUnauthorized},
		{"missing scope", narrow, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, tsrv.URL+narrationResearchPath, strings.NewReader(`{}`))
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			res, err := tsrv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != tc.want {
				var body map[string]any
				json.NewDecoder(res.Body).Decode(&body)
				t.Fatalf("status = %d, want %d: %v", res.StatusCode, tc.want, body)
			}
		})
	}
}

func TestNarrationResearchCORSPreflight(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(upstream.Close)
	ts, _, _ := newNarrationHub(t, upstream.URL)
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+narrationResearchPath, nil)
	req.Header.Set("Origin", "https://narrated-page.akshay95014.chatgpt.site")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", res.StatusCode)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "https://narrated-page.akshay95014.chatgpt.site" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}
