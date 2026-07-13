package hub

// Provisioning reliability tests, built around a fake Sprites API. The fake
// does more than canned responses: for the chunk-upload protocol it actually
// emulates the sprite's shell (heredoc trailing newlines, chunk files, base64
// -d | gunzip assembly), so these tests prove the bytes that land on the
// "sprite" are identical to the source binary — including across injected
// failures and retries.

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- fake Sprites API ----

type fakeSprites struct {
	t   *testing.T
	srv *httptest.Server

	mu            sync.Mutex
	spriteCreates int
	execs         []string
	kindCount     map[string]int
	spriteURL     string            // "" -> GET /sprites/<name> 404s
	files         map[string][]byte // simulated on-sprite filesystem
	respond       func(kind string, n int, script string) (int, string)
}

func newFakeSprites(t *testing.T) *fakeSprites {
	f := &fakeSprites{t: t, kindCount: map[string]int{}, files: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSprites) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/sprites":
		f.mu.Lock()
		f.spriteCreates++
		f.mu.Unlock()
		fmt.Fprint(w, `{}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exec"):
		body, _ := io.ReadAll(r.Body)
		script := string(body)
		kind := execScriptKind(script)
		f.mu.Lock()
		f.execs = append(f.execs, script)
		n := f.kindCount[kind]
		f.kindCount[kind] = n + 1
		respond := f.respond
		f.mu.Unlock()
		status, out := 200, "ok\n"
		if respond != nil {
			status, out = respond(kind, n, script)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, out)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sprites/"):
		f.mu.Lock()
		url := f.spriteURL
		f.mu.Unlock()
		if url == "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"url":%q}`, url)
	default:
		http.NotFound(w, r)
	}
}

// execScriptKind fingerprints the scripts AgentManager sends so tests can
// program per-kind behavior without caring about global call order.
func execScriptKind(s string) string {
	switch {
	case strings.Contains(s, "cat > /tmp/afs-up-"):
		return "chunk"
	case strings.Contains(s, "tr -d '\\n' | base64 -d"):
		return "assemble"
	case strings.Contains(s, "AFSRUNEOF"):
		return "start"
	case strings.Contains(s, "AFS_RC=$(cat"):
		return "probe"
	case strings.Contains(s, "afs-install.log"):
		return "afs-install"
	case strings.Contains(s, "AFS_BUNDLE_OK"):
		return "bundle"
	case strings.Contains(s, "AFS_ENV_UPDATED"):
		return "envupdate"
	case strings.Contains(s, "ls -1 /home/sprite/workspace"):
		return "scan"
	default:
		return "other"
	}
}

func (f *fakeSprites) kindCalls(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.kindCount[kind]
}

func (f *fakeSprites) creates() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spriteCreates
}

func (f *fakeSprites) setSpriteURL(url string) {
	f.mu.Lock()
	f.spriteURL = url
	f.mu.Unlock()
}

func (f *fakeSprites) file(path string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.files[path]
}

func (f *fakeSprites) scriptsOfKind(kind string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.execs {
		if execScriptKind(s) == kind {
			out = append(out, s)
		}
	}
	return out
}

// ---- shell emulation for the chunk-upload protocol ----

var (
	chunkWriteRe     = regexp.MustCompile(`(?s)^mkdir -p \S+ && cat > (\S+) <<'AFSCHUNK'\n(.*)\nAFSCHUNK\nsha256sum`)
	assembleFilesRe  = regexp.MustCompile(`cat ((?:\S+ )+)\| tr -d`)
	assembleRemoteRe = regexp.MustCompile(`> (\S+)\.tmp\n`)
	assembleWantRe   = regexp.MustCompile(`!= "([0-9a-f]{64})"`)
)

