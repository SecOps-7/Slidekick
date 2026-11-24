// Package hub fans deck change messages out to every open browser.
package hub

import (
	"fmt"
	"sync"
)

// Hub is a per-deck publish/subscribe fan-out. Slow subscribers are dropped
// (their channel is closed) rather than blocking writers; clients reconnect
// and refetch.
//
// It also counts a deck's viewers: subscribers that join with a viewer key
// (a viewer page's device id, or the deck's cast TV). Several connections
// with the same key, such as two tabs on one device or a reconnect that
// overlaps the old connection, count once. When the count changes, every
// subscriber gets {"type":"viewers","count":n}.
type Hub struct {
	mu      sync.Mutex
	subs    map[string]map[chan []byte]struct{}
	viewers map[string]map[string]int // deck -> viewer key -> open connections
}

func New() *Hub {
	return &Hub{subs: map[string]map[chan []byte]struct{}{}, viewers: map[string]map[string]int{}}
}

// Subscribe returns a channel of messages for one deck and a cancel func.
func (h *Hub) Subscribe(deckID string) (<-chan []byte, func()) {
	return h.SubscribeViewer(deckID, "")
}

// SubscribeViewer is Subscribe for a connection that counts as a viewer under
// key; an empty key doesn't count (the editor, the presenter view).
func (h *Hub) SubscribeViewer(deckID, key string) (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	if h.subs[deckID] == nil {
		h.subs[deckID] = map[chan []byte]struct{}{}
	}
	h.subs[deckID][ch] = struct{}{}
	changed := h.addViewer(deckID, key, 1)
	h.mu.Unlock()
	if changed {
		h.announce(deckID)
	}
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
			changed := h.addViewer(deckID, key, -1)
			h.mu.Unlock()
			if changed {
				h.announce(deckID)
			}
		})
	}
}

// addViewer adjusts key's connection count; it reports whether the number of
// viewers changed. Callers hold h.mu.
func (h *Hub) addViewer(deckID, key string, d int) bool {
	if key == "" {
		return false
	}
	m := h.viewers[deckID]
	if m == nil {
		m = map[string]int{}
		h.viewers[deckID] = m
	}
	before := m[key]
	m[key] += d
	after := m[key]
	if after <= 0 {
		delete(m, key)
	}
	if len(m) == 0 {
		delete(h.viewers, deckID)
	}
	return (before > 0) != (after > 0)
}

// ViewersPrefix starts every viewer-count message, so a connection that
// shouldn't see the count can skip it without parsing.
var ViewersPrefix = []byte(`{"type":"viewers",`)

// ViewersMsg is the viewer-count message for n viewers.
func ViewersMsg(n int) []byte { return fmt.Appendf(nil, `{"type":"viewers","count":%d}`, n) }

func (h *Hub) announce(deckID string) { h.Publish(deckID, ViewersMsg(h.Viewers(deckID))) }

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

// Count returns how many browsers are connected to a deck: viewers, editors
// and presenters alike.
func (h *Hub) Count(deckID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[deckID])
}

// Viewers returns how many viewers are watching a deck: distinct viewer
// devices, with a cast TV counting as one.
func (h *Hub) Viewers(deckID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.viewers[deckID])
}
