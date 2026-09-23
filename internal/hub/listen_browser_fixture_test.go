package hub

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestListenBrowserFixture(t *testing.T) {
	output := os.Getenv("AFS_LISTEN_BROWSER_FIXTURE")
	if output == "" {
		t.Skip("browser harness only")
	}
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", map[string]string{
		"01-reading.md":   "---\ndescription: A source-following demonstration\n---\n# A page worth listening to\n\nThis is the original page. Its words stay the same while the voice follows each passage.\n\n## Read at your own pace\n\nYou can pause, choose a different voice, or jump ahead to a paragraph. The highlight keeps your place.\n\n- First, choose a page.\n- Then press Play.\n\n## A small comparison\n\n| Feature | Behavior |\n| --- | --- |\n| Voice | Gemini |\n| Text | Unchanged |\n\n```python\nprint('Hello, reader')\n```\n",
		"02-next-page.md": "# The next page\n\nYour playlist continues here.\n",
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/narrate/voices" {
			json.NewEncoder(w).Encode(map[string]any{"voices": []map[string]string{{"name": "Kore"}, {"name": "Puck"}, {"name": "Aoede"}}})
			return
		}
		time.Sleep(300 * time.Millisecond)
		pcm := make([]byte, 24000*2*2)
		json.NewEncoder(w).Encode(map[string]any{"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(pcm), "mimeType": "audio/L16;codec=pcm;rate=24000", "durationMs": 2000}})
	}))
	defer upstream.Close()
	srv.narration.upstream = upstream.URL
	data, _ := json.Marshal(map[string]any{"url": ts.URL, "cookie": sessionCookieFor(srv, "alice")})
	os.WriteFile(output, data, 0600)
	for i := 0; i < 600; i++ {
		if _, e := os.Stat(output + ".stop"); e == nil {
			return
		}
		time.Sleep(time.Second)
	}
}
