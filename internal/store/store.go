// Package store persists decks and their version history in SQLite
// (pure Go driver, no CGO).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// KeepVersions is how many recent versions of each deck are kept for undo and history.
const KeepVersions = 500

// ErrNotFound is returned when a deck does not exist.
var ErrNotFound = errors.New("deck not found")

// ErrVersionMismatch is returned when a compare-and-swap update loses a race.
var ErrVersionMismatch = errors.New("deck version changed")

type Store struct{ db *sql.DB }

// Summary is one row of the deck list.
type Summary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Theme     string    `json:"theme"`
	Version   int       `json:"version"`
	Slides    int       `json:"slides"`
	UpdatedAt time.Time `json:"updatedAt"`
	Owner     string    `json:"owner,omitempty"`
	OwnerName string    `json:"ownerName,omitempty"`
	Private   bool      `json:"private"`
}

// VersionEntry is one entry of a deck's history.
type VersionEntry struct {
	Version   int       `json:"version"`
	Actor     string    `json:"actor"`
	Ops       []byte    `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
}

const schema = `
CREATE TABLE IF NOT EXISTS decks (
  id         TEXT PRIMARY KEY,
  title      TEXT NOT NULL,
  theme      TEXT NOT NULL,
  slides     INTEGER NOT NULL DEFAULT 0,
  version    INTEGER NOT NULL,
  doc        BLOB NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS versions (
  deck_id    TEXT NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
  version    INTEGER NOT NULL,
  actor      TEXT NOT NULL,
  ops        BLOB NOT NULL,
  doc        BLOB NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (deck_id, version)
);
CREATE TABLE IF NOT EXISTS comments (
  id          TEXT PRIMARY KEY,
  deck_id     TEXT NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
  slide_id    TEXT NOT NULL DEFAULT '',
  element_id  TEXT NOT NULL DEFAULT '',
  text        TEXT NOT NULL,
  author      TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'open',
  reply       TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL,
  resolved_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS comments_deck ON comments(deck_id, status);
CREATE TABLE IF NOT EXISTS assets (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  mime       TEXT NOT NULL,
  ext        TEXT NOT NULL,
  size       INTEGER NOT NULL,
  sha256     TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS deck_access (
  deck_id   TEXT PRIMARY KEY REFERENCES decks(id) ON DELETE CASCADE,
  private   INTEGER NOT NULL DEFAULT 0,
  view_key  TEXT NOT NULL DEFAULT '',
  edit_hash TEXT NOT NULL DEFAULT '',
  present_key TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS deck_access_edit ON deck_access(edit_hash);
-- A live slide's current content as agents left it (update_live): one row
-- per slide, overwritten in place, so live updates never add versions.
CREATE TABLE IF NOT EXISTS live_slides (
  deck_id    TEXT NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
  slide_id   TEXT NOT NULL,
  regions    TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (deck_id, slide_id)
);
`

// Open opens (creating if needed) the database at dir/slidekick.db.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dir, "slidekick.db") +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite has one writer; keep it simple and safe.
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	if err := migrateAccounts(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func parseTime(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

// Create inserts a new deck at version 1.
func (s *Store) Create(ctx context.Context, id, title, theme string, slides int, doc []byte, actor string) error {
	return s.CreateOwned(ctx, id, "", title, theme, slides, doc, actor)
}

// CreateOwned stores a new deck owned by a user ("" = no owner: only admins
// and the server token may change it).
func (s *Store) CreateOwned(ctx context.Context, id, owner, title, theme string, slides int, doc []byte, actor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ts := now()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO decks(id,title,theme,slides,version,doc,created_at,updated_at,owner) VALUES(?,?,?,?,1,?,?,?,?)`,
		id, title, theme, slides, doc, ts, ts, owner); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO versions(deck_id,version,actor,ops,doc,created_at) VALUES(?,1,?,?,?,?)`,
		id, actor, []byte("[]"), doc, ts); err != nil {
		return err
	}
	return tx.Commit()
}

// Get returns the current document and version of a deck.
func (s *Store) Get(ctx context.Context, id string) ([]byte, int, error) {
	var doc []byte
	var ver int
	err := s.db.QueryRowContext(ctx, `SELECT doc, version FROM decks WHERE id=?`, id).Scan(&doc, &ver)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	return doc, ver, err
}

// Update writes a new version if the stored version still equals base.
func (s *Store) Update(ctx context.Context, id string, base int, title, theme string, slides int, doc, ops []byte, actor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ts := now()
	res, err := tx.ExecContext(ctx,
		`UPDATE decks SET title=?, theme=?, slides=?, version=version+1, doc=?, updated_at=? WHERE id=? AND version=?`,
		title, theme, slides, doc, ts, id, base)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrVersionMismatch
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO versions(deck_id,version,actor,ops,doc,created_at) VALUES(?,?,?,?,?,?)`,
		id, base+1, actor, ops, doc, ts); err != nil {
		return err
	}
	// Keep the most recent KeepVersions versions (plus version 1) per deck.
	if _, err := tx.ExecContext(ctx, "DELETE FROM versions WHERE deck_id=? AND version>1 AND version<=?", id, base+1-KeepVersions); err != nil {
		return err
	}
	return tx.Commit()
}

// List returns all decks, most recently updated first.
func (s *Store) List(ctx context.Context) ([]Summary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT d.id,d.title,d.theme,d.version,d.slides,d.updated_at,d.owner,COALESCE(u.username,''),COALESCE(a.private,0)
		 FROM decks d LEFT JOIN users u ON u.id=d.owner LEFT JOIN deck_access a ON a.deck_id=d.id ORDER BY d.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var r Summary
		var ts string
		var priv int
		if err := rows.Scan(&r.ID, &r.Title, &r.Theme, &r.Version, &r.Slides, &ts, &r.Owner, &r.OwnerName, &priv); err != nil {
			return nil, err
		}
		r.UpdatedAt = parseTime(ts)
		r.Private = priv == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// History returns a deck's versions, newest first, without documents.
func (s *Store) History(ctx context.Context, id string, limit int) ([]VersionEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT version,actor,ops,created_at FROM versions WHERE deck_id=? ORDER BY version DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VersionEntry
	for rows.Next() {
		var v VersionEntry
		var ts string
		if err := rows.Scan(&v.Version, &v.Actor, &v.Ops, &ts); err != nil {
			return nil, err
		}
		v.CreatedAt = parseTime(ts)
		out = append(out, v)
	}
	return out, rows.Err()
}

// VersionDoc returns the document as it was at a given version.
func (s *Store) VersionDoc(ctx context.Context, id string, version int) ([]byte, error) {
	var doc []byte
	err := s.db.QueryRowContext(ctx, `SELECT doc FROM versions WHERE deck_id=? AND version=?`, id, version).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return doc, err
}

// Asset is an uploaded file's metadata. The bytes live on disk.
type Asset struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	Ext       string    `json:"ext"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"createdAt"`
}

// URL is the path the asset is served at.
func (a *Asset) URL() string { return "/files/" + a.ID + a.Ext }

// PutAsset records an asset; re-uploading identical bytes is a no-op.
func (s *Store) PutAsset(ctx context.Context, a *Asset) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO assets(id,name,mime,ext,size,sha256,created_at) VALUES(?,?,?,?,?,?,?)`,
		a.ID, a.Name, a.MIME, a.Ext, a.Size, a.SHA256, now())
	return err
}

// GetAsset returns an asset's metadata.
func (s *Store) GetAsset(ctx context.Context, id string) (*Asset, error) {
	var a Asset
	var ts string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,mime,ext,size,sha256,created_at FROM assets WHERE id=?`, id).
		Scan(&a.ID, &a.Name, &a.MIME, &a.Ext, &a.Size, &a.SHA256, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	a.CreatedAt = parseTime(ts)
	return &a, err
}

// Comment is a note pinned to a deck, slide or element ("Send to agent").
type Comment struct {
	ID         string    `json:"id"`
	DeckID     string    `json:"deckId"`
	SlideID    string    `json:"slideId,omitempty"`
	ElementID  string    `json:"elementId,omitempty"`
	Text       string    `json:"text"`
	Author     string    `json:"author"`
	Status     string    `json:"status" jsonschema:"open or resolved"`
	Reply      string    `json:"reply,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	ResolvedAt string    `json:"resolvedAt,omitempty"`
}

func (s *Store) AddComment(ctx context.Context, c *Comment) error {
	ts := now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO comments(id,deck_id,slide_id,element_id,text,author,status,created_at) VALUES(?,?,?,?,?,?,'open',?)`,
		c.ID, c.DeckID, c.SlideID, c.ElementID, c.Text, c.Author, ts)
	c.Status, c.CreatedAt = "open", parseTime(ts)
	return err
}

// Comments lists a deck's comments, oldest first; resolved ones only when all is true.
func (s *Store) Comments(ctx context.Context, deckID string, all bool) ([]Comment, error) {
	q := `SELECT id,deck_id,slide_id,element_id,text,author,status,reply,created_at,resolved_at FROM comments WHERE deck_id=?`
	if !all {
		q += ` AND status='open'`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at`, deckID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		var c Comment
		var ts string
		if err := rows.Scan(&c.ID, &c.DeckID, &c.SlideID, &c.ElementID, &c.Text, &c.Author, &c.Status, &c.Reply, &ts, &c.ResolvedAt); err != nil {
			return nil, err
		}
		c.CreatedAt = parseTime(ts)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResolveComment marks a comment resolved with an optional reply.
func (s *Store) ResolveComment(ctx context.Context, deckID, id, reply string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE comments SET status='resolved', reply=?, resolved_at=? WHERE id=? AND deck_id=?`, reply, now(), id, deckID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// CountOpenComments returns how many comments on a deck are open.
func (s *Store) CountOpenComments(ctx context.Context, deckID string) int {
	var n int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE deck_id=? AND status='open'`, deckID).Scan(&n)
	return n
}
