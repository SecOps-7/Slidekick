package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SecOps-7/slidekick/internal/cast"
	"github.com/SecOps-7/slidekick/internal/cast/casttest"
	"github.com/SecOps-7/slidekick/internal/service"
)

// nextCastEvent waits for the next {"type":"cast"} event on a deck's hub.
func nextCastEvent(t *testing.T, ch <-chan []byte) map[string]any {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case b := <-ch:
			var m map[string]any
			json.Unmarshal(b, &m)
			if m["type"] == "cast" {
				return m
			}
		case <-timeout:
			t.Fatal("no cast event")
		}
	}
}

func TestCastRoutes(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	owner := e.addUser(t, admin, "edna", "editor")
	other := e.addUser(t, admin, "eddie", "editor")
	view := e.addUser(t, admin, "vera", "viewer")
	tv := casttest.New(t)

	// Only admins manage devices.
	for _, c := range []*client{owner, view} {
		if code, _ := c.do("POST", "/api/admin/cast/devices", map[string]any{"name": "Sneaky", "host": "172.16.5.9"}); code != 403 {
			t.Fatalf("non-admin add device: %d", code)
		}
	}
	if code, out := admin.do("POST", "/api/admin/cast/devices", map[string]any{"name": "Bad", "host": "http://172.16.5.9/x"}); code != 422 {
		t.Fatalf("bad host: %d %v", code, out)
	}
	code, out := admin.do("POST", "/api/admin/cast/devices", map[string]any{"name": "Living room TV", "host": tv.Host, "port": tv.Port})
	if code != 200 || out["port"].(float64) != float64(tv.Port) {
		t.Fatalf("add device: %d %v", code, out)
	}
	devID := out["id"].(string)
	code, out = admin.do("GET", "/api/admin/cast/devices", nil)
	if code != 200 || len(out["devices"].([]any)) != 1 {
		t.Fatalf("admin list: %d %v", code, out)
	}

	// Editors see device names, not addresses; viewers see nothing.
	code, out = owner.do("GET", "/api/cast/devices", nil)
	if code != 200 || strings.Contains(fmt.Sprint(out), tv.Host) || !strings.Contains(fmt.Sprint(out), "Living room TV") {
		t.Fatalf("editor list: %d %v", code, out)
	}
	if code, _ := view.do("GET", "/api/cast/devices", nil); code != 403 {
		t.Fatalf("viewer list: %d", code)
	}

	_, out = owner.do("POST", "/api/decks", map[string]any{"title": "Quarterly", "slides": oneSlide})
	id := out["deckId"].(string)
	key := e.env.Svc.ViewKey(context.Background(), id)
	if key == "" {
		t.Fatal("new decks should be private, with a view key")
	}

	// Viewers and editors who don't own the deck can't cast it.
	for name, c := range map[string]*client{"viewer": view, "other editor": other} {
		if code, _ := c.do("POST", "/api/decks/"+id+"/cast", map[string]any{"deviceId": devID}); code != 403 {
			t.Fatalf("%s cast: %d", name, code)
		}
		if code, _ := c.do("DELETE", "/api/decks/"+id+"/cast", nil); code != 403 {
			t.Fatalf("%s stop: %d", name, code)
		}
	}
	if len(tv.Launched()) != 0 {
		t.Fatal("a refused cast reached the device")
	}

	ch, cancel := e.env.Svc.Hub.Subscribe(id)
	defer cancel()
	code, out = owner.do("POST", "/api/decks/"+id+"/cast", map[string]any{"deviceId": devID})
	if code != 200 {
		t.Fatalf("owner cast: %d %v", code, out)
	}
	ev := nextCastEvent(t, ch)
	if ev["state"] != "started" || ev["device"] != "Living room TV" || ev["actor"] != "user:edna" {
		t.Fatalf("started event %v", ev)
	}
	loaded := tv.Loaded()
	if len(loaded) != 1 {
		t.Fatalf("loaded %v", loaded)
	}
	want := "http://test/d/" + id + "/present?k=" + key + "&cast=1"
	if loaded[0]["url"] != want {
		t.Fatalf("cast URL %v, want %s", loaded[0]["url"], want)
	}

	code, out = owner.do("GET", "/api/decks/"+id+"/cast", nil)
	if c, _ := out["casting"].(map[string]any); code != 200 || c["device"] != "Living room TV" || c["startedBy"] != "user:edna" {
		t.Fatalf("status: %d %v", code, out)
	}

	// Admins may stop anyone's cast.
	if code, out := admin.do("DELETE", "/api/decks/"+id+"/cast", nil); code != 200 {
		t.Fatalf("stop: %d %v", code, out)
	}
	if ev := nextCastEvent(t, ch); ev["state"] != "stopped" || ev["device"] != "Living room TV" {
		t.Fatalf("stopped event %v", ev)
	}
	if len(tv.Stopped()) != 1 {
		t.Fatalf("device stopped %v", tv.Stopped())
	}
	// Stopping again is harmless.
	if code, _ := owner.do("DELETE", "/api/decks/"+id+"/cast", nil); code != 200 {
		t.Fatalf("second stop: %d", code)
	}

	// Unknown device: a clear 422, nothing sent.
	if code, out := owner.do("POST", "/api/decks/"+id+"/cast", map[string]any{"deviceId": "nope"}); code != 422 || !strings.Contains(out["error"].(string), "Living room TV") {
		t.Fatalf("unknown device: %d %v", code, out)
	}

	if code, _ := admin.do("DELETE", "/api/admin/cast/devices/"+devID, nil); code != 200 {
		t.Fatalf("remove: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/admin/cast/devices/"+devID, nil); code != 404 {
		t.Fatalf("remove twice: %d", code)
	}
}

func TestStartCastServiceAPI(t *testing.T) {
	e := newAccServer(t, "")
	tv := casttest.New(t)
	svc := e.env.Svc
	ctx := context.Background()
	if _, err := svc.AddCastDevice(ctx, "Office TV", tv.Addr(), 0); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Create(ctx, "Talk", "", "", oneSlide, "agent")
	if err != nil {
		t.Fatal(err)
	}
	devs, err := svc.CastDevices(ctx)
	if err != nil || len(devs) != 1 || devs[0].Port != tv.Port {
		t.Fatalf("devices %v %v", devs, err)
	}
	// The AI presenter names devices the way people do.
	if err := svc.StartCast(ctx, res.DeckID, "office tv", "ai:presenter"); err != nil {
		t.Fatal(err)
	}
	if s := svc.CastStatus(res.DeckID); s == nil || s.StartedBy != "ai:presenter" {
		t.Fatalf("status %+v", s)
	}
	if err := svc.StopCast(ctx, res.DeckID); err != nil {
		t.Fatal(err)
	}
	if err := svc.StopCast(ctx, res.DeckID); err == nil {
		t.Fatal("want ErrNotCasting")
	}
	// A localhost base URL can't work for a TV: say so instead of casting a dead link.
	svc.BaseURL = "http://localhost:7410"
	if err := svc.StartCast(ctx, res.DeckID, "Office TV", "agent"); err == nil || !strings.Contains(err.Error(), "SLIDEKICK_BASE_URL") {
		t.Fatalf("localhost base URL: %v", err)
	}
}

func TestFoundCastDeviceRoutes(t *testing.T) {
	e := newAccServer(t, "")
	admin := e.withAdmin(t)
	owner := e.addUser(t, admin, "edna", "editor")
	view := e.addUser(t, admin, "vera", "viewer")
	tv := casttest.New(t)
	const devID = "0f5a2b3c4d5e6f708192a3b4c5d6e7f8"
	e.env.Svc.CastDiscovery = service.CastDiscoveryConfig{Find: func(ctx context.Context, progress func(int, int)) ([]cast.Found, error) {
		return []cast.Found{{ID: devID, Name: "Kitchen", Model: "Chromecast", Host: tv.Host, Port: tv.Port}}, nil
	}}
	_, out := owner.do("POST", "/api/decks", map[string]any{"title": "Quarterly", "slides": oneSlide})
	id := out["deckId"].(string)

	// Nothing found yet: the menu is empty. Viewers can't refresh.
	if code, _ := view.do("POST", "/api/decks/"+id+"/cast/refresh", nil); code != 403 {
		t.Fatalf("viewer refresh: %d", code)
	}
	code, out := owner.do("POST", "/api/decks/"+id+"/cast/refresh", nil)
	devs, _ := out["devices"].([]any)
	if code != 200 || len(devs) != 1 || strings.Contains(fmt.Sprint(out), tv.Host) {
		t.Fatalf("refresh: %d %v", code, out)
	}
	d := devs[0].(map[string]any)
	if d["id"] != "auto-"+devID || d["name"] != "Kitchen" || d["auto"] != true {
		t.Fatalf("found device %v", d)
	}
	// An editor casts to it with no admin step.
	if code, out := owner.do("POST", "/api/decks/"+id+"/cast", map[string]any{"deviceId": "auto-" + devID}); code != 200 {
		t.Fatalf("cast: %d %v", code, out)
	}
	if len(tv.Loaded()) != 1 {
		t.Fatal("not loaded")
	}
	owner.do("DELETE", "/api/decks/"+id+"/cast", nil)

	// Admin view: found list with discovery status.
	if code, _ := owner.do("POST", "/api/admin/cast/discover", nil); code != 403 {
		t.Fatalf("editor discover: %d", code)
	}
	code, out = admin.do("POST", "/api/admin/cast/discover", nil)
	if code != 200 || len(out["devices"].([]any)) != 1 || out["discovery"].(map[string]any)["found"].(float64) != 1 {
		t.Fatalf("discover: %d %v", code, out)
	}
	if code, _ := owner.do("POST", "/api/admin/cast/found/auto-"+devID+"/hide", nil); code != 403 {
		t.Fatalf("editor hide: %d", code)
	}
	if code, _ := admin.do("POST", "/api/admin/cast/found/auto-"+devID+"/hide", nil); code != 200 {
		t.Fatalf("hide: %d", code)
	}
	if _, out := owner.do("GET", "/api/decks/"+id+"/cast", nil); len(out["devices"].([]any)) != 0 {
		t.Fatalf("hidden but listed: %v", out)
	}
	code, out = admin.do("GET", "/api/admin/cast/devices", nil)
	if f := out["found"].([]any); code != 200 || len(f) != 1 || f[0].(map[string]any)["hidden"] != true || len(out["devices"].([]any)) != 0 {
		t.Fatalf("admin list: %d %v", code, out)
	}
	admin.do("DELETE", "/api/admin/cast/found/auto-"+devID+"/hide", nil)
	code, out = admin.do("POST", "/api/admin/cast/found/auto-"+devID+"/save", map[string]any{"name": "Kitchen TV"})
	if code != 200 || out["name"] != "Kitchen TV" || out["deviceId"] != devID {
		t.Fatalf("save: %d %v", code, out)
	}
	_, out = owner.do("GET", "/api/decks/"+id+"/cast", nil)
	if devs := out["devices"].([]any); len(devs) != 1 || devs[0].(map[string]any)["name"] != "Kitchen TV" || devs[0].(map[string]any)["auto"] != nil {
		t.Fatalf("after save: %v", out)
	}
	if code, _ := admin.do("POST", "/api/admin/cast/found/auto-ffff/save", nil); code != 404 {
		t.Fatalf("save unknown: %d", code)
	}
}
