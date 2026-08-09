package hub

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentsfs.ai/afs/internal/core"
)

// Tests for the /api/v1 save API (apiv1.go, apiv1_files.go) and the first-party
// OAuth client that fronts it (oauth_firstparty.go).

// --- helpers ---------------------------------------------------------------

// mkOAuthToken mints an access token for user with an exact scope string,
// attributed to clientID — the short path for the scope-enforcement tests, which
// care about what a token carries, not how it was obtained. The full
// authorization-code + PKCE journey is exercised separately by
// TestMarkdownToClientPKCEFlow.
func mkOAuthToken(t *testing.T, acc *AccountStore, clientID, user, scope string) string {
	t.Helper()
	access, _, err := acc.IssueOAuthTokens(clientID, user, scope)
	if err != nil {
		t.Fatalf("issue tokens: %v", err)
	}
	return access
}

// allV1Scopes is everything the save API understands, for a "fully granted"
// token.
const allV1Scopes = "profile instances:read instances:write sharelinks:create"

// v1Do issues a request against the save API and returns status, body, and
// headers. body is sent verbatim (raw file bytes for a PUT, JSON for the rest).
func v1Do(t *testing.T, ts *httptest.Server, method, path, token, body string, headers map[string]string) (int, []byte, http.Header) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res.Header
}

// v1JSON decodes a response body into a map, failing the test on garbage.
func v1JSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return out
}

// v1Put saves content and returns status plus the decoded result.
func v1Put(t *testing.T, ts *httptest.Server, token, path, content, ifMatch string) (int, map[string]any) {
	t.Helper()
	h := map[string]string{}
	if ifMatch != "" {
		h["If-Match"] = ifMatch
	}
	status, body, _ := v1Do(t, ts, http.MethodPut, path, token, content, h)
	return status, v1JSON(t, body)
}

// --- the source hash contract ----------------------------------------------

// TestSourceHashIsPatchEngineHash pins the exact hash the If-Match/ETag
// precondition speaks. The Markdown To patch engine computes
// sha256(text, utf-8).digest('hex') and refuses any mutation whose `expect` does
// not match, so this API must produce byte-identical values or the two conflict
// models silently diverge. If this test ever needs changing, the client side has
// to change in the same commit.
func TestSourceHashIsPatchEngineHash(t *testing.T) {
	const doc = "---\nmarkdownto: kanban@1\n---\n\n# Board\n"
	want := sha256.Sum256([]byte(doc))
	if got := sourceHash([]byte(doc)); got != hex.EncodeToString(want[:]) {
		t.Fatalf("sourceHash = %q, want lowercase hex sha256 %q", got, hex.EncodeToString(want[:]))
	}
	// Known-answer check so a refactor can't quietly switch algorithms.
	if got := sourceHash([]byte("")); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("sourceHash(\"\") = %q, want the sha256 empty-string digest", got)
	}
}

func TestETagMatches(t *testing.T) {
	const h = "abc123"
	for _, tc := range []struct {
		ifMatch string
		want    bool
	}{
		{`"abc123"`, true},
		{`abc123`, true},           // bare, tolerated
		{`"nope", "abc123"`, true}, // RFC 7232 list form
		{`"nope"`, false},
		{`W/"abc123"`, false}, // weak validators never authorize an overwrite
		{``, false},
	} {
		if got := etagMatches(tc.ifMatch, h); got != tc.want {
			t.Errorf("etagMatches(%q) = %v, want %v", tc.ifMatch, got, tc.want)
		}
	}
}

// --- first-party client registration ---------------------------------------

// TestMarkdownToClientRegistered asserts the declared client is reconciled into
// the store on open, with the redirect list code says it has.
func TestMarkdownToClientRegistered(t *testing.T) {
	_, _, acc := newOAuthHub(t)
	c, ok := acc.OAuthClient(markdownToClientID)
	if !ok {
		t.Fatalf("first-party client %q was not seeded", markdownToClientID)
	}
	if c.Kind != firstPartyKind {
		t.Errorf("kind = %q, want %q", c.Kind, firstPartyKind)
	}
	if c.Name != "Markdown To" {
		t.Errorf("name = %q", c.Name)
	}
	if !redirectAllowed(c, "https://markdownto.ai/app/") {
		t.Errorf("production redirect not registered: %v", c.RedirectURIs)
	}
	// RFC 8252: a loopback redirect may use any port.
	if !redirectAllowed(c, "http://localhost:5173/app/") {
		t.Errorf("loopback dev redirect on another port must match: %v", c.RedirectURIs)
	}
	// Anything not registered stays refused, including a neighboring path on the
	// same host.
	for _, bad := range []string{"https://markdownto.ai/", "https://markdownto.ai/app", "https://evil.example/app/"} {
		if redirectAllowed(c, bad) {
			t.Errorf("redirect %q must not be allowed", bad)
		}
	}
}

