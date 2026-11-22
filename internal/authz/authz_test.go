package authz

import "testing"

func TestPolicy(t *testing.T) {
	editAll := false
	pol := Policy{EditorsEditAll: func() bool { return editAll }}
	admin := Principal{Kind: KindUser, UserID: "a", Role: RoleAdmin}
	ed := Principal{Kind: KindUser, UserID: "e", Role: RoleEditor}
	other := Principal{Kind: KindUser, UserID: "o", Role: RoleEditor}
	view := Principal{Kind: KindUser, UserID: "v", Role: RoleViewer}
	deckTok := Principal{Kind: KindDeck, Deck: "d1"}
	anon := Principal{}
	mine := &Deck{ID: "d1", Owner: "e"}
	unowned := &Deck{ID: "d2"}

	type c struct {
		name string
		p    Principal
		a    Action
		d    *Deck
		want bool
	}
	cases := []c{
		{"admin edits any", admin, Edit, unowned, true},
		{"admin manages users", admin, ManageUsers, nil, true},
		{"server token", Principal{Kind: KindServer}, ManageSettings, nil, true},
		{"editor edits own", ed, Edit, mine, true},
		{"editor shares own", ed, Share, mine, true},
		{"editor edits other's", other, Edit, mine, false},
		{"editor edits unowned", ed, Edit, unowned, false},
		{"editor views other's", other, View, mine, true},
		{"editor creates", ed, Create, nil, true},
		{"editor uses AI", ed, UseAI, nil, true},
		{"editor manages users", ed, ManageUsers, nil, false},
		{"editor manages settings", ed, ManageSettings, nil, false},
		{"viewer views", view, View, mine, true},
		{"viewer presents", view, Present, mine, true},
		{"viewer comments", view, Comment, mine, true},
		{"viewer edits", view, Edit, mine, false},
		{"viewer creates", view, Create, nil, false},
		{"viewer uploads", view, Upload, nil, false},
		{"viewer exports", view, Export, mine, false},
		{"viewer uses AI", view, UseAI, nil, false},
		{"deck token own deck", deckTok, Edit, mine, true},
		{"deck token other deck", deckTok, Edit, unowned, false},
		{"deck token creates", deckTok, Create, nil, false},
		{"deck token mints tokens", deckTok, MintDeckToken, mine, false},
		{"deck token uses AI", deckTok, UseAI, mine, false},
		{"anonymous", anon, View, mine, false},
		{"unknown role", Principal{Kind: KindUser, Role: "root"}, View, mine, false},
		{"owner deletes", ed, Delete, mine, true},
		{"other editor deletes", other, Delete, mine, false},
		{"viewer deletes", view, Delete, mine, false},
		{"deck token deletes", deckTok, Delete, mine, false},
		{"admin deletes", admin, Delete, unowned, true},
		{"owner updates live", ed, LiveUpdate, mine, true},
		{"admin updates live", admin, LiveUpdate, unowned, true},
		{"server token updates live", Principal{Kind: KindServer}, LiveUpdate, mine, true},
		{"other editor updates live", other, LiveUpdate, mine, false},
		{"viewer updates live", view, LiveUpdate, mine, false},
		{"deck token updates live", deckTok, LiveUpdate, mine, false},
		{"presenter link updates live", Principal{Kind: KindPresenter, Deck: "d1"}, LiveUpdate, mine, false},
		{"anonymous updates live", anon, LiveUpdate, mine, false},
	}
	for _, tc := range cases {
		if got := pol.Can(tc.p, tc.a, tc.d); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
	editAll = true
	if !pol.Can(other, Edit, mine) || pol.Can(view, Edit, mine) {
		t.Error("editors-edit-all setting")
	}
	if pol.Can(other, Delete, mine) {
		t.Error("editors-edit-all must not allow deleting others' decks")
	}
	if pol.Can(other, LiveUpdate, mine) {
		t.Error("editors-edit-all must not let other editors update live slides")
	}
}

func TestTokenInfoRoundTrip(t *testing.T) {
	p := Principal{Kind: KindUser, UserID: "u1", Username: "ann", Role: RoleEditor}
	if got := FromTokenInfo(TokenInfo(p)); got != p {
		t.Fatalf("round trip: %+v", got)
	}
	if FromTokenInfo(nil).Kind != KindServer {
		t.Fatal("stdio should be trusted")
	}
	if a, b := TokenInfo(p).UserID, TokenInfo(Principal{Kind: KindDeck, Deck: "d"}).UserID; a == b {
		t.Fatal("different principals share a session id")
	}
}
