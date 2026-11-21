package cast_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/SecOps-7/slidekick/internal/cast"
	"github.com/SecOps-7/slidekick/internal/cast/casttest"
)

// closedPort is a loopback port nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

func portOf(t *testing.T, rawURL string) int {
	t.Helper()
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://"))
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A real device's answer to /setup/eureka_info?params=name,device_info
// (fields trimmed).
const eurekaJSON = `{"name":"Living room TV","device_info":{"manufacturer":"Google Inc.","model_name":"Chromecast with Google TV","ssdp_udn":"0f5a2b3c-4d5e-6f70-8192-a3b4c5d6e7f8"}}`

func eurekaHandler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/setup/eureka_info" || !strings.Contains(r.URL.Query().Get("params"), "device_info") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(eurekaJSON))
	})
}

var loop8 = netip.MustParsePrefix("127.0.0.0/29") // .1 answers, .2-.6 don't

func TestScanNamesDeviceFromEurekaHTTPS(t *testing.T) {
	tv := casttest.New(t)
	eu := httptest.NewTLSServer(eurekaHandler(t))
	defer eu.Close()
	var last, total int
	found, err := cast.Scan(context.Background(), cast.ScanOptions{Subnets: []netip.Prefix{loop8}, Port: tv.Port,
		HTTPSPort: portOf(t, eu.URL), HTTPPort: closedPort(t), Progress: func(d, n int) { last, total = d, n }})
	if err != nil {
		t.Fatal(err)
	}
	want := cast.Found{ID: "0f5a2b3c4d5e6f708192a3b4c5d6e7f8", Name: "Living room TV", Model: "Chromecast with Google TV", Host: "127.0.0.1", Port: tv.Port}
	if len(found) != 1 || found[0] != want {
		t.Fatalf("found %+v", found)
	}
	if total != 6 || last != 6 {
		t.Fatalf("progress %d/%d", last, total)
	}
	if len(tv.Launched()) != 0 {
		t.Fatal("a scan must never launch anything")
	}
}

func TestScanFallsBackToHTTPThenCastStatus(t *testing.T) {
	tv := casttest.New(t)
	eu := httptest.NewServer(eurekaHandler(t))
	defer eu.Close()
	one := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	found, _ := cast.Scan(context.Background(), cast.ScanOptions{Subnets: one, Port: tv.Port, HTTPSPort: closedPort(t), HTTPPort: portOf(t, eu.URL)})
	if len(found) != 1 || found[0].Name != "Living room TV" {
		t.Fatalf("http eureka: %+v", found)
	}
	// No setup endpoint at all: a GET_STATUS answer still identifies it.
	found, _ = cast.Scan(context.Background(), cast.ScanOptions{Subnets: one, Port: tv.Port, HTTPSPort: closedPort(t), HTTPPort: closedPort(t)})
	if len(found) != 1 || found[0].Name != "Cast device (127.0.0.1)" || found[0].ID != "" {
		t.Fatalf("status fallback: %+v", found)
	}
}

func TestScanIgnoresNonCastListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	found, err := cast.Scan(context.Background(), cast.ScanOptions{Subnets: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Port: ln.Addr().(*net.TCPAddr).Port, HTTPSPort: closedPort(t), HTTPPort: closedPort(t)})
	if err != nil || len(found) != 0 {
		t.Fatalf("found %+v %v", found, err)
	}
}

func TestScanRefusesPublicAndHugeRanges(t *testing.T) {
	for _, p := range []string{"8.8.8.0/24", "10.0.0.0/21"} {
		if _, err := cast.Scan(context.Background(), cast.ScanOptions{Subnets: []netip.Prefix{netip.MustParsePrefix(p)}}); err == nil {
			t.Fatalf("scan of %s should be refused", p)
		}
	}
}

func TestParseSubnets(t *testing.T) {
	got, err := cast.ParseSubnets(" 172.16.5.0/24, 192.168.1.7 ,172.16.5.128/25")
	if err != nil || len(got) != 2 || got[0].String() != "172.16.5.0/24" || got[1].String() != "192.168.1.7/32" {
		t.Fatalf("got %v %v", got, err)
	}
	if got, err := cast.ParseSubnets("172.16.5.0/24,10.0.1.0/24,10.0.2.0/24,10.0.3.0/24"); err != nil || len(got) != 4 {
		t.Fatalf("four /24s: %v %v", got, err)
	}
	for _, bad := range []string{"10.0.0.0/22,192.168.0.0/24", "10.0.0.0/16", "8.8.8.0/24", "172.15.0.0/24", "100.64.0.0/24", "fd00::/64", "nonsense"} {
		if _, err := cast.ParseSubnets(bad); err == nil {
			t.Fatalf("%q should be refused", bad)
		}
	}
}

