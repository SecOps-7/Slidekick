// Package hub fans deck change messages out to every open browser.
package hub

import "sync"

// Hub is a per-deck publish/subscribe fan-out. Slow subscribers are dropped
// (their channel is closed) rather than blocking writers; clients reconnect
// and refetch.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan []byte]struct{}
}

func New() *Hub { return &Hub{subs: map[string]map[chan []byte]struct{}{}} }

// Subscribe returns a channel of messages for one deck and a cancel func.
func (h *Hub) Subscribe(deckID string) (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	if h.subs[deckID] == nil {
		h.subs[deckID] = map[chan []byte]struct{}{}
	}
	h.subs[deckID][ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			if _, ok := h.subs[deckID][ch]; ok {
				delete(h.subs[deckID], ch)
				close(ch)
			}
			if len(h.subs[deckID]) == 0 {
				delete(h.subs, deckID)
			}
			h.mu.Unlock()
		})
	}
}

// Publish sends msg to every subscriber of deckID without blocking.
func (h *Hub) Publish(deckID string, msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[deckID] {
		select {
		case ch <- msg:
		default:
			delete(h.subs[deckID], ch)
			close(ch)
		}
	}
}

// Count returns how many viewers are watching a deck.
func (h *Hub) Count(deckID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[deckID])
}
