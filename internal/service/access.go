package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

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

// ---- shared links ----
//
// A deck has two kinds of link: viewer links (/d/<id>?k=...) watch it, and
// presenter links (/d/<id>/present?p=...) present it without signing in.
// A link either carries the deck's key itself (it never expires) or a signed
// token "<expiry in base36>.<HMAC>" keyed by that key, which stops working at
// its expiry. Replacing the key (Make new links) ends both kinds at once.

// LinkKinds are the kinds of shared link.
const (
	LinkView    = "view"
	LinkPresent = "present"
)

// LinkTTLs are the expiry choices offered for shared links (0: never expires).
var LinkTTLs = []time.Duration{0, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 12 * time.Hour, 24 * time.Hour}

// ErrLinkTTL rejects an expiry that isn't one of LinkTTLs.
var ErrLinkTTL = errors.New("pick an expiry of 30 minutes to 24 hours, or none")

func linkSig(key, kind, id string, exp int64) string {
	m := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(m, "slidekick-link|%s|%s|%d", kind, id, exp)
	return hex.EncodeToString(m.Sum(nil)[:12])
}

func linkToken(key, kind, id string, exp time.Time) string {
	e := exp.Unix()
	return strconv.FormatInt(e, 36) + "." + linkSig(key, kind, id, e)
}

// checkLink reports whether k unlocks a link of this kind: the key itself
// (no expiry: exp is zero) or an unexpired token signed with it.
func checkLink(key, kind, id, k string, now time.Time) (ok bool, exp time.Time) {
	if key == "" || k == "" || len(k) > 96 {
		return false, time.Time{}
	}
	if subtle.ConstantTimeCompare([]byte(k), []byte(key)) == 1 {
		return true, time.Time{}
	}
	i := strings.IndexByte(k, '.')
	if i < 1 {
		return false, time.Time{}
	}
	e, err := strconv.ParseInt(k[:i], 36, 64)
	if err != nil || now.Unix() >= e {
		return false, time.Time{}
	}
	if !hmac.Equal([]byte(k[i+1:]), []byte(linkSig(key, kind, id, e))) {
		return false, time.Time{}
	}
	return true, time.Unix(e, 0)
}

// MintLink makes a shared link of kind (LinkView or LinkPresent) that stops
// working after ttl (0: it never expires). A public deck's viewer link is its
// plain address, which needs no key and never expires.
func (s *Service) MintLink(ctx context.Context, id, kind string, ttl time.Duration) (link string, expires time.Time, err error) {
	okTTL := false
	for _, t := range LinkTTLs {
		okTTL = okTTL || t == ttl
	}
	if !okTTL {
		return "", time.Time{}, ErrLinkTTL
	}
	a, err := s.access(ctx, id)
	if err != nil {
		return "", time.Time{}, err
	}
	base := s.LinkBase(ctx) + "/d/" + id
	var key, param string
	switch kind {
	case LinkView:
		if !a.Private || a.ViewKey == "" {
			return base, time.Time{}, nil
		}
		key, param = a.ViewKey, "?k="
	case LinkPresent:
		if a.PresentKey == "" {
			a.PresentKey = randHex(12)
			if err := s.store.SetAccess(ctx, id, a); err != nil {
				return "", time.Time{}, err
			}
		}
		key, param, base = a.PresentKey, "?p=", base+"/present"
	default:
		return "", time.Time{}, fmt.Errorf("unknown link kind %q (want view or present)", kind)
	}
	if ttl == 0 {
		return base + param + url.QueryEscape(key), time.Time{}, nil
	}
	expires = time.Now().Add(ttl).Truncate(time.Second)
	return base + param + linkToken(key, kind, id, expires), expires, nil
}

// SetPrivate makes a deck private (its links then need the view key) or
// public. Making a deck private creates a view key if it has none.
// rotate replaces the view key and the presenter key, which ends every
// viewer and presenter link handed out so far.
func (s *Service) SetPrivate(ctx context.Context, id string, private, rotate bool) (*Access, error) {
	a, err := s.access(ctx, id)
	if err != nil {
		return nil, err
	}
	a.Private = private
	if rotate || (private && a.ViewKey == "") {
		a.ViewKey = randHex(12)
	}
	if rotate && a.PresentKey != "" {
		a.PresentKey = randHex(12)
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
	ok, _ := s.ViewAccess(ctx, id, key)
	return ok
}

// ViewAccess is CanView, plus when that access ends (zero: it doesn't).
func (s *Service) ViewAccess(ctx context.Context, id, key string) (bool, time.Time) {
	a, err := s.store.Access(ctx, id)
	if err != nil {
		return false, time.Time{}
	}
	if !a.Private {
		return true, time.Time{}
	}
	return checkLink(a.ViewKey, LinkView, id, key, time.Now())
}

// PresentAccess reports whether key is a valid presenter link for the deck,
// and when it expires (zero: it doesn't).
func (s *Service) PresentAccess(ctx context.Context, id, key string) (bool, time.Time) {
	a, err := s.store.Access(ctx, id)
	if err != nil {
		return false, time.Time{}
	}
	return checkLink(a.PresentKey, LinkPresent, id, key, time.Now())
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
	u := s.LinkBase(context.Background()) + "/d/" + id
	if a.Private && a.ViewKey != "" {
		u += "?k=" + url.QueryEscape(a.ViewKey)
	}
	return u
}
