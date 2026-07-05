package hub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const sessionCookie = "afs_session"

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

// secret derives a stable HMAC key from the configured tokens, so browser
// sessions survive restarts without persisting a separate secret and are
// invalidated automatically if the tokens change.
func (t *TokenStore) secret() []byte {
	keys := make([]string, 0, len(t.tokens))
	for k := range t.tokens {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	h.Write([]byte("afs-hub-session-v1"))
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
	}
	return h.Sum(nil)
}

// makeSession returns a signed "<b64 user|exp>.<b64 hmac>" cookie value.
func makeSession(secret []byte, user string, exp int64) string {
	msg := user + "|" + strconv.FormatInt(exp, 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString([]byte(msg)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// parseSession verifies a session cookie and returns the user if the signature
// is valid and the token has not expired.
func parseSession(secret []byte, token string) (string, bool) {
	dot := strings.LastIndexByte(token, '.')
	if dot < 0 {
		return "", false
	}
	msg, err := base64.RawURLEncoding.DecodeString(token[:dot])
	if err != nil {
		return "", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(token[dot+1:])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(msg)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", false
	}
	parts := strings.SplitN(string(msg), "|", 2)
	if len(parts) != 2 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return parts[0], true
}
