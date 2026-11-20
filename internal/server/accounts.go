package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SecOps-7/slidekick/internal/accounts"
	"github.com/SecOps-7/slidekick/internal/authz"
	"github.com/SecOps-7/slidekick/internal/store"
)

// accountRoutes adds sign-in, setup, the account page API, user and settings
// administration, and single sign-on.
func (s *server) accountRoutes(mux *http.ServeMux) {
	if s.acc == nil {
		return
	}
	mux.Handle("GET /api/me", s.authn(http.HandlerFunc(s.me)))
	mux.Handle("POST /api/login", s.authn(http.HandlerFunc(s.login)))
	mux.Handle("POST /api/logout", s.authn(http.HandlerFunc(s.logout)))
	mux.Handle("GET /api/setup", s.authn(http.HandlerFunc(s.setupInfo)))
	mux.Handle("POST /api/setup", s.authn(http.HandlerFunc(s.setup)))
	mux.Handle("POST /api/me/password", s.requireUser(s.changePassword))
	mux.Handle("GET /api/me/tokens", s.requireUser(s.listTokens))
	mux.Handle("POST /api/me/tokens", s.requireUser(s.createToken))
	mux.Handle("DELETE /api/me/tokens/{tid}", s.requireUser(s.deleteToken))

	mux.Handle("GET /api/admin/users", s.require(authz.ManageUsers, false, s.listUsers))
	mux.Handle("POST /api/admin/users", s.require(authz.ManageUsers, false, s.createUser))
	mux.Handle("PATCH /api/admin/users/{uid}", s.require(authz.ManageUsers, false, s.updateUser))
	mux.Handle("DELETE /api/admin/users/{uid}", s.require(authz.ManageUsers, false, s.deleteUser))
	mux.Handle("POST /api/admin/users/{uid}/password", s.require(authz.ManageUsers, false, s.resetPassword))
	mux.Handle("GET /api/admin/settings", s.require(authz.ManageSettings, false, s.getSettings))
	mux.Handle("PUT /api/admin/settings", s.require(authz.ManageSettings, false, s.putSettings))
	mux.Handle("GET /api/admin/sso", s.require(authz.ManageSettings, false, s.listSSO))
	mux.Handle("PUT /api/admin/sso/{pid}", s.require(authz.ManageSettings, false, s.putSSO))
	mux.Handle("DELETE /api/admin/sso/{pid}", s.require(authz.ManageSettings, false, s.deleteSSO))
	mux.Handle("POST /api/admin/sso/{pid}/check", s.require(authz.ManageSettings, false, s.checkSSO))

	mux.HandleFunc("GET /api/auth/providers", s.publicProviders)
	mux.HandleFunc("GET /auth/sso/{pid}/login", s.ssoLogin)
	mux.HandleFunc("GET /auth/sso/{pid}/callback", s.ssoCallback)
}

// requireUser needs a signed-in account (not the server or a deck token).
func (s *server) requireUser(next http.HandlerFunc) http.Handler {
	return s.authn(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authFrom(r.Context()).user == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in first"})
			return
		}
		next(w, r)
	}))
}

func (s *server) secureCookies() bool { return strings.HasPrefix(s.opt.BaseURL, "https://") }

func (s *server) setSession(w http.ResponseWriter, cookie string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: cookie, Path: "/", HttpOnly: true, Secure: s.secureCookies(),
		SameSite: http.SameSiteLaxMode, MaxAge: int(accounts.SessionTTL / time.Second)})
}

func (s *server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

// safeNext keeps post-login redirects on this site.
func safeNext(n string) string {
	if n == "" || !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") || strings.HasPrefix(n, "/\\") || strings.ContainsAny(n, "\r\n") {
		return "/"
	}
	return n
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body"})
		return false
	}
	return true
}

func writeAccErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, accounts.ErrValidation):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": strings.TrimPrefix(err.Error(), "invalid: ")})
	case errors.Is(err, accounts.ErrLastAdmin):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

// ---- who am I ----

type meOut struct {
	SignedIn    bool            `json:"signedIn"`
	Kind        string          `json:"kind,omitempty"`
	User        *store.User     `json:"user,omitempty"`
	CSRF        string          `json:"csrf,omitempty"`
	SetupNeeded bool            `json:"setupNeeded"`
	OpenMode    bool            `json:"openMode"`
	Can         map[string]bool `json:"can"`
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	a := authFrom(r.Context())
	out := meOut{SignedIn: a.p.Authenticated(), Kind: string(a.p.Kind), User: a.user, CSRF: a.csrf,
		SetupNeeded: s.acc.SetupNeeded(r.Context()), OpenMode: a.p.Kind == authz.KindOpen, Can: map[string]bool{}}
	pol := s.policy()
	for _, act := range []authz.Action{authz.Create, authz.Upload, authz.UseAI, authz.ManageUsers, authz.ManageSettings, authz.ListDecks} {
		out.Can[string(act)] = pol.Can(a.p, act, nil)
	}
	if id := r.URL.Query().Get("deck"); id != "" && a.p.Authenticated() {
		if d, err := s.deckRef(r.Context(), id); err == nil {
			for _, act := range []authz.Action{authz.View, authz.Present, authz.Comment, authz.Edit, authz.Export, authz.Share, authz.MintDeckToken} {
				out.Can["deck:"+string(act)] = pol.Can(a.p, act, d)
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}

// ---- password sign-in ----

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-site sign-in refused"})
		return
	}
	var body struct{ Username, Password string }
	if !decode(w, r, &body) {
		return
	}
	ip := clientIP(r)
	userKey := "user:" + strings.ToLower(strings.TrimSpace(body.Username))
	if !s.fails.allowed(ip) || !s.fails.allowed(userKey) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many failed attempts; try again in a few minutes"})
		return
	}
	u, err := s.acc.Login(r.Context(), body.Username, body.Password)
	if err != nil {
		s.fails.fail(ip)
		s.fails.fail(userKey)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": accounts.ErrBadLogin.Error()})
		return
	}
	s.fails.succeed(ip)
	s.fails.succeed(userKey)
	s.startSession(w, r, u, "password")
}