func (f *fakeSprites) interpretChunk(script string) (int, string) {
	m := chunkWriteRe.FindStringSubmatch(script)
	if m == nil {
		return 500, "unrecognized chunk script"
	}
	content := []byte(m[2] + "\n") // the heredoc write appends a newline
	f.mu.Lock()
	f.files[m[1]] = content
	f.mu.Unlock()
	sum := sha256.Sum256(content)
	return 200, hex.EncodeToString(sum[:]) + "\n"
}

func (f *fakeSprites) interpretAssemble(script string) (int, string) {
	fm := assembleFilesRe.FindStringSubmatch(script)
	rm := assembleRemoteRe.FindStringSubmatch(script)
	wm := assembleWantRe.FindStringSubmatch(script)
	if fm == nil || rm == nil || wm == nil {
		return 500, "unrecognized assemble script"
	}
	remote, want := rm[1], wm[1]
	f.mu.Lock()
	defer f.mu.Unlock()
	if cur, ok := f.files[remote]; ok {
		sum := sha256.Sum256(cur)
		if hex.EncodeToString(sum[:]) == want {
			return 200, want + "\n" // idempotent early-exit branch
		}
	}
	var b64 strings.Builder
	for _, file := range strings.Fields(fm[1]) {
		chunk, ok := f.files[file]
		if !ok {
			return 200, "cat: " + file + ": No such file or directory\n"
		}
		b64.Write(bytes.ReplaceAll(chunk, []byte("\n"), nil))
	}
	gz, err := base64.StdEncoding.DecodeString(b64.String())
	if err != nil {
		return 200, "base64: invalid input\n"
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return 200, "gunzip: not in gzip format\n"
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return 200, "gunzip: unexpected end of file\n"
	}
	f.files[remote] = raw
	sum := sha256.Sum256(raw)
	return 200, hex.EncodeToString(sum[:]) + "\n"
}

// ---- helpers ----

func newTestAgentManager(t *testing.T, f *fakeSprites, accounts *AccountStore) *AgentManager {
	t.Helper()
	t.Setenv("CHAT_REASONING_EFFORT", "high")
	m := NewAgentManager("sprites-test-token", "openai-test-key", "", "https://hub.example", accounts, log.New(io.Discard, "", 0))
	m.spritesBase = f.srv.URL
	m.sleep = func(time.Duration) {}
	m.pollInterval = time.Millisecond
	m.bootBudget = 2 * time.Second
	m.healthWait = time.Second
	m.wakeGrace = 50 * time.Millisecond
	return m
}

func newTestAccountsWithUser(t *testing.T, user string) *AccountStore {
	t.Helper()
	a, err := OpenAccounts(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateUser(user, user+"@example.com", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	return a
}

func waitFor(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("timed out waiting for " + msg)
}

func patNames(t *testing.T, a *AccountStore, user string) map[string]int {
	t.Helper()
	pats, err := a.ListPATs(user)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]int{}
	for _, p := range pats {
		names[p.Name]++
	}
	return names
}

func healthServer(t *testing.T, ok *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---- exec client semantics ----

func TestExecRejectsNon2xx(t *testing.T) {
	f := newFakeSprites(t)
	m := newTestAgentManager(t, f, nil)

	f.respond = func(kind string, n int, script string) (int, string) { return 502, "upstream connect error" }
	out, err := m.exec("afs-user-alice", "echo hi", time.Second)
	if err == nil {
		t.Fatalf("502 response treated as success, out=%q", out)
	}
	if !execOutcomeUnknown(err) {
		t.Fatalf("5xx must be outcome-unknown (script may have run): %v", err)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error should carry the status for diagnostics: %v", err)
	}

	f.respond = func(kind string, n int, script string) (int, string) { return 404, "no such sprite" }
	if _, err := m.exec("afs-user-alice", "echo hi", time.Second); err == nil {
		t.Fatal("404 response treated as success")
	} else if execOutcomeUnknown(err) {
		t.Fatalf("4xx means the API refused the request — outcome is KNOWN: %v", err)
	}
}

func TestExecPropagatesTruncatedBody(t *testing.T) {
	// A server that promises 4096 bytes and delivers 3 — the old client
	// returned ("AFS", nil) here and callers took it as real output.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("AFS"))
	}))
	defer srv.Close()

	m := NewAgentManager("tok", "oai", "", "https://hub.example", nil, log.New(io.Discard, "", 0))
	m.spritesBase = srv.URL
	_, err := m.exec("afs-user-alice", "echo hi", time.Second)
	if err == nil {
		t.Fatal("truncated response body treated as success")
	}
	if !execOutcomeUnknown(err) {
		t.Fatalf("a truncated body leaves the remote outcome unknown: %v", err)
	}
}

