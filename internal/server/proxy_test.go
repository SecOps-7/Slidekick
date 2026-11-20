package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/SecOps-7/slidekick/internal/server"
)

func loginCookie(t *testing.T, base string, hdr map[string]string, pw string) (int, string) {
	return loginAs(t, base, hdr, "boss", pw)
}

func loginAs(t *testing.T, base string, hdr map[string]string, user, pw string) (int, string) {
	b, _ := json.Marshal(map[string]string{"username": user, "password": pw})
	req, _ := http.NewRequest("POST", base+"/api/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode, res.Header.Get("Set-Cookie")
}

// Decision 2026-11-20: Slidekick sits behind TLS on the LAN; sign-in cookies
// are Secure when the request came in over https through the trusted proxy.
func TestSecureCookiesBehindTrustedProxy(t *testing.T) {
	trusted, _ := server.ParseTrustedProxies("127.0.0.1")
	e := newAccServerWith(t, "", func(o *server.Options) { o.TrustedProxies = trusted })
	e.withAdmin(t)
	https := map[string]string{"X-Forwarded-Proto": "https", "X-Real-IP": "172.16.5.20"}
	if _, c := loginCookie(t, e.ts.URL, https, adminPW); !strings.Contains(c, "Secure") || !strings.Contains(c, "HttpOnly") {
		t.Fatalf("https via trusted proxy: cookie %q", c)
	}
	// Plain http on the LAN (no proxy) keeps working, without the Secure flag.
	if _, c := loginCookie(t, e.ts.URL, nil, adminPW); c == "" || strings.Contains(c, "Secure") {
		t.Fatalf("plain http: cookie %q", c)
	}
	// Lockouts follow the real client the proxy reports, not the proxy itself.
	// (Guessing another username, so only the address lockout is in play.)
	for range 10 {
		loginAs(t, e.ts.URL, map[string]string{"X-Real-IP": "172.16.5.66"}, "nobody", "wrong-password-xx")
	}
	if code, _ := loginCookie(t, e.ts.URL, map[string]string{"X-Real-IP": "172.16.5.66"}, adminPW); code != 429 {
		t.Fatalf("attacker behind proxy not locked out: %d", code)
	}
	if code, _ := loginCookie(t, e.ts.URL, map[string]string{"X-Real-IP": "172.16.5.20"}, adminPW); code != 200 {
		t.Fatalf("other client behind proxy locked out too: %d", code)
	}
}

func TestForwardedHeadersIgnoredFromOthers(t *testing.T) {
	trusted, _ := server.ParseTrustedProxies("10.9.9.9/32")
	e := newAccServerWith(t, "", func(o *server.Options) { o.TrustedProxies = trusted })
	e.withAdmin(t)
	if _, c := loginCookie(t, e.ts.URL, map[string]string{"X-Forwarded-Proto": "https"}, adminPW); strings.Contains(c, "Secure") {
		t.Fatalf("untrusted peer claimed https: %q", c)
	}
	// A client can't dodge the lockout by inventing addresses.
	for i := range 10 {
		loginAs(t, e.ts.URL, map[string]string{"X-Real-IP": "10.1.1." + string(rune('1'+i))}, "nobody", "wrong-password-xx")
	}
	if code, _ := loginCookie(t, e.ts.URL, map[string]string{"X-Real-IP": "10.1.2.3"}, adminPW); code != 429 {
		t.Fatalf("spoofed X-Real-IP escaped the lockout: %d", code)
	}
	if _, err := server.ParseTrustedProxies("not-an-ip"); err == nil {
		t.Fatal("bad proxy address accepted")
	}
}
