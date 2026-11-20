package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ValidateBaseURL accepts http or https URLs with a host, no userinfo, no
// query and no fragment. Private/LAN addresses are allowed on purpose (an
// admin sets them); the caller decides who may set URLs.
func ValidateBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("llm: base URL is empty")
	}
	if strings.ContainsAny(raw, "?#") {
		return nil, errors.New("llm: base URL must not have a query or fragment")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("llm: base URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		u.Scheme = strings.ToLower(u.Scheme)
	default:
		return nil, errors.New("llm: base URL must use http or https")
	}
	if u.Opaque != "" {
		return nil, errors.New("llm: base URL is not absolute")
	}
	if u.User != nil {
		return nil, errors.New("llm: base URL must not contain credentials")
	}
	if u.Hostname() == "" {
		return nil, errors.New("llm: base URL has no host")
	}
	if ps := u.Port(); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("llm: base URL has an invalid port")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("llm: base URL has an empty port")
	}
	return u, nil
}

// origin is the only scheme+host+port a client may reach.
type origin struct {
	scheme string
	host   string // lower-case hostname or IP literal, no brackets
	port   string
}

func originOf(u *url.URL) origin {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return origin{scheme: strings.ToLower(u.Scheme), host: strings.ToLower(u.Hostname()), port: port}
}

func (o origin) matches(u *url.URL) bool {
	return u != nil && originOf(u) == o
}

// NewClient returns an http.Client that can only reach p.BaseURL's
// scheme+host+port. Requests to anything else fail before a connection is
// made; every dial is re-checked against the host and port and goes to an
// address the allowed host resolved to; redirects are refused; environment
// proxies are ignored. Dial timeout 10s, response-header timeout 60s, no
// overall timeout (streams are long).
func NewClient(p Provider) (*http.Client, error) {
	u, err := ValidateBaseURL(p.BaseURL)
	if err != nil {
		return nil, err
	}
	o := originOf(u)
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	resolver := net.DefaultResolver

	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(host, o.host) || port != o.port {
			return nil, fmt.Errorf("llm: dial to %s refused: only %s is allowed", addr, net.JoinHostPort(o.host, o.port))
		}
		var ips []net.IP
		if ip := net.ParseIP(host); ip != nil {
			ips = []net.IP{ip}
		} else {
			addrs, err := resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, a := range addrs {
				ips = append(ips, a.IP)
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("llm: %s did not resolve", host)
		}
		// Dial the resolved address itself, so nothing can re-resolve the
		// name to a different host between check and connect.
		var lastErr error
		for _, ip := range ips {
			c, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return c, nil
			}
			lastErr = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, lastErr
	}

	tr := &http.Transport{
		Proxy:                 nil, // never route through an environment proxy
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          8,
	}
	return &http.Client{
		Transport: &originGuard{allowed: o, next: tr},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("llm: redirects are not followed")
		},
	}, nil
}

// originGuard refuses requests to any other origin before the transport sees them.
type originGuard struct {
	allowed origin
	next    http.RoundTripper
}

func (g *originGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if !g.allowed.matches(req.URL) {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, fmt.Errorf("llm: request to %s://%s refused: only %s://%s is allowed",
			req.URL.Scheme, req.URL.Host, g.allowed.scheme, net.JoinHostPort(g.allowed.host, g.allowed.port))
	}
	return g.next.RoundTrip(req)
}
