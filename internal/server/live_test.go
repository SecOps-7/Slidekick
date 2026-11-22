package server_test

import (
	"context"
	"net/url"
	"testing"
)

func TestLiveUpdatesWhoMay(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	owner := e.addUser(t, admin, "edna", "editor")
	other := e.addUser(t, admin, "eddie", "editor")
	slides := []any{map[string]any{"layout": "live", "regions": map[string]any{
		"title":  []any{map[string]any{"type": "heading", "props": map[string]any{"text": "Home"}}},
		"figure": []any{map[string]any{"id": "e_temp", "type": "stat", "props": map[string]any{"value": 20, "suffix": " °C"}}},
	}}}
	code, out := owner.do("POST", "/api/decks", map[string]any{"title": "Live", "slides": slides})
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	id := out["deckId"].(string)
	_, deckOut := owner.do("GET", "/api/decks/"+id, nil)
	sid := deckOut["deck"].(map[string]any)["slides"].([]any)[0].(map[string]any)["id"].(string)
	body := map[string]any{"slide_id": sid, "element_id": "e_temp", "value": 21.5}
	path := "/api/decks/" + id + "/live"

	// Off by default: even the owner is refused, with a reason.
	if code, out := owner.do("POST", path, body); code != 403 || out["error"] == nil {
		t.Fatalf("switched off: %d %v", code, out)
	}
	if code, _ := owner.do("PUT", "/api/admin/settings", map[string]any{"liveSlides": true}); code != 403 {
		t.Fatalf("an editor turned it on: %d", code)
	}
	if code, out := admin.do("PUT", "/api/admin/settings", map[string]any{"liveSlides": true}); code != 200 || out["liveSlides"] != true {
		t.Fatalf("enable: %d %v", code, out)
	}

	// The owner and admins may; other editors, deck tokens and links may not.
	if code, out := owner.do("POST", path, body); code != 200 || out["elementId"] != "e_temp" {
		t.Fatalf("owner: %d %v", code, out)
	}
	if code, _ := admin.do("POST", path, map[string]any{"slide_id": sid, "element_id": "e_temp", "value": 22}); code != 200 {
		t.Fatalf("admin: %d", code)
	}
	if code, _ := other.do("POST", path, body); code != 403 {
		t.Fatalf("other editor: %d", code)
	}
	tok, err := e.env.Svc.IssueEditToken(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	deckTok := e.client(t)
	deckTok.bear = tok
	if code, _ := deckTok.do("POST", path, body); code != 403 {
		t.Fatalf("deck token: %d", code)
	}
	viewKey := e.env.Svc.ViewKey(context.Background(), id)
	anon := e.client(t)
	if code, _ := anon.do("POST", path+"?k="+url.QueryEscape(viewKey), body); code != 401 {
		t.Fatalf("viewer link: %d", code)
	}
	_, link := owner.do("POST", "/api/decks/"+id+"/links", map[string]any{"kind": "present"})
	pu, _ := url.Parse(link["url"].(string))
	if code, _ := anon.do("POST", path+"?p="+url.QueryEscape(pu.Query().Get("p")), body); code != 403 {
		t.Fatalf("presenter link: %d", code)
	}

	// Screens that open the deck get the last values; viewer links can read them.
	code, out = anon.do("GET", "/api/decks/"+id+"?k="+url.QueryEscape(viewKey), nil)
	live, _ := out["live"].(map[string]any)
	if code != 200 || live[sid] == nil {
		t.Fatalf("deck with live content: %d %v", code, out["live"])
	}
	// Bad values are refused with a reason; switching it off refuses again.
	if code, _ := owner.do("POST", path, map[string]any{"slide_id": sid, "element_id": "e_temp", "value": "warm"}); code != 422 {
		t.Fatalf("bad value: %d", code)
	}
	admin.do("PUT", "/api/admin/settings", map[string]any{"liveSlides": false})
	if code, _ := owner.do("POST", path, body); code != 403 {
		t.Fatalf("switched off again: %d", code)
	}
	if code, out = anon.do("GET", "/api/decks/"+id+"?k="+url.QueryEscape(viewKey), nil); out["live"].(map[string]any)[sid] == nil {
		t.Fatal("last values gone while switched off")
	}
}
