package hub

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newOAuthHub builds an AccountStore-backed hub wrapped in an httptest server,
// with PublicBaseURL pinned to the test server's own URL so the advertised
// issuer/endpoints and the token audience all line up with where requests land.
// It does not need git (the OAuth AS touches none of the git machinery).
func newOAuthHub(t *testing.T) (*httptest.Server, *Server, *AccountStore) {
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
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	srv.PublicBaseURL = ts.URL
	return ts, srv, acc
}

// noRedirect is an http client that captures redirects instead of following
// them, so a test can read the Location a flow bounces to.
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// pkcePair returns a random verifier and its S256 challenge.
func pkcePair() (verifier, challenge string) {
	verifier = "verifier-" + base64.RawURLEncoding.EncodeToString([]byte(time.Now().String()))[:43]
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// mkAccount creates a password account (for session-cookie flows).
func mkAccount(t *testing.T, acc *AccountStore, name string) {
	t.Helper()
	if _, err := acc.CreateUser(name, name+"@example.com", "pw12345678"); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
}

// registerDCR registers a public client and returns its client_id.
func registerDCR(t *testing.T, ts *httptest.Server, name string, redirectURIs []string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"redirect_uris": redirectURIs, "client_name": name})
	res, err := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("register: status %d: %s", res.StatusCode, b)
	}
	var out struct {
		ClientID     string   `json:"client_id"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.ClientID, "cli_") {
		t.Fatalf("client_id should be cli_-prefixed, got %q", out.ClientID)
	}
	if out.AuthMethod != "none" {
		t.Fatalf("token_endpoint_auth_method = %q, want none", out.AuthMethod)
	}
	return out.ClientID
}

// authorizeQuery builds an /oauth/authorize query string.
func authorizeQuery(p map[string]string) string {
	q := url.Values{}
	for k, v := range p {
		if v != "" {
			q.Set(k, v)
		}
	}
	return q.Encode()
}

// approve drives a GET authorize (asserting the consent page renders) then POSTs
// an approval, returning the redirect Location. allowWrite controls the write
// checkbox.
func approve(t *testing.T, ts *httptest.Server, srv *Server, user string, p map[string]string, allowWrite bool) string {
	t.Helper()
	cli := noRedirect()
	// GET the consent screen.
	req, _ := http.NewRequest("GET", ts.URL+"/oauth/authorize?"+authorizeQuery(p), nil)
	req.AddCookie(sessionCookieFor(srv, user))
	res, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("authorize GET: status %d: %s", res.StatusCode, body)
	}
	// POST the approval with the session-derived CSRF token.
	form := url.Values{}
	for k, v := range p {
		form.Set(k, v)
	}
	form.Set("csrf", oauthCSRFToken(srv.sessionSecret(), user))
	form.Set("decision", "approve")
	if allowWrite {
		form.Set("allow_write", "1")
	}
	req2, _ := http.NewRequest("POST", ts.URL+"/oauth/authorize", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(sessionCookieFor(srv, user))
	res2, err := cli.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusFound {
		t.Fatalf("authorize approve: status %d, want 302", res2.StatusCode)
	}
	return res2.Header.Get("Location")
}

// postToken issues a form-urlencoded token request and returns status + parsed body.
func postToken(t *testing.T, ts *httptest.Server, form url.Values) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(ts.URL+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("token Cache-Control = %q, want no-store", cc)
	}
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func codeFromLocation(t *testing.T, loc string) string {
	t.Helper()
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect %q", loc)
	}
	return code
}

// --- metadata --------------------------------------------------------------

func TestOAuthWellKnownMetadata(t *testing.T) {
	ts, _, _ := newOAuthHub(t)

	// Authorization-server metadata + the OIDC alias must be byte-identical.
	var asBody []byte
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, res.StatusCode)
		}
		if asBody == nil {
			asBody = b
		} else if string(asBody) != string(b) {
			t.Fatalf("openid-configuration must match oauth-authorization-server")
		}
	}
	var meta map[string]any
	json.Unmarshal(asBody, &meta)
	if meta["issuer"] != ts.URL {
		t.Errorf("issuer = %v, want %v", meta["issuer"], ts.URL)
	}
	if meta["authorization_endpoint"] != ts.URL+"/oauth/authorize" {
		t.Errorf("authorization_endpoint = %v", meta["authorization_endpoint"])
	}
	if meta["token_endpoint"] != ts.URL+"/oauth/token" {
		t.Errorf("token_endpoint = %v", meta["token_endpoint"])
	}
	if meta["registration_endpoint"] != ts.URL+"/oauth/register" {
		t.Errorf("registration_endpoint = %v", meta["registration_endpoint"])
	}
	if meta["client_id_metadata_document_supported"] != true {
		t.Errorf("client_id_metadata_document_supported = %v, want true", meta["client_id_metadata_document_supported"])
	}
	if !hasJSONString(meta["code_challenge_methods_supported"], "S256") {
		t.Errorf("code_challenge_methods_supported missing S256: %v", meta["code_challenge_methods_supported"])
	}
	if !hasJSONString(meta["token_endpoint_auth_methods_supported"], "none") {
		t.Errorf("token_endpoint_auth_methods_supported missing none: %v", meta["token_endpoint_auth_methods_supported"])
	}
	if !hasJSONString(meta["grant_types_supported"], "authorization_code") || !hasJSONString(meta["grant_types_supported"], "refresh_token") {
		t.Errorf("grant_types_supported = %v", meta["grant_types_supported"])
	}

	// Protected-resource metadata at both the bare and /mcp path forms.
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var prm map[string]any
		json.NewDecoder(res.Body).Decode(&prm)
		res.Body.Close()
		if prm["resource"] != ts.URL+"/mcp" {
			t.Errorf("%s resource = %v, want %v/mcp", path, prm["resource"], ts.URL)
		}
		if !hasJSONString(prm["authorization_servers"], ts.URL) {
			t.Errorf("%s authorization_servers = %v", path, prm["authorization_servers"])
		}
		if !hasJSONString(prm["bearer_methods_supported"], "header") {
			t.Errorf("%s bearer_methods_supported = %v", path, prm["bearer_methods_supported"])
		}
	}
}

func hasJSONString(v any, want string) bool {
	arr, ok := v.([]any)
	if !ok {
		return false
	}
	for _, e := range arr {
		if s, ok := e.(string); ok && s == want {
			return true
		}
	}
	return false
}

// --- registration ----------------------------------------------------------

func TestOAuthRegister(t *testing.T) {
	ts, _, _ := newOAuthHub(t)

	// Happy path.
	id := registerDCR(t, ts, "Claude", []string{"https://claude.ai/api/mcp/auth_callback", "http://127.0.0.1/callback"})
	if id == "" {
		t.Fatal("expected a client_id")
	}

	tooMany := make([]string, 33)
	for i := range tooMany {
		tooMany[i] = "https://a.example/cb"
	}
	cases := []struct {
		name string
		body string
	}{
		{"empty redirect_uris", `{"redirect_uris":[],"client_name":"x"}`},
		{"non-loopback http", `{"redirect_uris":["http://evil.example/cb"]}`},
		{"bad scheme", `{"redirect_uris":["ftp://x/cb"]}`},
		{"not json", `not json at all`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(c.body))
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", res.StatusCode)
			}
		})
	}
	// Too many redirect URIs.
	body, _ := json.Marshal(map[string]any{"redirect_uris": tooMany})
	res, _ := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(string(body)))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf(">32 uris: status %d, want 400", res.StatusCode)
	}
	res.Body.Close()

	// Oversized body.
	big := strings.Repeat("a", 70<<10)
	res2, _ := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["https://x/`+big+`"]}`))
	if res2.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized: status %d, want 400", res2.StatusCode)
	}
	res2.Body.Close()
}

