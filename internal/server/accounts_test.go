package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick"
	"github.com/SecOps-7/slidekick/internal/accounts"
	"github.com/SecOps-7/slidekick/internal/mcpserver"
	"github.com/SecOps-7/slidekick/internal/oidc"
	"github.com/SecOps-7/slidekick/internal/secret"
	"github.com/SecOps-7/slidekick/internal/server"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

type accEnv struct {
	ts  *httptest.Server
	env *testenv.Env
	acc *accounts.Service
}

// newAccServer starts a server with accounts. With serverToken "" and no
// users it starts in open mode, like a fresh install.
func newAccServer(t *testing.T, serverToken string) *accEnv {
	return newAccServerWith(t, serverToken, nil)
}

func newAccServerWith(t *testing.T, serverToken string, tweak func(*server.Options)) *accEnv {
	t.Helper()
	env := testenv.New(t)
	box, _, err := secret.Load("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acc := accounts.New(env.Store, box, oidc.NewManager(nil))
	e := &accEnv{env: env, acc: acc}
	var h http.Handler
	e.ts = httptest.NewUnstartedServer(http.NotFoundHandler())
	e.ts.Start()
	opt := server.Options{Token: serverToken, BaseURL: e.ts.URL, Accounts: acc, SchemaFS: slidekick.Schema, Log: slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		Web: fstest.MapFS{"index.html": {Data: []byte("app")}}}
	if tweak != nil {
		tweak(&opt)
	}
	mcpSrv := mcpserver.New(env.Svc, slidekick.Schema, "test", mcpserver.Options{Policy: acc.Policy(), Presenter: opt.Presenter})
	h = server.New(env.Svc, mcpSrv, opt)
	e.ts.Config.Handler = h
	t.Cleanup(e.ts.Close)
	return e
}

type client struct {
	t    *testing.T
	base string
	hc   *http.Client
	csrf string
	bear string
}

func (e *accEnv) client(t *testing.T) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: e.ts.URL, hc: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *client) req(method, path string, body any, hdr map[string]string) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.bear != "" {
		req.Header.Set("Authorization", "Bearer "+c.bear)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	return c.req(method, path, body, nil)
}

func (c *client) login(user, pw string) {
	c.t.Helper()
	code, out := c.do("POST", "/api/login", map[string]string{"username": user, "password": pw})
	if code != 200 {
		c.t.Fatalf("login %s: %d %v", user, code, out)
	}
	c.csrf, _ = out["csrf"].(string)
}

const adminPW = "correct horse battery"

// withAdmin sets the instance up through /setup and returns the admin's client.
func (e *accEnv) withAdmin(t *testing.T) *client {
	c := e.client(t)
	code, out := c.do("POST", "/api/setup", map[string]string{"code": e.acc.SetupCode(), "username": "boss", "password": adminPW})
	if code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	c.csrf = out["csrf"].(string)
	return c
}

func (e *accEnv) addUser(t *testing.T, admin *client, name, role string) *client {
	code, out := admin.do("POST", "/api/admin/users", map[string]string{"username": name, "password": name + "-password-123", "role": role})
	if code != 200 {
		t.Fatalf("create %s: %d %v", name, code, out)
	}
	c := e.client(t)
	c.login(name, name+"-password-123")
	return c
}

var oneSlide = []any{map[string]any{"layout": "title", "regions": map[string]any{"title": []any{map[string]any{"type": "heading", "props": map[string]any{"text": "Hi"}}}}}}

func patchTitle(title string) map[string]any {
	return map[string]any{"ops": []any{map[string]any{"op": "replace", "path": "/title", "value": title}}}
}

func TestFirstRunSetup(t *testing.T) {
	e := newAccServer(t, "")
	anon := e.client(t)
	// Fresh install: open mode until the first admin exists.
	_, me := anon.do("GET", "/api/me", nil)
	if me["openMode"] != true || me["setupNeeded"] != true {
		t.Fatalf("fresh install me: %v", me)
	}
	if code, _ := anon.do("POST", "/api/setup", map[string]string{"code": "WRONG", "username": "boss", "password": adminPW}); code != 403 {
		t.Fatalf("wrong setup code: %d", code)
	}
	if code, _ := anon.do("POST", "/api/setup", map[string]string{"code": e.acc.SetupCode(), "username": "boss", "password": "short"}); code != 422 {
		t.Fatalf("short password: %d", code)
	}
	admin := e.withAdmin(t)
	// Setup is one-shot, and open mode is over.
	if code, _ := anon.do("POST", "/api/setup", map[string]string{"code": "x", "username": "evil", "password": adminPW}); code != 409 {
		t.Fatalf("second setup: %d", code)
	}
	if code, _ := anon.do("GET", "/api/decks", nil); code != 401 {
		t.Fatalf("anonymous deck list after setup: %d", code)
	}
	if code, _ := admin.do("GET", "/api/decks", nil); code != 200 {
		t.Fatalf("admin deck list: %d", code)
	}
}

