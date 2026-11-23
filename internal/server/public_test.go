package server_test

import (
	"strings"
	"testing"
)

// Public access (Settings > General): a public URL for links handed out, and
// trusted proxies (a reverse proxy or a tunnel) set without a restart.
func TestPublicURLAndTrustedProxies(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	code, out := admin.do("POST", "/api/decks", map[string]any{"title": "Remote"})
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	id := out["deckId"].(string)
	link := func(kind string) string {
		t.Helper()
		code, out := admin.do("POST", "/api/decks/"+id+"/links", map[string]any{"kind": kind})
		if code != 200 {
			t.Fatalf("%s link: %d %v", kind, code, out)
		}
		return out["url"].(string)
	}

	// Unset: links use the base URL, and settings say so.
	before := link("view")
	if !strings.Contains(before, "/d/"+id) || strings.HasPrefix(before, "https://slides.example.com") {
		t.Fatalf("view link before: %s", before)
	}
	base := before[:strings.Index(before, "/d/")]
	_, s := admin.do("GET", "/api/admin/settings", nil)
	if s["publicUrl"] != "" || len(s["trustedProxies"].([]any)) != 0 || s["arrival"] == nil {
		t.Fatalf("defaults: %v", s)
	}

	// Bad values are refused with a reason and change nothing.
	for _, bad := range []map[string]any{
		{"publicUrl": "slides.example.com"},
		{"publicUrl": "https://slides.example.com/slidekick"},
		{"publicUrl": "ftp://slides.example.com"},
		{"trustedProxies": []string{"0.0.0.0/0"}},
		{"trustedProxies": []string{"proxy.lan"}},
	} {
		if code, out := admin.do("PUT", "/api/admin/settings", bad); code != 422 || out["error"] == nil {
			t.Fatalf("%v accepted: %d %v", bad, code, out)
		}
	}

	// Set (with a trailing slash and upper case, normalised); fields left out
	// of a save keep their values.
	code, s = admin.do("PUT", "/api/admin/settings", map[string]any{"publicUrl": "https://Slides.Example.com/", "trustedProxies": []string{"127.0.0.1", "172.18.0.0/16"}})
	if code != 200 || s["publicUrl"] != "https://slides.example.com" {
		t.Fatalf("set: %d %v", code, s)
	}
	if _, s = admin.do("PUT", "/api/admin/settings", map[string]any{"liveSlides": true}); s["publicUrl"] != "https://slides.example.com" || len(s["trustedProxies"].([]any)) != 2 {
		t.Fatalf("an unrelated save cleared public access: %v", s)
	}

	// Links handed out use the public URL; the signed-in page learns it too.
	for _, kind := range []string{"view", "present"} {
		if u := link(kind); !strings.HasPrefix(u, "https://slides.example.com/d/"+id) {
			t.Fatalf("%s link: %s", kind, u)
		}
	}
	if _, me := admin.do("GET", "/api/me", nil); me["linkBase"] != "https://slides.example.com" {
		t.Fatalf("me: %v", me)
	}
	if _, me := e.client(t).do("GET", "/api/me", nil); me["linkBase"] != nil {
		t.Fatalf("anonymous caller learned the public URL: %v", me)
	}

	// The proxy listed in Settings is believed at once: https and the client
	// address it reports.
	via := map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "203.0.113.7"}
	_, s = admin.req("GET", "/api/admin/settings", nil, via)
	arr := s["arrival"].(map[string]any)
	if arr["trusted"] != true || arr["https"] != true || arr["client"] != "203.0.113.7" || arr["forwarded"] != true {
		t.Fatalf("arrival through trusted proxy: %v", arr)
	}
	if _, c := loginCookie(t, e.ts.URL, via, adminPW); !strings.Contains(c, "Secure") {
		t.Fatalf("login through the proxy: cookie %q", c)
	}

	// A browser on the public address passes the same-origin check.
	if code, _ := admin.req("PUT", "/api/admin/settings", map[string]any{"editorsEditAll": true}, map[string]string{"Origin": "https://slides.example.com"}); code != 200 {
		t.Fatalf("write from the public origin: %d", code)
	}
	if code, _ := admin.req("PUT", "/api/admin/settings", map[string]any{"editorsEditAll": true}, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Fatalf("write from another site: %d", code)
	}

	// Single sign-on: register the public callback first, the local one too.
	_, sso := admin.do("GET", "/api/admin/sso", nil)
	bases := sso["redirectBases"].([]any)
	if sso["redirectBase"] != "https://slides.example.com/auth/sso/" || len(bases) != 2 || bases[1] != e.ts.URL+"/auth/sso/" {
		t.Fatalf("sso bases: %v", sso)
	}

	// Removing the proxy stops believing it; clearing the URL restores links.
	_, s = admin.do("PUT", "/api/admin/settings", map[string]any{"publicUrl": "", "trustedProxies": []string{}})
	_, s = admin.req("GET", "/api/admin/settings", nil, via)
	if arr := s["arrival"].(map[string]any); arr["trusted"] != false || arr["https"] != false {
		t.Fatalf("proxy still trusted after removal: %v", arr)
	}
	if u := link("view"); !strings.HasPrefix(u, base+"/d/"+id) {
		t.Fatalf("view link after clearing: %s", u)
	}
}

// Making Slidekick public before anyone can sign in would hand the open
// instance to the internet.
func TestPublicURLNeedsAnAdminFirst(t *testing.T) {
	e := newAccServer(t, "server-token-0123456789abcdef")
	c := e.client(t)
	c.bear = "server-token-0123456789abcdef"
	if code, out := c.do("PUT", "/api/admin/settings", map[string]any{"publicUrl": "https://slides.example.com"}); code != 422 || !strings.Contains(out["error"].(string), "admin") {
		t.Fatalf("public URL before setup: %d %v", code, out)
	}
}
