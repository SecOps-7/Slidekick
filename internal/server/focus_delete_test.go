package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpAs(t *testing.T, e *accEnv, tok string) *mcp.ClientSession {
	tr := &mcp.StreamableClientTransport{Endpoint: e.ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerRT{tok}}}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func personalToken(t *testing.T, c *client) string {
	_, out := c.do("POST", "/api/me/tokens", map[string]string{"name": "agent"})
	return out["token"].(string)
}

// nextEvent waits for a live event of the given type on the deck.
func nextEvent(t *testing.T, ch <-chan []byte, typ string) map[string]any {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case b := <-ch:
			var m map[string]any
			json.Unmarshal(b, &m)
			if m["type"] == typ {
				return m
			}
		case <-deadline:
			t.Fatalf("no %q event", typ)
			return nil
		}
	}
}

var threeSlides = []any{
	map[string]any{"layout": "title", "regions": map[string]any{"title": []any{map[string]any{"type": "heading", "props": map[string]any{"text": "One"}}}}},
	map[string]any{"layout": "title", "regions": map[string]any{"title": []any{map[string]any{"type": "heading", "props": map[string]any{"text": "Two"}}}}},
	map[string]any{"layout": "title", "regions": map[string]any{"title": []any{map[string]any{"type": "heading", "props": map[string]any{"text": "Three"}}}}},
}

func TestFocusSlide(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	ed := e.addUser(t, admin, "edna", "editor")
	view := e.addUser(t, admin, "vera", "viewer")
	_, out := ed.do("POST", "/api/decks", map[string]any{"title": "Talk", "slides": threeSlides})
	id := out["deckId"].(string)
	ch, cancel := e.env.Svc.Hub.Subscribe(id)
	defer cancel()
	ctx := context.Background()

	cs := mcpAs(t, e, personalToken(t, ed))
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "focus_slide", Arguments: map[string]any{"deck_id": id, "slide": 3}})
	if err != nil || res.IsError {
		t.Fatalf("focus_slide: %v %+v", err, res)
	}
	ev := nextEvent(t, ch, "focus")
	d, _, _, _ := e.env.Svc.Get(ctx, id)
	if ev["slideId"] != d.Slides[2].ID || ev["index"] != float64(2) || ev["actor"] != "agent:edna" {
		t.Fatalf("focus event %v", ev)
	}
	if res, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "focus_slide", Arguments: map[string]any{"deck_id": id, "slide": 9}}); !res.IsError {
		t.Fatal("focused a slide that doesn't exist")
	}
	// Viewers can't move everyone's view.
	vcs := mcpAs(t, e, personalToken(t, view))
	if res, _ := vcs.CallTool(ctx, &mcp.CallToolParams{Name: "focus_slide", Arguments: map[string]any{"deck_id": id, "slide": 1}}); !res.IsError {
		t.Fatal("viewer focused a slide")
	}
	// Edits move the view too: patching slide 2 names it as the focus.
	cs.CallTool(ctx, &mcp.CallToolParams{Name: "patch_deck", Arguments: map[string]any{"deck_id": id, "ops": []any{map[string]any{"op": "replace", "path": "/slides/1/regions/title/0/props/text", "value": "Two!"}}}})
	if p := nextEvent(t, ch, "patch"); p["focusSlide"] != d.Slides[1].ID {
		t.Fatalf("patch focus %v", p["focusSlide"])
	}
}

func TestDeckDeletion(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	ed := e.addUser(t, admin, "edna", "editor")
	ed2 := e.addUser(t, admin, "eddie", "editor")
	view := e.addUser(t, admin, "vera", "viewer")
	ctx := context.Background()
	_, out := ed.do("POST", "/api/decks", map[string]any{"title": "Old talk", "slides": oneSlide})
	id := out["deckId"].(string)
	_, out = ed2.do("POST", "/api/decks", map[string]any{"title": "Eddie's", "slides": oneSlide})
	other := out["deckId"].(string)
	admin.do("PUT", "/api/admin/settings", map[string]bool{"editorsEditAll": true})

	// The list says who may delete what.
	_, list := ed.do("GET", "/api/decks", nil)
	flags := map[string]bool{}
	for _, x := range list["decks"].([]any) {
		m := x.(map[string]any)
		flags[m["id"].(string)] = m["canDelete"].(bool)
	}
	if !flags[id] || flags[other] {
		t.Fatalf("canDelete flags %v", flags)
	}
	// Editing everything doesn't mean deleting others' decks; viewers and deck tokens can't either.
	if code, _ := ed2.do("DELETE", "/api/decks/"+id, nil); code != 403 {
		t.Fatalf("other editor deleted: %d", code)
	}
	if code, _ := view.do("DELETE", "/api/decks/"+id, nil); code != 403 {
		t.Fatalf("viewer deleted: %d", code)
	}
	tok, _ := e.env.Svc.IssueEditToken(ctx, id)
	dt := e.client(t)
	dt.bear = tok
	if code, _ := dt.do("DELETE", "/api/decks/"+id, nil); code != 403 {
		t.Fatalf("deck token deleted: %d", code)
	}
	// The owner deletes it; watchers are told; it's gone with its history.
	ch, cancel := e.env.Svc.Hub.Subscribe(id)
	defer cancel()
	if code, out := ed.do("DELETE", "/api/decks/"+id, nil); code != 200 {
		t.Fatalf("owner delete: %d %v", code, out)
	}
	nextEvent(t, ch, "deleted")
	if code, _ := admin.do("GET", "/api/decks/"+id, nil); code != 404 {
		t.Fatalf("deleted deck still opens: %d", code)
	}
	if _, err := e.env.Store.VersionDoc(ctx, id, 1); err == nil {
		t.Fatal("history survived")
	}
	if code, _ := ed.do("DELETE", "/api/decks/"+id, nil); code != 404 {
		t.Fatalf("second delete: %d", code)
	}
	// Admins can delete anyone's deck.
	if code, _ := admin.do("DELETE", "/api/decks/"+other, nil); code != 200 {
		t.Fatalf("admin delete: %d", code)
	}
	if _, l := admin.do("GET", "/api/decks", nil); strings.Contains(toJSON(l), other) {
		t.Fatal("deleted deck still listed")
	}
}
