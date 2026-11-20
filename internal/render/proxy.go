package render

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// Headless Chromium renders deck content written by agents, so it must not
// become a way to read the server's network. Every request it makes goes
// through this forward proxy, which only lets connections out to public IP
// addresses, plus the Slidekick server itself. The check runs on the address
// actually dialled, so DNS rebinding can't sneak a private address past it.

var errBlocked = errors.New("blocked: private or internal address")

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 can reach IPv4 internals
}

// publicAddr reports whether ip is a globally routable unicast address.
func publicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

type egressProxy struct {
	self   string // host:port of the Slidekick server, always allowed
	dialer *net.Dialer
	srv    *http.Server
	ln     net.Listener
}

func startProxy(self string) (*egressProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &egressProxy{self: self, ln: ln}
	p.dialer = &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			if address == p.self {
				return nil
			}
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !publicAddr(ap.Addr()) {
				return errBlocked
			}
			return nil
		},
	}
	p.srv = &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}
	go p.srv.Serve(ln)
	return p, nil
}

func (p *egressProxy) addr() string { return p.ln.Addr().String() }

func (p *egressProxy) close() { p.srv.Close() }

func (p *egressProxy) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	// Only http(s) ports to keep the browser from being used as a port scanner.
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if addr != p.self && port != "443" && port != "80" {
		return nil, errBlocked
	}
	return p.dialer.DialContext(ctx, network, addr)
}

func (p *egressProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.tunnel(w, r)
		return
	}
	if r.URL.Scheme != "http" || r.URL.Host == "" {
		http.Error(w, "unsupported", http.StatusBadRequest)
		return
	}
	host := r.URL.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	tr := &http.Transport{
		DialContext:     func(ctx context.Context, n, _ string) (net.Conn, error) { return p.dial(ctx, n, host) },
		Proxy:           nil,
		IdleConnTimeout: 30 * time.Second,
	}
	defer tr.CloseIdleConnections()
	out := r.Clone(r.Context())
	out.RequestURI = ""
	for _, h := range []string{"Proxy-Connection", "Proxy-Authorization", "Connection", "Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		out.Header.Del(h)
	}
	res, err := tr.RoundTrip(out)
	if err != nil {
		http.Error(w, "blocked or unreachable", http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	for k, vs := range res.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
}

func (p *egressProxy) tunnel(w http.ResponseWriter, r *http.Request) {
	dst, err := p.dial(r.Context(), "tcp", r.Host)
	if err != nil {
		http.Error(w, "blocked or unreachable", http.StatusForbidden)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		dst.Close()
		http.Error(w, "no hijack", http.StatusInternalServerError)
		return
	}
	src, buf, err := hj.Hijack()
	if err != nil {
		dst.Close()
		return
	}
	src.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	if buf.Reader.Buffered() > 0 {
		io.CopyN(dst, buf, int64(buf.Reader.Buffered()))
	}
	go func() {
		io.Copy(dst, src)
		dst.Close()
	}()
	io.Copy(src, dst)
	src.Close()
}
