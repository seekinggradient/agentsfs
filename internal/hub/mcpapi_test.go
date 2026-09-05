package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMCPHub reuses the agent-API harness (Accounts + Metrics + Threads + git)
// and pins PublicBaseURL to the test server so blob URLs and the protected-
// resource-metadata challenge line up with where requests actually land.
func newMCPHub(t *testing.T) (*httptest.Server, *Server, *AccountStore) {
	t.Helper()
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	return ts, srv, acc
}

// bearerRT injects a fixed Authorization: Bearer header on every request — the
// way a consumer client (or its custom http.Client) carries the token to the
// stateless MCP endpoint, where each request is re-verified.
type bearerRT struct {
	token string
	base  http.RoundTripper
}

func (b *bearerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	base := b.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// mcpConnect dials the /mcp endpoint with the SDK client over Streamable HTTP,
// injecting the bearer via a custom http.Client transport. DisableStandaloneSSE
// keeps the client from opening the optional GET stream (stateless mode answers
// GET with 405, which is spec-correct but noise for a test).
func mcpConnect(t *testing.T, ts *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: &bearerRT{token: token}},
		DisableStandaloneSSE: true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// callText runs a tool and returns content[0].text, failing on a protocol error.
func callText(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(testCtx(t), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

// firstText extracts content[0].text from a tool result.
func firstText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("result has no content: %+v", res)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return tc.Text
}

// --- auth challenge --------------------------------------------------------

func TestMCPUnauthenticatedChallenge(t *testing.T) {
	ts, _, _ := newMCPHub(t)

	// No bearer → 401 with a WWW-Authenticate that points at the /mcp PRM path so
	// a client can discover the authorization server.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token /mcp = %d, want 401", res.StatusCode)
	}
	wa := res.Header.Get("WWW-Authenticate")
	want := ts.URL + "/.well-known/oauth-protected-resource/mcp"
	if !strings.Contains(wa, `resource_metadata="`+want+`"`) {
		t.Fatalf("WWW-Authenticate = %q, want resource_metadata=%q", wa, want)
	}
}

// --- tools/list: full set + annotations, and scope-gated write -------------

func TestMCPToolsListPATHasAllToolsWithAnnotations(t *testing.T) {
	ts, _, acc := newMCPHub(t)
	tok := mkUser(t, acc, "alice")
	sess := mcpConnect(t, ts, tok)

	res, err := sess.ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		tools[tl.Name] = tl
	}
	for _, want := range []string{"search", "fetch", "list_workspaces", "list_kbs", "tree", "docs", "write"} {
		if tools[want] == nil {
			t.Fatalf("tools/list missing %q; got %v", want, keysOf(tools))
		}
	}
	// Read tools all carry ReadOnlyHint:true (and non-destructive).
	for _, rn := range []string{"search", "fetch", "list_workspaces", "list_kbs", "tree", "docs"} {
		a := tools[rn].Annotations
		if a == nil || !a.ReadOnlyHint {
			t.Fatalf("%s annotations = %+v, want ReadOnlyHint:true", rn, a)
		}
		if a.DestructiveHint == nil || *a.DestructiveHint {
			t.Fatalf("%s DestructiveHint = %v, want false", rn, a.DestructiveHint)
		}
	}
	// The write tool is NOT read-only.
	wa := tools["write"].Annotations
	if wa == nil || wa.ReadOnlyHint {
		t.Fatalf("write annotations = %+v, want ReadOnlyHint:false", wa)
	}
	if wa.OpenWorldHint == nil || *wa.OpenWorldHint {
		t.Fatalf("write OpenWorldHint = %v, want false", wa.OpenWorldHint)
	}
}

func TestMCPReadOnlyTokenHidesWrite(t *testing.T) {
	ts, _, acc := newMCPHub(t)
	mkAccount(t, acc, "alice")
	// Mint an OAuth access token scoped to afs:read only, straight through the
	// store (the simplest read-only seam).
	access, _, err := acc.IssueOAuthTokens("cli_test", "alice", scopeRead)
	if err != nil {
		t.Fatal(err)
	}
	sess := mcpConnect(t, ts, access)

	res, err := sess.ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	for _, tl := range res.Tools {
		if tl.Name == "write" {
			t.Fatal("a read-only connection must not list the write tool")
		}
	}
	// A direct call is rejected too: the tool was never registered on this
	// connection's server, so it is unknown.
	if _, err := sess.CallTool(testCtx(t), &mcp.CallToolParams{
		Name: "write", Arguments: map[string]any{"repo": "alice/kb", "changes": []map[string]any{{"path": "x.md", "content": "y"}}},
	}); err == nil {
		t.Fatal("write on a read-only connection should be rejected")
	}
}

// --- search: cross-repo, dual-encoding, scoping, isolation -----------------

