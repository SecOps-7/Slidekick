package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/service"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

func liveSlide() map[string]any {
	return map[string]any{"layout": "live", "regions": map[string]any{
		"title":  []any{map[string]any{"type": "heading", "props": map[string]any{"text": "Network"}}},
		"figure": []any{map[string]any{"id": "e_rate", "type": "stat", "props": map[string]any{"value": 10, "suffix": " Mbps", "label": "Download"}}},
		"chart":  []any{map[string]any{"id": "e_chart", "type": "chart", "props": map[string]any{"kind": "line", "labels": []any{"a", "b"}, "series": []any{map[string]any{"values": []any{1, 2}}}}}},
		"status": []any{map[string]any{"id": "e_status", "type": "text", "props": map[string]any{"text": "All good"}}},
	}}
}

// liveProps reads an element's props from the stored live content.
func liveProps(t *testing.T, env *testenv.Env, deckID, slideID, elementID string) map[string]any {
	t.Helper()
	d, _, _, _ := env.Svc.Get(ctx, deckID)
	v, ok := env.Svc.LiveValues(ctx, d)[slideID]
	if !ok {
		return nil
	}
	var rs map[string][]map[string]any
	json.Unmarshal(v.Regions, &rs)
	for _, els := range rs {
		for _, e := range els {
			if e["id"] == elementID {
				p, _ := e["props"].(map[string]any)
				return p
			}
		}
	}
	return nil
}

