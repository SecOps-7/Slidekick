package cast

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

// DashCast is the public receiver app that shows a web page on the TV (the
// one catt uses). Verified against pychromecast, which catt builds on:
// pychromecast/config.py has APP_DASHCAST = "84912283", and
// pychromecast/controllers/dashcast.py talks on "urn:x-cast:com.madmod.dashcast"
// with {"url", "force", "reload", "reload_time"}. The receiver page itself
// (github.com/madmod/dashcast, receiver.html) listens on that namespace; with
// force set it navigates the whole receiver to the URL instead of framing it,
// which Slidekick needs because its pages refuse to be framed. The receiver
// page is loaded by the TV from the internet; everything else stays on the LAN.
const (
	DashCastAppID     = "84912283"
	DashCastNamespace = "urn:x-cast:com.madmod.dashcast"
)

// dashCastLoad is the message that makes DashCast open a URL.
func dashCastLoad(url string) map[string]any {
	return map[string]any{"url": url, "force": true, "reload": false, "reload_time": 0}
}

// Device is a Cast device the server can reach.
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Addr is host:port (port 8009 when unset).
func (d Device) Addr() string {
	p := d.Port
	if p <= 0 {
		p = DefaultPort
	}
	return net.JoinHostPort(d.Host, strconv.Itoa(p))
}

// Session is a deck showing on a device.
type Session struct {
	DeckID    string    `json:"deckId"`
	Device    Device    `json:"-"`
	DeviceID  string    `json:"deviceId"`
	Name      string    `json:"device"`
	StartedBy string    `json:"startedBy"`
	StartedAt time.Time `json:"startedAt"`
	sessionID string    // the receiver app session, for STOP
}

// ErrNotCasting means the deck isn't showing anywhere.
var ErrNotCasting = errors.New("this deck isn't being cast")

// Manager tracks which deck shows on which device. One deck shows on at most
// one device, and one device shows at most one deck: starting a cast moves
// it. Sessions live in memory: after a restart the TV keeps showing the deck
// (and keeps following it) until someone stops it from the device.
type Manager struct {
	// AppID is the receiver app to launch (DashCastAppID when empty).
	AppID string
	// Timeout bounds one start or stop (default 30 s).
	Timeout time.Duration

	op       sync.Mutex // one device conversation at a time: casts are rare
	mu       sync.Mutex
	sessions map[string]*Session // by deck id
}

func NewManager() *Manager { return &Manager{sessions: map[string]*Session{}} }

func (m *Manager) appID() string {
	if m.AppID != "" {
		return m.AppID
	}
	return DashCastAppID
}

func (m *Manager) ctx(ctx context.Context) (context.Context, context.CancelFunc) {
	t := m.Timeout
	if t <= 0 {
		t = 30 * time.Second
	}
	return context.WithTimeout(ctx, t)
}

func sameDevice(a, b Device) bool {
	return (a.ID != "" && a.ID == b.ID) || a.Addr() == b.Addr()
}

// Start launches DashCast on the device and points it at url. It returns the
// new session and any sessions it displaced (other decks that were showing
// on this device, which the caller may want to announce as stopped).
func (m *Manager) Start(ctx context.Context, deckID string, dev Device, url, startedBy string) (*Session, []Session, error) {
	ctx, cancel := m.ctx(ctx)
	defer cancel()
	m.op.Lock()
	defer m.op.Unlock()

	// The same deck on another device: take it off that one first (best effort).
	m.mu.Lock()
	prev := m.sessions[deckID]
	m.mu.Unlock()
	if prev != nil && !sameDevice(prev.Device, dev) {
		m.stopOn(ctx, prev.Device, prev.sessionID)
	}

	c, err := Dial(ctx, dev.Addr())
	if err != nil {
		return nil, nil, err
	}
	defer c.Close()
	app, err := c.Launch(ctx, m.appID())
	if err != nil {
		return nil, nil, err
	}
	if err := c.SendApp(app, DashCastNamespace, dashCastLoad(url)); err != nil {
		return nil, nil, err
	}
	// A status round trip proves the device read everything sent before it.
	if _, err := c.Status(ctx); err != nil {
		return nil, nil, err
	}

	s := &Session{DeckID: deckID, Device: dev, DeviceID: dev.ID, Name: dev.Name, StartedBy: startedBy, StartedAt: time.Now().UTC(), sessionID: app.SessionID}
	var displaced []Session
	m.mu.Lock()
	for id, o := range m.sessions {
		if id != deckID && sameDevice(o.Device, dev) {
			displaced = append(displaced, *o)
			delete(m.sessions, id)
		}
	}
	m.sessions[deckID] = s
	m.mu.Unlock()
	out := *s
	return &out, displaced, nil
}

// Stop ends the deck's cast: the receiver app is stopped and the TV returns
// to its home screen. The session is forgotten even if the device can't be
// reached (the error says so).
func (m *Manager) Stop(ctx context.Context, deckID string) (*Session, error) {
	ctx, cancel := m.ctx(ctx)
	defer cancel()
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	s := m.sessions[deckID]
	delete(m.sessions, deckID)
	m.mu.Unlock()
	if s == nil {
		return nil, ErrNotCasting
	}
	return s, m.stopOn(ctx, s.Device, s.sessionID)
}

// stopOn stops our receiver app on a device: the session we launched, or
// whatever DashCast session is running there now.
func (m *Manager) stopOn(ctx context.Context, dev Device, sessionID string) error {
	c, err := Dial(ctx, dev.Addr())
	if err != nil {
		return err
	}
	defer c.Close()
	st, err := c.Status(ctx)
	if err != nil {
		return err
	}
	for _, a := range st.Applications {
		if (sessionID != "" && a.SessionID == sessionID) || a.AppID == m.appID() {
			return c.Stop(ctx, a.SessionID)
		}
	}
	return nil // already gone (someone pressed Home on the TV)
}

// Status returns the deck's cast session, or nil.
func (m *Manager) Status(deckID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[deckID]; s != nil {
		out := *s
		return &out
	}
	return nil
}

// Forget drops a deck's session without talking to the device (for a deck
// that was deleted while showing).
func (m *Manager) Forget(deckID string) {
	m.mu.Lock()
	delete(m.sessions, deckID)
	m.mu.Unlock()
}
