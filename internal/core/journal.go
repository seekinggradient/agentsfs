package core

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	afs "agentsfs.ai/afs"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

const BootstrapTargetTokens = 2000
const BootstrapMaxTokens = 3000
const episodeMaxBytes = 128 * 1024

type Episode struct {
	ID           string `json:"id" yaml:"episode_id"`
	Status       string `json:"status" yaml:"status"`
	Description  string `json:"description" yaml:"description"`
	Started      string `json:"started" yaml:"started"`
	Checkpointed string `json:"checkpointed" yaml:"checkpointed"`
	Path         string `json:"path" yaml:"-"`
	Hash         string `json:"hash" yaml:"-"`
	Body         string `json:"body" yaml:"-"`
	Legacy       bool   `json:"legacy,omitempty" yaml:"-"`
}

type JournalPlan struct {
	Version       int       `json:"version"`
	Journal       string    `json:"journal"`
	BootstrapHash string    `json:"bootstrap_hash"`
	Episodes      []Episode `json:"episodes"`
}

type journalTransaction struct {
	Plan      JournalPlan `json:"plan"`
	Bootstrap string      `json:"bootstrap"`
}

func journalHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// journalPath rejects path escape and symlink traversal for every journal write.
func journalPath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("invalid journal path %q", rel)
	}
	p := root
	for _, part := range strings.Split(rel, "/") {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("journal path is a symlink: %s", p)
		}
	}
	return p, nil
}

func journalDir(root string) (string, error) {
	roles, err := ResolveReservedDirs(root)
	if err != nil {
		return "", err
	}
	if roles.Journal == "" || len(roles.DuplicateJournal) > 0 || roles.JournalSource != "marker" {
		return "", fmt.Errorf("declare exactly one journal role before journaling; run afs roles")
	}
	_, err = journalPath(root, roles.Journal)
	return roles.Journal, err
}

func journalRead(root, rel string) ([]byte, error) {
	p, err := journalPath(root, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > episodeMaxBytes {
		return nil, fmt.Errorf("journal file is not a regular file or exceeds %d bytes: %s", episodeMaxBytes, rel)
	}
	return os.ReadFile(p)
}

func journalAtomicWrite(root, rel string, data []byte) error {
	p, err := journalPath(root, rel)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".journal-write-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0644); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, p)
}

// A temp snapshot is fully written before its exclusive archive publication.
// A crash can leave a harmless dotfile, but never a partial archive destination.
func journalArchiveSnapshot(dest string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(dest), ".journal-archive-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0644); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Link(f.Name(), dest)
}

// SQLite supplies a process-safe, automatically released lock on supported CLI
// platforms. Only lock state lives in this disposable machine-state database.
func withJournalLock(root string, fn func() error) error {
	p, err := journalPath(root, ".agentsfs")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(p, 0755); err != nil {
		return err
	}
	lock, err := journalPath(root, ".agentsfs/journal-lock.sqlite")
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", lock)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return err
	}
	if _, err = db.Exec("BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("journal busy: %w", err)
	}
	defer db.Exec("ROLLBACK")
	if err = fn(); err != nil {
		return err
	}
	_, err = db.Exec("COMMIT")
	return err
}

// EnsureJournalLayout is additive and respects relocated role directories.
// Legacy flat entries remain untouched until explicitly consolidated.
func EnsureJournalLayout(root string) ([]string, error) {
	dir, err := journalDir(root)
	if err != nil {
		return nil, err
	}
	var made []string
	for _, rel := range []string{"active/INDEX.md", "archive/INDEX.md", "bootstrap.md"} {
		dest, err := journalPath(root, dir+"/"+rel)
		if err != nil {
			return made, err
		}
		if _, err = os.Stat(dest); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return made, err
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return made, err
		}
		data, err := fs.ReadFile(afs.TemplateFS, "template/agent-journal/"+rel)
		if err != nil {
			return made, err
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return made, err
		}
		_, err = f.Write(data)
		ce := f.Close()
		if err != nil {
			return made, err
		}
		if ce != nil {
			return made, ce
		}
		made = append(made, dir+"/"+rel)
	}
	return made, nil
}