func TestLiveSlides(t *testing.T) {
	env := testenv.New(t)
	r, err := env.Svc.Create(ctx, "Live", "", "", []any{slide("title", "title", "Intro"), liveSlide()}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	d, _, ver, _ := env.Svc.Get(ctx, r.DeckID)
	plain, live := d.Slides[0].ID, d.Slides[1].ID
	ch, cancel := env.Svc.Hub.Subscribe(r.DeckID)
	defer cancel()

	// Only live slides take updates; values are checked like any edit.
	var input *service.LiveInputError
	if _, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: plain, ElementID: d.Slides[0].Regions["title"][0].ID, Value: "x"}); !errors.As(err, &input) {
		t.Fatalf("plain slide: %v", err)
	}
	if _, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, ElementID: "e_rate", Value: "fast"}); err == nil {
		t.Fatal("a string accepted as a stat value")
	}
	if _, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, ElementID: "e_chart", Value: 5}); !errors.As(err, &input) {
		t.Fatalf("number as chart value: %v", err)
	}
	if _, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, ElementID: "nope", Value: 1}); !errors.As(err, &input) {
		t.Fatalf("unknown element: %v", err)
	}

	// A value changes one element, keeps its other props, adds no version and is streamed.
	res, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, ElementID: "e_rate", Value: 42.5, Animate: true})
	if err != nil {
		t.Fatal(err)
	}
	if p := liveProps(t, env, r.DeckID, live, "e_rate"); p["value"] != 42.5 || p["suffix"] != " Mbps" {
		t.Fatalf("stored props %v", p)
	}
	if _, _, v2, _ := env.Svc.Get(ctx, r.DeckID); v2 != ver {
		t.Fatalf("a live update added a version: %d -> %d", ver, v2)
	}
	var ev map[string]any
	select {
	case msg := <-ch:
		json.Unmarshal(msg, &ev)
	case <-time.After(2 * time.Second):
		t.Fatal("no live event")
	}
	if ev["type"] != "live" || ev["op"] != "set" || ev["elementId"] != "e_rate" || ev["animate"] != true || ev["props"].(map[string]any)["value"] != 42.5 {
		t.Fatalf("event %v", ev)
	}
	if res.At.IsZero() {
		t.Fatal("no time")
	}
	// Text, chart and extra props.
	if _, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, ElementID: "e_status", Value: "Link flapping"}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, ElementID: "e_chart",
		Value: map[string]any{"labels": []any{"a", "b", "c"}, "series": []any{map[string]any{"values": []any{1, 2, 3}}}}, Props: map[string]any{"unit": "Mbps"}}); err != nil {
		t.Fatal(err)
	}
	if p := liveProps(t, env, r.DeckID, live, "e_chart"); len(p["labels"].([]any)) != 3 || p["unit"] != "Mbps" || p["kind"] != "line" {
		t.Fatalf("chart props %v", p)
	}

	// Add and remove elements.
	add, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, Op: "add", Region: "status",
		Element: map[string]any{"type": "text", "props": map[string]any{"text": "Upload 12 Mbps"}, "build": map[string]any{"step": 1}}})
	if err != nil || !strings.HasPrefix(add.ElementID, "e_") || add.Element["build"] != nil {
		t.Fatalf("add: %+v %v", add, err)
	}
	if liveProps(t, env, r.DeckID, live, add.ElementID)["text"] != "Upload 12 Mbps" {
		t.Fatal("added element not stored")
	}
	if _, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, Op: "add", Region: "status", Element: map[string]any{"type": "image", "props": map[string]any{"src": "/x.png"}}}); !errors.As(err, &input) {
		t.Fatalf("adding an image: %v", err)
	}
	if _, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, Op: "add", Region: "nowhere", Element: map[string]any{"type": "text", "props": map[string]any{"text": "x"}}}); err == nil {
		t.Fatal("added to a region the layout doesn't have")
	}
	if _, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, Op: "remove", ElementID: "e_status"}); err != nil {
		t.Fatal(err)
	}
	if liveProps(t, env, r.DeckID, live, "e_status") != nil {
		t.Fatal("removed element still there")
	}

	// A deck edit to the slide replaces its live content, so the edit shows.
	d, _, ver, _ = env.Svc.Get(ctx, r.DeckID)
	if _, err := env.Svc.Patch(ctx, r.DeckID, &ver, []deck.Op{{Op: "replace", Path: "/slides/1/regions/title/0/props/text", Value: "Edited"}}, "human"); err != nil {
		t.Fatal(err)
	}
	if liveProps(t, env, r.DeckID, live, "e_rate") != nil {
		t.Fatal("live content kept after the slide was edited")
	}
	// An edit to another slide keeps it.
	env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, ElementID: "e_rate", Value: 7})
	_, _, ver, _ = env.Svc.Get(ctx, r.DeckID)
	env.Svc.Patch(ctx, r.DeckID, &ver, []deck.Op{{Op: "replace", Path: "/slides/0/regions/title/0/props/text", Value: "Intro 2"}}, "human")
	if liveProps(t, env, r.DeckID, live, "e_rate")["value"] != 7.0 {
		t.Fatal("live content lost after editing another slide")
	}
	// The HTML export carries the live content.
	d, raw, _, _ := env.Svc.Get(ctx, r.DeckID)
	if !strings.Contains(string(env.Svc.WithLive(ctx, d, raw)), `"value":7`) {
		t.Fatal("export without live values")
	}
}

func TestLiveSwitchAndRate(t *testing.T) {
	env := testenv.New(t)
	r, _ := env.Svc.Create(ctx, "Live", "", "", []any{liveSlide()}, "agent")
	d, _, _, _ := env.Svc.Get(ctx, r.DeckID)
	live := d.Slides[0].ID

	on := false
	env.Svc.LiveAllowed = func(context.Context) bool { return on }
	if _, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, ElementID: "e_rate", Value: 1}); !errors.Is(err, service.ErrLiveOff) {
		t.Fatalf("switched off: %v", err)
	}
	on = true
	ok, limited := 0, 0
	for i := range 30 {
		_, err := env.Svc.UpdateLive(ctx, r.DeckID, service.LiveUpdate{SlideID: live, ElementID: "e_rate", Value: i})
		switch {
		case err == nil:
			ok++
		case errors.Is(err, service.ErrLiveRate):
			limited++
		default:
			t.Fatal(err)
		}
	}
	if ok < service.LiveBurst || ok > service.LiveBurst+2 || limited == 0 {
		t.Fatalf("rate limit: %d allowed, %d limited", ok, limited)
	}
}
