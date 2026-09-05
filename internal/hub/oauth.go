package hub

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// This file is the OAuth 2.1 authorization server the Hub hosts for its own MCP
// endpoint, plus the discovery metadata that lets a consumer client (ChatGPT,
// claude.ai, Claude Code, …) find it. The design is exacting because the two
// major hosts are: PKCE-S256 is mandatory, the token endpoint is
// form-urlencoded only, refresh tokens rotate with family-reuse revocation,
// redirect URIs match exactly except for the RFC 8252 loopback-port rule, and
// clients register by either DCR (RFC 7591) or a Client ID Metadata Document
// (CIMD). See agentsfs/rfcs/hub-mcp-server.md for the full rationale; the
// persistence half lives in oauth_store.go.
//
// A guiding invariant: we NEVER redirect to a redirect_uri we haven't validated
// against the client's registered set. Errors discovered before that point
// render an HTML page; only after the redirect_uri is proven do we bounce OAuth
// error codes back to the client per RFC 6749 §4.1.2.1.

// mcpResourcePath is the single protected resource this AS mints tokens for. An
// authorize request's optional RFC 8707 `resource` must equal PublicURL()+this.
const mcpResourcePath = "/mcp"

// PublicURL is the externally reachable origin of this hub (no trailing slash) —
// the issuer, the base for every advertised OAuth endpoint, and the base of the
// MCP resource identifier. It MUST be a stable configured value because it is
// baked into signed session-independent metadata and into the token audience;
// deriving it per-request from the Host header would let a client that reached
// the hub by a different name mint tokens for the wrong audience.
//
// Source order: the explicit PublicBaseURL set from HUB_PUBLIC_URL in main; then
// the AgentManager's HubBase (also HUB_PUBLIC_URL, its long-standing home); then
// the production default. Tests set PublicBaseURL to their httptest URL.
func (s *Server) PublicURL() string {
	if b := strings.TrimRight(s.PublicBaseURL, "/"); b != "" {
		return b
	}
	if s.Agent != nil {
		if b := strings.TrimRight(s.Agent.HubBase, "/"); b != "" {
			return b
		}
	}
	return "https://hub.agentsfs.ai"
}

// mcpResource is the canonical resource identifier tokens are minted for.
func (s *Server) mcpResource() string { return s.PublicURL() + mcpResourcePath }

// ---- routing -------------------------------------------------------------
//
// ServeHTTP calls these for the exact well-known paths and the /oauth/ prefix.

// handleOAuth dispatches the three authorization-server endpoints.
func (s *Server) handleOAuth(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil {
		http.Error(w, "accounts are not enabled on this hub", http.StatusNotFound)
		return
	}
	switch r.URL.Path {
	case "/oauth/authorize":
		s.handleAuthorize(w, r)
	case "/oauth/token":
		s.handleToken(w, r)
	case "/oauth/register":
		s.handleRegister(w, r)
	default:
		http.NotFound(w, r)
	}
}

// ---- discovery metadata ---------------------------------------------------

// authServerMeta serializes the SDK's RFC 8414 metadata type; the SDK already
// carries the client_id_metadata_document_supported field, so no wrapper is
// needed — we set it true directly.
func (s *Server) authServerMeta() oauthex.AuthServerMeta {
	base := s.PublicURL()
	return oauthex.AuthServerMeta{
		Issuer:                            base,
		AuthorizationEndpoint:             base + "/oauth/authorize",
		TokenEndpoint:                     base + "/oauth/token",
		RegistrationEndpoint:              base + "/oauth/register",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
		ScopesSupported:                   scopeOrder,
		ClientIDMetadataDocumentSupported: true,
	}
}

// handleAuthServerMetadata answers both /.well-known/oauth-authorization-server
// (RFC 8414) and the /.well-known/openid-configuration alias some clients probe
// first. Identical body; the two paths exist because 2025-11-25 MCP clients may
// try either.
func (s *Server) handleAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	writeMetadataJSON(w, s.authServerMeta())
}