func parseEpisode(rel string, data []byte, legacy bool) (Episode, error) {
	e := Episode{Path: rel, Hash: journalHash(data), Legacy: legacy}
	s := string(data)
	if strings.HasPrefix(s, "---\n") {
		end := strings.Index(s[4:], "\n---\n")
		if end < 0 {
			return e, fmt.Errorf("malformed episode frontmatter: %s", rel)
		}
		if err := yaml.Unmarshal([]byte(s[4:4+end]), &e); err != nil {
			return e, fmt.Errorf("%s: %w", rel, err)
		}
		e.Body = s[4+end+5:]
	} else if legacy {
		e.Body = s
	} else {
		return e, fmt.Errorf("episode lacks frontmatter: %s", rel)
	}
	if legacy {
		if e.ID == "" {
			e.ID = "legacy-" + journalHash([]byte(rel))[:24]
		}
		if e.Status == "" {
			e.Status = "complete"
		}
	} else if e.ID == "" || e.Description == "" || (e.Status != "running" && e.Status != "complete" && e.Status != "interrupted") {
		return e, fmt.Errorf("invalid episode metadata: %s", rel)
	}
	return e, nil
}

func isJournalEpisode(rel, dir string) bool {
	if !strings.HasPrefix(rel, dir+"/") || !isMarkdown(rel) || strings.EqualFold(baseName(rel), "INDEX.md") {
		return false
	}
	sub := strings.TrimPrefix(rel, dir+"/")
	return (strings.HasPrefix(sub, "active/") && !strings.Contains(strings.TrimPrefix(sub, "active/"), "/")) || (!strings.Contains(sub, "/") && !strings.EqualFold(sub, "bootstrap.md"))
}

// JournalEpisodes returns all unconsolidated episodes, including legacy flat
// notes, oldest first. Archived episodes and bootstrap are never pending work.
func JournalEpisodes(root string) ([]Episode, error) {
	dir, err := journalDir(root)
	if err != nil {
		return nil, err
	}
	var out []Episode
	for _, sub := range []string{"", "/active"} {
		p, err := journalPath(root, dir+sub)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			rel := dir + sub + "/" + entry.Name()
			if entry.IsDir() || !isJournalEpisode(rel, dir) {
				continue
			}
			data, err := journalRead(root, rel)
			if err != nil {
				return nil, err
			}
			episode, err := parseEpisode(rel, data, sub == "")
			if err != nil {
				return nil, err
			}
			out = append(out, episode)
		}
	}
	sort.Slice(out, func(i, j int) bool { return baseName(out[i].Path) < baseName(out[j].Path) })
	return out, nil
}

func episodeBytes(e Episode) ([]byte, error) {
	fm, err := yaml.Marshal(e)
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(fm) + "---\n" + strings.TrimSpace(e.Body) + "\n"), nil
}

func journalPending(root, dir string) error {
	p, err := journalPath(root, dir+"/consolidation.json")
	if err != nil {
		return err
	}
	if _, err = os.Stat(p); err == nil {
		return fmt.Errorf("journal consolidation needs recovery: afs journal recover %s", root)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func BeginEpisode(root, session, description string) (Episode, error) {
	var result Episode
	if strings.TrimSpace(description) == "" || len(description) > 500 {
		return result, fmt.Errorf("provide a description of at most 500 bytes")
	}
	if session == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return result, err
		}
		session = hex.EncodeToString(b)
	}
	if len(session) > 1024 {
		return result, fmt.Errorf("session key too long")
	}
	id := journalHash([]byte(session))[:24]
	dir, err := journalDir(root)
	if err != nil {
		return result, err
	}
	err = withJournalLock(root, func() error {
		if err := journalPending(root, dir); err != nil {
			return err
		}
		if _, err := EnsureJournalLayout(root); err != nil {
			return err
		}
		episodes, err := JournalEpisodes(root)
		if err != nil {
			return err
		}
		for _, e := range episodes {
			if e.ID == id {
				result = e
				return nil
			}
		}
		archive, err := os.ReadDir(filepath.Join(root, dir, "archive"))
		if err != nil {
			return err
		}
		for _, f := range archive {
			if strings.Contains(f.Name(), "-"+id+"-") {
				return fmt.Errorf("session already archived: %s", f.Name())
			}
		}
		now := time.Now().UTC()
		result = Episode{ID: id, Status: "running", Description: description, Started: now.Format(time.RFC3339), Checkpointed: now.Format(time.RFC3339), Body: "## Intent\n" + description + "\n\n## Progress\nWork started.\n\n## Open\nWork is in progress.\n\n## Artifacts\n"}
		result.Path = dir + "/active/" + now.Format("2006-01-02T150405Z") + "-" + id + "-episode.md"
		data, err := episodeBytes(result)
		if err != nil {
			return err
		}
		result.Hash = journalHash(data)
		return journalAtomicWrite(root, result.Path, data)
	})
	return result, err
}