// TestFirstPartySeedIsIdempotent re-opens the same database and checks the
// client is reconciled, not duplicated — the seed runs on every start.
func TestFirstPartySeedIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		acc, err := OpenAccounts(dir + "/acc.db")
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		var n int
		if err := acc.db.QueryRow(`SELECT count(*) FROM oauth_clients WHERE id=?`, markdownToClientID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("after open %d: %d rows for the first-party client, want 1", i, n)
		}
	}
}

// TestScopeMetadataAdvertised checks the AS advertises the new scopes, and that
// the protected-resource metadata for /mcp still advertises only the two MCP
// scopes — the save API is a different resource and must not widen that document.
func TestScopeMetadataAdvertised(t *testing.T) {
	ts, _, _ := newOAuthHub(t)
	res, err := http.Get(ts.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	json.NewDecoder(res.Body).Decode(&meta)
	res.Body.Close()
	for _, want := range []string{scopeProfile, scopeInstancesRead, scopeInstancesWrite, scopeShareLinksCreate, scopeRead, scopeWrite} {
		if !hasJSONString(meta["scopes_supported"], want) {
			t.Errorf("scopes_supported missing %q: %v", want, meta["scopes_supported"])
		}
	}
	res2, err := http.Get(ts.URL + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatal(err)
	}
	var prm map[string]any
	json.NewDecoder(res2.Body).Decode(&prm)
	res2.Body.Close()
	if hasJSONString(prm["scopes_supported"], scopeInstancesWrite) {
		t.Errorf("the /mcp resource must not advertise save-API scopes: %v", prm["scopes_supported"])
	}
}

// TestMarkdownToClientPKCEFlow walks the whole authorization-code + PKCE journey
// the playground runs: authorize with the save-API scopes, consent (naming the
// app and each grant), code → tokens, the access token works on the API, and the
// refresh token rotates.
func TestMarkdownToClientPKCEFlow(t *testing.T) {
	ts, srv, acc := newOAuthHub(t)
	mkAccount(t, acc, "alice")
	verifier, challenge := pkcePair()
	const redirect = "https://markdownto.ai/app/"
	params := map[string]string{
		"client_id": markdownToClientID, "redirect_uri": redirect, "response_type": "code",
		"scope": allV1Scopes, "state": "xyz",
		"code_challenge": challenge, "code_challenge_method": "S256",
	}

	// The consent screen names the app and every requested grant.
	cli := noRedirect()
	req, _ := http.NewRequest("GET", ts.URL+"/oauth/authorize?"+authorizeQuery(params), nil)
	req.AddCookie(sessionCookieFor(srv, "alice"))
	res, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("consent GET: %d: %s", res.StatusCode, page)
	}
	for _, want := range []string{"Markdown To", "markdownto.ai", "Save files into your knowledge bases", "Create share links"} {
		if !strings.Contains(string(page), want) {
			t.Errorf("consent page does not mention %q", want)
		}
	}

	loc := approveGrants(t, ts, srv, "alice", params, []string{scopeInstancesWrite, scopeShareLinksCreate})
	code := codeFromLocation(t, loc)
	if u, _ := url.Parse(loc); u.Query().Get("state") != "xyz" {
		t.Errorf("state not returned: %s", loc)
	}
	if !strings.HasPrefix(loc, redirect) {
		t.Errorf("redirected to %q, want the registered redirect", loc)
	}

	status, tok := postToken(t, ts, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"client_id": {markdownToClientID}, "redirect_uri": {redirect},
		"code_verifier": {verifier},
	})
	if status != http.StatusOK {
		t.Fatalf("token exchange: %d %v", status, tok)
	}
	if tok["scope"] != allV1Scopes {
		t.Fatalf("granted scope = %v, want %q", tok["scope"], allV1Scopes)
	}
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("missing tokens: %v", tok)
	}

	// The access token resolves with its client attributed, which is what a save
	// stamps into the commit trailer.
	g, ok := acc.VerifyOAuthAccess(access)
	if !ok || g.User != "alice" || g.ClientID != markdownToClientID {
		t.Fatalf("VerifyOAuthAccess = %+v %v", g, ok)
	}
	// And it does NOT carry the MCP scopes: a save-API grant is not a tool-surface
	// grant.
	if hasScope(g.Scope, scopeWrite) || hasScope(g.Scope, scopeRead) {
		t.Errorf("save-API grant leaked MCP scopes: %q", g.Scope)
	}

	// Rotation works and narrowing is allowed; widening is not.
	status2, tok2 := postToken(t, ts, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "scope": {"profile instances:read"},
	})
	if status2 != http.StatusOK || tok2["scope"] != "profile instances:read" {
		t.Fatalf("refresh narrow: %d %v", status2, tok2)
	}
	newRefresh, _ := tok2["refresh_token"].(string)
	status3, tok3 := postToken(t, ts, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {newRefresh}, "scope": {allV1Scopes},
	})
	if status3 != http.StatusBadRequest || tok3["error"] != "invalid_scope" {
		t.Fatalf("refresh widening: %d %v, want 400 invalid_scope", status3, tok3)
	}
}