// handleProtectedResourceMetadata answers RFC 9728 PRM at both
// /.well-known/oauth-protected-resource and its /mcp path form (Claude probes
// the path form first). The resource MUST be the exact URL the user typed, so
// it is PublicURL()+"/mcp" with no normalization.
func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeMetadataJSON(w, oauthex.ProtectedResourceMetadata{
		Resource:               s.mcpResource(),
		AuthorizationServers:   []string{s.PublicURL()},
		ScopesSupported:        []string{scopeRead, scopeWrite},
		BearerMethodsSupported: []string{"header"},
	})
}

// writeMetadataJSON emits discovery metadata with a short cache lifetime — these
// documents are stable but clients re-probe them, so a little caching is polite.
func writeMetadataJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	json.NewEncoder(w).Encode(v)
}

// ---- dynamic client registration (RFC 7591) -------------------------------

// dcrRequest is the subset of the RFC 7591 registration request we honor.
type dcrRequest struct {
	RedirectURIs []string `json:"redirect_uris"`
	ClientName   string   `json:"client_name"`
}

const maxRegisterBody = 64 << 10 // 64 KiB — a registration body is tiny; anything larger is abuse.
const maxRedirectURIs = 32

// handleRegister implements open (unauthenticated) DCR for public clients. It
// stores the redirect URIs and name, and echoes back the minted client_id with
// token_endpoint_auth_method "none" (we issue no client secrets — every client
// is public and proves itself with PKCE).
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRegisterBody+1))
	if err != nil {
		registerError(w, "invalid_client_metadata", "could not read request body")
		return
	}
	if len(body) > maxRegisterBody {
		registerError(w, "invalid_client_metadata", "registration request too large")
		return
	}
	var req dcrRequest
	if err := json.Unmarshal(body, &req); err != nil {
		registerError(w, "invalid_client_metadata", "request body must be JSON")
		return
	}
	if len(req.RedirectURIs) == 0 {
		registerError(w, "invalid_redirect_uri", "at least one redirect_uri is required")
		return
	}
	if len(req.RedirectURIs) > maxRedirectURIs {
		registerError(w, "invalid_redirect_uri", "too many redirect_uris")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirectURI(u) {
			registerError(w, "invalid_redirect_uri", "redirect_uri must be https:// or an http loopback address")
			return
		}
	}
	client, err := s.Accounts.RegisterOAuthClient(req.ClientName, req.RedirectURIs)
	if err != nil {
		http.Error(w, "could not register client", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"client_id":                  client.ID,
		"redirect_uris":              client.RedirectURIs,
		"client_name":                client.Name,
		"token_endpoint_auth_method": "none",
	})
}

// registerError renders an RFC 7591 registration error (always HTTP 400).
func registerError(w http.ResponseWriter, code, desc string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc})
}

// ---- client resolution (DCR lookup or CIMD fetch) -------------------------

// resolveClient turns a presented client_id into a client record. A "cli_"/DCR
// id is a straight store lookup; an https:// id is a CIMD reference — served
// fresh from cache when recent, otherwise fetched (SSRF-guarded) and cached.
func (s *Server) resolveClient(clientID string) (*OAuthClient, error) {
	if strings.HasPrefix(clientID, "https://") {
		return s.resolveCIMD(clientID)
	}
	c, ok := s.Accounts.OAuthClient(clientID)
	if !ok {
		return nil, errUnknownClient
	}
	return c, nil
}

var errUnknownClient = errors.New("unknown client")

// cimdCacheTTL is how long a fetched metadata document is trusted before a
// re-fetch. Short enough that a client rotating its redirect URIs recovers
// within the hour; long enough that we don't fetch on every authorize.
const cimdCacheTTL = time.Hour

// cimdDoc is the subset of a Client ID Metadata Document we consume.
type cimdDoc struct {
	ClientID     string   `json:"client_id"`
	RedirectURIs []string `json:"redirect_uris"`
	ClientName   string   `json:"client_name"`
}