func TestMCPSearchCrossRepoAndIsolation(t *testing.T) {
	ts, srv, acc := newMCPHub(t)
	aliceTok := mkUser(t, acc, "alice")
	bobTok := mkUser(t, acc, "bob")

	seedCommit(t, ts, srv, aliceTok, "alice", "kb1", "", map[string]string{
		"alpha.md": "# Alpha\nmagicword lives in alpha\n",
	}, nil)
	seedCommit(t, ts, srv, aliceTok, "alice", "kb2", "", map[string]string{
		"beta.md": "# Beta\nmagicword also lives in beta\n",
	}, nil)
	// bob's private repo also contains the term but must never appear for alice.
	seedCommit(t, ts, srv, bobTok, "bob", "secret", "", map[string]string{
		"gamma.md": "# Gamma\nmagicword is bob's secret\n",
	}, nil)

	sess := mcpConnect(t, ts, aliceTok)

	// Unscoped search fans across BOTH of alice's workspaces, never touches bob's.
	res := callText(t, sess, "search", map[string]any{"query": "magicword"})
	ids := searchIDs(t, res)
	if !ids["alice/kb1/alpha.md"] || !ids["alice/kb2/beta.md"] {
		t.Fatalf("cross-repo search ids = %v, want both alice workspaces", ids)
	}
	for id := range ids {
		if strings.HasPrefix(id, "bob/") {
			t.Fatalf("search leaked bob's private repo: %v", ids)
		}
	}

	// structuredContent and content[0].text must be the SAME JSON (dual-encode).
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent is %T, want object", res.StructuredContent)
	}
	scResults, ok := sc["results"].([]any)
	if !ok || len(scResults) == 0 {
		t.Fatalf("structuredContent.results = %v", sc["results"])
	}
	first := scResults[0].(map[string]any)
	for _, k := range []string{"id", "title", "url"} {
		if _, present := first[k]; !present {
			t.Fatalf("result missing %q: %v", k, first)
		}
	}
	// The url is an absolute hub blob URL.
	if u, _ := first["url"].(string); !strings.HasPrefix(u, ts.URL+"/") || !strings.Contains(u, "/blob/") {
		t.Fatalf("result url = %q, want an absolute hub blob URL", first["url"])
	}
	// content[0].text is valid JSON that matches structuredContent.
	var textShape struct {
		Results []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &textShape); err != nil {
		t.Fatalf("content[0].text not valid JSON: %v", err)
	}
	if len(textShape.Results) != len(scResults) {
		t.Fatalf("content/structured length mismatch: %d vs %d", len(textShape.Results), len(scResults))
	}
	if textShape.Results[0].ID != first["id"] || textShape.Results[0].URL != first["url"] {
		t.Fatalf("content[0].text does not match structuredContent: %+v vs %v", textShape.Results[0], first)
	}
	// The title format carries the section and the owner/repo.
	if !strings.Contains(textShape.Results[0].Title, " — alice/") {
		t.Fatalf("title = %q, want it to name the owner/repo", textShape.Results[0].Title)
	}

	// Scoped search: only kb1.
	scoped := searchIDs(t, callText(t, sess, "search", map[string]any{"query": "magicword", "repo": "alice/kb1"}))
	if !scoped["alice/kb1/alpha.md"] || scoped["alice/kb2/beta.md"] {
		t.Fatalf("scoped search ids = %v, want only kb1", scoped)
	}

	// Scoping to a repo alice can't read is a tool error, not a leak.
	errRes, err := sess.CallTool(testCtx(t), &mcp.CallToolParams{
		Name: "search", Arguments: map[string]any{"query": "magicword", "repo": "bob/secret"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !errRes.IsError {
		t.Fatal("searching bob's private repo should be a tool error")
	}
}

// searchIDs runs (or reuses) a search result and returns the set of its ids.
func searchIDs(t *testing.T, res *mcp.CallToolResult) map[string]bool {
	t.Helper()
	var shape struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &shape); err != nil {
		t.Fatalf("search result not JSON: %v", err)
	}
	out := map[string]bool{}
	for _, r := range shape.Results {
		out[r.ID] = true
	}
	return out
}

// --- fetch: round-trip + truncation ----------------------------------------

func TestMCPFetchRoundTripAndTruncation(t *testing.T) {
	ts, srv, acc := newMCPHub(t)
	tok := mkUser(t, acc, "alice")
	seedCommit(t, ts, srv, tok, "alice", "kb", "", map[string]string{
		"note.md": "# Note\nthe magicword body\n",
	}, nil)
	sess := mcpConnect(t, ts, tok)

	// An id from search round-trips into fetch.
	ids := searchIDs(t, callText(t, sess, "search", map[string]any{"query": "magicword"}))
	if !ids["alice/kb/note.md"] {
		t.Fatalf("search didn't return the note: %v", ids)
	}
	res := callText(t, sess, "fetch", map[string]any{"id": "alice/kb/note.md"})
	var fetched struct {
		ID       string            `json:"id"`
		Title    string            `json:"title"`
		Text     string            `json:"text"`
		URL      string            `json:"url"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &fetched); err != nil {
		t.Fatalf("fetch text not JSON: %v", err)
	}
	if fetched.Text != "# Note\nthe magicword body\n" {
		t.Fatalf("fetch text = %q, want the seeded content", fetched.Text)
	}
	if fetched.Title != "note.md" || fetched.Metadata["repo"] != "kb" || fetched.Metadata["owner"] != "alice" {
		t.Fatalf("fetch metadata = %+v", fetched)
	}
	if fetched.Metadata["rev"] == "" {
		t.Fatal("fetch metadata should carry the rev")
	}
	// Dual-encode: structuredContent matches.
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok || sc["id"] != "alice/kb/note.md" {
		t.Fatalf("fetch structuredContent = %v", res.StructuredContent)
	}

	// A >100k file comes back truncated with the notice.
	big := strings.Repeat("A", mcpFetchCap+5000)
	seedCommit(t, ts, srv, tok, "alice", "kb", srv.RepoResolve("alice", "kb"),
		map[string]string{"big.md": big}, nil)
	bigRes := callText(t, sess, "fetch", map[string]any{"id": "alice/kb/big.md"})
	var bigFetched struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(firstText(t, bigRes)), &bigFetched); err != nil {
		t.Fatalf("big fetch text not JSON: %v", err)
	}
	if !strings.Contains(bigFetched.Text, "truncated at 100000 chars") {
		t.Fatalf("large fetch should carry the truncation notice; got %d chars", len(bigFetched.Text))
	}
	if len(bigFetched.Text) >= len(big) {
		t.Fatalf("large fetch text was not truncated: %d chars", len(bigFetched.Text))
	}
}

// --- tree, list_workspaces, docs: smoke -------------------------------------------

func TestMCPTreeListDocsSmoke(t *testing.T) {
	ts, srv, acc := newMCPHub(t)
	tok := mkUser(t, acc, "alice")
	seedCommit(t, ts, srv, tok, "alice", "kb", "", map[string]string{
		"AGENTS.md":  "---\ndescription: alice kb\n---\n",
		"notes/a.md": "a\n",
		"notes/b.md": "b\n",
	}, nil)
	sess := mcpConnect(t, ts, tok)

	tree := firstText(t, callText(t, sess, "tree", map[string]any{"repo": "alice/kb"}))
	if !strings.Contains(tree, "notes/") || !strings.Contains(tree, "AGENTS.md") {
		t.Fatalf("tree output = %q", tree)
	}

	list := firstText(t, callText(t, sess, "list_workspaces", map[string]any{}))
	if !strings.Contains(list, "alice/kb") || !strings.Contains(list, "alice kb") {
		t.Fatalf("list_workspaces output = %q", list)
	}

	legacyList := firstText(t, callText(t, sess, "list_kbs", map[string]any{}))
	if legacyList != list {
		t.Fatalf("legacy listing differs from list_workspaces: %q != %q", legacyList, list)
	}

	docs := firstText(t, callText(t, sess, "docs", map[string]any{}))
	if strings.TrimSpace(docs) == "" {
		t.Fatal("docs returned nothing")
	}
}

// --- write: happy path, CAS conflict, read-only rejection ------------------

func TestMCPWriteHappyPath(t *testing.T) {
	ts, srv, acc := newMCPHub(t)
	tok := mkUser(t, acc, "alice")
	rev1 := seedCommit(t, ts, srv, tok, "alice", "kb", "", map[string]string{"note.md": "v1\n"}, nil)
	sess := mcpConnect(t, ts, tok)

	// base_rev omitted → defaults to HEAD; a fast-forward commit lands.
	res := callText(t, sess, "write", map[string]any{
		"repo":    "alice/kb",
		"message": "add hello",
		"changes": []map[string]any{{"path": "hello.md", "content": "hello world\n"}},
	})
	if res.IsError {
		t.Fatalf("write returned an error: %s", firstText(t, res))
	}
	msg := firstText(t, res)
	if !strings.Contains(msg, "Committed") || !strings.Contains(msg, "New HEAD:") {
		t.Fatalf("write result = %q", msg)
	}

	// The commit really landed and HEAD moved.
	content, _, _, err := srv.RepoReadFile("alice", "kb", "", "hello.md")
	if err != nil {
		t.Fatalf("read committed file: %v", err)
	}
	if string(content) != "hello world\n" {
		t.Fatalf("committed content = %q", content)
	}
	if srv.RepoResolve("alice", "kb") == rev1 {
		t.Fatal("HEAD should have advanced past rev1")
	}
}

func TestMCPWriteCASConflict(t *testing.T) {
	ts, srv, acc := newMCPHub(t)
	tok := mkUser(t, acc, "alice")
	rev1 := seedCommit(t, ts, srv, tok, "alice", "kb", "", map[string]string{"note.md": "v1\n"}, nil)
	// A concurrent commit moves HEAD onto note.md, out from under the stale base.
	rev2 := seedCommit(t, ts, srv, tok, "alice", "kb", rev1, map[string]string{"note.md": "v2\n"}, nil)
	if rev1 == rev2 {
		t.Fatal("concurrent commit should have advanced HEAD")
	}

	sess := mcpConnect(t, ts, tok)
	// Write against the STALE base_rev with a change to the same path → conflict.
	res := callText(t, sess, "write", map[string]any{
		"repo":     "alice/kb",
		"base_rev": rev1,
		"changes":  []map[string]any{{"path": "note.md", "content": "v3\n"}},
	})
	// A CAS conflict is NOT a tool error: it returns actionable guidance.
	if res.IsError {
		t.Fatalf("CAS conflict should be a normal result, not an error: %s", firstText(t, res))
	}
	msg := firstText(t, res)
	if !strings.Contains(msg, "Conflict") || !strings.Contains(msg, rev2) {
		t.Fatalf("conflict guidance = %q, want the new HEAD %s", msg, rev2)
	}
	if !strings.Contains(msg, "note.md") {
		t.Fatalf("conflict guidance should name the conflicting path: %q", msg)
	}
}

func TestMCPWriteRejectedForReadOnlyToken(t *testing.T) {
	ts, srv, acc := newMCPHub(t)
	tok := mkUser(t, acc, "alice")
	seedCommit(t, ts, srv, tok, "alice", "kb", "", map[string]string{"note.md": "v1\n"}, nil)

	// A read-only OAuth token: the write tool is absent AND unreachable.
	access, _, err := acc.IssueOAuthTokens("cli_test", "alice", scopeRead)
	if err != nil {
		t.Fatal(err)
	}
	sess := mcpConnect(t, ts, access)
	if _, err := sess.CallTool(testCtx(t), &mcp.CallToolParams{
		Name:      "write",
		Arguments: map[string]any{"repo": "alice/kb", "changes": []map[string]any{{"path": "x.md", "content": "y"}}},
	}); err == nil {
		t.Fatal("write must be rejected for a read-only token")
	}
	// And the file was never written.
	if _, _, _, err := srv.RepoReadFile("alice", "kb", "", "x.md"); err == nil {
		t.Fatal("read-only write must not have created the file")
	}
}

func TestMCPWriteAutoCreatesOwnKB(t *testing.T) {
	ts, srv, acc := newMCPHub(t)
	tok := mkUser(t, acc, "alice")
	sess := mcpConnect(t, ts, tok)

	// First write into a workspace that doesn't exist yet, in alice's OWN namespace:
	// auto-created (mirroring serveGit's owner first-push semantics) and seeded
	// with the contract template, then the change commits on top.
	res := callText(t, sess, "write", map[string]any{
		"repo":    "alice/fresh",
		"message": "first note",
		"changes": []map[string]any{{"path": "ideas.md", "content": "born from MCP\n"}},
	})
	if res.IsError {
		t.Fatalf("auto-create write returned an error: %s", firstText(t, res))
	}
	if msg := firstText(t, res); !strings.Contains(msg, "Created workspace alice/fresh") {
		t.Fatalf("expected creation notice, got %q", msg)
	}
	content, _, _, err := srv.RepoReadFile("alice", "fresh", "", "ideas.md")
	if err != nil || string(content) != "born from MCP\n" {
		t.Fatalf("committed content = %q, err %v", content, err)
	}
	// The workspace was born a real agentsfs instance, not a bare repo.
	if agentsMD, _, _, err := srv.RepoReadFile("alice", "fresh", "", "AGENTS.md"); err != nil || len(agentsMD) == 0 {
		t.Fatalf("auto-created workspace should carry the seeded contract: %v", err)
	}

	// A write into someone ELSE'S nonexistent namespace never creates anything.
	res = callText(t, sess, "write", map[string]any{
		"repo":    "bob/fresh",
		"changes": []map[string]any{{"path": "x.md", "content": "y"}},
	})
	if !res.IsError {
		t.Fatal("cross-namespace write to a nonexistent repo must fail")
	}
	if srv.Storage.Exists("bob", "fresh") {
		t.Fatal("cross-namespace write must not create the repo")
	}
}

func keysOf(m map[string]*mcp.Tool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