// --- full flow: authorize -> code -> token -> refresh -> rotate-reuse ------

func TestOAuthFullFlow(t *testing.T) {
	ts, srv, acc := newOAuthHub(t)
	mkAccount(t, acc, "alice")
	redirect := "https://app.example/cb"
	clientID := registerDCR(t, ts, "Test App", []string{redirect})
	verifier, challenge := pkcePair()

	p := map[string]string{
		"client_id":             clientID,
		"redirect_uri":          redirect,
		"response_type":         "code",
		"scope":                 "afs:read afs:write",
		"state":                 "st-123",
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"resource":              ts.URL + "/mcp",
	}
	loc := approve(t, ts, srv, "alice", p, true)
	u, _ := url.Parse(loc)
	if u.Query().Get("state") != "st-123" {
		t.Errorf("state not echoed: %q", loc)
	}
	code := codeFromLocation(t, loc)

	// Exchange the code.
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"redirect_uri":  {redirect},
		"code_verifier": {verifier},
	}
	status, tok := postToken(t, ts, form)
	if status != http.StatusOK {
		t.Fatalf("token exchange: status %d: %v", status, tok)
	}
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if !strings.HasPrefix(access, "afsmcp_") || !strings.HasPrefix(refresh, "afsmcpr_") {
		t.Fatalf("token prefixes wrong: access=%q refresh=%q", access, refresh)
	}
	if tok["token_type"] != "Bearer" {
		t.Errorf("token_type = %v, want Bearer", tok["token_type"])
	}
	if tok["expires_in"].(float64) != 7200 {
		t.Errorf("expires_in = %v, want 7200", tok["expires_in"])
	}
	if tok["scope"] != "afs:read afs:write" {
		t.Errorf("scope = %v", tok["scope"])
	}

	// The access token resolves through the MCP seam.
	user, scopes, ok := srv.VerifyMCPBearer(access)
	if !ok || user != "alice" || strings.Join(scopes, " ") != "afs:read afs:write" {
		t.Fatalf("VerifyMCPBearer(access) = %q %v %v", user, scopes, ok)
	}

	// The code is single-use: a replay fails invalid_grant.
	status2, tok2 := postToken(t, ts, form)
	if status2 != http.StatusBadRequest || tok2["error"] != "invalid_grant" {
		t.Fatalf("code replay: status %d err %v, want 400 invalid_grant", status2, tok2["error"])
	}

	// Rotate the refresh token.
	rform := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}
	status3, tok3 := postToken(t, ts, rform)
	if status3 != http.StatusOK {
		t.Fatalf("refresh: status %d: %v", status3, tok3)
	}
	access2, _ := tok3["access_token"].(string)
	refresh2, _ := tok3["refresh_token"].(string)
	if refresh2 == refresh || access2 == access {
		t.Fatal("rotation must produce fresh tokens")
	}
	if _, _, ok := srv.VerifyMCPBearer(access2); !ok {
		t.Fatal("rotated access token should verify")
	}

	// Reuse of the CONSUMED first refresh token revokes the whole family.
	status4, tok4 := postToken(t, ts, rform)
	if status4 != http.StatusBadRequest || tok4["error"] != "invalid_grant" {
		t.Fatalf("refresh reuse: status %d err %v, want 400 invalid_grant", status4, tok4["error"])
	}
	if _, _, ok := srv.VerifyMCPBearer(access2); ok {
		t.Fatal("family revocation should have killed the rotated access token")
	}
	// The second refresh (same family) is now dead too.
	status5, tok5 := postToken(t, ts, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh2}})
	if status5 != http.StatusBadRequest || tok5["error"] != "invalid_grant" {
		t.Fatalf("post-revocation refresh: status %d err %v", status5, tok5["error"])
	}
}

