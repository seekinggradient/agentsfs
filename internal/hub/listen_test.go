package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestListenPassagesFollowSource(t *testing.T) {
	content := "---\ndescription: Do not read metadata\n---\n# A title\n\nFirst **bold** &amp; [linked](https://example.com) sentence.\nWrapped line.\n\n- First item\n- Second [[Page|label]]\n\n| Name | Value |\n| --- | --- |\n| A | B |\n\n```go\nfmt.Println(42)\n```\n\n![A useful diagram](figure.png)\n\n<script>alert(1)</script>\n"
	var passages []listenPassage
	rendered, err := renderMarkdownWith(content, markdownOptions{narration: &passages, resolveWiki: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	var speech []string
	for _, p := range passages {
		speech = append(speech, p.Text)
		if !strings.Contains(rendered, `data-listen-target="`+p.Target+`"`) {
			t.Fatalf("missing target: %+v", p)
		}
	}
	joined := strings.Join(speech, "\n")
	for _, wanted := range []string{"A title", "First bold & linked sentence. Wrapped line.", "First item", "Second label", "Name Value A B", "fmt.Println(42)", "A useful diagram"} {
		if !strings.Contains(joined, wanted) {
			t.Errorf("missing %q in %q", wanted, joined)
		}
	}
	if strings.Contains(joined, "metadata") || strings.Contains(joined, "alert") || strings.Contains(rendered, "<script>") {
		t.Fatalf("unsafe or hidden content leaked: %s / %s", joined, rendered)
	}
	if !passages[0].Heading {
		t.Error("heading missing chapter marker")
	}
}
func TestListenChunksPreserveText(t *testing.T) {
	for _, s := range []string{strings.Repeat("A sentence. ", 400), strings.Repeat("🦊", 2100), strings.Repeat("word ", 600)} {
		chunks := listenChunks(strings.TrimSpace(s), 1000)
		for _, c := range chunks {
			if len([]rune(c)) > 1000 || c == "" {
				t.Fatalf("bad chunk: %d", len([]rune(c)))
			}
		}
		if strings.ReplaceAll(strings.Join(chunks, ""), " ", "") != strings.ReplaceAll(strings.TrimSpace(s), " ", "") {
			t.Error("chunking lost or duplicated text")
		}
	}
}
func listenPost(t *testing.T, ts *httptest.Server, srv *Server, user, route, origin, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/alice/brain/listen/"+route, strings.NewReader(body))
	if user != "" {
		req.AddCookie(sessionCookieFor(srv, user))
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}
func TestListenReadOnlyReaderAndSpeechBoundary(t *testing.T) {
	ts, srv, acc := newShareTestHub(t)
	mkAccount(t, acc, "bob")
	acc.AddCollaborator("alice", "brain", "bob", "read")
	seedShareRepo(t, srv, "alice", "brain", map[string]string{"page.md": "# Page\n\nRead this exact sentence.\n", "other.txt": "Not Markdown"})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		user, ok := acc.UserForToken(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if !ok || user != "bob" {
			t.Errorf("wrong service identity %q", user)
		}
		if r.Header.Get("Cookie") != "" {
			t.Error("session leaked upstream")
		}
		if r.URL.Path == "/v1/narrate/voices" {
			io.WriteString(w, `{"voices":[{"name":"Kore"},{"name":"Puck"}]}`)
			return
		}
		var payload map[string]string
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["text"] != "Read this exact sentence." {
			t.Errorf("speech changed: %v", payload)
		}
		io.WriteString(w, `{"audio":{"data":"AAAAAA==","mimeType":"audio/L16;codec=pcm;rate=24000","durationMs":1}}`)
	}))
	defer upstream.Close()
	srv.narration.upstream = upstream.URL
	res, page := mdtoGet(t, ts, srv, "bob", "/alice/brain/listen?path=page.md")
	if res.StatusCode != 200 || !strings.Contains(page, "Read along") {
		t.Fatalf("reader denied: %d %s", res.StatusCode, page)
	}
	_, body := mdtoGet(t, ts, srv, "bob", "/alice/brain/listen/source?path=page.md")
	var source listenSource
	if err := json.Unmarshal([]byte(body), &source); err != nil {
		t.Fatal(body)
	}
	if len(source.Passages) != 2 {
		t.Fatalf("passages=%+v", source.Passages)
	}
	payload := func(hash, voice string) string {
		b, _ := json.Marshal(map[string]any{"path": "page.md", "hash": hash, "index": 1, "voice": voice})
		return string(b)
	}
	before := headOID("git", srv.Storage.RepoDir("alice", "brain"), defaultRef)
	for _, test := range []struct {
		user, origin, body string
		want               int
	}{
		{"bob", "", payload(source.Hash, "Kore"), 403},
		{"bob", "https://evil.example", payload(source.Hash, "Kore"), 403},
		{"bob", ts.URL, payload("stale", "Kore"), 409},
		{"bob", ts.URL, `{"path":"page.md","hash":"x","index":0,"voice":"Kore","text":"injected"}`, 400},
		{"bob", ts.URL, payload(source.Hash, "Kore"), 200},
		{"bob", ts.URL, payload(source.Hash, "Kore"), 200},
		{"bob", ts.URL, payload(source.Hash, "Puck"), 200},
	} {
		status, b := listenPost(t, ts, srv, test.user, "speech", test.origin, test.body)
		if status != test.want {
			t.Errorf("status %d want %d: %s", status, test.want, b)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("want one synthesis per voice, got %d", calls.Load())
	}
	if headOID("git", srv.Storage.RepoDir("alice", "brain"), defaultRef) != before {
		t.Error("listening edited repository")
	}
	res, _ = mdtoGet(t, ts, srv, "bob", "/alice/brain/listen/pages")
	if res.StatusCode != 200 {
		t.Error("reader cannot list pages")
	}
	// Unrelated authenticated users cannot reach any route, even cached audio.
	mkAccount(t, acc, "dave")
	status, _ := listenPost(t, ts, srv, "dave", "speech", ts.URL, payload(source.Hash, "Kore"))
	if status != 403 {
		t.Fatalf("stranger status %d", status)
	}
}
func TestListenDeduplicatesPendingAndBoundsConcurrency(t *testing.T) {
	ts, srv, acc := newShareTestHub(t)
	mkAccount(t, acc, "bob")
	acc.AddCollaborator("alice", "brain", "bob", "read")
	seedShareRepo(t, srv, "alice", "brain", map[string]string{"page.md": "One.\n\nTwo.\n\nThree."})
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		io.WriteString(w, `{"audio":{"data":"AAAAAA==","mimeType":"audio/L16;codec=pcm;rate=24000","durationMs":1}}`)
	}))
	defer upstream.Close()
	srv.narration.upstream = upstream.URL
	source, _ := srv.listenSource("alice", "brain", "page.md")
	payload := func(i int) string {
		b, _ := json.Marshal(map[string]any{"path": "page.md", "hash": source.Hash, "index": i, "voice": "Kore"})
		return string(b)
	}
	results := make(chan int, 3)
	for _, i := range []int{0, 1} {
		go func(i int) {
			status, _ := listenPost(t, ts, srv, "bob", "speech", ts.URL, payload(i))
			results <- status
		}(i)
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("upstream not reached")
		}
	}
	status, _ := listenPost(t, ts, srv, "bob", "speech", ts.URL, payload(2))
	if status != 429 {
		t.Errorf("concurrency not bounded: %d", status)
	}
	go func() { status, _ := listenPost(t, ts, srv, "bob", "speech", ts.URL, payload(0)); results <- status }()
	close(release)
	for i := 0; i < 3; i++ {
		if <-results != 200 {
			t.Error("request failed")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("duplicated in-flight request: %d", calls.Load())
	}
}

