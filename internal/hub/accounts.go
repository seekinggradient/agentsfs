package hub

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"
)

// Account errors surfaced to the UI.
var (
	ErrUserExists = errors.New("that username is taken")
	ErrBadLogin   = errors.New("wrong username or password")
)

// AccountStore is the hub's user/account store: usernames, argon2id password
// hashes, and per-account git tokens (PATs). It is the one piece of state that
// is NOT rebuildable from the git repos — but it holds access metadata, not the
// knowledge itself (that stays plain git). Embedded SQLite (a single file on
// the same volume) keeps it self-hostable with no extra infrastructure.
type AccountStore struct {
	db     *sql.DB
	secret []byte
}

// OpenAccounts opens (creating if needed) the SQLite account store at path.
func OpenAccounts(path string) (*AccountStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // modernc/sqlite: serialize access, avoid SQLITE_BUSY
	a := &AccountStore{db: db}
	if err := a.migrate(); err != nil {
		return nil, err
	}
	a.secret, err = a.ensureSecret()
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (a *AccountStore) migrate() error {
	_, err := a.db.Exec(`
CREATE TABLE IF NOT EXISTS config (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY,
  username      TEXT UNIQUE NOT NULL,
  email         TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tokens (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL,
  name       TEXT NOT NULL DEFAULT '',
  token_hash TEXT UNIQUE NOT NULL,
  created_at INTEGER NOT NULL,
  last_used  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS allowlist (
  email    TEXT PRIMARY KEY,
  added_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS waitlist (
  email      TEXT PRIMARY KEY,
  username   TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS collaborators (
  repo_owner TEXT NOT NULL,
  repo       TEXT NOT NULL,
  username   TEXT NOT NULL,
  role       TEXT NOT NULL,
  added_at   INTEGER NOT NULL,
  PRIMARY KEY (repo_owner, repo, username)
);
CREATE INDEX IF NOT EXISTS idx_collab_user ON collaborators(username);
CREATE TABLE IF NOT EXISTS collaborator_invites (
  repo_owner TEXT NOT NULL,
  repo       TEXT NOT NULL,
  email      TEXT NOT NULL,
  role       TEXT NOT NULL,
  token_hash TEXT UNIQUE NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  PRIMARY KEY (repo_owner, repo, email)
);
CREATE INDEX IF NOT EXISTS idx_collab_invite_token ON collaborator_invites(token_hash);`)
	return err
}

// --- per-repo collaborators -----------------------------------------------
//
// A repo <owner>/<repo> can grant another account "read" or "write" access,
// sitting beside the public/private visibility flag. Attribution is preserved:
// the collaborator uses their OWN account (never a shared token). Stored here
// (not in repo git-config like visibility) because "which repos are shared with
// me" is a reverse lookup the dashboard + a user's sprite both need.

// Collaborator is one grant on a repo.
type Collaborator struct {
	Username string
	Role     string // "read" | "write"
	AddedAt  int64
}

// CollaboratorInvite is a pending email invitation. The raw token is never
// stored; it is only returned when an invite is created so the owner can pass
// the link to the recipient.
type CollaboratorInvite struct {
	Email     string
	Role      string
	CreatedAt int64
}

// Invite is the private data needed to render or redeem an invite link.
type Invite struct {
	Owner, Repo, Email, Role string
	CreatedAt, ExpiresAt     int64
}

// SharedRepo is a repo shared WITH some user (from their perspective).
type SharedRepo struct {
	Owner, Repo, Role string
}

// AddCollaborator grants username read/write on owner/repo. The collaborator
// must be an existing account and cannot be the owner themselves.
func (a *AccountStore) AddCollaborator(owner, repo, username, role string) error {
	owner = strings.ToLower(strings.TrimSpace(owner))
	repo = normRepo(repo)
	username = strings.ToLower(strings.TrimSpace(username))
	if role != "read" && role != "write" {
		return errors.New("role must be read or write")
	}
	if !validSlug(username) {
		return errors.New("invalid username")
	}
	if username == owner {
		return errors.New("the owner already has full access")
	}
	if !a.Exists(username) {
		return fmt.Errorf("no account named %q — they need to sign up first", username)
	}
	_, err := a.db.Exec(
		`INSERT INTO collaborators(repo_owner,repo,username,role,added_at) VALUES(?,?,?,?,?)
		 ON CONFLICT(repo_owner,repo,username) DO UPDATE SET role=excluded.role`,
		owner, repo, username, role, time.Now().Unix())
	return err
}

const collaboratorInviteTTL = 14 * 24 * time.Hour

// UserByEmail finds an existing account by its case-insensitive email.
func (a *AccountStore) UserByEmail(email string) (*User, bool) {
	email = normEmail(email)
	if email == "" {
		return nil, false
	}
	var u User
	var passwordHash string
	err := a.db.QueryRow(`SELECT id,username,email,password_hash FROM users WHERE lower(email)=? LIMIT 1`, email).Scan(&u.ID, &u.Username, &u.Email, &passwordHash)
	if err != nil {
		return nil, false
	}
	u.HasPassword = passwordHash != ""
	return &u, true
}

// AddCollaboratorByEmail grants an existing account immediately. If the email
// is not registered yet, it creates/replaces a pending invite and returns the
// raw token used to redeem it.
func (a *AccountStore) AddCollaboratorByEmail(owner, repo, email, role string) (username, inviteToken string, err error) {
	email = normEmail(email)
	if !looksLikeEmail(email) {
		return "", "", errors.New("enter a valid email address")
	}
	if role != "read" && role != "write" {
		return "", "", errors.New("role must be read or write")
	}
	if u, ok := a.UserByEmail(email); ok {
		if err := a.AddCollaborator(owner, repo, u.Username, role); err != nil {
			return "", "", err
		}
		// An old pending invite for this address is no longer needed.
		a.db.Exec(`DELETE FROM collaborator_invites WHERE repo_owner=? AND repo=? AND email=?`,
			strings.ToLower(owner), normRepo(repo), email)
		return u.Username, "", nil
	}

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	inviteToken = "inv_" + base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	_, err = a.db.Exec(`INSERT INTO collaborator_invites(repo_owner,repo,email,role,token_hash,created_at,expires_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(repo_owner,repo,email) DO UPDATE SET role=excluded.role,
		 token_hash=excluded.token_hash, created_at=excluded.created_at, expires_at=excluded.expires_at`,
		strings.ToLower(owner), normRepo(repo), email, role, tokenHash(inviteToken), now.Unix(), now.Add(collaboratorInviteTTL).Unix())
	if err != nil {
		return "", "", err
	}
	return "", inviteToken, nil
}

// InviteForToken returns a non-expired invite for a raw URL token.
func (a *AccountStore) InviteForToken(token string) (*Invite, bool) {
	if token == "" {
		return nil, false
	}
	var inv Invite
	err := a.db.QueryRow(`SELECT repo_owner,repo,email,role,created_at,expires_at
		FROM collaborator_invites WHERE token_hash=? AND expires_at>?`, tokenHash(token), time.Now().Unix()).
		Scan(&inv.Owner, &inv.Repo, &inv.Email, &inv.Role, &inv.CreatedAt, &inv.ExpiresAt)
	if err != nil {
		return nil, false
	}
	return &inv, true
}

// AcceptCollaboratorInvite attaches an invite to the named account. The
// account email must match the invitation, preventing a forwarded link from
// granting access to the wrong identity.
func (a *AccountStore) AcceptCollaboratorInvite(token, username string) error {
	inv, ok := a.InviteForToken(token)
	if !ok {
		return errors.New("that invitation is invalid or expired")
	}
	var email string
	if err := a.db.QueryRow(`SELECT email FROM users WHERE username=?`, strings.ToLower(strings.TrimSpace(username))).Scan(&email); err != nil {
		return errors.New("account not found")
	}
	if normEmail(email) != inv.Email {
		return errors.New("this invitation belongs to a different email address")
	}
	if _, err := a.db.Exec(`INSERT INTO collaborators(repo_owner,repo,username,role,added_at) VALUES(?,?,?,?,?)
		ON CONFLICT(repo_owner,repo,username) DO UPDATE SET role=excluded.role`,
		inv.Owner, inv.Repo, strings.ToLower(strings.TrimSpace(username)), inv.Role, time.Now().Unix()); err != nil {
		return err
	}
	_, err := a.db.Exec(`DELETE FROM collaborator_invites WHERE token_hash=?`, tokenHash(token))
	return err
}

// ListCollaboratorInvites returns pending invitations for an owner's repo.
func (a *AccountStore) ListCollaboratorInvites(owner, repo string) []CollaboratorInvite {
	if a == nil {
		return nil
	}
	now := time.Now().Unix()
	a.db.Exec(`DELETE FROM collaborator_invites WHERE expires_at<=?`, now)
	rows, err := a.db.Query(`SELECT email,role,created_at FROM collaborator_invites
		WHERE repo_owner=? AND repo=? ORDER BY created_at DESC`, strings.ToLower(owner), normRepo(repo))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []CollaboratorInvite
	for rows.Next() {
		var inv CollaboratorInvite
		if rows.Scan(&inv.Email, &inv.Role, &inv.CreatedAt) == nil {
			out = append(out, inv)
		}
	}
	return out
}

// RemoveCollaborator revokes a grant.
func (a *AccountStore) RemoveCollaborator(owner, repo, username string) error {
	_, err := a.db.Exec(`DELETE FROM collaborators WHERE repo_owner=? AND repo=? AND username=?`,
		strings.ToLower(owner), normRepo(repo), strings.ToLower(strings.TrimSpace(username)))
	return err
}

// CollaboratorRole returns "read", "write", or "" for username on owner/repo.
// Safe on a nil store (returns "").
func (a *AccountStore) CollaboratorRole(owner, repo, username string) string {
	if a == nil {
		return ""
	}
	var role string
	a.db.QueryRow(`SELECT role FROM collaborators WHERE repo_owner=? AND repo=? AND username=?`,
		strings.ToLower(owner), normRepo(repo), strings.ToLower(strings.TrimSpace(username))).Scan(&role)
	return role
}

// ListCollaborators returns the grants on owner/repo, newest first.
func (a *AccountStore) ListCollaborators(owner, repo string) []Collaborator {
	if a == nil {
		return nil
	}
	rows, err := a.db.Query(`SELECT username,role,added_at FROM collaborators
		WHERE repo_owner=? AND repo=? ORDER BY added_at DESC`, strings.ToLower(owner), normRepo(repo))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Collaborator
	for rows.Next() {
		var c Collaborator
		if rows.Scan(&c.Username, &c.Role, &c.AddedAt) == nil {
			out = append(out, c)
		}
	}
	return out
}

// ReposSharedWith returns every repo shared with username. Safe on a nil store.
func (a *AccountStore) ReposSharedWith(username string) []SharedRepo {
	if a == nil {
		return nil
	}
	rows, err := a.db.Query(`SELECT repo_owner,repo,role FROM collaborators
		WHERE username=? ORDER BY repo_owner,repo`, strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SharedRepo
	for rows.Next() {
		var s SharedRepo
		if rows.Scan(&s.Owner, &s.Repo, &s.Role) == nil {
			out = append(out, s)
		}
	}
	return out
}

// DeleteRepoCollaborators drops every grant on a repo, so a deleted repo
// doesn't leave dangling access behind for whoever's slug gets that name next.
func (a *AccountStore) DeleteRepoCollaborators(owner, repo string) {
	if a == nil {
		return
	}
	a.db.Exec(`DELETE FROM collaborators WHERE repo_owner=? AND repo=?`, strings.ToLower(owner), normRepo(repo))
	a.db.Exec(`DELETE FROM collaborator_invites WHERE repo_owner=? AND repo=?`, strings.ToLower(owner), normRepo(repo))
}

// RenameRepoCollaborators moves grants to the new slug so a rename keeps its
// collaborators.
func (a *AccountStore) RenameRepoCollaborators(owner, oldRepo, newRepo string) {
	if a == nil {
		return
	}
	a.db.Exec(`UPDATE collaborators SET repo=? WHERE repo_owner=? AND repo=?`, normRepo(newRepo), strings.ToLower(owner), normRepo(oldRepo))
	a.db.Exec(`UPDATE collaborator_invites SET repo=? WHERE repo_owner=? AND repo=?`, normRepo(newRepo), strings.ToLower(owner), normRepo(oldRepo))
}

// normEmail lowercases + trims an email for case-insensitive matching.
func normEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// normRepo canonicalizes a repo slug so collaborator grants store, look up, and
// revoke under one key — otherwise a grant added via /owner/Repo and revoked via
// /owner/repo would miss, leaving stale access on a case-insensitive filesystem.
func normRepo(repo string) string { return strings.ToLower(strings.TrimSpace(repo)) }

// --- signup allowlist + waitlist -----------------------------------------
//
// When the allowlist is non-empty it gates self-serve signup: only emails on it
// may create an account; everyone else is recorded on the waitlist. An empty
// allowlist means open signup (backward-compatible). The list is seeded on boot
// from AFS_HUB_ALLOWLIST and can be extended from the admin UI (e.g. admitting
// someone off the waitlist).

// AllowEmail adds an email to the signup allowlist (idempotent).
func (a *AccountStore) AllowEmail(email string) error {
	email = normEmail(email)
	if email == "" {
		return errors.New("empty email")
	}
	_, err := a.db.Exec(`INSERT INTO allowlist(email,added_at) VALUES(?,?)
		ON CONFLICT(email) DO NOTHING`, email, time.Now().Unix())
	return err
}

// RemoveAllowed drops an email from the allowlist.
func (a *AccountStore) RemoveAllowed(email string) error {
	_, err := a.db.Exec(`DELETE FROM allowlist WHERE email=?`, normEmail(email))
	return err
}

// AllowlistActive reports whether the allowlist is gating signup (non-empty).
func (a *AccountStore) AllowlistActive() bool {
	var n int
	a.db.QueryRow(`SELECT count(*) FROM allowlist`).Scan(&n)
	return n > 0
}

// IsAllowed reports whether an email may create an account.
func (a *AccountStore) IsAllowed(email string) bool {
	var n int
	a.db.QueryRow(`SELECT count(*) FROM allowlist WHERE email=?`, normEmail(email)).Scan(&n)
	return n > 0
}

// ListAllowlist returns the allowlisted emails, newest first.
func (a *AccountStore) ListAllowlist() []string {
	rows, err := a.db.Query(`SELECT email FROM allowlist ORDER BY added_at DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if rows.Scan(&e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// WaitlistEntry is one person waiting for access.
type WaitlistEntry struct {
	Email, Username string
	CreatedAt       int64
}

// AddToWaitlist records an email requesting access (idempotent; keeps the first
// timestamp, refreshes the claimed username).
func (a *AccountStore) AddToWaitlist(email, username string) error {
	email = normEmail(email)
	if email == "" {
		return errors.New("empty email")
	}
	_, err := a.db.Exec(`INSERT INTO waitlist(email,username,created_at) VALUES(?,?,?)
		ON CONFLICT(email) DO UPDATE SET username=excluded.username`,
		email, strings.ToLower(strings.TrimSpace(username)), time.Now().Unix())
	return err
}

// RemoveFromWaitlist drops an email from the waitlist (e.g. once admitted).
func (a *AccountStore) RemoveFromWaitlist(email string) error {
	_, err := a.db.Exec(`DELETE FROM waitlist WHERE email=?`, normEmail(email))
	return err
}

// OnWaitlist reports whether an email is already on the waitlist.
func (a *AccountStore) OnWaitlist(email string) bool {
	var n int
	a.db.QueryRow(`SELECT count(*) FROM waitlist WHERE email=?`, normEmail(email)).Scan(&n)
	return n > 0
}

// ListWaitlist returns everyone waiting, newest first.
func (a *AccountStore) ListWaitlist() []WaitlistEntry {
	rows, err := a.db.Query(`SELECT email,username,created_at FROM waitlist ORDER BY created_at DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []WaitlistEntry
	for rows.Next() {
		var e WaitlistEntry
		if rows.Scan(&e.Email, &e.Username, &e.CreatedAt) == nil {
			out = append(out, e)
		}
	}
	return out
}

// ensureSecret returns a stable random session-signing secret, generating and
// persisting it on first run.
func (a *AccountStore) ensureSecret() ([]byte, error) {
	var v string
	err := a.db.QueryRow(`SELECT value FROM config WHERE key='session_secret'`).Scan(&v)
	if err == nil {
		return hex.DecodeString(v)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if _, err := a.db.Exec(`INSERT INTO config(key,value) VALUES('session_secret',?)`, hex.EncodeToString(b)); err != nil {
		return nil, err
	}
	return b, nil
}

// SessionSecret is the stable key used to sign session cookies.
func (a *AccountStore) SessionSecret() []byte { return a.secret }

// User is a hub account.
type User struct {
	ID          int64
	Username    string
	Email       string
	HasPassword bool
}

// CreateUser inserts a new account. password may be empty (a bootstrap account
// that must set one later). Username must be a valid slug and unique.
func (a *AccountStore) CreateUser(username, email, password string) (*User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !validSlug(username) {
		return nil, errors.New("username must be lowercase letters, digits, and hyphens")
	}
	if isReserved(username) {
		return nil, errors.New("that username is reserved")
	}
	hash := ""
	if password != "" {
		hash = hashPassword(password)
	}
	email = normEmail(email)
	res, err := a.db.Exec(`INSERT INTO users(username,email,password_hash,created_at) VALUES(?,?,?,?)`,
		username, email, hash, time.Now().Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrUserExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, Email: email, HasPassword: hash != ""}, nil
}

// Exists reports whether a username is taken.
func (a *AccountStore) Exists(username string) bool {
	var n int
	a.db.QueryRow(`SELECT count(*) FROM users WHERE username=?`, strings.ToLower(username)).Scan(&n)
	return n > 0
}

// VerifyPassword returns the user when the password matches.
func (a *AccountStore) VerifyPassword(username, password string) (*User, error) {
	var id int64
	var email, ph string
	err := a.db.QueryRow(`SELECT id,email,password_hash FROM users WHERE username=?`,
		strings.ToLower(username)).Scan(&id, &email, &ph)
	if err != nil || ph == "" || !verifyPassword(password, ph) {
		return nil, ErrBadLogin
	}
	return &User{ID: id, Username: strings.ToLower(username), Email: email, HasPassword: true}, nil
}

// HasPassword reports whether a user has a password set (bootstrap accounts
// start without one).
func (a *AccountStore) HasPassword(username string) bool {
	var ph string
	if err := a.db.QueryRow(`SELECT password_hash FROM users WHERE username=?`, strings.ToLower(username)).Scan(&ph); err != nil {
		return false
	}
	return ph != ""
}

// SetPassword sets or replaces a user's password.
func (a *AccountStore) SetPassword(username, password string) error {
	_, err := a.db.Exec(`UPDATE users SET password_hash=? WHERE username=?`,
		hashPassword(password), strings.ToLower(username))
	return err
}

// PAT is a Personal Access Token record (the plaintext is only ever shown once).
type PAT struct {
	ID       int64
	Name     string
	Created  int64
	LastUsed int64
}

// CreatePAT mints a new git token for a user, stores only its hash, and returns
// the plaintext once.
func (a *AccountStore) CreatePAT(username, name string) (string, error) {
	plain, _, err := a.CreatePATWithID(username, name)
	return plain, err
}

// CreatePATWithID is CreatePAT plus the new token's row id, so callers that
// manage a token's lifecycle (agent provisioning) can later revoke exactly the
// token they minted — and only that one.
func (a *AccountStore) CreatePATWithID(username, name string) (string, int64, error) {
	var uid int64
	if err := a.db.QueryRow(`SELECT id FROM users WHERE username=?`, strings.ToLower(username)).Scan(&uid); err != nil {
		return "", 0, err
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", 0, err
	}
	plain := "afs_" + base64.RawURLEncoding.EncodeToString(raw)
	res, err := a.db.Exec(`INSERT INTO tokens(user_id,name,token_hash,created_at) VALUES(?,?,?,?)`,
		uid, strings.TrimSpace(name), tokenHash(plain), time.Now().Unix())
	if err != nil {
		return "", 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", 0, err
	}
	return plain, id, nil
}

// UserForToken returns the username a PAT authenticates, updating last_used.
func (a *AccountStore) UserForToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	th := tokenHash(token)
	var username string
	if err := a.db.QueryRow(
		`SELECT u.username FROM tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=?`, th,
	).Scan(&username); err != nil {
		return "", false
	}
	a.db.Exec(`UPDATE tokens SET last_used=? WHERE token_hash=?`, time.Now().Unix(), th)
	return username, true
}

func (a *AccountStore) ListPATs(username string) ([]PAT, error) {
	rows, err := a.db.Query(
		`SELECT t.id,t.name,t.created_at,t.last_used FROM tokens t JOIN users u ON u.id=t.user_id
		 WHERE u.username=? ORDER BY t.created_at DESC`, strings.ToLower(username))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PAT
	for rows.Next() {
		var p PAT
		if err := rows.Scan(&p.ID, &p.Name, &p.Created, &p.LastUsed); err == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func (a *AccountStore) RevokePAT(username string, id int64) error {
	_, err := a.db.Exec(
		`DELETE FROM tokens WHERE id=? AND user_id=(SELECT id FROM users WHERE username=?)`,
		id, strings.ToLower(username))
	return err
}

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// ---- argon2id password hashing ----

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	const t, m, p = 3, 64 * 1024, 2
	key := argon2.IDKey([]byte(pw), salt, t, m, p, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", m, t, p,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func verifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t, p int
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, uint32(t), uint32(m), uint8(p), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
