package render

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestPublicAddr(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.10", "169.254.169.254", "100.64.0.1",
		"0.0.0.0", "::1", "fe80::1", "fc00::1", "::ffff:192.168.1.1", "64:ff9b::a00:1", "224.0.0.1", "198.18.0.1"}
	for _, s := range blocked {
		if publicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

// A page rendered through the proxy can reach the Slidekick server itself but
// not other local or private services.
func TestProxyBlocksInternalTargets(t *testing.T) {
	self := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "slidekick") }))
	defer self.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "secret") }))
	defer other.Close()

	selfURL, _ := url.Parse(self.URL)
	p, err := startProxy(selfURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	proxyURL, _ := url.Parse("http://" + p.addr())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	res, err := client.Get(self.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "slidekick" {
		t.Fatalf("self request through proxy: %d %q", res.StatusCode, body)
	}

	res, err = client.Get(other.URL)
	if err == nil {
		body, _ = io.ReadAll(res.Body)
		res.Body.Close()
		if strings.Contains(string(body), "secret") {
			t.Fatal("proxy let a request reach another local service")
		}
	}

	// CONNECT tunnels to private addresses are refused too.
	conn, err := net.Dial("tcp", p.addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "CONNECT 10.0.0.1:443 HTTP/1.1\r\nHost: 10.0.0.1:443\r\n\r\n")
	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "403") {
		t.Fatalf("CONNECT to private address: %q", buf[:n])
	}
}