func TestExecVerifiedRequiresSentinel(t *testing.T) {
	f := newFakeSprites(t)
	m := newTestAgentManager(t, f, nil)
	f.respond = func(kind string, n int, script string) (int, string) { return 200, "partial output, no marker" }
	if _, err := m.execVerified("s", "echo AFS_X", "AFS_X", time.Second, 2); err == nil {
		t.Fatal("2xx without the sentinel must not count as success")
	}
	if got := f.kindCalls("other"); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

// ---- chunked upload protocol ----

func TestUploadAfsChunkedRetrySurvivesTransientFailure(t *testing.T) {
	f := newFakeSprites(t)
	m := newTestAgentManager(t, f, nil)

	bin := make([]byte, 1_600_000) // incompressible -> ~2.1MB base64 -> 4 chunks
	if _, err := rand.Read(bin); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(t.TempDir(), "afs-linux")
	if err := os.WriteFile(binPath, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	m.AfsBin = binPath

	var failedOnce atomic.Bool
	f.respond = func(kind string, n int, script string) (int, string) {
		switch kind {
		case "chunk":
			if strings.Contains(script, "/c0001") && failedOnce.CompareAndSwap(false, true) {
				return 502, "edge hiccup" // transient loss of one chunk write
			}
			return f.interpretChunk(script)
		case "assemble":
			return f.interpretAssemble(script)
		}
		return 200, "ok\n"
	}

	if err := m.uploadAfs("afs-user-alice"); err != nil {
		t.Fatalf("upload failed despite retryable error: %v", err)
	}
	got := f.file("/home/sprite/.local/bin/afs")
	if !bytes.Equal(got, bin) {
		t.Fatalf("assembled binary differs from source (%d vs %d bytes) — chunk protocol corrupted data", len(got), len(bin))
	}
	if !failedOnce.Load() {
		t.Fatal("test never exercised the injected chunk failure")
	}

	// Re-running the whole upload (e.g. a retried provision) must be a no-op
	// yielding the identical binary — the assembly early-exits on matching hash.
	before := f.kindCalls("assemble")
	if err := m.uploadAfs("afs-user-alice"); err != nil {
		t.Fatalf("re-upload not idempotent: %v", err)
	}
	if !bytes.Equal(f.file("/home/sprite/.local/bin/afs"), bin) {
		t.Fatal("re-upload corrupted the installed binary")
	}
	if f.kindCalls("assemble") <= before {
		t.Fatal("expected a second assemble call")
	}
}

func TestUploadAfsDetectsCorruptedChunkAtAssembly(t *testing.T) {
	f := newFakeSprites(t)
	m := newTestAgentManager(t, f, nil)

	bin := make([]byte, 900_000)
	if _, err := rand.Read(bin); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(t.TempDir(), "afs-linux")
	if err := os.WriteFile(binPath, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	m.AfsBin = binPath

	// Silent corruption: the chunk write "succeeds" (returns the expected
	// checksum) but stores flipped bytes. The final source-hash verification
	// must catch it — never a partial/corrupt binary reported as installed.
	f.respond = func(kind string, n int, script string) (int, string) {
		switch kind {
		case "chunk":
			status, out := f.interpretChunk(script)
			if strings.Contains(script, "/c0000") {
				if m := chunkWriteRe.FindStringSubmatch(script); m != nil {
					f.mu.Lock()
					b := append([]byte(nil), f.files[m[1]]...)
					if len(b) > 0 {
						if b[0] == 'A' {
							b[0] = 'B'
						} else {
							b[0] = 'A'
						}
					}
					f.files[m[1]] = b
					f.mu.Unlock()
				}
			}
			return status, out
		case "assemble":
			return f.interpretAssemble(script)
		}
		return 200, "ok\n"
	}

	err := m.uploadAfs("afs-user-alice")
	if err == nil {
		t.Fatal("corrupted upload reported as success")
	}
	if !strings.Contains(err.Error(), "assemble") {
		t.Fatalf("expected assembly verification failure, got: %v", err)
	}
}

// ---- provisioning flow ----

// provisionRespond programs the fake for a full provisioning run whose boot
// probes are supplied by bootProbe (called with the waitDetached probe index,
// i.e. excluding the initial adoption probe).
func provisionRespond(f *fakeSprites, onStart func(), bootProbe func(n int) (int, string)) {
	f.respond = func(kind string, n int, script string) (int, string) {
		switch kind {
		case "probe":
			if n == 0 {
				return 200, "AFS_STAGE=\nAFS_DEAD\nAFS_LOG_BEGIN\n" // adoption check: nothing running
			}
			return bootProbe(n - 1)
		case "bundle":
			return 200, "AFS_BUNDLE_OK\n"
		case "afs-install":
			return 200, "AFS_INSTALLED\n"
		case "start":
			if onStart != nil {
				onStart()
			}
			return 200, "AFS_START_OK\n"
		}
		return 200, "ok\n"
	}
}

func TestProvisionUserSuccessSweepsStalePATs(t *testing.T) {
	f := newFakeSprites(t)
	accounts := newTestAccountsWithUser(t, "alice")
	m := newTestAgentManager(t, f, accounts)

	// Leftovers from the incident era: auto-minted PATs that were never revoked,
	// plus a user-created PAT that must survive.
	for _, name := range []string{"agent-user", "agent-user", "agent-reconcile", "agent-sprite:notes", "laptop"} {
		if _, err := accounts.CreatePAT("alice", name); err != nil {
			t.Fatal(err)
		}
	}

	var healthOK atomic.Bool
	agent := healthServer(t, &healthOK)
	provisionRespond(f, func() { f.setSpriteURL(agent.URL); healthOK.Store(true) }, func(n int) (int, string) {
		if n == 0 {
			return 200, "AFS_STAGE=deps\nAFS_ALIVE\n"
		}
		return 200, "AFS_RC=0\nAFS_STAGE=done\nAFS_LOG_BEGIN\nAFS_BOOT_OK\n"
	})

	if _, ready := m.EnsureUser("alice", []RepoRef{{"alice", "notes"}}); ready {
		t.Fatal("no sprite exists yet — must not be ready")
	}
	waitFor(t, 5*time.Second, "provisioning success", func() bool {
		st := m.ProvisionStatus("alice")
		return !st.Running && st.LastError == "" && st.Attempt == 0
	})

	names := patNames(t, accounts, "alice")
	if names["agent-user"] != 1 || names["agent-reconcile"] != 0 || names["agent-sprite:notes"] != 0 {
		t.Fatalf("stale automatic PATs not swept: %v", names)
	}
	if names["laptop"] != 1 {
		t.Fatalf("user-created PAT must never be swept: %v", names)
	}
	if got := f.creates(); got != 1 {
		t.Fatalf("expected exactly 1 sprite create, got %d", got)
	}
}

func TestLostBootResponseWithHealthyServiceIsSuccess(t *testing.T) {
	f := newFakeSprites(t)
	accounts := newTestAccountsWithUser(t, "alice")
	m := newTestAgentManager(t, f, accounts)

	// Every waitDetached probe is lost (edge 502s) — the exact incident shape:
	// the hub can't see the boot, but the boot succeeded and the service is up.
	var healthOK atomic.Bool
	healthOK.Store(true)
	agent := healthServer(t, &healthOK)
	provisionRespond(f, func() { f.setSpriteURL(agent.URL) }, func(n int) (int, string) {
		return 502, "edge lost the response"
	})

	m.EnsureUser("alice", nil)
	waitFor(t, 5*time.Second, "success via health arbitration", func() bool {
		st := m.ProvisionStatus("alice")
		return !st.Running && st.LastError == "" && st.Attempt == 0
	})

	// The PAT the boot was started with is in service — it must survive.
	if names := patNames(t, accounts, "alice"); names["agent-user"] != 1 {
		t.Fatalf("healthy-but-response-lost boot lost its PAT: %v", names)
	}
	if got := f.kindCalls("start"); got != 1 {
		t.Fatalf("healthy service must not be re-provisioned, saw %d boot starts", got)
	}
}

func TestBootFailureRevokesPATAndBacksOff(t *testing.T) {
	f := newFakeSprites(t)
	accounts := newTestAccountsWithUser(t, "alice")
	m := newTestAgentManager(t, f, accounts)

	var healthOK atomic.Bool // stays false: service never up
	agent := healthServer(t, &healthOK)
	provisionRespond(f, func() { f.setSpriteURL(agent.URL) }, func(n int) (int, string) {
		return 200, "AFS_RC=1\nAFS_STAGE=deps\nAFS_LOG_BEGIN\nnpm ERR! EAI_AGAIN registry.npmjs.org\n"
	})

	m.EnsureUser("alice", nil)
	waitFor(t, 5*time.Second, "attempt failure", func() bool {
		st := m.ProvisionStatus("alice")
		return !st.Running && st.LastError != ""
	})

	st := m.ProvisionStatus("alice")
	if !strings.Contains(st.LastError, "rc=1") || !strings.Contains(st.LastError, "npm ERR!") {
		t.Fatalf("failure should carry the boot log tail for diagnosis: %q", st.LastError)
	}
	if !st.NextRetry.After(time.Now()) {
		t.Fatal("failed attempt must arm the backoff gate")
	}
	if names := patNames(t, accounts, "alice"); names["agent-user"] != 0 {
		t.Fatalf("definite boot failure must revoke the attempt's PAT: %v", names)
	}

	// The starting page refreshes every 4s. Refreshes during backoff must not
	// start new attempts or mint new credentials.
	starts := f.kindCalls("start")
	for i := 0; i < 5; i++ {
		if _, ready := m.EnsureUser("alice", nil); ready {
			t.Fatal("unhealthy sprite reported ready")
		}
	}
	time.Sleep(20 * time.Millisecond)
	if got := f.kindCalls("start"); got != starts {
		t.Fatalf("meta-refresh during backoff started %d extra attempts", got-starts)
	}
	if names := patNames(t, accounts, "alice"); names["agent-user"] != 0 {
		t.Fatalf("refresh minted PATs during backoff: %v", names)
	}

	// Explicit retry bypasses the backoff and starts exactly one new attempt.
	m.RetryProvision("alice")
	m.EnsureUser("alice", nil)
	waitFor(t, 5*time.Second, "second attempt failure", func() bool {
		st := m.ProvisionStatus("alice")
		return !st.Running && st.Attempt == 2
	})
	if got := f.kindCalls("start"); got != starts+1 {
		t.Fatalf("explicit retry should start exactly one attempt, saw %d", got-starts)
	}
}

func TestEnsureUserSingleFlight(t *testing.T) {
	f := newFakeSprites(t)
	accounts := newTestAccountsWithUser(t, "alice")
	m := newTestAgentManager(t, f, accounts)

	release := make(chan struct{})
	var healthOK atomic.Bool
	agent := healthServer(t, &healthOK)
	f.respond = func(kind string, n int, script string) (int, string) {
		switch kind {
		case "probe":
			if n == 0 {
				return 200, "AFS_STAGE=\nAFS_DEAD\nAFS_LOG_BEGIN\n"
			}
			return 200, "AFS_RC=0\nAFS_STAGE=done\nAFS_LOG_BEGIN\nAFS_BOOT_OK\n"
		case "bundle":
			<-release // hold every attempt inside the bundle stage
			return 200, "AFS_BUNDLE_OK\n"
		case "afs-install":
			return 200, "AFS_INSTALLED\n"
		case "start":
			f.setSpriteURL(agent.URL)
			healthOK.Store(true)
			return 200, "AFS_START_OK\n"
		}
		return 200, "ok\n"
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.EnsureUser("alice", nil) // ten tabs of the 4s-refresh starting page
		}()
	}
	wg.Wait()

	waitFor(t, 2*time.Second, "the single attempt to reach the bundle stage", func() bool {
		return f.kindCalls("bundle") >= 1
	})
	st := m.ProvisionStatus("alice")
	if !st.Running || st.Attempt != 1 {
		t.Fatalf("expected exactly one running attempt, got %+v", st)
	}
	if got := f.creates(); got != 1 {
		t.Fatalf("concurrent EnsureUser created %d sprites", got)
	}

	close(release)
	waitFor(t, 5*time.Second, "provisioning success", func() bool {
		st := m.ProvisionStatus("alice")
		return !st.Running && st.LastError == "" && st.Attempt == 0
	})
	if names := patNames(t, accounts, "alice"); names["agent-user"] != 1 {
		t.Fatalf("single-flight attempt should mint exactly one PAT: %v", names)
	}
}

func TestExistingSpriteAdoptedAfterWakeGrace(t *testing.T) {
	f := newFakeSprites(t)
	accounts := newTestAccountsWithUser(t, "alice")
	m := newTestAgentManager(t, f, accounts)

	// The sprite exists (hub restarted mid-boot) and a boot is still running on
	// it. The hub must adopt that run — not push a bundle, not mint a PAT, not
	// start a second boot that would wipe the first one's workspace.
	var healthOK atomic.Bool
	agent := healthServer(t, &healthOK)
	f.setSpriteURL(agent.URL)
	f.respond = func(kind string, n int, script string) (int, string) {
		switch kind {
		case "probe":
			if n == 0 {
				return 200, "AFS_STAGE=deps\nAFS_ALIVE\n" // adoption check: live boot found
			}
			healthOK.Store(true)
			return 200, "AFS_RC=0\nAFS_STAGE=done\nAFS_LOG_BEGIN\nAFS_BOOT_OK\n"
		}
		return 200, "ok\n"
	}

	// First sight of the unhealthy sprite starts the wake grace, not an attempt.
	m.EnsureUser("alice", nil)
	if st := m.ProvisionStatus("alice"); st.Running {
		t.Fatal("an existing sprite must get a wake grace before any attempt")
	}
	time.Sleep(60 * time.Millisecond) // > wakeGrace
	m.EnsureUser("alice", nil)

	waitFor(t, 5*time.Second, "adopted boot to finish", func() bool {
		st := m.ProvisionStatus("alice")
		return !st.Running && st.LastError == "" && st.Attempt == 0
	})
	if got := f.kindCalls("start"); got != 0 {
		t.Fatalf("adoption must not start a second boot, saw %d starts", got)
	}
	if got := f.kindCalls("bundle"); got != 0 {
		t.Fatalf("adoption must not re-push the bundle, saw %d", got)
	}
	if names := patNames(t, accounts, "alice"); len(names) != 0 {
		t.Fatalf("adoption must not mint credentials: %v", names)
	}
}

func TestReconcileUpdatesServiceEnvInPlaceOnConfigDrift(t *testing.T) {
	f := newFakeSprites(t)
	accounts := newTestAccountsWithUser(t, "alice")
	m := newTestAgentManager(t, f, accounts)

	var healthOK atomic.Bool
	healthOK.Store(true)
	agent := healthServer(t, &healthOK)
	f.setSpriteURL(agent.URL)
	f.respond = func(kind string, n int, script string) (int, string) {
		switch kind {
		case "scan":
			// Workspace complete; marker records an older model config.
			return 200, "notes\nAFS_SCAN_DIVIDER\nv1 model=gpt-5.5 effort=medium\nAFS_SCAN_DIVIDER\nAFS_PRESENT\n"
		case "envupdate":
			return 200, "AFS_ENV_UPDATED\n"
		}
		return 200, "ok\n"
	}

	url, ready := m.EnsureUser("alice", []RepoRef{{"alice", "notes"}})
	if !ready || url != agent.URL {
		t.Fatalf("healthy sprite should be ready immediately, got (%q, %v)", url, ready)
	}
	waitFor(t, 5*time.Second, "in-place env update", func() bool {
		return f.kindCalls("envupdate") == 1
	})

	scripts := f.scriptsOfKind("envupdate")
	if len(scripts) != 1 {
		t.Fatalf("expected 1 env update script, got %d", len(scripts))
	}
	up := scripts[0]
	for _, want := range []string{
		"CHAT_MODEL=gpt-5.6-luna", "CHAT_REASONING_EFFORT=high", // the desired config
		"__AFS_TOKEN__",   // placeholder — the real token is spliced sprite-side
		`hub.json`,        // credential source stays on the sprite
		serviceMarkerPath, // marker rewritten so the update is once-only
		"services create agent",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("env update script missing %q", want)
		}
	}
	if strings.Contains(up, "afs_") {
		t.Fatal("env update script must never carry a plaintext PAT")
	}
	// A config-only change must not mint credentials or reprovision.
	if names := patNames(t, accounts, "alice"); len(names) != 0 {
		t.Fatalf("in-place update minted credentials: %v", names)
	}
	if got := f.creates(); got != 0 {
		t.Fatalf("in-place update created %d sprites", got)
	}
	if got := f.kindCalls("start"); got != 0 {
		t.Fatalf("in-place update ran %d full boots", got)
	}
}

// ---- unit-level pieces ----

func TestDecodeExecStream(t *testing.T) {
	// Framed (current sprite runtimes): \x01=stdout line, \x02=stderr line,
	// \x03 + one byte = exit code trailer. Observed live 2026-07-13.
	out, code := decodeExecStream([]byte("\x01AFS_STAGE=deps\n\x01AFS_ALIVE\n\x03\x00"))
	if code != 0 || out != "AFS_STAGE=deps\nAFS_ALIVE\n" {
		t.Fatalf("framed decode = (%q, %d)", out, code)
	}
	out, code = decodeExecStream([]byte("\x01AFS_DEAD\n\x02tail: no such file\n\x03\x01"))
	if code != 1 || !strings.Contains(out, "AFS_DEAD") || !strings.Contains(out, "tail: no such file") {
		t.Fatalf("framed stderr decode = (%q, %d)", out, code)
	}
	// Unframed (older runtimes): pass through untouched, exit unknown.
	out, code = decodeExecStream([]byte("plain output\nAFS_BOOT_OK\n"))
	if code != -1 || out != "plain output\nAFS_BOOT_OK\n" {
		t.Fatalf("unframed decode = (%q, %d)", out, code)
	}
	if out, code = decodeExecStream(nil); out != "" || code != -1 {
		t.Fatalf("empty decode = (%q, %d)", out, code)
	}
}

// The end-to-end run on the real platform (2026-07-13) failed precisely here:
// framed probe output parsed as "no markers at all", so a live boot read as
// dead. Keep the raw observed bytes flowing through the full exec+parse path.
func TestProbeParsingSurvivesFramedResponses(t *testing.T) {
	f := newFakeSprites(t)
	m := newTestAgentManager(t, f, nil)
	f.respond = func(kind string, n int, script string) (int, string) {
		return 200, "\x01AFS_STAGE=deps\n\x01AFS_ALIVE\n\x03\x00"
	}
	p, err := m.probeDetached("afs-user-alice", bootRunBase)
	if err != nil {
		t.Fatal(err)
	}
	if !p.running || p.stage != "deps" || p.done {
		t.Fatalf("framed probe misparsed: %+v", p)
	}
}

func TestParseDetachedProbe(t *testing.T) {
	p := parseDetachedProbe("AFS_RC=0\nAFS_STAGE=done\nAFS_LOG_BEGIN\nline1\nAFS_BOOT_OK\n")
	if !p.done || p.rc != 0 || p.stage != "done" || !strings.Contains(p.logTail, "AFS_BOOT_OK") {
		t.Fatalf("finished probe misparsed: %+v", p)
	}
	p = parseDetachedProbe("AFS_STAGE=clone alice/notes\nAFS_ALIVE\n")
	if p.done || !p.running || p.stage != "clone alice/notes" {
		t.Fatalf("running probe misparsed: %+v", p)
	}
	p = parseDetachedProbe("AFS_STAGE=deps\nAFS_DEAD\nAFS_LOG_BEGIN\nkilled\n")
	if p.done || p.running || !strings.Contains(p.logTail, "killed") {
		t.Fatalf("dead probe misparsed: %+v", p)
	}
	p = parseDetachedProbe("AFS_RC=garbage\n")
	if !p.done || p.rc == 0 {
		t.Fatalf("garbled rc must never parse as success: %+v", p)
	}
}

func TestScrubRedactsSecrets(t *testing.T) {
	in := `clone failed: Authorization: Basic dXNlcjpzZWNyZXQ= and AGENTSFS_LLM_KEY=afs_AbC123xyz789_-Q and header Bearer sk-live-abcdef1234567890`
	out := scrub(in)
	for _, leaked := range []string{"dXNlcjpzZWNyZXQ", "afs_AbC123xyz789", "sk-live-abcdef1234567890"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("scrub leaked %q in %q", leaked, out)
		}
	}
	if !strings.Contains(out, "clone failed") {
		t.Fatalf("scrub destroyed non-secret context: %q", out)
	}
}

