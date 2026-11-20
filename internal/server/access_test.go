package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick"
	"github.com/SecOps-7/slidekick/internal/mcpserver"
	"github.com/SecOps-7/slidekick/internal/server"
	"github.com/SecOps-7/slidekick/internal/service"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

func setupTwo(t *testing.T) (*httptest.Server, *service.Service, string, string) {
	env := testenv.New(t)
	h := server.New(env.Svc, mcpserver.New(env.Svc, slidekick.Schema, "test", mcpserver.Options{}),
		server.Options{Token: token, SchemaFS: slidekick.Schema, Web: fstest.MapFS{"index.html": {Data: []byte("app")}}})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	mk := func(title string) string {
		r, err := env.Svc.Create(context.Background(), title, "", "", []any{map[string]any{
			"layout": "title", "regions": map[string]any{"title": []any{map[string]any{"type": "heading", "props": map[string]any{"text": "Hi"}}}},
		}}, "agent")
		if err != nil {
			t.Fatal(err)
		}
		env.Svc.SetPrivate(context.Background(), r.DeckID, false, false) // start public; tests make it private
		return r.DeckID
	}
	return ts, env.Svc, mk("Mine"), mk("Other")
}

func getJSON(t *testing.T, url, tok string, out any) int {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestDeckTokenScope(t *testing.T) {
	ts, svc, mine, other := setupTwo(t)
	tok, err := svc.IssueEditToken(context.Background(), mine)
	if err != nil || !strings.HasPrefix(tok, "ske_") {
		t.Fatalf("token %q, %v", tok, err)
	}
	patch := map[string]any{"ops": []any{map[string]any{"op": "replace", "path": "/title", "value": "Edited"}}}

	if res := do(t, "POST", ts.URL+"/api/decks/"+mine+"/patch", tok, patch); res.StatusCode != 200 {
		t.Fatalf("own deck patch: %d", res.StatusCode)
	}
	if res := do(t, "POST", ts.URL+"/api/decks/"+other+"/patch", tok, patch); res.StatusCode != 403 {
		t.Fatalf("other deck patch: want 403, got %d", res.StatusCode)
	}
	if res := do(t, "POST", ts.URL+"/api/decks/"+mine+"/edit-token", tok, nil); res.StatusCode != 403 {
		t.Fatalf("deck token minting tokens: want 403, got %d", res.StatusCode)
	}
	var list struct{ Decks []struct{ ID string } }
	getJSON(t, ts.URL+"/api/decks", tok, &list)
	if len(list.Decks) != 1 || list.Decks[0].ID != mine {
		t.Fatalf("deck token sees %+v", list.Decks)
	}
	var who struct{ Scope, Deck string }
	getJSON(t, ts.URL+"/api/auth", tok, &who)
	if who.Scope != "deck" || who.Deck != mine {
		t.Fatalf("auth info %+v", who)
	}

	// Revoking kills the token.
	if res := do(t, "DELETE", ts.URL+"/api/decks/"+mine+"/edit-token", token, nil); res.StatusCode != 200 {
		t.Fatalf("revoke: %d", res.StatusCode)
	}
	if res := do(t, "POST", ts.URL+"/api/decks/"+mine+"/patch", tok, patch); res.StatusCode != 401 {
		t.Fatalf("revoked token: want 401, got %d", res.StatusCode)
	}
}

func TestPrivateDeck(t *testing.T) {
	ts, svc, mine, _ := setupTwo(t)
	ctx := context.Background()
	if code := getJSON(t, ts.URL+"/api/decks/"+mine, "", nil); code != 200 {
		t.Fatalf("public deck: %d", code)
	}
	var a service.Access
	res := do(t, "POST", ts.URL+"/api/decks/"+mine+"/access", token, map[string]any{"private": true})
	if res.StatusCode != 200 {
		t.Fatalf("set private: %d", res.StatusCode)
	}
	getJSON(t, ts.URL+"/api/decks/"+mine+"/access", token, &a)
	if !a.Private || len(a.ViewKey) < 20 || !strings.Contains(a.ShareURL, "?k="+a.ViewKey) {
		t.Fatalf("access %+v", a)
	}
	for _, p := range []string{"", "/history", "/comments", "/export.html"} {
		if code := getJSON(t, ts.URL+"/api/decks/"+mine+p, "", nil); code != 404 {
			t.Fatalf("private %q without key: want 404, got %d", p, code)
		}
		if code := getJSON(t, ts.URL+"/api/decks/"+mine+p+"?k=wrong", "", nil); code != 404 {
			t.Fatalf("private %q with wrong key: want 404, got %d", p, code)
		}
	}
	if code := getJSON(t, ts.URL+"/api/decks/"+mine+"?k="+a.ViewKey, "", nil); code != 200 {
		t.Fatalf("private with key: %d", code)
	}
	if code := getJSON(t, ts.URL+"/api/decks/"+mine, token, nil); code != 200 {
		t.Fatalf("private with server token: %d", code)
	}
	if !strings.Contains(svc.ShareURL(mine), "?k=") {
		t.Fatal("share url lacks key")
	}

	// WebSocket: no key, no auth -> closed; key -> hello; token auth -> hello.
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/decks/" + mine
	readType := func(c *websocket.Conn) string {
		rctx, done := context.WithTimeout(ctx, 3*time.Second)
		defer done()
		_, b, err := c.Read(rctx)
		if err != nil {
			return "closed"
		}
		var m struct{ Type string }
		json.Unmarshal(b, &m)
		return m.Type
	}
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Write(ctx, websocket.MessageText, []byte(`{"type":"auth","token":"nope"}`))
	if got := readType(c); got != "closed" {
		t.Fatalf("keyless ws got %q", got)
	}
	c, _, _ = websocket.Dial(ctx, wsURL+"?k="+a.ViewKey, nil)
	if got := readType(c); got != "hello" {
		t.Fatalf("keyed ws got %q", got)
	}
	// Rotating the key drops viewers holding the old one.
	if _, err := svc.SetPrivate(ctx, mine, true, true); err != nil {
		t.Fatal(err)
	}
	if got := readType(c); got != "closed" {
		t.Fatalf("after rotation, old-key viewer got %q", got)
	}
	c, _, _ = websocket.Dial(ctx, wsURL, nil)
	c.Write(ctx, websocket.MessageText, []byte(`{"type":"auth","token":"`+token+`"}`))
	if got := readType(c); got != "auth" {
		t.Fatalf("token ws got %q", got)
	}
	if got := readType(c); got != "hello" {
		t.Fatalf("token ws then got %q", got)
	}
	c.CloseNow()
}

type bearerRT struct{ tok string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}

func TestMCPDeckScope(t *testing.T) {
	ts, svc, mine, other := setupTwo(t)
	ctx := context.Background()
	tok, _ := svc.IssueEditToken(ctx, mine)
	tr := &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerRT{tok}}}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	callErr := func(name string, args map[string]any) bool {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res.IsError
	}
	if callErr("get_deck", map[string]any{"deck_id": mine}) {
		t.Fatal("own deck refused")
	}
	if !callErr("get_deck", map[string]any{"deck_id": other}) {
		t.Fatal("other deck allowed")
	}
	if !callErr("create_deck", map[string]any{"title": "New"}) {
		t.Fatal("deck token created a deck")
	}
	res, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_decks", Arguments: map[string]any{}})
	b, _ := json.Marshal(res.StructuredContent)
	if strings.Contains(string(b), other) || !strings.Contains(string(b), mine) {
		t.Fatalf("list_decks leaked: %s", b)
	}
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "slidekick://decks/" + other + "/comments"}); err == nil {
		t.Fatal("other deck's comments readable")
	}

	// A session made with the deck token can't be reused with the server token.
	sid := cs.ID()
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Mcp-Session-Id", sid)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatalf("session hijack: want 403, got %d", r.StatusCode)
	}
}
