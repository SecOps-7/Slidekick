package service

import (
	"context"
	"log/slog"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SecOps-7/slidekick/internal/cast"
	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/store"
)

// Automatic cast discovery: in the background (shortly after start, then
// every ten minutes) and on demand, the server looks for Cast devices with
// mDNS and with a scan of its LAN subnets (see cast.Scan), and keeps what
// it found in memory. Found devices appear in cast menus as "auto-…"
// devices without an admin step; admins can save them (pin, rename) or
// hide them.

// CastDiscoveryConfig sets up discovery. The zero value runs it with the
// defaults but scans no subnets (mDNS only).
type CastDiscoveryConfig struct {
	// Off stops the background runs and keeps found devices out of cast
	// menus (SLIDEKICK_CAST_DISCOVERY=off). Admins can still scan by hand.
	Off bool
	// Subnets are scanned besides mDNS (see cast.PlanSubnets); Notes say
	// what was left out and why.
	Subnets []netip.Prefix
	Notes   []string
	// Delay before the first background run (20 s) and the time between
	// runs (10 min).
	Delay, Every time.Duration
	// Scan carries port and timeout overrides (tests).
	Scan cast.ScanOptions
	// Find replaces mDNS plus scan (tests).
	Find func(ctx context.Context, progress func(done, total int)) ([]cast.Found, error)
	Log  *slog.Logger
}

// foundExpiry drops devices discovery hasn't seen for this long.
const foundExpiry = 72 * time.Hour

// menuRefreshEvery limits how often cast-menu refreshes rescan.
const menuRefreshEvery = 15 * time.Second

type foundDevice struct {
	cast.Found
	key       string
	firstSeen time.Time
	lastSeen  time.Time
}

type castDiscovery struct {
	mu          sync.Mutex
	found       map[string]*foundDevice // by key
	running     chan struct{}           // closed when the current run ends; nil when idle
	done, total int
	lastStart   time.Time
	lastEnd     time.Time
	lastErr     string
	lastCount   int
}