func TestPlanSubnets(t *testing.T) {
	ctx := context.Background()
	ifaces := []cast.Interface{
		{Name: "eth0", Prefix: netip.MustParsePrefix("172.17.0.2/16")},   // Docker bridge, in a container
		{Name: "docker0", Prefix: netip.MustParsePrefix("10.9.0.1/16")},  // virtual
		{Name: "eno1", Prefix: netip.MustParsePrefix("192.168.1.20/16")}, // LAN, clamped to a /24
		{Name: "wlan0", Prefix: netip.MustParsePrefix("203.0.113.5/24")}, // public: never
		{Name: "eno2", Prefix: netip.MustParsePrefix("172.16.5.9/24")},   // same /24 as the base URL
	}
	got, notes, err := cast.PlanSubnets(ctx, "", "http://172.16.5.5:7410", ifaces, true)
	if err != nil || len(notes) != 0 || len(got) != 2 || got[0].String() != "172.16.5.0/24" || got[1].String() != "192.168.1.0/24" {
		t.Fatalf("got %v notes %v err %v", got, notes, err)
	}
	// A bridged container's own network is Docker's, whatever its pool: only
	// the server's LAN address (from the base URL) is searched.
	bridged := []cast.Interface{{Name: "eth0", Prefix: netip.MustParsePrefix("192.168.32.2/20")}}
	got, _, _ = cast.PlanSubnets(ctx, "", "http://172.16.5.5:7410", bridged, true)
	if len(got) != 1 || got[0].String() != "172.16.5.0/24" {
		t.Fatalf("bridged container: %v", got)
	}
	// Outside a container the 172.17 interface is a real network.
	got, _, _ = cast.PlanSubnets(ctx, "", "http://172.16.5.5:7410", ifaces[:1], false)
	if len(got) != 2 || got[1].String() != "172.17.0.0/24" {
		t.Fatalf("no container: %v", got)
	}
	// A public base URL adds nothing, and says why.
	got, notes, _ = cast.PlanSubnets(ctx, "", "http://8.8.8.8:7410", nil, true)
	if len(got) != 0 || len(notes) != 1 {
		t.Fatalf("public: %v %v", got, notes)
	}
	// The explicit list wins, and is checked.
	got, _, err = cast.PlanSubnets(ctx, "192.168.50.0/24", "http://172.16.5.5:7410", ifaces, true)
	if err != nil || len(got) != 1 || got[0].String() != "192.168.50.0/24" {
		t.Fatalf("explicit: %v %v", got, err)
	}
	if _, _, err := cast.PlanSubnets(ctx, "0.0.0.0/0", "", nil, true); err == nil {
		t.Fatal("0.0.0.0/0 must be refused")
	}
	// Too many derived networks are trimmed to the cap.
	many := []cast.Interface{}
	for i := range 6 {
		many = append(many, cast.Interface{Name: "eno" + strconv.Itoa(i), Prefix: netip.MustParsePrefix("10.1." + strconv.Itoa(i) + ".1/24")})
	}
	got, notes, _ = cast.PlanSubnets(ctx, "", "", many, false)
	if len(got) != 4 || len(notes) != 2 {
		t.Fatalf("cap: %v %v", got, notes)
	}
}

func TestNormalizeID(t *testing.T) {
	for in, want := range map[string]string{
		"uuid:0F5A2B3C-4D5E-6F70-8192-A3B4C5D6E7F8": "0f5a2b3c4d5e6f708192a3b4c5d6e7f8",
		"0f5a2b3c4d5e6f708192a3b4c5d6e7f8":          "0f5a2b3c4d5e6f708192a3b4c5d6e7f8",
		"not an id":                                 "",
	} {
		if got := cast.NormalizeID(in); got != want {
			t.Fatalf("%q: %q", in, got)
		}
	}
}
