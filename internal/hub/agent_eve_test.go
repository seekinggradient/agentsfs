package hub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// eveProxiedRequest captures what the upstream Eve app actually received, so the
// path-mapping, credential-stripping, and identity-handoff assertions can be
// made against ground truth rather than the proxy's intentions.
type eveProxiedRequest struct {
	path, query, cookie, acceptEncoding string
	afsUser, afsSignature, afsExpiry    string
	afsPAT                              string
}

func captureEveUpstream(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, <-chan eveProxiedRequest) {
	t.Helper()
	seen := make(chan eveProxiedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- eveProxiedRequest{
			path:           r.URL.Path,
			query:          r.URL.RawQuery,
			cookie:         r.Header.Get("Cookie"),
			acceptEncoding: r.Header.Get("Accept-Encoding"),
			afsUser:        r.Header.Get("X-AFS-User"),
			afsSignature:   r.Header.Get("X-AFS-Signature"),
			afsExpiry:      r.Header.Get("X-AFS-Expiry"),
			afsPAT:         r.Header.Get("X-AFS-PAT"),
		}
		handler(w, r)
	}))
	t.Cleanup(upstream.Close)
	return upstream, seen
}

func newEveManager(upstreamURL string) *AgentManager {
	m := NewAgentManager("", "", "", "", nil, nil)
	m.EveURL = upstreamURL
	m.EveSecret = "eve-hmac-secret"
	return m
}

// --- mode selection -------------------------------------------------------

func TestEveModeSelection(t *testing.T) {
	sprite := NewAgentManager("sprite-token", "openai-key", "", "", nil, nil)
	if sprite.EveMode() {
		t.Fatal("sprite manager (no HUB_EVE_AGENT_URL) reports Eve mode")
	}

	eve := newEveManager("https://eve.example")
	if !eve.EveMode() {
		t.Fatal("manager with HUB_EVE_AGENT_URL does not report Eve mode")
	}
	if !eve.Enabled() {
		t.Fatal("Eve mode should make the agent feature Enabled even with no sprite token")
	}

	var nilMgr *AgentManager
	if nilMgr.EveMode() {
		t.Fatal("nil manager must not report Eve mode")
	}
}

// --- path mapping matrix --------------------------------------------------

func TestEveProxyPathMappingMatrix(t *testing.T) {
	upstream, seen := captureEveUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})
	m := newEveManager(upstream.URL)

	cases := []struct {
		name      string
		method    string
		inPath    string
		inQuery   string
		wantPath  string
		wantQuery string
	}{
		{"eve session create (mapped to root: eve service is not basePath-mounted)", http.MethodPost, "/agent/eve/v1/session", "", "/eve/v1/session", ""},
		{"eve health (mapped to root)", http.MethodGet, "/agent/eve/v1/health", "", "/eve/v1/health", ""},
		{"eve stream with query preserved (mapped to root)", http.MethodGet, "/agent/eve/v1/session/abc/stream", "startIndex=3", "/eve/v1/session/abc/stream", "startIndex=3"},
		{"bare /agent/ normalized to /agent (Next 308s the slash; hardener drops Location)", http.MethodGet, "/agent/", "", "/agent", ""},
		{"framework asset under prefix", http.MethodGet, "/agent/_next/static/chunk.js", "", "/agent/_next/static/chunk.js", ""},
		{"workflow callback forwarded un-stripped", http.MethodPost, "/.well-known/workflow/v1/flow", "", "/.well-known/workflow/v1/flow", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.NewReader("")
			req := httptest.NewRequest(tc.method, tc.inPath, body)
			if tc.inQuery != "" {
				req.URL.RawQuery = tc.inQuery
			}
			rec := httptest.NewRecorder()
			m.EveProxy(rec, req, "alice")

			got := <-seen
			if got.path != tc.wantPath {
				t.Fatalf("upstream path = %q, want %q", got.path, tc.wantPath)
			}
			if got.query != tc.wantQuery {
				t.Fatalf("upstream query = %q, want %q", got.query, tc.wantQuery)
			}
		})
	}
}

