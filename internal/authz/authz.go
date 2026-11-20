// Package authz decides who may do what. Every entry point (REST routes, the
// WebSocket, MCP tools and the AI chat) asks Policy.Can; the UI only mirrors
// these answers, it never enforces them.
//
// Roles:
//
//	admin   everything: all decks, users, sign-in providers, AI settings
//	editor  creates decks; edits, exports and shares the decks they own (or every
//	        deck when the "editors can edit all decks" setting is on); uploads;
//	        uses AI; views, presents and comments on every deck
//	viewer  views, presents and comments on every deck
//
// Every deck has an owner (the user who created it). Signed-in users can see
// every deck on the instance, as a team library; changing a deck needs
// ownership, the admin role, or the instance-wide setting.
package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

// Roles in order of power, for the UI.
var Roles = []Role{RoleAdmin, RoleEditor, RoleViewer}

func ParseRole(s string) (Role, error) {
	switch r := Role(strings.ToLower(strings.TrimSpace(s))); r {
	case RoleAdmin, RoleEditor, RoleViewer:
		return r, nil
	}
	return "", fmt.Errorf("unknown role %q (want admin, editor or viewer)", s)
}

// Kind is how a request authenticated.
type Kind string

const (
	KindAnon   Kind = ""       // no credentials
	KindServer Kind = "server" // SLIDEKICK_TOKEN: full access, for automation and recovery
	KindUser   Kind = "user"   // session cookie or a personal API token (skp_)
	KindDeck   Kind = "deck"   // a deck edit token (ske_): one deck only
	KindOpen   Kind = "open"   // no token and no accounts yet: legacy open mode
)

type Principal struct {
	Kind     Kind
	UserID   string
	Username string
	Role     Role
	Deck     string // KindDeck: the only deck it may touch
}

func (p Principal) Authenticated() bool { return p.Kind != KindAnon }

// Actor is how this principal's writes appear in deck history.
func (p Principal) Actor() string {
	switch p.Kind {
	case KindUser:
		return "user:" + p.Username
	case KindDeck:
		return "deck-token"
	}
	return "agent"
}

type Action string

const (
	View           Action = "view"
	Present        Action = "present"
	Comment        Action = "comment"
	Edit           Action = "edit"
	Export         Action = "export"
	Share          Action = "share"      // private toggle and view key
	MintDeckToken  Action = "deck-token" // create or revoke a deck's edit token
	Create         Action = "create"
	Upload         Action = "upload"
	UseAI          Action = "ai"
	ManageUsers    Action = "manage-users"
	ManageSettings Action = "manage-settings" // sign-in providers, AI providers, instance settings
	ListDecks      Action = "list"
)

// Deck is what a decision needs to know about a deck.
type Deck struct{ ID, Owner string }

type Policy struct {
	// EditorsEditAll reports the instance setting that lets editors change
	// every deck, not only their own. Nil means off.
	EditorsEditAll func() bool
}

var ErrForbidden = errors.New("forbidden")

// Can reports whether p may perform a on d (d is nil for actions that aren't
// about one deck).
func (pol Policy) Can(p Principal, a Action, d *Deck) bool {
	switch p.Kind {
	case KindServer, KindOpen:
		return true
	case KindDeck:
		if a == Upload || a == ListDecks { // the list is filtered to its deck
			return true
		}
		if d == nil || d.ID != p.Deck {
			return false
		}
		switch a {
		case View, Present, Comment, Edit, Export, Share:
			return true
		}
		return false
	case KindUser:
	default:
		return false
	}
	switch p.Role {
	case RoleAdmin:
		return true
	case RoleEditor:
		switch a {
		case View, Present, Comment, Export, Create, Upload, UseAI, ListDecks:
			return true
		case Edit, Share, MintDeckToken:
			if d == nil {
				return false
			}
			return (d.Owner != "" && d.Owner == p.UserID) || (pol.EditorsEditAll != nil && pol.EditorsEditAll())
		}
	case RoleViewer:
		switch a {
		case View, Present, Comment, ListDecks:
			return true
		}
	}
	return false
}

// ---- carrying principals ----

type ctxKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func FromContext(ctx context.Context) Principal {
	p, _ := ctx.Value(ctxKey{}).(Principal)
	return p
}

// TokenInfo packs a principal for the MCP SDK, which hands it to every tool
// call and binds MCP sessions to it (a session can't be reused by another
// principal).
func TokenInfo(p Principal) *auth.TokenInfo {
	ti := &auth.TokenInfo{UserID: string(p.Kind) + ":" + p.UserID + ":" + p.Deck, Extra: map[string]any{
		"kind": string(p.Kind), "user": p.UserID, "username": p.Username, "role": string(p.Role), "deck": p.Deck,
	}}
	return ti
}

// FromTokenInfo unpacks TokenInfo. Nil TokenInfo means a trusted local
// transport (stdio), which acts with full access as before.
func FromTokenInfo(ti *auth.TokenInfo) Principal {
	if ti == nil {
		return Principal{Kind: KindServer}
	}
	s := func(k string) string { v, _ := ti.Extra[k].(string); return v }
	return Principal{Kind: Kind(s("kind")), UserID: s("user"), Username: s("username"), Role: Role(s("role")), Deck: s("deck")}
}
