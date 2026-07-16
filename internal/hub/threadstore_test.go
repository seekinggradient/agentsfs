package hub

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestSelectAppendable mirrors the Eve store's idempotency contract: appends are
// keyed by ABSOLUTE stream index — already-archived events are skipped, and a
// gap (non-contiguous range) is refused so the archive stays a contiguous prefix.
func TestSelectAppendable(t *testing.T) {
	ev := func(n int) []json.RawMessage {
		out := make([]json.RawMessage, n)
		for i := range out {
			out[i] = json.RawMessage(`{}`)
		}
		return out
	}
	cases := []struct {
		name               string
		n, fromIndex, have int
		want               int
	}{
		{"all new from empty", 3, 0, 0, 3},
		{"dedupe already-archived prefix", 3, 0, 2, 1},
		{"all already present", 3, 0, 5, 0},
		{"contiguous continuation", 2, 2, 2, 2},
		{"gap is refused", 1, 5, 2, 0},
		{"partial overlap then continue", 4, 0, 2, 2},
	}
	for _, tc := range cases {
		got := selectAppendable(ev(tc.n), tc.fromIndex, tc.have)
		if len(got) != tc.want {
			t.Errorf("%s: selectAppendable(n=%d,from=%d,have=%d) = %d, want %d",
				tc.name, tc.n, tc.fromIndex, tc.have, len(got), tc.want)
		}
	}
}

const goodThreadID = "thread-abcd-0001"

// appendCounts is the POST /events response shape the HubThreadStore consumes.
type appendCounts struct {
	AppendedEvents int `json:"appendedEvents"`
	EveCount       int `json:"eveCount"`
	AppendedVoice  int `json:"appendedVoice"`
	VoiceCount     int `json:"voiceCount"`
}

// TestAPIThreadEventsIdempotentAppend exercises the Eve-event append idempotency
// over HTTP: re-POSTing the same absolute range never dupes; a gap is refused;
// fromIndex counts Eve events only.
func TestAPIThreadEventsIdempotentAppend(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	base := "/api/agent/v1/thread/" + goodThreadID + "/events"

	post := func(body string) appendCounts {
		code, b := apiDo(t, ts, http.MethodPost, base, tok, body)
		if code != http.StatusOK {
			t.Fatalf("append = %d (%s)", code, b)
		}
		var out appendCounts
		_ = json.Unmarshal(b, &out)
		return out
	}

	if r := post(`{"fromIndex":0,"events":[{"type":"a"},{"type":"b"}]}`); r.AppendedEvents != 2 || r.EveCount != 2 {
		t.Fatalf("first append = %+v, want appendedEvents 2 eveCount 2", r)
	}
	// Same range again — idempotent, nothing new.
	if r := post(`{"fromIndex":0,"events":[{"type":"a"},{"type":"b"}]}`); r.AppendedEvents != 0 || r.EveCount != 2 {
		t.Fatalf("re-append = %+v, want appendedEvents 0 eveCount 2", r)
	}
	// Contiguous continuation.
	if r := post(`{"fromIndex":2,"events":[{"type":"c"}]}`); r.AppendedEvents != 1 || r.EveCount != 3 {
		t.Fatalf("continue = %+v, want appendedEvents 1 eveCount 3", r)
	}
	// Gap: fromIndex beyond what's on disk — refused.
	if r := post(`{"fromIndex":9,"events":[{"type":"z"}]}`); r.AppendedEvents != 0 || r.EveCount != 3 {
		t.Fatalf("gap append = %+v, want appendedEvents 0 eveCount 3", r)
	}

	// GET returns the mixed-archive shape with the contiguous events.
	var got struct {
		Events       []json.RawMessage `json:"events"`
		VoiceEntries []json.RawMessage `json:"voiceEntries"`
	}
	apiJSON(t, ts, http.MethodGet, base, tok, "", &got)
	if len(got.Events) != 3 {
		t.Fatalf("archive = %d events, want 3", len(got.Events))
	}
	if got.VoiceEntries == nil || len(got.VoiceEntries) != 0 {
		t.Fatalf("voiceEntries = %v, want present-and-empty", got.VoiceEntries)
	}
}

