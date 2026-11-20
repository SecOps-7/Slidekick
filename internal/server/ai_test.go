package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SecOps-7/slidekick"
	"github.com/SecOps-7/slidekick/internal/accounts"
	"github.com/SecOps-7/slidekick/internal/ai"
	"github.com/SecOps-7/slidekick/internal/mcpserver"
	"github.com/SecOps-7/slidekick/internal/oidc"
	"github.com/SecOps-7/slidekick/internal/secret"
	"github.com/SecOps-7/slidekick/internal/server"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

const fakeKey = "sk-test-super-secret-key-1234567890"

// fakeLLM is an OpenAI-compatible chat completions endpoint that plays a
// script: each request gets the next step (tool calls or text).
type fakeLLM struct {
	srv   *httptest.Server
	mu    sync.Mutex
	steps []func(w http.ResponseWriter, r *http.Request)
	seen  []map[string]any // request bodies
}

func newFakeLLM(t *testing.T) *fakeLLM {
	f := &fakeLLM{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fakeKey {
			w.WriteHeader(401)
			fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s"}}`, r.Header.Get("Authorization"))
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.seen = append(f.seen, body)
		var step func(http.ResponseWriter, *http.Request)
		if len(f.steps) > 0 {
			step, f.steps = f.steps[0], f.steps[1:]
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if step == nil {
			sayText("Nothing more to do.")(w, r)
			return
		}
		step(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLLM) script(steps ...func(http.ResponseWriter, *http.Request)) {
	f.mu.Lock()
	f.steps = steps
	f.seen = nil
	f.mu.Unlock()
}

func sse(w http.ResponseWriter, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
	w.(http.Flusher).Flush()
}

func sayText(text string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		sse(w, map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": text}}}})
		sse(w, map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

func callTool(name string, args map[string]any) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		a, _ := json.Marshal(args)
		sse(w, map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"index": 0, "id": "call_" + name, "type": "function", "function": map[string]any{"name": name, "arguments": string(a)}},
		}}}}})
		sse(w, map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}})
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

type aiEnv struct {
	*accEnv
	llm *fakeLLM
	log *bytes.Buffer
}

func newAIServer(t *testing.T) *aiEnv {
	t.Helper()
	env := testenv.New(t)
	box, _, _ := secret.Load("", t.TempDir())
	acc := accounts.New(env.Store, box, oidc.NewManager(nil))
	e := &aiEnv{accEnv: &accEnv{env: env, acc: acc}, llm: newFakeLLM(t), log: &bytes.Buffer{}}
	mcpSrv := mcpserver.New(env.Svc, slidekick.Schema, "test", mcpserver.Options{Policy: acc.Policy()})
	e.ts = httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(e.ts.Close)
	aiSvc := &ai.Service{Store: env.Store, Box: box, Accounts: acc, MCPURL: e.ts.URL + "/mcp"}
	var logMu sync.Mutex
	e.ts.Config.Handler = server.New(env.Svc, mcpSrv, server.Options{BaseURL: e.ts.URL, Accounts: acc, AI: aiSvc, SchemaFS: slidekick.Schema,
		Web: fstest.MapFS{"index.html": {Data: []byte("app")}}, Log: newLockedLogger(e.log, &logMu)})
	return e
}

// chat posts one message and collects the streamed events.
func (c *client) chat(deck, msg string) (int, []ai.Event) {
	c.t.Helper()
	b, _ := json.Marshal(ai.ChatRequest{Provider: "fake", Model: "fake-model", Message: msg})
	req, _ := http.NewRequest("POST", c.base+"/api/decks/"+deck+"/ai/chat", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf)
	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var evs []ai.Event
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		if line, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var ev ai.Event
			json.Unmarshal([]byte(line), &ev)
			evs = append(evs, ev)
		}
	}
	return res.StatusCode, evs
}

func (e *aiEnv) setupProvider(t *testing.T, admin *client) {
	code, out := admin.do("PUT", "/api/admin/ai/fake", map[string]any{"name": "Fake", "kind": "openai-compatible", "baseUrl": e.llm.srv.URL + "/v1",
		"apiKey": fakeKey, "models": []string{"fake-model"}, "enabled": true})
	if code != 200 {
		t.Fatalf("add provider: %d %v", code, out)
	}
}

func deckTitle(t *testing.T, e *aiEnv, id string) string {
	d, _, _, err := e.env.Svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d.Title
}

