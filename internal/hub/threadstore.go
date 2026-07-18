package hub

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
)

// ThreadStore is the Hub-side, per-user conversation store for the hosted agent.
// It mirrors the Eve app's local ThreadStore (agentsfs-eve/lib/threads.ts) —
// the HubThreadStore client there consumes this API expecting IDENTICAL
// semantics: one JSON record per thread, and one append-only JSONL archive per
// thread that interleaves TWO line kinds in chronological (append) order:
//
//   - raw Eve stream events (discriminated by `type`, never carrying `kind`), and
//   - voice entries (discriminated by `kind:"voice-entry"` — lib/voice-entry.ts).
//
// The load-bearing idempotency contract, applied server-side exactly as the
// local store applies it:
//
//   - Eve events append by ABSOLUTE stream index (selectAppendable): `fromIndex`
//     counts ONLY the Eve-event lines (voice lines never perturb the bookmark),
//     already-archived indices are skipped, and a gap refuses to write, so the
//     archive stays a contiguous prefix of the eve stream.
//   - Voice entries append by ID: an entry whose id is already archived (or
//     repeated within the batch) is skipped, so a re-sent batch never dupes.
//
// The thread index is Hub-OWNED and derived: GET /threads lists summaries
// parsed straight from the record files (threadId, title, repo, createdAt,
// updatedAt), so it can never drift from the records. Files live on the volume
// under a per-user dir outside any git repo; whole-file writes are atomic
// (temp + fsync + rename).
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

// voiceEntryKind is the archive-line discriminator for voice entries
// (lib/voice-entry.ts VOICE_ENTRY_KIND). Eve stream events never carry `kind`.
const voiceEntryKind = "voice-entry"