// --- PKCE, missing challenge, redirect + loopback, resource, form enc ------

func TestOAuthPKCEWrongVerifier(t *testing.T) {
	ts, srv, acc := newOAuthHub(t)
	mkAccount(t, acc, "alice")
	redirect := "https://app.example/cb"
	clientID := registerDCR(t, ts, "App", []string{redirect})
	_, challenge := pkcePair()
	p := map[string]string{
		"client_id": clientID, "redirect_uri": redirect, "response_type": "code",
		"scope": "afs:read", "code_challenge": challenge, "code_challenge_method": "S256",
	}
	code := codeFromLocation(t, approve(t, ts, srv, "alice", p, false))
	status, tok := postToken(t, ts, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {redirect}, "code_verifier": {"the-wrong-verifier-entirely"},
	})
	if status != http.StatusBadRequest || tok["error"] != "invalid_grant" {
		t.Fatalf("wrong verifier: status %d err %v, want 400 invalid_grant", status, tok["error"])
	}
}

func TestOAuthMissingCodeChallenge(t *testing.T) {
	ts, srv, acc := newOAuthHub(t)
	mkAccount(t, acc, "alice")
	redirect := "https://app.example/cb"
	clientID := registerDCR(t, ts, "App", []string{redirect})
	// No code_challenge at all. redirect_uri is valid, so this bounces the error
	// back to it per RFC 6749 rather than rendering a page.
	q := authorizeQuery(map[string]string{
		"client_id": clientID, "redirect_uri": redirect, "response_type": "code", "scope": "afs:read",
	})
	req, _ := http.NewRequest("GET", ts.URL+"/oauth/authorize?"+q, nil)
	req.AddCookie(sessionCookieFor(srv, "alice"))
	res, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status %d, want 302", res.StatusCode)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	if loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("error = %q, want invalid_request", loc.Query().Get("error"))
	}
}

