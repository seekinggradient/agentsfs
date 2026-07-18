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
// (shell, assets, and /agent/eve/v1/* API/stream) lives under /agent/ — and the
// app serves those same paths under /agent upstream too, so this proxy forwards
// the path UN-stripped (stripping /agent would 404 the basePath-aware app). The
// workflow-world callback prefix
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

// agentUserPAT returns the long-lived per-user agent PAT to inject as X-AFS-PAT,
// minting and persisting it on first use. It returns "" (injection skipped) when
// the PAT store or the accounts store is not configured (unit tests, or a
// misconfigured deployment) so the signed-identity handoff still works on its
// own and the Eve app can fall back to its env PAT.
func (m *AgentManager) agentUserPAT(user string) string {
	if m == nil || m.PATStore == nil || m.Accounts == nil || user == "" {
		return ""
	}
	tok, err := m.PATStore.GetOrMint(user, func() (string, error) {
		return m.Accounts.CreatePAT(user, agentUserPATName)
	})
	if err != nil {
		m.logf("eve: mint agent PAT for %s: %v", user, err)
		return ""
	}
	return tok
}

// EveProxy reverse-proxies an already-authenticated Hub request to the hosted
// Eve upstream, forwarding the incoming path UN-STRIPPED. The Eve app runs with
// basePath "/agent" (docs/eve-hub-integration.md), so its entire browser surface
// — shell, /agent/_next/* assets, and the /agent/eve/v1/* API/stream — is served
// under "/agent" on the upstream too; stripping the prefix here would 404 every
// request against the basePath-aware app. The top-level /.well-known/workflow/*
// callback prefix is likewise forwarded unchanged. Any base path on EveURL (a
// mount prefix) is still joined ahead of the forwarded path.
//
// It injects the signed identity handoff and the per-user agent PAT (always
// stripping any inbound copy of those headers first, so a client can never spoof
// another user or smuggle a foreign PAT), drops the Hub session cookie so it
// never leaves the Hub, preserves NDJSON/SSE streaming (FlushInterval -1), and
// hardens the response.
func (m *AgentManager) EveProxy(w http.ResponseWriter, r *http.Request, user string) {
	upstreamPath := r.URL.Path
	// Deployed layout reality (verified live): the Next shell/assets/api live
	// UNDER /agent (basePath), but eve's own service is routed at the deployment
	// ROOT — withEve does not move /eve/v1/* under basePath on Vercel. Map that
	// one family down; everything else stays un-stripped. Also normalize the
	// bare "/agent/" to "/agent" ourselves: Next 308s the trailing slash and the
	// response hardener drops Location, which would dead-end the browser.
	if strings.HasPrefix(upstreamPath, "/agent/eve/") {
		upstreamPath = strings.TrimPrefix(upstreamPath, "/agent")
	} else if upstreamPath == "/agent/" {
		upstreamPath = "/agent"
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
	// The long-lived per-user agent PAT for hub-client calls the Eve app makes on
	// this user's behalf. Resolved once per request (like the expiry) so a retry
	// reuses it and the mint/lookup happens outside the Director's hot path.
	pat := m.agentUserPAT(user)

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
			// crafted request cannot impersonate another user or supply its own
			// PAT to the upstream.
			req.Header.Del("X-AFS-User")
			req.Header.Del("X-AFS-Signature")
			req.Header.Del("X-AFS-Expiry")
			req.Header.Del("X-AFS-PAT")
			req.Header.Set("X-AFS-User", user)
			req.Header.Set("X-AFS-Expiry", strconv.FormatInt(expiry, 10))
			req.Header.Set("X-AFS-Signature", signature)
			// Inject the per-user agent PAT when available. When absent (no store
			// configured) the header is simply omitted and the Eve app falls back
			// to its env PAT, so the identity handoff still stands alone.
			if pat != "" {
				req.Header.Set("X-AFS-PAT", pat)
			}
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
	// The eve session protocol returns its cursor in x-eve-* response headers
	// (session id on create; the client persists them). They carry no cookies,
	// no origin authority, and no redirect semantics — pass them through, or the
	// chat client can never resume the session it just created (verified live:
	// dropping them broke session pickup through the Hub).
	for name, values := range resp.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-eve-") {
			for _, v := range values {
				headers.Add(name, v)
			}
		}
	}

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