func (s *server) startSession(w http.ResponseWriter, r *http.Request, u *store.User, via string) {
	if old, err := r.Cookie(sessionCookie); err == nil {
		s.acc.EndSession(r.Context(), old.Value) // no session fixation: always a fresh id
	}
	cookie, csrf, err := s.acc.NewSession(r.Context(), u.ID, via)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.setSession(w, cookie)
	writeJSON(w, 200, map[string]any{"signedIn": true, "user": u, "csrf": csrf})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	a := authFrom(r.Context())
	if a.session != "" {
		s.acc.EndSession(r.Context(), a.session)
	}
	s.clearSession(w)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- first admin ----

func (s *server) setupInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"needed": s.acc.SetupNeeded(r.Context()), "acceptsServerToken": s.opt.Token != ""})
}

func (s *server) setup(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-site request refused"})
		return
	}
	var body struct{ Code, Username, Password string }
	if !decode(w, r, &body) {
		return
	}
	ip := clientIP(r)
	if !s.fails.allowed(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many failed attempts; try again in a few minutes"})
		return
	}
	if !s.acc.SetupNeeded(r.Context()) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Slidekick is already set up; sign in instead"})
		return
	}
	if !s.acc.CheckSetupCode(body.Code, s.opt.Token) {
		s.fails.fail(ip)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "wrong setup code: use the code printed in the server log, or the server token"})
		return
	}
	u, err := s.acc.CreateFirstAdmin(r.Context(), body.Username, body.Password)
	if err != nil {
		writeAccErr(w, err)
		return
	}
	if s.opt.Log != nil {
		s.opt.Log.Info("first admin created", "username", u.Username)
	}
	s.startSession(w, r, u, "password")
}

// ---- own account ----

func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	a := authFrom(r.Context())
	var body struct{ Current, New string }
	if !decode(w, r, &body) {
		return
	}
	if err := s.acc.ChangeOwnPassword(r.Context(), a.user, body.Current, body.New, a.session); err != nil {
		writeAccErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) listTokens(w http.ResponseWriter, r *http.Request) {
	ts, err := s.acc.Store.APITokens(r.Context(), authFrom(r.Context()).user.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"tokens": ts})
}

func (s *server) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name string }
	if !decode(w, r, &body) {
		return
	}
	tok, t, err := s.acc.CreateAPIToken(r.Context(), authFrom(r.Context()).user.ID, body.Name)
	if err != nil {
		writeAccErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"token": tok, "info": t})
}

func (s *server) deleteToken(w http.ResponseWriter, r *http.Request) {
	if err := s.acc.Store.DeleteAPIToken(r.Context(), authFrom(r.Context()).user.ID, r.PathValue("tid")); err != nil {
		writeAccErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- user administration ----

func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	us, err := s.acc.Store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"users": us, "roles": authz.Roles})
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	var body accounts.NewUser
	if !decode(w, r, &body) {
		return
	}
	u, err := s.acc.CreateUser(r.Context(), body)
	if err != nil {
		writeAccErr(w, err)
		return
	}
	writeJSON(w, 200, u)
}

func (s *server) updateUser(w http.ResponseWriter, r *http.Request) {
	var body accounts.UserUpdate
	if !decode(w, r, &body) {
		return
	}
	u, err := s.acc.UpdateUser(r.Context(), r.PathValue("uid"), body)
	if err != nil {
		writeAccErr(w, err)
		return
	}
	writeJSON(w, 200, u)
}

func (s *server) deleteUser(w http.ResponseWriter, r *http.Request) {
	if a := authFrom(r.Context()); a.user != nil && a.user.ID == r.PathValue("uid") {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "you can't delete your own account"})
		return
	}
	if err := s.acc.DeleteUser(r.Context(), r.PathValue("uid"), r.URL.Query().Get("transferTo")); err != nil {
		writeAccErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct{ Password string }
	if !decode(w, r, &body) {
		return
	}
	if err := s.acc.SetPassword(r.Context(), r.PathValue("uid"), body.Password, ""); err != nil {
		writeAccErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.acc.Settings(r.Context()))
}

