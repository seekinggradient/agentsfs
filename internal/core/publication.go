package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const PublicationSchemaVersion = 2

const (
	PublicationModeStandalone         = "standalone"
	PublicationModeEmbeddedProjection = "embedded-projection"
	ProjectionProtocolVersion         = 2
	ProjectionLedgerRef               = "refs/agentsfs/projection"
)

// PublicationMetadata is rebuildable, credential-free machine state written
// only after a Hub push has been remotely verified.
type PublicationMetadata struct {
	SchemaVersion int    `json:"schema_version"`
	Mode          string `json:"mode,omitempty"`
	SyncVersion   int    `json:"sync_version,omitempty"`
	LedgerRef     string `json:"ledger_ref,omitempty"`
	RemoteName    string `json:"remote_name"`
	RemoteURL     string `json:"remote_url"`
	Repository    string `json:"repository,omitempty"`
	PublishBranch string `json:"publish_branch"`
	// IntegratedHubCommit is the Hub tip whose content has been three-way
	// folded into the host. It need not be a host commit ancestor: embedded
	// pulls intentionally create one ordinary folded host commit.
	IntegratedHubCommit string               `json:"integrated_hub_commit,omitempty"`
	LastPush            *PublicationLastPush `json:"last_push,omitempty"`
}

type PublicationLastPush struct {
	SourceRepoHead       string `json:"source_repo_head"`
	ProjectedCommit      string `json:"projected_commit"`
	VerifiedRemoteCommit string `json:"verified_remote_commit"`
}

func PublicationMetadataPath(instance string) string {
	return filepath.Join(instance, ".agentsfs", "hub.json")
}

func LoadPublicationMetadata(instance string) (PublicationMetadata, error) {
	var metadata PublicationMetadata
	data, err := os.ReadFile(PublicationMetadataPath(instance))
	if err != nil {
		return metadata, err
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return metadata, err
	}
	metadata.RemoteURL = CredentialFreeURL(metadata.RemoteURL)
	return metadata, nil
}

func SavePublicationMetadata(instance string, metadata PublicationMetadata) error {
	metadata.SchemaVersion = PublicationSchemaVersion
	if metadata.Mode == PublicationModeEmbeddedProjection {
		metadata.SyncVersion = ProjectionProtocolVersion
		if metadata.LedgerRef == "" {
			metadata.LedgerRef = ProjectionLedgerRef
		}
	}
	metadata.RemoteURL = CredentialFreeURL(metadata.RemoteURL)
	if metadata.PublishBranch == "" {
		metadata.PublishBranch = "main"
	}
	dir := filepath.Dir(PublicationMetadataPath(instance))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(ignore); os.IsNotExist(err) {
		if err := os.WriteFile(ignore, []byte("*\n!.gitignore\n"), 0o644); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".hub-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, PublicationMetadataPath(instance))
}

// PublicationTrackingRef is namespaced per instance so multiple embedded
// instances in one Git worktree never overwrite each other's cached Hub main.
func PublicationTrackingRef(instance string) string {
	canonical, err := canonicalPath(instance)
	if err != nil {
		canonical = filepath.Clean(instance)
	}
	sum := sha256.Sum256([]byte(canonical))
	return "refs/remotes/afs-hub/" + hex.EncodeToString(sum[:6]) + "/main"
}

// PublicationLedgerTrackingRef is the local, per-instance cache of the Hub's
// recoverable projection ledger. It is deliberately separate from the cached
// main ref: main may advance through Hub-side writers while the ledger remains
// the last successful host projection correspondence.
func PublicationLedgerTrackingRef(instance string) string {
	canonical, err := canonicalPath(instance)
	if err != nil {
		canonical = filepath.Clean(instance)
	}
	sum := sha256.Sum256([]byte(canonical))
	return "refs/remotes/afs-hub/" + hex.EncodeToString(sum[:6]) + "/projection"
}

func CredentialFreeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func PublicationRepository(raw string) string {
	u, err := url.Parse(CredentialFreeURL(raw))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	owner := parts[len(parts)-2]
	slug := strings.TrimSuffix(parts[len(parts)-1], ".git")
	if owner == "" || slug == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s", owner, slug)
}
