package cast

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Subnet scanning finds Cast devices where mDNS can't: from a container on
// Docker's default bridge network multicast never reaches the LAN, but
// ordinary outgoing TCP does. Every address in a few private /24s gets a
// short TCP connect to port 8009; whatever answers is asked who it is.
//
// Naming a device uses the setup endpoint the Google Home app reads,
// /setup/eureka_info, as pychromecast does (pychromecast/dial.py,
// _get_eureka_info): HTTPS on 8443 with a self-signed certificate first,
// then plain HTTP on 8008, with ?params=... selecting the sections. It is the
// one setup endpoint that still answers without the local authorization
// token (most others return 403 since 2019). Its JSON carries "name" and a
// "device_info" object with "model_name", "manufacturer" and "ssdp_udn" (a
// UUID; with the dashes removed it equals the id in the mDNS TXT record).
// Devices that don't answer it (or answer without a name) are confirmed with
// a Cast v2 GET_STATUS instead and named after their address.

// MaxScanAddrs caps how many addresses one scan probes in total.
const MaxScanAddrs = 1024

// ScanOptions tunes Scan. Zero values pick the defaults; the ports exist so
// tests can point the scan at fakes.
type ScanOptions struct {
	Subnets     []netip.Prefix
	Port        int           // Cast port to probe (8009)
	HTTPPort    int           // eureka_info over HTTP (8008)
	HTTPSPort   int           // eureka_info over HTTPS (8443)
	DialTimeout time.Duration // per address (400 ms)
	InfoTimeout time.Duration // per identification attempt (1.5 s)
	Concurrency int           // parallel probes (64)
	// Progress, when set, is called after each address with how many of
	// how many are done.
	Progress func(done, total int)
}

func (o *ScanOptions) defaults() {
	if o.Port <= 0 {
		o.Port = DefaultPort
	}
	if o.HTTPPort <= 0 {
		o.HTTPPort = 8008
	}
	if o.HTTPSPort <= 0 {
		o.HTTPSPort = 8443
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 400 * time.Millisecond
	}
	if o.InfoTimeout <= 0 {
		o.InfoTimeout = 1500 * time.Millisecond
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 64
	}
}

// scannable is the guard every probed address passes: private IPv4 only
// (loopback too, which never leaves the machine and is what tests use).
func scannable(a netip.Addr) bool {
	return a.Is4() && (a.IsPrivate() || a.IsLoopback())
}

// prefixPrivate reports whether the whole prefix lies in private (or
// loopback) IPv4 space.
func prefixPrivate(p netip.Prefix) bool {
	p = p.Masked()
	if !p.Addr().Is4() {
		return false
	}
	return scannable(p.Addr()) && scannable(lastAddr(p))
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().As4()
	n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	n |= (1 << (32 - p.Bits())) - 1
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

func prefixSize(p netip.Prefix) int { return 1 << (32 - p.Bits()) }

// hostsOf lists a prefix's addresses, leaving out the network and broadcast
// addresses of anything larger than a /31.
func hostsOf(p netip.Prefix) []netip.Addr {
	p = p.Masked()
	var out []netip.Addr
	last := lastAddr(p)
	for a := p.Addr(); ; a = a.Next() {
		if p.Bits() >= 31 || (a != p.Addr() && a != last) {
			out = append(out, a)
		}
		if a == last {
			break
		}
	}
	return out
}

// ParseSubnets reads a comma-separated list of IPv4 CIDRs (a bare address
// means that one host). Public ranges and lists over MaxScanAddrs addresses
// are refused.
func ParseSubnets(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var p netip.Prefix
		var err error
		if strings.Contains(part, "/") {
			p, err = netip.ParsePrefix(part)
		} else {
			var a netip.Addr
			a, err = netip.ParseAddr(part)
			p = netip.PrefixFrom(a, 32)
		}
		if err != nil || !p.Addr().Is4() {
			return nil, fmt.Errorf("%q isn't an IPv4 subnet like 192.168.1.0/24", part)
		}
		if !prefixPrivate(p) {
			return nil, fmt.Errorf("%s isn't a private network range: only 10.0.0.0/8, 172.16.0.0/12 and 192.168.0.0/16 are scanned", p)
		}
		out = append(out, p.Masked())
	}
	out = dedupePrefixes(out)
	if n := countAddrs(out); n > MaxScanAddrs {
		return nil, fmt.Errorf("those subnets hold %d addresses; the most Slidekick scans is %d (four /24s)", n, MaxScanAddrs)
	}
	return out, nil
}

func countAddrs(ps []netip.Prefix) int {
	n := 0
	for _, p := range ps {
		n += prefixSize(p)
	}
	return n
}

