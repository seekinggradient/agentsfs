package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func searchFixture(t *testing.T) string {
	return newInstance(t, map[string]string{
		"notes/INDEX.md": "---\ndescription: Notes.\n---\n",
		"notes/claim.md": "---\ndescription: Insurance claim state of play.\n---\n# Claim\n\nDeductible is $500.\n\n## Next actions\n\nSend the bank statement to the adjuster before the deadline.\n",
		"notes/cat.md":   "---\ndescription: About the cat.\n---\nThe cat sleeps on the windowsill all afternoon.\n",
	})
}

func TestFullTextSearch(t *testing.T) {
	root := searchFixture(t)
	results, err := Search(root, "bank statement deadline", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Path != "notes/claim.md" || results[0].Heading != "Next actions" {
		t.Fatalf("results = %+v", results)
	}
	// Reindex from zero must reproduce: same query, same top hit.
	if _, err := ReindexFTS(root); err != nil {
		t.Fatal(err)
	}
	again, err := Search(root, "bank statement deadline", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(results) || again[0] != results[0] {
		t.Fatalf("reindex changed results: %+v vs %+v", again, results)
	}
	// Quotes and FTS operators in agent queries must not error.
	if _, err := Search(root, `cat AND "windowsill" OR (deductible)`, 5); err != nil {
		t.Fatalf("operator-ish query errored: %v", err)
	}
}

// fakeEmbedServer embeds by crude keyword feature — enough to verify the
// wiring ranks by cosine without any real provider.
func fakeEmbedServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		type item struct {
			Embedding []float32 `json:"embedding"`
		}
		var data []item
		for _, in := range req.Input {
			lower := strings.ToLower(in)
			vec := []float32{0.01, 0.01, 0.01}
			if strings.Contains(lower, "cat") || strings.Contains(lower, "pet") {
				vec[0] = 1
			}
			if strings.Contains(lower, "claim") || strings.Contains(lower, "insurance") {
				vec[1] = 1
			}
			data = append(data, item{vec})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func TestSemanticSearch(t *testing.T) {
	root := searchFixture(t)
	srv := fakeEmbedServer(t)
	defer srv.Close()
	t.Setenv("AFS_EMBED_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "test")
	t.Setenv("AFS_EMBED_URL", srv.URL)

	if _, err := ReindexEmbeddings(root); err != nil {
		t.Fatal(err)
	}
	// "pet" appears nowhere in the files — only semantic association finds the cat note.
	results, warning, err := SemanticSearch(root, "pet", 3)
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" {
		t.Errorf("unexpected staleness warning: %s", warning)
	}
	if len(results) == 0 || results[0].Path != "notes/cat.md" {
		t.Fatalf("semantic results = %+v, want notes/cat.md first", results)
	}
}

func TestReindexEmbeddingsSplitsLargeChunks(t *testing.T) {
	root := newInstance(t, map[string]string{
		"notes/INDEX.md": "---\ndescription: Notes.\n---\n",
		"notes/long.md":  "---\ndescription: Long note.\n---\n# Long\n\n## Huge section\n\n" + strings.Repeat("alpha beta gamma delta ", 5000),
	})
	var inputCount int
	var maxSeen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		type item struct {
			Embedding []float32 `json:"embedding"`
		}
		var data []item
		for _, in := range req.Input {
			inputCount++
			if n := len([]rune(in)); n > maxSeen {
				maxSeen = n
			}
			data = append(data, item{[]float32{1, 0, 0}})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	t.Setenv("AFS_EMBED_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "test")
	t.Setenv("AFS_EMBED_URL", srv.URL)

	if _, err := ReindexEmbeddings(root); err != nil {
		t.Fatal(err)
	}
	if inputCount < 3 {
		t.Fatalf("large section was not split; saw %d embedding inputs", inputCount)
	}
	if maxSeen > maxEmbeddingChunkRunes {
		t.Fatalf("embedding input exceeded limit: saw %d runes, want <= %d", maxSeen, maxEmbeddingChunkRunes)
	}
}

// Finding 3 regression: querying with a different provider/model than the
// index was built with must error, not silently return garbage rankings.
func TestSemanticSearchProviderMismatch(t *testing.T) {
	root := searchFixture(t)
	srv := fakeEmbedServer(t)
	defer srv.Close()
	t.Setenv("AFS_EMBED_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "test")
	t.Setenv("AFS_EMBED_URL", srv.URL)
	if _, err := ReindexEmbeddings(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AFS_EMBED_MODEL", "some-other-model")
	_, _, err := SemanticSearch(root, "anything", 3)
	if err == nil || !strings.Contains(err.Error(), "reindex") {
		t.Fatalf("want mismatch error telling the user to reindex, got %v", err)
	}
}

func TestSemanticSearchWithoutProvider(t *testing.T) {
	root := searchFixture(t)
	t.Setenv("AFS_EMBED_PROVIDER", "")
	t.Setenv("VOYAGE_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, _, err := SemanticSearch(root, "anything", 3); err == nil {
		t.Fatal("want a helpful error with no provider configured")
	}
}
