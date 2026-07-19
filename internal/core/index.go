package core

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// The index is a sidecar, never the truth: one SQLite file under
// .agentsfs/, fully reproducible from the markdown alone. Full-text is
// rebuilt automatically whenever the files change; embeddings only on
// explicit request (they cost API calls).

// ftsSchemaVersion identifies the on-disk shape of docs_fts — most importantly
// its tokenizer. Bump it whenever the CREATE statement changes so existing
// indexes are force-rebuilt on next open. The content fingerprint alone cannot
// catch a schema change: the files are byte-for-byte identical, only the table
// definition moved, so an index built by an older afs would otherwise keep
// serving results from the old (unstemmed) tokenizer forever.
const ftsSchemaVersion = "3-porter-descsentinel"

func indexPath(root string) string {
	return filepath.Join(root, ".agentsfs", "index.db")
}

func openIndex(root string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", indexPath(root))
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);
		CREATE TABLE IF NOT EXISTS embeddings (
			path TEXT, heading TEXT, body TEXT, vec BLOB
		);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Chunk is the unit of indexing and retrieval: a markdown file split at
// top-level (##) sections, so hits point into files, not just at them.
type Chunk struct {
	Path    string
	Heading string
	Body    string
}

func chunkInstance(root string) ([]Chunk, error) {
	entries, err := ListEntries(root)
	if err != nil {
		return nil, err
	}
	var chunks []Chunk
	for _, e := range entries {
		if e.IsDir || !isMarkdown(e.Rel) {
			continue
		}
		data, err := os.ReadFile(joinRel(root, e.Rel))
		if err != nil {
			return nil, err
		}
		heading := strings.TrimSuffix(baseName(e.Rel), ".md")
		body := strings.Builder{}
		flush := func() {
			if b := strings.TrimSpace(body.String()); b != "" {
				chunks = append(chunks, Chunk{e.Rel, heading, b})
			}
			body.Reset()
		}
		for _, line := range strings.Split(stripFrontmatter(string(data)), "\n") {
			if h, ok := strings.CutPrefix(line, "## "); ok {
				flush()
				heading = strings.TrimSpace(h)
				continue
			}
			body.WriteString(line)
			body.WriteString("\n")
		}
		flush()
		// The description is knowledge too — index it with the file's first chunk.
		// It gets the descHeading sentinel (not the literal "description") so a
		// file with a real `## description` section does not collide with it.
		if d := Description(joinRel(root, e.Rel)); d != "" {
			chunks = append(chunks, Chunk{e.Rel, descHeading, d})
		}
	}
	return chunks, nil
}

// fingerprint changes iff any markdown file's path, size, or mtime does.
func fingerprint(root string) (string, error) {
	entries, err := ListEntries(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, e := range entries { // ListEntries sorts, so this is stable
		if e.IsDir || !isMarkdown(e.Rel) {
			continue
		}
		info, err := os.Stat(joinRel(root, e.Rel))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s|%d|%d\n", e.Rel, info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReindexFTS rebuilds the full-text index from zero — same files in, same
// index out (Principle 2 enforced by construction: drop and re-derive).
func ReindexFTS(root string) (int, error) {
	db, err := openIndex(root)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return reindexFTS(db, root)
}

func reindexFTS(db *sql.DB, root string) (int, error) {
	chunks, err := chunkInstance(root)
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
	if _, err := tx.Exec(`DROP TABLE IF EXISTS docs_fts`); err != nil {
		return 0, err
	}
	// porter stemming lets "disputing" match "dispute" and "asking" match "ask";
	// unicode61 keeps the default Unicode-aware boundary handling. Natural-
	// language queries are the norm for agent memory, so stemming is the floor,
	// not a tuning knob.
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE docs_fts USING fts5(path, heading, body, tokenize = 'porter unicode61')`); err != nil {
		return 0, err
	}
	for _, c := range chunks {
		if _, err := tx.Exec(`INSERT INTO docs_fts (path, heading, body) VALUES (?, ?, ?)`, c.Path, c.Heading, c.Body); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES ('fingerprint', ?)`, fp); err != nil {
		return 0, err
	}
	// Record the schema version alongside the content fingerprint so ftsFresh
	// can force a rebuild when the tokenizer/shape changes even though the files
	// did not.
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES ('fts_schema_version', ?)`, ftsSchemaVersion); err != nil {
		return 0, err
	}
	return len(chunks), tx.Commit()
}

func ftsFresh(db *sql.DB, root string) bool {
	// A schema mismatch means the docs_fts table was built by a different afs
	// (e.g. before porter stemming). Treat it as stale so the existing reindex
	// path drops and recreates the table with the current tokenizer.
	var schema string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'fts_schema_version'`).Scan(&schema); err != nil || schema != ftsSchemaVersion {
		return false
	}
	var stored string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'fingerprint'`).Scan(&stored); err != nil {
		return false
	}
	fp, err := fingerprint(root)
	return err == nil && fp == stored
}

func vecToBlob(v []float32) []byte {
	out := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(f))
	}
	return out
}

func blobToVec(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return -1
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return -1
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
