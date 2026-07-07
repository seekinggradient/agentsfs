package hub

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMetricsSummary(t *testing.T) {
	m, err := OpenMetrics(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()

	// alice: two good calls, bob: one good + one error, plus an OLD alice call
	// that must fall outside a 24h window.
	m.Record(LLMCall{Ts: now, User: "alice", Endpoint: "responses", Model: "gpt-5.1", Status: 200, InputTokens: 1000, OutputTokens: 500, CostUSD: costUSD("gpt-5.1", 1000, 500)})
	m.Record(LLMCall{Ts: now, User: "alice", Endpoint: "responses", Model: "gpt-5.1", Status: 200, InputTokens: 2000, OutputTokens: 0, CostUSD: costUSD("gpt-5.1", 2000, 0)})
	m.Record(LLMCall{Ts: now, User: "bob", Endpoint: "responses", Model: "gpt-4o-mini", Status: 200, InputTokens: 100, OutputTokens: 50, CostUSD: costUSD("gpt-4o-mini", 100, 50)})
	m.Record(LLMCall{Ts: now, User: "bob", Endpoint: "responses", Status: 502})
	m.Record(LLMCall{Ts: now - int64(48*time.Hour/time.Second), User: "alice", Endpoint: "responses", Status: 200, InputTokens: 9999})

	sm, err := m.Summary(24)
	if err != nil {
		t.Fatal(err)
	}
	if sm.TotalCalls != 4 {
		t.Fatalf("TotalCalls = %d, want 4 (old call excluded)", sm.TotalCalls)
	}
	if sm.Errors != 1 {
		t.Fatalf("Errors = %d, want 1", sm.Errors)
	}
	if sm.TotalInput != 3100 || sm.TotalOutput != 550 {
		t.Fatalf("tokens = %d/%d, want 3100/550", sm.TotalInput, sm.TotalOutput)
	}
	// ordered by cost desc → alice first (gpt-5.1 is pricier + more tokens)
	if len(sm.Users) != 2 || sm.Users[0].User != "alice" {
		t.Fatalf("users = %+v, want alice leading", sm.Users)
	}
	if sm.Users[0].Errors != 0 || sm.Users[1].Errors != 1 {
		t.Fatalf("per-user errors wrong: %+v", sm.Users)
	}
}

func TestCostUSD(t *testing.T) {
	// gpt-5.1: $2.5/1M in, $10/1M out
	if got := costUSD("gpt-5.1", 1_000_000, 1_000_000); got != 12.5 {
		t.Fatalf("costUSD exact = %v, want 12.5", got)
	}
	// dated snapshot resolves via longest-prefix
	if got := costUSD("gpt-5.1-2025-11-01", 1_000_000, 0); got != 2.5 {
		t.Fatalf("costUSD prefix = %v, want 2.5", got)
	}
	// unknown model → 0, never a crash
	if got := costUSD("some-future-model", 1000, 1000); got != 0 {
		t.Fatalf("costUSD unknown = %v, want 0", got)
	}
}

func TestMeteringBodyParsesUsage(t *testing.T) {
	// A Responses-style SSE stream whose final event carries usage. The parser
	// should pick the LAST token counts (finals) and the model name.
	body := strings.Join([]string{
		`event: response.created`,
		`data: {"model":"gpt-5.1","usage":{"input_tokens":10,"output_tokens":0}}`,
		`event: response.completed`,
		`data: {"response":{"usage":{"input_tokens":1234,"output_tokens":567}}}`,
		``,
	}, "\n")

	var got usageParse
	mb := &meteringBody{rc: io.NopCloser(strings.NewReader(body)), onClose: func(u usageParse) { got = u }}
	if _, err := io.ReadAll(mb); err != nil {
		t.Fatal(err)
	}
	if err := mb.Close(); err != nil {
		t.Fatal(err)
	}
	if got.in != 1234 || got.out != 567 {
		t.Fatalf("parsed usage = %d/%d, want 1234/567", got.in, got.out)
	}
	if got.model != "gpt-5.1" {
		t.Fatalf("parsed model = %q, want gpt-5.1", got.model)
	}
}