func (s *server) putSettings(w http.ResponseWriter, r *http.Request) {
	var body accounts.Settings
	if !decode(w, r, &body) {
		return
	}
	if err := s.acc.SaveSettings(r.Context(), body); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, s.acc.Settings(r.Context()))
}

// ---- decks: create from the UI, change owner ----

func (s *server) createDeck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title, Theme string
		Slides       []any
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		body.Title = "Untitled deck"
	}
	owner := ""
	if u := authFrom(r.Context()).user; u != nil {
		owner = u.ID
	}
	res, err := s.svc.CreateFor(r.Context(), owner, body.Title, body.Theme, "", body.Slides, "human")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *server) setDeckOwner(w http.ResponseWriter, r *http.Request) {
	var body struct{ Owner string }
	if !decode(w, r, &body) {
		return
	}
	if body.Owner != "" {
		if _, err := s.acc.Store.GetUser(r.Context(), body.Owner); err != nil {
			writeAccErr(w, err)
			return
		}
	}
	if err := s.svc.SetDeckOwner(r.Context(), r.PathValue("id"), body.Owner); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- single sign-on ----

func (s *server) ssoRedirectURL(id string) string {
	return strings.TrimRight(s.opt.BaseURL, "/") + "/auth/sso/" + url.PathEscape(id) + "/callback"
}

type ssoOut struct {
	*store.SSOProvider
	RedirectURL string `json:"redirectUrl"`
}

func (s *server) listSSO(w http.ResponseWriter, r *http.Request) {
	ps, err := s.acc.Store.SSOProviders(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := []ssoOut{}
	for _, p := range ps {
		out = append(out, ssoOut{p, s.ssoRedirectURL(p.ID)})
	}
	writeJSON(w, 200, map[string]any{"providers": out, "redirectBase": strings.TrimRight(s.opt.BaseURL, "/") + "/auth/sso/"})
}

func (s *server) putSSO(w http.ResponseWriter, r *http.Request) {
	var in accounts.ProviderInput
	if !decode(w, r, &in) {
		return
	}
	in.ID = r.PathValue("pid")
	p, err := s.acc.SaveProvider(r.Context(), in)
	if err != nil {
		writeAccErr(w, err)
		return
	}
	writeJSON(w, 200, ssoOut{p, s.ssoRedirectURL(p.ID)})
}

func (s *server) deleteSSO(w http.ResponseWriter, r *http.Request) {
	if err := s.acc.Store.DeleteSSOProvider(r.Context(), r.PathValue("pid")); err != nil {
		writeAccErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) checkSSO(w http.ResponseWriter, r *http.Request) {
	iss, err := s.acc.CheckProvider(r.Context(), r.PathValue("pid"))
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "issuer": iss})
}

func (s *server) publicProviders(w http.ResponseWriter, r *http.Request) {
	ps, _ := s.acc.Store.SSOProviders(r.Context())
	out := []map[string]string{}
	for _, p := range ps {
		if p.Enabled {
			out = append(out, map[string]string{"id": p.ID, "name": p.Name})
		}
	}
	writeJSON(w, 200, map[string]any{"providers": out})
}

func (s *server) ssoLogin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("pid")
	authURL, state, err := s.acc.BeginSSO(r.Context(), id, s.ssoRedirectURL(id), safeNext(r.URL.Query().Get("next")))
	if err != nil {
		if s.opt.Log != nil {
			s.opt.Log.Warn("sso: can't start sign-in", "provider", id, "err", err)
		}
		http.Redirect(w, r, "/login?error="+url.QueryEscape("That sign-in provider isn't available right now."), http.StatusFound)
		return
	}
	// Binds the flow to this browser: the callback must come back with the same state.
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: state, Path: "/auth/sso/", HttpOnly: true, Secure: s.secureCookies(),
		SameSite: http.SameSiteLaxMode, MaxAge: 600})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("pid")
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/auth/sso/", HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	ip := clientIP(r)
	if !s.fails.allowed(ip) {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Too many failed attempts; try again in a few minutes."), http.StatusFound)
		return
	}
	cookieState := ""
	if c, err := r.Cookie(ssoCookie); err == nil {
		cookieState = c.Value
	}
	u, next, err := s.acc.FinishSSO(r.Context(), id, s.ssoRedirectURL(id), cookieState, r.URL.Query())
	if err != nil {
		s.fails.fail(ip)
		if s.opt.Log != nil {
			s.opt.Log.Warn("sso: sign-in failed", "provider", id, "err", err)
		}
		msg := "Single sign-on failed. Try again, or ask an admin to check the provider settings."
		if errors.Is(err, accounts.ErrNoAccount) {
			msg = accounts.ErrNoAccount.Error()
		}
		http.Redirect(w, r, "/login?error="+url.QueryEscape(msg), http.StatusFound)
		return
	}
	s.fails.succeed(ip)
	if old, err := r.Cookie(sessionCookie); err == nil {
		s.acc.EndSession(r.Context(), old.Value)
	}
	cookie, _, err := s.acc.NewSession(r.Context(), u.ID, "sso:"+id)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.setSession(w, cookie)
	http.Redirect(w, r, safeNext(next), http.StatusFound)
}