func TestListenCacheStillChecksSourceAndAccess(t *testing.T) {
	ts, srv, acc := newShareTestHub(t)
	mkAccount(t, acc, "bob")
	acc.AddCollaborator("alice", "brain", "bob", "read")
	work := seedShareRepo(t, srv, "alice", "brain", map[string]string{"page.md": "Original sentence."})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"audio":{"data":"AAAAAA==","mimeType":"audio/L16;codec=pcm;rate=24000","durationMs":1}}`)
	}))
	defer upstream.Close()
	srv.narration.upstream = upstream.URL
	source, _ := srv.listenSource("alice", "brain", "page.md")
	payload, _ := json.Marshal(map[string]any{"path": "page.md", "hash": source.Hash, "index": 0, "voice": "Kore"})
	if status, _ := listenPost(t, ts, srv, "bob", "speech", ts.URL, string(payload)); status != 200 {
		t.Fatal(status)
	}
	pushShareCommit(t, srv, work, "alice", "brain", map[string]string{"page.md": "Changed sentence."})
	if status, _ := listenPost(t, ts, srv, "bob", "speech", ts.URL, string(payload)); status != 409 {
		t.Fatalf("stale cached audio: %d", status)
	}
	if calls.Load() != 1 {
		t.Error("stale request spent synthesis")
	}
	for _, p := range []string{"../page.md", "/etc/passwd", "page.txt"} {
		if _, err := srv.listenSource("alice", "brain", p); err == nil {
			t.Errorf("accepted %q", p)
		}
	}
}
func TestListenUpstreamFailureDoesNotLeakOrRetry(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", map[string]string{"page.md": "A sentence."})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		io.WriteString(w, `{"error":"internal-secret-provider-diagnostic"}`)
	}))
	defer upstream.Close()
	srv.narration.upstream = upstream.URL
	source, _ := srv.listenSource("alice", "brain", "page.md")
	payload, _ := json.Marshal(map[string]any{"path": "page.md", "hash": source.Hash, "index": 0, "voice": "Kore"})
	for i := 0; i < 2; i++ {
		status, body := listenPost(t, ts, srv, "alice", "speech", ts.URL, string(payload))
		if status != 503 || strings.Contains(body, "internal-secret") {
			t.Fatalf("bad failure %d %s", status, body)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("retried failed paid request: %d", calls.Load())
	}
}

func TestListenValidatesAudio(t *testing.T) {
	for _, bad := range []string{`{}`, `{"audio":{"data":"invalid!","mimeType":"audio/L16;codec=pcm;rate=24000","durationMs":100}}`, `{"audio":{"data":"AAAA","mimeType":"audio/L16;codec=pcm;rate=24000","durationMs":100}}`, `{"audio":{"data":"AAAAAA==","mimeType":"text/html","durationMs":100}}`} {
		if validListenAudio([]byte(bad)) {
			t.Errorf("accepted invalid audio %s", bad)
		}
	}
}
