package store

import (
	"context"
	"database/sql"
	"errors"
)

// Access is a deck's sharing settings. The zero value is a public deck with
// no deck-scoped edit token.
type Access struct {
	Private    bool
	ViewKey    string // needed to view a private deck
	EditHash   string // sha256 (hex) of the deck's edit token, if one was issued
	PresentKey string // presenter links: present this deck without signing in
}

// Access returns a deck's sharing settings.
func (s *Store) Access(ctx context.Context, deckID string) (Access, error) {
	var a Access
	var priv int
	err := s.db.QueryRowContext(ctx, `SELECT private,view_key,edit_hash,present_key FROM deck_access WHERE deck_id=?`, deckID).
		Scan(&priv, &a.ViewKey, &a.EditHash, &a.PresentKey)
	if errors.Is(err, sql.ErrNoRows) {
		return Access{}, nil
	}
	a.Private = priv == 1
	return a, err
}

// SetAccess stores a deck's sharing settings.
func (s *Store) SetAccess(ctx context.Context, deckID string, a Access) error {
	priv := 0
	if a.Private {
		priv = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO deck_access(deck_id,private,view_key,edit_hash,present_key) VALUES(?,?,?,?,?)
ON CONFLICT(deck_id) DO UPDATE SET private=excluded.private, view_key=excluded.view_key, edit_hash=excluded.edit_hash, present_key=excluded.present_key`,
		deckID, priv, a.ViewKey, a.EditHash, a.PresentKey)
	return err
}

// DeckForEditHash finds the deck whose edit token hashes to hash.
func (s *Store) DeckForEditHash(ctx context.Context, hash string) (string, error) {
	if hash == "" {
		return "", ErrNotFound
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT deck_id FROM deck_access WHERE edit_hash=?`, hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}
