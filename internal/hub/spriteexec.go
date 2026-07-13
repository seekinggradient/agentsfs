package hub

// Sprite exec transport layer. Provisioning reliability lives or dies here:
// the July 2026 reprovision incident traced back to an exec client that
// treated any HTTP response — 502 error pages, truncated bodies — as the
// script's output, and to an append-based chunk uploader whose lost writes
// only surfaced minutes later as a final checksum mismatch. This file gives
// provisioning three primitives with honest failure semantics:
//
//   - exec/execVerified: non-2xx and body-read failures are errors, classified
//     by whether the remote script may have run anyway (execOutcomeUnknown).
//   - uploadFileChunks: indexed, checksum-acknowledged chunk files (never
//     appends), safe to retry or re-run at any point.
//   - startDetached/waitDetached: long scripts run under a lock on the sprite
//     itself, decoupled from any single HTTP response; the hub polls short
//     status probes, so a lost response no longer means a lost boot.

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// execError classifies a failed sprite exec. unknown distinguishes "the API
// rejected this before it ran" from "the response was lost — the script may
// have run (or still be running)"; callers must not treat the latter as proof
// of remote failure.
type execError struct {
	status  int  // HTTP status when a complete response was received (0 otherwise)
	unknown bool // the remote script may have run despite the error
	msg     string
	cause   error
}

func (e *execError) Error() string {
	var b strings.Builder
	b.WriteString("sprite exec: ")
	b.WriteString(e.msg)
	if e.status != 0 {
		fmt.Fprintf(&b, " (http %d)", e.status)
	}
	if e.cause != nil {
		fmt.Fprintf(&b, ": %v", e.cause)
	}
	return b.String()
}

func (e *execError) Unwrap() error { return e.cause }

// execOutcomeUnknown reports whether err leaves open the possibility that the
// remote command ran anyway (timeout, connection loss, truncated body, 5xx).
func execOutcomeUnknown(err error) bool {
	var ee *execError
	return errors.As(err, &ee) && ee.unknown
}

var (
	scrubTokenRe  = regexp.MustCompile(`afs_[A-Za-z0-9_-]{8,}`)
	scrubAuthRe   = regexp.MustCompile(`(?i)(authorization: *(?:basic|bearer) +)[A-Za-z0-9+/=_.-]+`)
	scrubBearerRe = regexp.MustCompile(`(?i)\bbearer +[A-Za-z0-9+/=_.-]{16,}`)
)

// scrub redacts credential-shaped strings (PATs, Authorization headers) from
// text that is about to be logged or stored as a user-visible error.
func scrub(s string) string {
	s = scrubTokenRe.ReplaceAllString(s, "afs_[redacted]")
	s = scrubAuthRe.ReplaceAllString(s, "${1}[redacted]")
	s = scrubBearerRe.ReplaceAllString(s, "bearer [redacted]")
	return s
}

// snippet flattens s to one short line for diagnostics.
func snippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}

func (m *AgentManager) api() string {
	if m.spritesBase != "" {
		return m.spritesBase
	}
	return spritesAPI
}

// exec runs a shell script inside the sprite and returns its combined output.
// A non-2xx status or a body-read failure is an error — otherwise a proxy
// error page or truncated body is indistinguishable from the script's real
// output (the root cause of the silent chunk-upload corruption and the
// "err=<nil> out=" boot failures in the reprovision incident).
func (m *AgentManager) exec(name, script string, timeout time.Duration) (string, error) {
	req, err := http.NewRequest(http.MethodPost, m.api()+"/sprites/"+name+"/exec?cmd=sh&stdin=true", strings.NewReader(script))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+m.Token)
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		// The request may have reached the sprite before the timeout or
		// connection loss — the script could still run to completion there.
		return "", &execError{unknown: true, msg: "request failed", cause: err}
	}
	defer resp.Body.Close()
	out, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", &execError{unknown: true, msg: "response body truncated", cause: readErr}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 4xx: the API refused the request and the script never ran.
		// 5xx: the script may have run behind the failed response.
		return "", &execError{status: resp.StatusCode, unknown: resp.StatusCode >= 500, msg: snippet(scrub(string(out)))}
	}
	return string(out), nil
}

// execVerified runs an IDEMPOTENT script whose success is proven by sentinel
// appearing in its output, retrying transient failures. Requiring the sentinel
// (rather than a nil error) means a lost or garbled response can never be
// recorded as success — the retry simply re-runs the script, and idempotency
// makes that safe.
func (m *AgentManager) execVerified(name, script, sentinel string, timeout time.Duration, tries int) (string, error) {
	var lastErr error
	for i := 0; i < tries; i++ {
		if i > 0 {
			m.sleep(time.Duration(i*i) * time.Second)
		}
		out, err := m.exec(name, script, timeout)
		if err != nil {
			lastErr = err
			continue
		}
		if strings.Contains(out, sentinel) {
			return out, nil
		}
		lastErr = fmt.Errorf("output missing %q: %s", sentinel, snippet(scrub(out)))
	}
	return "", lastErr
}