// TestConsentDowngradeDropsScope confirms unticking a grant on the consent form
// narrows what the code carries.
func TestConsentDowngradeDropsScope(t *testing.T) {
	ts, srv, acc := newOAuthHub(t)
	mkAccount(t, acc, "alice")
	verifier, challenge := pkcePair()
	const redirect = "https://markdownto.ai/app/"
	params := map[string]string{
		"client_id": markdownToClientID, "redirect_uri": redirect, "response_type": "code",
		"scope": allV1Scopes, "code_challenge": challenge, "code_challenge_method": "S256",
	}
	// Approve everything except share links.
	loc := approveGrants(t, ts, srv, "alice", params, []string{scopeInstancesWrite})
	status, tok := postToken(t, ts, url.Values{
		"grant_type": {"authorization_code"}, "code": {codeFromLocation(t, loc)},
		"client_id": {markdownToClientID}, "redirect_uri": {redirect}, "code_verifier": {verifier},
	})
	if status != http.StatusOK {
		t.Fatalf("token: %d %v", status, tok)
	}
	if tok["scope"] != "profile instances:read instances:write" {
		t.Fatalf("scope = %v, want share links dropped", tok["scope"])
	}
}

// approveGrants drives the consent POST, ticking exactly the named downgradable
// scopes. It is the multi-scope sibling of oauth_test.go's approve helper.
func approveGrants(t *testing.T, ts *httptest.Server, srv *Server, user string, p map[string]string, grants []string) string {
	t.Helper()
	form := url.Values{}
	for k, v := range p {
		form.Set(k, v)
	}
	form.Set("csrf", oauthCSRFToken(srv.sessionSecret(), user))
	form.Set("decision", "approve")
	for _, g := range grants {
		form.Add("grant", g)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(sessionCookieFor(srv, user))
	res, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("consent approve: status %d, want 302", res.StatusCode)
	}
	return res.Header.Get("Location")
}

// --- /me -------------------------------------------------------------------

func TestAPIV1Me(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)

	status, body, _ := v1Do(t, ts, http.MethodGet, "/api/v1/me", token, "", nil)
	if status != http.StatusOK {
		t.Fatalf("me: %d %s", status, body)
	}
	me := v1JSON(t, body)
	if me["user"] != "alice" || me["client"] != markdownToClientID {
		t.Fatalf("me = %v", me)
	}
	def, _ := me["defaultTarget"].(map[string]any)
	if def["instance"] != defaultInstanceName || def["dir"] != defaultCollectionDir || def["exists"] != false {
		t.Fatalf("defaultTarget = %v", def)
	}
	// A token without the profile scope cannot read it.
	narrow := mkOAuthToken(t, acc, markdownToClientID, "alice", "instances:read")
	if status, _, _ := v1Do(t, ts, http.MethodGet, "/api/v1/me", narrow, "", nil); status != http.StatusForbidden {
		t.Fatalf("me without profile = %d, want 403", status)
	}
}

// --- scope enforcement -----------------------------------------------------

// TestAPIV1ScopeEnforcement walks every endpoint with a token that holds every
// scope EXCEPT the one it requires, and asserts a 403 naming the missing scope.
func TestAPIV1ScopeEnforcement(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	// Something to address; created with a fully-scoped token.
	full := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)
	if st, res := v1Put(t, ts, full, "/api/v1/instances/alice/apps/files/apps/b.md", "# b\n", ""); st != http.StatusCreated {
		t.Fatalf("setup save: %d %v", st, res)
	}

	cases := []struct {
		name, method, path, body, required string
	}{
		{"me", http.MethodGet, "/api/v1/me", "", scopeProfile},
		{"list instances", http.MethodGet, "/api/v1/instances", "", scopeInstancesRead},
		{"create instance", http.MethodPost, "/api/v1/instances", `{"name":"other"}`, scopeInstancesWrite},
		{"list files", http.MethodGet, "/api/v1/instances/alice/apps/files", "", scopeInstancesRead},
		{"get file", http.MethodGet, "/api/v1/instances/alice/apps/files/apps/b.md", "", scopeInstancesRead},
		{"put file", http.MethodPut, "/api/v1/instances/alice/apps/files/apps/c.md", "# c\n", scopeInstancesWrite},
		{"share link", http.MethodPost, "/api/v1/instances/alice/apps/sharelinks", `{"path":"apps/b.md"}`, scopeShareLinksCreate},
	}
	for _, tc := range cases {
		// Everything except the required scope.
		var kept []string
		for _, sc := range strings.Fields(allV1Scopes) {
			if sc != tc.required {
				kept = append(kept, sc)
			}
		}
		token := mkOAuthToken(t, acc, markdownToClientID, "alice", strings.Join(kept, " "))
		status, body, hdr := v1Do(t, ts, tc.method, tc.path, token, tc.body, nil)
		if status != http.StatusForbidden {
			t.Errorf("%s without %s: status %d, want 403 (%s)", tc.name, tc.required, status, body)
			continue
		}
		if !strings.Contains(string(body), tc.required) {
			t.Errorf("%s: 403 body %s should name %q", tc.name, body, tc.required)
		}
		if ch := hdr.Get("WWW-Authenticate"); !strings.Contains(ch, "insufficient_scope") {
			t.Errorf("%s: WWW-Authenticate = %q, want insufficient_scope", tc.name, ch)
		}
		// And with the scope present it is not a 403.
		ok := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)
		if status, body, _ := v1Do(t, ts, tc.method, tc.path, ok, tc.body, map[string]string{"If-Match": "*"}); status == http.StatusForbidden {
			t.Errorf("%s with %s: still 403 (%s)", tc.name, tc.required, body)
		}
	}
}

