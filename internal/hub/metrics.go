package hub

import (
	"database/sql"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// MetricsStore records every model call the hub proxies for its agent sprites,
// so the operator has one fleet-wide view of usage + cost per user. It's the
// natural chokepoint: all model traffic flows through /v1/agent-llm. Stored in
// its own SQLite file on the same volume (independent of the accounts DB).
type MetricsStore struct {
	db *sql.DB
}

func OpenMetrics(path string) (*MetricsStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // modernc/sqlite: serialize, avoid SQLITE_BUSY
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS llm_calls (
  id            INTEGER PRIMARY KEY,
  ts            INTEGER NOT NULL,
  username      TEXT NOT NULL,
  endpoint      TEXT NOT NULL,
  model         TEXT NOT NULL DEFAULT '',
  status        INTEGER NOT NULL DEFAULT 0,
  latency_ms    INTEGER NOT NULL DEFAULT 0,
  input_tokens  INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  cost_usd      REAL NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_llm_ts ON llm_calls(ts);
CREATE INDEX IF NOT EXISTS idx_llm_user ON llm_calls(username);`); err != nil {
		return nil, err
	}
	return &MetricsStore{db: db}, nil
}

// LLMCall is one proxied model call.
type LLMCall struct {
	Ts                        int64
	User, Endpoint, Model     string
	Status, LatencyMs         int
	InputTokens, OutputTokens int
	CostUSD                   float64
}

func (m *MetricsStore) Record(c LLMCall) {
	if m == nil {
		return
	}
	_, _ = m.db.Exec(
		`INSERT INTO llm_calls(ts,username,endpoint,model,status,latency_ms,input_tokens,output_tokens,cost_usd)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		c.Ts, c.User, c.Endpoint, c.Model, c.Status, c.LatencyMs, c.InputTokens, c.OutputTokens, c.CostUSD)
}

// UserUsage is one row of the per-user breakdown.
type UserUsage struct {
	User                                        string
	Calls, Errors, InputTokens, OutputTokens    int
	CostUSD                                     float64
}

// MetricsSummary is the aggregate view over a window.
type MetricsSummary struct {
	SinceHours                                       int
	TotalCalls, Errors, TotalInput, TotalOutput      int
	TotalCost                                        float64
	Users                                            []UserUsage
}

func (m *MetricsStore) Summary(sinceHours int) (MetricsSummary, error) {
	out := MetricsSummary{SinceHours: sinceHours}
	if m == nil {
		return out, nil
	}
	since := time.Now().Add(-time.Duration(sinceHours) * time.Hour).Unix()
	rows, err := m.db.Query(`
SELECT username,
       COUNT(*),
       SUM(CASE WHEN status >= 400 OR status = 0 THEN 1 ELSE 0 END),
       COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cost_usd),0)
FROM llm_calls WHERE ts >= ? GROUP BY username ORDER BY SUM(cost_usd) DESC`, since)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var u UserUsage
		if err := rows.Scan(&u.User, &u.Calls, &u.Errors, &u.InputTokens, &u.OutputTokens, &u.CostUSD); err != nil {
			continue
		}
		out.Users = append(out.Users, u)
		out.TotalCalls += u.Calls
		out.Errors += u.Errors
		out.TotalInput += u.InputTokens
		out.TotalOutput += u.OutputTokens
		out.TotalCost += u.CostUSD
	}
	return out, nil
}

// --- streaming usage extraction + pricing ---------------------------------

// $ per 1M tokens, matched to agentsfs-chat src/pricing.ts.
var modelPrices = map[string][2]float64{
	"gpt-5.1":     {2.5, 10},
	"gpt-5":       {2.5, 10},
	"gpt-4.1":     {2.0, 8},
	"gpt-4o-mini": {0.15, 0.6},
}

func costUSD(model string, in, out int) float64 {
	p, ok := modelPrices[model]
	if !ok { // longest-prefix for dated snapshots (gpt-5.1-2025-…)
		for k, v := range modelPrices {
			if strings.HasPrefix(model, k) {
				p, ok = v, true
				break
			}
		}
	}
	if !ok {
		return 0
	}
	return (float64(in)*p[0] + float64(out)*p[1]) / 1_000_000
}

var (
	reInTok  = regexp.MustCompile(`"(?:input_tokens|prompt_tokens)":\s*(\d+)`)
	reOutTok = regexp.MustCompile(`"(?:output_tokens|completion_tokens)":\s*(\d+)`)
	reModel  = regexp.MustCompile(`"model":\s*"([^"]+)"`)
)

// usage parsed out of a proxied model response.
type usageParse struct {
	in, out int
	model   string
}

// meteringBody wraps a streamed response body: it keeps a rolling tail (the
// usage totals arrive in the final SSE event / JSON), and calls onClose with the
// parsed usage when the stream finishes. Transparent to the proxy — Read returns
// the same bytes, so streaming is unaffected.
type meteringBody struct {
	rc      io.ReadCloser
	tail    []byte
	onClose func(usageParse)
	once    sync.Once
}

const meteringTailCap = 128 * 1024

func (b *meteringBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.tail = append(b.tail, p[:n]...)
		if len(b.tail) > meteringTailCap {
			b.tail = b.tail[len(b.tail)-meteringTailCap:]
		}
	}
	return n, err
}

func (b *meteringBody) Close() error {
	b.once.Do(func() {
		u := usageParse{}
		if mm := reInTok.FindAllSubmatch(b.tail, -1); len(mm) > 0 {
			u.in, _ = strconv.Atoi(string(mm[len(mm)-1][1]))
		}
		if mm := reOutTok.FindAllSubmatch(b.tail, -1); len(mm) > 0 {
			u.out, _ = strconv.Atoi(string(mm[len(mm)-1][1]))
		}
		if mm := reModel.FindSubmatch(b.tail); mm != nil {
			u.model = string(mm[1])
		}
		b.onClose(u)
	})
	return b.rc.Close()
}
