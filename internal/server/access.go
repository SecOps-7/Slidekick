package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/SecOps-7/slidekick/internal/accounts"
	"github.com/SecOps-7/slidekick/internal/authz"
	"github.com/SecOps-7/slidekick/internal/store"
)

// How a request proves who it is, in order:
//
//	Authorization: Bearer <SLIDEKICK_TOKEN>   server token: full access (automation, recovery)
//	Authorization: Bearer skp_...             personal API token: acts as that user, with their role
//	Authorization: Bearer ske_...             deck edit token: one deck only
//	Cookie: sk_session=...                    signed-in browser (writes also need X-CSRF-Token)
//	nothing                                   anonymous: only public or keyed deck links
//
// With no server token and no accounts yet, the server runs in the legacy
// open mode (everyone is admin) until the first admin is created.

const (
	sessionCookie = "sk_session"
	ssoCookie     = "sk_sso"
	csrfHeader    = "X-CSRF-Token"
)

type reqAuth struct {
	p       authz.Principal
	user    *store.User
	session string // session cookie value, when signed in by cookie
	csrf    string
	until   time.Time // a presenter link's expiry (zero: none)
}

type authKey struct{}

func authFrom(ctx context.Context) reqAuth {
	a, _ := ctx.Value(authKey{}).(reqAuth)
	return a
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *server) openMode(ctx context.Context) bool {
	return s.opt.Token == "" && (s.acc == nil || s.acc.SetupNeeded(ctx))
}

// resolveToken maps a bearer token to a principal.
func (s *server) resolveToken(ctx context.Context, tok string) (reqAuth, bool) {
	if tok == "" {
		return reqAuth{}, false
	}
	if s.opt.Token != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.opt.Token)) == 1 {
		return reqAuth{p: authz.Principal{Kind: authz.KindServer, Username: "server-token"}}, true
	}
	if s.acc != nil {
		if u, ok := s.acc.APITokenUser(ctx, tok); ok {
			return reqAuth{p: accounts.Principal(u), user: u}, true
		}
	}
	if id, ok := s.svc.DeckForToken(ctx, tok); ok {
		return reqAuth{p: authz.Principal{Kind: authz.KindDeck, Deck: id}}, true
	}
	return reqAuth{}, false
}

// identify works out who is calling. bad means credentials were offered but
// didn't check out (counts toward the lockout).
func (s *server) identify(r *http.Request) (a reqAuth, bad bool) {
	ctx := r.Context()
	if tok := bearer(r); tok != "" {
		a, ok := s.resolveToken(ctx, tok)
		return a, !ok
	}
	var signedIn *reqAuth
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" && s.acc != nil {
		if u, ss, err := s.acc.SessionUser(ctx, c.Value); err == nil {
			signedIn = &reqAuth{p: accounts.Principal(u), user: u, session: c.Value, csrf: ss.CSRF}
		}
	}
	// A presenter link (?p=) presents the deck in the path. Someone signed in
	// who may already drive it keeps their own identity.
	if pk := r.URL.Query().Get("p"); pk != "" {
		id := r.PathValue("id")
		ok, until := false, time.Time{}
		if id != "" {
			ok, until = s.svc.PresentAccess(ctx, id, pk)
		}
		if !ok {
			if signedIn != nil {
				return *signedIn, false
			}
			return reqAuth{}, true // expired, replaced or made up: counts toward the lockout
		}
		if signedIn != nil {
			if d, err := s.deckRef(ctx, id); err == nil && s.policy().Can(signedIn.p, authz.Drive, d) {
				return *signedIn, false
			}
		}
		return reqAuth{p: authz.Principal{Kind: authz.KindPresenter, Deck: id}, until: until}, false
	}
	if signedIn != nil {
		return *signedIn, false
	}
	if s.openMode(ctx) {
		return reqAuth{p: authz.Principal{Kind: authz.KindOpen, Username: "open-mode"}}, false
	}
	return reqAuth{}, false
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// sameOrigin rejects cross-site browser requests: the Origin (or Referer)
// must match this server. Requests from non-browser clients carry neither.
func (s *server) sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" || o == "null" {
		if ref := r.Header.Get("Referer"); ref != "" {
			o = ref
		} else {
			return o == ""
		}
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if b, err := url.Parse(s.opt.BaseURL); err == nil && b.Host != "" && strings.EqualFold(u.Host, b.Host) {
		return true
	}
	return false
}

