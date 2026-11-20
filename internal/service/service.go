// Package service is the deck service: the only code path that changes deck
// state. Every write is a JSON Patch that is validated, versioned, linted,
// stored and then broadcast to open browsers.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	jsonpatch "github.com/evanphx/json-patch/v5"

	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/hub"
	"github.com/SecOps-7/slidekick/internal/lint"
	"github.com/SecOps-7/slidekick/internal/store"
)

type Service struct {
	store   *store.Store
	v       *deck.Validator
	Cat     *deck.Catalog
	Themes  *deck.ThemeSet
	Hub     *hub.Hub
	BaseURL string
	// FilesDir holds uploaded assets; empty disables uploads.
	FilesDir string
	mu       sync.Mutex // serialises writes inside this process
}

func New(st *store.Store, v *deck.Validator, cat *deck.Catalog, themes *deck.ThemeSet, h *hub.Hub, baseURL string) *Service {
	return &Service{store: st, v: v, Cat: cat, Themes: themes, Hub: h, BaseURL: strings.TrimRight(baseURL, "/")}
}

// Result is returned by every write: the new version plus lint findings, so
// an agent can fix problems without another call.
type Result struct {
	DeckID  string       `json:"deckId"`
	Version int          `json:"version"`
	Changed []string     `json:"changed,omitempty" jsonschema:"ids of slides and elements touched by this write"`
	Lint    []lint.Issue `json:"lint"`
	URL     string       `json:"url" jsonschema:"live viewer link"`
	// OpenComments counts notes humans pinned for the agent (see list_comments).
	OpenComments int `json:"openComments,omitempty" jsonschema:"open notes from humans; read them with list_comments"`
}

// ConflictError means the caller's base version is stale.
type ConflictError struct{ Base, Current int }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("base_version %d is stale: the deck is now at version %d (someone else edited it). Re-read it with get_deck and retry.", e.Base, e.Current)
}

// ErrNotFound is returned for unknown decks.
var ErrNotFound = store.ErrNotFound

// ShareURL is the live viewer link for a deck.
func (s *Service) ShareURL(id string) string { return s.BaseURL + "/d/" + id }

// Get returns the parsed deck, its raw JSON and version.
func (s *Service) Get(ctx context.Context, id string) (*deck.Deck, []byte, int, error) {
	raw, ver, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, nil, 0, err
	}
	raw, _, err = deck.Migrate(raw)
	if err != nil {
		return nil, nil, 0, err
	}
	d, err := deck.Parse(raw)
	return d, raw, ver, err
}

func (s *Service) List(ctx context.Context) ([]store.Summary, error) { return s.store.List(ctx) }

func (s *Service) History(ctx context.Context, id string, limit int) ([]store.VersionEntry, error) {
	return s.store.History(ctx, id, limit)
}