// TestAPIV1Unauthenticated checks the whole surface is closed without a
// credential.
func TestAPIV1Unauthenticated(t *testing.T) {
	ts, _, _ := newAPIHub(t)
	for _, p := range []string{"/api/v1/me", "/api/v1/instances", "/api/v1/instances/alice/apps/files/x.md"} {
		status, _, hdr := v1Do(t, ts, http.MethodGet, p, "", "", nil)
		if status != http.StatusUnauthorized {
			t.Errorf("%s unauthenticated = %d, want 401", p, status)
		}
		if !strings.HasPrefix(hdr.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: missing bearer challenge", p)
		}
	}
}

// TestAPIV1PATScopes documents the PAT bargain: a personal access token reaches
// the read/write surface (the same reach its owner has over the git remote) but
// can never mint a share link — publishing a private file stays a decision a
// human makes at a browser.
func TestAPIV1PATScopes(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	pat := mkUser(t, acc, "alice")

	if st, res := v1Put(t, ts, pat, "/api/v1/instances/alice/apps/files/apps/b.md", "# b\n", ""); st != http.StatusCreated {
		t.Fatalf("PAT save: %d %v", st, res)
	}
	if st, _, _ := v1Do(t, ts, http.MethodGet, "/api/v1/me", pat, "", nil); st != http.StatusOK {
		t.Errorf("PAT /me = %d, want 200", st)
	}
	status, body, _ := v1Do(t, ts, http.MethodPost, "/api/v1/instances/alice/apps/sharelinks", pat, `{"path":"apps/b.md"}`, nil)
	if status != http.StatusForbidden || !strings.Contains(string(body), scopeShareLinksCreate) {
		t.Fatalf("PAT share link = %d %s, want 403 naming %s", status, body, scopeShareLinksCreate)
	}
}

// --- If-Match / conflicts --------------------------------------------------

func TestAPIV1SaveIfMatch(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)
	const p = "/api/v1/instances/alice/apps/files/apps/board.kanban.md"
	const v1 = "---\nmarkdownto: kanban@1\n---\n\n# Board\n"
	const v2 = "---\nmarkdownto: kanban@1\n---\n\n# Board v2\n"

	// Create: no If-Match needed, and the response carries the hash for next time.
	status, res := v1Put(t, ts, token, p, v1, "")
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, res)
	}
	if res["hash"] != sourceHash([]byte(v1)) {
		t.Fatalf("hash = %v, want %v", res["hash"], sourceHash([]byte(v1)))
	}
	if res["created"] != true || res["collection"] != true {
		t.Fatalf("create result = %v", res)
	}

	// Overwriting with no expectation at all is refused, and the refusal says
	// what the file currently is.
	status, res = v1Put(t, ts, token, p, v2, "")
	if status != http.StatusPreconditionRequired {
		t.Fatalf("unconditional overwrite: %d %v, want 428", status, res)
	}
	if res["hash"] != sourceHash([]byte(v1)) {
		t.Fatalf("428 body should carry the current hash: %v", res)
	}

	// A stale expectation is refused with the current hash.
	status, res = v1Put(t, ts, token, p, v2, `"`+sourceHash([]byte("something else"))+`"`)
	if status != http.StatusPreconditionFailed || res["hash"] != sourceHash([]byte(v1)) {
		t.Fatalf("stale If-Match: %d %v, want 412 + current hash", status, res)
	}
	// ...and nothing was written.
	_, body, _ := v1Do(t, ts, http.MethodGet, p, token, "", nil)
	if string(body) != v1 {
		t.Fatalf("a refused save must not change the file: %q", body)
	}

	// The correct expectation succeeds.
	status, res = v1Put(t, ts, token, p, v2, `"`+sourceHash([]byte(v1))+`"`)
	if status != http.StatusOK {
		t.Fatalf("conditional overwrite: %d %v", status, res)
	}
	if res["hash"] != sourceHash([]byte(v2)) || res["created"] != false {
		t.Fatalf("overwrite result = %v", res)
	}

	// If-Match on a file that does not exist is a 412, not a create.
	status, res = v1Put(t, ts, token, "/api/v1/instances/alice/apps/files/apps/ghost.md", v1, `"`+sourceHash([]byte(v1))+`"`)
	if status != http.StatusPreconditionFailed {
		t.Fatalf("If-Match on a missing file: %d %v, want 412", status, res)
	}
	// If-Match: * requires a current representation, too.
	status, res = v1Put(t, ts, token, "/api/v1/instances/alice/apps/files/apps/ghost.md", v1, "*")
	if status != http.StatusPreconditionFailed {
		t.Fatalf("If-Match:* on a missing file: %d %v, want 412", status, res)
	}
	// ...and overwrites an existing one without naming a hash.
	status, res = v1Put(t, ts, token, p, v1, "*")
	if status != http.StatusOK {
		t.Fatalf("If-Match:*: %d %v", status, res)
	}
}