func TestOAuthRedirectValidation(t *testing.T) {
	ts, srv, acc := newOAuthHub(t)
	mkAccount(t, acc, "alice")
	_, challenge := pkcePair()

	// A redirect_uri not registered for the client is refused with an HTML page,
	// never a redirect to the attacker's URI.
	clientID := registerDCR(t, ts, "App", []string{"https://app.example/cb"})
	q := authorizeQuery(map[string]string{
		"client_id": clientID, "redirect_uri": "https://evil.example/cb", "response_type": "code",
		"code_challenge": challenge, "code_challenge_method": "S256",
	})
	req, _ := http.NewRequest("GET", ts.URL+"/oauth/authorize?"+q, nil)
	req.AddCookie(sessionCookieFor(srv, "alice"))
	res, _ := noRedirect().Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("mismatched redirect: status %d, want 400 (no redirect)", res.StatusCode)
	}

	// Loopback port variance succeeds: registered on one port, used on another,
	// exchanged on a third (RFC 8252).
	loopClient := registerDCR(t, ts, "CLI", []string{"http://127.0.0.1:8976/callback"})
	verifier, ch2 := pkcePair()
	p := map[string]string{
		"client_id": loopClient, "redirect_uri": "http://127.0.0.1:54321/callback",
		"response_type": "code", "scope": "afs:read", "code_challenge": ch2, "code_challenge_method": "S256",
	}
	code := codeFromLocation(t, approve(t, ts, srv, "alice", p, false))
	status, tok := postToken(t, ts, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {loopClient},
		"redirect_uri": {"http://127.0.0.1:12345/callback"}, "code_verifier": {verifier},
	})
	if status != http.StatusOK {
		t.Fatalf("loopback port variance token exchange: status %d: %v", status, tok)
	}
}

