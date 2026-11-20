package server

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

type httpsKey struct{}

// isHTTPS reports whether the request reached Slidekick over https: directly
// over TLS, or through a trusted proxy that said so.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	v, _ := r.Context().Value(httpsKey{}).(bool)
	return v
}

func (s *server) trusted(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range s.opt.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// forwardedClient picks the client address a trusted proxy reports:
// X-Real-IP, else the last entry of X-Forwarded-For (the one the proxy added).
func forwardedClient(r *http.Request) (netip.Addr, bool) {
	if ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
		return ip.Unmap(), true
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
			return ip.Unmap(), true
		}
	}
	return netip.Addr{}, false
}

// proxyHeaders believes forwarding headers only from a trusted proxy. From
// anyone else they are ignored, so a LAN client can't fake its address (to dodge
// the lockout) or claim https.
func (s *server) proxyHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.opt.TrustedProxies) == 0 || !s.trusted(r.RemoteAddr) {
			if r.Header.Get("X-Forwarded-Proto") != "" || r.Header.Get("X-Real-IP") != "" {
				s.noteUntrustedProxy(r.RemoteAddr)
			}
			next.ServeHTTP(w, r)
			return
		}
		if ip, ok := forwardedClient(r); ok {
			r.RemoteAddr = net.JoinHostPort(ip.String(), "0")
		}
		if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
			r = r.WithContext(context.WithValue(r.Context(), httpsKey{}, true))
		}
		next.ServeHTTP(w, r)
	})
}

var untrustedSeen sync.Map

// noteUntrustedProxy logs, once per address, a peer that sends forwarding
// headers without being trusted: usually a newly set-up proxy whose address
// belongs in SLIDEKICK_TRUSTED_PROXIES.
func (s *server) noteUntrustedProxy(remote string) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	if _, seen := untrustedSeen.LoadOrStore(host, true); seen || s.opt.Log == nil {
		return
	}
	s.opt.Log.Info("ignoring forwarding headers from an untrusted peer; if this is your TLS proxy, add it to SLIDEKICK_TRUSTED_PROXIES", "peer", host)
}

// ParseTrustedProxies reads a comma-separated list of addresses or CIDR ranges.
func ParseTrustedProxies(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
		if p, err := netip.ParsePrefix(f); err == nil {
			out = append(out, p.Masked())
			continue
		}
		ip, err := netip.ParseAddr(f)
		if err != nil {
			return nil, err
		}
		ip = ip.Unmap()
		out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
	}
	return out, nil
}