// TestAPIV1RoundTripsBytes checks a saved document comes back byte-identical,
// with the hash and ETag a client needs to save again.
func TestAPIV1RoundTripsBytes(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)
	const p = "/api/v1/instances/alice/apps/files/apps/todo.md"
	// Bytes chosen to break a naive implementation: CRLF, a trailing space, a
	// tab, no trailing newline, and non-ASCII.
	content := "---\nmarkdownto: todo@1\ntitle: Café — plans\n---\r\n\n- [ ] one \n\t- [x] two"

	if status, res := v1Put(t, ts, token, p, content, ""); status != http.StatusCreated {
		t.Fatalf("save: %d %v", status, res)
	}
	status, body, hdr := v1Do(t, ts, http.MethodGet, p, token, "", nil)
	if status != http.StatusOK {
		t.Fatalf("get: %d %s", status, body)
	}
	if string(body) != content {
		t.Fatalf("round trip changed the bytes:\n got %q\nwant %q", body, content)
	}
	want := sourceHash([]byte(content))
	if hdr.Get("ETag") != `"`+want+`"` || hdr.Get("X-Afs-Source-Hash") != want {
		t.Fatalf("ETag %q / X-Afs-Source-Hash %q, want %q", hdr.Get("ETag"), hdr.Get("X-Afs-Source-Hash"), want)
	}
	if hdr.Get("X-Afs-Rev") == "" || hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers = %v", hdr)
	}
	// HEAD gives the hash without the body.
	status, body, hdr = v1Do(t, ts, http.MethodHead, p, token, "", nil)
	if status != http.StatusOK || len(body) != 0 || hdr.Get("X-Afs-Source-Hash") != want {
		t.Fatalf("HEAD: %d %q %v", status, body, hdr)
	}
}

// --- instance bootstrap ----------------------------------------------------

// TestAPIV1AutoInstance covers the contract's zero-decisions path: the first
// save creates a real agentsfs instance whose saves directory is a declared
// collection, and the second save changes nothing about it.
func TestAPIV1AutoInstance(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)
	const p = "/api/v1/instances/alice/apps/files/apps/board.kanban.md"

	status, res := v1Put(t, ts, token, p, "# one\n", "")
	if status != http.StatusCreated {
		t.Fatalf("first save: %d %v", status, res)
	}
	if res["instanceCreated"] != true {
		t.Fatalf("first save should have bootstrapped the instance: %v", res)
	}
	if !srv.Storage.Exists("alice", "apps") {
		t.Fatal("instance was not created")
	}

	bare := srv.Storage.RepoDir("alice", "apps")
	// It is an ordinary agentsfs: the contract template is there, so a plain
	// clone explains itself.
	for _, want := range []string{"AGENTS.md", "INDEX.md"} {
		if _, ok := BlobSize("git", bare, defaultRef, want); !ok {
			t.Errorf("bootstrapped instance is missing %s", want)
		}
	}
	// The saves directory is a declared collection, which is what makes a file
	// with no description: of its own legal under the contract.
	index, ok := BlobContent("git", bare, defaultRef, "apps/INDEX.md")
	if !ok {
		t.Fatal("collection INDEX.md missing")
	}
	if role := core.FrontmatterValueFromReader(strings.NewReader(index), "agentsfs_role"); role != core.RoleCollection {
		t.Errorf("apps/INDEX.md agentsfs_role = %q, want %q", role, core.RoleCollection)
	}
	if desc := core.FrontmatterValueFromReader(strings.NewReader(index), "description"); desc == "" {
		t.Error("a collection INDEX.md still needs its own description:")
	}
	head1 := srv.RepoResolve("alice", "apps")

	// A second save is an ordinary save: no re-bootstrap, no second INDEX.
	status, res = v1Put(t, ts, token, p, "# two\n", `"`+sourceHash([]byte("# one\n"))+`"`)
	if status != http.StatusOK {
		t.Fatalf("second save: %d %v", status, res)
	}
	if res["instanceCreated"] != false {
		t.Fatalf("second save must not re-bootstrap: %v", res)
	}
	index2, _ := BlobContent("git", bare, defaultRef, "apps/INDEX.md")
	if index2 != index {
		t.Error("the collection INDEX.md was rewritten by a later save")
	}
	if head2 := srv.RepoResolve("alice", "apps"); head2 == head1 {
		t.Error("the second save did not commit")
	}

	// Every save is a real, attributed commit whose message records the app.
	log, err := gitCmd("git", bare, nil, nil, "log", "-1", "--format=%an <%ae>%n%B")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "alice") {
		t.Errorf("commit is not authored by the user: %q", log)
	}
	if !strings.Contains(log, "Via: Markdown To (markdownto.ai)") {
		t.Errorf("commit message does not record the client: %q", log)
	}
}