func TestProvisionBackoffSchedule(t *testing.T) {
	if provisionBackoff(1) != 15*time.Second || provisionBackoff(5) != 5*time.Minute || provisionBackoff(9) != 10*time.Minute {
		t.Fatal("unexpected backoff schedule")
	}
}

func TestWorkspaceServiceEnvKeepsOperatorKeyOut(t *testing.T) {
	f := newFakeSprites(t)
	m := newTestAgentManager(t, f, nil)
	env := m.workspaceServiceEnv("afs-user-pat", ",AFS_BIN=/home/sprite/.local/bin/afs")
	for _, want := range []string{
		"AGENTSFS_MODE=workspace",
		"CHAT_MODEL=gpt-5.6-luna",
		"CHAT_REASONING_EFFORT=high",
		"AGENTSFS_LLM_BASE_URL=https://hub.example/v1/agent-llm",
		"AGENTSFS_LLM_KEY=afs-user-pat",
		"AFS_BIN=/home/sprite/.local/bin/afs",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("service env missing %q", want)
		}
	}
	if strings.Contains(env, "OPENAI_API_KEY") || strings.Contains(env, m.OpenAIKey) {
		t.Fatal("workspace service env exposes the operator OpenAI key")
	}
}

func TestProvisionStageLabels(t *testing.T) {
	if got := provisionStageLabel("clone alice/notes"); got != "Cloning alice/notes…" {
		t.Fatalf("clone label = %q", got)
	}
	if got := provisionStageLabel("deps"); got != "Installing dependencies…" {
		t.Fatalf("deps label = %q", got)
	}
	if got := provisionStageLabel("mystery-stage"); got != "Setting up…" {
		t.Fatalf("unknown stage label = %q", got)
	}
}
