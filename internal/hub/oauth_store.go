package hub

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// The hub is its own OAuth 2.1 authorization server (see oauth.go for the HTTP
// endpoints and the RFC that drives the exacting shape of all this). This file
// is the persistence half: the AccountStore methods that mint, rotate, and
// verify the opaque credentials that flow through that protocol — registered
// clients, single-use authorization codes, and access/refresh token families.
//
// Two disciplines are load-bearing and mirror the PAT model already in
// accounts.go: secrets are generated with crypto/rand and only ever their
// SHA-256 hash (tokenHash) is written to SQLite, so a database leak never
// yields a usable token; and refresh tokens rotate as a *family*, so a single
// replayed refresh token invalidates the whole lineage (the standard OAuth 2.1
// defense against a stolen-then-reused refresh token — RFC 6819 §5.2.2.3).

// The two scopes the MCP surface understands. afs:read covers the read tools;
// afs:write is additionally required for the commit/write path. Kept in a
// canonical order (read before write) so every stored/emitted scope string is
// byte-stable and callers can compare them directly.
const (
	scopeRead  = "afs:read"
	scopeWrite = "afs:write"
)

// Token lifetimes: a short access token so a leaked one expires fast, and a
// long rolling refresh token so a live connection rarely has to bounce the user
// back through consent. Both are the values the RFC calls for (2 h / 30 d).
const (
	oauthAccessTTL  = 2 * time.Hour
	oauthRefreshTTL = 30 * 24 * time.Hour
	oauthCodeTTL    = 10 * time.Minute
)

// OAuth store errors. The token endpoint maps these to the RFC 6749 error codes
// clients expect: a bad/expired/reused code or refresh token is invalid_grant;
// an over-broad refresh scope is invalid_scope. errOAuthTokenReuse is
// deliberately distinct from errOAuthTokenInvalid so the caller (and the tests)
// can assert the family-revocation path specifically, even though both surface
// to the client as invalid_grant.
var (
	errOAuthCodeInvalid  = errors.New("authorization code is invalid, expired, or already used")
	errOAuthTokenInvalid = errors.New("refresh token is invalid or expired")
	errOAuthTokenReuse   = errors.New("refresh token reuse detected; token family revoked")
	errOAuthScope        = errors.New("requested scope exceeds the granted scope")
)

// OAuthClient is a registered MCP client. For a DCR client (RFC 7591) the ID is
// an opaque "cli_"-prefixed random string we mint; for a CIMD client the ID is
// the https:// URL of the client's own metadata document (which we fetch and
// cache — see resolveClient). RedirectURIs is the exact allow-list a callback
// is matched against (with the RFC 8252 loopback-port exception).
type OAuthClient struct {
	ID           string
	RedirectURIs []string
	Name         string
	Created      int64  // unix seconds; for a CIMD client this doubles as the fetched-at cache stamp
	Kind         string // "dcr" | "cimd"
}

