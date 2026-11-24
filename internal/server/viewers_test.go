package server_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The presenter view sees how many devices watch the deck (a cast TV counts
// as one); viewers never learn the audience size.
func TestViewerCount(t *testing.T) {
	e, _ := newPresentServer(t)
	admin := e.withAdmin(t)
	owner := e.addUser(t, admin, "edna", "editor")
	_, out := owner.do("POST", "/api/decks", map[string]any{"title": "Talk", "slides": notedSlides("Welcome.")})
	id := out["deckId"].(string)
	_, out = owner.do("POST", "/api/decks/"+id+"/links", map[string]any{"kind": "view"})
	vk := url.QueryEscape(linkParam(t, out["url"].(string), "k"))
	_, out = owner.do("POST", "/api/decks/"+id+"/links", map[string]any{"kind": "present"})
	pk := url.QueryEscape(linkParam(t, out["url"].(string), "p"))

	ctx := context.Background()
	wsBase := "ws" + strings.TrimPrefix(e.ts.URL, "http") + "/ws/decks/" + id
	dial := func(q string) (*websocket.Conn, map[string]any) {
		t.Helper()
		c, _, err := websocket.Dial(ctx, wsBase+q, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.CloseNow() })
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		hello := map[string]any{}
		json.Unmarshal(b, &hello)
		return c, hello
	}
	// count reads c until a viewer count arrives (ok false when none comes in time).
	count := func(c *websocket.Conn, wait time.Duration) (n float64, ok bool) {
		rctx, done := context.WithTimeout(ctx, wait)
		defer done()
		for {
			_, b, err := c.Read(rctx)
			if err != nil {
				return 0, false
			}
			m := map[string]any{}
			json.Unmarshal(b, &m)
			if m["type"] == "viewers" {
				return m["count"].(float64), true
			}
		}
	}

	pres, hello := dial("?p=" + pk)
	if hello["viewers"] != float64(0) {
		t.Fatalf("presenter hello: %v", hello)
	}
	viewer, hello := dial("?k=" + vk + "&as=viewer&dev=device-aaaa")
	if _, has := hello["viewers"]; has {
		t.Fatalf("a viewer learned the count: %v", hello)
	}
	if n, _ := count(pres, 3*time.Second); n != 1 {
		t.Fatalf("after one viewer: %v", n)
	}
	dial("?k=" + vk + "&as=viewer&dev=device-aaaa") // a second tab on the same device
	dial("?k=" + vk + "&as=viewer&dev=device-bbbb")
	if n, _ := count(pres, 3*time.Second); n != 2 {
		t.Fatalf("two devices, three tabs: %v", n)
	}
	tv, _ := dial("?k=" + vk + "&as=cast")
	if n, _ := count(pres, 3*time.Second); n != 3 {
		t.Fatalf("with the cast TV: %v", n)
	}
	dial("?k=" + vk) // a page that doesn't say it's a viewer (the editor) doesn't count
	if _, late := dial("?p=" + pk); late["viewers"] != float64(3) {
		t.Fatalf("a presenter joining late: %v", late)
	}
	tv.Close(websocket.StatusNormalClosure, "")
	if n, _ := count(pres, 3*time.Second); n != 2 {
		t.Fatalf("after the TV left: %v", n)
	}
	if _, got := count(viewer, 500*time.Millisecond); got {
		t.Fatal("a viewer received the count")
	}
}
