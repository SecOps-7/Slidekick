package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick/internal/cast/casttest"
	"github.com/SecOps-7/slidekick/internal/presenting"
	"github.com/SecOps-7/slidekick/internal/server"
)

// newPresentServer is an accounts server with presenting on. There is no
// voice service, so every line is a browser voice: no audio is fetched.
func newPresentServer(t *testing.T) (*accEnv, *presenting.Manager) {
	m := &presenting.Manager{Slack: 50 * time.Millisecond, Pause: 5 * time.Millisecond}
	e := newAccServerWith(t, "", func(o *server.Options) { o.Presenter = m })
	m.Deps = presenting.SvcDeps{Svc: e.env.Svc}
	return e, m
}

func notedSlides(notes ...string) []any {
	out := make([]any, len(notes))
	for i, n := range notes {
		s := map[string]any{"layout": "title", "regions": map[string]any{"title": []any{map[string]any{"type": "heading", "props": map[string]any{"text": fmt.Sprint("Topic ", i+1)}}}}}
		if n != "" {
			s["notes"] = n
		}
		out[i] = s
	}
	return out
}

// anonAck acks a step the way a cast TV does: no cookie, only the view key.
func anonAck(t *testing.T, base, id, key string, seq any) int {
	t.Helper()
	res, err := http.Post(base+"/api/decks/"+id+"/present/ack?k="+url.QueryEscape(key), "application/json", strings.NewReader(fmt.Sprintf(`{"seq":%v}`, seq)))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestPresentRoutes(t *testing.T) {
	e, _ := newPresentServer(t)
	admin := e.withAdmin(t)
	owner := e.addUser(t, admin, "edna", "editor")
	other := e.addUser(t, admin, "eddie", "editor")
	view := e.addUser(t, admin, "vera", "viewer")

	_, out := owner.do("POST", "/api/decks", map[string]any{"title": "No notes", "slides": notedSlides("Hello.", "", "")})
	bare := out["deckId"].(string)
	// Slides don't need notes: the narration is written from their content.
	if code, out := owner.do("POST", "/api/decks/"+bare+"/present", map[string]any{"script": []any{map[string]any{"slide": 2, "lines": []string{"Revenue grew."}}}}); code != 200 || out["running"] != true {
		t.Fatalf("start without notes: %d %v", code, out)
	}
	if code, out := owner.do("DELETE", "/api/decks/"+bare+"/present", nil); code != 200 || out["stopped"] != true {
		t.Fatalf("stop: %d %v", code, out)
	}
	if code, _ := owner.do("POST", "/api/decks/"+bare+"/present", map[string]any{"script": []any{map[string]any{"slide": 9, "lines": []string{"x"}}}}); code == 200 {
		t.Fatal("a script for slide 9 of 3 should be refused")
	}

	// The second slide's notes are a stage direction followed by content.
	_, out = owner.do("POST", "/api/decks", map[string]any{"title": "Talk", "slides": notedSlides("Welcome.", "Here is the second slide. Revenue grew 12%.")})
	id := out["deckId"].(string)
	key := e.env.Svc.ViewKey(context.Background(), id)
	for name, c := range map[string]*client{"viewer": view, "other editor": other} {
		if code, _ := c.do("POST", "/api/decks/"+id+"/present", map[string]any{}); code != 403 {
			t.Fatalf("%s start: %d", name, code)
		}
		if code, _ := c.do("DELETE", "/api/decks/"+id+"/present", nil); code != 403 {
			t.Fatalf("%s stop: %d", name, code)
		}
	}
	// Without the view key a private deck's ack and status are hidden.
	if code := anonAck(t, e.ts.URL, id, "wrong", 1); code != 404 {
		t.Fatalf("ack without key: %d", code)
	}

	ch, cancel := e.env.Svc.Hub.Subscribe(id)
	defer cancel()
	start := time.Now()
	code, out := owner.do("POST", "/api/decks/"+id+"/present", map[string]any{"voice": "Samantha"})
	if code != 200 || out["running"] != true || out["actor"] != "user:edna" {
		t.Fatalf("start: %d %v", code, out)
	}
	if code, _ := owner.do("POST", "/api/decks/"+id+"/present", map[string]any{}); code != 409 {
		t.Fatalf("second start: %d", code)
	}
	if ev := nextEvent(t, ch, "present"); ev["state"] != "started" {
		t.Fatalf("%v", ev)
	}
	for _, want := range []string{"Welcome.", "Revenue grew 12%."} {
		ev := nextEvent(t, ch, "present")
		if ev["state"] != "step" || ev["text"] != want || ev["browserVoice"] != true || ev["voice"] != "Samantha" {
			t.Fatalf("step %v", ev)
		}
		if code := anonAck(t, e.ts.URL, id, key, ev["seq"]); code != 200 {
			t.Fatalf("ack: %d", code)
		}
	}
	if ev := nextEvent(t, ch, "present"); ev["state"] != "ended" || ev["error"] != nil {
		t.Fatalf("%v", ev)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("acks should pace the run")
	}
	code, out = view.do("GET", "/api/decks/"+id+"/present", nil)
	if code != 200 || out["running"] != false || out["slide"] != float64(2) {
		t.Fatalf("status: %d %v", code, out)
	}

	// Stop mid-run.
	owner.do("POST", "/api/decks/"+id+"/present", map[string]any{})
	nextEvent(t, ch, "present") // started
	nextEvent(t, ch, "present") // step 1, never acked
	if code, out := admin.do("DELETE", "/api/decks/"+id+"/present", nil); code != 200 || out["stopped"] != true {
		t.Fatalf("stop: %d %v", code, out)
	}
	if ev := nextEvent(t, ch, "present"); ev["state"] != "stopped" {
		t.Fatalf("%v", ev)
	}
	if code, out := owner.do("DELETE", "/api/decks/"+id+"/present", nil); code != 200 || out["stopped"] != false {
		t.Fatalf("second stop: %d %v", code, out)
	}
}

func toolText(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestPresentTools(t *testing.T) {
	e, _ := newPresentServer(t)
	admin := e.withAdmin(t)
	ed := e.addUser(t, admin, "edna", "editor")
	view := e.addUser(t, admin, "vera", "viewer")
	ctx := context.Background()
	_, out := ed.do("POST", "/api/decks", map[string]any{"title": "Talk", "slides": notedSlides("Welcome.", "")})
	id := out["deckId"].(string)
	key := e.env.Svc.ViewKey(ctx, id)
	ch, cancel := e.env.Svc.Hub.Subscribe(id)
	defer cancel()

	cs := mcpAs(t, e, personalToken(t, ed))
	call := func(cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, *mcp.CallToolResult) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, _ := json.Marshal(res.StructuredContent)
		var m map[string]any
		json.Unmarshal(b, &m)
		return m, res
	}

	// present_slide blocks until the presenting screen acks the line.
	type result struct {
		out map[string]any
		res *mcp.CallToolResult
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		o, r := call(cs, "present_slide", map[string]any{"deck_id": id, "slide": 2, "say": "Here is the second slide. Revenue grew 12% this year."})
		done <- result{o, r}
	}()
	ev := nextEvent(t, ch, "present")
	if ev["state"] != "step" || ev["index"] != float64(1) || ev["text"] != "Revenue grew 12% this year." || ev["actor"] != "agent:edna" {
		t.Fatalf("step %v", ev)
	}
	anonAck(t, e.ts.URL, id, key, ev["seq"])
	r := <-done
	if r.res.IsError || r.out["heard"] != true || r.out["slide"] != float64(2) || r.out["voice"] != "browser" {
		t.Fatalf("present_slide: %v %s", r.out, toolText(r.res))
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("present_slide should return on the ack")
	}

	// start_presentation runs without notes on slide 2, and takes the caller's script.
	o, res := call(cs, "start_presentation", map[string]any{"deck_id": id, "script": []any{map[string]any{"slide": 1, "lines": []string{"Let's move on. Our margins doubled."}}}})
	if res.IsError || o["status"].(map[string]any)["running"] != true {
		t.Fatalf("start: %v %s", o, toolText(res))
	}
	if o, _ := call(cs, "presentation_status", map[string]any{"deck_id": id}); o["running"] != true {
		t.Fatalf("status %v", o)
	}
	if _, res := call(cs, "present_slide", map[string]any{"deck_id": id, "slide": 1}); !res.IsError || !strings.Contains(toolText(res), "stop_presentation") {
		t.Fatalf("present_slide during a run: %s", toolText(res))
	}
	if o, _ := call(cs, "stop_presentation", map[string]any{"deck_id": id}); o["stopped"] != true {
		t.Fatalf("stop %v", o)
	}

	// Viewers and deck tokens can't present: it needs sharing rights and the voices.
	vcs := mcpAs(t, e, personalToken(t, view))
	for _, tool := range []string{"present_slide", "start_presentation", "stop_presentation", "cast_deck", "stop_cast", "list_cast_devices"} {
		if _, res := call(vcs, tool, map[string]any{"deck_id": id, "say": "hi", "device": "x"}); !res.IsError {
			t.Fatalf("viewer %s allowed", tool)
		}
	}
	if o, res := call(vcs, "presentation_status", map[string]any{"deck_id": id}); res.IsError || o["slides"] != float64(2) {
		t.Fatalf("viewer status: %v %s", o, toolText(res))
	}
	_, tokOut := ed.do("POST", "/api/decks/"+id+"/edit-token", nil)
	dcs := mcpAs(t, e, tokOut["token"].(string))
	if _, res := call(dcs, "present_slide", map[string]any{"deck_id": id, "say": "hi"}); !res.IsError || !strings.Contains(toolText(res), "deck token") {
		t.Fatalf("deck token present: %s", toolText(res))
	}

	// Casting by device name.
	tv := casttest.New(t)
	if _, err := e.env.Svc.AddCastDevice(ctx, "Office TV", tv.Addr(), 0); err != nil {
		t.Fatal(err)
	}
	o, _ = call(cs, "list_cast_devices", map[string]any{})
	if s := toJSON(o); !strings.Contains(s, "Office TV") || strings.Contains(s, tv.Host) {
		t.Fatalf("devices %s", s)
	}
	if _, res := call(cs, "cast_deck", map[string]any{"deck_id": id, "device": "kitchen"}); !res.IsError || !strings.Contains(toolText(res), "Office TV") {
		t.Fatalf("unknown device: %s", toolText(res))
	}
	o, res = call(cs, "cast_deck", map[string]any{"deck_id": id, "device": "office tv"})
	if res.IsError || o["casting"].(map[string]any)["device"] != "Office TV" || len(tv.Loaded()) != 1 {
		t.Fatalf("cast: %v %s", o, toolText(res))
	}
	if o, _ := call(cs, "stop_cast", map[string]any{"deck_id": id}); o["stopped"] != true {
		t.Fatalf("stop_cast %v", o)
	}
	if o, _ := call(cs, "stop_cast", map[string]any{"deck_id": id}); o["stopped"] != false {
		t.Fatalf("stop_cast twice %v", o)
	}
}