func TestAIProviderKeyHandling(t *testing.T) {
	e := newAIServer(t)
	admin := e.withAdmin(t)
	e.setupProvider(t, admin)
	_, list := admin.do("GET", "/api/admin/ai", nil)
	if strings.Contains(toJSON(list), fakeKey) || !strings.Contains(toJSON(list), `"hasKey":true`) {
		t.Fatalf("admin list: %s", toJSON(list))
	}
	p, _ := e.env.Store.GetAIProvider(context.Background(), "fake")
	if p.KeyEnc == "" || strings.Contains(p.KeyEnc, fakeKey) || strings.Contains(p.KeyEnc, "secret") {
		t.Fatalf("key stored in the clear: %q", p.KeyEnc)
	}
	// Saving without a key keeps it; editors and viewers can't touch settings.
	admin.do("PUT", "/api/admin/ai/fake", map[string]any{"name": "Fake 2", "kind": "openai-compatible", "baseUrl": e.llm.srv.URL + "/v1", "models": []string{"fake-model"}, "enabled": true})
	if p2, _ := e.env.Store.GetAIProvider(context.Background(), "fake"); p2.KeyEnc == "" {
		t.Fatal("key lost when saving without one")
	}
	ed := e.addUser(t, admin, "edna", "editor")
	if code, _ := ed.do("GET", "/api/admin/ai", nil); code != 403 {
		t.Fatalf("editor lists AI settings: %d", code)
	}
	_, pub := ed.do("GET", "/api/ai/providers", nil)
	if s := toJSON(pub); strings.Contains(s, fakeKey) || strings.Contains(s, e.llm.srv.URL) || !strings.Contains(s, "fake-model") {
		t.Fatalf("user provider list: %s", s)
	}
	if code, _ := admin.do("PUT", "/api/admin/ai/bad", map[string]any{"kind": "openai-compatible", "baseUrl": "file:///etc/passwd", "models": []string{"m"}}); code != 422 {
		t.Fatalf("bad base URL: %d", code)
	}
}

func TestAIChatEditsAsUser(t *testing.T) {
	e := newAIServer(t)
	admin := e.withAdmin(t)
	e.setupProvider(t, admin)
	ed := e.addUser(t, admin, "edna", "editor")
	_, out := ed.do("POST", "/api/decks", map[string]any{"title": "Draft", "slides": oneSlide})
	id := out["deckId"].(string)

	e.llm.script(
		callTool("get_deck", map[string]any{"deck_id": id}),
		callTool("patch_deck", map[string]any{"deck_id": id, "ops": []any{map[string]any{"op": "replace", "path": "/title", "value": "Q3 launch"}}}),
		sayText("Renamed the deck to Q3 launch."),
	)
	code, evs := ed.chat(id, "Call it Q3 launch")
	if code != 200 {
		t.Fatalf("chat: %d", code)
	}
	var text string
	var patched, done bool
	for _, ev := range evs {
		switch ev.Type {
		case "text":
			text += ev.Text
		case "tool_result":
			if ev.Tool == "patch_deck" && ev.OK && len(ev.Changed) > 0 {
				patched = true
			}
		case "done":
			done = ev.Version == ev.Start+1
		case "error":
			t.Fatalf("chat error: %s", ev.Text)
		}
	}
	if !patched || !done || text != "Renamed the deck to Q3 launch." {
		t.Fatalf("events: %+v", evs)
	}
	if got := deckTitle(t, e, id); got != "Q3 launch" {
		t.Fatalf("title %q", got)
	}
	// The edit is recorded as an agent acting for edna, and the model saw the tool result.
	h, _ := e.env.Svc.History(context.Background(), id, 1)
	if h[0].Actor != "agent:edna" {
		t.Fatalf("actor %q", h[0].Actor)
	}
	if b := toJSON(e.llm.seen[2]); !strings.Contains(b, `"role":"tool"`) || !strings.Contains(b, "Q3 launch") {
		t.Fatalf("tool result not sent back to the model: %s", b)
	}
	// The key never reaches the browser or the log.
	if strings.Contains(toJSON(evs), fakeKey) || strings.Contains(e.log.String(), fakeKey) {
		t.Fatal("key leaked")
	}
}