// EveURL may carry a base path (a mount prefix on the upstream); it must be
// preserved ahead of the un-stripped request path.
func TestEveProxyPreservesUpstreamBasePath(t *testing.T) {
	upstream, seen := captureEveUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})
	m := newEveManager(upstream.URL + "/mounted")

	req := httptest.NewRequest(http.MethodPost, "/agent/eve/v1/session", strings.NewReader(""))
	m.EveProxy(httptest.NewRecorder(), req, "alice")

	if got := <-seen; got.path != "/mounted/eve/v1/session" {
		t.Fatalf("upstream path = %q, want /mounted/eve/v1/session", got.path)
	}
}

// --- identity handoff: injection + HMAC correctness -----------------------

func TestEveProxyInjectsSignedIdentity(t *testing.T) {
	upstream, seen := captureEveUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})
	m := newEveManager(upstream.URL)

	before := time.Now().Add(eveIdentityTTL).Unix()
	req := httptest.NewRequest(http.MethodPost, "/agent/eve/v1/session", strings.NewReader(""))
	req.Header.Set("Cookie", "afs_session=real-user-session")
	m.EveProxy(httptest.NewRecorder(), req, "alice")
	after := time.Now().Add(eveIdentityTTL).Unix()

	got := <-seen
	if got.cookie != "" {
		t.Fatalf("hub session cookie leaked upstream: %q", got.cookie)
	}
	if got.acceptEncoding != "identity" {
		t.Fatalf("upstream Accept-Encoding = %q, want identity", got.acceptEncoding)
	}
	if got.afsUser != "alice" {
		t.Fatalf("X-AFS-User = %q, want alice", got.afsUser)
	}
	expiry, err := strconv.ParseInt(got.afsExpiry, 10, 64)
	if err != nil {
		t.Fatalf("X-AFS-Expiry = %q, not an integer: %v", got.afsExpiry, err)
	}
	if expiry < before || expiry > after {
		t.Fatalf("X-AFS-Expiry = %d, want within [%d,%d] (~5 min out)", expiry, before, after)
	}

	// Independently recompute the HMAC the way the Eve app must, from the exact
	// documented construction: hex(HMAC-SHA256(secret, user + "|" + expiry)).
	mac := hmac.New(sha256.New, []byte("eve-hmac-secret"))
	mac.Write([]byte("alice|" + got.afsExpiry))
	want := hex.EncodeToString(mac.Sum(nil))
	if got.afsSignature != want {
		t.Fatalf("X-AFS-Signature = %q, want %q", got.afsSignature, want)
	}
	if !hmac.Equal([]byte(eveSignature("eve-hmac-secret", "alice", expiry)), []byte(got.afsSignature)) {
		t.Fatal("eveSignature helper disagrees with forwarded signature")
	}
}

// A crafted request must never be able to spoof the identity handoff: inbound
// X-AFS-* headers are stripped before the Hub injects its own.
func TestEveProxyStripsInboundIdentityHeaders(t *testing.T) {
	upstream, seen := captureEveUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})
	m := newEveManager(upstream.URL)

	req := httptest.NewRequest(http.MethodPost, "/agent/eve/v1/session", strings.NewReader(""))
	req.Header.Set("X-AFS-User", "attacker")
	req.Header.Set("X-AFS-Signature", "forged")
	req.Header.Set("X-AFS-Expiry", "9999999999")
	req.Header.Set("X-AFS-PAT", "afs_smuggled-token")
	m.EveProxy(httptest.NewRecorder(), req, "victim")

	got := <-seen
	if got.afsUser != "victim" {
		t.Fatalf("X-AFS-User = %q, want victim (spoofed value survived)", got.afsUser)
	}
	if got.afsExpiry == "9999999999" {
		t.Fatal("spoofed X-AFS-Expiry survived to the upstream")
	}
	// A smuggled X-AFS-PAT must never reach the upstream. This manager has no PAT
	// store configured, so nothing is re-injected — the header must be empty.
	if got.afsPAT != "" {
		t.Fatalf("inbound X-AFS-PAT survived to the upstream: %q", got.afsPAT)
	}
	expiry, _ := strconv.ParseInt(got.afsExpiry, 10, 64)
	if got.afsSignature != eveSignature("eve-hmac-secret", "victim", expiry) {
		t.Fatal("forwarded signature does not match a fresh Hub-signed handoff")
	}
}

