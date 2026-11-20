package service

import "context"

// Delete removes a deck for good and tells everyone watching it.
func (s *Service) Delete(ctx context.Context, id, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.DeleteDeck(ctx, id); err != nil {
		return err
	}
	s.Publish(id, map[string]any{"type": "deleted", "actor": actor})
	return nil
}