// uploadFileChunks ships raw bytes to remotePath (made executable) through the
// exec API, whose request body caps out around a few MB. The payload is
// gzip+base64'd and split into indexed chunk FILES — written whole, never
// appended — so a retried or duplicated exec rewrites the same bytes to the
// same path instead of corrupting a stream. Each chunk is acknowledged by
// checksum before the next is sent, and the assembled file must match the
// source hash before it atomically replaces remotePath. The staging dir is
// content-addressed, so an interrupted attempt's chunks are safely reused.
func (m *AgentManager) uploadFileChunks(name, remotePath string, raw []byte) error {
	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if _, err := zw.Write(raw); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	b64 := base64.StdEncoding.EncodeToString(gz.Bytes())

	rawSum := sha256.Sum256(raw)
	want := hex.EncodeToString(rawSum[:])
	dir := "/tmp/afs-up-" + want[:12]

	const chunkSize = 700_000
	total := (len(b64) + chunkSize - 1) / chunkSize
	files := make([]string, 0, total)
	for i := 0; i*chunkSize < len(b64); i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(b64) {
			end = len(b64)
		}
		chunk := b64[start:end]
		file := fmt.Sprintf("%s/c%04d", dir, i)
		files = append(files, file)
		chunkSum := sha256.Sum256([]byte(chunk + "\n")) // the heredoc write appends the newline
		script := "mkdir -p " + dir + " && cat > " + file + " <<'AFSCHUNK'\n" + chunk + "\nAFSCHUNK\nsha256sum " + file + " | cut -d' ' -f1"
		if _, err := m.execVerified(name, script, hex.EncodeToString(chunkSum[:]), 90*time.Second, 3); err != nil {
			return fmt.Errorf("upload chunk %d/%d: %w", i+1, total, err)
		}
	}

	// Assembly is idempotent: if remotePath already matches (an earlier attempt
	// finished but its response was lost), skip straight to the final checksum.
	assemble := "set -e\n" +
		"mkdir -p " + path.Dir(remotePath) + "\n" +
		"have=$(sha256sum " + remotePath + " 2>/dev/null | cut -d' ' -f1 || true)\n" +
		"if [ \"$have\" != \"" + want + "\" ]; then\n" +
		"  cat " + strings.Join(files, " ") + " | tr -d '\\n' | base64 -d | gunzip > " + remotePath + ".tmp\n" +
		"  chmod +x " + remotePath + ".tmp\n" +
		"  mv " + remotePath + ".tmp " + remotePath + "\n" +
		"fi\n" +
		"rm -rf " + dir + "\n" +
		"sha256sum " + remotePath + " | cut -d' ' -f1"
	if _, err := m.execVerified(name, assemble, want, 90*time.Second, 2); err != nil {
		return fmt.Errorf("assemble %s: %w", remotePath, err)
	}
	return nil
}

// bootRunBase is the fixed on-sprite prefix for the workspace boot run. Fixed
// (not per-attempt) on purpose: the .lock dir is what guarantees at most one
// boot ever runs per sprite, across hub retries, hub restarts, and lost
// responses — a second starter attaches to the live run instead of racing it.
const bootRunBase = "/tmp/afs-boot"

