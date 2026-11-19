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

var ctx = context.Background()

func ptr(i int) *int { return &i }

func slide(layout, region, text string) map[string]any {
	return map[string]any{
		"layout": layout,
		"regions": map[string]any{
			region: []any{map[string]any{"type": "heading", "props": map[string]any{"text": text}}},
		},
	}
}

func TestGoldenExampleValidatesAndLintsClean(t *testing.T) {
	env := testenv.New(t)
	d, err := env.Validator.Validate(testenv.Example(t, "launch.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Slides) != 7 {
		t.Fatalf("want 7 slides, got %d", len(d.Slides))
	}
}

func TestCreatePatchVersioning(t *testing.T) {
	env := testenv.New(t)
	r, err := env.Svc.Create(ctx, "Demo", "", "", []any{slide("title", "title", "Hello")}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != 1 || !strings.HasPrefix(r.DeckID, "d_") {
		t.Fatalf("bad create result %+v", r)
	}
	d, _, _, err := env.Svc.Get(ctx, r.DeckID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Theme != "aurora" || d.Aspect != "16:9" {
		t.Fatalf("defaults not applied: %+v", d)
	}
	el := d.Slides[0].Regions["title"][0]
	if el.ID == "" || d.Slides[0].ID == "" {
		t.Fatal("ids were not assigned")
	}

	ch, cancel := env.Svc.Hub.Subscribe(r.DeckID)
	defer cancel()

	r2, err := env.Svc.Patch(ctx, r.DeckID, ptr(1), []deck.Op{
		{Op: "replace", Path: "/slides/0/regions/title/0/props/text", Value: "Hello, world"},
	}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Version != 2 {
		t.Fatalf("want version 2, got %d", r2.Version)
	}
	select {
	case msg := <-ch:
		var ev service.Event
		json.Unmarshal(msg, &ev)
		if ev.Version != 2 || ev.FocusSlide != d.Slides[0].ID || len(ev.Changed) != 2 {
			t.Fatalf("bad event %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no broadcast")
	}

	// Stale base version is a conflict.
	_, err = env.Svc.Patch(ctx, r.DeckID, ptr(1), []deck.Op{{Op: "replace", Path: "/title", Value: "X"}}, "human")
	var ce *service.ConflictError
	if !errors.As(err, &ce) || ce.Current != 2 {
		t.Fatalf("want conflict at version 2, got %v", err)
	}
}

func TestInvalidWritesAreRejectedAtomically(t *testing.T) {
	env := testenv.New(t)
	r, _ := env.Svc.Create(ctx, "Demo", "paper", "", []any{slide("title", "title", "Hi")}, "agent")
	cases := map[string][]deck.Op{
		"unknown region": {{Op: "add", Path: "/slides/0/regions/body", Value: []any{}}},
		"unknown theme":  {{Op: "replace", Path: "/theme", Value: "nope"}},
		"bad element":    {{Op: "add", Path: "/slides/0/regions/title/-", Value: map[string]any{"type": "heading", "props": map[string]any{}}}},
		"raw html prop":  {{Op: "add", Path: "/slides/0/regions/title/0/props/html", Value: "<b>x</b>"}},
		"id change":      {{Op: "replace", Path: "/id", Value: "d_other"}},
		"bad pointer":    {{Op: "replace", Path: "/slides/9/layout", Value: "title"}},
		"second op bad":  {{Op: "replace", Path: "/title", Value: "Fine"}, {Op: "replace", Path: "/aspect", Value: "21:9"}},
	}
	for name, ops := range cases {
		if _, err := env.Svc.Patch(ctx, r.DeckID, nil, ops, "agent"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	d, _, ver, _ := env.Svc.Get(ctx, r.DeckID)
	if ver != 1 || d.Title != "Demo" {
		t.Fatalf("failed writes changed the deck: v%d %q", ver, d.Title)
	}
}

func TestSlideHelpers(t *testing.T) {
	env := testenv.New(t)
	r, _ := env.Svc.Create(ctx, "Demo", "", "", []any{slide("title", "title", "One")}, "agent")
	if _, err := env.Svc.AddSlide(ctx, r.DeckID, nil, slide("title-body", "title", "Two"), nil, "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.AddSlide(ctx, r.DeckID, nil, slide("quote", "quote", "Zero"), ptr(0), "agent"); err != nil {
		t.Fatal(err)
	}
	d, _, _, _ := env.Svc.Get(ctx, r.DeckID)
	if len(d.Slides) != 3 || d.Slides[0].Layout != "quote" {
		t.Fatalf("insert at 0 failed: %+v", d.Slides)
	}
	last := d.Slides[2].ID
	if _, err := env.Svc.MoveSlide(ctx, r.DeckID, nil, last, 0, "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.UpdateSlide(ctx, r.DeckID, nil, last, map[string]any{"notes": "hi", "transition": map[string]any{"type": "morph"}}, "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.UpdateSlide(ctx, r.DeckID, nil, last, map[string]any{"id": "x"}, "agent"); err == nil {
		t.Fatal("updating id must be refused")
	}
	if _, err := env.Svc.DeleteSlide(ctx, r.DeckID, nil, d.Slides[1].ID, "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.SetTheme(ctx, r.DeckID, nil, "paper", "agent"); err != nil {
		t.Fatal(err)
	}
	d, _, ver, _ := env.Svc.Get(ctx, r.DeckID)
	if ver != 7 || len(d.Slides) != 2 || d.Slides[0].ID != last || d.Slides[0].Notes != "hi" || d.Theme != "paper" {
		t.Fatalf("unexpected deck v%d: %+v", ver, d)
	}
	h, _ := env.Svc.History(ctx, r.DeckID, 10)
	if len(h) != 7 || h[0].Version != 7 {
		t.Fatalf("history: %+v", h)
	}
}
