package store

import "context"

// DeleteDeck removes a deck with its versions, comments and sharing settings
// (they cascade). Uploaded files stay: they are shared and content-addressed.
func (s *Store) DeleteDeck(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM decks WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
