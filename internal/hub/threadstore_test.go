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

// TestAPIThreadEventsIdempotentAppend exercises the append idempotency over HTTP:
// re-POSTing the same absolute range never dupes; a gap is refused.
func TestAPIThreadEventsIdempotentAppend(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	base := "/api/agent/v1/thread/" + goodThreadID + "/events"

	post := func(fromIndex int, events string) map[string]int {
		body := `{"fromIndex":` + itoa(fromIndex) + `,"events":` + events + `}`
		code, b := apiDo(t, ts, http.MethodPost, base, tok, body)
		if code != http.StatusOK {
			t.Fatalf("append = %d (%s)", code, b)
		}
		var out map[string]int
		_ = json.Unmarshal(b, &out)
		return out
	}

	if r := post(0, `[{"type":"a"},{"type":"b"}]`); r["appended"] != 2 || r["total"] != 2 {
		t.Fatalf("first append = %v, want appended 2 total 2", r)
	}
	// Same range again — idempotent, nothing new.
	if r := post(0, `[{"type":"a"},{"type":"b"}]`); r["appended"] != 0 || r["total"] != 2 {
		t.Fatalf("re-append = %v, want appended 0 total 2", r)
	}
	// Contiguous continuation.
	if r := post(2, `[{"type":"c"}]`); r["appended"] != 1 || r["total"] != 3 {
		t.Fatalf("continue = %v, want appended 1 total 3", r)
	}
	// Gap: fromIndex beyond what's on disk — refused.
	if r := post(9, `[{"type":"z"}]`); r["appended"] != 0 || r["total"] != 3 {
		t.Fatalf("gap append = %v, want appended 0 total 3", r)
	}

	// GET returns the contiguous archive.
	var got struct {
		Count  int               `json:"count"`
		Events []json.RawMessage `json:"events"`
	}
	apiJSON(t, ts, http.MethodGet, base, tok, "", &got)
	if got.Count != 3 || len(got.Events) != 3 {
		t.Fatalf("archive = %d events, want 3", got.Count)
	}
}

// TestAPIThreadRecordAndIndex round-trips the opaque record + index blobs.
func TestAPIThreadRecordAndIndex(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")

	// Record absent → 404.
	code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/thread/"+goodThreadID, tok, "")
	if code != http.StatusNotFound {
		t.Fatalf("absent record = %d, want 404", code)
	}
	// PUT then GET the record verbatim.
	rec := `{"threadId":"` + goodThreadID + `","title":"hello","streamIndex":3}`
	if code, _ := apiDo(t, ts, http.MethodPut, "/api/agent/v1/thread/"+goodThreadID, tok, rec); code != http.StatusOK {
		t.Fatalf("put record = %d, want 200", code)
	}
	code, body := apiDo(t, ts, http.MethodGet, "/api/agent/v1/thread/"+goodThreadID, tok, "")
	if code != http.StatusOK || string(body) != rec {
		t.Fatalf("get record = %d %q, want the stored blob", code, body)
	}

	// Index defaults to {} then round-trips.
	code, body = apiDo(t, ts, http.MethodGet, "/api/agent/v1/threads", tok, "")
	if code != http.StatusOK || string(body) != "{}" {
		t.Fatalf("default index = %d %q, want {}", code, body)
	}
	idx := `{"` + goodThreadID + `":{"title":"hello"}}`
	if code, _ := apiDo(t, ts, http.MethodPut, "/api/agent/v1/threads", tok, idx); code != http.StatusOK {
		t.Fatalf("put index = %d, want 200", code)
	}
	code, body = apiDo(t, ts, http.MethodGet, "/api/agent/v1/threads", tok, "")
	if code != http.StatusOK || string(body) != idx {
		t.Fatalf("get index = %d %q, want stored", code, body)
	}

	// DELETE the thread.
	code, body = apiDo(t, ts, http.MethodDelete, "/api/agent/v1/thread/"+goodThreadID, tok, "")
	if code != http.StatusOK {
		t.Fatalf("delete = %d, want 200", code)
	}
	if code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/thread/"+goodThreadID, tok, ""); code != http.StatusNotFound {
		t.Fatalf("record after delete = %d, want 404", code)
	}
}

// TestAPIThreadIsolation proves a user's threads are invisible to other users.
func TestAPIThreadIsolation(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	bobTok := mkUser(t, acc, "bob")

	rec := `{"threadId":"` + goodThreadID + `","secret":"alice-only"}`
	if code, _ := apiDo(t, ts, http.MethodPut, "/api/agent/v1/thread/"+goodThreadID, aliceTok, rec); code != http.StatusOK {
		t.Fatalf("alice put = %d", code)
	}
	// bob cannot read alice's thread (his namespace has no such record).
	if code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/thread/"+goodThreadID, bobTok, ""); code != http.StatusNotFound {
		t.Fatalf("bob read alice thread = %d, want 404", code)
	}
	// bob's index is independent (empty), not alice's.
	code, body := apiDo(t, ts, http.MethodGet, "/api/agent/v1/threads", bobTok, "")
	if code != http.StatusOK || string(body) != "{}" {
		t.Fatalf("bob index = %d %q, want {} (isolated)", code, body)
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

// itoa avoids importing strconv just for the test body.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