// resolveCIMD returns a cached CIMD client if fresh, else fetches and caches it.
func (s *Server) resolveCIMD(clientID string) (*OAuthClient, error) {
	if c, ok := s.Accounts.OAuthClient(clientID); ok && c.Kind == "cimd" &&
		time.Now().Unix()-c.Created < int64(cimdCacheTTL/time.Second) {
		return c, nil
	}
	doc, err := s.fetchCIMD(clientID)
	if err != nil {
		return nil, err
	}
	// The document's own client_id MUST equal the URL it was fetched from,
	// otherwise a metadata host could impersonate any client id.
	if doc.ClientID != clientID {
		return nil, errors.New("client metadata document client_id does not match its URL")
	}
	if len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > maxRedirectURIs {
		return nil, errors.New("client metadata document has no usable redirect_uris")
	}
	for _, u := range doc.RedirectURIs {
		if !validRedirectURI(u) {
			return nil, errors.New("client metadata document has an invalid redirect_uri")
		}
	}
	return s.Accounts.UpsertCIMDClient(clientID, doc.ClientName, doc.RedirectURIs)
}

// fetchCIMD retrieves and parses a Client ID Metadata Document with the full set
// of SSRF and abuse guards the RFC demands: https only, no redirects followed, a
// 10 s deadline, a 64 KiB read cap, and — critically — a dial-time check that
// the resolved IP is a public address, closing the DNS-rebinding hole (the
// Control hook sees the post-resolution address right before connect). Loopback
// is blocked in production; tests flip cimdAllowLoopback so an httptest server
// on 127.0.0.1 can stand in for a real metadata host.
func (s *Server) fetchCIMD(rawURL string) (*cimdDoc, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("client metadata URL must be https")
	}
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !s.cimdAddrAllowed(ip) {
				return errors.New("blocked non-public metadata address " + address)
			}
			return nil
		},
	}
	client := &http.Client{
		Transport: &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true, TLSClientConfig: s.cimdTLSConfig},
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("client metadata document must not redirect")
		},
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("client metadata document fetch returned " + strconv.Itoa(resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRegisterBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxRegisterBody {
		return nil, errors.New("client metadata document too large")
	}
	var doc cimdDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("client metadata document is not valid JSON")
	}
	return &doc, nil
}

// cimdAddrAllowed reports whether a resolved IP may be dialed for a CIMD fetch.
// Loopback is gated on the test-only cimdAllowLoopback; every other non-public
// range (private, link-local, multicast, unspecified) is always refused.
func (s *Server) cimdAddrAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return s.cimdAllowLoopback
	}
	if ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	return true
}

// ---- redirect URI validation ---------------------------------------------

// validRedirectURI reports whether a redirect URI is registerable: https with a
// host, or http to a loopback host (RFC 8252). No fragment is permitted
// (RFC 6749 §3.1.2).
func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "http":
		return isLoopbackHost(u.Hostname())
	default:
		return false
	}
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// redirectURIMatches implements exact-match validation with the one sanctioned
// exception: two loopback http URIs match when scheme, host, and path agree but
// the port differs (RFC 8252 — Claude Code and other native clients bind an
// ephemeral loopback port the AS can't know in advance).
func redirectURIMatches(registered, presented string) bool {
	if registered == presented {
		return true
	}
	ru, err1 := url.Parse(registered)
	pu, err2 := url.Parse(presented)
	if err1 != nil || err2 != nil {
		return false
	}
	if ru.Scheme != "http" || pu.Scheme != "http" || !isLoopbackHost(ru.Hostname()) || !isLoopbackHost(pu.Hostname()) {
		return false
	}
	return strings.EqualFold(ru.Hostname(), pu.Hostname()) && ru.Path == pu.Path
}

// redirectAllowed reports whether presented matches any of the client's
// registered redirect URIs.
func redirectAllowed(client *OAuthClient, presented string) bool {
	for _, reg := range client.RedirectURIs {
		if redirectURIMatches(reg, presented) {
			return true
		}
	}
	return false
}