// startDetached launches script on the sprite in the background, decoupled
// from this HTTP request: the script's stdout/exit code land in files next to
// base, and the caller observes them with probeDetached/waitDetached. Returns
// started=false when a live run already holds the lock (caller should adopt
// it). Retried starts are safe — the lock dedupes them.
func (m *AgentManager) startDetached(name, base, script string) (started bool, err error) {
	wrapper := detachedStartScript(base, script)
	var lastErr error
	for i := 0; i < 3; i++ {
		if i > 0 {
			m.sleep(time.Duration(i) * 2 * time.Second)
		}
		out, err := m.exec(name, wrapper, 30*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		switch {
		case strings.Contains(out, "AFS_START_OK"):
			return true, nil
		case strings.Contains(out, "AFS_START_DUP"):
			return false, nil
		}
		lastErr = fmt.Errorf("start script returned neither OK nor DUP: %s", snippet(scrub(out)))
	}
	return false, lastErr
}

// detachedStartScript wraps script for a locked, background, session-detached
// run whose pid/log/exit-code land in files next to base. If a live run
// already holds the lock it reports DUP instead of starting a second copy; a
// stale lock (finished or dead run) is cleaned and replaced.
func detachedStartScript(base, script string) string {
	return fmt.Sprintf(`if [ -d %[1]s.lock ]; then
  pid=$(cat %[1]s.pid 2>/dev/null)
  if [ ! -f %[1]s.rc ] && [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    echo AFS_START_DUP
    exit 0
  fi
  rm -rf %[1]s.lock %[1]s.rc %[1]s.pid %[1]s.stage %[1]s.log %[1]s.sh
fi
mkdir %[1]s.lock 2>/dev/null || { echo AFS_START_DUP; exit 0; }
cat > %[1]s.sh <<'AFSRUNEOF'
%[2]s
AFSRUNEOF
AFS_RUN_BASE=%[1]s setsid nohup sh -c 'echo $$ > %[1]s.pid; sh %[1]s.sh > %[1]s.log 2>&1; echo $? > %[1]s.rc' >/dev/null 2>&1 &
echo AFS_START_OK`, base, script)
}

// detachedProbe is one observation of a detached run's state.
type detachedProbe struct {
	done    bool
	rc      int
	running bool
	stage   string
	logTail string
}

func (m *AgentManager) probeDetached(name, base string) (detachedProbe, error) {
	script := fmt.Sprintf(`if [ -f %[1]s.rc ]; then
  echo "AFS_RC=$(cat %[1]s.rc)"
  echo "AFS_STAGE=$(tail -n 1 %[1]s.stage 2>/dev/null)"
  echo AFS_LOG_BEGIN
  tail -n 20 %[1]s.log 2>/dev/null
  exit 0
fi
echo "AFS_STAGE=$(tail -n 1 %[1]s.stage 2>/dev/null)"
pid=$(cat %[1]s.pid 2>/dev/null)
if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
  echo AFS_ALIVE
else
  echo AFS_DEAD
  echo AFS_LOG_BEGIN
  tail -n 20 %[1]s.log 2>/dev/null
fi`, base)
	out, err := m.exec(name, script, 20*time.Second)
	if err != nil {
		return detachedProbe{}, err
	}
	return parseDetachedProbe(out), nil
}

func parseDetachedProbe(out string) detachedProbe {
	var p detachedProbe
	var log strings.Builder
	inLog := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case inLog:
			log.WriteString(line)
			log.WriteString("\n")
		case strings.HasPrefix(line, "AFS_RC="):
			p.done = true
			rc, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "AFS_RC=")))
			if err != nil {
				rc = -1 // garbled rc file: treat as failure, never as success
			}
			p.rc = rc
		case strings.HasPrefix(line, "AFS_STAGE="):
			p.stage = strings.TrimSpace(strings.TrimPrefix(line, "AFS_STAGE="))
		case strings.TrimSpace(line) == "AFS_ALIVE":
			p.running = true
		case strings.TrimSpace(line) == "AFS_LOG_BEGIN":
			inLog = true
		}
	}
	p.logTail = strings.TrimRight(log.String(), "\n")
	return p
}

// stageSpan records how long a detached run spent in one stage (as observed at
// poll granularity — close enough for "where did 34 minutes go" forensics).
type stageSpan struct {
	Name string
	D    time.Duration
}

type detachedResult struct {
	rc      int
	logTail string
	spans   []stageSpan
}

const detachedMaxProbeMisses = 8

// waitDetached polls a detached run until it finishes, dies, or budget
// expires. Stage transitions (written by the script to $AFS_RUN_BASE.stage)
// are surfaced through onStage. Errors are classified: a run that is
// unreachable or overran its budget is outcome-UNKNOWN (it may yet succeed —
// callers should consult the service health endpoint before treating it as
// failed); a run that died without an exit code, or exited non-zero, is a
// definite failure.
func (m *AgentManager) waitDetached(name, base string, budget time.Duration, onStage func(string)) (detachedResult, error) {
	var res detachedResult
	deadline := time.Now().Add(budget)
	stageStart := time.Now()
	lastStage := ""
	misses, deadProbes := 0, 0
	closeSpan := func() {
		if lastStage != "" {
			res.spans = append(res.spans, stageSpan{lastStage, time.Since(stageStart)})
		}
	}
	for {
		if time.Now().After(deadline) {
			closeSpan()
			return res, &execError{unknown: true, msg: fmt.Sprintf("still running after %s", budget)}
		}
		p, err := m.probeDetached(name, base)
		if err != nil {
			misses++
			if misses >= detachedMaxProbeMisses {
				closeSpan()
				return res, &execError{unknown: true, msg: "lost contact with the sprite while the run was in progress", cause: err}
			}
			m.sleep(m.pollInterval)
			continue
		}
		misses = 0
		if p.stage != "" && p.stage != lastStage {
			closeSpan()
			lastStage = p.stage
			stageStart = time.Now()
			if onStage != nil {
				onStage(p.stage)
			}
		}
		if p.done {
			closeSpan()
			res.rc = p.rc
			res.logTail = p.logTail
			return res, nil
		}
		if !p.running {
			// One dead probe can be the instant between the script exiting and
			// its rc file landing; two in a row means the process is gone.
			deadProbes++
			if deadProbes >= 2 {
				closeSpan()
				res.logTail = p.logTail
				return res, fmt.Errorf("run died without an exit status: %s", snippet(scrub(p.logTail)))
			}
		} else {
			deadProbes = 0
		}
		m.sleep(m.pollInterval)
	}
}

func formatSpans(spans []stageSpan) string {
	if len(spans) == 0 {
		return "n/a"
	}
	parts := make([]string, 0, len(spans))
	for _, s := range spans {
		parts = append(parts, fmt.Sprintf("%s=%.0fs", s.Name, s.D.Seconds()))
	}
	return strings.Join(parts, " ")
}