// CheckpointEpisode requires the hash from the last read. A second writer or
// resumed stale context cannot silently overwrite newer progress.
func CheckpointEpisode(root, id, expected, body, status string) (Episode, error) {
	var result Episode
	if expected == "" {
		return result, fmt.Errorf("expected hash is required; read the current episode first")
	}
	if len(body) > episodeMaxBytes/2 || strings.TrimSpace(body) == "" {
		return result, fmt.Errorf("checkpoint body must be nonempty and at most %d bytes", episodeMaxBytes/2)
	}
	if status != "running" && status != "complete" && status != "interrupted" {
		return result, fmt.Errorf("invalid status %q", status)
	}
	dir, err := journalDir(root)
	if err != nil {
		return result, err
	}
	err = withJournalLock(root, func() error {
		if err := journalPending(root, dir); err != nil {
			return err
		}
		episodes, err := JournalEpisodes(root)
		if err != nil {
			return err
		}
		for _, e := range episodes {
			if e.ID != id {
				continue
			}
			if e.Legacy {
				return fmt.Errorf("legacy episodes are immutable")
			}
			if e.Hash != expected {
				return fmt.Errorf("episode changed; reread before checkpointing")
			}
			if e.Status == "complete" {
				return fmt.Errorf("completed episode is immutable; start a new episode")
			}
			e.Body = body
			e.Status = status
			e.Checkpointed = time.Now().UTC().Format(time.RFC3339)
			existing, err := journalRead(root, e.Path)
			if err != nil {
				return err
			}
			end := strings.Index(string(existing[4:]), "\n---\n")
			var metadata map[string]any
			if err = yaml.Unmarshal(existing[4:4+end], &metadata); err != nil {
				return err
			}
			metadata["status"] = e.Status
			metadata["checkpointed"] = e.Checkpointed
			fm, err := yaml.Marshal(metadata)
			data := []byte("---\n" + string(fm) + "---\n" + strings.TrimSpace(e.Body) + "\n")
			if err != nil {
				return err
			}
			e.Hash = journalHash(data)
			result = e
			return journalAtomicWrite(root, e.Path, data)
		}
		return fmt.Errorf("active episode %q not found", id)
	})
	return result, err
}

func PrepareJournal(root string) (JournalPlan, error) {
	var plan JournalPlan
	dir, err := journalDir(root)
	if err != nil {
		return plan, err
	}
	err = withJournalLock(root, func() error {
		if err := journalPending(root, dir); err != nil {
			return err
		}
		if _, err := EnsureJournalLayout(root); err != nil {
			return err
		}
		b, err := journalRead(root, dir+"/bootstrap.md")
		if err != nil {
			return err
		}
		plan = JournalPlan{Version: 1, Journal: dir, BootstrapHash: journalHash(b), Episodes: []Episode{}}
		episodes, err := JournalEpisodes(root)
		if err != nil {
			return err
		}
		for _, e := range episodes {
			if (e.Status == "complete" || e.Status == "interrupted") && len(plan.Episodes) < 32 {
				plan.Episodes = append(plan.Episodes, e)
			}
		}
		return nil
	})
	return plan, err
}

func ValidateBootstrap(data []byte) error {
	if len(data) > episodeMaxBytes || estTokens(string(data)) > BootstrapMaxTokens {
		return fmt.Errorf("bootstrap exceeds %d estimated tokens; synthesize it more densely", BootstrapMaxTokens)
	}
	for _, h := range []string{"## Overview", "## Chronology", "## Key knowledge"} {
		if !strings.Contains(string(data), h+"\n") {
			return fmt.Errorf("bootstrap needs %s", h)
		}
	}
	if strings.TrimSpace(string(data)) == "" {
		return fmt.Errorf("empty bootstrap")
	}
	return nil
}

