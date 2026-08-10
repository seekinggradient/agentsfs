package hubclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

// projectionLedger is the recoverable half of an embedded instance's local
// .agentsfs/hub.json. The Hub stores its append-only commit at
// refs/agentsfs/projection and updates it atomically with main. A fresh clone of
// the host repository can therefore recover the last exact host/Hub
// correspondence after the machine-local .agentsfs directory is gone.
type projectionLedger struct {
	SchemaVersion  int    `json:"schema_version"`
	Mode           string `json:"mode"`
	Repository     string `json:"repository"`
	SourceRepoHead string `json:"source_repo_head"`
	HubCommit      string `json:"hub_commit"`
	ProjectedTree  string `json:"projected_tree"`
}

func metadataFromLedger(remote string, ledger projectionLedger) core.PublicationMetadata {
	return core.PublicationMetadata{
		Mode:          core.PublicationModeEmbeddedProjection,
		SyncVersion:   core.ProjectionProtocolVersion,
		LedgerRef:     core.ProjectionLedgerRef,
		RemoteName:    "hub",
		RemoteURL:     core.CredentialFreeURL(remote),
		Repository:    ledger.Repository,
		PublishBranch: "main",
		LastPush: &core.PublicationLastPush{
			SourceRepoHead:       ledger.SourceRepoHead,
			ProjectedCommit:      ledger.HubCommit,
			VerifiedRemoteCommit: ledger.HubCommit,
		},
	}
}

func publicationBaseMatchesHost(repoRoot, prefix, head string, metadata core.PublicationMetadata) bool {
	if metadata.LastPush == nil || metadata.LastPush.SourceRepoHead == "" || metadata.LastPush.ProjectedCommit == "" {
		return false
	}
	source := metadata.LastPush.SourceRepoHead
	if !gitIsAncestor(repoRoot, source, head) {
		return false
	}
	spec := source + "^{tree}"
	if prefix != "." {
		spec = source + ":" + prefix
	}
	hostTree, ok := gitOutput(repoRoot, "rev-parse", spec)
	return ok && hostTree != "" && hostTree == commitTree(repoRoot, metadata.LastPush.ProjectedCommit)
}

func readProjectionLedger(repoRoot, ref string) (projectionLedger, error) {
	var ledger projectionLedger
	out, err := exec.Command("git", "-C", repoRoot, "show", ref+":projection.json").Output()
	if err != nil {
		return ledger, fmt.Errorf("reading %s: %w", ref, err)
	}
	if err := json.Unmarshal(out, &ledger); err != nil {
		return ledger, err
	}
	if ledger.SchemaVersion != core.ProjectionProtocolVersion ||
		ledger.Mode != core.PublicationModeEmbeddedProjection ||
		ledger.Repository == "" || ledger.SourceRepoHead == "" ||
		ledger.HubCommit == "" || ledger.ProjectedTree == "" {
		return ledger, errors.New("missing or unsupported projection identity fields")
	}
	if commitTree(repoRoot, ledger.HubCommit) != ledger.ProjectedTree {
		return ledger, errors.New("recorded Hub commit does not have the recorded projected tree")
	}
	return ledger, nil
}

func createProjectionLedger(repoRoot, parent string, ledger projectionLedger) (string, error) {
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	blob, err := gitInput(repoRoot, data, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	treeLine := fmt.Sprintf("100644 blob %s\tprojection.json\n", blob)
	tree, err := gitInput(repoRoot, []byte(treeLine), "mktree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, "-F", "-")
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=agentsfs projection protocol",
		"GIT_AUTHOR_EMAIL=projection@agentsfs",
		"GIT_COMMITTER_NAME=agentsfs projection protocol",
		"GIT_COMMITTER_EMAIL=projection@agentsfs",
	)
	cmd.Stdin = strings.NewReader("Record embedded projection base\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git commit-tree: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// publishLegacyProjectionLedger upgrades a schema-v1 embedded projection's
// recoverable identity without moving main. It is a normal create-only ref
// push, never a force update. The Hub sees the ref after receive-pack and may
// safely re-enable Hub writers because protocol-v2 pull now exists.
func publishLegacyProjectionLedger(repoRoot, remote, repository, remoteMain string, metadata core.PublicationMetadata, cfg Config) (string, error) {
	if metadata.LastPush == nil || metadata.LastPush.SourceRepoHead == "" || metadata.LastPush.VerifiedRemoteCommit == "" {
		return "", errors.New("legacy publication metadata does not contain a complete projection base")
	}
	base := metadata.LastPush.VerifiedRemoteCommit
	if !gitIsAncestor(repoRoot, base, remoteMain) {
		return "", fmt.Errorf("legacy projection base %s is not an ancestor of hub/main %s", shortCommit(base), shortCommit(remoteMain))
	}
	tree := commitTree(repoRoot, base)
	if tree == "" {
		return "", errors.New("legacy projection base is not available after fetching hub/main")
	}
	ledgerCommit, err := createProjectionLedger(repoRoot, "", projectionLedger{
		SchemaVersion:  core.ProjectionProtocolVersion,
		Mode:           core.PublicationModeEmbeddedProjection,
		Repository:     repository,
		SourceRepoHead: metadata.LastPush.SourceRepoHead,
		HubCommit:      base,
		ProjectedTree:  tree,
	})
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "-C", repoRoot, "push", remote, ledgerCommit+":"+core.ProjectionLedgerRef)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("publishing the projection protocol marker: %v: %s", err, sanitizeGitError(string(out), cfg, remote))
	}
	return ledgerCommit, nil
}

func gitInput(repoRoot string, input []byte, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func commitTree(repoRoot, commit string) string {
	if commit == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", commit+"^{tree}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitTreesEqual(repoRoot, a, b string) bool {
	return commitTree(repoRoot, a) != "" && commitTree(repoRoot, a) == commitTree(repoRoot, b)
}