// Lint lints the current version of a deck.
func (s *Service) Lint(ctx context.Context, id string) ([]lint.Issue, int, error) {
	d, _, ver, err := s.Get(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	return lint.Deck(d, s.Cat, s.Themes), ver, nil
}

// Create makes a new deck. Slides may be nil; ids are assigned where missing.
func (s *Service) Create(ctx context.Context, title, theme, aspect string, slides []any, actor string) (*Result, error) {
	if theme == "" {
		theme = "aurora"
	}
	if aspect == "" {
		aspect = "16:9"
	}
	if slides == nil {
		slides = []any{}
	}
	norm, err := normalize(slides)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{
		"schemaVersion": deck.CurrentSchemaVersion,
		"id":            deck.NewID("d"),
		"title":         title,
		"theme":         theme,
		"aspect":        aspect,
		"slides":        deck.AssignIDs(norm),
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	d, err := s.v.Validate(raw)
	if err != nil {
		return nil, err
	}
	if err := s.store.Create(ctx, d.ID, d.Title, d.Theme, len(d.Slides), raw, actor); err != nil {
		return nil, err
	}
	return &Result{DeckID: d.ID, Version: 1, Lint: lint.Deck(d, s.Cat, s.Themes), URL: s.ShareURL(d.ID)}, nil
}

// Patch applies raw JSON Patch operations.
func (s *Service) Patch(ctx context.Context, id string, base *int, ops []deck.Op, actor string) (*Result, error) {
	return s.mutate(ctx, id, base, actor, func(*deck.Deck) ([]deck.Op, error) { return ops, nil })
}

// AddSlide inserts a slide at position (end when nil).
func (s *Service) AddSlide(ctx context.Context, id string, base *int, slide map[string]any, position *int, actor string) (*Result, error) {
	return s.mutate(ctx, id, base, actor, func(d *deck.Deck) ([]deck.Op, error) {
		if slide == nil {
			return nil, errors.New("slide is required")
		}
		path := "/slides/-"
		if position != nil {
			p := *position
			if p < 0 || p > len(d.Slides) {
				return nil, fmt.Errorf("position %d is out of range 0..%d", p, len(d.Slides))
			}
			path = "/slides/" + strconv.Itoa(p)
		}
		return []deck.Op{{Op: "add", Path: path, Value: slide}}, nil
	})
}

var slideFields = map[string]bool{"layout": true, "regions": true, "transition": true, "notes": true, "background": true}

// UpdateSlide replaces the given top-level fields of a slide. A null value
// removes an optional field.
func (s *Service) UpdateSlide(ctx context.Context, id string, base *int, slideID string, fields map[string]any, actor string) (*Result, error) {
	return s.mutate(ctx, id, base, actor, func(d *deck.Deck) ([]deck.Op, error) {
		i := d.SlideIndex(slideID)
		if i < 0 {
			return nil, fmt.Errorf("no slide with id %q", slideID)
		}
		var ops []deck.Op
		for k, v := range fields {
			if !slideFields[k] {
				return nil, fmt.Errorf("cannot update slide field %q (allowed: layout, regions, transition, notes, background)", k)
			}
			p := fmt.Sprintf("/slides/%d/%s", i, k)
			if v == nil {
				ops = append(ops, deck.Op{Op: "remove", Path: p})
			} else {
				ops = append(ops, deck.Op{Op: "add", Path: p, Value: v})
			}
		}
		return ops, nil
	})
}

func (s *Service) DeleteSlide(ctx context.Context, id string, base *int, slideID, actor string) (*Result, error) {
	return s.mutate(ctx, id, base, actor, func(d *deck.Deck) ([]deck.Op, error) {
		i := d.SlideIndex(slideID)
		if i < 0 {
			return nil, fmt.Errorf("no slide with id %q", slideID)
		}
		return []deck.Op{{Op: "remove", Path: fmt.Sprintf("/slides/%d", i)}}, nil
	})
}

func (s *Service) MoveSlide(ctx context.Context, id string, base *int, slideID string, to int, actor string) (*Result, error) {
	return s.mutate(ctx, id, base, actor, func(d *deck.Deck) ([]deck.Op, error) {
		i := d.SlideIndex(slideID)
		if i < 0 {
			return nil, fmt.Errorf("no slide with id %q", slideID)
		}
		if to < 0 || to >= len(d.Slides) {
			return nil, fmt.Errorf("to %d is out of range 0..%d", to, len(d.Slides)-1)
		}
		if to == i {
			return nil, errors.New("slide is already at that position")
		}
		return []deck.Op{{Op: "move", From: fmt.Sprintf("/slides/%d", i), Path: fmt.Sprintf("/slides/%d", to)}}, nil
	})
}

func (s *Service) SetTheme(ctx context.Context, id string, base *int, theme, actor string) (*Result, error) {
	return s.mutate(ctx, id, base, actor, func(*deck.Deck) ([]deck.Op, error) {
		return []deck.Op{{Op: "replace", Path: "/theme", Value: theme}}, nil
	})
}

// Event is broadcast to open browsers after every write.
type Event struct {
	Type        string    `json:"type"`
	DeckID      string    `json:"deckId"`
	Version     int       `json:"version"`
	BaseVersion int       `json:"baseVersion"`
	Actor       string    `json:"actor"`
	Ops         []deck.Op `json:"ops"`
	Changed     []string  `json:"changed"`
	FocusSlide  string    `json:"focusSlide,omitempty"`
}

func (s *Service) mutate(ctx context.Context, id string, base *int, actor string, build func(*deck.Deck) ([]deck.Op, error)) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, raw, ver, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if base != nil && *base != ver {
		return nil, &ConflictError{Base: *base, Current: ver}
	}
	ops, err := build(cur)
	if err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return nil, errors.New("nothing to change: no operations given")
	}
	for i := range ops {
		if ops[i].Value != nil {
			v, err := normalize(ops[i].Value)
			if err != nil {
				return nil, fmt.Errorf("op %d value: %w", i, err)
			}
			ops[i].Value = deck.AssignIDs(v)
		}
	}
	opsJSON, err := json.Marshal(ops)
	if err != nil {
		return nil, err
	}
	patch, err := jsonpatch.DecodePatch(opsJSON)
	if err != nil {
		return nil, fmt.Errorf("invalid JSON Patch: %w", err)
	}
	newRaw, err := patch.Apply(raw)
	if err != nil {
		return nil, fmt.Errorf("patch failed: %v (paths are JSON Pointers into the deck, e.g. /slides/0/regions/title/0/props/text; read the deck with get_deck)", err)
	}
	d, err := s.v.Validate(newRaw)
	if err != nil {
		return nil, err
	}
	if d.ID != id {
		return nil, errors.New("the deck id cannot be changed")
	}
	if err := s.store.Update(ctx, id, ver, d.Title, d.Theme, len(d.Slides), newRaw, opsJSON, actor); err != nil {
		if errors.Is(err, store.ErrVersionMismatch) {
			return nil, &ConflictError{Base: ver, Current: ver + 1}
		}
		return nil, err
	}
	changed, focus := changedIDs(cur, d, ops)
	ev := Event{Type: "patch", DeckID: id, Version: ver + 1, BaseVersion: ver, Actor: actor, Ops: ops, Changed: changed, FocusSlide: focus}
	if msg, err := json.Marshal(ev); err == nil {
		s.Hub.Publish(id, msg)
	}
	return &Result{DeckID: id, Version: ver + 1, Changed: changed, Lint: lint.Deck(d, s.Cat, s.Themes), URL: s.ShareURL(id),
		OpenComments: s.store.CountOpenComments(ctx, id)}, nil
}

