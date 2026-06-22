package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Embeddings are an optional enhancer, never a core dependency: with no
// API key configured, semantic search is simply absent and everything
// else works fully. Provider selection is by environment:
//
//	AFS_EMBED_PROVIDER  voyage | openai (optional; auto-detected from keys)
//	AFS_EMBED_MODEL     model override
//	AFS_EMBED_URL       endpoint override (mainly for tests)
//	VOYAGE_API_KEY / OPENAI_API_KEY
//
// Environment variables take precedence over the user-local config written by
// `afs embeddings setup`.
//
// Both providers speak the same request/response shape.
type EmbeddingProvider struct {
	Name       string
	Model      string
	URL        string
	KeyName    string
	KeySource  string
	ConfigPath string
	key        string
}

func DetectEmbeddingProvider() (*EmbeddingProvider, error) {
	cfg, configPath, err := loadEmbeddingConfig()
	if err != nil {
		return nil, err
	}
	value := func(name string) (string, string) {
		if v := os.Getenv(name); v != "" {
			return v, "environment"
		}
		if v := cfg[name]; v != "" {
			return v, configPath
		}
		return "", ""
	}
	build := func(name, model, url, keyName string) (*EmbeddingProvider, error) {
		key, keySource := value(keyName)
		if key == "" {
			return nil, fmt.Errorf("%s is not configured — run `afs embeddings setup %s` or set %s", keyName, name, keyName)
		}
		if m, _ := value("AFS_EMBED_MODEL"); m != "" {
			model = m
		}
		if u, _ := value("AFS_EMBED_URL"); u != "" {
			url = u
		}
		return &EmbeddingProvider{
			Name:       name,
			Model:      model,
			URL:        url,
			KeyName:    keyName,
			KeySource:  keySource,
			ConfigPath: configPath,
			key:        key,
		}, nil
	}
	voyage := func() *EmbeddingProvider {
		p, _ := build("voyage", "voyage-3.5-lite", "https://api.voyageai.com/v1/embeddings", "VOYAGE_API_KEY")
		return p
	}
	openai := func() *EmbeddingProvider {
		p, _ := build("openai", "text-embedding-3-small", "https://api.openai.com/v1/embeddings", "OPENAI_API_KEY")
		return p
	}
	providerName, _ := value("AFS_EMBED_PROVIDER")
	switch providerName {
	case "voyage":
		return build("voyage", "voyage-3.5-lite", "https://api.voyageai.com/v1/embeddings", "VOYAGE_API_KEY")
	case "openai":
		return build("openai", "text-embedding-3-small", "https://api.openai.com/v1/embeddings", "OPENAI_API_KEY")
	case "":
		if key, _ := value("VOYAGE_API_KEY"); key != "" {
			return voyage(), nil
		}
		if key, _ := value("OPENAI_API_KEY"); key != "" {
			return openai(), nil
		}
		return nil, fmt.Errorf("no embedding provider configured — run `afs embeddings setup openai` or set VOYAGE_API_KEY/OPENAI_API_KEY (semantic search is optional; full-text search works without it)")
	default:
		return nil, fmt.Errorf("unknown AFS_EMBED_PROVIDER %q (want voyage or openai)", providerName)
	}
}

const embedBatchSize = 64

// Embed returns one vector per input, batching requests.
func (p *EmbeddingProvider) Embed(inputs []string) ([][]float32, error) {
	var out [][]float32
	client := &http.Client{Timeout: 60 * time.Second}
	for start := 0; start < len(inputs); start += embedBatchSize {
		batch := inputs[start:min(start+embedBatchSize, len(inputs))]
		body, err := json.Marshal(map[string]any{"model": p.Model, "input": batch})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest("POST", p.URL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+p.key)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s embeddings API: %s: %s", p.Name, resp.Status, truncate(string(raw), 300))
		}
		var parsed struct {
			Data []struct {
				Embedding []float32 `json:"embedding"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, err
		}
		if len(parsed.Data) != len(batch) {
			return nil, fmt.Errorf("%s embeddings API returned %d vectors for %d inputs", p.Name, len(parsed.Data), len(batch))
		}
		for _, d := range parsed.Data {
			out = append(out, d.Embedding)
		}
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
