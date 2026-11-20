package service

import (
	"context"
	"fmt"
)

// FocusResult names the slide that was brought into view.
type FocusResult struct {
	SlideID string
	Index   int // zero-based
}

// Focus asks every browser watching the deck (editors and viewers that follow
// along) to show one slide: slide is 1-based; slideID wins when both are set.
func (s *Service) Focus(ctx context.Context, deckID string, slide int, slideID, actor string) (*FocusResult, error) {
	d, _, _, err := s.Get(ctx, deckID)
	if err != nil {
		return nil, err
	}
	idx := -1
	if slideID != "" {
		idx = d.SlideIndex(slideID)
	} else if slide >= 1 && slide <= len(d.Slides) {
		idx = slide - 1
	}
	if idx < 0 {
		return nil, fmt.Errorf("no such slide (the deck has %d slides; pass slide 1 to %d or a slide_id)", len(d.Slides), len(d.Slides))
	}
	id := d.Slides[idx].ID
	s.Publish(deckID, map[string]any{"type": "focus", "slideId": id, "index": idx, "actor": actor})
	return &FocusResult{SlideID: id, Index: idx}, nil
}
