package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Accounts: users, browser sessions, personal API tokens, single sign-on
// identities and providers, and instance settings. Secrets are never stored in
// plain text here: passwords as argon2id hashes, tokens and session ids as
// SHA-256, provider secrets encrypted by the caller (see internal/secret).

const accountsSchema = `
CREATE TABLE IF NOT EXISTS users (
  id            TEXT PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
  email         TEXT NOT NULL DEFAULT '',
  name          TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL,
  password_hash TEXT NOT NULL DEFAULT '',
  disabled      INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  last_login    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS users_email ON users(email COLLATE NOCASE);
CREATE TABLE IF NOT EXISTS sessions (
  id_hash    TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  via        TEXT NOT NULL DEFAULT 'password'
);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id);
CREATE TABLE IF NOT EXISTS api_tokens (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  hash       TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  last_used  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS identities (
  provider_id TEXT NOT NULL,
  subject     TEXT NOT NULL,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at  TEXT NOT NULL,
  PRIMARY KEY (provider_id, subject)
);
CREATE TABLE IF NOT EXISTS sso_providers (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  issuer       TEXT NOT NULL,
  client_id    TEXT NOT NULL,
  secret_enc   TEXT NOT NULL DEFAULT '',
  scopes       TEXT NOT NULL DEFAULT '',
  groups_claim TEXT NOT NULL DEFAULT 'groups',
  trust_email  INTEGER NOT NULL DEFAULT 0,
  auto_create  INTEGER NOT NULL DEFAULT 0,
  default_role TEXT NOT NULL DEFAULT 'viewer',
  role_rules   TEXT NOT NULL DEFAULT '[]',
  enabled      INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sso_flows (
  state       TEXT PRIMARY KEY,
  provider_id TEXT NOT NULL,
  nonce       TEXT NOT NULL,
  verifier    TEXT NOT NULL,
  next        TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

func migrateAccounts(db *sql.DB) error {
	if _, err := db.Exec(accountsSchema); err != nil {
		return err
	}
	// Decks gained an owner. Existing decks keep '' until the first admin
	// exists, then become theirs (see CreateUser).
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('decks') WHERE name='owner'`).Scan(&n)
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE decks ADD COLUMN owner TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	// Decks gained presenter links (the key is made when the first one is shared).
	n = 0
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('deck_access') WHERE name='present_key'`).Scan(&n)
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE deck_access ADD COLUMN present_key TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}

var ErrExists = errors.New("already exists")

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"-"`
	HasPassword  bool      `json:"hasPassword"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"createdAt"`
	LastLogin    string    `json:"lastLogin,omitempty"`
	SSO          []string  `json:"sso,omitempty"` // linked provider ids
}

const userCols = `id,username,email,name,role,password_hash,disabled,created_at,last_login`

func scanUser(sc interface{ Scan(...any) error }) (*User, error) {
	var u User
	var dis int
	var ts string
	if err := sc.Scan(&u.ID, &u.Username, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &dis, &ts, &u.LastLogin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Disabled, u.CreatedAt, u.HasPassword = dis == 1, parseTime(ts), u.PasswordHash != ""
	return &u, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CountActiveAdmins counts enabled admins, so the last one can't be removed.
func (s *Store) CountActiveAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role='admin' AND disabled=0`).Scan(&n)
	return n, err
}

// CreateUser adds a user. The first admin also becomes the owner of every
// deck created before accounts existed.
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ts := now()
	_, err = tx.ExecContext(ctx, `INSERT INTO users(id,username,email,name,role,password_hash,disabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		u.ID, u.Username, u.Email, u.Name, u.Role, u.PasswordHash, b2i(u.Disabled), ts, ts)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrExists
		}
		return err
	}
	if u.Role == "admin" {
		if _, err := tx.ExecContext(ctx, `UPDATE decks SET owner=? WHERE owner=''`, u.ID); err != nil {
			return err
		}
	}
	u.CreatedAt = parseTime(ts)
	return tx.Commit()
}

func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
}

func (s *Store) UserByName(ctx context.Context, username string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username=?`, username))
}

// UsersByEmail returns users with this email (case-insensitive).
func (s *Store) UsersByEmail(ctx context.Context, email string) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users WHERE email<>'' AND email=? COLLATE NOCASE`, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, u)
	}
	rows.Close()
	by := map[string]*User{}
	for _, u := range out {
		by[u.ID] = u
	}
	links, err := s.db.QueryContext(ctx, `SELECT user_id, provider_id FROM identities`)
	if err == nil {
		defer links.Close()
		for links.Next() {
			var uid, pid string
			if links.Scan(&uid, &pid) == nil && by[uid] != nil {
				by[uid].SSO = append(by[uid].SSO, pid)
			}
		}
	}
	return out, nil
}

