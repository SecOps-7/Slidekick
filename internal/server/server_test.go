package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/coder/websocket"

	"github.com/SecOps-7/slidekick"
	"github.com/SecOps-7/slidekick/internal/mcpserver"
	"github.com/SecOps-7/slidekick/internal/server"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

const token = "test-token"

func setup(t *testing.T) (*httptest.Server, string) {
	env := testenv.New(t)
	h := server.New(env.Svc, mcpserver.New(env.Svc, slidekick.Schema, "test", mcpserver.Options{}),
		server.Options{Token: token, SchemaFS: slidekick.Schema, Web: fstest.MapFS{"index.html": {Data: []byte("app")}}})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	r, err := env.Svc.Create(context.Background(), "Demo", "", "", []any{map[string]any{
		"layout": "title", "regions": map[string]any{"title": []any{map[string]any{"type": "heading", "props": map[string]any{"text": "Hi"}}}},
	}}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	// These tests are about public decks: new decks start private, so open it up.
	if _, err := env.Svc.SetPrivate(context.Background(), r.DeckID, false, false); err != nil {
		t.Fatal(err)
	}
	return ts, r.DeckID
}

func do(t *testing.T, method, url, tok string, body any) *http.Response {
	var b bytes.Buffer
	if body != nil {
		json.NewEncoder(&b).Encode(body)
	}
	req, _ := http.NewRequest(method, url, &b)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func TestWritesNeedToken(t *testing.T) {
	routes := []struct{ method, path string }{
		{"GET", "/api/auth"},
		{"GET", "/api/decks"},
		{"POST", "/api/decks/{id}/patch"},
		{"POST", "/api/decks/{id}/restore"},
		{"POST", "/api/decks/{id}/export?format=pdf"},
		{"POST", "/api/decks/{id}/comments"},
		{"POST", "/api/decks/{id}/comments/c_x/resolve"},
		{"POST", "/api/files"},
		{"POST", "/mcp"},
	}
	for _, rt := range routes {
		ts, id := setup(t) // fresh server: keep below the failed-attempt lockout
		u := ts.URL + strings.ReplaceAll(rt.path, "{id}", id)
		if res := do(t, rt.method, u, "", nil); res.StatusCode != 401 {
			t.Errorf("%s %s without token: %d", rt.method, rt.path, res.StatusCode)
		}
		if res := do(t, rt.method, u, "wrong", nil); res.StatusCode != 401 {
			t.Errorf("%s %s wrong token: %d", rt.method, rt.path, res.StatusCode)
		}
	}
	ts, id := setup(t)
	base := ts.URL + "/api/decks/" + id
	// Reads stay open.
	for _, u := range []string{base, base + "/comments", ts.URL + "/api/catalog", ts.URL + "/themes/aurora/theme.css", ts.URL + "/d/" + id} {
		if res := do(t, "GET", u, "", nil); res.StatusCode != 200 {
			t.Errorf("GET %s: %d", u, res.StatusCode)
		}
	}
	if res := do(t, "GET", ts.URL+"/api/auth", token, nil); res.StatusCode != 200 {
		t.Errorf("auth check with token: %d", res.StatusCode)
	}
	if res := do(t, "POST", base+"/comments", token, map[string]string{"text": "hello"}); res.StatusCode != 200 {
		t.Errorf("comment with token: %d", res.StatusCode)
	}
	// The font route serves only woff2 files listed in faces.json.
	for _, f := range []string{"faces.json", "README.md", "licenses%2Finter.txt"} {
		if res := do(t, "GET", ts.URL+"/fonts/"+f, "", nil); res.StatusCode != 404 {
			t.Errorf("/fonts/%s: %d, want 404", f, res.StatusCode)
		}
	}
	if res := do(t, "GET", ts.URL+"/fonts/inter-latin-wght-normal.woff2", "", nil); res.StatusCode != 200 {
		t.Errorf("listed font: %d", res.StatusCode)
	}
}

// Presenter messages are relayed only from connections that authenticated.
func TestPresentRelayNeedsAuth(t *testing.T) {
	ts, id := setup(t)
	ctx := context.Background()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/decks/" + id
	dial := func() *websocket.Conn {
		c, _, err := websocket.Dial(ctx, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.CloseNow() })
		c.Read(ctx) // hello
		return c
	}
	viewer, anon, presenter := dial(), dial(), dial()
	send := func(c *websocket.Conn, v any) {
		b, _ := json.Marshal(v)
		c.Write(ctx, websocket.MessageText, b)
	}
	send(anon, map[string]any{"type": "present", "index": 5, "step": 0})
	send(presenter, map[string]any{"type": "auth", "token": token})
	if _, b, _ := presenter.Read(ctx); !strings.Contains(string(b), `"ok":true`) {
		t.Fatalf("auth reply: %s", b)
	}
	send(presenter, map[string]any{"type": "present", "index": 1, "step": 2})
	_, b, err := viewer.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	if m["type"] != "present" || m["index"].(float64) != 1 || m["step"].(float64) != 2 {
		t.Fatalf("viewer got %s (the unauthenticated message must not be relayed)", b)
	}
}

func TestBruteForceLockout(t *testing.T) {
	ts, _ := setup(t)
	for i := 0; i < 10; i++ {
		do(t, "GET", ts.URL+"/api/auth", "guess", nil)
	}
	// Locked out now, even with the right token.
	if res := do(t, "GET", ts.URL+"/api/auth", token, nil); res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("want 429 after 10 failures, got %d", res.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	ts, id := setup(t)
	res, err := http.Get(ts.URL + "/d/" + id)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %s", want, csp)
		}
	}
	if res.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("missing X-Frame-Options")
	}
}
