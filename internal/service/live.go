package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	jsonpatch "github.com/evanphx/json-patch/v5"

	"github.com/SecOps-7/slidekick/internal/deck"
)

// Live slides: slides with the "live" layout show content an agent keeps up
// to date while the deck is shown (update_live): it sets new values on
// elements, adds elements and removes them. Each change is validated like a
// deck edit, stored as the slide's current live content (one row per slide,
// overwritten) and streamed to every open screen as a "live" event, which
// changes only that element in place. It never adds a deck version, so
// history stays readable however often values change. A deck edit to the
// slide itself replaces its live content, so the edit shows.

// LiveLayout is the only layout that takes live updates.
const LiveLayout = "live"

// Live update limits, per slide: a steady rate and a short burst.
const (
	LiveRate  = 5.0 // updates per second
	LiveBurst = 10
)

// Live operations.
const (
	LiveSet    = "set"
	LiveAdd    = "add"
	LiveRemove = "remove"
)

// valueTypes take new values; addTypes can be added to a live slide.
var (
	valueTypes = map[string]bool{"stat": true, "text": true, "chart": true}
	addTypes   = map[string]bool{"stat": true, "text": true, "chart": true, "heading": true}
)

var (
	// ErrLiveOff means an admin hasn't allowed live slides.
	ErrLiveOff = errors.New("live slides are switched off on this Slidekick: an admin can allow them under Settings > General")
	// ErrLiveRate means a slide got more updates than LiveRate allows.
	ErrLiveRate = fmt.Errorf("too many live updates for this slide: at most %g a second with short bursts of %d; update less often", LiveRate, LiveBurst)
)

// LiveInputError is a live update that can't apply (wrong slide, element or value).
type LiveInputError struct{ Msg string }

func (e *LiveInputError) Error() string { return e.Msg }

func liveErr(format string, a ...any) error { return &LiveInputError{fmt.Sprintf(format, a...)} }

// LiveUpdate is one change to a live slide.
type LiveUpdate struct {
	SlideID string `json:"slide_id" jsonschema:"id of a slide with the live layout"`
	Op      string `json:"op,omitempty" jsonschema:"set (default): new value on an element; add: put a new element on the slide; remove: take an element off"`
	// set and remove
	ElementID string `json:"element_id,omitempty" jsonschema:"set and remove: the element's id"`
	// set
	Value any            `json:"value,omitempty" jsonschema:"set: a number for a stat (props.value), a string for text (props.text), or {labels?, series} for a chart"`
	Props map[string]any `json:"props,omitempty" jsonschema:"set: other props to change at the same time, e.g. a stat's label or suffix; null removes one"`
	// add
	Region   string         `json:"region,omitempty" jsonschema:"add: the region to put it in: title, figure, chart or status"`
	Position *int           `json:"position,omitempty" jsonschema:"add: zero-based position in the region; appends when omitted"`
	Element  map[string]any `json:"element,omitempty" jsonschema:"add: the element, {type, props, style?}: a stat, chart, text or heading; id optional"`
	// Animate plays a value change: a stat counts from its old value to the
	// new one, a chart draws in again, text fades in. Adds and removes always
	// ease in and out.
	Animate bool `json:"animate,omitempty" jsonschema:"set: true animates the change (a stat counts from the old to the new value, a chart draws in, text fades in); default: the new value appears without motion. Adds and removes always ease in and out."`
}

// LiveResult is what an update did.
type LiveResult struct {
	DeckID    string         `json:"deckId"`
	SlideID   string         `json:"slideId"`
	Op        string         `json:"op"`
	ElementID string         `json:"elementId"`
	Element   map[string]any `json:"element,omitempty"` // set and add: the element as it is now
	At        time.Time      `json:"at"`
}

// LiveEntry is a live slide's current content, for screens that open the deck.
type LiveEntry struct {
	Regions json.RawMessage `json:"regions"`
	At      time.Time       `json:"at"`
}

type bucket struct {
	tokens float64
	last   time.Time
}

type liveLimiter struct {
	mu sync.Mutex
	b  map[string]*bucket
}