// dedupePrefixes drops prefixes contained in another one.
func dedupePrefixes(ps []netip.Prefix) []netip.Prefix {
	sort.Slice(ps, func(i, j int) bool { return ps[i].Bits() < ps[j].Bits() })
	var out []netip.Prefix
	for _, p := range ps {
		inside := false
		for _, o := range out {
			if o.Contains(p.Addr()) {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr().Less(out[j].Addr()) })
	return out
}

// around24 is the /24 holding a private IPv4 address.
func around24(a netip.Addr) (netip.Prefix, bool) {
	a = a.Unmap()
	if !a.Is4() || !a.IsPrivate() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(a, 24).Masked(), true
}

// Interface is one local address, for PlanSubnets.
type Interface struct {
	Name   string
	Prefix netip.Prefix // address with its mask
}

// virtualIface matches interfaces that never face the LAN the TVs are on.
func virtualIface(name string) bool {
	for _, p := range []string{"lo", "docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "vxlan", "tun", "tap", "wg", "tailscale", "zt", "kube"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// dockerPool is where Docker puts bridge networks by default.
var dockerPool = netip.MustParsePrefix("172.16.0.0/12")

// PlanSubnets decides what to scan: the explicit list when there is one
// (SLIDEKICK_CAST_SUBNETS), else the /24 around the server's LAN address
// (the host in the base URL, resolved if it is a name) plus the /24s of the
// machine's own LAN-facing interfaces. In a bridged container only the base
// URL counts; with host networking, Docker's 172.16.0.0/12 pool is skipped.
// Notes says why candidates were left out.
func PlanSubnets(ctx context.Context, explicit, baseURL string, ifaces []Interface, inContainer bool) (subnets []netip.Prefix, notes []string, err error) {
	if strings.TrimSpace(explicit) != "" {
		ps, err := ParseSubnets(explicit)
		return ps, nil, err
	}
	var out []netip.Prefix
	if u, err := url.Parse(baseURL); err == nil && u.Hostname() != "" {
		h := u.Hostname()
		var addrs []netip.Addr
		if a, err := netip.ParseAddr(h); err == nil {
			addrs = append(addrs, a)
		} else if h != "localhost" && !strings.HasSuffix(h, ".localhost") {
			lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			ips, _ := net.DefaultResolver.LookupNetIP(lctx, "ip4", h)
			cancel()
			addrs = append(addrs, ips...)
		}
		for _, a := range addrs {
			if p, ok := around24(a); ok {
				out = append(out, p)
			} else {
				notes = append(notes, fmt.Sprintf("the base URL's address %s isn't a private LAN address, so its network isn't scanned", a))
			}
		}
	}
	// In a bridged container the interfaces are Docker's own networks (any
	// address pool, e.g. 192.168.32.0/20), not the LAN the server is on, so
	// only the base URL's address says where that is. With host networking the
	// container sees the host's interfaces, Docker's virtual ones included.
	if inContainer && !hostNetwork(ifaces) {
		ifaces = nil
	}
	for _, i := range ifaces {
		a := i.Prefix.Addr().Unmap()
		if virtualIface(i.Name) || !a.Is4() || (inContainer && dockerPool.Contains(a)) {
			continue
		}
		p := i.Prefix.Masked()
		if p.Bits() < 24 {
			p = netip.PrefixFrom(a, 24).Masked()
		}
		if prefixPrivate(p) {
			out = append(out, p)
		}
	}
	out = dedupePrefixes(out)
	for countAddrs(out) > MaxScanAddrs {
		notes = append(notes, fmt.Sprintf("left out %s: a scan covers at most %d addresses (set SLIDEKICK_CAST_SUBNETS to choose)", out[len(out)-1], MaxScanAddrs))
		out = out[:len(out)-1]
	}
	return out, notes, nil
}

// hostNetwork reports whether the interfaces are the host's own: only there
// are Docker's bridge (docker0, br-*) and veth interfaces visible.
func hostNetwork(ifaces []Interface) bool {
	for _, i := range ifaces {
		if i.Name == "docker0" || strings.HasPrefix(i.Name, "br-") || strings.HasPrefix(i.Name, "veth") {
			return true
		}
	}
	return false
}

// LocalInterfaces lists this machine's IPv4 interface addresses.
func LocalInterfaces() []Interface {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Interface
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(n.IP.To4()); ok && n.IP.To4() != nil {
					ones, _ := n.Mask.Size()
					out = append(out, Interface{Name: i.Name, Prefix: netip.PrefixFrom(ip, ones)})
				}
			}
		}
	}
	return out
}

