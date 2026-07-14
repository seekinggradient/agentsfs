package hub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httputil"
	neturl "net/url"
	"strconv"
	"strings"
	"time"
)

// Hosted-Eve upstream mode — a flag-gated sibling of the sprite proxy.
//
// When HUB_EVE_AGENT_URL is set, the Hub stops provisioning per-user sprites and
// instead reverse-proxies /agent/* to a single trusted, Vercel-hosted Eve app
// (docs/eve-hosting.md, Decision 5: "Hub reverse-proxies a Vercel-hosted Eve
// app"). The Hub stays the identity + git home; the Eve app is our own
// deployment (not a user-controlled VM), so the response-hardening pass is a
// deliberately lighter cousin of hardenAgentProxyResponse: it strips
// origin-affecting headers and forces no-store/nosniff, but preserves the
// upstream's own Content-Type instead of forcing a per-route allowlist, because
// the trusted upstream legitimately serves many content types (HTML shell,
// framework assets, application/json, and application/x-ndjson event streams).
//
// Path mapping (evidence in docs/eve-hub-integration.md, derived from the eve
// client source): the Eve browser client sends every request through a
// configurable base URL joined with a fixed "/eve/v1/*" route. With the Eve app
// configured basePath=/agent + client host=/agent, the browser's ENTIRE surface
// (shell, assets, and /agent/eve/v1/* API/stream) lives under /agent/, which
// this proxy strips to the upstream root. The workflow-world callback prefix
// /.well-known/workflow/* is server-to-server (never a browser path) and, in the
// Vercel-hosted topology, is delivered by Vercel Workflow directly to the eve
// deployment — it does not traverse the Hub. It is nonetheless forwarded here
// (un-stripped, same webUser gate) to honor eve's documented reverse-proxy
// contract and to keep the self-hosted-Eve-behind-the-Hub fallback viable.

// eveIdentityTTL is how far out the signed identity handoff's expiry is set. The
// Eve app rejects a handoff whose expiry is in the past (allowing a small clock
// skew); 5 minutes is long enough to absorb skew and a slow request without
// letting a captured header be replayed for long.
const eveIdentityTTL = 5 * time.Minute

// EveMode reports whether the hosted-Eve upstream is configured. When true the
// sprite provisioning/proxy path is bypassed entirely for /agent/*.
func (m *AgentManager) EveMode() bool {
	return m != nil && m.EveURL != ""
}