// ---- authorize endpoint ---------------------------------------------------

// authorizeParams is the validated state carried through the authorize flow and
// re-serialized into the login `next=` round-trip and the consent form's hidden
// fields, so the eventual code is bound to exactly what the client asked for.
type authorizeParams struct {
	ClientID, RedirectURI, ResponseType, Scope, State string
	CodeChallenge, CodeChallengeMethod, Resource      string
}

func authorizeParamsFrom(v url.Values) authorizeParams {
	return authorizeParams{
		ClientID:            v.Get("client_id"),
		RedirectURI:         v.Get("redirect_uri"),
		ResponseType:        v.Get("response_type"),
		Scope:               v.Get("scope"),
		State:               v.Get("state"),
		CodeChallenge:       v.Get("code_challenge"),
		CodeChallengeMethod: v.Get("code_challenge_method"),
		Resource:            v.Get("resource"),
	}
}

// toURL rebuilds the canonical /oauth/authorize URL for this request — used to
// set the login `next=` target so the user lands back here after signing in.
func (p authorizeParams) toURL() string {
	q := url.Values{}
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("response_type", p.ResponseType)
	if p.Scope != "" {
		q.Set("scope", p.Scope)
	}
	if p.State != "" {
		q.Set("state", p.State)
	}
	q.Set("code_challenge", p.CodeChallenge)
	q.Set("code_challenge_method", p.CodeChallengeMethod)
	if p.Resource != "" {
		q.Set("resource", p.Resource)
	}
	return "/oauth/authorize?" + q.Encode()
}

// handleAuthorize serves the consent screen (GET) and processes the user's
// decision (POST). Validation runs before either branch; only after the
// redirect_uri is proven do we ever redirect an OAuth error back to the client.
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.oauthErrorPage(w, r, http.StatusBadRequest, "The authorization request could not be read.")
		return
	}
	p := authorizeParamsFrom(r.Form)

	// 1. Resolve the client and validate the redirect URI FIRST. Until both hold
	//    we have no safe URI to redirect to, so any failure is an HTML page.
	if p.ClientID == "" {
		s.oauthErrorPage(w, r, http.StatusBadRequest, "Missing client_id.")
		return
	}
	client, err := s.resolveClient(p.ClientID)
	if err != nil {
		s.oauthErrorPage(w, r, http.StatusBadRequest, "This application is not registered or its client metadata could not be verified.")
		return
	}
	if p.RedirectURI == "" || !redirectAllowed(client, p.RedirectURI) {
		s.oauthErrorPage(w, r, http.StatusBadRequest, "The redirect URI is not registered for this application.")
		return
	}

	// 2. From here the redirect_uri is trusted: protocol errors go back to it.
	if p.ResponseType != "code" {
		s.redirectError(w, r, p, "unsupported_response_type", "only response_type=code is supported")
		return
	}
	if p.CodeChallenge == "" || p.CodeChallengeMethod != "S256" {
		s.redirectError(w, r, p, "invalid_request", "PKCE code_challenge with method S256 is required")
		return
	}
	if p.Resource != "" && p.Resource != s.mcpResource() {
		s.redirectError(w, r, p, "invalid_target", "resource must be the MCP endpoint")
		return
	}
	scope, ok := normalizeScope(p.Scope)
	if !ok {
		s.redirectError(w, r, p, "invalid_scope", "unknown scope requested")
		return
	}
	p.Scope = scope

	// 3. Require a browser session. A PAT must NOT silently authorize an OAuth
	//    grant, so this is cookie-only; without one, bounce through /login and
	//    come back to the exact same authorize URL.
	user, ok := s.webSessionUser(r)
	if !ok {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(p.toURL()), http.StatusFound)
		return
	}

	if r.Method == http.MethodPost {
		s.handleConsentDecision(w, r, p, user)
		return
	}
	s.renderConsent(w, r, p, client, user)
}

