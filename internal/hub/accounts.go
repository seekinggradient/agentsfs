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
);`)
	return err
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
	hash := ""
	if password != "" {
		hash = hashPassword(password)
	}
	res, err := a.db.Exec(`INSERT INTO users(username,email,password_hash,created_at) VALUES(?,?,?,?)`,
		username, strings.TrimSpace(email), hash, time.Now().Unix())
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
	var uid int64
	if err := a.db.QueryRow(`SELECT id FROM users WHERE username=?`, strings.ToLower(username)).Scan(&uid); err != nil {
		return "", err
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	plain := "afs_" + base64.RawURLEncoding.EncodeToString(raw)
	if _, err := a.db.Exec(`INSERT INTO tokens(user_id,name,token_hash,created_at) VALUES(?,?,?,?)`,
		uid, strings.TrimSpace(name), tokenHash(plain), time.Now().Unix()); err != nil {
		return "", err
	}
	return plain, nil
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
