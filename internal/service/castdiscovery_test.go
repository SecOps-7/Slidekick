package service_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SecOps-7/slidekick/internal/cast"
	"github.com/SecOps-7/slidekick/internal/cast/casttest"
	"github.com/SecOps-7/slidekick/internal/service"
	"github.com/SecOps-7/slidekick/internal/store"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

const (
	idKitchen = "0f5a2b3c4d5e6f708192a3b4c5d6e7f8"
	idOffice  = "11112222333344445555666677778888"
)

// finder is a fake discovery whose answer the test changes between runs.
type finder struct {
	calls atomic.Int32
	found atomic.Value // []cast.Found
}

func (f *finder) set(fs ...cast.Found) { f.found.Store(fs) }

func (f *finder) find(ctx context.Context, progress func(int, int)) ([]cast.Found, error) {
	f.calls.Add(1)
	progress(1, 1)
	fs, _ := f.found.Load().([]cast.Found)
	return fs, nil
}

func discoveryEnv(t *testing.T) (*service.Service, *finder) {
	env := testenv.New(t)
	f := &finder{}
	f.set()
	env.Svc.CastDiscovery = service.CastDiscoveryConfig{Find: f.find}
	return env.Svc, f
}

func byID(ds []store.CastDevice) map[string]store.CastDevice {
	m := map[string]store.CastDevice{}
	for _, d := range ds {
		m[d.ID] = d
	}
	return m
}

func TestDiscoveryMergesDedupesAndFollowsMoves(t *testing.T) {
	svc, f := discoveryEnv(t)
	// The office TV was saved by IP before discovery existed.
	office, err := svc.AddCastDevice(ctx, "Office", "192.168.1.30", 0)
	if err != nil {
		t.Fatal(err)
	}
	// mDNS and the scan both see the kitchen TV (the scan by UDN, with
	// dashes); the scan also finds the office TV and an unnamed receiver.
	f.set(
		cast.Found{ID: idKitchen, Name: "Kitchen", Model: "Chromecast", Host: "192.168.1.20", Port: 8009},
		cast.Found{ID: "0F5A2B3C-4D5E-6F70-8192-A3B4C5D6E7F8", Name: "Kitchen", Host: "192.168.1.20", Port: 8009},
		cast.Found{ID: idOffice, Name: "Office TV", Model: "Google TV", Host: "192.168.1.30", Port: 8009},
		cast.Found{Name: "Cast device (192.168.1.40)", Host: "192.168.1.40", Port: 8009},
	)
	if _, err := svc.RefreshCastDevices(ctx, true); err != nil {
		t.Fatal(err)
	}
	ds, err := svc.CastDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := byID(ds)
	if len(ds) != 3 || ds[0].ID != office.ID || ds[0].Auto || ds[0].LastSeen == nil {
		t.Fatalf("devices %+v", ds)
	}
	if k := m["auto-"+idKitchen]; !k.Auto || k.Name != "Kitchen" || k.Model != "Chromecast" || k.Host != "192.168.1.20" {
		t.Fatalf("kitchen %+v", k)
	}
	if _, ok := m["auto-ip-192-168-1-40"]; !ok {
		t.Fatalf("unnamed receiver missing: %+v", ds)
	}
	// The saved office TV learned its device id...
	saved, _ := svc.CastDevices(ctx)
	if saved[0].DeviceID != idOffice || saved[0].Model != "Google TV" {
		t.Fatalf("office not learned: %+v", saved[0])
	}

	// ...so when DHCP moves both TVs, the saved one follows and the found one
	// is the same device at its new address (no duplicates).
	f.set(
		cast.Found{ID: idKitchen, Name: "Kitchen", Host: "192.168.1.21", Port: 8009},
		cast.Found{ID: idOffice, Name: "Office TV", Host: "192.168.1.31", Port: 8009},
	)
	svc.RefreshCastDevices(ctx, true)
	ds, _ = svc.CastDevices(ctx)
	m = byID(ds)
	if len(ds) != 3 || m[office.ID].Host != "192.168.1.31" || m["auto-"+idKitchen].Host != "192.168.1.21" || m["auto-"+idKitchen].Model != "Chromecast" {
		t.Fatalf("after move %+v", ds)
	}

	// Another device taking over an address replaces whatever was there.
	f.set(cast.Found{ID: idOffice, Name: "Office TV", Host: "192.168.1.40", Port: 8009})
	svc.RefreshCastDevices(ctx, true)
	found, _ := svc.FoundCastDevices(ctx)
	for _, d := range found {
		if d.ID == "auto-ip-192-168-1-40" {
			t.Fatalf("stale address entry kept: %+v", found)
		}
	}
}

