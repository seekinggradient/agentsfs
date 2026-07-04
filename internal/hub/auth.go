package hub

import (
	"net/http"
	"strings"
)

// TokenStore maps an opaque token to the user (account / namespace) it
// authenticates. Phase 0 keeps this in memory, seeded from flags/env; it
// moves to D1 with accounts in a later phase. Tokens are secrets and must
// never be written into a repo — the CLI stores them in the OS git
// credential helper.
type TokenStore struct {
	tokens map[string]string // token -> user
}

func NewTokenStore() *TokenStore {
	return &TokenStore{tokens: map[string]string{}}
}

// Add grants token the identity of user. A user may hold several tokens.
func (t *TokenStore) Add(user, token string) {
	t.tokens[token] = user
}

// Len reports how many tokens are configured (used to warn on an open server).
func (t *TokenStore) Len() int { return len(t.tokens) }

// UserFor returns the user a token authenticates and whether it is valid.
func (t *TokenStore) UserFor(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	u, ok := t.tokens[token]
	return u, ok
}

// tokenFromRequest pulls the token from HTTP Basic auth (git's default over
// HTTP: the token is the password) or an Authorization: Bearer header.
func tokenFromRequest(r *http.Request) string {
	if _, pass, ok := r.BasicAuth(); ok && pass != "" {
		return pass
	}
	const bearer = "Bearer "
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, bearer) {
		return strings.TrimSpace(strings.TrimPrefix(h, bearer))
	}
	return ""
}