// TestAPIV1SavedInstancePassesDoctor is the claim the whole collection mechanism
// exists to support, checked the only way that means anything: materialize the
// instance a browser save produced and run the real `afs doctor` over it. A
// document saved from a playground carries a `markdownto:` envelope and no
// `description:` of its own — which is a contract violation ANYWHERE except
// inside a declared collection. If the bootstrap ever stops declaring one, this
// test reports the exact findings a user's `afs doctor` would.
func TestAPIV1SavedInstancePassesDoctor(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)
	for _, f := range []struct{ path, body string }{
		{"apps/board.kanban.md", "---\nmarkdownto: kanban@1\n---\n\n# Board\n"},
		{"apps/list.todo.md", "---\nmarkdownto: todo@1\n---\n\n- [ ] a\n"},
	} {
		if st, res := v1Put(t, ts, token, "/api/v1/instances/alice/apps/files/"+f.path, f.body, ""); st != http.StatusCreated {
			t.Fatalf("save %s: %d %v", f.path, st, res)
		}
	}

	root := checkoutBare(t, srv.Storage.RepoDir("alice", "apps"))
	roles, err := core.ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles.Collections) != 1 || roles.Collections[0] != defaultCollectionDir {
		t.Fatalf("collections = %v, want [%s]", roles.Collections, defaultCollectionDir)
	}
	findings, err := core.Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		t.Errorf("doctor finding on a freshly-saved instance: %+v", f)
	}
}

// checkoutBare materializes a bare repo's HEAD into a temp directory, so a test
// can run the filesystem-shaped core tooling (doctor, roles) over what the Hub
// actually stored.
func checkoutBare(t *testing.T, bare string) string {
	t.Helper()
	root := t.TempDir()
	out, err := exec.Command("git", "-C", bare, "archive", "--format=tar", defaultRef).Output()
	if err != nil {
		t.Fatalf("git archive: %v", err)
	}
	tr := tar.NewReader(bytes.NewReader(out))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue // git archive's pax_global_header, not a file in the tree
		}
		dest := filepath.Join(root, filepath.FromSlash(hdr.Name))
		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(dest)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			t.Fatal(err)
		}
		f.Close()
	}
	return root
}