// --- response hardening + NDJSON allowance --------------------------------

func TestEveProxyHardensResponseAndAllowsNDJSON(t *testing.T) {
	upstream, _ := captureEveUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		// A compromised or misbehaving upstream must not weaken the Hub origin.
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Add("Set-Cookie", "afs_session=attacker; Path=/")
		w.Header().Set("Clear-Site-Data", `"cookies"`)
		w.Header().Set("Location", "/account")
		w.Header().Set("Refresh", "0; url=/logout")
		w.Header().Set("X-Eve-Session-Id", "wrun_test123")
		io.WriteString(w, "{\"type\":\"message.delta\"}\n")
	})
	m := newEveManager(upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/agent/eve/v1/session/abc/stream", strings.NewReader(""))
	rec := httptest.NewRecorder()
	m.EveProxy(rec, req, "alice")

	res := rec.Result()
	defer res.Body.Close()

	// NDJSON is a first-class allowed response media type: it must survive verbatim.
	if got := res.Header.Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("Content-Type = %q, want application/x-ndjson preserved", got)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := res.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}
	if cookies := res.Header.Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("upstream Set-Cookie reached hub client: %q", cookies)
	}
	// The eve session cursor headers are protocol, not authority — must pass.
	if got := res.Header.Get("X-Eve-Session-Id"); got != "wrun_test123" {
		t.Fatalf("X-Eve-Session-Id = %q, want passthrough", got)
	}
	for _, h := range []string{
		"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials",
		"Clear-Site-Data", "Location", "Refresh",
	} {
		if got := res.Header.Get(h); got != "" {
			t.Errorf("untrusted %s header reached Hub client: %q", h, got)
		}
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "message.delta") {
		t.Fatalf("proxied NDJSON body altered: %q", body)
	}
}

// --- NDJSON streaming flush (slow chunked upstream) -----------------------

func TestEveProxyFlushesNDJSONBeforeUpstreamCompletes(t *testing.T) {
	first := "{\"type\":\"message.delta\",\"text\":\"first\"}\n"
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, first)
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "{\"type\":\"turn.completed\"}\n")
	}))
	defer upstream.Close()

	m := newEveManager(upstream.URL)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.EveProxy(w, r, "alice")
	}))
	defer hub.Close()

	client := hub.Client()
	client.Timeout = 2 * time.Second
	req, err := http.NewRequest(http.MethodGet, hub.URL+"/agent/eve/v1/session/abc/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	buf := make([]byte, len(first))
	if _, err := io.ReadFull(res.Body, buf); err != nil {
		t.Fatalf("first NDJSON frame was buffered until completion: %v", err)
	}
	if got := string(buf); got != first {
		t.Fatalf("first NDJSON frame = %q, want %q", got, first)
	}
	close(release)
	released = true
	rest, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rest), "turn.completed") {
		t.Fatalf("final NDJSON frame missing: %q", rest)
	}
}

// --- upstream unreachable -------------------------------------------------

