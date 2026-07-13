//go:build e2e

package hub

// Manual end-to-end exercise of the NEW provisioning path against the real
// Sprites API, using a THROWAWAY sprite and a local temp account store —
// nothing touches the production sprite or the production hub database.
// Run: SPRITES_TOKEN=... go test -tags e2e -run TestE2EProvisionThrowaway -v -timeout 15m ./internal/hub/
// Not part of the regular suite (build-tagged); the sprite is deleted at the end.

import (
	"log"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestE2EProvisionThrowaway(t *testing.T) {
	token := os.Getenv("SPRITES_TOKEN")
	if token == "" {
		t.Skip("SPRITES_TOKEN not set")
	}
	const name = "afs-e2e-provision-check"
	accounts := newTestAccountsWithUser(t, "e2etest")
	m := NewAgentManager(token, "e2e-unused-model-key", "", "", accounts, log.New(os.Stderr, "", log.LstdFlags))

	cleanup := func() {
		if resp, err := m.authed(http.MethodDelete, "/sprites/"+name, nil, 60*time.Second); err == nil {
			t.Logf("cleanup: delete sprite -> %s", resp.Status)
			resp.Body.Close()
		} else {
			t.Logf("cleanup: delete sprite failed: %v", err)
		}
	}
	cleanup() // clear any leftover from a previous run
	defer cleanup()

	// Seed the state entry the way maybeStartProvision would.
	m.mu.Lock()
	m.state["user:e2etest"] = &provisionState{Running: true, Attempt: 1, StartedAt: time.Now(), Stage: "starting"}
	m.mu.Unlock()

	start := time.Now()
	m.provisionUser("e2etest", name, nil) // zero repos: clone stage is a no-op, everything else is real
	t.Logf("provisionUser returned after %s", time.Since(start).Round(time.Second))

	if st := m.ProvisionStatus("e2etest"); st.Running || st.LastError != "" {
		// Dump run-file state for diagnosis. Never the script itself — it
		// embeds the (temp-store) credential; the log/stage/rc files carry
		// only script output.
		diag, derr := m.exec(name,
			"ls -la /tmp/afs-boot* 2>&1; echo '--- rc:'; cat /tmp/afs-boot.rc 2>/dev/null; echo '--- stage:'; cat /tmp/afs-boot.stage 2>/dev/null; echo '--- pid:'; cat /tmp/afs-boot.pid 2>/dev/null; echo '--- log tail:'; tail -n 40 /tmp/afs-boot.log 2>/dev/null; echo '--- sh size:'; wc -c /tmp/afs-boot.sh 2>/dev/null",
			30*time.Second)
		t.Logf("boot run state (err=%v):\n%s", derr, scrub(diag))
		t.Fatalf("provisioning did not succeed: %+v", st)
	}
	url, err := m.spriteURL(name)
	if err != nil || url == "" {
		t.Fatalf("sprite lookup after provision: url=%q err=%v", url, err)
	}
	if !m.healthy(url) {
		t.Fatal("agent service is not healthy after provisioning")
	}
	t.Log("agent service healthy — end-to-end provision OK")
}
