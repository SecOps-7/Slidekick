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
	ts, id := setup(t)
	base := ts.URL + "/api/decks/" + id
	writes := []struct{ method, url string }{
		{"GET", ts.URL + "/api/auth"},
		{"POST", base + "/patch"},
		{"POST", base + "/restore"},
		{"POST", base + "/comments"},
		{"POST", base + "/comments/c_x/resolve"},
		{"POST", ts.URL + "/api/files"},
		{"POST", ts.URL + "/mcp"},
	}
	for _, w := range writes {
		if res := do(t, w.method, w.url, "", nil); res.StatusCode != 401 {
			t.Errorf("%s %s without token: %d", w.method, w.url, res.StatusCode)
		}
		if res := do(t, w.method, w.url, "wrong", nil); res.StatusCode != 401 {
			t.Errorf("%s %s wrong token: %d", w.method, w.url, res.StatusCode)
		}
	}
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
