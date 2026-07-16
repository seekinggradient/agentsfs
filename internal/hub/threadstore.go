package hub

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// ThreadStore is the Hub-side, per-user conversation store for the hosted agent.
// It mirrors the Eve app's local ThreadStore (agentsfs-eve/lib/threads.ts): one
// opaque JSON record per thread, an append-only JSONL event archive per thread,
// and one opaque index blob per user. When the agent runs hosted (no per-user
// sandbox filesystem), these files move onto the Hub volume under a per-user dir
// that lives OUTSIDE any git repo, so threads are durable, private to their
// owner, and never leak into a knowledge base.
//
// The append protocol is the load-bearing contract copied verbatim from the Eve
// store: appends are idempotent by ABSOLUTE stream index (selectAppendable), so
// the same event range synced twice — or two concurrent syncs — never dupes and
// the archive stays a contiguous prefix of the eve stream.
type ThreadStore struct {
	base  string
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewThreadStore roots a store at base (e.g. <volume>/.threads). No I/O happens
// until a thread is touched.
func NewThreadStore(base string) *ThreadStore {
	return &ThreadStore{base: base, locks: map[string]*sync.Mutex{}}
}

// threadIDRe mirrors isValidThreadId in the Eve store: client-generated ids that
// are safe to use as a filename, so a hostile id can never escape the data dir.
var threadIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

func validThreadID(id string) bool { return threadIDRe.MatchString(id) }

// lock serializes operations sharing a key (a user's thread id, or their index),
// mirroring the Eve store's per-key promise-chain lock. Returns the unlock func.
func (s *ThreadStore) lock(key string) func() {
	s.mu.Lock()
	m := s.locks[key]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[key] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

func (s *ThreadStore) userDir(user string) string { return filepath.Join(s.base, user) }
func (s *ThreadStore) indexPath(user string) string {
	return filepath.Join(s.userDir(user), "index.json")
}
func (s *ThreadStore) recordPath(user, id string) string {
	return filepath.Join(s.userDir(user), "threads", id+".json")
}
func (s *ThreadStore) archivePath(user, id string) string {
	return filepath.Join(s.userDir(user), "archive", id+".jsonl")
}

// writeFileAtomic writes data via a temp file + rename in the same directory, so
// a reader never observes a half-written file (the writeFileSafe pattern the Eve
// store uses). It fsyncs before rename for crash durability.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// --- index (opaque per-user blob) -----------------------------------------

// GetIndex returns the user's raw index blob, or "{}" when none exists yet
// (mirroring the Eve store's loadIndex default).
func (s *ThreadStore) GetIndex(user string) ([]byte, error) {
	b, err := os.ReadFile(s.indexPath(user))
	if os.IsNotExist(err) {
		return []byte("{}"), nil
	}
	return b, err
}

// PutIndex overwrites the user's index blob atomically.
func (s *ThreadStore) PutIndex(user string, raw []byte) error {
	defer s.lock(user + "\x00::index::")()
	return writeFileAtomic(s.indexPath(user), raw)
}

// --- record (opaque per-thread blob) --------------------------------------

// GetRecord returns the raw thread record, or ok=false when absent.
func (s *ThreadStore) GetRecord(user, id string) ([]byte, bool) {
	b, err := os.ReadFile(s.recordPath(user, id))
	if err != nil {
		return nil, false
	}
	return b, true
}

// PutRecord overwrites a thread record atomically.
func (s *ThreadStore) PutRecord(user, id string, raw []byte) error {
	defer s.lock(user + "\x00" + id)()
	return writeFileAtomic(s.recordPath(user, id), raw)
}

// Delete removes a thread's record and archive. It does NOT touch the index blob
// (which the client owns and maintains via PutIndex). Returns whether a record
// existed.
func (s *ThreadStore) Delete(user, id string) bool {
	defer s.lock(user + "\x00" + id)()
	_, existed := s.GetRecord(user, id)
	os.Remove(s.recordPath(user, id))
	os.Remove(s.archivePath(user, id))
	return existed
}

// --- archive (append-only JSONL) ------------------------------------------

// readArchiveLines returns the non-empty JSONL lines on disk for a thread.
func (s *ThreadStore) readArchiveLines(user, id string) [][]byte {
	raw, err := os.ReadFile(s.archivePath(user, id))
	if err != nil || len(raw) == 0 {
		return nil
	}
	var out [][]byte
	for _, ln := range bytes.Split(raw, []byte("\n")) {
		if len(ln) > 0 {
			out = append(out, ln)
		}
	}
	return out
}

// ReadEvents returns the archived events (as raw JSON values) and their count.
func (s *ThreadStore) ReadEvents(user, id string) ([]json.RawMessage, int) {
	lines := s.readArchiveLines(user, id)
	out := make([]json.RawMessage, 0, len(lines))
	for _, ln := range lines {
		out = append(out, json.RawMessage(ln))
	}
	return out, len(out)
}

// AppendEvents appends events starting at absolute fromIndex, idempotently. It
// re-reads the on-disk length under the per-thread lock and writes only events
// whose absolute index is new AND contiguous, so the same range synced twice (or
// two concurrent syncs) never dupes and the archive stays a contiguous prefix.
// Returns (appended, total-after).
func (s *ThreadStore) AppendEvents(user, id string, events []json.RawMessage, fromIndex int) (int, int, error) {
	defer s.lock(user + "\x00" + id)()
	have := len(s.readArchiveLines(user, id))
	toAppend := selectAppendable(events, fromIndex, have)
	if len(toAppend) == 0 {
		return 0, have, nil
	}
	var block bytes.Buffer
	for _, e := range toAppend {
		// Compact each event to a single newline-free line so the JSONL invariant
		// (one value per line) holds even if the caller sent pretty-printed JSON.
		var buf bytes.Buffer
		if err := json.Compact(&buf, e); err != nil {
			return 0, have, err
		}
		block.Write(buf.Bytes())
		block.WriteByte('\n')
	}
	path := s.archivePath(user, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, have, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, have, err
	}
	if _, err := f.Write(block.Bytes()); err != nil {
		f.Close()
		return 0, have, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return 0, have, err
	}
	if err := f.Close(); err != nil {
		return 0, have, err
	}
	return len(toAppend), have + len(toAppend), nil
}

// selectAppendable is the idempotency guard ported verbatim from the Eve store:
// given events with absolute indices [fromIndex, fromIndex+len), and `have`
// lines already on disk, it returns the prefix that is new (abs >= have) AND
// contiguous (never past have+written), skipping already-archived events and
// refusing to write across a gap.
func selectAppendable(events []json.RawMessage, fromIndex, have int) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(events))
	for k := 0; k < len(events); k++ {
		abs := fromIndex + k
		if abs < have {
			continue // already archived — dedupe
		}
		if abs > have+len(out) {
			break // gap — refuse to write non-contiguously
		}
		out = append(out, events[k])
	}
	return out
}