func validateJournalPlan(root string, plan JournalPlan, bootstrap []byte) error {
	dir, err := journalDir(root)
	if err != nil {
		return err
	}
	if plan.Version != 1 || plan.Journal != dir || len(plan.Episodes) == 0 || len(plan.Episodes) > 32 {
		return fmt.Errorf("invalid or empty consolidation plan")
	}
	if err = ValidateBootstrap(bootstrap); err != nil {
		return err
	}
	b, err := journalRead(root, dir+"/bootstrap.md")
	if err != nil {
		return err
	}
	if journalHash(b) != plan.BootstrapHash {
		return fmt.Errorf("bootstrap changed; prepare again")
	}
	seen := map[string]bool{}
	for _, e := range plan.Episodes {
		if !isJournalEpisode(e.Path, dir) || seen[e.Path] {
			return fmt.Errorf("invalid or duplicate planned episode: %s", e.Path)
		}
		seen[e.Path] = true
		key := "archive/" + baseName(e.Path)
		if seen[key] {
			return fmt.Errorf("duplicate archive filename: %s", key)
		}
		seen[key] = true
		b, err := journalRead(root, e.Path)
		if err != nil {
			return err
		}
		actual, err := parseEpisode(e.Path, b, !strings.HasPrefix(e.Path, dir+"/active/"))
		if err != nil {
			return err
		}
		if actual.Hash != e.Hash || actual.ID != e.ID || (actual.Status != "complete" && actual.Status != "interrupted") {
			return fmt.Errorf("episode changed or is running: %s", e.Path)
		}
		dest, err := journalPath(root, dir+"/archive/"+baseName(e.Path))
		if err != nil {
			return err
		}
		if _, err = os.Stat(dest); !os.IsNotExist(err) {
			return fmt.Errorf("archive destination exists or is unreadable: %s", dest)
		}
	}
	return nil
}

// ConsolidateJournal records a recoverable intent before any archive move.
// The intent is durable journal data (not disposable machine state). It is
// removed only after bootstrap and all archives match the planned bytes.
func ConsolidateJournal(root string, plan JournalPlan, bootstrap []byte) error {
	return withJournalLock(root, func() error {
		if err := journalPending(root, plan.Journal); err != nil {
			return err
		}
		if err := validateJournalPlan(root, plan, bootstrap); err != nil {
			return err
		}
		tx := journalTransaction{Plan: plan, Bootstrap: string(bootstrap)}
		tx.Plan.Episodes = append([]Episode(nil), plan.Episodes...)
		for i := range tx.Plan.Episodes {
			tx.Plan.Episodes[i].Body = ""
		}
		data, err := json.MarshalIndent(tx, "", "  ")
		if err != nil {
			return err
		}
		if err = journalAtomicWrite(root, plan.Journal+"/consolidation.json", data); err != nil {
			return err
		}
		return recoverJournal(root, tx)
	})
}

func RecoverJournal(root string) error {
	dir, err := journalDir(root)
	if err != nil {
		return err
	}
	return withJournalLock(root, func() error {
		b, err := journalRead(root, dir+"/consolidation.json")
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		var tx journalTransaction
		if err = json.Unmarshal(b, &tx); err != nil {
			return err
		}
		if tx.Plan.Journal != dir || tx.Plan.Version != 1 {
			return fmt.Errorf("invalid recovery journal")
		}
		return recoverJournal(root, tx)
	})
}

