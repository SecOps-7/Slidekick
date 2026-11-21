package mcpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick"
	"github.com/SecOps-7/slidekick/internal/mcpserver"
	"github.com/SecOps-7/slidekick/internal/presenting"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	env := testenv.New(t)
	srv := mcpserver.New(env.Svc, slidekick.Schema, "test", mcpserver.Options{})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) (map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		return map[string]any{"error": res.Content[0].(*mcp.TextContent).Text}, false
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out, true
}

func TestAgentLoop(t *testing.T) {
	cs := connect(t)
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 24 { // the presenting tools need Options.Presenter
		t.Fatalf("want 24 tools, got %d", len(tools.Tools))
	}

	var deck map[string]any
	json.Unmarshal(testenvExample(t), &deck)
	out, ok := call(t, cs, "create_deck", map[string]any{"title": "Launch", "theme": "aurora", "slides": deck["slides"]})
	if !ok {
		t.Fatalf("create_deck: %v", out)
	}
	id := out["deckId"].(string)
	if !strings.HasPrefix(out["url"].(string), "http://test/d/") {
		t.Fatalf("url: %v", out["url"])
	}

	// A write with a lint problem returns the finding in the same response.
	out, ok = call(t, cs, "add_slide", map[string]any{"deck_id": id, "base_version": 1, "slide": map[string]any{
		"layout": "hero-image", "regions": map[string]any{"image": []any{map[string]any{"type": "image", "props": map[string]any{"src": "https://example.com/a.jpg"}}}},
	}})
	if !ok || out["version"].(float64) != 2 {
		t.Fatalf("add_slide: %v", out)
	}
	if !strings.Contains(mustJSON(out["lint"]), "missing-alt") {
		t.Fatalf("expected missing-alt in lint: %v", out["lint"])
	}

	// Stale base_version is reported as a tool error the agent can read.
	out, ok = call(t, cs, "patch_deck", map[string]any{"deck_id": id, "base_version": 1,
		"ops": []any{map[string]any{"op": "replace", "path": "/title", "value": "x"}}})
	if ok || !strings.Contains(out["error"].(string), "stale") {
		t.Fatalf("expected stale error, got %v", out)
	}

	// Fix via patch using the new version.
	out, ok = call(t, cs, "patch_deck", map[string]any{"deck_id": id, "base_version": 2,
		"ops": []any{map[string]any{"op": "add", "path": "/slides/7/regions/image/0/props/alt", "value": "Launch stage"}}})
	if !ok || strings.Contains(mustJSON(out["lint"]), "missing-alt") {
		t.Fatalf("patch_deck: %v", out)
	}

	if out, ok := call(t, cs, "get_deck", map[string]any{"deck_id": id}); !ok || out["version"].(float64) != 3 {
		t.Fatalf("get_deck: %v", out)
	}
	if out, ok := call(t, cs, "list_themes", map[string]any{}); !ok || len(out["themes"].([]any)) < 2 {
		t.Fatalf("list_themes: %v", out)
	}
	if out, ok := call(t, cs, "lint_deck", map[string]any{"deck_id": "nope"}); ok || !strings.Contains(out["error"].(string), "list_decks") {
		t.Fatalf("lint unknown deck: %v", out)
	}

	for _, uri := range []string{"slidekick://schema/deck", "slidekick://catalog/layouts", "slidekick://catalog/animations", "slidekick://catalog/themes", "slidekick://guide"} {
		r, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil || len(r.Contents) == 0 || r.Contents[0].Text == "" {
			t.Fatalf("resource %s: %v", uri, err)
		}
	}
	p, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "outline_to_deck", Arguments: map[string]string{"outline": "Q3 results"}})
	if err != nil || !strings.Contains(p.Messages[0].Content.(*mcp.TextContent).Text, "Q3 results") {
		t.Fatalf("prompt: %v", err)
	}
}

func testenvExample(t *testing.T) []byte { return testenv.Example(t, "launch.json") }

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestPresentingToolsListed(t *testing.T) {
	env := testenv.New(t)
	m := &presenting.Manager{Deps: presenting.SvcDeps{Svc: env.Svc}}
	srv := mcpserver.New(env.Svc, slidekick.Schema, "test", mcpserver.Options{Presenter: m})
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
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range tools.Tools {
		have[tl.Name] = true
	}
	for _, n := range []string{"present_slide", "start_presentation", "stop_presentation", "presentation_status", "list_cast_devices", "cast_deck", "stop_cast"} {
		if !have[n] {
			t.Errorf("missing tool %s", n)
		}
	}
	if len(tools.Tools) != 28 {
		t.Fatalf("want 28 tools, got %d", len(tools.Tools))
	}
	// The server token (stdio) may present; a deck without notes says how to fix it.
	res, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "create_deck", Arguments: map[string]any{"title": "T", "slides": []any{
		map[string]any{"layout": "title", "regions": map[string]any{"title": []any{map[string]any{"type": "heading", "props": map[string]any{"text": "Hi"}}}}}}}})
	b, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	json.Unmarshal(b, &out)
	res, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "start_presentation", Arguments: map[string]any{"deck_id": out["deckId"]}})
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "slide 1 has no speaker notes") {
		t.Fatalf("start: %+v", res.Content)
	}
}
