package mcpserver_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick"
	"github.com/SecOps-7/slidekick/internal/export"
	"github.com/SecOps-7/slidekick/internal/mcpserver"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

// The slide, theme, history, comment, sharing, live and export tools, each
// called the way an agent calls it, through MCP.
func TestEditingTools(t *testing.T) {
	env := testenv.New(t)
	web := fstest.MapFS{
		"standalone/slidekick.js":      {Data: []byte("/*full*/")},
		"standalone/slidekick-lite.js": {Data: []byte("/*lite*/")},
		"standalone/slidekick.css":     {Data: []byte(".sk-stage{}")},
	}
	exp := &export.Exporter{Web: web, Cat: env.Cat, Themes: env.Themes,
		Assets: func(context.Context, string) (string, []byte, error) {
			return "", nil, errors.New("no assets in this test")
		}}
	srv := mcpserver.New(env.Svc, slidekick.Schema, "test", mcpserver.Options{Exporter: exp})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	heading := func(text string) map[string]any {
		return map[string]any{"layout": "title", "regions": map[string]any{"title": []any{map[string]any{"type": "heading", "props": map[string]any{"text": text}}}}}
	}
	live := map[string]any{"layout": "live", "regions": map[string]any{
		"title":  []any{map[string]any{"type": "heading", "props": map[string]any{"text": "Network"}}},
		"figure": []any{map[string]any{"id": "e_rate", "type": "stat", "props": map[string]any{"value": 10, "label": "Download"}}},
	}}
	out, ok := call(t, cs, "create_deck", map[string]any{"title": "Tools", "theme": "aurora", "slides": []any{heading("A"), heading("B"), live}})
	if !ok {
		t.Fatalf("create_deck: %v", out)
	}
	id := out["deckId"].(string)
	deck := func() map[string]any {
		t.Helper()
		out, ok := call(t, cs, "get_deck", map[string]any{"deck_id": id})
		if !ok {
			t.Fatalf("get_deck: %v", out)
		}
		return out["deck"].(map[string]any)
	}
	slideIDs := func() []string {
		var ids []string
		for _, s := range deck()["slides"].([]any) {
			ids = append(ids, s.(map[string]any)["id"].(string))
		}
		return ids
	}
	ids := slideIDs()
	a, b, liveID := ids[0], ids[1], ids[2]
	version := func(name string, out map[string]any, ok bool, want float64) {
		t.Helper()
		if !ok || out["version"] != want {
			t.Fatalf("%s: want version %v, got %v", name, want, out)
		}
	}

	// update_slide replaces the fields it is given.
	out, ok = call(t, cs, "update_slide", map[string]any{"deck_id": id, "base_version": 1, "slide_id": a, "fields": map[string]any{"notes": "Say hello"}})
	version("update_slide", out, ok, 2)
	if n := deck()["slides"].([]any)[0].(map[string]any)["notes"]; n != "Say hello" {
		t.Fatalf("update_slide notes: %v", n)
	}

	// move_slide puts B first.
	out, ok = call(t, cs, "move_slide", map[string]any{"deck_id": id, "base_version": 2, "slide_id": b, "to": 0})
	version("move_slide", out, ok, 3)
	if got := slideIDs(); got[0] != b || got[1] != a {
		t.Fatalf("move_slide order: %v", got)
	}

	// set_theme changes the look only.
	out, ok = call(t, cs, "set_theme", map[string]any{"deck_id": id, "base_version": 3, "theme": "paper"})
	version("set_theme", out, ok, 4)
	if th := deck()["theme"]; th != "paper" {
		t.Fatalf("set_theme: %v", th)
	}

	// delete_slide removes A.
	out, ok = call(t, cs, "delete_slide", map[string]any{"deck_id": id, "base_version": 4, "slide_id": a})
	version("delete_slide", out, ok, 5)
	if got := slideIDs(); len(got) != 2 || got[0] != b || got[1] != liveID {
		t.Fatalf("delete_slide left: %v", got)
	}

	// get_history lists the versions newest first.
	out, ok = call(t, cs, "get_history", map[string]any{"deck_id": id, "limit": 10})
	if vs, _ := out["versions"].([]any); !ok || len(vs) != 5 || vs[0].(map[string]any)["version"] != float64(5) {
		t.Fatalf("get_history: %v", out)
	}

	// restore_version brings version 1 back as a new version.
	out, ok = call(t, cs, "restore_version", map[string]any{"deck_id": id, "base_version": 5, "version": 1})
	version("restore_version", out, ok, 6)
	if d := deck(); d["theme"] != "aurora" || len(d["slides"].([]any)) != 3 {
		t.Fatalf("restore_version: theme %v, %d slides", d["theme"], len(d["slides"].([]any)))
	}

	// list_comments shows a human's note; resolve_comment closes it with a reply.
	c, err := env.Svc.AddComment(ctx, id, b, "", "Make B punchier", "human")
	if err != nil {
		t.Fatal(err)
	}
	out, ok = call(t, cs, "list_comments", map[string]any{"deck_id": id})
	if got, _ := out["comments"].([]any); !ok || len(got) != 1 || got[0].(map[string]any)["text"] != "Make B punchier" {
		t.Fatalf("list_comments: %v", out)
	}
	if out, ok := call(t, cs, "resolve_comment", map[string]any{"deck_id": id, "comment_id": c.ID, "reply": "Shorter headline"}); !ok || out["ok"] != true {
		t.Fatalf("resolve_comment: %v", out)
	}
	if out, ok := call(t, cs, "resolve_comment", map[string]any{"deck_id": id, "comment_id": "nope"}); ok || !strings.Contains(out["error"].(string), "no comment") {
		t.Fatalf("resolve unknown comment: %v", out)
	}
	out, _ = call(t, cs, "list_comments", map[string]any{"deck_id": id})
	if open, _ := out["comments"].([]any); len(open) != 0 {
		t.Fatalf("open comments after resolving: %v", open)
	}
	out, _ = call(t, cs, "list_comments", map[string]any{"deck_id": id, "all": true})
	if all, _ := out["comments"].([]any); len(all) != 1 || all[0].(map[string]any)["status"] != "resolved" || all[0].(map[string]any)["reply"] != "Shorter headline" {
		t.Fatalf("resolved comment: %v", out)
	}

	// get_share_url gives the viewer link.
	out, ok = call(t, cs, "get_share_url", map[string]any{"deck_id": id})
	if u, _ := out["url"].(string); !ok || !strings.HasPrefix(u, "http://test/d/"+id) {
		t.Fatalf("get_share_url: %v", out)
	}

	// update_live changes one element on a live slide without a new version.
	out, ok = call(t, cs, "update_live", map[string]any{"deck_id": id, "slide_id": liveID, "element_id": "e_rate", "value": 42})
	if !ok || out["elementId"] != "e_rate" || out["element"].(map[string]any)["props"].(map[string]any)["value"] != float64(42) {
		t.Fatalf("update_live: %v", out)
	}
	if out, ok := call(t, cs, "update_live", map[string]any{"deck_id": id, "slide_id": b, "element_id": "e_rate", "value": 1}); ok {
		t.Fatalf("update_live on a plain slide: %v", out)
	}
	if out, _ := call(t, cs, "get_deck", map[string]any{"deck_id": id}); out["version"] != float64(6) {
		t.Fatalf("update_live made a version: %v", out["version"])
	}

	// export_deck: html is a download link; pdf needs Chromium, which this test leaves out.
	out, ok = call(t, cs, "export_deck", map[string]any{"deck_id": id})
	if u, _ := out["url"].(string); !ok || !strings.Contains(u, "/api/decks/"+id+"/export.html?download=1") || out["filename"] == "" || out["bytes"].(float64) <= 0 {
		t.Fatalf("export_deck html: %v", out)
	}
	if out, ok := call(t, cs, "export_deck", map[string]any{"deck_id": id, "format": "docx"}); ok || !strings.Contains(out["error"].(string), "not supported") {
		t.Fatalf("export_deck docx: %v", out)
	}
	if out, ok := call(t, cs, "export_deck", map[string]any{"deck_id": id, "format": "pdf"}); ok {
		t.Fatalf("export_deck pdf without a renderer: %v", out)
	}
}