// UpdateUser saves username, email, name, role and disabled. Disabling a user
// ends their sessions; their API tokens stop working because every request
// re-checks the user.
func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET username=?,email=?,name=?,role=?,disabled=?,updated_at=? WHERE id=?`,
		u.Username, u.Email, u.Name, u.Role, b2i(u.Disabled), now(), u.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrExists
		}
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	if u.Disabled {
		s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, u.ID)
	}
	return nil
}

// SetPassword replaces a user's password hash and ends their other sessions.
func (s *Store) SetPassword(ctx context.Context, id, hash, keepSession string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash=?,updated_at=? WHERE id=?`, hash, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND id_hash<>?`, id, keepSession)
	return err
}

func (s *Store) TouchLogin(ctx context.Context, id string) {
	s.db.ExecContext(ctx, `UPDATE users SET last_login=? WHERE id=?`, now(), id)
}

// DeleteUser removes a user. Their decks pass to newOwner ("" leaves them
// unowned: admins only).
func (s *Store) DeleteUser(ctx context.Context, id, newOwner string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE decks SET owner=? WHERE owner=?`, newOwner, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}

// ---- sessions ----

type Session struct {
	UserID, CSRF, Via string
	Expires           time.Time
}

func (s *Store) CreateSession(ctx context.Context, idHash, userID, csrf, via string, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id_hash,user_id,csrf,created_at,expires_at,via) VALUES(?,?,?,?,?,?)`,
		idHash, userID, csrf, now(), expires.UTC().Format(time.RFC3339Nano), via)
	return err
}

func (s *Store) GetSession(ctx context.Context, idHash string) (*Session, error) {
	var ss Session
	var exp string
	err := s.db.QueryRowContext(ctx, `SELECT user_id,csrf,expires_at,via FROM sessions WHERE id_hash=?`, idHash).Scan(&ss.UserID, &ss.CSRF, &exp, &ss.Via)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ss.Expires = parseTime(exp)
	if time.Now().After(ss.Expires) {
		s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash=?`, idHash)
		return nil, ErrNotFound
	}
	return &ss, nil
}

func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash=?`, idHash)
	return err
}

// PruneExpired removes expired sessions and stale sign-in flows.
func (s *Store) PruneExpired(ctx context.Context) {
	s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<?`, now())
	s.db.ExecContext(ctx, `DELETE FROM sso_flows WHERE created_at<?`, time.Now().Add(-15*time.Minute).UTC().Format(time.RFC3339Nano))
}

// ---- personal API tokens ----

type APIToken struct {
	ID        string    `json:"id"`
	UserID    string    `json:"-"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	LastUsed  string    `json:"lastUsed,omitempty"`
}

func (s *Store) CreateAPIToken(ctx context.Context, t *APIToken, hash string) error {
	ts := now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_tokens(id,user_id,name,hash,created_at) VALUES(?,?,?,?,?)`, t.ID, t.UserID, t.Name, hash, ts)
	t.CreatedAt = parseTime(ts)
	return err
}

func (s *Store) APITokens(ctx context.Context, userID string) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,name,created_at,last_used FROM api_tokens WHERE user_id=? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		var t APIToken
		var ts string
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &ts, &t.LastUsed); err != nil {
			return nil, err
		}
		t.CreatedAt = parseTime(ts)
		out = append(out, t)
	}
	return out, rows.Err()
}

