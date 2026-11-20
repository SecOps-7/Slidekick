package service

import (
	"context"
	"errors"
	"strings"

	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/store"
)

// AddComment pins a note to a deck, slide or element. Humans use it to send
// requests to the agent; agents can read and resolve them over MCP.
func (s *Service) AddComment(ctx context.Context, deckID, slideID, elementID, text, author string) (*store.Comment, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("comment text is empty")
	}
	if len(text) > 2000 {
		return nil, errors.New("comment is longer than 2000 characters")
	}
	d, _, _, err := s.Get(ctx, deckID)
	if err != nil {
		return nil, err
	}
	if slideID != "" {
		i := d.SlideIndex(slideID)
		if i < 0 {
			return nil, errors.New("no slide with that id")
		}
		if elementID != "" {
			found := false
			for _, e := range d.Slides[i].Elements(s.Cat) {
				found = found || e.ID == elementID
			}
			if !found {
				return nil, errors.New("no element with that id on the slide")
			}
		}
	} else if elementID != "" {
		return nil, errors.New("elementId needs a slideId")
	}
	c := &store.Comment{ID: deck.NewID("c"), DeckID: deckID, SlideID: slideID, ElementID: elementID, Text: text, Author: author}
	if err := s.store.AddComment(ctx, c); err != nil {
		return nil, err
	}
	s.Publish(deckID, map[string]any{"type": "comments", "deckId": deckID})
	return c, nil
}

// Comments lists a deck's comments (open only unless all).
func (s *Service) Comments(ctx context.Context, deckID string, all bool) ([]store.Comment, error) {
	if _, _, _, err := s.Get(ctx, deckID); err != nil {
		return nil, err
	}
	return s.store.Comments(ctx, deckID, all)
}

// ResolveComment closes a comment with an optional reply.
func (s *Service) ResolveComment(ctx context.Context, deckID, id, reply string) error {
	if err := s.store.ResolveComment(ctx, deckID, id, strings.TrimSpace(reply)); err != nil {
		return err
	}
	s.Publish(deckID, map[string]any{"type": "comments", "deckId": deckID})
	return nil
}
