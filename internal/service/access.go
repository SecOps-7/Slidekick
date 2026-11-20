package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/SecOps-7/slidekick/internal/store"
)

// EditTokenPrefix starts every deck-scoped edit token, so the server can tell
// them from the server-wide token without a database lookup.
const EditTokenPrefix = "ske_"

// Access is a deck's sharing state as shown to people who may edit it.
type Access struct {
	Private      bool   `json:"private" jsonschema:"true when viewers need the view key"`
	ViewKey      string `json:"viewKey,omitempty" jsonschema:"key a private deck's links carry as ?k="`
	HasEditToken bool   `json:"hasEditToken" jsonschema:"a deck-scoped edit token exists"`
	ShareURL     string `json:"shareUrl" jsonschema:"viewer link (includes the key for private decks)"`
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func (s *Service) access(ctx context.Context, id string) (store.Access, error) {
	if _, _, err := s.store.Get(ctx, id); err != nil {
		return store.Access{}, err
	}
	return s.store.Access(ctx, id)
}

func (s *Service) view(id string, a store.Access) Access {
	return Access{Private: a.Private, ViewKey: a.ViewKey, HasEditToken: a.EditHash != "", ShareURL: s.shareURL(id, a)}
}

// GetAccess returns a deck's sharing state.
func (s *Service) GetAccess(ctx context.Context, id string) (*Access, error) {
	a, err := s.access(ctx, id)
	if err != nil {
		return nil, err
	}
	out := s.view(id, a)
	return &out, nil
}

// SetPrivate makes a deck private (its links then need the view key) or
// public. Making a deck private creates a view key if it has none.
// rotate replaces the view key, which breaks every link handed out so far.
func (s *Service) SetPrivate(ctx context.Context, id string, private, rotate bool) (*Access, error) {
	a, err := s.access(ctx, id)
	if err != nil {
		return nil, err
	}
	a.Private = private
	if rotate || (private && a.ViewKey == "") {
		a.ViewKey = randHex(12)
	}
	if err := s.store.SetAccess(ctx, id, a); err != nil {
		return nil, err
	}
	s.Publish(id, map[string]any{"type": "access"}) // live viewers re-check their key
	out := s.view(id, a)
	return &out, nil
}

// IssueEditToken creates a token that can edit, present and export this one
// deck (and nothing else), replacing any earlier one. It is shown only once:
// only its hash is stored.
func (s *Service) IssueEditToken(ctx context.Context, id string) (string, error) {
	a, err := s.access(ctx, id)
	if err != nil {
		return "", err
	}
	tok := EditTokenPrefix + randHex(20)
	a.EditHash = hashToken(tok)
	return tok, s.store.SetAccess(ctx, id, a)
}

// RevokeEditToken removes a deck's edit token.
func (s *Service) RevokeEditToken(ctx context.Context, id string) error {
	a, err := s.access(ctx, id)
	if err != nil {
		return err
	}
	a.EditHash = ""
	return s.store.SetAccess(ctx, id, a)
}

// DeckForToken returns the deck an edit token belongs to.
func (s *Service) DeckForToken(ctx context.Context, tok string) (string, bool) {
	if !strings.HasPrefix(tok, EditTokenPrefix) || len(tok) > 128 {
		return "", false
	}
	id, err := s.store.DeckForEditHash(ctx, hashToken(tok))
	return id, err == nil
}

// CanView reports whether key unlocks viewing the deck. Public decks need no key.
func (s *Service) CanView(ctx context.Context, id, key string) bool {
	a, err := s.store.Access(ctx, id)
	if err != nil {
		return false
	}
	if !a.Private {
		return true
	}
	return key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(a.ViewKey)) == 1
}

// ViewKey is the key a private deck's links need ("" for public decks).
func (s *Service) ViewKey(ctx context.Context, id string) string {
	a, err := s.store.Access(ctx, id)
	if err != nil || !a.Private {
		return ""
	}
	return a.ViewKey
}

func (s *Service) shareURL(id string, a store.Access) string {
	u := s.BaseURL + "/d/" + id
	if a.Private && a.ViewKey != "" {
		u += "?k=" + url.QueryEscape(a.ViewKey)
	}
	return u
}