func TestHideAndSaveFoundDevices(t *testing.T) {
	svc, f := discoveryEnv(t)
	f.set(cast.Found{ID: idKitchen, Name: "Kitchen", Host: "192.168.1.20", Port: 8009},
		cast.Found{ID: idOffice, Name: "Office TV", Model: "Google TV", Host: "192.168.1.30", Port: 8009})
	svc.RefreshCastDevices(ctx, true)

	if err := svc.HideFoundCastDevice(ctx, "auto-"+idKitchen, true); err != nil {
		t.Fatal(err)
	}
	ds, _ := svc.CastDevices(ctx)
	if len(ds) != 1 || ds[0].Name != "Office TV" {
		t.Fatalf("hidden device still listed: %+v", ds)
	}
	if err := svc.StartCast(ctx, "d_nope", "Kitchen", "agent"); err == nil {
		t.Fatal("cast to a hidden device")
	}
	found, _ := svc.FoundCastDevices(ctx)
	if len(found) != 2 || !found[0].Hidden || found[0].Name != "Kitchen" {
		t.Fatalf("found %+v", found)
	}
	// Hiding survives a rescan (it is stored); showing again works.
	svc.RefreshCastDevices(ctx, true)
	if ds, _ := svc.CastDevices(ctx); len(ds) != 1 {
		t.Fatalf("hidden after rescan: %+v", ds)
	}
	if err := svc.HideFoundCastDevice(ctx, "auto-"+idKitchen, false); err != nil {
		t.Fatal(err)
	}
	if ds, _ := svc.CastDevices(ctx); len(ds) != 2 {
		t.Fatalf("unhidden: %+v", ds)
	}
	if err := svc.HideFoundCastDevice(ctx, "auto-ffff", true); err != store.ErrNotFound {
		t.Fatalf("hide unknown: %v", err)
	}

	// Saving pins it under a new name; the found entry isn't listed twice.
	d, err := svc.SaveFoundCastDevice(ctx, "auto-"+idOffice, "Boardroom")
	if err != nil || d.Name != "Boardroom" || d.DeviceID != idOffice || d.Model != "Google TV" {
		t.Fatalf("save %+v %v", d, err)
	}
	ds, _ = svc.CastDevices(ctx)
	if len(ds) != 2 || ds[0].Name != "Boardroom" || ds[0].Auto || !ds[1].Auto {
		t.Fatalf("after save %+v", ds)
	}
	if _, err := svc.SaveFoundCastDevice(ctx, "auto-"+idOffice, ""); err == nil || !strings.Contains(err.Error(), "Boardroom") {
		t.Fatalf("save twice: %v", err)
	}
}

func TestStartCastWithFoundDevice(t *testing.T) {
	svc, f := discoveryEnv(t)
	tv := casttest.New(t)
	f.set(cast.Found{ID: idKitchen, Name: "Kitchen", Host: tv.Host, Port: tv.Port})
	svc.RefreshCastDevices(ctx, true)
	res, err := svc.Create(ctx, "Talk", "", "", nil, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.StartCast(ctx, res.DeckID, "auto-"+idKitchen, "user:edna"); err != nil {
		t.Fatal(err)
	}
	if s := svc.CastStatus(res.DeckID); s == nil || s.DeviceID != "auto-"+idKitchen || s.Name != "Kitchen" {
		t.Fatalf("status %+v", s)
	}
	if len(tv.Loaded()) != 1 {
		t.Fatalf("loaded %v", tv.Loaded())
	}
	// By name, the way the AI presenter asks.
	if err := svc.StartCast(ctx, res.DeckID, "kitchen", "ai:presenter"); err != nil {
		t.Fatal(err)
	}
	svc.StopCast(ctx, res.DeckID)
}

func TestDiscoveryOffSwitch(t *testing.T) {
	svc, f := discoveryEnv(t)
	f.set(cast.Found{ID: idKitchen, Name: "Kitchen", Host: "192.168.1.20", Port: 8009})
	svc.CastDiscovery.Off = true
	svc.CastDiscovery.Delay = time.Millisecond
	bg, cancel := context.WithCancel(ctx)
	defer cancel()
	svc.StartCastDiscovery(bg)
	// A cast-menu refresh doesn't scan either.
	if st, _ := svc.RefreshCastDevices(ctx, false); st.Enabled {
		t.Fatal("status should say off")
	}
	time.Sleep(30 * time.Millisecond)
	if n := f.calls.Load(); n != 0 {
		t.Fatalf("discovery ran %d times while off", n)
	}
	// Admins may still scan by hand; results don't reach cast menus.
	svc.RefreshCastDevices(ctx, true)
	if ds, _ := svc.CastDevices(ctx); len(ds) != 0 {
		t.Fatalf("off but listed %+v", ds)
	}
	if found, _ := svc.FoundCastDevices(ctx); len(found) != 1 {
		t.Fatalf("admin view %+v", found)
	}
}

func TestDiscoveryBackgroundAndThrottle(t *testing.T) {
	svc, f := discoveryEnv(t)
	svc.CastDiscovery.Delay = time.Millisecond
	svc.CastDiscovery.Every = time.Hour
	bg, cancel := context.WithCancel(ctx)
	defer cancel()
	svc.StartCastDiscovery(bg)
	deadline := time.Now().Add(2 * time.Second)
	for f.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	for svc.CastDiscoveryStatus().LastRun == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("background runs: %d", f.calls.Load())
	}
	// Cast-menu refreshes right after a run reuse it.
	st, _ := svc.RefreshCastDevices(ctx, false)
	if f.calls.Load() != 1 || !st.Enabled || st.LastRun == nil {
		t.Fatalf("throttle: %d calls, %+v", f.calls.Load(), st)
	}
}
