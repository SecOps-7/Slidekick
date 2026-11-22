package server_test

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SecOps-7/slidekick/internal/localtls"
	"github.com/SecOps-7/slidekick/internal/server"
)

// frameHeaders fetches a page anonymously and returns its X-Frame-Options and
// the frame-ancestors part of its CSP ("" when the page sends no CSP).
func frameHeaders(t *testing.T, base, path string) (xfo, ancestors string) {
	t.Helper()
	res, err := http.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	for _, d := range strings.Split(res.Header.Get("Content-Security-Policy"), ";") {
		if d = strings.TrimSpace(d); strings.HasPrefix(d, "frame-ancestors") {
			ancestors = strings.TrimSpace(strings.TrimPrefix(d, "frame-ancestors"))
		}
	}
	return res.Header.Get("X-Frame-Options"), ancestors
}

// neverFramed are pages that must stay unframeable whatever the setting says.
var neverFramed = []string{
	"/d/d_abc/present",           // presenter view
	"/d/d_abc/present?p=key.sig", // presenter link
	"/d/d_abc?p=key.sig",         // a presenter key on the viewer path
	"/d/d_abc/edit",              // editor
	"/d/d_abc/print",
	"/login", "/setup", "/settings", "/", "/healthz", "/api/me",
}

func TestEmbedViewerLinks(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	editor := e.addUser(t, admin, "edna", "editor")
	viewer := "/d/d_abc?k=0123456789abcdef01234567"

	denyAll := func(stage string) {
		t.Helper()
		for _, p := range append([]string{viewer, "/d/d_abc"}, neverFramed...) {
			xfo, anc := frameHeaders(t, e.ts.URL, p)
			if xfo != "DENY" || (anc != "" && anc != "'none'") {
				t.Errorf("%s: %s is frameable (X-Frame-Options %q, frame-ancestors %q)", stage, p, xfo, anc)
			}
		}
	}
	denyAll("off by default")
	if code, out := admin.do("GET", "/api/admin/settings", nil); code != 200 || out["embedViewer"] != false {
		t.Fatalf("defaults: %d %v", code, out)
	}

	// Only admins change it; on needs at least one origin; origins are checked.
	if code, _ := editor.do("PUT", "/api/admin/settings", map[string]any{"embedViewer": true, "embedOrigins": []string{"https://dash.example"}}); code != 403 {
		t.Fatalf("editor: %d", code)
	}
	if code, _ := admin.do("PUT", "/api/admin/settings", map[string]any{"embedViewer": true, "embedOrigins": []string{}}); code != 422 {
		t.Fatalf("on without origins: %d", code)
	}
	for _, bad := range []string{"https://*.example", "https://dash.example/path", "javascript:alert(1)", "https://dash.example; script-src *",
		"dash.example", "ftp://dash.example", "https://user@dash.example", "https://dash.example:99999", "'self'", "*"} {
		if code, _ := admin.do("PUT", "/api/admin/settings", map[string]any{"embedViewer": true, "embedOrigins": []string{bad}}); code != 422 {
			t.Errorf("origin %q accepted: %d", bad, code)
		}
	}
	denyAll("after rejected saves")

	code, out := admin.do("PUT", "/api/admin/settings", map[string]any{"embedViewer": true,
		"embedOrigins": []string{"https://dash.example:8443", "HTTPS://Board.Example:443/", "http://192.168.1.20:3000"}})
	if code != 200 {
		t.Fatalf("enable: %d %v", code, out)
	}
	if got := strings.Join(toStrings(out["embedOrigins"]), " "); got != "https://dash.example:8443 https://board.example http://192.168.1.20:3000" {
		t.Fatalf("stored origins %q", got)
	}
	for _, p := range []string{viewer, "/d/d_abc", "/d/d_abc/", "/d/d_abc?loop=10"} {
		xfo, anc := frameHeaders(t, e.ts.URL, p)
		if xfo != "" || anc != "https://dash.example:8443 https://board.example http://192.168.1.20:3000" {
			t.Errorf("viewer %s: X-Frame-Options %q, frame-ancestors %q", p, xfo, anc)
		}
	}
	// The rest of the page's CSP is untouched.
	res, _ := http.Get(e.ts.URL + viewer)
	res.Body.Close()
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "object-src 'none'") {
		t.Errorf("viewer CSP lost directives: %q", csp)
	}
	for _, p := range neverFramed {
		if xfo, anc := frameHeaders(t, e.ts.URL, p); xfo != "DENY" || (anc != "" && anc != "'none'") {
			t.Errorf("on: %s is frameable (X-Frame-Options %q, frame-ancestors %q)", p, xfo, anc)
		}
	}

	// Removing a site takes effect on the next request.
	admin.do("PUT", "/api/admin/settings", map[string]any{"embedViewer": true, "embedOrigins": []string{"https://dash.example:8443"}})
	if _, anc := frameHeaders(t, e.ts.URL, viewer); anc != "https://dash.example:8443" {
		t.Errorf("after removing sites: frame-ancestors %q", anc)
	}
	// Switching it off takes effect at once and keeps the list for later.
	code, out = admin.do("PUT", "/api/admin/settings", map[string]any{"embedViewer": false, "embedOrigins": []string{"https://dash.example:8443"}})
	if code != 200 || len(toStrings(out["embedOrigins"])) != 1 {
		t.Fatalf("disable: %d %v", code, out)
	}
	denyAll("switched off")
	// The editors setting still saves alongside.
	if code, out := admin.do("PUT", "/api/admin/settings", map[string]any{"editorsEditAll": true}); code != 200 || out["editorsEditAll"] != true {
		t.Fatalf("editorsEditAll: %d %v", code, out)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestLocalTLSRoutes(t *testing.T) {
	var mgr *localtls.Manager
	e := newAccServerWith(t, "", func(o *server.Options) {
		mgr = &localtls.Manager{Store: o.Accounts.Store, Box: o.Accounts.Box, Addr: "127.0.0.1:0"}
		o.LocalTLS = mgr
	})
	mgr.Handler = e.ts.Config.Handler
	t.Cleanup(mgr.Close)
	admin := e.withAdmin(t)
	editor := e.addUser(t, admin, "edna", "editor")

	// Off by default; nothing to download; only admins see or change it.
	if code, out := admin.do("GET", "/api/admin/tls", nil); code != 200 || out["enabled"] != false || out["listening"] != false {
		t.Fatalf("default: %d %v", code, out)
	}
	if code, _ := editor.do("GET", "/api/admin/tls", nil); code != 403 {
		t.Fatalf("editor read: %d", code)
	}
	if code, _ := editor.do("PUT", "/api/admin/tls", map[string]any{"enabled": true, "names": []string{"127.0.0.1"}}); code != 403 {
		t.Fatalf("editor write: %d", code)
	}
	if res, _ := http.Get(e.ts.URL + "/tls/ca.crt"); res.StatusCode != 404 {
		t.Fatalf("CA before any: %d", res.StatusCode)
	}
	// Public addresses are refused: LAN only.
	if code, out := admin.do("PUT", "/api/admin/tls", map[string]any{"enabled": true, "names": []string{"8.8.8.8"}}); code != 422 {
		t.Fatalf("public IP: %d %v", code, out)
	}

	code, out := admin.do("PUT", "/api/admin/tls", map[string]any{"enabled": true, "mode": "generate", "names": []string{"127.0.0.1", "slides.lan"}})
	if code != 200 || out["listening"] != true || out["ca"] == nil {
		t.Fatalf("enable: %d %v", code, out)
	}
	res, err := http.Get(e.ts.URL + "/tls/ca.crt")
	if err != nil || res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), "slidekick-local-ca.crt") {
		t.Fatalf("CA download: %v %v", err, res)
	}
	caPEM, _ := io.ReadAll(res.Body)
	res.Body.Close()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("CA download isn't a certificate")
	}

	// The same app over HTTPS, trusted through the downloaded CA. Requests
	// that arrive over TLS count as https (Secure cookies), with no proxy.
	hc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	r2, err := hc.Get("https://" + mgr.BoundAddr() + "/healthz")
	if err != nil || r2.StatusCode != 200 {
		t.Fatalf("https healthz: %v %v", err, r2)
	}
	r2.Body.Close()
	body := strings.NewReader(`{"username":"boss","password":"` + adminPW + `"}`)
	r3, err := hc.Post("https://"+mgr.BoundAddr()+"/api/login", "application/json", body)
	if err != nil || r3.StatusCode != 200 {
		t.Fatalf("https login: %v %v", err, r3)
	}
	r3.Body.Close()
	secure := false
	for _, c := range r3.Cookies() {
		if c.Name == "sk_session" {
			secure = c.Secure
		}
	}
	if !secure {
		t.Error("session cookie over local HTTPS isn't Secure")
	}
	// Plain HTTP stays available (proxies and cast TVs use it).
	if r4, err := http.Get(e.ts.URL + "/healthz"); err != nil || r4.StatusCode != 200 {
		t.Fatalf("http after enabling https: %v", err)
	}

	// Off again: the listener stops and the CA is still offered for devices.
	addr := mgr.BoundAddr()
	if code, out := admin.do("PUT", "/api/admin/tls", map[string]any{"enabled": false, "mode": "generate", "names": []string{"127.0.0.1"}}); code != 200 || out["listening"] != false {
		t.Fatalf("disable: %d %v", code, out)
	}
	if _, err := hc.Get("https://" + addr + "/healthz"); err == nil {
		t.Fatal("still serving https after switching it off")
	}
}
