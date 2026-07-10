package hub

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type proxiedAgentRequest struct {
	path, query, authorization, cookie string
}

func TestAgentProxyIsolatesPreviewFromHubOrigin(t *testing.T) {
	seen := make(chan proxiedAgentRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- proxiedAgentRequest{
			path:          r.URL.Path,
			query:         r.URL.RawQuery,
			authorization: r.Header.Get("Authorization"),
			cookie:        r.Header.Get("Cookie"),
		}
		// A compromised or stale sprite must not be able to weaken the hub's
		// isolation policy or set a cookie on the authenticated hub origin.
		w.Header().Set("Content-Security-Policy", "sandbox allow-same-origin allow-scripts")
		w.Header().Set("Access-Control-Allow-Origin", "null")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Add("Set-Cookie", "afs_session=attacker; Path=/")
		w.Header().Set("Clear-Site-Data", `"cookies", "storage"`)
		w.Header().Set("Location", "/account")
		w.Header().Set("Refresh", "0; url=/logout")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<!doctype html><p>preview</p>")
	}))
	defer upstream.Close()

	m := NewAgentManager("sprite-token", "openai-key", "", "", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/agent/preview/?v=1", nil)
	req.Header.Set("Cookie", "afs_session=real-user-session")
	recorder := httptest.NewRecorder()
	m.Proxy(recorder, req, upstream.URL, "/agent")

	gotRequest := <-seen
	if gotRequest.path != "/preview/" || gotRequest.query != "v=1" {
		t.Fatalf("upstream URL = %q?%q, want /preview/?v=1", gotRequest.path, gotRequest.query)
	}
	if gotRequest.authorization != "Bearer sprite-token" {
		t.Fatalf("upstream Authorization = %q", gotRequest.authorization)
	}
	if gotRequest.cookie != "" {
		t.Fatalf("hub session cookie leaked upstream: %q", gotRequest.cookie)
	}

	res := recorder.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("proxy status = %d, want 200", res.StatusCode)
	}
	csp := res.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "allow-scripts") {
		t.Fatalf("preview CSP = %q, want sandbox with scripts", csp)
	}
	if strings.Contains(csp, "allow-same-origin") || strings.Contains(csp, "allow-top-navigation") {
		t.Fatalf("preview CSP grants trusted-origin/navigation access: %q", csp)
	}
	if strings.Contains(csp, "allow-forms") {
		t.Fatalf("preview CSP permits form submission: %q", csp)
	}
	for _, want := range []string{
		"connect-src 'none'",
		"frame-src 'none'",
		"worker-src 'none'",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("preview CSP = %q, missing %q", csp, want)
		}
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
	}
	if got := res.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want empty", got)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := res.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
	if got := res.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want forced preview HTML", got)
	}
	if cookies := res.Header.Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("sprite Set-Cookie reached hub client: %q", cookies)
	}
	for _, header := range []string{"Clear-Site-Data", "Location", "Refresh"} {
		if got := res.Header.Get(header); got != "" {
			t.Errorf("untrusted %s header reached Hub client: %q", header, got)
		}
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "preview") {
		t.Fatalf("proxied body = %q", body)
	}
}

func TestAgentProxyHardensAPIAndPreservesSSE(t *testing.T) {
	seen := make(chan proxiedAgentRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- proxiedAgentRequest{
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			cookie:        r.Header.Get("Cookie"),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "script-src * 'unsafe-inline'")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Set-Cookie", "afs_session=attacker")
		w.Header().Set("Clear-Site-Data", `"*"`)
		w.Header().Set("Location", "/logout")
		w.Header().Set("Refresh", "0; url=/logout")
		io.WriteString(w, "event: delta\ndata: {\"text\":\"hi\"}\n\n")
	}))
	defer upstream.Close()

	m := NewAgentManager("sprite-token", "openai-key", "", "", nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/agent/api/chat", strings.NewReader(`{"message":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "afs_session=real-user-session")
	recorder := httptest.NewRecorder()
	m.Proxy(recorder, req, upstream.URL, "/agent")

	gotRequest := <-seen
	if gotRequest.path != "/api/chat" || gotRequest.authorization != "Bearer sprite-token" {
		t.Fatalf("upstream request = %#v", gotRequest)
	}
	if gotRequest.cookie != "" {
		t.Fatalf("hub session cookie leaked upstream: %q", gotRequest.cookie)
	}

	res := recorder.Result()
	defer res.Body.Close()
	if got := res.Header.Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want event stream", got)
	}
	csp := res.Header.Get("Content-Security-Policy")
	if csp != agentAPICSP || strings.Contains(csp, "allow-scripts") {
		t.Fatalf("API CSP = %q", csp)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := res.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store, no-transform" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := res.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q", got)
	}
	for _, header := range []string{
		"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Set-Cookie",
		"Clear-Site-Data", "Location", "Refresh",
	} {
		if got := res.Header.Get(header); got != "" {
			t.Errorf("untrusted %s header reached Hub client: %q", header, got)
		}
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "event: delta\ndata: {\"text\":\"hi\"}\n\n" {
		t.Fatalf("SSE body changed: %q", got)
	}
}

func TestAgentProxyRejectsUpstreamRedirect(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/agent/app.js")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()

	m := NewAgentManager("sprite-token", "openai-key", "", "", nil, nil)
	recorder := httptest.NewRecorder()
	m.Proxy(recorder, httptest.NewRequest(http.MethodGet, "/agent/api/config", nil), upstream.URL, "/agent")

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", recorder.Code)
	}
	if got := recorder.Header().Get("Location"); got != "" {
		t.Fatalf("redirect Location reached client: %q", got)
	}
}

func TestAgentProxyFlushesSSEBeforeUpstreamCompletes(t *testing.T) {
	first := "event: delta\ndata: {\"text\":\"first\"}\n\n"
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, first)
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "event: done\ndata: {}\n\n")
	}))
	defer upstream.Close()

	m := NewAgentManager("sprite-token", "openai-key", "", "", nil, nil)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Proxy(w, r, upstream.URL, "/agent")
	}))
	defer hub.Close()
	client := hub.Client()
	client.Timeout = 2 * time.Second
	req, err := http.NewRequest(http.MethodPost, hub.URL+"/agent/api/chat", strings.NewReader(`{"message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	buf := make([]byte, len(first))
	if _, err := io.ReadFull(res.Body, buf); err != nil {
		t.Fatalf("first SSE frame was buffered until completion: %v", err)
	}
	if got := string(buf); got != first {
		t.Fatalf("first SSE frame = %q", got)
	}
	close(release)
	released = true
	rest, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rest), "event: done") {
		t.Fatalf("final SSE frame missing: %q", rest)
	}
}