// lock serializes operations sharing a key (a user's thread id), mirroring the
// Eve store's per-key promise-chain lock. Returns the unlock func.
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
func (s *ThreadStore) threadsDir(user string) string {
	return filepath.Join(s.userDir(user), "threads")
}
func (s *ThreadStore) recordPath(user, id string) string {
	return filepath.Join(s.threadsDir(user), id+".json")
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

// --- record (opaque per-thread blob) ---------------------------------------

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

// Delete removes a thread's record and archive. The derived index needs no
// separate cleanup — summaries are parsed from the record files, so a deleted
// record vanishes from the listing automatically. Returns whether a record
// existed.
func (s *ThreadStore) Delete(user, id string) bool {
	defer s.lock(user + "\x00" + id)()
	_, existed := s.GetRecord(user, id)
	os.Remove(s.recordPath(user, id))
	os.Remove(s.archivePath(user, id))
	return existed
}

// --- index (derived from records) -------------------------------------------

// threadSummary is the drawer-list projection of a record, mirroring the Eve
// store's ThreadSummary. Repo is a pointer so null round-trips as null.
type threadSummary struct {
	ThreadID  string  `json:"threadId"`
	Title     string  `json:"title"`
	Repo      *string `json:"repo"`
	CreatedAt float64 `json:"createdAt"`
	UpdatedAt float64 `json:"updatedAt"`
}

// ListSummaries derives the user's thread index from the record files (the
// Hub-owned index): each record is parsed for its summary fields, unreadable
// records are skipped, and the result is sorted newest-first by updatedAt.
func (s *ThreadStore) ListSummaries(user string) []threadSummary {
	out := []threadSummary{}
	ents, err := os.ReadDir(s.threadsDir(user))
	if err != nil {
		return out
	}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		id := name[:len(name)-len(".json")]
		if !validThreadID(id) {
			continue
		}
		raw, ok := s.GetRecord(user, id)
		if !ok {
			continue
		}
		var sum threadSummary
		if err := json.Unmarshal(raw, &sum); err != nil {
			continue // never let one corrupt record hide the rest
		}
		if sum.ThreadID == "" {
			sum.ThreadID = id
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// --- archive (append-only mixed JSONL) --------------------------------------

// archiveContents is the parsed archive split into its two line kinds,
// preserving order within each — the Go port of the Eve store's
// readArchiveContents. Voice entries keep their raw line bytes (kind included)
// for id extraction; the HTTP layer strips `kind` on the way out.
type archiveContents struct {
	events   []json.RawMessage // Eve stream events, in append order
	voice    []json.RawMessage // voice-entry lines (kind still present)
	voiceIDs map[string]bool   // archived voice-entry ids (the dedupe set)
}

// eveCount is the pull bookmark / resume cursor: the count of Eve-event lines
// ONLY (voice entries interleaved in the same file never shift it).
func (a archiveContents) eveCount() int { return len(a.events) }

// readArchive parses the on-disk JSONL for a thread. Malformed lines are
// skipped (never throw a whole transcript away over one bad line).
func (s *ThreadStore) readArchive(user, id string) archiveContents {
	out := archiveContents{voiceIDs: map[string]bool{}}
	raw, err := os.ReadFile(s.archivePath(user, id))
	if err != nil || len(raw) == 0 {
		return out
	}
	for _, ln := range bytes.Split(raw, []byte("\n")) {
		if len(ln) == 0 {
			continue
		}
		var probe struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(ln, &probe); err != nil {
			continue // skip a corrupt line rather than lose the rest
		}
		if probe.Kind == voiceEntryKind {
			out.voice = append(out.voice, json.RawMessage(ln))
			if probe.ID != "" {
				out.voiceIDs[probe.ID] = true
			}
		} else {
			out.events = append(out.events, json.RawMessage(ln))
		}
	}
	return out
}

// ReadArchive returns the archived Eve events and voice entries (in append
// order within each kind), with the `kind` discriminator stripped from voice
// entries — exactly what the local store's readArchive hands its callers.
func (s *ThreadStore) ReadArchive(user, id string) (events, voiceEntries []json.RawMessage) {
	a := s.readArchive(user, id)
	events = a.events
	voiceEntries = make([]json.RawMessage, 0, len(a.voice))
	for _, v := range a.voice {
		voiceEntries = append(voiceEntries, stripKind(v))
	}
	return events, voiceEntries
}

// stripKind removes the top-level `kind` discriminator from a voice-entry line
// (the local store destructures it away before returning entries). On any parse
// hiccup the line is returned unmodified.
func stripKind(line json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return line
	}
	delete(m, "kind")
	out, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return out
}

// appendResult reports one mixed append: how many of each kind were newly
// written and the totals after.
type appendResult struct {
	appendedEvents, eveCount  int
	appendedVoice, voiceCount int
}

// AppendMixed appends Eve events (idempotent by absolute index, fromIndex
// counting Eve lines only) and voice entries (idempotent by id) to the thread's
// archive in one serialized operation. Either slice may be empty. Events are
// written before voice entries, matching the call order of the local store's
// separate appendEvents/appendVoiceEntries paths.
func (s *ThreadStore) AppendMixed(user, id string, events []json.RawMessage, fromIndex int, voiceEntries []json.RawMessage) (appendResult, error) {
	defer s.lock(user + "\x00" + id)()
	a := s.readArchive(user, id)
	res := appendResult{eveCount: a.eveCount(), voiceCount: len(a.voice)}

	var block bytes.Buffer
	writeLine := func(e json.RawMessage) error {
		// Compact to a single newline-free line so the JSONL invariant (one value
		// per line) holds even if the caller sent pretty-printed JSON.
		var buf bytes.Buffer
		if err := json.Compact(&buf, e); err != nil {
			return err
		}
		block.Write(buf.Bytes())
		block.WriteByte('\n')
		return nil
	}

	// Eve events: contiguous-prefix append by absolute index (selectAppendable).
	toAppend := selectAppendable(events, fromIndex, a.eveCount())
	for _, e := range toAppend {
		if err := writeLine(e); err != nil {
			return res, err
		}
	}

	// Voice entries: id-based dedupe against the archive AND within the batch;
	// entries without a string id are skipped (mirrors the local guard). The
	// `kind` discriminator is stamped on before writing.
	var freshVoice int
	for _, v := range voiceEntries {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(v, &m); err != nil {
			continue
		}
		var vid string
		if err := json.Unmarshal(m["id"], &vid); err != nil || vid == "" {
			continue
		}
		if a.voiceIDs[vid] {
			continue
		}
		a.voiceIDs[vid] = true // dedupe within the incoming batch too
		m["kind"] = json.RawMessage(`"` + voiceEntryKind + `"`)
		line, err := json.Marshal(m)
		if err != nil {
			continue
		}
		if err := writeLine(line); err != nil {
			return res, err
		}
		freshVoice++
	}

	if block.Len() > 0 {
		path := s.archivePath(user, id)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return res, err
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return res, err
		}
		if _, err := f.Write(block.Bytes()); err != nil {
			f.Close()
			return res, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return res, err
		}
		if err := f.Close(); err != nil {
			return res, err
		}
	}

	res.appendedEvents = len(toAppend)
	res.eveCount += len(toAppend)
	res.appendedVoice = freshVoice
	res.voiceCount += freshVoice
	return res, nil
}

// selectAppendable is the idempotency guard ported verbatim from the Eve store:
// given events with absolute indices [fromIndex, fromIndex+len), and `have`
// Eve-event lines already on disk, it returns the prefix that is new
// (abs >= have) AND contiguous (never past have+written), skipping
// already-archived events and refusing to write across a gap.
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

// --- HTTP handlers -----------------------------------------------------------

// apiThreadsIndex serves GET /api/agent/v1/threads: the caller's thread
// summaries, newest-first, derived from the record files (the Hub owns the
// index — the client never writes one).
func (s *Server) apiThreadsIndex(w http.ResponseWriter, r *http.Request, user string) {
	if s.Threads == nil {
		apiError(w, http.StatusServiceUnavailable, "thread store unavailable")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": s.Threads.ListSummaries(user)})
}

// apiThread serves the per-thread routes: /thread/<id> (GET/PUT/DELETE record)
// and /thread/<id>/events (GET/POST archive). Records travel wrapped as
// {record: …} in both directions (the HubThreadStore client contract).
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
		writeRecordJSON(w, b)
	case http.MethodPut:
		raw, ok := readJSONBody(w, r)
		if !ok {
			return
		}
		var body struct {
			Record json.RawMessage `json:"record"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || len(body.Record) == 0 {
			apiError(w, http.StatusBadRequest, "body must be {record: …}")
			return
		}
		var rec bytes.Buffer
		if err := json.Compact(&rec, body.Record); err != nil {
			apiError(w, http.StatusBadRequest, "record is not valid json")
			return
		}
		if err := s.Threads.PutRecord(user, id, rec.Bytes()); err != nil {
			apiError(w, http.StatusInternalServerError, "write thread")
			return
		}
		writeRecordJSON(w, rec.Bytes())
	case http.MethodDelete:
		existed := s.Threads.Delete(user, id)
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": existed})
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// apiThreadEvents serves the mixed archive: GET returns {events, voiceEntries}
// (kind stripped from voice entries); POST appends {fromIndex, events,
// voiceEntries?} and returns {appendedEvents, eveCount, appendedVoice,
// voiceCount} — events index-idempotent (fromIndex counts Eve lines only),
// voice entries id-idempotent.
func (s *Server) apiThreadEvents(w http.ResponseWriter, r *http.Request, user, id string) {
	switch r.Method {
	case http.MethodGet:
		events, voice := s.Threads.ReadArchive(user, id)
		if events == nil {
			events = []json.RawMessage{}
		}
		if voice == nil {
			voice = []json.RawMessage{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events, "voiceEntries": voice})
	case http.MethodPost:
		var body struct {
			FromIndex    int               `json:"fromIndex"`
			Events       []json.RawMessage `json:"events"`
			VoiceEntries []json.RawMessage `json:"voiceEntries"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&body); err != nil {
			apiError(w, http.StatusBadRequest, "bad json")
			return
		}
		if body.FromIndex < 0 {
			apiError(w, http.StatusBadRequest, "bad fromIndex")
			return
		}
		res, err := s.Threads.AppendMixed(user, id, body.Events, body.FromIndex, body.VoiceEntries)
		if err != nil {
			apiError(w, http.StatusInternalServerError, "append events")
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{
			"appendedEvents": res.appendedEvents,
			"eveCount":       res.eveCount,
			"appendedVoice":  res.appendedVoice,
			"voiceCount":     res.voiceCount,
		})
	default:
		w.Header().Set("Allow", "GET, POST")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// writeRecordJSON writes {"record": <raw>} without re-marshaling the blob.
func writeRecordJSON(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"record":`))
	_, _ = w.Write(raw)
	_, _ = w.Write([]byte(`}`))
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
