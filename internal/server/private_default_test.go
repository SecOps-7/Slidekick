package server_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
)

// Decision 2026-11-20: new decks are private; decks that existed before stay
// public and keep their links.
func TestNewDecksArePrivate(t *testing.T) {
	e := newAccServer(t, token)
	admin := e.withAdmin(t)
	view := e.addUser(t, admin, "vera", "viewer")
	anon := e.client(t)
	ctx := context.Background()

	// Created three ways: the editor UI (REST), an agent (MCP), and the service directly.
	_, out := admin.do("POST", "/api/decks", map[string]any{"title": "From the UI", "slides": oneSlide})
	fromUI := out["deckId"].(string)
	tr := &mcp.StreamableClientTransport{Endpoint: e.ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerRT{token}}}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "create_deck", Arguments: map[string]any{"title": "From an agent", "slides": oneSlide}})
	if err != nil || res.IsError {
		t.Fatalf("create_deck: %v", err)
	}
	created, _ := res.StructuredContent.(map[string]any)
	fromAgent, _ := created["deckId"].(string)
	agentURL, _ := created["url"].(string)
	r, _ := e.env.Svc.Create(ctx, "From the service", "", "", oneSlide, "agent")

	for _, id := range []string{fromUI, fromAgent, r.DeckID} {
		a, err := e.env.Svc.GetAccess(ctx, id)
		if err != nil || !a.Private || len(a.ViewKey) < 20 || !strings.Contains(a.ShareURL, "?k=") {
			t.Fatalf("%s: new deck not private: %+v %v", id, a, err)
		}
		if code, _ := anon.do("GET", "/api/decks/"+id, nil); code != 404 {
			t.Fatalf("%s: anonymous without key: %d", id, code)
		}
		if code, _ := anon.do("GET", "/api/decks/"+id+"?k="+url.QueryEscape(a.ViewKey), nil); code != 200 {
			t.Fatalf("%s: anonymous with the private link: %d", id, code)
		}
		if code, _ := view.do("GET", "/api/decks/"+id, nil); code != 200 {
			t.Fatalf("%s: signed-in viewer: %d", id, code)
		}
	}
	// The link an agent hands out is the private one.
	if !strings.Contains(agentURL, "?k=") {
		t.Fatalf("create_deck returned a public link: %q", agentURL)
	}
}

func TestExistingPublicDeckUnchanged(t *testing.T) {
	e := newAccServer(t, token)
	admin := e.withAdmin(t)
	ctx := context.Background()
	old := legacyDeck(t, e.env, "Before the change")
	linkBefore := e.env.Svc.ShareURL(old)
	if strings.Contains(linkBefore, "?k=") {
		t.Fatalf("existing deck got a private link: %q", linkBefore)
	}
	// New decks being private, and edits to the old one, don't touch it.
	admin.do("POST", "/api/decks", map[string]any{"title": "New", "slides": oneSlide})
	if code, out := admin.do("POST", "/api/decks/"+old+"/patch", patchTitle("Edited later")); code != 200 {
		t.Fatalf("edit old deck: %d %v", code, out)
	}
	a, _ := e.env.Svc.GetAccess(ctx, old)
	if a.Private || e.env.Svc.ShareURL(old) != linkBefore {
		t.Fatalf("existing deck changed: %+v", a)
	}
	if code, _ := e.client(t).do("GET", "/api/decks/"+old, nil); code != 200 {
		t.Fatalf("existing public link: %d", code)
	}
}