// --- HTTP handlers --------------------------------------------------------

// apiThreadsIndex serves GET/PUT /api/agent/v1/threads: the caller's opaque
// index blob (the drawer list the Eve client maintains).
func (s *Server) apiThreadsIndex(w http.ResponseWriter, r *http.Request, user string) {
	if s.Threads == nil {
		apiError(w, http.StatusServiceUnavailable, "thread store unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		b, err := s.Threads.GetIndex(user)
		if err != nil {
			apiError(w, http.StatusInternalServerError, "read index")
			return
		}
		writeRawJSON(w, http.StatusOK, b)
	case http.MethodPut:
		raw, ok := readJSONBody(w, r)
		if !ok {
			return
		}
		if err := s.Threads.PutIndex(user, raw); err != nil {
			apiError(w, http.StatusInternalServerError, "write index")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		w.Header().Set("Allow", "GET, PUT")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// apiThread serves the per-thread routes: /thread/<id> (GET/PUT/DELETE record)
// and /thread/<id>/events (GET/POST archive).
func (s *Server) apiThread(w http.ResponseWriter, r *http.Request, user, tail string) {
	if s.Threads == nil {
		apiError(w, http.StatusServiceUnavailable, "thread store unavailable")
		return
	}
	id, sub := splitFirst(tail)
	if !validThreadID(id) {
		apiError(w, http.StatusBadRequest, "bad thread id")
		return
	}
	if sub == "events" {
		s.apiThreadEvents(w, r, user, id)
		return
	}
	if sub != "" {
		apiError(w, http.StatusNotFound, "unknown thread route")
		return
	}
	switch r.Method {
	case http.MethodGet:
		b, ok := s.Threads.GetRecord(user, id)
		if !ok {
			apiError(w, http.StatusNotFound, "no such thread")
			return
		}
		writeRawJSON(w, http.StatusOK, b)
	case http.MethodPut:
		raw, ok := readJSONBody(w, r)
		if !ok {
			return
		}
		if err := s.Threads.PutRecord(user, id, raw); err != nil {
			apiError(w, http.StatusInternalServerError, "write thread")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case http.MethodDelete:
		existed := s.Threads.Delete(user, id)
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": existed})
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// apiThreadEvents serves GET (read archive) and POST (idempotent append) on a
// thread's event archive.
func (s *Server) apiThreadEvents(w http.ResponseWriter, r *http.Request, user, id string) {
	switch r.Method {
	case http.MethodGet:
		events, count := s.Threads.ReadEvents(user, id)
		if events == nil {
			events = []json.RawMessage{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events, "count": count})
	case http.MethodPost:
		var body struct {
			FromIndex int               `json:"fromIndex"`
			Events    []json.RawMessage `json:"events"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&body); err != nil {
			apiError(w, http.StatusBadRequest, "bad json")
			return
		}
		if body.FromIndex < 0 {
			apiError(w, http.StatusBadRequest, "bad fromIndex")
			return
		}
		appended, total, err := s.Threads.AppendEvents(user, id, body.Events, body.FromIndex)
		if err != nil {
			apiError(w, http.StatusInternalServerError, "append events")
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"appended": appended, "total": total})
	default:
		w.Header().Set("Allow", "GET, POST")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// readJSONBody reads a bounded request body and confirms it is syntactically
// valid JSON (so an opaque blob store never persists garbage). Returns the raw
// bytes.
func readJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	raw, err := readAllLimited(r, 1<<20)
	if err != nil {
		apiError(w, http.StatusBadRequest, "read body")
		return nil, false
	}
	if !json.Valid(raw) {
		apiError(w, http.StatusBadRequest, "body is not valid json")
		return nil, false
	}
	return raw, true
}

func readAllLimited(r *http.Request, max int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, max))
}

// writeRawJSON writes an already-serialized JSON blob verbatim.
func writeRawJSON(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	_, _ = w.Write(raw)
}
