package cast_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/SecOps-7/slidekick/internal/cast"
	"github.com/SecOps-7/slidekick/internal/cast/casttest"
)

func dev(f *casttest.Device, name string) cast.Device {
	return cast.Device{ID: "c_" + name, Name: name, Host: f.Host, Port: f.Port}
}

func TestStartLoadsURLAndStop(t *testing.T) {
	f := casttest.New(t)
	m := cast.NewManager()
	ctx := context.Background()
	url := "http://192.168.1.10:7410/d/d_abc/present?k=key&cast=1"
	s, displaced, err := m.Start(ctx, "d_abc", dev(f, "Living room TV"), url, "user:edna")
	if err != nil {
		t.Fatal(err)
	}
	if len(displaced) != 0 || s.Name != "Living room TV" || s.StartedBy != "user:edna" || s.DeckID != "d_abc" {
		t.Fatalf("session %+v displaced %v", s, displaced)
	}
	if got := f.Launched(); len(got) != 1 || got[0] != cast.DashCastAppID {
		t.Fatalf("launched %v", got)
	}
	loaded := f.Loaded()
	if len(loaded) != 1 {
		t.Fatalf("loaded %v", loaded)
	}
	l := loaded[0]
	if l["_namespace"] != cast.DashCastNamespace || l["url"] != url || l["force"] != true || l["reload"] != false {
		t.Fatalf("DashCast message %v", l)
	}
	if st := m.Status("d_abc"); st == nil || st.Name != "Living room TV" {
		t.Fatalf("status %+v", st)
	}

	if _, err := m.Stop(ctx, "d_abc"); err != nil {
		t.Fatal(err)
	}
	if got := f.Stopped(); len(got) != 1 || f.Running() != "" {
		t.Fatalf("stopped %v, running %q", got, f.Running())
	}
	if m.Status("d_abc") != nil {
		t.Fatal("session should be gone")
	}
	if _, err := m.Stop(ctx, "d_abc"); err != cast.ErrNotCasting {
		t.Fatalf("second stop: %v", err)
	}
}

func TestStartDisplacesOtherDeckOnSameDevice(t *testing.T) {
	f := casttest.New(t)
	m := cast.NewManager()
	ctx := context.Background()
	if _, _, err := m.Start(ctx, "d_one", dev(f, "TV"), "http://lan/d/d_one/present?cast=1", "agent"); err != nil {
		t.Fatal(err)
	}
	_, displaced, err := m.Start(ctx, "d_two", dev(f, "TV"), "http://lan/d/d_two/present?cast=1", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if len(displaced) != 1 || displaced[0].DeckID != "d_one" || m.Status("d_one") != nil {
		t.Fatalf("displaced %v", displaced)
	}
	// DashCast was already running: one launch reused, two URLs loaded.
	if n := len(f.Loaded()); n != 2 {
		t.Fatalf("loads: %d", n)
	}
}

func TestMovingDeckStopsOldDevice(t *testing.T) {
	a, b := casttest.New(t), casttest.New(t)
	m := cast.NewManager()
	ctx := context.Background()
	if _, _, err := m.Start(ctx, "d_x", dev(a, "Kitchen"), "http://lan/x", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Start(ctx, "d_x", dev(b, "Office"), "http://lan/x", "agent"); err != nil {
		t.Fatal(err)
	}
	if a.Running() != "" || b.Running() != cast.DashCastAppID {
		t.Fatalf("kitchen %q office %q", a.Running(), b.Running())
	}
}

func TestLaunchErrorAndUnreachable(t *testing.T) {
	f := casttest.New(t)
	f.LaunchError = "NOT_ALLOWED"
	m := cast.NewManager()
	ctx := context.Background()
	if _, _, err := m.Start(ctx, "d_x", dev(f, "TV"), "http://lan/x", "agent"); err == nil || !strings.Contains(err.Error(), "NOT_ALLOWED") {
		t.Fatalf("want launch error, got %v", err)
	}
	// A closed port fails fast instead of hanging.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().(*net.TCPAddr)
	ln.Close()
	start := time.Now()
	_, _, err := m.Start(ctx, "d_x", cast.Device{Name: "Gone", Host: "127.0.0.1", Port: addr.Port}, "http://lan/x", "agent")
	if err == nil || time.Since(start) > 6*time.Second {
		t.Fatalf("unreachable: %v after %s", err, time.Since(start))
	}
	if m.Status("d_x") != nil {
		t.Fatal("failed start must not leave a session")
	}
}

func TestClientStatusAndHeartbeat(t *testing.T) {
	f := casttest.New(t)
	ctx := context.Background()
	c, err := cast.Dial(ctx, f.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	st, err := c.Status(ctx)
	if err != nil || len(st.Applications) != 0 {
		t.Fatalf("status %v %v", st, err)
	}
	app, err := c.Launch(ctx, "CC1AD845")
	if err != nil || app.TransportID == "" || app.SessionID == "" {
		t.Fatalf("launch %+v %v", app, err)
	}
	if err := c.Stop(ctx, "nope"); err == nil {
		t.Fatal("stopping an unknown session should fail")
	}
}

func TestDiscoverDegradesGracefully(t *testing.T) {
	// Whatever the network looks like here (often no multicast at all), it
	// must return quickly and never fail outright.
	start := time.Now()
	found, err := cast.Discover(context.Background(), 300*time.Millisecond)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if found == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("found %v after %s", found, time.Since(start))
	}
}
