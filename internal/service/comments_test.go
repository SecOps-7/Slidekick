package service_test

import (
	"testing"

	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

func TestCommentsAndRestore(t *testing.T) {
	env := testenv.New(t)
	r, err := env.Svc.Create(ctx, "Demo", "", "", []any{slide("title", "title", "Original")}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	d, _, _, _ := env.Svc.Get(ctx, r.DeckID)
	sid, eid := d.Slides[0].ID, d.Slides[0].Regions["title"][0].ID

	c, err := env.Svc.AddComment(ctx, r.DeckID, sid, eid, "Make this punchier", "human")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.AddComment(ctx, r.DeckID, sid, "nope", "x", "human"); err == nil {
		t.Fatal("unknown element should be rejected")
	}
	if _, err := env.Svc.AddComment(ctx, r.DeckID, "", "", "  ", "human"); err == nil {
		t.Fatal("empty comment should be rejected")
	}

	// Writes report open comments so the agent notices them.
	r2, err := env.Svc.Patch(ctx, r.DeckID, ptr(1), []deck.Op{{Op: "replace", Path: "/slides/0/regions/title/0/props/text", Value: "Changed"}}, "agent")
	if err != nil || r2.OpenComments != 1 {
		t.Fatalf("want 1 open comment in result, got %+v %v", r2, err)
	}
	if err := env.Svc.ResolveComment(ctx, r.DeckID, c.ID, "Done: shorter headline"); err != nil {
		t.Fatal(err)
	}
	open, _ := env.Svc.Comments(ctx, r.DeckID, false)
	all, _ := env.Svc.Comments(ctx, r.DeckID, true)
	if len(open) != 0 || len(all) != 1 || all[0].Reply != "Done: shorter headline" || all[0].Status != "resolved" {
		t.Fatalf("open=%v all=%v", open, all)
	}

	// Restore version 1 as version 3 (undo), then version 2 as version 4 (redo).
	r3, err := env.Svc.Restore(ctx, r.DeckID, ptr(2), 1, "human")
	if err != nil {
		t.Fatal(err)
	}
	d, _, _, _ = env.Svc.Get(ctx, r.DeckID)
	if r3.Version != 3 || d.Slides[0].Regions["title"][0].Str("text") != "Original" {
		t.Fatalf("undo failed: v%d %q", r3.Version, d.Slides[0].Regions["title"][0].Str("text"))
	}
	if _, err := env.Svc.Restore(ctx, r.DeckID, ptr(3), 2, "human"); err != nil {
		t.Fatal(err)
	}
	d, _, ver, _ := env.Svc.Get(ctx, r.DeckID)
	if ver != 4 || d.Slides[0].Regions["title"][0].Str("text") != "Changed" {
		t.Fatalf("redo failed: v%d", ver)
	}
	if _, err := env.Svc.Restore(ctx, r.DeckID, nil, 99, "human"); err == nil {
		t.Fatal("restoring a missing version should fail")
	}
}