// TestAPIThreadVoiceEntriesIdempotentById proves voice entries dedupe by id: a
// re-sent batch (and an in-batch duplicate) never dupes, and entries without an
// id are skipped.
func TestAPIThreadVoiceEntriesIdempotentById(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	base := "/api/agent/v1/thread/" + goodThreadID + "/events"

	post := func(body string) appendCounts {
		code, b := apiDo(t, ts, http.MethodPost, base, tok, body)
		if code != http.StatusOK {
			t.Fatalf("append = %d (%s)", code, b)
		}
		var out appendCounts
		_ = json.Unmarshal(b, &out)
		return out
	}

	// Two fresh entries, one in-batch duplicate, one id-less (skipped).
	r := post(`{"voiceEntries":[
		{"id":"v1","ts":1,"afterMessageCount":0,"user":"hi","assistant":"hello"},
		{"id":"v2","ts":2,"afterMessageCount":0,"user":"more","assistant":"sure"},
		{"id":"v1","ts":1,"afterMessageCount":0,"user":"hi","assistant":"hello"},
		{"ts":3,"user":"no id","assistant":"skipped"}]}`)
	if r.AppendedVoice != 2 || r.VoiceCount != 2 {
		t.Fatalf("voice append = %+v, want appendedVoice 2 voiceCount 2", r)
	}
	if r.AppendedEvents != 0 || r.EveCount != 0 {
		t.Fatalf("voice-only append touched events: %+v", r)
	}

	// Re-POST the same batch — fully deduped.
	r = post(`{"voiceEntries":[
		{"id":"v1","ts":1,"afterMessageCount":0,"user":"hi","assistant":"hello"},
		{"id":"v2","ts":2,"afterMessageCount":0,"user":"more","assistant":"sure"}]}`)
	if r.AppendedVoice != 0 || r.VoiceCount != 2 {
		t.Fatalf("voice re-append = %+v, want appendedVoice 0 voiceCount 2", r)
	}
}

// TestAPIThreadMixedAppendAndInterleavedReadback exercises the full mixed
// contract: one POST carrying both events and voice entries; voice lines never
// perturb the Eve-index bookmark; the GET splits the kinds and strips the
// voice-entry `kind` discriminator.
func TestAPIThreadMixedAppendAndInterleavedReadback(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	base := "/api/agent/v1/thread/" + goodThreadID + "/events"

	post := func(body string) appendCounts {
		code, b := apiDo(t, ts, http.MethodPost, base, tok, body)
		if code != http.StatusOK {
			t.Fatalf("append = %d (%s)", code, b)
		}
		var out appendCounts
		_ = json.Unmarshal(b, &out)
		return out
	}

	// Mixed POST: two Eve events + one voice entry in one call.
	r := post(`{"fromIndex":0,
		"events":[{"type":"message.received"},{"type":"turn.completed"}],
		"voiceEntries":[{"id":"vx","ts":5,"afterMessageCount":1,"user":"aside","assistant":"noted"}]}`)
	if r.AppendedEvents != 2 || r.EveCount != 2 || r.AppendedVoice != 1 || r.VoiceCount != 1 {
		t.Fatalf("mixed append = %+v", r)
	}

	// The voice line must NOT shift the Eve bookmark: the next contiguous Eve
	// append still starts at fromIndex == eveCount == 2.
	r = post(`{"fromIndex":2,"events":[{"type":"message.received","n":3}]}`)
	if r.AppendedEvents != 1 || r.EveCount != 3 || r.VoiceCount != 1 {
		t.Fatalf("post-voice continue = %+v, want appendedEvents 1 eveCount 3", r)
	}

	// Read back: kinds split, order preserved within each, voice `kind` stripped.
	var got struct {
		Events       []map[string]any `json:"events"`
		VoiceEntries []map[string]any `json:"voiceEntries"`
	}
	apiJSON(t, ts, http.MethodGet, base, tok, "", &got)
	if len(got.Events) != 3 || len(got.VoiceEntries) != 1 {
		t.Fatalf("readback = %d events / %d voice, want 3/1", len(got.Events), len(got.VoiceEntries))
	}
	if got.Events[0]["type"] != "message.received" || got.Events[1]["type"] != "turn.completed" {
		t.Fatalf("event order lost: %v", got.Events)
	}
	v := got.VoiceEntries[0]
	if v["id"] != "vx" || v["user"] != "aside" || v["assistant"] != "noted" {
		t.Fatalf("voice entry mangled: %v", v)
	}
	if _, hasKind := v["kind"]; hasKind {
		t.Fatalf("voice entry still carries the archive discriminator: %v", v)
	}
}