func TestOAuthResourceMismatch(t *testing.T) {
	ts, srv, acc := newOAuthHub(t)
	mkAccount(t, acc, "alice")
	redirect := "https://app.example/cb"
	clientID := registerDCR(t, ts, "App", []string{redirect})
	_, challenge := pkcePair()
	q := authorizeQuery(map[string]string{
		"client_id": clientID, "redirect_uri": redirect, "response_type": "code",
		"scope": "afs:read", "code_challenge": challenge, "code_challenge_method": "S256",
		"resource": "https://not-this-hub.example/mcp",
	})
	req, _ := http.NewRequest("GET", ts.URL+"/oauth/authorize?"+q, nil)
	req.AddCookie(sessionCookieFor(srv, "alice"))
	res, _ := noRedirect().Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status %d, want 302", res.StatusCode)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	if loc.Query().Get("error") != "invalid_target" {
		t.Fatalf("error = %q, want invalid_target", loc.Query().Get("error"))
	}
}

func TestOAuthTokenFormURLEncodedRequired(t *testing.T) {
	ts, _, _ := newOAuthHub(t)
	// A JSON body is a common client bug: reject with a clear 400 invalid_request.
	res, err := http.Post(ts.URL+"/oauth/token", "application/json",
		strings.NewReader(`{"grant_type":"authorization_code"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("json token body: status %d, want 400", res.StatusCode)
	}
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	if out["error"] != "invalid_request" {
		t.Fatalf("error = %v, want invalid_request", out["error"])
	}
}

func TestOAuthScopeDowngrade(t *testing.T) {
	ts, srv, acc := newOAuthHub(t)
	mkAccount(t, acc, "alice")
	redirect := "https://app.example/cb"
	clientID := registerDCR(t, ts, "App", []string{redirect})
	verifier, challenge := pkcePair()
	p := map[string]string{
		"client_id": clientID, "redirect_uri": redirect, "response_type": "code",
		"scope": "afs:read afs:write", "code_challenge": challenge, "code_challenge_method": "S256",
	}
	// Approve with the write checkbox OFF -> downgraded to read-only.
	code := codeFromLocation(t, approve(t, ts, srv, "alice", p, false))
	status, tok := postToken(t, ts, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {redirect}, "code_verifier": {verifier},
	})
	if status != http.StatusOK {
		t.Fatalf("status %d: %v", status, tok)
	}
	if tok["scope"] != "afs:read" {
		t.Fatalf("downgraded scope = %v, want afs:read", tok["scope"])
	}
	// A refresh may not widen back to write.
	access, _ := tok["access_token"].(string)
	if _, sc, _ := srv.VerifyMCPBearer(access); strings.Join(sc, " ") != "afs:read" {
		t.Fatalf("access scope = %v, want afs:read", sc)
	}
	refresh, _ := tok["refresh_token"].(string)
	status2, tok2 := postToken(t, ts, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "scope": {"afs:read afs:write"},
	})
	if status2 != http.StatusBadRequest || tok2["error"] != "invalid_scope" {
		t.Fatalf("scope widening: status %d err %v, want 400 invalid_scope", status2, tok2["error"])
	}
}

// --- CIMD: happy path (loopback allowed) + SSRF rejection ------------------

func TestOAuthCIMD(t *testing.T) {
	ts, srv, acc := newOAuthHub(t)
	mkAccount(t, acc, "alice")

	// A TLS metadata host serving the client's own document. httptest's default
	// cert covers 127.0.0.1, so trusting it lets the fetch complete over https.
	var metaURL string
	meta := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a := r.Header.Get("Accept"); !strings.Contains(a, "application/json") {
			t.Errorf("CIMD fetch Accept = %q", a)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"client_id":     metaURL,
			"client_name":   "Claude Code",
			"redirect_uris": []string{"https://claude.ai/cb", "http://localhost/callback"},
		})
	}))
	t.Cleanup(meta.Close)
	metaURL = meta.URL // client_id == the exact document URL

	// SSRF-rejection FIRST, with the default (loopback-blocking) guard: the
	// metadata host is on 127.0.0.1, so the fetch is refused and authorize can't
	// verify the client -> HTML error, never a redirect.
	_, challenge := pkcePair()
	q := authorizeQuery(map[string]string{
		"client_id": metaURL, "redirect_uri": "https://claude.ai/cb", "response_type": "code",
		"code_challenge": challenge, "code_challenge_method": "S256",
	})
	req, _ := http.NewRequest("GET", ts.URL+"/oauth/authorize?"+q, nil)
	req.AddCookie(sessionCookieFor(srv, "alice"))
	res, _ := noRedirect().Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("SSRF guard should refuse loopback CIMD fetch: status %d, want 400", res.StatusCode)
	}

	// Now allow loopback (test stand-in for a real public metadata host) and
	// trust the test server's certificate.
	pool := x509.NewCertPool()
	pool.AddCert(meta.Certificate())
	srv.cimdAllowLoopback = true
	srv.cimdTLSConfig = &tls.Config{RootCAs: pool}

	// Happy path: a loopback redirect_uri declared INSIDE the doc is honored even
	// though a loopback FETCH target would be blocked without the flag — the two
	// guards are independent.
	verifier, ch2 := pkcePair()
	p := map[string]string{
		"client_id": metaURL, "redirect_uri": "http://localhost:9999/callback", "response_type": "code",
		"scope": "afs:read", "code_challenge": ch2, "code_challenge_method": "S256",
	}
	loc := approve(t, ts, srv, "alice", p, false)
	code := codeFromLocation(t, loc)
	status, tok := postToken(t, ts, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {metaURL},
		"redirect_uri": {"http://localhost:9999/callback"}, "code_verifier": {verifier},
	})
	if status != http.StatusOK {
		t.Fatalf("CIMD token exchange: status %d: %v", status, tok)
	}
	// The client is now cached with kind cimd.
	if c, ok := acc.OAuthClient(metaURL); !ok || c.Kind != "cimd" || c.Name != "Claude Code" {
		t.Fatalf("CIMD client not cached correctly: %+v ok=%v", c, ok)
	}
}

// --- VerifyMCPBearer: PAT fallback + expired access ------------------------

func TestVerifyMCPBearerPATAndExpiry(t *testing.T) {
	ts, srv, acc := newOAuthHub(t)
	_ = ts
	// A hub PAT resolves with full scope (the power-user path).
	mkAccount(t, acc, "bob")
	pat, err := acc.CreatePAT("bob", "cli")
	if err != nil {
		t.Fatal(err)
	}
	user, scopes, ok := srv.VerifyMCPBearer(pat)
	if !ok || user != "bob" || strings.Join(scopes, " ") != "afs:read afs:write" {
		t.Fatalf("PAT bearer = %q %v %v", user, scopes, ok)
	}
	// A bootstrap env token also resolves with full scope.
	srv.Tokens.Add("carol", "boot-token-xyz")
	if u, sc, ok := srv.VerifyMCPBearer("boot-token-xyz"); !ok || u != "carol" || strings.Join(sc, " ") != "afs:read afs:write" {
		t.Fatalf("bootstrap bearer = %q %v %v", u, sc, ok)
	}
	// Garbage fails.
	if _, _, ok := srv.VerifyMCPBearer("nope"); ok {
		t.Fatal("bogus bearer must not resolve")
	}

	// An expired OAuth access token fails closed. Insert one directly (same
	// package) with an expiry in the past.
	if _, err := acc.db.Exec(`INSERT INTO oauth_tokens(token_hash,kind,family,client_id,user,scope,expires,revoked) VALUES(?,?,?,?,?,?,?,0)`,
		tokenHash("afsmcp_expired"), "access", "fam_x", "cli_x", "bob", "afs:read", time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := srv.VerifyMCPBearer("afsmcp_expired"); ok {
		t.Fatal("expired access token must not verify")
	}
	// A refresh token is not a bearer credential for the resource.
	_, refresh, err := acc.IssueOAuthTokens("cli_x", "bob", "afs:read")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := srv.VerifyMCPBearer(refresh); ok {
		t.Fatal("a refresh token must not verify as a bearer")
	}
}