// Restore makes the deck look exactly as it did at an earlier version, as a
// new version (history is never rewritten). Undo and redo are built on this.
func (s *Service) Restore(ctx context.Context, id string, base *int, version int, actor string) (*Result, error) {
	old, err := s.store.VersionDoc(ctx, id, version)
	if err != nil {
		return nil, fmt.Errorf("version %d of deck %s: %w", version, id, err)
	}
	old, _, err = deck.Migrate(old)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(old, &doc); err != nil {
		return nil, err
	}
	return s.mutate(ctx, id, base, actor, func(*deck.Deck) ([]deck.Op, error) {
		var ops []deck.Op
		for _, k := range []string{"title", "theme", "aspect", "slides"} {
			ops = append(ops, deck.Op{Op: "replace", Path: "/" + k, Value: doc[k]})
		}
		return ops, nil
	})
}

// Publish sends an arbitrary event to a deck's open browsers.
func (s *Service) Publish(deckID string, ev any) {
	if msg, err := json.Marshal(ev); err == nil {
		s.Hub.Publish(deckID, msg)
	}
}

// normalize turns any Go value into plain JSON types (map[string]any, []any...).
func normalize(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	err = json.Unmarshal(b, &out)
	return out, err
}

// changedIDs maps patch paths to the slide and element ids they touch, and
// picks the slide the viewer should follow.
func changedIDs(before, after *deck.Deck, ops []deck.Op) ([]string, string) {
	seen := map[string]bool{}
	var out []string
	focus := ""
	addID := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	look := func(path string, d *deck.Deck) (slideID, elID string) {
		seg := strings.Split(strings.TrimPrefix(path, "/"), "/")
		if len(seg) < 2 || seg[0] != "slides" {
			return "", ""
		}
		n, err := strconv.Atoi(seg[1])
		if seg[1] == "-" {
			n, err = len(d.Slides)-1, nil
		}
		if err != nil || n < 0 || n >= len(d.Slides) {
			return "", ""
		}
		sl := d.Slides[n]
		slideID = sl.ID
		if len(seg) >= 5 && seg[2] == "regions" {
			if k, err := strconv.Atoi(seg[4]); err == nil && k >= 0 && k < len(sl.Regions[seg[3]]) {
				elID = sl.Regions[seg[3]][k].ID
			}
		}
		return
	}
	for _, op := range ops {
		for _, p := range []string{op.Path, op.From} {
			if p == "" {
				continue
			}
			if p == "/theme" || p == "/title" || p == "/aspect" {
				addID("deck")
				continue
			}
			d := after
			if op.Op == "remove" || p == op.From {
				d = before
			}
			sid, eid := look(p, d)
			addID(sid)
			addID(eid)
			if focus == "" && op.Op != "remove" && after.SlideIndex(sid) >= 0 {
				focus = sid
			}
		}
	}
	return out, focus
}