// TestAPIThreadRecordWrappingAndDerivedIndex round-trips the {record} wrapper
// and checks GET /threads derives summaries from the records, newest-first.
func TestAPIThreadRecordWrappingAndDerivedIndex(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")

	// Record absent → 404.
	code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/thread/"+goodThreadID, tok, "")
	if code != http.StatusNotFound {
		t.Fatalf("absent record = %d, want 404", code)
	}

	// PUT {record} → the response and a later GET both wrap as {record}.
	rec := `{"threadId":"` + goodThreadID + `","title":"hello","repo":null,"streamIndex":3,"voiceSeenHighWater":0,"createdAt":100,"updatedAt":200}`
	code, body := apiDo(t, ts, http.MethodPut, "/api/agent/v1/thread/"+goodThreadID, tok, `{"record":`+rec+`}`)
	if code != http.StatusOK {
		t.Fatalf("put record = %d (%s)", code, body)
	}
	var wrapped struct {
		Record map[string]any `json:"record"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil || wrapped.Record["title"] != "hello" {
		t.Fatalf("put response = %s, want {record:{…title:hello}}", body)
	}
	code, body = apiDo(t, ts, http.MethodGet, "/api/agent/v1/thread/"+goodThreadID, tok, "")
	if code != http.StatusOK {
		t.Fatalf("get record = %d", code)
	}
	wrapped.Record = nil
	if err := json.Unmarshal(body, &wrapped); err != nil || wrapped.Record["threadId"] != goodThreadID {
		t.Fatalf("get record = %s, want the {record}-wrapped blob", body)
	}

	// A bare (unwrapped) PUT body is rejected.
	if code, _ := apiDo(t, ts, http.MethodPut, "/api/agent/v1/thread/"+goodThreadID, tok, rec); code != http.StatusBadRequest {
		t.Fatalf("unwrapped put = %d, want 400", code)
	}

	// Second, newer record → /threads lists both, newest-first, as summaries.
	rec2 := `{"threadId":"thread-abcd-0002","title":"newer","repo":"brain","createdAt":150,"updatedAt":900}`
	if code, _ := apiDo(t, ts, http.MethodPut, "/api/agent/v1/thread/thread-abcd-0002", tok, `{"record":`+rec2+`}`); code != http.StatusOK {
		t.Fatal("put second record failed")
	}
	var idx struct {
		Threads []struct {
			ThreadID  string  `json:"threadId"`
			Title     string  `json:"title"`
			Repo      *string `json:"repo"`
			UpdatedAt float64 `json:"updatedAt"`
		} `json:"threads"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/threads", tok, "", &idx)
	if len(idx.Threads) != 2 {
		t.Fatalf("threads = %d entries, want 2", len(idx.Threads))
	}
	if idx.Threads[0].ThreadID != "thread-abcd-0002" || idx.Threads[0].Title != "newer" {
		t.Fatalf("threads[0] = %+v, want the newest first", idx.Threads[0])
	}
	if idx.Threads[0].Repo == nil || *idx.Threads[0].Repo != "brain" {
		t.Fatalf("threads[0].repo = %v, want brain", idx.Threads[0].Repo)
	}
	if idx.Threads[1].Repo != nil {
		t.Fatalf("threads[1].repo = %v, want null", idx.Threads[1].Repo)
	}

	// DELETE removes the record and it drops out of the derived index.
	if code, _ := apiDo(t, ts, http.MethodDelete, "/api/agent/v1/thread/"+goodThreadID, tok, ""); code != http.StatusOK {
		t.Fatal("delete failed")
	}
	if code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/thread/"+goodThreadID, tok, ""); code != http.StatusNotFound {
		t.Fatal("record survived delete")
	}
	idx.Threads = nil
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/threads", tok, "", &idx)
	if len(idx.Threads) != 1 || idx.Threads[0].ThreadID != "thread-abcd-0002" {
		t.Fatalf("threads after delete = %+v, want only thread-abcd-0002", idx.Threads)
	}
}

// TestAPIThreadIsolation proves a user's threads are invisible to other users.
func TestAPIThreadIsolation(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	bobTok := mkUser(t, acc, "bob")

	body := `{"record":{"threadId":"` + goodThreadID + `","title":"alice-only","updatedAt":1}}`
	if code, _ := apiDo(t, ts, http.MethodPut, "/api/agent/v1/thread/"+goodThreadID, aliceTok, body); code != http.StatusOK {
		t.Fatal("alice put failed")
	}
	// bob cannot read alice's thread (his namespace has no such record).
	if code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/thread/"+goodThreadID, bobTok, ""); code != http.StatusNotFound {
		t.Fatal("bob read alice thread, want 404")
	}
	// bob's derived index is independent (empty), not alice's.
	var idx struct {
		Threads []json.RawMessage `json:"threads"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/threads", bobTok, "", &idx)
	if len(idx.Threads) != 0 {
		t.Fatalf("bob threads = %v, want empty (isolated)", idx.Threads)
	}
}

// TestAPIThreadBadID rejects unsafe/short thread ids before any filesystem use.
func TestAPIThreadBadID(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	for _, id := range []string{"short", "../escape", "has/slash", "bad.id.dots"} {
		code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/thread/"+id, tok, "")
		if code != http.StatusBadRequest && code != http.StatusNotFound {
			t.Errorf("bad id %q = %d, want 400/404", id, code)
		}
	}
}