func TestAIChatCannotExceedUser(t *testing.T) {
	e := newAIServer(t)
	admin := e.withAdmin(t)
	e.setupProvider(t, admin)
	ed := e.addUser(t, admin, "edna", "editor")
	view := e.addUser(t, admin, "vera", "viewer")
	_, out := admin.do("POST", "/api/decks", map[string]any{"title": "Board deck", "slides": oneSlide})
	adminDeck := out["deckId"].(string)
	_, out = ed.do("POST", "/api/decks", map[string]any{"title": "Mine", "slides": oneSlide})
	mine := out["deckId"].(string)
	accBefore, _ := e.env.Svc.GetAccess(context.Background(), mine)
	keyBefore := accBefore.ViewKey

	// Viewers can't use AI at all.
	if code, _ := view.chat(adminDeck, "hi"); code != 403 {
		t.Fatalf("viewer chat: %d", code)
	}
	if code, _ := view.do("GET", "/api/ai/providers", nil); code != 403 {
		t.Fatalf("viewer providers: %d", code)
	}

	// An editor's AI can't change someone else's deck, can't use tools outside
	// the chat set, and its invalid output is rejected by validation.
	e.llm.script(
		callTool("patch_deck", map[string]any{"deck_id": adminDeck, "ops": []any{map[string]any{"op": "replace", "path": "/title", "value": "hijacked"}}}),
		callTool("set_deck_access", map[string]any{"deck_id": mine, "private": false}),
		callTool("upload_asset", map[string]any{"name": "x.svg", "mime": "image/svg+xml", "data": "PHN2Zy8+"}),
		callTool("patch_deck", map[string]any{"deck_id": mine, "ops": []any{map[string]any{"op": "add", "path": "/slides/0/regions/title/-", "value": map[string]any{"type": "script", "props": map[string]any{"src": "https://evil.example/x.js"}}}}}),
		sayText("I couldn't do that."),
	)
	_, evs := ed.chat(mine, "Rename the board deck and add a script")
	results := map[string]bool{}
	for _, ev := range evs {
		if ev.Type == "tool_result" {
			if ev.OK {
				t.Fatalf("%s succeeded: %+v", ev.Tool, ev)
			}
			results[ev.Tool] = true
		}
	}
	if len(results) != 3 { // patch_deck (twice), set_deck_access, upload_asset
		t.Fatalf("results %v", results)
	}
	if got := deckTitle(t, e, adminDeck); got != "Board deck" {
		t.Fatalf("someone else's deck changed: %q", got)
	}
	if acc, _ := e.env.Svc.GetAccess(context.Background(), mine); !acc.Private || acc.ViewKey != keyBefore {
		t.Fatal("chat changed sharing")
	}
	d, _, _, _ := e.env.Svc.Get(context.Background(), mine)
	if len(d.Slides[0].Regions["title"]) != 1 {
		t.Fatal("invalid element got into the deck")
	}
}