// UserForAPIToken returns the user a token hash belongs to and marks it used.
func (s *Store) UserForAPIToken(ctx context.Context, hash string) (string, error) {
	var id, uid string
	err := s.db.QueryRowContext(ctx, `SELECT id,user_id FROM api_tokens WHERE hash=?`, hash).Scan(&id, &uid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err == nil {
		s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used=? WHERE id=?`, now(), id)
	}
	return uid, err
}

func (s *Store) DeleteAPIToken(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// ---- single sign-on ----

type SSOProvider struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Issuer      string     `json:"issuer"`
	ClientID    string     `json:"clientId"`
	SecretEnc   string     `json:"-"`
	HasSecret   bool       `json:"hasSecret"`
	Scopes      string     `json:"scopes"`
	GroupsClaim string     `json:"groupsClaim"`
	TrustEmail  bool       `json:"trustEmail"`
	AutoCreate  bool       `json:"autoCreate"`
	DefaultRole string     `json:"defaultRole"`
	RoleRules   []RoleRule `json:"roleRules"`
	Enabled     bool       `json:"enabled"`
}

// RoleRule maps a claim value to a role: Claim is "email", "domain" or "group".
type RoleRule struct {
	Claim string `json:"claim"`
	Value string `json:"value"`
	Role  string `json:"role"`
}

const ssoCols = `id,name,issuer,client_id,secret_enc,scopes,groups_claim,trust_email,auto_create,default_role,role_rules,enabled`

func scanSSO(sc interface{ Scan(...any) error }) (*SSOProvider, error) {
	var p SSOProvider
	var te, ac, en int
	var rules string
	if err := sc.Scan(&p.ID, &p.Name, &p.Issuer, &p.ClientID, &p.SecretEnc, &p.Scopes, &p.GroupsClaim, &te, &ac, &p.DefaultRole, &rules, &en); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.TrustEmail, p.AutoCreate, p.Enabled, p.HasSecret = te == 1, ac == 1, en == 1, p.SecretEnc != ""
	p.RoleRules = parseRules(rules)
	return &p, nil
}

func (s *Store) SSOProviders(ctx context.Context) ([]*SSOProvider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+ssoCols+` FROM sso_providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SSOProvider{}
	for rows.Next() {
		p, err := scanSSO(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetSSOProvider(ctx context.Context, id string) (*SSOProvider, error) {
	return scanSSO(s.db.QueryRowContext(ctx, `SELECT `+ssoCols+` FROM sso_providers WHERE id=?`, id))
}

// SaveSSOProvider inserts or updates a provider (all fields, including SecretEnc).
func (s *Store) SaveSSOProvider(ctx context.Context, p *SSOProvider) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sso_providers(`+ssoCols+`,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, issuer=excluded.issuer, client_id=excluded.client_id, secret_enc=excluded.secret_enc,
 scopes=excluded.scopes, groups_claim=excluded.groups_claim, trust_email=excluded.trust_email, auto_create=excluded.auto_create,
 default_role=excluded.default_role, role_rules=excluded.role_rules, enabled=excluded.enabled`,
		p.ID, p.Name, p.Issuer, p.ClientID, p.SecretEnc, p.Scopes, p.GroupsClaim, b2i(p.TrustEmail), b2i(p.AutoCreate), p.DefaultRole, rulesJSON(p.RoleRules), b2i(p.Enabled), now())
	return err
}

func (s *Store) DeleteSSOProvider(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM identities WHERE provider_id=?`, id); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM sso_providers WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UserForIdentity(ctx context.Context, providerID, subject string) (string, error) {
	var uid string
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM identities WHERE provider_id=? AND subject=?`, providerID, subject).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return uid, err
}

func (s *Store) LinkIdentity(ctx context.Context, providerID, subject, userID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO identities(provider_id,subject,user_id,created_at) VALUES(?,?,?,?)`, providerID, subject, userID, now())
	return err
}

type SSOFlow struct {
	State, ProviderID, Nonce, Verifier, Next string
	Created                                  time.Time
}

func (s *Store) SaveSSOFlow(ctx context.Context, f SSOFlow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sso_flows(state,provider_id,nonce,verifier,next,created_at) VALUES(?,?,?,?,?,?)`,
		f.State, f.ProviderID, f.Nonce, f.Verifier, f.Next, f.Created.UTC().Format(time.RFC3339Nano))
	return err
}

// TakeSSOFlow returns and removes a flow: each state works once.
func (s *Store) TakeSSOFlow(ctx context.Context, state string) (*SSOFlow, error) {
	var f SSOFlow
	var ts string
	err := s.db.QueryRowContext(ctx, `SELECT state,provider_id,nonce,verifier,next,created_at FROM sso_flows WHERE state=?`, state).
		Scan(&f.State, &f.ProviderID, &f.Nonce, &f.Verifier, &f.Next, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.db.ExecContext(ctx, `DELETE FROM sso_flows WHERE state=?`, state)
	f.Created = parseTime(ts)
	return &f, nil
}

// ---- settings and deck owners ----

func (s *Store) Setting(ctx context.Context, key string) string {
	var v string
	s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return v
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) DeckOwner(ctx context.Context, id string) (string, error) {
	var o string
	err := s.db.QueryRowContext(ctx, `SELECT owner FROM decks WHERE id=?`, id).Scan(&o)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return o, err
}

func (s *Store) SetDeckOwner(ctx context.Context, id, owner string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE decks SET owner=? WHERE id=?`, owner, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