func (l *liveLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.b == nil {
		l.b = map[string]*bucket{}
	}
	b := l.b[key]
	if b == nil {
		b = &bucket{tokens: LiveBurst, last: now}
		l.b[key] = b
	}
	b.tokens = min(LiveBurst, b.tokens+now.Sub(b.last).Seconds()*LiveRate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type regions = map[string][]map[string]any

func findIn(rs regions, id string) (string, int) {
	for r, els := range rs {
		for i, e := range els {
			if e["id"] == id {
				return r, i
			}
		}
	}
	return "", -1
}

// UpdateLive applies one change to a live slide.
func (s *Service) UpdateLive(ctx context.Context, deckID string, u LiveUpdate) (*LiveResult, error) {
	if s.LiveAllowed != nil && !s.LiveAllowed(ctx) {
		return nil, ErrLiveOff
	}
	d, raw, _, err := s.Get(ctx, deckID)
	if err != nil {
		return nil, err
	}
	si := d.SlideIndex(u.SlideID)
	if si < 0 {
		return nil, liveErr("no slide %q in deck %s", u.SlideID, d.ID)
	}
	if l := d.Slides[si].Layout; l != LiveLayout {
		return nil, liveErr("slide %q has the %q layout; only slides with the live layout take live updates", u.SlideID, l)
	}

	// Start from the slide's live content, else its saved regions.
	rs := regions{}
	base, _ := json.Marshal(d.Slides[si].Regions)
	if cur, ok := s.liveSlides(ctx, deckID)[u.SlideID]; ok {
		base = cur.Regions
	}
	if err := json.Unmarshal(base, &rs); err != nil {
		return nil, err
	}

	res := &LiveResult{DeckID: deckID, SlideID: u.SlideID, Op: u.Op}
	ev := map[string]any{"type": "live", "slideId": u.SlideID, "animate": u.Animate}
	switch u.Op {
	case "", LiveSet:
		res.Op = LiveSet
		r, i := findIn(rs, u.ElementID)
		if i < 0 {
			return nil, liveErr("no element %q on slide %q", u.ElementID, u.SlideID)
		}
		e := rs[r][i]
		typ, _ := e["type"].(string)
		if !valueTypes[typ] {
			return nil, liveErr("element %q is a %s; live values change stat, chart and text elements", u.ElementID, typ)
		}
		if u.Value == nil && len(u.Props) == 0 {
			return nil, liveErr("give a value (or props) to set")
		}
		props, _ := e["props"].(map[string]any)
		if props == nil {
			props = map[string]any{}
		}
		if u.Value != nil {
			switch typ {
			case "stat":
				props["value"] = u.Value
			case "text":
				props["text"] = u.Value
			case "chart":
				m, ok := u.Value.(map[string]any)
				if !ok {
					return nil, liveErr(`a chart's value is an object: {"labels": [...], "series": [{"name": "...", "values": [...]}]}`)
				}
				for k, v := range m {
					props[k] = v
				}
			}
		}
		for k, v := range u.Props {
			if v == nil {
				delete(props, k)
			} else {
				props[k] = v
			}
		}
		e["props"] = props
		res.ElementID, res.Element = u.ElementID, e
		ev["op"], ev["elementId"], ev["props"] = LiveSet, u.ElementID, props
	case LiveAdd:
		if u.Element == nil || u.Region == "" {
			return nil, liveErr("add needs a region and an element")
		}
		typ, _ := u.Element["type"].(string)
		if !addTypes[typ] {
			return nil, liveErr("a live slide can take stat, chart, text and heading elements, not %q", typ)
		}
		e := deck.AssignIDs(u.Element).(map[string]any)
		delete(e, "build") // live elements appear as they're added, not on a build step
		if _, i := findIn(rs, e["id"].(string)); i >= 0 {
			return nil, liveErr("element id %q is already on this slide", e["id"])
		}
		pos := len(rs[u.Region])
		if u.Position != nil && *u.Position >= 0 && *u.Position < pos {
			pos = *u.Position
		}
		list := append([]map[string]any{}, rs[u.Region][:pos]...)
		list = append(list, e)
		rs[u.Region] = append(list, rs[u.Region][pos:]...)
		res.ElementID, res.Element = e["id"].(string), e
		ev["op"], ev["region"], ev["index"], ev["element"] = LiveAdd, u.Region, pos, e
	case LiveRemove:
		r, i := findIn(rs, u.ElementID)
		if i < 0 {
			return nil, liveErr("no element %q on slide %q", u.ElementID, u.SlideID)
		}
		rs[r] = append(rs[r][:i:i], rs[r][i+1:]...)
		res.ElementID = u.ElementID
		ev["op"], ev["elementId"] = LiveRemove, u.ElementID
	default:
		return nil, liveErr("op is set, add or remove, not %q", u.Op)
	}

	// Validate exactly as a deck edit would: the whole deck with this content.
	ops, _ := json.Marshal([]deck.Op{{Op: "replace", Path: "/slides/" + strconv.Itoa(si) + "/regions", Value: rs}})
	patch, err := jsonpatch.DecodePatch(ops)
	if err != nil {
		return nil, err
	}
	newRaw, err := patch.Apply(raw)
	if err != nil {
		return nil, err
	}
	if _, err := s.v.Validate(newRaw); err != nil {
		return nil, err
	}
	now := time.Now()
	if !s.live.allow(deckID+"/"+u.SlideID, now) {
		return nil, ErrLiveRate
	}
	rj, _ := json.Marshal(rs)
	if err := s.store.SetLiveSlide(ctx, deckID, u.SlideID, string(rj), now); err != nil {
		return nil, err
	}
	res.At = now.UTC()
	ev["at"] = res.At
	s.Publish(deckID, ev)
	return res, nil
}

func (s *Service) liveSlides(ctx context.Context, deckID string) map[string]LiveEntry {
	out := map[string]LiveEntry{}
	rows, err := s.store.LiveSlides(ctx, deckID)
	if err != nil {
		return out
	}
	for _, v := range rows {
		out[v.SlideID] = LiveEntry{Regions: json.RawMessage(v.Regions), At: v.UpdatedAt.UTC()}
	}
	return out
}

// LiveValues are a deck's live slides' current content, keyed by slide id:
// only slides that still exist with the live layout.
func (s *Service) LiveValues(ctx context.Context, d *deck.Deck) map[string]LiveEntry {
	out := map[string]LiveEntry{}
	for id, v := range s.liveSlides(ctx, d.ID) {
		if i := d.SlideIndex(id); i >= 0 && d.Slides[i].Layout == LiveLayout {
			out[id] = v
		}
	}
	return out
}

// WithLive returns the deck document with its live content in place, for
// the standalone HTML export (which has no live connection).
func (s *Service) WithLive(ctx context.Context, d *deck.Deck, raw []byte) []byte {
	var ops []deck.Op
	for id, v := range s.LiveValues(ctx, d) {
		var rs any
		json.Unmarshal(v.Regions, &rs)
		ops = append(ops, deck.Op{Op: "replace", Path: "/slides/" + strconv.Itoa(d.SlideIndex(id)) + "/regions", Value: rs})
	}
	if len(ops) == 0 {
		return raw
	}
	b, _ := json.Marshal(ops)
	p, err := jsonpatch.DecodePatch(b)
	if err != nil {
		return raw
	}
	if out, err := p.Apply(raw); err == nil {
		return out
	}
	return raw
}
