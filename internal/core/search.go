package core

import (
	"fmt"
	"strings"
)

const maxEmbeddingChunkRunes = 6000

// SearchResult is one hit, pointing into a file at section granularity.
type SearchResult struct {
	Path    string  `json:"path"`
	Heading string  `json:"heading"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score,omitempty"`
}

// Search runs full-text search, transparently rebuilding the index when
// the files have changed since it was built (rebuilds are sub-second at
// personal agentsfs scale, so freshness beats cleverness).
func Search(root, query string, limit int) ([]SearchResult, error) {
	db, err := openIndex(root)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if !ftsFresh(db, root) {
		if _, err := reindexFTS(db, root); err != nil {
			return nil, err
		}
	}
	rows, err := db.Query(
		`SELECT path, heading, snippet(docs_fts, 2, '«', '»', '…', 14)
		 FROM docs_fts WHERE docs_fts MATCH ? ORDER BY rank LIMIT ?`,
		ftsQuery(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Path, &r.Heading, &r.Snippet); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ftsQuery makes agent-typed queries safe for FTS5: each whitespace token
// becomes a quoted term, joined with implicit AND. Operator syntax is
// deliberately not exposed — predictability beats power here.
func ftsQuery(q string) string {
	var terms []string
	for _, t := range strings.Fields(q) {
		terms = append(terms, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
	}
	return strings.Join(terms, " ")
}

// ReindexEmbeddings re-embeds every chunk. Explicit-only (API calls cost
// money); records the provider/model so semantic queries use the same one.
func ReindexEmbeddings(root string) (int, error) {
	provider, err := DetectEmbeddingProvider()
	if err != nil {
		return 0, err
	}
	db, err := openIndex(root)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	chunks, err := embeddingChunks(root)
	if err != nil {
		return 0, err
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Heading + "\n" + c.Body
	}
	vecs, err := provider.Embed(texts)
	if err != nil {
		return 0, err
	}

	fp, err := fingerprint(root)
	if err != nil {
		return 0, err
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM embeddings`); err != nil {
		return 0, err
	}
	for i, c := range chunks {
		if _, err := tx.Exec(`INSERT INTO embeddings (path, heading, body, vec) VALUES (?, ?, ?, ?)`,
			c.Path, c.Heading, c.Body, vecToBlob(vecs[i])); err != nil {
			return 0, err
		}
	}
	for k, v := range map[string]string{
		"embed_fingerprint": fp,
		"embed_provider":    provider.Name,
		"embed_model":       provider.Model,
	} {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`, k, v); err != nil {
			return 0, err
		}
	}
	return len(chunks), tx.Commit()
}

func embeddingChunks(root string) ([]Chunk, error) {
	chunks, err := chunkInstance(root)
	if err != nil {
		return nil, err
	}
	var out []Chunk
	for _, c := range chunks {
		out = append(out, splitEmbeddingChunk(c)...)
	}
	return out, nil
}

func splitEmbeddingChunk(c Chunk) []Chunk {
	if len([]rune(c.Heading+"\n"+c.Body)) <= maxEmbeddingChunkRunes {
		return []Chunk{c}
	}
	bodyLimit := maxEmbeddingChunkRunes - len([]rune(c.Heading)) - 32
	if bodyLimit < maxEmbeddingChunkRunes/2 {
		bodyLimit = maxEmbeddingChunkRunes / 2
	}
	parts := splitTextAtParagraphs(c.Body, bodyLimit)
	out := make([]Chunk, 0, len(parts))
	for i, part := range parts {
		heading := c.Heading
		if len(parts) > 1 {
			heading = fmt.Sprintf("%s (part %d)", c.Heading, i+1)
		}
		out = append(out, Chunk{Path: c.Path, Heading: heading, Body: part})
	}
	return out
}

func splitTextAtParagraphs(text string, limit int) []string {
	var parts []string
	var current strings.Builder
	flush := func() {
		if s := strings.TrimSpace(current.String()); s != "" {
			parts = append(parts, s)
		}
		current.Reset()
	}
	for _, para := range strings.SplitAfter(text, "\n\n") {
		if len([]rune(para)) > limit {
			flush()
			parts = append(parts, splitLongText(para, limit)...)
			continue
		}
		if current.Len() > 0 && len([]rune(current.String()+para)) > limit {
			flush()
		}
		current.WriteString(para)
	}
	flush()
	return parts
}

func splitLongText(text string, limit int) []string {
	runes := []rune(text)
	var parts []string
	for len(runes) > limit {
		cut := limit
		for i := limit; i > limit/2; i-- {
			if runes[i] == '\n' || runes[i] == ' ' || runes[i] == '\t' {
				cut = i + 1
				break
			}
		}
		parts = append(parts, strings.TrimSpace(string(runes[:cut])))
		runes = runes[cut:]
	}
	if tail := strings.TrimSpace(string(runes)); tail != "" {
		parts = append(parts, tail)
	}
	return parts
}

// SemanticSearch embeds the query and ranks chunks by cosine similarity.
// Returns a warning string when the embedding index is stale relative to
// the files (it never auto-rebuilds — that costs API calls).
func SemanticSearch(root, query string, limit int) ([]SearchResult, string, error) {
	provider, err := DetectEmbeddingProvider()
	if err != nil {
		return nil, "", err
	}
	db, err := openIndex(root)
	if err != nil {
		return nil, "", err
	}
	defer db.Close()

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM embeddings`).Scan(&n); err != nil || n == 0 {
		return nil, "", fmt.Errorf("no embedding index — run `afs reindex --embeddings` first")
	}
	// Query vectors must come from the same model as the index, or the
	// rankings are silently meaningless.
	var idxProvider, idxModel string
	db.QueryRow(`SELECT value FROM meta WHERE key = 'embed_provider'`).Scan(&idxProvider)
	db.QueryRow(`SELECT value FROM meta WHERE key = 'embed_model'`).Scan(&idxModel)
	if idxProvider != "" && (idxProvider != provider.Name || idxModel != provider.Model) {
		return nil, "", fmt.Errorf("embedding index was built with %s/%s but the current configuration is %s/%s — run `afs reindex --embeddings`",
			idxProvider, idxModel, provider.Name, provider.Model)
	}
	warning := ""
	var embFP string
	db.QueryRow(`SELECT value FROM meta WHERE key = 'embed_fingerprint'`).Scan(&embFP)
	if fp, err := fingerprint(root); err == nil && fp != embFP {
		warning = "embedding index is stale (files changed since `afs reindex --embeddings`)"
	}

	qv, err := provider.Embed([]string{query})
	if err != nil {
		return nil, "", err
	}
	rows, err := db.Query(`SELECT path, heading, body, vec FROM embeddings`)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var all []SearchResult
	for rows.Next() {
		var r SearchResult
		var body string
		var blob []byte
		if err := rows.Scan(&r.Path, &r.Heading, &body, &blob); err != nil {
			return nil, "", err
		}
		r.Score = cosine(qv[0], blobToVec(blob))
		r.Snippet = truncate(strings.Join(strings.Fields(body), " "), 160)
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	// Selection sort for the top-N keeps this dependency-free and obvious.
	for i := 0; i < len(all) && i < limit; i++ {
		best := i
		for j := i + 1; j < len(all); j++ {
			if all[j].Score > all[best].Score {
				best = j
			}
		}
		all[i], all[best] = all[best], all[i]
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, warning, nil
}