// TestAPIV1BootstrapEndpoint checks the explicit bootstrap is idempotent — it is
// the call a client makes at sign-in, and asking twice must not be an error.
func TestAPIV1BootstrapEndpoint(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)

	status, body, _ := v1Do(t, ts, http.MethodPost, "/api/v1/instances", token, "", nil)
	if status != http.StatusCreated {
		t.Fatalf("bootstrap: %d %s", status, body)
	}
	first := v1JSON(t, body)
	if first["instance"] != defaultInstanceName || first["created"] != true || first["collectionCreated"] != true {
		t.Fatalf("bootstrap = %v", first)
	}

	status, body, _ = v1Do(t, ts, http.MethodPost, "/api/v1/instances", token, `{"name":"apps"}`, nil)
	if status != http.StatusOK {
		t.Fatalf("re-bootstrap: %d %s", status, body)
	}
	second := v1JSON(t, body)
	if second["created"] != false || second["collectionCreated"] != false {
		t.Fatalf("re-bootstrap must be a no-op: %v", second)
	}
	if second["rev"] != first["rev"] {
		t.Errorf("re-bootstrap moved HEAD: %v then %v", first["rev"], second["rev"])
	}

	// And the instance shows up in the listing.
	status, body, _ = v1Do(t, ts, http.MethodGet, "/api/v1/instances", token, "", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"apps"`) {
		t.Fatalf("list instances: %d %s", status, body)
	}
}

// --- listing + envelope filter ---------------------------------------------

func TestAPIV1ListFilesEnvelopeFilter(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)
	save := func(p, content string) {
		t.Helper()
		if st, res := v1Put(t, ts, token, "/api/v1/instances/alice/apps/files/"+p, content, ""); st != http.StatusCreated {
			t.Fatalf("save %s: %d %v", p, st, res)
		}
	}
	save("apps/board.kanban.md", "---\nmarkdownto: kanban@1\ntitle: My board\n---\n\n# Board\n")
	save("apps/list.todo.md", "---\nmarkdownto: todo@1\n---\n\n- [ ] a\n")
	save("apps/notes.md", "---\ndescription: just a note\n---\n\nplain\n")

	list := func(query string) map[string]any {
		t.Helper()
		status, body, _ := v1Do(t, ts, http.MethodGet, "/api/v1/instances/alice/apps/files"+query, token, "", nil)
		if status != http.StatusOK {
			t.Fatalf("list%s: %d %s", query, status, body)
		}
		return v1JSON(t, body)
	}
	paths := func(m map[string]any) []string {
		var out []string
		for _, f := range m["files"].([]any) {
			out = append(out, f.(map[string]any)["path"].(string))
		}
		return out
	}

	// No filter: every markdown document, including the instance's own contract
	// files.
	all := paths(list(""))
	if len(all) < 4 {
		t.Fatalf("unfiltered listing looks wrong: %v", all)
	}

	// Envelope filter: "the little apps I made", in one query.
	got := paths(list("?markdownto"))
	if strings.Join(got, ",") != "apps/board.kanban.md,apps/list.todo.md" {
		t.Fatalf("envelope filter = %v", got)
	}
	// By spec name, version-agnostic.
	if got := paths(list("?markdownto=kanban")); strings.Join(got, ",") != "apps/board.kanban.md" {
		t.Fatalf("spec filter = %v", got)
	}
	// By exact envelope value.
	if got := paths(list("?markdownto=todo@1")); strings.Join(got, ",") != "apps/list.todo.md" {
		t.Fatalf("exact envelope filter = %v", got)
	}
	if got := paths(list("?markdownto=nosuchspec")); len(got) != 0 {
		t.Fatalf("unknown spec filter = %v, want none", got)
	}
	// Directory scoping.
	if got := paths(list("?dir=apps&markdownto")); len(got) != 2 {
		t.Fatalf("dir-scoped filter = %v", got)
	}
	if got := paths(list("?dir=agent-journal&markdownto")); len(got) != 0 {
		t.Fatalf("dir scope leaked: %v", got)
	}

	// Entries carry what a client needs to open a file without a second call.
	entry := list("?markdownto=kanban")["files"].([]any)[0].(map[string]any)
	if entry["markdownto"] != "kanban@1" || entry["title"] != "My board" {
		t.Fatalf("entry = %v", entry)
	}
	if entry["hash"] != sourceHash([]byte("---\nmarkdownto: kanban@1\ntitle: My board\n---\n\n# Board\n")) {
		t.Fatalf("entry hash = %v", entry["hash"])
	}
}

// --- share links -----------------------------------------------------------

func TestAPIV1CreateShareLink(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	mkUser(t, acc, "bob")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)
	if st, res := v1Put(t, ts, token, "/api/v1/instances/alice/apps/files/apps/b.kanban.md", "# b\n", ""); st != http.StatusCreated {
		t.Fatalf("save: %d %v", st, res)
	}

	status, body, _ := v1Do(t, ts, http.MethodPost, "/api/v1/instances/alice/apps/sharelinks", token,
		`{"path":"apps/b.kanban.md"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("share: %d %s", status, body)
	}
	out := v1JSON(t, body)
	link, _ := out["url"].(string)
	if !strings.Contains(link, sharePrefix+"shr_") {
		t.Fatalf("share url = %q", link)
	}
	// The link really serves the file, to a reader with no account at all.
	res, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(page), "b.kanban.md") {
		t.Fatalf("share view: %d %s", res.StatusCode, page)
	}

	// A file that isn't there can't be published.
	if status, _, _ := v1Do(t, ts, http.MethodPost, "/api/v1/instances/alice/apps/sharelinks", token,
		`{"path":"apps/ghost.md"}`, nil); status != http.StatusNotFound {
		t.Errorf("publishing a missing file = %d, want 404", status)
	}
	// Someone else's instance is invisible, not merely forbidden.
	bobToken := mkOAuthToken(t, acc, markdownToClientID, "bob", allV1Scopes)
	if status, _, _ := v1Do(t, ts, http.MethodPost, "/api/v1/instances/alice/apps/sharelinks", bobToken,
		`{"path":"apps/b.kanban.md"}`, nil); status != http.StatusNotFound {
		t.Errorf("bob publishing alice's file = %d, want 404", status)
	}
}

// --- access boundaries -----------------------------------------------------

func TestAPIV1AccessBoundaries(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	mkUser(t, acc, "bob")
	alice := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)
	bob := mkOAuthToken(t, acc, markdownToClientID, "bob", allV1Scopes)
	if st, res := v1Put(t, ts, alice, "/api/v1/instances/alice/apps/files/apps/b.md", "# b\n", ""); st != http.StatusCreated {
		t.Fatalf("save: %d %v", st, res)
	}

	// Bob cannot see alice's private instance at all.
	if st, _, _ := v1Do(t, ts, http.MethodGet, "/api/v1/instances/alice/apps/files/apps/b.md", bob, "", nil); st != http.StatusNotFound {
		t.Errorf("bob reading alice's private file = %d, want 404", st)
	}
	// And a save into her namespace neither writes nor creates an instance.
	if st, _ := v1Put(t, ts, bob, "/api/v1/instances/alice/other/files/x.md", "hi", ""); st != http.StatusNotFound {
		t.Errorf("bob saving into alice's namespace = %d, want 404", st)
	}
	if srv.Storage.Exists("alice", "other") {
		t.Error("a rejected save created an instance in someone else's namespace")
	}
	// A read collaborator may read but not save.
	if err := acc.AddCollaborator("alice", "apps", "bob", "read"); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := v1Do(t, ts, http.MethodGet, "/api/v1/instances/alice/apps/files/apps/b.md", bob, "", nil); st != http.StatusOK {
		t.Errorf("read collaborator = %d, want 200", st)
	}
	if st, _ := v1Put(t, ts, bob, "/api/v1/instances/alice/apps/files/apps/b.md", "# nope\n", "*"); st != http.StatusForbidden {
		t.Errorf("read collaborator saving = %d, want 403", st)
	}
	// Path traversal is refused.
	if st, _ := v1Put(t, ts, alice, "/api/v1/instances/alice/apps/files/../../../etc/passwd", "x", ""); st != http.StatusBadRequest && st != http.StatusNotFound {
		t.Errorf("traversal = %d, want 400/404", st)
	}
}

