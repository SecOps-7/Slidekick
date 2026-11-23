package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// Public access (Settings > General): the address people use to reach
// Slidekick from outside, through a reverse proxy or a tunnel, and the
// proxies whose forwarding headers are believed.

const (
	settingPublicURL = "public_url"      // scheme://host[:port], "" when unset
	settingProxies   = "trusted_proxies" // JSON list of addresses or CIDR ranges
	maxProxies       = 20
)

// Public is the public-access configuration, kept in memory because the
// proxy list is consulted on every request.
type Public struct {
	URL     string         // "" when unset
	Proxies []netip.Prefix // set in Settings, in addition to SLIDEKICK_TRUSTED_PROXIES
}

// Public returns the current public-access configuration.
func (s *Service) Public(ctx context.Context) Public {
	if p := s.pub.Load(); p != nil {
		return *p
	}
	p := Public{URL: s.Store.Setting(ctx, settingPublicURL)}
	var list []string
	if raw := s.Store.Setting(ctx, settingProxies); raw != "" {
		json.Unmarshal([]byte(raw), &list)
	}
	p.Proxies, _ = ParseProxies(list)
	s.pub.Store(&p)
	return p
}

// NormalizePublicURL checks the public address: http or https, a host and
// optionally a port, nothing else (Slidekick is served from the root of
// its address). "" clears it.
func NormalizePublicURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	u, err := NormalizeOrigin(raw)
	if err != nil {
		return "", fmt.Errorf("%q isn't a public URL: use the scheme and host people open, like https://slides.example.com, with no path", raw)
	}
	return u, nil
}

// ParseProxies reads proxy addresses or CIDR ranges. Trusting every address
// (0.0.0.0/0, ::/0) would let anyone fake their client address, so it is
// refused.
func ParseProxies(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range list {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		var p netip.Prefix
		if pp, err := netip.ParsePrefix(f); err == nil {
			p = netip.PrefixFrom(pp.Addr().Unmap(), pp.Bits()).Masked()
		} else if ip, err := netip.ParseAddr(f); err == nil {
			ip = ip.Unmap()
			p = netip.PrefixFrom(ip, ip.BitLen())
		} else {
			return nil, fmt.Errorf("%q isn't an IP address or range (like 192.168.1.10 or 172.18.0.0/16)", f)
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("%q would trust every address: list your proxy's address or its network", f)
		}
		out = append(out, p)
	}
	return out, nil
}

// ProxyStrings formats proxies as they are shown and stored: a bare address
// for a single host, CIDR notation for a range.
func ProxyStrings(ps []netip.Prefix) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if p.Bits() == p.Addr().BitLen() {
			out = append(out, p.Addr().String())
		} else {
			out = append(out, p.String())
		}
	}
	return out
}

// savePublic validates and stores the public-access fields; nil leaves a
// field as it is.
func (s *Service) savePublic(ctx context.Context, publicURL *string, proxies *[]string) error {
	if publicURL == nil && proxies == nil {
		return nil
	}
	next := s.Public(ctx)
	if publicURL != nil {
		v, err := NormalizePublicURL(*publicURL)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrValidation, err.Error())
		}
		if v != "" && s.SetupNeeded(ctx) {
			return fmt.Errorf("%w: create the first admin account before making Slidekick reachable from outside", ErrValidation)
		}
		next.URL = v
	}
	if proxies != nil {
		ps, err := ParseProxies(*proxies)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrValidation, err.Error())
		}
		if len(ps) > maxProxies {
			return fmt.Errorf("%w: at most %d trusted proxies", ErrValidation, maxProxies)
		}
		next.Proxies = ps
	}
	if publicURL != nil {
		if err := s.Store.SetSetting(ctx, settingPublicURL, next.URL); err != nil {
			return err
		}
	}
	if proxies != nil {
		b, _ := json.Marshal(ProxyStrings(next.Proxies))
		if err := s.Store.SetSetting(ctx, settingProxies, string(b)); err != nil {
			return err
		}
	}
	s.pub.Store(&next)
	return nil
}
