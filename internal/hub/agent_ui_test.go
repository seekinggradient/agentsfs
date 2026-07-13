package hub

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgentRouteAllowlist(t *testing.T) {
	type routeCase struct {
		path, allow, contentType string
		kind                     agentRouteKind
	}
	cases := []routeCase{
		{path: "/", allow: "GET, HEAD", kind: agentRouteUI},
		{path: "/index.html", allow: "GET, HEAD", kind: agentRouteUI},
		{path: "/app.js", allow: "GET, HEAD", kind: agentRouteUI},
		{path: "/lib.js", allow: "GET, HEAD", kind: agentRouteUI},
		{path: "/styles.css", allow: "GET, HEAD", kind: agentRouteUI},
		{path: "/preview", allow: "GET, HEAD", kind: agentRoutePreview},
		{path: "/preview/", allow: "GET, HEAD", kind: agentRoutePreview},
		{path: "/api/health", allow: "GET", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
		{path: "/api/config", allow: "GET", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
		{path: "/api/instances", allow: "GET", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
		{path: "/api/instance", allow: "POST", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
		{path: "/api/focus", allow: "POST", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
		{path: "/api/chat", allow: "POST", kind: agentRouteAPI, contentType: "text/event-stream; charset=utf-8"},
		{path: "/api/tools/call", allow: "POST", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
		{path: "/api/realtime/token", allow: "POST", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
		{path: "/api/review/commit", allow: "POST", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
		{path: "/api/review/discard", allow: "POST", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
		{path: "/api/conversations", allow: "GET, POST", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
		{path: "/api/conversations/12345678", allow: "GET, DELETE", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
		{path: "/api/conversations/123e4567-e89b-12d3-a456-426614174000", allow: "GET, DELETE", kind: agentRouteAPI, contentType: "application/json; charset=utf-8"},
	}
	methods := []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(strings.TrimPrefix(tc.path, "/"), "/", "_")+"_routes", func(t *testing.T) {
			route, ok := classifyAgentPath(tc.path)
			if !ok {
				t.Fatalf("classifyAgentPath(%q) rejected an active route", tc.path)
			}
			if route.kind != tc.kind || route.allow != tc.allow || route.contentType != tc.contentType {
				t.Fatalf("classifyAgentPath(%q) = %#v", tc.path, route)
			}
			for _, method := range methods {
				want := strings.Contains(", "+tc.allow+", ", ", "+method+", ")
				if got := route.allows(method); got != want {
					t.Errorf("%s %s allowed = %v, want %v", method, tc.path, got, want)
				}
			}
		})
	}

	for _, p := range []string{
		"", "/app.js.map", "/previewevil", "/previews/", "/preview/index.html", "/preview/evil.js", "/api", "/api/chat/",
		"/api/unknown", "/api/conversations/", "/api/conversations/short",
		"/api/conversations/12345678/extra", "/api/conversations/../../tools/call",
		"/api/review", "/api/review/", "/api/review/commit/", "/api/review/unknown",
	} {
		if route, ok := classifyAgentPath(p); ok {
			t.Errorf("classifyAgentPath(%q) unexpectedly allowed %#v", p, route)
		}
	}
}

func TestRelativeAgentPathIsBoundaryAware(t *testing.T) {
	for _, tc := range []struct {
		full, want string
		ok         bool
	}{
		{full: "/agent", want: "/", ok: true},
		{full: "/agent/", want: "/", ok: true},
		{full: "/agent/api/config", want: "/api/config", ok: true},
		{full: "/agent/previewevil", want: "/previewevil", ok: true},
		{full: "/agentish/api/config", ok: false},
		{full: "/other/agent/api/config", ok: false},
	} {
		got, ok := relativeAgentPath(tc.full, "/agent")
		if ok != tc.ok || got != tc.want {
			t.Errorf("relativeAgentPath(%q) = %q, %v; want %q, %v", tc.full, got, ok, tc.want, tc.ok)
		}
	}
}

func TestEmbeddedAgentUIAssetsAreServedByHub(t *testing.T) {
	paths := make([]string, 0, len(agentUIBundleFiles))
	for p := range agentUIBundleFiles {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		asset := agentUIAssets[p]
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+"_"+strings.ReplaceAll(p, "/", "_"), func(t *testing.T) {
				req := httptest.NewRequest(method, "https://hub.example/agent"+p, nil)
				recorder := httptest.NewRecorder()
				serveAgentUI(recorder, req, p)
				res := recorder.Result()
				defer res.Body.Close()
				if res.StatusCode != http.StatusOK {
					t.Fatalf("status = %d", res.StatusCode)
				}
				if got := res.Header.Get("Content-Type"); got != asset.contentType {
					t.Errorf("Content-Type = %q, want %q", got, asset.contentType)
				}
				if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
					t.Errorf("X-Content-Type-Options = %q", got)
				}
				body, err := io.ReadAll(res.Body)
				if err != nil {
					t.Fatal(err)
				}
				if method == http.MethodHead {
					if len(body) != 0 {
						t.Fatalf("HEAD returned %d body bytes", len(body))
					}
				} else if p != "/" && p != "/index.html" && string(body) != string(asset.body) {
					t.Fatal("served asset differs from immutable embedded bundle bytes")
				}
				if p == "/" || p == "/index.html" {
					assertAgentUICSP(t, res.Header.Get("Content-Security-Policy"), body, asset.body)
				} else if got := res.Header.Get("Content-Security-Policy"); got != "" {
					t.Errorf("non-document asset has CSP %q", got)
				}
			})
		}
	}
}

func assertAgentUICSP(t *testing.T, csp string, served, original []byte) {
	t.Helper()
	for _, want := range []string{
		"default-src 'none'", "'strict-dynamic'", "style-src 'self' 'unsafe-inline'",
		"connect-src 'self' https://api.openai.com", "frame-src 'none'", "worker-src 'none'",
		"object-src 'none'", "base-uri 'none'", "frame-ancestors 'self'", "form-action 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("agent UI CSP = %q, missing %q", csp, want)
		}
	}
	scriptDirective := strings.Split(strings.SplitN(csp, "script-src ", 2)[1], ";")[0]
	if strings.Contains(scriptDirective, "'self'") || strings.Contains(scriptDirective, "'unsafe-inline'") {
		t.Fatalf("agent UI script CSP trusts ambient origin or inline script: %q", scriptDirective)
	}
	nonceMatch := regexp.MustCompile(`script-src 'nonce-([^']+)' 'strict-dynamic'`).FindStringSubmatch(csp)
	if len(nonceMatch) != 2 {
		t.Fatalf("agent UI CSP has no strict nonce: %q", csp)
	}
	if _, err := base64.RawStdEncoding.DecodeString(nonceMatch[1]); err != nil {
		t.Fatalf("agent UI nonce is not base64: %q", nonceMatch[1])
	}
	if len(served) == 0 {
		return // HEAD: the CSP still carries a fresh, syntactically valid nonce.
	}
	wantAttr := []byte(` nonce="` + nonceMatch[1] + `"`)
	if got, want := bytes.Count(served, wantAttr), bytes.Count(original, []byte("<script")); got != want || want == 0 {
		t.Fatalf("nonce attribute count = %d, want %d", got, want)
	}
	if restored := bytes.ReplaceAll(served, wantAttr, nil); !bytes.Equal(restored, original) {
		t.Fatal("Hub changed embedded index bytes beyond adding trusted script nonces")
	}
}

func TestAgentUIETagRevalidation(t *testing.T) {
	asset := agentUIAssets["/app.js"]
	cacheReq := httptest.NewRequest(http.MethodGet, "https://hub.example/agent/app.js", nil)
	cacheRecorder := httptest.NewRecorder()
	serveAgentUI(cacheRecorder, cacheReq, "/app.js")
	if got := cacheRecorder.Header().Get("Cache-Control"); got != "private, max-age=300, stale-while-revalidate=86400" {
		t.Fatalf("Cache-Control = %q", got)
	}

	req := httptest.NewRequest(http.MethodGet, "https://hub.example/agent/app.js", nil)
	req.Header.Set("If-None-Match", asset.etag)
	recorder := httptest.NewRecorder()
	serveAgentUI(recorder, req, "/app.js")
	if recorder.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Fatal("304 response included a body")
	}
}

func TestEmbeddedAgentUIRetriesInitialConfig(t *testing.T) {
	app := string(agentUIAssets["/app.js"].body)
	for _, want := range []string{"loadInitialConfig", "waking agent · reconnecting…", "offline · waiting for connection…"} {
		if !strings.Contains(app, want) {
			t.Fatalf("embedded agent app is missing config recovery marker %q", want)
		}
	}
}

func TestAgentProxyDenyByDefaultNeverContactsSprite(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	m := NewAgentManager("sprite-token", "openai-key", "", "", nil, nil)

	for _, tc := range []struct {
		method, path, allow string
		status              int
	}{
		{method: http.MethodGet, path: "/agent/app.js", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/agent/previewevil", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/agent/preview/evil.js", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/agent/api/unknown", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/agent/api/chat", status: http.StatusMethodNotAllowed, allow: "POST"},
		{method: http.MethodPost, path: "/agent/preview/", status: http.StatusMethodNotAllowed, allow: "GET, HEAD"},
	} {
		recorder := httptest.NewRecorder()
		m.Proxy(recorder, httptest.NewRequest(tc.method, tc.path, nil), upstream.URL, "/agent")
		if recorder.Code != tc.status {
			t.Errorf("%s %s status = %d, want %d", tc.method, tc.path, recorder.Code, tc.status)
		}
		if got := recorder.Header().Get("Allow"); got != tc.allow {
			t.Errorf("%s %s Allow = %q, want %q", tc.method, tc.path, got, tc.allow)
		}
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("denied routes reached Sprite %d times", got)
	}
}