// --- CORS ------------------------------------------------------------------

func TestAPIV1CORS(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)

	preflight := func(origin, path string) http.Header {
		t.Helper()
		req, _ := http.NewRequest(http.MethodOptions, ts.URL+path, nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "PUT")
		req.Header.Set("Access-Control-Request-Headers", "authorization, if-match, x-afs-message")
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("preflight %s: status %d, want 204", origin, res.StatusCode)
		}
		return res.Header
	}

	// A first-party origin is admitted, with the headers a save needs.
	h := preflight("https://markdownto.ai", "/api/v1/instances/alice/apps/files/apps/b.md")
	if h.Get("Access-Control-Allow-Origin") != "https://markdownto.ai" {
		t.Fatalf("allow-origin = %q", h.Get("Access-Control-Allow-Origin"))
	}
	for _, want := range []string{"Authorization", "If-Match", "X-Afs-Message"} {
		if !strings.Contains(h.Get("Access-Control-Allow-Headers"), want) {
			t.Errorf("allow-headers %q missing %q", h.Get("Access-Control-Allow-Headers"), want)
		}
	}
	if !strings.Contains(h.Get("Access-Control-Allow-Methods"), "PUT") {
		t.Errorf("allow-methods = %q", h.Get("Access-Control-Allow-Methods"))
	}
	if !strings.Contains(h.Get("Access-Control-Expose-Headers"), "ETag") {
		t.Errorf("expose-headers %q must include ETag, or the SPA cannot read the hash it must send back", h.Get("Access-Control-Expose-Headers"))
	}
	// Credentials are NEVER allowed: this API is bearer-only, so no cross-site
	// page can ride an ambient Hub session into a knowledge base.
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("allow-credentials must never be set, got %q", h.Get("Access-Control-Allow-Credentials"))
	}
	if !strings.Contains(h.Get("Vary"), "Origin") {
		t.Errorf("Vary = %q, must include Origin", h.Get("Vary"))
	}

	// A dev server on loopback is admitted on any port.
	if got := preflight("http://localhost:5173", "/api/v1/instances").Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("loopback origin = %q", got)
	}
	// An unknown origin is not.
	if got := preflight("https://evil.example", "/api/v1/instances").Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("unknown origin admitted: %q", got)
	}
	// An operator-configured extra origin is.
	srv.APIOrigins = []string{"https://staging.markdownto.ai"}
	if got := preflight("https://staging.markdownto.ai", "/api/v1/instances").Get("Access-Control-Allow-Origin"); got != "https://staging.markdownto.ai" {
		t.Errorf("configured origin = %q", got)
	}

	// A real (non-preflight) response carries the headers too.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/me", nil)
	req.Header.Set("Origin", "https://markdownto.ai")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Header.Get("Access-Control-Allow-Origin") != "https://markdownto.ai" {
		t.Errorf("actual response allow-origin = %q", res.Header.Get("Access-Control-Allow-Origin"))
	}

	// The token endpoint answers cross-origin too, or the SPA can never exchange
	// its code.
	req2, _ := http.NewRequest(http.MethodOptions, ts.URL+"/oauth/token", nil)
	req2.Header.Set("Origin", "https://markdownto.ai")
	res2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.Header.Get("Access-Control-Allow-Origin") != "https://markdownto.ai" {
		t.Errorf("token endpoint preflight allow-origin = %q", res2.Header.Get("Access-Control-Allow-Origin"))
	}
	form := url.Values{"grant_type": {"authorization_code"}}
	req3, _ := http.NewRequest(http.MethodPost, ts.URL+"/oauth/token", strings.NewReader(form.Encode()))
	req3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req3.Header.Set("Origin", "https://markdownto.ai")
	res3, err := ts.Client().Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	res3.Body.Close()
	if res3.Header.Get("Access-Control-Allow-Origin") != "https://markdownto.ai" {
		t.Errorf("token endpoint allow-origin = %q", res3.Header.Get("Access-Control-Allow-Origin"))
	}
}
