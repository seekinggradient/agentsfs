package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// AgentPATStore persists the long-lived per-user agent PATs the hosted Eve
// upstream receives as the X-AFS-PAT header.
//
// Why a plaintext store is required: the accounts DB keeps only a SHA-256 HASH
// of every PAT (accounts.go CreatePAT), so once minted the plaintext cannot be
// recovered. The Eve app needs the SAME token on every request and across Hub
// restarts (its turns are durable — a parked tool call may run hours after the
// inbound request), so the Hub must retain the plaintext itself. This is a 0600
// JSON file on the data volume keyed by username — the same trust class as a
// sprite storing its own PAT on disk (docs/eve-hub-integration.md). It holds no
// knowledge, only injectable credentials that already exist (hashed) in the
// accounts DB.
//
// The file is the source of truth: it is re-read on every GetOrMint, so the
// documented rotation — delete the user's entry AND revoke the PAT in the
// account — takes effect on the very next request (which finds no entry and
// mints a fresh token) without a Hub restart.
type AgentPATStore struct {
	path string
	mu   sync.Mutex // serializes the read-modify-write of the whole file
}

// NewAgentPATStore roots a store at path (e.g. <volume>/.agent-pats.json). No I/O
// happens until a token is requested.
func NewAgentPATStore(path string) *AgentPATStore {
	return &AgentPATStore{path: path}
}

// GetOrMint returns the stored PAT for user, minting and persisting one via mint
// on the first request (or after the entry has been rotated out). Minting is
// serialized under the store mutex, so two concurrent first requests for the
// same user can never mint two tokens. A nil store or empty user yields ("",nil)
// so the caller simply skips injection.
func (s *AgentPATStore) GetOrMint(user string, mint func() (string, error)) (string, error) {
	if s == nil || user == "" {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.load()
	if tok := m[user]; tok != "" {
		return tok, nil
	}
	tok, err := mint()
	if err != nil {
		return "", err
	}
	if tok == "" {
		return "", nil
	}
	m[user] = tok
	if err := s.save(m); err != nil {
		return "", err
	}
	return tok, nil
}

// Get returns the stored PAT for user without minting (ok=false when absent).
func (s *AgentPATStore) Get(user string) (string, bool) {
	if s == nil || user == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tok := s.load()[user]
	return tok, tok != ""
}

// Delete drops a user's entry (the rotation story's first half; the operator
// also revokes the PAT in the account). A missing entry is a no-op.
func (s *AgentPATStore) Delete(user string) error {
	if s == nil || user == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.load()
	if _, ok := m[user]; !ok {
		return nil
	}
	delete(m, user)
	return s.save(m)
}

// load reads the JSON map, returning an empty map when the file is absent,
// unreadable, or corrupt — a bad file must never wedge the agent, and the next
// mint self-heals it.
func (s *AgentPATStore) load() map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return m
	}
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return map[string]string{}
	}
	return m
}

// save writes the map atomically with 0600 permissions (temp + chmod + fsync +
// rename in the same directory), so the plaintext tokens are never
// world-readable and a reader never observes a half-written file.
func (s *AgentPATStore) save(m map[string]string) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".agent-pats-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
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
	return os.Rename(tmpName, s.path)
}