func TestAIChatStopAndErrors(t *testing.T) {
	e := newAIServer(t)
	admin := e.withAdmin(t)
	e.setupProvider(t, admin)
	_, out := admin.do("POST", "/api/decks", map[string]any{"title": "Deck", "slides": oneSlide})
	id := out["deckId"].(string)

	// Stop: the model hangs mid-stream; cancelling the request ends it and no tool runs.
	hung := make(chan struct{})
	e.llm.script(func(w http.ResponseWriter, r *http.Request) {
		sse(w, map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "Thinking"}}}})
		<-r.Context().Done()
		close(hung)
	})
	ctx, cancel := context.WithCancel(context.Background())
	b, _ := json.Marshal(ai.ChatRequest{Provider: "fake", Model: "fake-model", Message: "go"})
	req, _ := http.NewRequestWithContext(ctx, "POST", admin.base+"/api/decks/"+id+"/ai/chat", bytes.NewReader(b))
	req.Header.Set("X-CSRF-Token", admin.csrf)
	res, err := admin.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(res.Body)
	for {
		line, _ := br.ReadString('\n')
		if strings.Contains(line, "Thinking") {
			break
		}
	}
	cancel()
	res.Body.Close()
	select {
	case <-hung:
	case <-time.After(5 * time.Second):
		t.Fatal("model call not cancelled by Stop")
	}

	// A provider error comes back without the key.
	e.llm.script()
	p, _ := e.env.Store.GetAIProvider(context.Background(), "fake")
	wrong := "sk-wrong-key-that-is-long-enough-123"
	admin.do("PUT", "/api/admin/ai/fake", map[string]any{"name": p.Name, "kind": p.Kind, "baseUrl": p.BaseURL, "apiKey": wrong, "models": p.Models, "enabled": true})
	_, evs := admin.chat(id, "hi")
	last := evs[len(evs)-1]
	if last.Type != "error" || strings.Contains(last.Text, wrong) || strings.Contains(last.Text, "1234567890") {
		t.Fatalf("error event: %+v", last)
	}
	if strings.Contains(e.log.String(), wrong) {
		t.Fatal("key in the log")
	}
	// Unknown model is refused before any call.
	b, _ = json.Marshal(ai.ChatRequest{Provider: "fake", Model: "gpt-unlisted", Message: "x"})
	req, _ = http.NewRequest("POST", admin.base+"/api/decks/"+id+"/ai/chat", bytes.NewReader(b))
	req.Header.Set("X-CSRF-Token", admin.csrf)
	res, _ = admin.hc.Do(req)
	body := new(bytes.Buffer)
	body.ReadFrom(res.Body)
	res.Body.Close()
	if !strings.Contains(body.String(), "isn't enabled") {
		t.Fatalf("unlisted model: %s", body)
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func newLockedLogger(b *bytes.Buffer, mu *sync.Mutex) *slog.Logger {
	return slog.New(slog.NewTextHandler(lockedWriter{mu, b}, nil))
}

func TestAIChatStaysOnOpenDeckAndFocuses(t *testing.T) {
	e := newAIServer(t)
	admin := e.withAdmin(t)
	e.setupProvider(t, admin)
	ed := e.addUser(t, admin, "edna", "editor")
	_, out := ed.do("POST", "/api/decks", map[string]any{"title": "Open deck", "slides": threeSlides})
	open := out["deckId"].(string)
	_, out = ed.do("POST", "/api/decks", map[string]any{"title": "Another of mine", "slides": oneSlide})
	elsewhere := out["deckId"].(string)
	ch, cancel := e.env.Svc.Hub.Subscribe(open)
	defer cancel()

	e.llm.script(
		callTool("create_deck", map[string]any{"title": "Out of view"}),
		callTool("patch_deck", map[string]any{"deck_id": elsewhere, "ops": []any{map[string]any{"op": "replace", "path": "/title", "value": "moved"}}}),
		callTool("focus_slide", map[string]any{"slide": 2}),
		sayText("Focused."),
	)
	_, evs := ed.chat(open, "Make slide 3 punchier")
	// Slide 3 came into view before the model did anything.
	if f := nextEvent(t, ch, "focus"); f["index"] != float64(2) {
		t.Fatalf("auto focus %v", f)
	}
	var steps []string
	for _, ev := range evs {
		if ev.Type == "tool_result" {
			steps = append(steps, fmt.Sprintf("%s:%v", ev.Tool, ev.OK))
		}
	}
	want := "focus_slide:true create_deck:false patch_deck:false focus_slide:true"
	if strings.Join(steps, " ") != want {
		t.Fatalf("steps %v, want %s", steps, want)
	}
	if f := nextEvent(t, ch, "focus"); f["index"] != float64(1) {
		t.Fatalf("model focus %v", f)
	}
	if deckTitle(t, e, elsewhere) != "Another of mine" {
		t.Fatal("chat changed another deck")
	}
	if _, list := ed.do("GET", "/api/decks", nil); strings.Contains(toJSON(list), "Out of view") {
		t.Fatal("chat created a deck")
	}
	// The model is never offered create_deck, and is told which deck is open.
	first := toJSON(e.llm.seen[0])
	if strings.Contains(first, `"name":"create_deck"`) || !strings.Contains(first, `"name":"focus_slide"`) || !strings.Contains(first, open) {
		t.Fatalf("tools/prompt sent to the model: %.400s", first)
	}
}

func TestMentionedSlide(t *testing.T) {
	cases := map[string]int{"make slide 4 punchier": 4, "Slide #2 needs a chart": 2, "on slides 3 and 5": 3, "slide no. 7": 7, "add a slide": 0, "slideshow 9": 0}
	for msg, want := range cases {
		if got := ai.MentionedSlide(msg); got != want {
			t.Errorf("%q: %d, want %d", msg, got, want)
		}
	}
}