// eveSignature is the identity handoff the Eve app verifies: it authenticates
// that the Hub (holder of the shared HMAC key) vouches for `user` until
// `expiry`. Construction is hex(HMAC-SHA256(secret, user + "|" + expiryUnix)) —
// documented verbatim in docs/eve-hub-integration.md so the Eve side can
// reproduce it exactly.
func eveSignature(secret, user string, expiry int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(user + "|" + strconv.FormatInt(expiry, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// EveProxy reverse-proxies an already-authenticated Hub request to the hosted
// Eve upstream. stripPrefix, when non-empty, is removed from the request path
// before forwarding (the /agent → upstream-root mapping); when empty the path is
// forwarded unchanged (the top-level /.well-known/workflow/* callback prefix).
//
// It injects the signed identity handoff (always stripping any inbound copy of
// those headers first, so a client can never spoof another user), drops the Hub
// session cookie so it never leaves the Hub, preserves NDJSON/SSE streaming
// (FlushInterval -1), and hardens the response.
func (m *AgentManager) EveProxy(w http.ResponseWriter, r *http.Request, user, stripPrefix string) {
	upstreamPath := r.URL.Path
	if stripPrefix != "" {
		p, ok := relativeAgentPath(r.URL.Path, stripPrefix)
		if !ok {
			http.NotFound(w, r)
			return
		}
		upstreamPath = p
	}
	target, err := neturl.Parse(m.EveURL)
	if err != nil {
		http.Error(w, "bad eve url", http.StatusInternalServerError)
		return
	}
	// Compute the handoff once per request. time.Now is evaluated here (not in
	// the Director) so a retry inside ReverseProxy reuses the same expiry.
	expiry := time.Now().Add(eveIdentityTTL).Unix()
	signature := eveSignature(m.EveSecret, user, expiry)

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			// Honor a base path on HUB_EVE_AGENT_URL (e.g. a mount prefix on the
			// upstream) by joining it ahead of the stripped path.
			req.URL.Path = singleJoiningSlash(target.Path, upstreamPath)
			req.URL.RawPath = ""
			// Keep the hop uncompressed: the response hardener rebuilds headers,
			// and forwarding an encoding we don't preserve would corrupt the body.
			req.Header.Set("Accept-Encoding", "identity")
			// The Hub session cookie authenticates the browser to the Hub only;
			// it must never reach the upstream.
			req.Header.Del("Cookie")
			// ALWAYS strip inbound identity headers before injecting ours, so a
			// crafted request cannot impersonate another user to the upstream.
			req.Header.Del("X-AFS-User")
			req.Header.Del("X-AFS-Signature")
			req.Header.Del("X-AFS-Expiry")
			req.Header.Set("X-AFS-User", user)
			req.Header.Set("X-AFS-Expiry", strconv.FormatInt(expiry, 10))
			req.Header.Set("X-AFS-Signature", signature)
		},
		FlushInterval:  -1, // flush each chunk immediately so NDJSON/SSE streams
		ModifyResponse: hardenEveProxyResponse,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if m.Log != nil {
				m.Log.Printf("eve proxy %s: %v", m.EveURL, err)
			}
			http.Error(w, "agent unreachable", http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// hardenEveProxyResponse keeps the trusted Eve upstream from acquiring ambient
// authority over the Hub origin. It is a lighter cousin of
// hardenAgentProxyResponse (see the file-level comment for why): the upstream is
// our own deployment serving many legitimate content types, so this pass
// PRESERVES the upstream Content-Type rather than forcing one, but still rebuilds
// the header map from scratch to drop Set-Cookie, CORS, Location/Refresh,
// Clear-Site-Data, and other origin-affecting headers, and forces
// no-store + nosniff. FlushInterval -1 plus X-Accel-Buffering:no keep the
// NDJSON/SSE stream unbuffered end to end.
func hardenEveProxyResponse(resp *http.Response) error {
	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	contentType := resp.Header.Get("Content-Type")

	headers := make(http.Header)
	if encoding == "gzip" || encoding == "br" || encoding == "deflate" {
		headers.Set("Content-Encoding", encoding)
	}
	// Preserve the upstream's own media type (application/json, the app shell's
	// text/html, framework assets, and application/x-ndjson streams all pass
	// through) while still asserting nosniff so a mislabeled body can't be
	// reinterpreted on the Hub origin.
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	headers.Set("Cache-Control", "no-store")
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("Cross-Origin-Resource-Policy", "same-origin")
	headers.Set("X-Frame-Options", "DENY")
	// Never let a proxied stream sit in an intermediary buffer.
	headers.Set("X-Accel-Buffering", "no")

	resp.Header = headers
	resp.Trailer = nil
	resp.ContentLength = -1
	return nil
}

// singleJoiningSlash joins a base path and a suffix with exactly one slash
// between them (the standard httputil idiom, inlined to avoid pulling the whole
// NewSingleHostReverseProxy machinery). base is "" or "/" for a root-mounted
// upstream.
func singleJoiningSlash(base, suffix string) string {
	if base == "" || base == "/" {
		return suffix
	}
	baseSlash := strings.HasSuffix(base, "/")
	sufSlash := strings.HasPrefix(suffix, "/")
	switch {
	case baseSlash && sufSlash:
		return base + suffix[1:]
	case !baseSlash && !sufSlash:
		return base + "/" + suffix
	}
	return base + suffix
}
