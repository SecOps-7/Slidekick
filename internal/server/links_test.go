package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// anon makes a request with no cookie or token: what someone holding only a
// shared link does.
func anon(t *testing.T, method, u string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, u, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out := map[string]any{}
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// linkParam pulls the k= or p= value out of a shared link.
func linkParam(t *testing.T, link, name string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(name)
}

func TestSharedLinks(t *testing.T) {
	e, _ := newPresentServer(t)
	admin := e.withAdmin(t)
	owner := e.addUser(t, admin, "edna", "editor")
	view := e.addUser(t, admin, "vera", "viewer")
	_, out := owner.do("POST", "/api/decks", map[string]any{"title": "Talk", "slides": notedSlides("Welcome.", "Revenue grew 12%.")})
	id := out["deckId"].(string)
	_, out = owner.do("POST", "/api/decks", map[string]any{"title": "Other", "slides": notedSlides("Hi.")})
	otherID := out["deckId"].(string)
	base := e.ts.URL + "/api/decks/" + id

	// Only people who may share the deck make links, with a listed expiry.
	if code, _ := view.do("POST", "/api/decks/"+id+"/links", map[string]any{"kind": "view"}); code != 403 {
		t.Fatalf("viewer minting: %d", code)
	}
	if code, _ := owner.do("POST", "/api/decks/"+id+"/links", map[string]any{"kind": "view", "minutes": 45}); code != 422 {
		t.Fatalf("45 minutes: %d", code)
	}
	if code, _ := owner.do("POST", "/api/decks/"+id+"/links", map[string]any{"kind": "edit"}); code != 422 {
		t.Fatalf("unknown kind: %d", code)
	}

	// A viewer link without expiry carries the deck's key; with one, a token.
	_, out = owner.do("POST", "/api/decks/"+id+"/links", map[string]any{"kind": "view"})
	if out["expiresAt"] != nil || linkParam(t, out["url"].(string), "k") != e.env.Svc.ViewKey(context.Background(), id) {
		t.Fatalf("no-expiry viewer link: %v", out)
	}
	_, out = owner.do("POST", "/api/decks/"+id+"/links", map[string]any{"kind": "view", "minutes": 60})
	vk := linkParam(t, out["url"].(string), "k")
	if exp, _ := time.Parse(time.RFC3339, out["expiresAt"].(string)); time.Until(exp) < 59*time.Minute || time.Until(exp) > 61*time.Minute {
		t.Fatalf("1 hour viewer link expires %v", out["expiresAt"])
	}
	if code, _ := anon(t, "GET", base+"?k="+url.QueryEscape(vk), nil); code != 200 {
		t.Fatalf("view with expiring link: %d", code)
	}
	if code, _ := anon(t, "GET", e.ts.URL+"/api/decks/"+otherID+"?k="+url.QueryEscape(vk), nil); code != 404 {
		t.Fatalf("a viewer link opens another deck: %d", code)
	}
	// A viewer link can't present.
	if code, _ := anon(t, "POST", base+"/present?k="+url.QueryEscape(vk), map[string]any{}); code != 401 {
		t.Fatalf("viewer link starting the AI voice: %d", code)
	}

	// A presenter link presents this deck, with no account...
	_, out = owner.do("POST", "/api/decks/"+id+"/links", map[string]any{"kind": "present", "minutes": 30})
	if !strings.Contains(out["url"].(string), "/d/"+id+"/present?p=") {
		t.Fatalf("presenter link: %v", out)
	}
	pk := linkParam(t, out["url"].(string), "p")
	q := "?p=" + url.QueryEscape(pk)
	if code, _ := anon(t, "GET", base+q, nil); code != 200 {
		t.Fatalf("presenter link viewing: %d", code)
	}
	if code, out := anon(t, "POST", base+"/present"+q, map[string]any{}); code != 200 || out["running"] != true || out["actor"] != "presenter-link" {
		t.Fatalf("presenter link starting the AI voice: %d %v", code, out)
	}
	if code, out := anon(t, "DELETE", base+"/present"+q, nil); code != 200 || out["stopped"] != true {
		t.Fatalf("presenter link stopping: %d %v", code, out)
	}
	// ...and nothing else: no editing, sharing or other decks.
	if code, _ := anon(t, "POST", base+"/patch"+q, patchTitle("Hijacked")); code != 403 {
		t.Fatalf("presenter link editing: %d", code)
	}
	if code, _ := anon(t, "POST", base+"/access"+q, map[string]any{"private": false}); code != 403 {
		t.Fatalf("presenter link changing access: %d", code)
	}
	if code, _ := anon(t, "POST", base+"/links"+q, map[string]any{"kind": "present"}); code != 403 {
		t.Fatalf("presenter link minting links: %d", code)
	}
	if code, _ := anon(t, "POST", e.ts.URL+"/api/decks/"+otherID+"/present"+q, map[string]any{}); code != 401 {
		t.Fatalf("presenter link on another deck: %d", code)
	}

	// A presenter link drives everyone watching over the live connection.
	ctx := context.Background()
	wsBase := "ws" + strings.TrimPrefix(e.ts.URL, "http") + "/ws/decks/" + id
	watcher, _, err := websocket.Dial(ctx, wsBase+"?k="+url.QueryEscape(vk), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.CloseNow()
	watcher.Read(ctx) // hello
	pres, _, err := websocket.Dial(ctx, wsBase+q, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pres.CloseNow()
	pres.Read(ctx) // hello
	pres.Write(ctx, websocket.MessageText, []byte(`{"type":"present","index":1,"step":0}`))
	rctx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	for {
		_, b, err := watcher.Read(rctx)
		if err != nil {
			t.Fatalf("the watcher never saw the presenter's move: %v", err)
		}
		if strings.Contains(string(b), `"type":"present"`) && strings.Contains(string(b), `"index":1`) {
			break
		}
	}

	// Make new links ends every link handed out so far, both kinds.
	if code, _ := owner.do("POST", "/api/decks/"+id+"/access", map[string]any{"private": true, "rotate": true}); code != 200 {
		t.Fatalf("rotate: %d", code)
	}
	if code, _ := anon(t, "GET", base+q, nil); code != 401 && code != 404 {
		t.Fatalf("old presenter link after new links: %d", code)
	}
	if code, _ := anon(t, "GET", base+"?k="+url.QueryEscape(vk), nil); code != 404 {
		t.Fatalf("old viewer link after new links: %d", code)
	}
	// The presenter's live connection is closed too.
	cctx, cdone := context.WithTimeout(ctx, 3*time.Second)
	defer cdone()
	var cerr error
	for cerr == nil {
		_, _, cerr = pres.Read(cctx)
	}
	if websocket.CloseStatus(cerr) != websocket.StatusPolicyViolation {
		t.Fatalf("presenter connection after new links: %v", cerr)
	}
}