// handleConsentDecision processes the Approve/Deny POST from the consent page.
func (s *Server) handleConsentDecision(w http.ResponseWriter, r *http.Request, p authorizeParams, user string) {
	// CSRF: the hidden token must be the session-derived HMAC. SameSite=Lax
	// already blocks cross-site cookie-bearing POSTs; this is defense in depth
	// (and follows the RFC's "signed token derived from the session" guidance).
	if !verifyOAuthCSRF(s.sessionSecret(), user, r.PostFormValue("csrf")) {
		s.oauthErrorPage(w, r, http.StatusForbidden, "This consent request expired or could not be verified. Please start again.")
		return
	}
	if r.PostFormValue("decision") != "approve" {
		s.redirectError(w, r, p, "access_denied", "the user denied the request")
		return
	}
	granted := grantedScopes(p.Scope, r.PostForm)
	if granted == "" { // every box off — nothing to grant
		s.redirectError(w, r, p, "access_denied", "no scopes were granted")
		return
	}
	code, err := s.Accounts.CreateOAuthCode(p.ClientID, p.RedirectURI, user, granted, p.CodeChallenge, p.Resource, oauthCodeTTL)
	if err != nil {
		s.oauthErrorPage(w, r, http.StatusInternalServerError, "Could not complete authorization.")
		return
	}
	dest := appendQuery(p.RedirectURI, map[string]string{"code": code, "state": p.State})
	http.Redirect(w, r, dest, http.StatusFound)
}

// redirectError sends an RFC 6749 §4.1.2.1 error back to a validated
// redirect_uri, preserving state.
func (s *Server) redirectError(w http.ResponseWriter, r *http.Request, p authorizeParams, code, desc string) {
	dest := appendQuery(p.RedirectURI, map[string]string{"error": code, "error_description": desc, "state": p.State})
	http.Redirect(w, r, dest, http.StatusFound)
}

// appendQuery adds params (skipping empties) to a URL's existing query string.
func appendQuery(base string, params map[string]string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ---- token endpoint -------------------------------------------------------

// handleToken is the OAuth token endpoint. It MUST accept only
// application/x-www-form-urlencoded (a JSON body is a common client bug and gets
// a clear 400, not a confusing 415), and every response carries
// Cache-Control: no-store since bodies contain fresh tokens.
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// A browser-based public client (the Markdown To playground) exchanges and
	// refreshes its tokens with fetch(), so the token endpoint — alone among the
	// AS endpoints — answers cross-origin. Never with credentials: the grant is
	// proven by the PKCE verifier in the body, never by an ambient cookie.
	if s.writeCORS(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		tokenError(w, http.StatusMethodNotAllowed, "invalid_request", "the token endpoint requires POST")
		return
	}
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	if ct != "application/x-www-form-urlencoded" {
		tokenError(w, http.StatusBadRequest, "invalid_request",
			"the token endpoint requires application/x-www-form-urlencoded (not JSON)")
		return
	}
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.tokenAuthorizationCode(w, r)
	case "refresh_token":
		s.tokenRefresh(w, r)
	default:
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant_type")
	}
}

// tokenAuthorizationCode redeems a code for a token pair after re-checking every
// binding the code carries, including the PKCE proof.
func (s *Server) tokenAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	code := r.PostForm.Get("code")
	clientID := r.PostForm.Get("client_id")
	redirectURI := r.PostForm.Get("redirect_uri")
	verifier := r.PostForm.Get("code_verifier")
	if code == "" || clientID == "" || verifier == "" {
		tokenError(w, http.StatusBadRequest, "invalid_request", "code, client_id, and code_verifier are required")
		return
	}
	rec, err := s.Accounts.ConsumeOAuthCode(code)
	if err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid, expired, or already used")
		return
	}
	if rec.ClientID != clientID {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "client_id does not match the authorization code")
		return
	}
	if !redirectURIMatches(rec.RedirectURI, redirectURI) {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the authorization request")
		return
	}
	if !verifyPKCE(verifier, rec.CodeChallenge) {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	access, refresh, err := s.Accounts.IssueOAuthTokens(rec.ClientID, rec.User, rec.Scope)
	if err != nil {
		tokenError(w, http.StatusInternalServerError, "server_error", "could not issue tokens")
		return
	}
	writeTokenResponse(w, access, refresh, rec.Scope)
}

