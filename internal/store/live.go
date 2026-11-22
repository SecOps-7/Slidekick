package store

import (
	"context"
	"strings"
	"time"
)

// LiveSlide is a live slide's current content as agents left it: its regions
// (the elements and their values) and when they last changed.
type LiveSlide struct {
	SlideID   string
	Regions   string // the slide's regions object as JSON
	UpdatedAt time.Time
}

// SetLiveSlide stores a live slide's current regions, replacing the previous
// ones. It never touches the deck's versions.
func (s *Store) SetLiveSlide(ctx context.Context, deckID, slideID, regions string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO live_slides(deck_id,slide_id,regions,updated_at) VALUES(?,?,?,?)
ON CONFLICT(deck_id,slide_id) DO UPDATE SET regions=excluded.regions, updated_at=excluded.updated_at`,
		deckID, slideID, regions, at.UTC().Format(time.RFC3339Nano))
	return err
}

// LiveSlides returns a deck's stored live slides.
func (s *Store) LiveSlides(ctx context.Context, deckID string) ([]LiveSlide, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT slide_id,regions,updated_at FROM live_slides WHERE deck_id=?`, deckID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LiveSlide
	for rows.Next() {
		var v LiveSlide
		var at string
		if err := rows.Scan(&v.SlideID, &v.Regions, &at); err != nil {
			return nil, err
		}
		v.UpdatedAt = parseTime(at)
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteLiveSlides forgets the live content of these slides (a deck edit to
// the slide replaces it). Ids that aren't live slides are ignored.
func (s *Store) DeleteLiveSlides(ctx context.Context, deckID string, slideIDs []string) error {
	if len(slideIDs) == 0 {
		return nil
	}
	args := []any{deckID}
	for _, id := range slideIDs {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM live_slides WHERE deck_id=? AND slide_id IN (?`+strings.Repeat(",?", len(slideIDs)-1)+`)`, args...)
	return err
}