func TestExistingInstallSetupWithServerToken(t *testing.T) {
	e := newAccServer(t, token)
	// Decks made before accounts existed...
	r := struct{ DeckID string }{legacyDeck(t, e.env, "Old deck")}
	// ...the server token still works, and is accepted as the setup code.
	srv := e.client(t)
	srv.bear = token
	if code, _ := srv.do("GET", "/api/decks", nil); code != 200 {
		t.Fatalf("server token: %d", code)
	}
	c := e.client(t)
	if code, out := c.do("POST", "/api/setup", map[string]string{"code": token, "username": "boss", "password": adminPW}); code != 200 {
		t.Fatalf("setup with server token: %d %v", code, out)
	}
	// The first admin owns the old decks, and they still open by link.
	owner, _ := e.env.Svc.DeckOwner(context.Background(), r.DeckID)
	u, _ := e.env.Store.UserByName(context.Background(), "boss")
	if owner != u.ID {
		t.Fatalf("old deck owner %q, want %q", owner, u.ID)
	}
	if code, _ := e.client(t).do("GET", "/api/decks/"+r.DeckID, nil); code != 200 {
		t.Fatalf("old deck link: %d", code)
	}
}

func TestLoginLockoutAndLogout(t *testing.T) {
	e := newAccServer(t, "")
	e.withAdmin(t)
	c := e.client(t)
	if code, _ := c.do("POST", "/api/login", map[string]string{"username": "boss", "password": "nope-nope-nope"}); code != 401 {
		t.Fatalf("bad password: %d", code)
	}
	if code, _ := c.do("POST", "/api/login", map[string]string{"username": "nobody", "password": "nope-nope-nope"}); code != 401 {
		t.Fatalf("unknown user: %d", code)
	}
	c.login("boss", adminPW)
	if _, me := c.do("GET", "/api/me", nil); me["signedIn"] != true || me["csrf"] == "" {
		t.Fatalf("me after login: %v", me)
	}
	if code, _ := c.do("POST", "/api/logout", nil); code != 200 {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := c.do("GET", "/api/decks", nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
	// Ten failures lock the address out, even for the right password.
	for range 10 {
		c.do("POST", "/api/login", map[string]string{"username": "boss", "password": "wrong-wrong-wrong"})
	}
	if code, _ := c.do("POST", "/api/login", map[string]string{"username": "boss", "password": adminPW}); code != 429 {
		t.Fatalf("after 10 failures: %d", code)
	}
}

func TestSessionCookieFlags(t *testing.T) {
	e := newAccServer(t, "")
	c := e.client(t)
	b, _ := json.Marshal(map[string]string{"code": e.acc.SetupCode(), "username": "boss", "password": adminPW})
	res, err := http.Post(e.ts.URL+"/api/setup", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	sc := res.Header.Get("Set-Cookie")
	for _, want := range []string{"sk_session=", "HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(sc, want) {
			t.Fatalf("session cookie %q lacks %q", sc, want)
		}
	}
	_ = c
}

func TestCSRF(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	r, _ := admin.do("POST", "/api/decks", map[string]any{"title": "Mine", "slides": oneSlide})
	_ = r
	_, list := admin.do("GET", "/api/decks", nil)
	id := list["decks"].([]any)[0].(map[string]any)["id"].(string)

	csrf := admin.csrf
	admin.csrf = ""
	if code, _ := admin.do("POST", "/api/decks/"+id+"/patch", patchTitle("x")); code != 403 {
		t.Fatalf("cookie write without CSRF token: %d", code)
	}
	admin.csrf = "wrong"
	if code, _ := admin.do("POST", "/api/decks/"+id+"/patch", patchTitle("x")); code != 403 {
		t.Fatalf("wrong CSRF token: %d", code)
	}
	admin.csrf = csrf
	if code, _ := admin.req("POST", "/api/decks/"+id+"/patch", patchTitle("x"), map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Fatalf("cross-site origin: %d", code)
	}
	if code, out := admin.do("POST", "/api/decks/"+id+"/patch", patchTitle("x")); code != 200 {
		t.Fatalf("good write: %d %v", code, out)
	}
	// Sign-in itself refuses cross-site posts (login CSRF).
	if code, _ := e.client(t).req("POST", "/api/login", map[string]string{"username": "boss", "password": adminPW}, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Fatalf("cross-site login: %d", code)
	}
}

func TestRoles(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	ed := e.addUser(t, admin, "edna", "editor")
	ed2 := e.addUser(t, admin, "eddie", "editor")
	view := e.addUser(t, admin, "vera", "viewer")

	code, out := ed.do("POST", "/api/decks", map[string]any{"title": "Edna's", "slides": oneSlide})
	if code != 200 {
		t.Fatalf("editor create: %d %v", code, out)
	}
	id := out["deckId"].(string)
	if code, _ := view.do("POST", "/api/decks", map[string]any{"title": "nope"}); code != 403 {
		t.Fatalf("viewer create: %d", code)
	}
	// Everyone signed in sees the team's decks.
	for _, c := range []*client{admin, ed2, view} {
		if code, out := c.do("GET", "/api/decks", nil); code != 200 || len(out["decks"].([]any)) != 1 {
			t.Fatalf("list: %d %v", code, out)
		}
	}
	// Only the owner (and admins) change a deck.
	cases := []struct {
		c    *client
		want int
	}{{ed, 200}, {admin, 200}, {ed2, 403}, {view, 403}}
	for i, tc := range cases {
		if code, _ := tc.c.do("POST", "/api/decks/"+id+"/patch", patchTitle("v")); code != tc.want {
			t.Fatalf("case %d patch: %d want %d", i, code, tc.want)
		}
	}
	if code, _ := view.do("POST", "/api/decks/"+id+"/comments", map[string]string{"text": "fix the title"}); code != 200 {
		t.Fatalf("viewer comment: %d", code)
	}
	if code, _ := view.do("POST", "/api/decks/"+id+"/access", map[string]bool{"private": true}); code != 403 {
		t.Fatalf("viewer share: %d", code)
	}
	if code, _ := ed2.do("POST", "/api/decks/"+id+"/edit-token", nil); code != 403 {
		t.Fatalf("other editor mints deck token: %d", code)
	}
	if code, _ := ed.do("POST", "/api/decks/"+id+"/edit-token", nil); code != 200 {
		t.Fatalf("owner mints deck token: %d", code)
	}
	if code, _ := view.do("POST", "/api/files?name=x.png", nil); code != 403 {
		t.Fatalf("viewer upload: %d", code)
	}
	// Admin-only areas.
	for _, c := range []*client{ed, view} {
		if code, _ := c.do("GET", "/api/admin/users", nil); code != 403 {
			t.Fatalf("non-admin user list: %d", code)
		}
		if code, _ := c.do("PUT", "/api/admin/settings", map[string]bool{"editorsEditAll": true}); code != 403 {
			t.Fatalf("non-admin settings: %d", code)
		}
	}
	// The instance setting lets editors change every deck.
	if code, _ := admin.do("PUT", "/api/admin/settings", map[string]bool{"editorsEditAll": true}); code != 200 {
		t.Fatal("settings")
	}
	if code, _ := ed2.do("POST", "/api/decks/"+id+"/patch", patchTitle("w")); code != 200 {
		t.Fatalf("editor with edit-all: %d", code)
	}
	if code, _ := view.do("POST", "/api/decks/"+id+"/patch", patchTitle("w")); code != 403 {
		t.Fatalf("viewer with edit-all: %d", code)
	}
}

func TestUserAdmin(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	ed := e.addUser(t, admin, "edna", "editor")
	_, list := admin.do("GET", "/api/admin/users", nil)
	var ednaID, bossID string
	for _, u := range list["users"].([]any) {
		m := u.(map[string]any)
		if _, leak := m["passwordHash"]; leak {
			t.Fatal("password hash in user list")
		}
		switch m["username"] {
		case "edna":
			ednaID = m["id"].(string)
		case "boss":
			bossID = m["id"].(string)
		}
	}
	// The last admin can't be demoted, disabled or deleted.
	if code, _ := admin.do("PATCH", "/api/admin/users/"+bossID, map[string]string{"role": "viewer"}); code != 409 {
		t.Fatalf("demote last admin: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/admin/users/"+bossID, nil); code != 409 {
		t.Fatalf("delete self: %d", code)
	}
	// Disabling a user ends their session at once.
	if code, _ := admin.do("PATCH", "/api/admin/users/"+ednaID, map[string]bool{"disabled": true}); code != 200 {
		t.Fatal("disable")
	}
	if code, _ := ed.do("GET", "/api/decks", nil); code != 401 {
		t.Fatalf("disabled user's session: %d", code)
	}
	if code, _ := e.client(t).do("POST", "/api/login", map[string]string{"username": "edna", "password": "edna-password-123"}); code != 401 {
		t.Fatalf("disabled login: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/admin/users/"+ednaID+"?transferTo="+bossID, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
}

func TestPersonalTokensAndMCPRoles(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	ed := e.addUser(t, admin, "edna", "editor")
	view := e.addUser(t, admin, "vera", "viewer")
	_, out := admin.do("POST", "/api/decks", map[string]any{"title": "Admin's", "slides": oneSlide})
	adminDeck := out["deckId"].(string)
	_, out = ed.do("POST", "/api/me/tokens", map[string]string{"name": "laptop agent"})
	edTok := out["token"].(string)
	_, out = view.do("POST", "/api/me/tokens", map[string]string{"name": "reader"})
	viewTok := out["token"].(string)
	if !strings.HasPrefix(edTok, "skp_") {
		t.Fatalf("token %q", edTok)
	}
	if _, l := ed.do("GET", "/api/me/tokens", nil); strings.Contains(toJSON(l), edTok) {
		t.Fatal("token list shows the secret")
	}

	ctx := context.Background()
	connect := func(tok string) *mcp.ClientSession {
		tr := &mcp.StreamableClientTransport{Endpoint: e.ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerRT{tok}}}
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, tr, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		return cs
	}
	call := func(cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := ""
		if len(res.Content) > 0 {
			if tc, ok := res.Content[0].(*mcp.TextContent); ok {
				text = tc.Text
			}
		}
		return res, text
	}
	edCS := connect(edTok)
	res, _ := call(edCS, "create_deck", map[string]any{"title": "Agent made", "slides": oneSlide})
	if res.IsError {
		t.Fatal("editor token create_deck refused")
	}
	b, _ := json.Marshal(res.StructuredContent)
	var created struct{ DeckID string }
	json.Unmarshal(b, &created)
	owner, _ := e.env.Svc.DeckOwner(ctx, created.DeckID)
	u, _ := e.env.Store.UserByName(ctx, "edna")
	if owner != u.ID {
		t.Fatalf("MCP-created deck owner %q, want edna", owner)
	}
	if res, _ := call(edCS, "patch_deck", map[string]any{"deck_id": created.DeckID, "ops": patchTitle("ok")["ops"]}); res.IsError {
		t.Fatal("editor patch own deck refused")
	}
	if res, msg := call(edCS, "patch_deck", map[string]any{"deck_id": adminDeck, "ops": patchTitle("no")["ops"]}); !res.IsError || !strings.Contains(msg, "owner") {
		t.Fatalf("editor patched admin's deck: %v %q", res.IsError, msg)
	}
	viewCS := connect(viewTok)
	if res, _ := call(viewCS, "get_deck", map[string]any{"deck_id": adminDeck}); res.IsError {
		t.Fatal("viewer read refused")
	}
	for _, tool := range []string{"create_deck", "patch_deck", "upload_asset", "set_deck_access"} {
		args := map[string]any{"deck_id": adminDeck, "title": "x", "ops": patchTitle("no")["ops"], "private": true, "name": "a.png", "mime": "image/png", "data": "AA=="}
		if res, _ := call(viewCS, tool, args); !res.IsError {
			t.Fatalf("viewer %s allowed", tool)
		}
	}
	// Session cookies don't authenticate MCP (it's for bearer tokens).
	if code, _ := admin.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}); code != 401 {
		t.Fatalf("cookie MCP: %d", code)
	}
	// A disabled user's token stops working.
	_, list := admin.do("GET", "/api/admin/users", nil)
	for _, x := range list["users"].([]any) {
		if m := x.(map[string]any); m["username"] == "edna" {
			admin.do("PATCH", "/api/admin/users/"+m["id"].(string), map[string]bool{"disabled": true})
		}
	}
	tc := e.client(t)
	tc.bear = edTok
	if code, _ := tc.do("GET", "/api/decks", nil); code != 401 {
		t.Fatalf("disabled user's token: %d", code)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(b)))
	return len(b), nil
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// ---- single sign-on against a fake identity provider ----

func (e *accEnv) addProvider(t *testing.T, admin *client, idp *testenv.FakeIdP, issuer string, extra map[string]any) {
	body := map[string]any{"name": "Fake IdP", "issuer": issuer, "clientId": idp.ClientID, "clientSecret": idp.ClientSecret, "enabled": true, "defaultRole": "viewer"}
	for k, v := range extra {
		body[k] = v
	}
	if code, out := admin.do("PUT", "/api/admin/sso/fake", body); code != 200 {
		t.Fatalf("add provider: %d %v", code, out)
	}
}

// ssoSignIn walks the browser through login -> IdP -> callback and returns the final redirect.
func (c *client) ssoSignIn(next string) (string, int) {
	c.t.Helper()
	res, err := c.hc.Get(c.base + "/auth/sso/fake/login?next=" + url.QueryEscape(next))
	if err != nil {
		c.t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 302 {
		return "", res.StatusCode
	}
	res, err = c.hc.Get(res.Header.Get("Location")) // the IdP signs us in and sends us back
	if err != nil {
		c.t.Fatal(err)
	}
	res.Body.Close()
	res, err = c.hc.Get(res.Header.Get("Location")) // our callback
	if err != nil {
		c.t.Fatal(err)
	}
	res.Body.Close()
	return res.Header.Get("Location"), res.StatusCode
}

func TestSSO(t *testing.T) {
	for _, slash := range []bool{false, true} {
		t.Run(map[bool]string{false: "issuer", true: "issuer-with-slash"}[slash], func(t *testing.T) {
			e := newAccServer(t, "")
			admin := e.withAdmin(t)
			idp := testenv.NewFakeIdP(t, slash)
			// The admin types the issuer the other way round from what discovery says.
			typed := strings.TrimSuffix(idp.Issuer, "/")
			if !slash {
				typed += "/"
			}
			e.addProvider(t, admin, idp, typed, map[string]any{"autoCreate": true,
				"roleRules": []map[string]string{{"claim": "group", "value": "slide-editors", "role": "editor"}}})
			_, list := admin.do("GET", "/api/admin/sso", nil)
			if strings.Contains(toJSON(list), idp.ClientSecret) {
				t.Fatal("client secret sent back to the browser")
			}

			idp.SignInAs(map[string]any{"sub": "u-1", "email": "ann@example.com", "email_verified": true, "name": "Ann", "groups": []string{"slide-editors"}})
			c := e.client(t)
			loc, code := c.ssoSignIn("/d/abc/edit")
			if code != 302 || loc != "/d/abc/edit" {
				t.Fatalf("callback: %d -> %q", code, loc)
			}
			_, me := c.do("GET", "/api/me", nil)
			user, _ := me["user"].(map[string]any)
			if me["signedIn"] != true || user["role"] != "editor" || user["email"] != "ann@example.com" {
				t.Fatalf("after SSO: %v", me)
			}
			// Signing in again finds the same account.
			c2 := e.client(t)
			c2.ssoSignIn("/")
			_, users := admin.do("GET", "/api/admin/users", nil)
			if n := len(users["users"].([]any)); n != 2 {
				t.Fatalf("users after two SSO sign-ins: %d", n)
			}
		})
	}
}

func TestSSORejections(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	idp := testenv.NewFakeIdP(t, false)
	e.addProvider(t, admin, idp, idp.Issuer, map[string]any{"autoCreate": false})
	idp.SignInAs(map[string]any{"sub": "u-2", "email": "boss@example.com", "email_verified": false})

	signedIn := func(c *client) bool { _, me := c.do("GET", "/api/me", nil); return me["signedIn"] == true }

	// Auto-creation off: no account, no session.
	c := e.client(t)
	if loc, _ := c.ssoSignIn("/"); !strings.HasPrefix(loc, "/login?error=") || signedIn(c) {
		t.Fatalf("auto-create off: %q", loc)
	}
	// An unverified email must not take over an existing account with that email.
	u, _ := e.env.Store.UserByName(context.Background(), "boss")
	u.Email = "boss@example.com"
	e.env.Store.UpdateUser(context.Background(), u)
	if loc, _ := e.client(t).ssoSignIn("/"); !strings.HasPrefix(loc, "/login?error=") {
		t.Fatalf("unverified email linked to the admin: %q", loc)
	}
	// Verified email links to the existing account.
	idp.SignInAs(map[string]any{"sub": "u-2", "email": "boss@example.com", "email_verified": true})
	c = e.client(t)
	if loc, _ := c.ssoSignIn("/"); loc != "/" || !signedIn(c) {
		t.Fatalf("verified email: %q", loc)
	}

	// Tampered tokens are refused: wrong audience, wrong issuer, wrong nonce.
	for name, tamper := range map[string]func(map[string]any){
		"audience": func(m map[string]any) { m["aud"] = "someone-else" },
		"issuer":   func(m map[string]any) { m["iss"] = "https://evil.example" },
		"nonce":    func(m map[string]any) { m["nonce"] = "replayed" },
	} {
		idp.Tamper = tamper
		c := e.client(t)
		if loc, _ := c.ssoSignIn("/"); !strings.HasPrefix(loc, "/login?error=") || signedIn(c) {
			t.Fatalf("tampered %s accepted: %q", name, loc)
		}
	}
	idp.Tamper = nil

	// The callback only works in the browser that started the sign-in (state cookie).
	starter := e.client(t)
	res, _ := starter.hc.Get(e.ts.URL + "/auth/sso/fake/login")
	res.Body.Close()
	res, _ = starter.hc.Get(res.Header.Get("Location"))
	res.Body.Close()
	victim := e.client(t)
	res, _ = victim.hc.Get(res.Header.Get("Location"))
	res.Body.Close()
	if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, "/login?error=") || signedIn(victim) {
		t.Fatalf("callback in another browser: %q", loc)
	}
	// Open redirects are refused.
	idp.SignInAs(map[string]any{"sub": "u-2", "email": "boss@example.com", "email_verified": true})
	if loc, _ := e.client(t).ssoSignIn("//evil.example/x"); loc != "/" {
		t.Fatalf("open redirect: %q", loc)
	}
}

func TestWebSocketPresentNeedsRole(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	view := e.addUser(t, admin, "vera", "viewer")
	_, out := admin.do("POST", "/api/decks", map[string]any{"title": "Talk", "slides": oneSlide})
	id := out["deckId"].(string)
	e.env.Svc.SetPrivate(context.Background(), id, false, false) // anonymous watchers need a public deck
	ctx := context.Background()
	wsURL := "ws" + strings.TrimPrefix(e.ts.URL, "http") + "/ws/decks/" + id
	dial := func(c *client) *websocket.Conn {
		h := http.Header{}
		if c != nil {
			u, _ := url.Parse(e.ts.URL)
			for _, ck := range c.hc.Jar.Cookies(u) {
				h.Add("Cookie", ck.Name+"="+ck.Value)
			}
		}
		conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		conn.Read(ctx) // hello
		return conn
	}
	watcher := dial(nil)
	anon := dial(nil)
	anon.Write(ctx, websocket.MessageText, []byte(`{"type":"present","index":3,"step":0}`))
	presenter := dial(view)
	presenter.Write(ctx, websocket.MessageText, []byte(`{"type":"present","index":1,"step":0}`))
	rctx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	_, b, err := watcher.Read(rctx)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Type  string
		Index int
	}
	json.Unmarshal(b, &m)
	if m.Type != "present" || m.Index != 1 {
		t.Fatalf("watcher got %s (anonymous presenter must be ignored, signed-in viewer relayed)", b)
	}
}

// legacyDeck stores a deck the way releases before private-by-default did:
// no access row at all, so it is public by link.
func legacyDeck(t *testing.T, env *testenv.Env, title string) string {
	t.Helper()
	id := "d_legacy" + strings.ToLower(strings.ReplaceAll(title, " ", ""))
	doc, _ := json.Marshal(map[string]any{"schemaVersion": 1, "id": id, "title": title, "theme": "aurora", "aspect": "16:9", "slides": []any{}})
	if err := env.Store.Create(context.Background(), id, title, "aurora", 0, doc, "agent"); err != nil {
		t.Fatal(err)
	}
	return id
}