// randToken returns prefix + 24 bytes of base64url randomness. Every opaque
// secret the AS hands out (client ids, codes, tokens, family ids) is minted
// here so they share one crypto/rand source and one encoding.
func randToken(prefix string) (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// RegisterOAuthClient records a DCR (RFC 7591) public client and returns its
// minted "cli_" id. The redirect URIs are stored verbatim (already validated by
// the caller) as a JSON array so the schema stays a single TEXT column.
func (a *AccountStore) RegisterOAuthClient(name string, redirectURIs []string) (*OAuthClient, error) {
	id, err := randToken("cli_")
	if err != nil {
		return nil, err
	}
	uris, err := json.Marshal(redirectURIs)
	if err != nil {
		return nil, err
	}
	created := time.Now().Unix()
	if _, err := a.db.Exec(`INSERT INTO oauth_clients(id,redirect_uris,name,created,kind) VALUES(?,?,?,?,?)`,
		id, string(uris), strings.TrimSpace(name), created, "dcr"); err != nil {
		return nil, err
	}
	return &OAuthClient{ID: id, RedirectURIs: redirectURIs, Name: strings.TrimSpace(name), Created: created, Kind: "dcr"}, nil
}

// UpsertCIMDClient caches a client whose id is its metadata-document URL. created
// is refreshed on every upsert so resolveClient can treat it as a fetched-at
// stamp and re-fetch a stale document (the CIMD cache TTL).
func (a *AccountStore) UpsertCIMDClient(id, name string, redirectURIs []string) (*OAuthClient, error) {
	uris, err := json.Marshal(redirectURIs)
	if err != nil {
		return nil, err
	}
	created := time.Now().Unix()
	if _, err := a.db.Exec(`INSERT INTO oauth_clients(id,redirect_uris,name,created,kind) VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET redirect_uris=excluded.redirect_uris, name=excluded.name, created=excluded.created, kind=excluded.kind`,
		id, string(uris), strings.TrimSpace(name), created, "cimd"); err != nil {
		return nil, err
	}
	return &OAuthClient{ID: id, RedirectURIs: redirectURIs, Name: strings.TrimSpace(name), Created: created, Kind: "cimd"}, nil
}

// OAuthClient looks up a registered/cached client by id.
func (a *AccountStore) OAuthClient(id string) (*OAuthClient, bool) {
	var c OAuthClient
	var uris string
	if err := a.db.QueryRow(`SELECT id,redirect_uris,name,created,kind FROM oauth_clients WHERE id=?`, id).
		Scan(&c.ID, &uris, &c.Name, &c.Created, &c.Kind); err != nil {
		return nil, false
	}
	if err := json.Unmarshal([]byte(uris), &c.RedirectURIs); err != nil {
		return nil, false
	}
	return &c, true
}

// CreateOAuthCode mints a single-use authorization code bound to everything the
// token exchange must re-verify: the client, the exact redirect URI, the user
// who consented, the granted scope, the PKCE challenge, and (RFC 8707) the
// resource the code was issued for. Only the code's hash is stored.
func (a *AccountStore) CreateOAuthCode(clientID, redirectURI, user, scope, codeChallenge, resource string, ttl time.Duration) (string, error) {
	code, err := randToken("afsmcpc_")
	if err != nil {
		return "", err
	}
	if _, err := a.db.Exec(`INSERT INTO oauth_codes(code_hash,client_id,redirect_uri,user,scope,code_challenge,resource,expires,used)
		VALUES(?,?,?,?,?,?,?,?,0)`,
		tokenHash(code), clientID, redirectURI, user, scope, codeChallenge, resource, time.Now().Add(ttl).Unix()); err != nil {
		return "", err
	}
	return code, nil
}

// OAuthCode is a consumed authorization code's bound context.
type OAuthCode struct {
	ClientID, RedirectURI, User, Scope, CodeChallenge, Resource string
	Expires                                                     int64
}

// ConsumeOAuthCode atomically redeems a code exactly once. A missing, expired,
// or already-used code returns errOAuthCodeInvalid — codes are single-use, so a
// replay (double token exchange) is rejected rather than issuing a second token
// pair. The UPDATE...WHERE used=0 guard makes the redemption atomic even under
// the store's single serialized connection.
func (a *AccountStore) ConsumeOAuthCode(code string) (*OAuthCode, error) {
	h := tokenHash(code)
	var c OAuthCode
	var used int
	if err := a.db.QueryRow(`SELECT client_id,redirect_uri,user,scope,code_challenge,resource,expires,used
		FROM oauth_codes WHERE code_hash=?`, h).
		Scan(&c.ClientID, &c.RedirectURI, &c.User, &c.Scope, &c.CodeChallenge, &c.Resource, &c.Expires, &used); err != nil {
		return nil, errOAuthCodeInvalid
	}
	if used != 0 || time.Now().Unix() > c.Expires {
		return nil, errOAuthCodeInvalid
	}
	res, err := a.db.Exec(`UPDATE oauth_codes SET used=1 WHERE code_hash=? AND used=0`, h)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, errOAuthCodeInvalid
	}
	return &c, nil
}

// IssueOAuthTokens mints a fresh access+refresh pair in a brand-new rotation
// family (the family a subsequent refresh will rotate within).
func (a *AccountStore) IssueOAuthTokens(clientID, user, scope string) (access, refresh string, err error) {
	family, err := randToken("fam_")
	if err != nil {
		return "", "", err
	}
	return a.issueTokenPair(family, clientID, user, scope)
}

