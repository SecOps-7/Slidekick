package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// principal is who a request acts as: the server token holder (admin) or the
// holder of one deck's edit token.
type principal struct {
	admin bool
	deck  string
}

func (p principal) can(deckID string) bool { return p.admin || (p.deck != "" && p.deck == deckID) }

func (p principal) scope() string {
	if p.admin {
		return "admin"
	}
	return "deck"
}

type principalKey struct{}

func principalFrom(ctx context.Context) (principal, bool) {
	p, ok := ctx.Value(principalKey{}).(principal)
	return p, ok
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// resolveToken maps a token to a principal. Without a server token the
// server is open and everyone is admin.
func (s *server) resolveToken(ctx context.Context, tok string) (principal, bool) {
	if s.opt.Token == "" {
		return principal{admin: true}, true
	}
	if tok == "" {
		return principal{}, false
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.opt.Token)) == 1 {
		return principal{admin: true}, true
	}
	if id, ok := s.svc.DeckForToken(ctx, tok); ok {
		return principal{deck: id}, true
	}
	return principal{}, false
}

// auth requires a valid token (server or deck) and records the principal.
// Failures count towards the per-IP lockout.
func (s *server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opt.Token == "" {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{admin: true})))
			return
		}
		ip := clientIP(r)
		if !s.fails.allowed(ip) {
			w.Header().Set("Retry-After", "300")
			http.Error(w, "too many failed attempts; try again in a few minutes", http.StatusTooManyRequests)
			return
		}
		p, ok := s.resolveToken(r.Context(), bearer(r))
		if !ok {
			s.fails.fail(ip)
			w.Header().Set("WWW-Authenticate", `Bearer realm="slidekick"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.fails.succeed(ip)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

// admin requires the server token.
func (s *server) admin(next http.Handler) http.Handler {
	return s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, _ := principalFrom(r.Context()); !p.admin {
			http.Error(w, "this needs the server token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// deckAuth requires a token that may edit the deck named in the path.
func (s *server) deckAuth(next http.Handler) http.Handler {
	return s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, _ := principalFrom(r.Context()); !p.can(r.PathValue("id")) {
			http.Error(w, "this token can't edit that deck", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// viewable reports whether the request may watch a deck: the deck is public,
// the request carries its view key (?k=), or a token that may edit it.
// Private decks answer 404 otherwise, so their ids can't be probed.
func (s *server) viewable(r *http.Request, id string) bool {
	if s.svc.CanView(r.Context(), id, r.URL.Query().Get("k")) {
		return true
	}
	tok := bearer(r)
	if tok == "" {
		return false
	}
	ip := clientIP(r)
	if !s.fails.allowed(ip) {
		return false
	}
	p, ok := s.resolveToken(r.Context(), tok)
	if !ok {
		s.fails.fail(ip)
		return false
	}
	return p.can(id)
}

func (s *server) viewGate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.viewable(r, r.PathValue("id")) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "deck not found"})
			return
		}
		next(w, r)
	}
}

// mcpAuth hands the principal to the MCP SDK as TokenInfo, so tools can
// scope themselves and sessions can't be reused under another token.
func (s *server) mcpAuth(next http.Handler) http.Handler {
	if s.opt.Token == "" {
		return next
	}
	verify := func(ctx context.Context, tok string, _ *http.Request) (*auth.TokenInfo, error) {
		p, ok := principalFrom(ctx)
		if !ok {
			return nil, auth.ErrInvalidToken
		}
		ti := &auth.TokenInfo{Expiration: time.Now().Add(time.Hour), UserID: "admin", Extra: map[string]any{}}
		if !p.admin {
			ti.UserID = "deck:" + p.deck
			ti.Extra["deck"] = p.deck
		}
		return ti, nil
	}
	return s.auth(auth.RequireBearerToken(verify, nil)(next))
}

func (s *server) authInfo(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	writeJSON(w, 200, map[string]any{"ok": true, "required": s.opt.Token != "", "scope": p.scope(), "deck": p.deck})
}

func (s *server) getAccess(w http.ResponseWriter, r *http.Request) {
	a, err := s.svc.GetAccess(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *server) setAccess(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Private bool `json:"private"`
		Rotate  bool `json:"rotate"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, err)
		return
	}
	a, err := s.svc.SetPrivate(r.Context(), r.PathValue("id"), body.Private, body.Rotate)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *server) issueEditToken(w http.ResponseWriter, r *http.Request) {
	tok, err := s.svc.IssueEditToken(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"token": tok})
}

func (s *server) revokeEditToken(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RevokeEditToken(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