func TestEveProxyUpstreamUnreachableReturns502(t *testing.T) {
	m := newEveManager("http://127.0.0.1:0") // nothing listening
	req := httptest.NewRequest(http.MethodGet, "/agent/eve/v1/health", nil)
	rec := httptest.NewRecorder()
	m.EveProxy(rec, req, "alice")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

// --- web-level routing: auth gate, forwarding, sprite regression ----------

func aliceSession(srv *Server) *http.Cookie {
	return &http.Cookie{
		Name:  sessionCookie,
		Value: makeSession(srv.sessionSecret(), "alice", time.Now().Add(time.Hour).Unix()),
	}
}

// In Eve mode, /agent/* and the workflow callback are still behind the same
// webUser session gate as the sprite path: no session -> needLogin.
func TestEveRouteRequiresSession(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	upstream, seen := captureEveUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})
	srv.Agent = newEveManager(upstream.URL)

	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	for _, path := range []string{"/agent/eve/v1/health", "/.well-known/workflow/v1/flow"} {
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusFound {
			t.Fatalf("%s without session = %d, want 302 to login", path, res.StatusCode)
		}
		if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
			t.Fatalf("%s redirected to %q, want /login", path, loc)
		}
	}

	select {
	case got := <-seen:
		t.Fatalf("unauthenticated request reached the Eve upstream: %+v", got)
	default:
	}
}

// A signed-in user's /agent/* and workflow-callback requests are forwarded to
// the Eve upstream with the prefix mapping and the identity handoff applied.
func TestEveRouteProxiesAuthenticatedRequest(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	upstream, seen := captureEveUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok"}`)
	})
	srv.Agent = newEveManager(upstream.URL)
	client := ts.Client()

	cases := []struct{ reqPath, wantUpstream string }{
		{"/agent/eve/v1/health", "/eve/v1/health"},
		{"/.well-known/workflow/v1/flow", "/.well-known/workflow/v1/flow"},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+tc.reqPath, nil)
		req.AddCookie(aliceSession(srv))
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d, want 200", tc.reqPath, res.StatusCode)
		}
		got := <-seen
		if got.path != tc.wantUpstream {
			t.Fatalf("%s -> upstream %q, want %q", tc.reqPath, got.path, tc.wantUpstream)
		}
		if got.afsUser != "alice" {
			t.Fatalf("%s -> X-AFS-User %q, want alice", tc.reqPath, got.afsUser)
		}
		if got.afsSignature == "" {
			t.Fatalf("%s -> missing X-AFS-Signature", tc.reqPath)
		}
	}
}

// Regression: with HUB_EVE_AGENT_URL unset the sprite/dev path is unchanged —
// the sprite route allow-list still governs /agent/*, and the Eve-only
// /eve/v1/* surface is NOT served. Uses DevURL as the flag-unset stand-in for a
// sprite upstream (same code path, no Sprites API needed).
func TestSpritePathUnchangedWhenEveFlagUnset(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	spriteSeen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case spriteSeen <- r.URL.Path:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	m := NewAgentManager("", "", "", "", nil, nil)
	m.DevURL = upstream.URL // sprite-contract upstream; EveURL stays empty
	srv.Agent = m
	if m.EveMode() {
		t.Fatal("EveMode true with HUB_EVE_AGENT_URL unset")
	}
	client := ts.Client()

	// A valid sprite route is still proxied by the sprite path.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/agent/api/config", nil)
	req.AddCookie(aliceSession(srv))
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/agent/api/config (sprite route) = %d, want 200", res.StatusCode)
	}
	if got := <-spriteSeen; got != "/api/config" {
		t.Fatalf("sprite upstream path = %q, want /api/config", got)
	}

	// The Eve-only surface is not routed when the flag is unset: the sprite
	// allow-list rejects /eve/v1/* as an unknown route (404), and it must never
	// reach the upstream.
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/agent/eve/v1/health", nil)
	req2.AddCookie(aliceSession(srv))
	res2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusNotFound {
		t.Fatalf("/agent/eve/v1/health with flag unset = %d, want 404", res2.StatusCode)
	}

	// And the workflow-callback route is not claimed at all when the flag is unset.
	res3, err := client.Do(mustReq(t, ts.URL+"/.well-known/workflow/v1/flow", srv))
	if err != nil {
		t.Fatal(err)
	}
	res3.Body.Close()
	if res3.StatusCode != http.StatusNotFound {
		t.Fatalf("/.well-known/workflow/* with flag unset = %d, want 404", res3.StatusCode)
	}
}

func mustReq(t *testing.T, url string, srv *Server) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(aliceSession(srv))
	return req
}