// issueTokenPair inserts an access + refresh token sharing a family. Access
// tokens are the bearer credential VerifyMCPToken accepts; refresh tokens are
// only ever redeemed at the token endpoint (never accepted as a bearer).
func (a *AccountStore) issueTokenPair(family, clientID, user, scope string) (access, refresh string, err error) {
	if access, err = randToken("afsmcp_"); err != nil {
		return "", "", err
	}
	if refresh, err = randToken("afsmcpr_"); err != nil {
		return "", "", err
	}
	now := time.Now()
	if _, err = a.db.Exec(`INSERT INTO oauth_tokens(token_hash,kind,family,client_id,user,scope,expires,revoked) VALUES(?,?,?,?,?,?,?,0)`,
		tokenHash(access), "access", family, clientID, user, scope, now.Add(oauthAccessTTL).Unix()); err != nil {
		return "", "", err
	}
	if _, err = a.db.Exec(`INSERT INTO oauth_tokens(token_hash,kind,family,client_id,user,scope,expires,revoked) VALUES(?,?,?,?,?,?,?,0)`,
		tokenHash(refresh), "refresh", family, clientID, user, scope, now.Add(oauthRefreshTTL).Unix()); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// RotateRefresh redeems a refresh token for a new access+refresh pair in the
// same family, consuming the old refresh so it can never be used twice. Three
// failure modes matter:
//
//   - unknown/expired refresh → errOAuthTokenInvalid (invalid_grant).
//   - a refresh already consumed or revoked → this is a *reuse*: either the
//     legitimate holder replayed (harmless but disallowed) or an attacker is
//     racing the real client with a stolen copy. We cannot tell which, so we
//     revoke the ENTIRE family and return errOAuthTokenReuse. This is the OAuth
//     2.1 rotation-with-reuse-detection guarantee.
//   - a requested scope not fully contained in the granted scope → errOAuthScope
//     (invalid_scope). Scope may narrow on refresh, never widen.
func (a *AccountStore) RotateRefresh(refresh, requestedScope string) (access, newRefresh, user, scope string, err error) {
	h := tokenHash(refresh)
	var kind, family, clientID string
	var expires int64
	var revoked int
	if e := a.db.QueryRow(`SELECT kind,family,client_id,user,scope,expires,revoked FROM oauth_tokens WHERE token_hash=?`, h).
		Scan(&kind, &family, &clientID, &user, &scope, &expires, &revoked); e != nil || kind != "refresh" {
		return "", "", "", "", errOAuthTokenInvalid
	}
	if revoked != 0 {
		// Reuse of a consumed/revoked refresh token: burn the whole lineage.
		_ = a.RevokeOAuthFamily(family)
		return "", "", "", "", errOAuthTokenReuse
	}
	if time.Now().Unix() > expires {
		a.db.Exec(`UPDATE oauth_tokens SET revoked=1 WHERE token_hash=?`, h)
		return "", "", "", "", errOAuthTokenInvalid
	}
	newScope := scope
	if strings.TrimSpace(requestedScope) != "" {
		var read, write bool
		for _, sc := range strings.Fields(requestedScope) {
			switch sc {
			case scopeRead:
				read = true
			case scopeWrite:
				write = true
			default:
				return "", "", "", "", errOAuthScope
			}
			if !hasScope(scope, sc) { // requesting a scope never granted == widening
				return "", "", "", "", errOAuthScope
			}
		}
		newScope = joinScopes(read, write)
	}
	// Consume the presented refresh token first, atomically. If it was consumed
	// out from under us by a concurrent rotation, treat that as reuse too.
	res, err := a.db.Exec(`UPDATE oauth_tokens SET revoked=1 WHERE token_hash=? AND revoked=0`, h)
	if err != nil {
		return "", "", "", "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		_ = a.RevokeOAuthFamily(family)
		return "", "", "", "", errOAuthTokenReuse
	}
	access, newRefresh, err = a.issueTokenPair(family, clientID, user, newScope)
	if err != nil {
		return "", "", "", "", err
	}
	return access, newRefresh, user, newScope, nil
}

// RevokeOAuthFamily revokes every token (access and refresh) in a rotation
// family, cutting off a connection whose refresh chain was compromised.
func (a *AccountStore) RevokeOAuthFamily(family string) error {
	_, err := a.db.Exec(`UPDATE oauth_tokens SET revoked=1 WHERE family=?`, family)
	return err
}

// VerifyMCPToken resolves an OAuth *access* token to its user and granted scope.
// Refresh tokens are rejected here (kind must be "access"): they are redemption
// credentials, not bearer credentials for the resource. Expired or revoked
// tokens fail closed.
func (a *AccountStore) VerifyMCPToken(token string) (user, scope string, ok bool) {
	if token == "" {
		return "", "", false
	}
	var kind string
	var expires int64
	var revoked int
	if err := a.db.QueryRow(`SELECT kind,user,scope,expires,revoked FROM oauth_tokens WHERE token_hash=?`, tokenHash(token)).
		Scan(&kind, &user, &scope, &expires, &revoked); err != nil {
		return "", "", false
	}
	if kind != "access" || revoked != 0 || time.Now().Unix() > expires {
		return "", "", false
	}
	return user, scope, true
}

// --- scope helpers ---------------------------------------------------------
//
// Scopes are held as a canonical space-separated string, always read-before-
// write, so every path (default, downgrade, intersect) yields a byte-stable
// value that callers and tests compare directly.

func hasScope(scope, want string) bool {
	for _, sc := range strings.Fields(scope) {
		if sc == want {
			return true
		}
	}
	return false
}

// scopeSlice splits a scope string into its individual scope values.
func scopeSlice(scope string) []string { return strings.Fields(scope) }

// joinScopes renders the canonical string for a read/write pair.
func joinScopes(read, write bool) string {
	var out []string
	if read {
		out = append(out, scopeRead)
	}
	if write {
		out = append(out, scopeWrite)
	}
	return strings.Join(out, " ")
}

// normalizeScope validates a requested scope string and returns its canonical
// form. An empty request defaults to both scopes (the full MCP surface). Any
// unrecognized scope value makes ok=false so authorize can reject invalid_scope.
func normalizeScope(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return joinScopes(true, true), true
	}
	var read, write bool
	for _, sc := range strings.Fields(raw) {
		switch sc {
		case scopeRead:
			read = true
		case scopeWrite:
			write = true
		default:
			return "", false
		}
	}
	return joinScopes(read, write), true
}