// authn identifies the caller, enforces the lockout and, for cookie-signed-in
// writes, the CSRF token and same-origin check. It never rejects anonymous
// callers: require does that.
func (s *server) authn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		hasCreds := bearer(r) != "" || r.URL.Query().Get("p") != ""
		if hasCreds && !s.fails.allowed(ip) {
			w.Header().Set("Retry-After", "300")
			http.Error(w, "too many failed attempts; try again in a few minutes", http.StatusTooManyRequests)
			return
		}
		a, bad := s.identify(r)
		if bad {
			s.fails.fail(ip)
			w.Header().Set("WWW-Authenticate", `Bearer realm="slidekick"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if hasCreds {
			s.fails.succeed(ip)
		}
		if a.session != "" && unsafeMethod(r.Method) {
			got := r.Header.Get(csrfHeader)
			if !s.sameOrigin(r) || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(a.csrf)) != 1 {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing or wrong CSRF token; reload the page"})
				return
			}
		}
		ctx := context.WithValue(r.Context(), authKey{}, a)
		next.ServeHTTP(w, r.WithContext(authz.WithPrincipal(ctx, a.p)))
	})
}

func (s *server) policy() authz.Policy {
	if s.acc == nil {
		return authz.Policy{}
	}
	return s.acc.Policy()
}

// deckRef loads what a policy decision needs about the deck in the path.
func (s *server) deckRef(ctx context.Context, id string) (*authz.Deck, error) {
	owner, err := s.svc.DeckOwner(ctx, id)
	if err != nil {
		return nil, err
	}
	return &authz.Deck{ID: id, Owner: owner}, nil
}

// require allows the request only if the caller may perform action (on the
// deck named by the {id} path value when perDeck).
func (s *server) require(action authz.Action, perDeck bool, next http.HandlerFunc) http.Handler {
	return s.authn(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := authFrom(r.Context())
		if !a.p.Authenticated() {
			w.Header().Set("WWW-Authenticate", `Bearer realm="slidekick"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in first"})
			return
		}
		var d *authz.Deck
		if perDeck {
			var err error
			if d, err = s.deckRef(r.Context(), r.PathValue("id")); err != nil {
				writeErr(w, err)
				return
			}
		}
		if action != anyAuthenticated && !s.policy().Can(a.p, action, d) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": forbiddenMsg(a.p, action)})
			return
		}
		next(w, r)
	}))
}

func forbiddenMsg(p authz.Principal, a authz.Action) string {
	switch {
	case p.Kind == authz.KindPresenter:
		return "a presenter link only presents deck " + p.Deck
	case p.Kind == authz.KindDeck:
		return "this token can't do that (it only edits deck " + p.Deck + ")"
	case p.Role == authz.RoleViewer:
		return "your role (viewer) can't do that"
	case a == authz.Edit || a == authz.Share || a == authz.MintDeckToken:
		return "only the deck's owner or an admin can do that"
	}
	return "your role can't do that"
}

// viewable reports whether the request may watch a deck: the deck is public,
// the request carries its view key (?k=), or the caller may view it.
// Private decks answer 404 otherwise, so their ids can't be probed.
func (s *server) viewable(r *http.Request, id string) bool {
	if s.svc.CanView(r.Context(), id, r.URL.Query().Get("k")) {
		return true
	}
	ip := clientIP(r)
	if (bearer(r) != "" || r.URL.Query().Get("p") != "") && !s.fails.allowed(ip) {
		return false
	}
	a, bad := s.identify(r)
	if bad {
		s.fails.fail(ip)
		return false
	}
	if !a.p.Authenticated() {
		return false
	}
	d, err := s.deckRef(r.Context(), id)
	return err == nil && s.policy().Can(a.p, authz.View, d)
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

// mcpAuth requires a bearer token (server, personal or deck) and hands the
// principal to the MCP SDK as TokenInfo, so tools can enforce roles and an MCP
// session can't be reused under another principal. In open mode MCP is open.
func (s *server) mcpAuth(next http.Handler) http.Handler {
	verify := func(ctx context.Context, tok string, _ *http.Request) (*auth.TokenInfo, error) {
		p := authz.FromContext(ctx)
		if p.Kind != authz.KindServer && p.Kind != authz.KindUser && p.Kind != authz.KindDeck {
			return nil, auth.ErrInvalidToken
		}
		ti := authz.TokenInfo(p)
		ti.Expiration = time.Now().Add(time.Hour)
		return ti, nil
	}
	guarded := auth.RequireBearerToken(verify, nil)(next)
	return s.authn(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authFrom(r.Context()).p.Kind == authz.KindOpen {
			next.ServeHTTP(w, r)
			return
		}
		if bearer(r) == "" { // cookies don't authenticate MCP
			w.Header().Set("WWW-Authenticate", `Bearer realm="slidekick"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "MCP needs a bearer token: a personal API token (Account page), a deck token or the server token"})
			return
		}
		guarded.ServeHTTP(w, r)
	}))
}

func (s *server) authInfo(w http.ResponseWriter, r *http.Request) {
	p := authFrom(r.Context()).p
	scope := "admin"
	switch {
	case p.Kind == authz.KindDeck:
		scope = "deck"
	case p.Kind == authz.KindUser && p.Role != authz.RoleAdmin:
		scope = string(p.Role)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "required": !s.openMode(r.Context()), "scope": scope, "deck": p.Deck, "user": p.Username})
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

// mintLink makes a viewer or presenter link: {"kind":"view"|"present","minutes":0|30..1440}.
func (s *server) mintLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind    string `json:"kind"`
		Minutes int    `json:"minutes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, err)
		return
	}
	u, exp, err := s.svc.MintLink(r.Context(), r.PathValue("id"), body.Kind, time.Duration(body.Minutes)*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	out := map[string]any{"url": u}
	if !exp.IsZero() {
		out["expiresAt"] = exp.UTC().Format(time.RFC3339)
	}
	writeJSON(w, 200, out)
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

// wsAuth checks a token sent in a WebSocket auth message: may it present this deck?
func (s *server) wsAuth(ctx context.Context, ip, tok, deckID string) bool {
	if !s.fails.allowed(ip) {
		return false
	}
	a, ok := s.resolveToken(ctx, tok)
	if !ok {
		s.fails.fail(ip)
		return false
	}
	s.fails.succeed(ip)
	d, err := s.deckRef(ctx, deckID)
	return err == nil && s.policy().Can(a.p, authz.Present, d)
}

// anyAuthenticated lets any valid principal through require.
const anyAuthenticated authz.Action = ""