func recoverJournal(root string, tx journalTransaction) error {
	dir := tx.Plan.Journal
	if err := ValidateBootstrap([]byte(tx.Bootstrap)); err != nil {
		return err
	}
	current, err := journalRead(root, dir+"/bootstrap.md")
	if err != nil {
		return err
	}
	hash := journalHash(current)
	if hash != tx.Plan.BootstrapHash && hash != journalHash([]byte(tx.Bootstrap)) {
		return fmt.Errorf("bootstrap changed during recovery; preserve consolidation.json and reconcile manually")
	}
	// Preflight all destinations and remaining sources before advancing recovery.
	for _, e := range tx.Plan.Episodes {
		if !isJournalEpisode(e.Path, dir) {
			return fmt.Errorf("invalid recovery episode path")
		}
		src, err := journalRead(root, e.Path)
		if err == nil {
			if journalHash(src) != e.Hash {
				return fmt.Errorf("episode changed during recovery: %s", e.Path)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		dst, derr := journalRead(root, dir+"/archive/"+baseName(e.Path))
		if derr == nil {
			if journalHash(dst) != e.Hash {
				return fmt.Errorf("archive collision: %s", e.Path)
			}
		} else if !os.IsNotExist(derr) {
			return derr
		}
		if os.IsNotExist(err) && os.IsNotExist(derr) {
			return fmt.Errorf("episode missing from active and archive: %s", e.Path)
		}
	}
	if hash != journalHash([]byte(tx.Bootstrap)) {
		if err = journalAtomicWrite(root, dir+"/bootstrap.md", []byte(tx.Bootstrap)); err != nil {
			return err
		}
	}
	for _, e := range tx.Plan.Episodes {
		src, _ := journalPath(root, e.Path)
		dst, _ := journalPath(root, dir+"/archive/"+baseName(e.Path))
		// Publish an independent snapshot exclusively. Never link the live source
		// inode into the archive: an in-place editor could otherwise change both.
		if _, err = os.Lstat(dst); os.IsNotExist(err) {
			data, readErr := journalRead(root, e.Path)
			if readErr != nil {
				return readErr
			}
			if journalHash(data) != e.Hash {
				return fmt.Errorf("episode changed during consolidation: %s", e.Path)
			}
			if err = journalArchiveSnapshot(dst, data); err != nil && !os.IsExist(err) {
				return err
			}
		} else if err != nil {
			return err
		}
		b, err := journalRead(root, dir+"/archive/"+baseName(e.Path))
		if err != nil {
			return err
		}
		if journalHash(b) != e.Hash {
			return fmt.Errorf("archive changed during consolidation")
		}
		if b, err := journalRead(root, e.Path); err == nil {
			if journalHash(b) != e.Hash {
				return fmt.Errorf("episode changed during consolidation; retained at %s", e.Path)
			}
			if err = os.Remove(src); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	p, _ := journalPath(root, dir+"/consolidation.json")
	return os.Remove(p)
}

func episodicFindings(root string, roles RoleDirs) []Finding {
	if roles.Journal == "" || roles.JournalSource != RoleSourceMarker || len(roles.DuplicateJournal) > 0 {
		return nil
	}
	dir := roles.Journal
	var out []Finding
	if _, err := os.Stat(joinRel(root, dir+"/bootstrap.md")); os.IsNotExist(err) {
		if CompareContractVersions(ContractVersion(root), "0.13.0") >= 0 {
			out = append(out, Finding{"warn", "journal-layout", dir, "episodic journal layout missing; run afs contract upgrade"})
		}
		return out
	}
	if b, err := journalRead(root, dir+"/bootstrap.md"); err != nil {
		out = append(out, Finding{"warn", "journal-bootstrap", dir + "/bootstrap.md", err.Error()})
	} else if err = ValidateBootstrap(b); err != nil {
		out = append(out, Finding{"warn", "journal-bootstrap", dir + "/bootstrap.md", err.Error()})
	}
	if err := journalPending(root, dir); err != nil {
		out = append(out, Finding{"warn", "journal-recovery", dir, err.Error()})
	}
	episodes, err := JournalEpisodes(root)
	if err != nil {
		return append(out, Finding{"warn", "journal-episode", dir, err.Error()})
	}
	ids := map[string]bool{}
	for _, e := range episodes {
		if ids[e.ID] {
			out = append(out, Finding{"warn", "journal-duplicate-id", e.Path, "duplicate episode ID; reconcile ownership before writing"})
		}
		ids[e.ID] = true
		if e.Status == "running" {
			t, err := time.Parse(time.RFC3339, e.Checkpointed)
			if err != nil || time.Since(t) > 48*time.Hour {
				out = append(out, Finding{"info", "journal-stale-running", e.Path, "running episode has no recent checkpoint; verify its owner before marking interrupted, never assume completion"})
			}
		}
	}
	return out
}
