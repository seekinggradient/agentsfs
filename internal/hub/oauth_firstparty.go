package hub

import (
	"encoding/json"
	"strings"
	"time"
)

// First-party OAuth clients are apps in this product family that ship with a
// STABLE, pre-registered client_id rather than discovering one at runtime. A
// static browser app cannot keep a client secret and cannot re-register itself
// on every deploy (DCR mints a fresh random "cli_" id each time, and a Client ID
// Metadata Document would make every login depend on that app's own hosting
// being up), so the id, name, and redirect allow-list are declared HERE in code
// and reconciled into the store on open. Code is therefore the source of truth:
// an operator cannot drift a first-party client's redirect list in the database,
// and a fresh volume comes up already knowing these apps.
//
// Everything else about them is an ordinary public client: authorization code +
// PKCE-S256, no secret, exact redirect matching (with the RFC 8252 loopback-port
// exception), and the same consent screen every other client gets.

// firstPartyKind marks a client seeded from this file, distinct from the "dcr"
// and "cimd" kinds resolveClient already knows. It exists so an operator (and
// the tests) can tell a declared client from a self-registered one.
const firstPartyKind = "first-party"

// markdownToClientID is markdownto.ai's stable client_id. It is the app's own
// domain — self-describing in logs and on the consent screen, and it can never
// collide with a DCR id ("cli_"-prefixed) or a CIMD id (an https:// URL, which
// is how resolveClient tells the two apart).
const markdownToClientID = "markdownto.ai"

// firstPartyClient is one declared app: the client record plus the browser
// origins it may call the JSON API and the token endpoint from.
type firstPartyClient struct {
	ID           string
	Name         string
	RedirectURIs []string
	// Origins are the CORS origins this app's pages are served from. They are
	// derived from, but not identical to, the redirect URIs: a redirect URI is a
	// full path ("https://markdownto.ai/app/") while an Origin is scheme+host.
	Origins []string
}

// firstPartyClients is the whole registry. Adding an app here is the entire
// registration step — OpenAccounts reconciles it on the next start.
var firstPartyClients = []firstPartyClient{{
	ID: markdownToClientID,
	// The name the consent screen renders: "Markdown To wants to connect to your
	// knowledge bases as <you>".
	Name: "Markdown To",
	RedirectURIs: []string{
		"https://markdownto.ai/app/",
		"https://www.markdownto.ai/app/",
		// Development. Only the scheme, host, and path have to match: RFC 8252
		// lets a loopback redirect vary its PORT (redirectURIMatches), so one
		// entry per loopback host covers every dev server port.
		"http://localhost:8080/app/",
		"http://127.0.0.1:8080/app/",
	},
	Origins: []string{
		"https://markdownto.ai",
		"https://www.markdownto.ai",
	},
}}

// seedFirstPartyClients reconciles the declared registry into oauth_clients. It
// is an upsert, so a redirect URI added in code lands on the next start and a
// row edited by hand is put back — the code is the registration.
func (a *AccountStore) seedFirstPartyClients() error {
	for _, c := range firstPartyClients {
		uris, err := json.Marshal(c.RedirectURIs)
		if err != nil {
			return err
		}
		if _, err := a.db.Exec(`INSERT INTO oauth_clients(id,redirect_uris,name,created,kind) VALUES(?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET redirect_uris=excluded.redirect_uris, name=excluded.name, kind=excluded.kind`,
			c.ID, string(uris), c.Name, time.Now().Unix(), firstPartyKind); err != nil {
			return err
		}
	}
	return nil
}

// firstPartyOrigins is the built-in browser-origin allow-list for the JSON API
// and the token endpoint (see apiv1 corsOrigin). It is the registry's Origins
// flattened, plus every http loopback origin, which is admitted dynamically
// rather than listed: a dev server's port is unknowable in advance, and CORS
// here never carries credentials — a page still needs a bearer token, which a
// localhost page that has one could already use from anywhere.
func firstPartyOrigins() []string {
	var out []string
	for _, c := range firstPartyClients {
		out = append(out, c.Origins...)
	}
	return out
}

// isLoopbackOrigin reports whether an Origin header names an http loopback host,
// with any port. Mirrors isLoopbackHost's allow-list, applied to an origin
// rather than a redirect URI.
func isLoopbackOrigin(origin string) bool {
	rest, ok := strings.CutPrefix(origin, "http://")
	if !ok {
		return false
	}
	host := rest
	if i := strings.LastIndexByte(rest, ':'); i >= 0 {
		host = rest[:i]
	}
	return isLoopbackHost(strings.Trim(host, "[]"))
}