// InContainer guesses whether this process runs in a Docker container.
func InContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// Scan probes every address in the subnets and returns the Cast devices
// among them, sorted by name.
func Scan(ctx context.Context, opt ScanOptions) ([]Found, error) {
	opt.defaults()
	var targets []netip.Addr
	for _, p := range opt.Subnets {
		if !prefixPrivate(p) {
			return nil, fmt.Errorf("cast: refusing to scan %s: not a private range", p)
		}
		targets = append(targets, hostsOf(p)...)
	}
	if len(targets) > MaxScanAddrs {
		return nil, fmt.Errorf("cast: refusing to scan %d addresses (at most %d)", len(targets), MaxScanAddrs)
	}
	var (
		mu    sync.Mutex
		found []Found
		done  atomic.Int64
		wg    sync.WaitGroup
		next  = make(chan netip.Addr)
		total = len(targets)
	)
	for range min(opt.Concurrency, max(total, 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range next {
				if f, ok := probe(ctx, a, opt); ok {
					mu.Lock()
					found = append(found, f)
					mu.Unlock()
				}
				n := done.Add(1)
				if opt.Progress != nil {
					opt.Progress(int(n), total)
				}
			}
		}()
	}
feed:
	for _, a := range targets {
		select {
		case next <- a:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if found == nil {
		found = []Found{}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found, ctx.Err()
}

// probe checks one address: is port 8009 open, and is it a Cast device?
func probe(ctx context.Context, a netip.Addr, opt ScanOptions) (Found, bool) {
	if !scannable(a) || ctx.Err() != nil {
		return Found{}, false
	}
	host := a.String()
	d := net.Dialer{Timeout: opt.DialTimeout}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(opt.Port)))
	if err != nil {
		return Found{}, false
	}
	c.Close()
	return Identify(ctx, host, opt)
}

// eurekaInfo is the part of /setup/eureka_info used here.
type eurekaInfo struct {
	Name       string `json:"name"`
	SSDPUDN    string `json:"ssdp_udn"`
	DeviceInfo struct {
		ModelName    string `json:"model_name"`
		Manufacturer string `json:"manufacturer"`
		SSDPUDN      string `json:"ssdp_udn"`
	} `json:"device_info"`
}

var eurekaClient = &http.Client{
	Transport: &http.Transport{
		// The setup endpoint presents a self-signed certificate (pychromecast
		// also turns verification off for it). It only names the device.
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, //nolint:gosec // see above
		DisableKeepAlives: true,
		Proxy:             nil, // never send LAN probes through a proxy
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func fetchEureka(ctx context.Context, rawURL string, timeout time.Duration) (*eurekaInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := eurekaClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("eureka_info: %s", resp.Status)
	}
	var info eurekaInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// NormalizeID turns a UDN ("uuid:aa-bb..." or "aabb...") into the form the
// mDNS TXT id uses: lower-case hex without dashes.
func NormalizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "uuid:")
	s = strings.ReplaceAll(s, "-", "")
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return ""
		}
	}
	return s
}

// Identify names the Cast device at host: from eureka_info when it answers,
// else by a Cast GET_STATUS round trip. ok is false when it isn't a Cast
// receiver at all.
func Identify(ctx context.Context, host string, opt ScanOptions) (Found, bool) {
	opt.defaults()
	f := Found{Host: host, Port: opt.Port}
	const path = "/setup/eureka_info?params=name,device_info"
	for _, u := range []string{
		"https://" + net.JoinHostPort(host, strconv.Itoa(opt.HTTPSPort)) + path,
		"http://" + net.JoinHostPort(host, strconv.Itoa(opt.HTTPPort)) + path,
	} {
		info, err := fetchEureka(ctx, u, opt.InfoTimeout)
		if err != nil || strings.TrimSpace(info.Name) == "" {
			continue
		}
		f.Name = strings.TrimSpace(info.Name)
		f.Model = strings.TrimSpace(info.DeviceInfo.ModelName)
		udn := info.DeviceInfo.SSDPUDN
		if udn == "" {
			udn = info.SSDPUDN
		}
		f.ID = NormalizeID(udn)
		return f, true
	}
	// No setup endpoint: a GET_STATUS answer still proves it's a receiver.
	cctx, cancel := context.WithTimeout(ctx, 2*opt.InfoTimeout)
	defer cancel()
	c, err := Dial(cctx, f.Addr())
	if err != nil {
		return Found{}, false
	}
	defer c.Close()
	if _, err := c.Status(cctx); err != nil {
		return Found{}, false
	}
	f.Name = "Cast device (" + host + ")"
	return f, true
}

// Addr is host:port.
func (f Found) Addr() string { return Device{Host: f.Host, Port: f.Port}.Addr() }

// ErrNothingToScan means no subnet could be worked out.
var ErrNothingToScan = errors.New("no private LAN subnet to scan: set SLIDEKICK_CAST_SUBNETS (like 192.168.1.0/24) or SLIDEKICK_BASE_URL to the server's LAN address")