// CastDiscoveryStatus describes discovery for the settings page.
type CastDiscoveryStatus struct {
	Enabled     bool       `json:"enabled"`
	Running     bool       `json:"running"`
	Done        int        `json:"done"`
	Total       int        `json:"total"`
	LastRun     *time.Time `json:"lastRun,omitempty"`
	TookSeconds float64    `json:"tookSeconds,omitempty"`
	Found       int        `json:"found"`
	Subnets     []string   `json:"subnets"`
	Notes       []string   `json:"notes,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// foundKey is a found device's identity: its own id when it has one, else
// its address.
func foundKey(f cast.Found) string {
	if id := cast.NormalizeID(f.ID); id != "" {
		return id
	}
	return "ip:" + f.Host
}

// autoID is the device id cast menus and StartCast use for a found device.
func autoID(key string) string {
	if h, ok := strings.CutPrefix(key, "ip:"); ok {
		return "auto-ip-" + strings.ReplaceAll(h, ".", "-")
	}
	return "auto-" + key
}

// keyFromAutoID reverses autoID.
func keyFromAutoID(id string) (string, bool) {
	rest, ok := strings.CutPrefix(id, "auto-")
	if !ok || rest == "" {
		return "", false
	}
	if h, ok := strings.CutPrefix(rest, "ip-"); ok {
		return "ip:" + strings.ReplaceAll(h, "-", "."), true
	}
	return rest, true
}

func (s *Service) discLog() *slog.Logger {
	if s.CastDiscovery.Log != nil {
		return s.CastDiscovery.Log
	}
	return slog.Default()
}

// StartCastDiscovery runs discovery in the background until ctx ends. It
// does nothing when discovery is off.
func (s *Service) StartCastDiscovery(ctx context.Context) {
	cfg := s.CastDiscovery
	if cfg.Off {
		s.discLog().Info("cast discovery is off (SLIDEKICK_CAST_DISCOVERY=off): only saved devices appear in cast menus")
		return
	}
	delay, every := cfg.Delay, cfg.Every
	if delay <= 0 {
		delay = 20 * time.Second
	}
	if every <= 0 {
		every = 10 * time.Minute
	}
	go func() {
		t := time.NewTimer(delay)
		defer t.Stop()
		first := true
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			st, err := s.RefreshCastDevices(ctx, true)
			if err == nil && (first || st.Error != "") {
				s.discLog().Info("cast discovery", "found", st.Found, "subnets", st.Subnets, "took_s", st.TookSeconds, "error", st.Error)
			}
			first = false
			t.Reset(every)
		}
	}()
}

// RefreshCastDevices looks for devices now and returns when done. A run
// already going is joined rather than repeated. Without force (cast-menu
// refreshes), a run that ended moments ago is reused, and nothing runs at
// all while discovery is off.
func (s *Service) RefreshCastDevices(ctx context.Context, force bool) (CastDiscoveryStatus, error) {
	d := &s.disc
	d.mu.Lock()
	if ch := d.running; ch != nil {
		d.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return s.CastDiscoveryStatus(), ctx.Err()
		}
		return s.CastDiscoveryStatus(), nil
	}
	if !force && (s.CastDiscovery.Off || time.Since(d.lastEnd) < menuRefreshEvery) {
		d.mu.Unlock()
		return s.CastDiscoveryStatus(), nil
	}
	ch := make(chan struct{})
	d.running, d.done, d.total, d.lastStart = ch, 0, 0, time.Now()
	d.mu.Unlock()

	// The run outlives a cancelled request: others may be waiting on it.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	found, err := s.findCast(rctx, func(done, total int) {
		d.mu.Lock()
		d.done, d.total = done, total
		d.mu.Unlock()
	})
	seen := s.mergeFound(found, time.Now())
	if err == nil {
		s.learnSavedDevices(rctx)
	}

	d.mu.Lock()
	d.running = nil
	d.lastEnd = time.Now()
	d.lastCount = seen
	d.lastErr = ""
	if err != nil {
		d.lastErr = err.Error()
	}
	d.mu.Unlock()
	close(ch)
	return s.CastDiscoveryStatus(), nil
}

// findCast runs mDNS and the subnet scan side by side.
func (s *Service) findCast(ctx context.Context, progress func(done, total int)) ([]cast.Found, error) {
	cfg := s.CastDiscovery
	if cfg.Find != nil {
		return cfg.Find(ctx, progress)
	}
	var mdns []cast.Found
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mdns, _ = cast.Discover(ctx, 3*time.Second)
	}()
	var scanned []cast.Found
	var err error
	if len(cfg.Subnets) > 0 {
		opt := cfg.Scan
		opt.Subnets = cfg.Subnets
		opt.Progress = progress
		scanned, err = cast.Scan(ctx, opt)
	}
	wg.Wait()
	if len(cfg.Subnets) == 0 && len(mdns) == 0 {
		err = cast.ErrNothingToScan
	}
	return append(mdns, scanned...), err
}

// mergeFound folds a run's results into what discovery knows and returns
// how many distinct devices the run saw.
func (s *Service) mergeFound(found []cast.Found, now time.Time) int {
	d := &s.disc
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.found == nil {
		d.found = map[string]*foundDevice{}
	}
	seenHosts := map[string]string{} // host -> key seen there this run
	for _, f := range found {
		if f.Host == "" {
			continue
		}
		if f.Port <= 0 {
			f.Port = cast.DefaultPort
		}
		f.ID = cast.NormalizeID(f.ID)
		k := foundKey(f)
		if f.ID == "" {
			// Seen without an id (scan fallback): if a device with an id is
			// known at this address, it's that one.
			for ok, o := range d.found {
				if o.ID != "" && o.Host == f.Host {
					k = ok
					f.ID = o.ID
					break
				}
			}
		} else {
			delete(d.found, "ip:"+f.Host) // the same device, now with its id
		}
		e := d.found[k]
		if e == nil {
			e = &foundDevice{key: k, firstSeen: now}
			d.found[k] = e
		} else {
			// Keep what an earlier answer knew that this one doesn't.
			if f.Model == "" {
				f.Model = e.Model
			}
			if strings.HasPrefix(f.Name, "Cast device (") && e.Name != "" {
				f.Name = e.Name
			}
		}
		e.Found = f
		e.lastSeen = now
		seenHosts[f.Host] = k
	}
	for k, e := range d.found {
		// A device that moved away from an address another device now uses,
		// or one not seen for days, is gone.
		if other, ok := seenHosts[e.Host]; (ok && other != k) || now.Sub(e.lastSeen) > foundExpiry {
			delete(d.found, k)
		}
	}
	return len(seenHosts)
}

func (s *Service) foundSnapshot() []foundDevice {
	d := &s.disc
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]foundDevice, 0, len(d.found))
	for _, e := range d.found {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].key < out[j].key
	})
	return out
}

// matchSaved finds the saved device a found one is: by device id, else by
// address.
func matchSaved(saved []*store.CastDevice, f foundDevice) *store.CastDevice {
	if f.ID != "" {
		for _, sd := range saved {
			if sd.DeviceID == f.ID {
				return sd
			}
		}
	}
	for _, sd := range saved {
		if strings.EqualFold(sd.Host, f.Host) && sd.Port == f.Port && (sd.DeviceID == "" || f.ID == "") {
			return sd
		}
	}
	return nil
}

// learnSavedDevices follows saved devices to their new address and records
// the device id of devices saved by IP, so a later move is followed too.
func (s *Service) learnSavedDevices(ctx context.Context) {
	saved, err := s.store.CastDevices(ctx)
	if err != nil {
		return
	}
	for _, f := range s.foundSnapshot() {
		sd := matchSaved(saved, f)
		if sd == nil || f.ID == "" {
			continue
		}
		if sd.Host != f.Host || sd.Port != f.Port || sd.DeviceID == "" || (sd.Model == "" && f.Model != "") {
			if err := s.store.UpdateCastDeviceAddr(ctx, sd.ID, f.Host, f.Port, f.ID, f.Model); err != nil {
				s.discLog().Warn("cast discovery: updating a saved device", "device", sd.Name, "err", err)
			} else if sd.Host != f.Host {
				s.discLog().Info("cast device moved", "device", sd.Name, "host", f.Host)
			}
		}
	}
}

// CastDiscoveryStatus reports the last (or current) run.
func (s *Service) CastDiscoveryStatus() CastDiscoveryStatus {
	cfg := s.CastDiscovery
	d := &s.disc
	d.mu.Lock()
	defer d.mu.Unlock()
	st := CastDiscoveryStatus{Enabled: !cfg.Off, Running: d.running != nil, Done: d.done, Total: d.total,
		Found: d.lastCount, Subnets: []string{}, Notes: cfg.Notes, Error: d.lastErr}
	for _, p := range cfg.Subnets {
		st.Subnets = append(st.Subnets, p.String())
	}
	if !d.lastEnd.IsZero() {
		t := d.lastEnd.UTC()
		st.LastRun = &t
		if !st.Running {
			st.TookSeconds = float64(d.lastEnd.Sub(d.lastStart).Milliseconds()) / 1000
		}
	}
	return st
}

// DiscoveredDevice is a device discovery found, for the settings page.
type DiscoveredDevice struct {
	cast.Found
	// ID (shadowing Found.ID) is the "auto-…" id; DeviceID the device's own.
	ID       string    `json:"id"`
	DeviceID string    `json:"deviceId,omitempty"`
	LastSeen time.Time `json:"lastSeen"`
	// Added is true when it is saved (SavedID, SavedName say as what).
	Added     bool   `json:"added"`
	SavedID   string `json:"savedId,omitempty"`
	SavedName string `json:"savedName,omitempty"`
	Hidden    bool   `json:"hidden"`
}

// FoundCastDevices lists everything discovery knows, saved and hidden ones
// included.
func (s *Service) FoundCastDevices(ctx context.Context) ([]DiscoveredDevice, error) {
	saved, err := s.store.CastDevices(ctx)
	if err != nil {
		return nil, err
	}
	hidden, err := s.store.HiddenCastDevices(ctx)
	if err != nil {
		return nil, err
	}
	out := []DiscoveredDevice{}
	for _, f := range s.foundSnapshot() {
		dd := DiscoveredDevice{Found: f.Found, ID: autoID(f.key), DeviceID: f.ID, LastSeen: f.lastSeen.UTC()}
		if sd := matchSaved(saved, f); sd != nil {
			dd.Added, dd.SavedID, dd.SavedName = true, sd.ID, sd.Name
		}
		_, dd.Hidden = hidden[f.key]
		out = append(out, dd)
	}
	return out, nil
}

// mergedCastDevices is saved devices followed by found ones nobody saved
// or hid (unless discovery is off).
func (s *Service) mergedCastDevices(ctx context.Context) ([]store.CastDevice, error) {
	saved, err := s.store.CastDevices(ctx)
	if err != nil {
		return nil, err
	}
	found := s.foundSnapshot()
	out := make([]store.CastDevice, 0, len(saved)+len(found))
	matched := map[string]bool{}
	for _, f := range found {
		if sd := matchSaved(saved, f); sd != nil {
			matched[f.key] = true
			t := f.lastSeen.UTC()
			if sd.LastSeen == nil || sd.LastSeen.Before(t) {
				sd.LastSeen = &t
			}
		}
	}
	for _, sd := range saved {
		out = append(out, *sd)
	}
	if s.CastDiscovery.Off {
		return out, nil
	}
	hidden, err := s.store.HiddenCastDevices(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range found {
		if _, h := hidden[f.key]; h || matched[f.key] {
			continue
		}
		t := f.lastSeen.UTC()
		out = append(out, store.CastDevice{ID: autoID(f.key), Name: f.Name, Host: f.Host, Port: f.Port, DeviceID: f.ID, Model: f.Model,
			CreatedAt: f.firstSeen.UTC(), Auto: true, LastSeen: &t})
	}
	return out, nil
}

func (s *Service) foundByAutoID(id string) (foundDevice, bool) {
	k, ok := keyFromAutoID(id)
	if !ok {
		return foundDevice{}, false
	}
	d := &s.disc
	d.mu.Lock()
	defer d.mu.Unlock()
	if e := d.found[k]; e != nil {
		return *e, true
	}
	return foundDevice{}, false
}

// SaveFoundCastDevice pins a found device under a name (its own when
// empty), so it stays in cast menus even while discovery doesn't see it.
func (s *Service) SaveFoundCastDevice(ctx context.Context, autoID, name string) (*store.CastDevice, error) {
	f, ok := s.foundByAutoID(autoID)
	if !ok {
		return nil, store.ErrNotFound
	}
	saved, err := s.store.CastDevices(ctx)
	if err != nil {
		return nil, err
	}
	if sd := matchSaved(saved, f); sd != nil {
		return nil, &CastInputError{"that device is already saved as " + sd.Name}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = f.Name
	}
	if len(name) > 80 {
		return nil, &CastInputError{"give the device a name (up to 80 characters)"}
	}
	d := &store.CastDevice{ID: deck.NewID("cast"), Name: name, Host: f.Host, Port: f.Port, DeviceID: f.ID, Model: f.Model}
	if err := s.store.AddCastDevice(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// HideFoundCastDevice keeps a found device out of cast menus (or lets it
// back in). Hiding is remembered across restarts.
func (s *Service) HideFoundCastDevice(ctx context.Context, autoID string, hide bool) error {
	k, ok := keyFromAutoID(autoID)
	if !ok {
		return store.ErrNotFound
	}
	name := ""
	if f, ok := s.foundByAutoID(autoID); ok {
		name = f.Name
	} else if hide {
		return store.ErrNotFound
	}
	return s.store.SetCastDeviceHidden(ctx, k, name, hide)
}