// tokenRefresh rotates a refresh token. Scope may narrow but never widen; a
// reused refresh token revokes its whole family (handled in the store) and
// surfaces here as invalid_grant.
func (s *Server) tokenRefresh(w http.ResponseWriter, r *http.Request) {
	refresh := r.PostForm.Get("refresh_token")
	if refresh == "" {
		tokenError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	access, newRefresh, _, scope, err := s.Accounts.RotateRefresh(refresh, r.PostForm.Get("scope"))
	if err != nil {
		switch {
		case errors.Is(err, errOAuthScope):
			tokenError(w, http.StatusBadRequest, "invalid_scope", "requested scope exceeds the granted scope")
		default:
			tokenError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid, expired, or was reused")
		}
		return
	}
	writeTokenResponse(w, access, newRefresh, scope)
}

// verifyPKCE checks the S256 proof: base64url(SHA256(verifier)) == challenge,
// compared in constant time.
func verifyPKCE(verifier, challenge string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

// writeTokenResponse emits the RFC 6749 §5.1 success body.
func writeTokenResponse(w http.ResponseWriter, access, refresh, scope string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(oauthAccessTTL / time.Second),
		"refresh_token": refresh,
		"scope":         scope,
	})
}

// tokenError emits an RFC 6749 §5.2 error body with no-store.
func tokenError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc})
}

// ---- CSRF for the consent form -------------------------------------------

// oauthCSRFToken derives a per-user consent CSRF token from the session-signing
// secret. It is stable for a session (no separate storage) and unforgeable
// without the secret.
func oauthCSRFToken(secret []byte, user string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("afs-oauth-consent|" + user))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifyOAuthCSRF(secret []byte, user, token string) bool {
	if token == "" {
		return false
	}
	want := oauthCSRFToken(secret, user)
	return hmac.Equal([]byte(want), []byte(token))
}

// ---- consent + error pages ------------------------------------------------

// consentScope is one requested scope row on the consent screen.
type consentScope struct {
	Value, Label, Detail string
	Write                bool // rendered as an unchecking-allowed checkbox
}

// consentScopes is what each scope says to the human approving it, and whether
// they may untick it. The unticking rule is "anything that changes or publishes
// something is optional; anything that only looks is implied by connecting at
// all" — so the read-ish scopes render checked-and-disabled and the write-ish
// ones render as live checkboxes the user can clear to downgrade the grant.
// Rows are emitted in scopeOrder, so the screen reads the same every time.
var consentScopes = map[string]consentScope{
	scopeRead: {
		Label:  "Read your workspaces",
		Detail: "search and read files in the workspaces you own or collaborate on",
	},
	scopeWrite: {
		Label:  "Write to your workspaces",
		Detail: "commit changes (every write is an attributed, revertible git commit)",
		Write:  true,
	},
	scopeProfile: {
		Label:  "See which account you are",
		Detail: "your agentsFS username — so the app can show whose workspaces it is working in",
	},
	scopeInstancesRead: {
		Label:  "List and open your files",
		Detail: "browse your workspaces and read the files in them",
	},
	scopeInstancesWrite: {
		Label:  "Save files into your workspaces",
		Detail: "each save is an attributed git commit you can revert; a save that would overwrite someone else's change is refused, never silently applied",
		Write:  true,
	},
	scopeShareLinksCreate: {
		Label:  "Create share links",
		Detail: "publish individual files you choose at an unlisted public URL that needs no account to view",
		Write:  true,
	},
	scopeNarrationRun: {
		Label:  "Create narrated explanations",
		Detail: "run the Narrated Page research skill through your Eve agent",
	},
}

