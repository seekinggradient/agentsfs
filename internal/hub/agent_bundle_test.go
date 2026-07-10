package hub

import (
	"bytes"
	"testing"

	"agentsfs.ai/afs/internal/hub/agentbundle"
)

// This test is the deploy-time guard for the tracked go:embed artifact. Even if
// someone bypasses the builder and replaces the archive manually, broad source
// tarballs (including .env, .git, tests, docs, or node_modules) fail CI.
func TestEmbeddedAgentBundleIsSafe(t *testing.T) {
	if err := agentbundle.Validate(bytes.NewReader(agentBundle)); err != nil {
		t.Fatalf("embedded agent bundle is unsafe: %v", err)
	}
}
