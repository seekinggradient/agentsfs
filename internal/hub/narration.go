package hub

import (
	"net/http"
)

// narrationResearchPath is the only browser-token-accessible Eve route. The
// broader /agent/* surface remains protected by the Hub's own session cookie.
const narrationResearchPath = "/api/narration/research"

// handleNarrationResearch authenticates a Narrated Page OAuth token, enforces
// its purpose-built scope, then hands the request to the existing Eve proxy.
// The proxy strips the browser credential before injecting a short-lived Hub
// HMAC identity and the user's existing agent PAT.
func (s *Server) handleNarrationResearch(w http.ResponseWriter, r *http.Request) {
	if s.writeCORS(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.Accounts == nil {
		apiError(w, http.StatusNotFound, "accounts are not enabled on this hub")
		return
	}
	c, ok := s.apiV1Caller(r)
	if !ok || c.ClientID == "" { // OAuth only: a long-lived PAT is not this grant.
		w.Header().Set("WWW-Authenticate", `Bearer realm="agentsfs hub"`)
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !requireScope(w, c, scopeNarrationRun) {
		return
	}
	if s.Agent == nil || !s.Agent.EveMode() {
		apiError(w, http.StatusServiceUnavailable, "the Eve narration service is not configured")
		return
	}

	// Eve's Next app is mounted under /agent. Clone the request so the public
	// browser URL remains purpose-specific while the trusted upstream receives
	// the route its basePath declares. Authorization is removed here as defense
	// in depth; EveProxy also rebuilds the privileged X-AFS-* identity headers.
	upstream := r.Clone(r.Context())
	upstream.URL.Path = "/agent/api/narration/research"
	upstream.URL.RawPath = ""
	upstream.Header = r.Header.Clone()
	upstream.Header.Del("Authorization")
	upstream.Header.Del("Cookie")
	upstream.Header.Del("Origin")
	upstream.Header.Del("Referer")
	upstream.Header.Del("Sec-Fetch-Site")
	upstream.Header.Del("Sec-Fetch-Mode")
	upstream.Header.Del("Sec-Fetch-Dest")
	s.Agent.EveProxy(w, upstream, c.User)
}