// grantedScopes reduces the REQUESTED scope string to what the user actually
// approved on the consent form. A scope survives when it was requested and
// either is not user-downgradable (the read-ish rows, rendered checked and
// disabled) or its checkbox came back ticked. Nothing outside the request can
// ever be granted, so a forged form field cannot widen a grant.
func grantedScopes(requested string, form url.Values) string {
	ticked := map[string]bool{}
	for _, v := range form["grant"] {
		ticked[v] = true
	}
	// Legacy alias: the form's write checkbox was named allow_write before the
	// scope set grew past read/write. Still honored so a consent page rendered by
	// an older build (or a bookmarked POST) keeps working.
	if form.Get("allow_write") != "" {
		ticked[scopeWrite] = true
	}
	out := map[string]bool{}
	for _, sc := range strings.Fields(requested) {
		meta, known := consentScopes[sc]
		if !known {
			continue
		}
		if !meta.Write || ticked[sc] {
			out[sc] = true
		}
	}
	return canonicalScopes(out)
}

type consentData struct {
	baseData
	ClientName   string
	RedirectHost string
	Scopes       []consentScope
	CSRF         string
	Params       authorizeParams
	Error        string
}

// renderConsent shows the approval screen: the client name, the redirect URI's
// HOSTNAME prominently (a Claude requirement and a phishing check for the user),
// and the requested scopes — read as fixed text, write as a checkbox the user
// may clear to downgrade the grant to read-only.
func (s *Server) renderConsent(w http.ResponseWriter, r *http.Request, p authorizeParams, client *OAuthClient, user string) {
	host := p.RedirectURI
	if u, err := url.Parse(p.RedirectURI); err == nil && u.Host != "" {
		host = u.Host
	}
	name := client.Name
	if name == "" {
		name = client.ID
	}
	var scopes []consentScope
	for _, sc := range scopeOrder {
		if !hasScope(p.Scope, sc) {
			continue
		}
		row := consentScopes[sc]
		row.Value = sc
		scopes = append(scopes, row)
	}
	s.renderPage(w, r, "consent", consentData{
		baseData:     baseData{User: user, Viewer: user},
		ClientName:   name,
		RedirectHost: host,
		Scopes:       scopes,
		CSRF:         oauthCSRFToken(s.sessionSecret(), user),
		Params:       p,
	})
}

// oauthErrorPage renders a plain styled error for failures that occur before a
// redirect_uri is validated (where redirecting would be unsafe). It renders to a
// buffer first so the status code and Content-Type are set correctly before any
// bytes are written (renderPage's gzip path assumes it owns the header).
func (s *Server) oauthErrorPage(w http.ResponseWriter, r *http.Request, status int, msg string) {
	viewer, _ := s.webUser(r)
	var buf bytes.Buffer
	if err := pages["consent"].ExecuteTemplate(&buf, "base", consentData{baseData: baseData{Viewer: viewer}, Error: msg}); err != nil {
		http.Error(w, msg, status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

// ---- bearer verification (the seam the MCP phase consumes) ----------------

// VerifyMCPBearer resolves a bearer credential presented to the MCP endpoint to
// its user and scopes. OAuth access tokens (scope-bearing, expiring) are tried
// first; a hub PAT (or bootstrap token) is the deliberate power-user fallback
// and carries the full scope set, matching the RFC's "PATs as MCP bearers"
// decision. The signature is fixed — the MCP phase depends on it exactly.
func (s *Server) VerifyMCPBearer(token string) (user string, scopes []string, ok bool) {
	if token == "" {
		return "", nil, false
	}
	if s.Accounts != nil {
		if u, scope, ok := s.Accounts.VerifyMCPToken(token); ok {
			return u, scopeSlice(scope), true
		}
	}
	if u, ok := s.userForToken(token); ok {
		return u, []string{scopeRead, scopeWrite}, true
	}
	return "", nil, false
}
