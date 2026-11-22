package service

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/SecOps-7/slidekick/internal/cast"
	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/store"
)

// Casting: the server itself tells a Chromecast on the LAN to open the
// deck's cast view (/d/{id}/present?cast=1), which follows whoever drives
// the deck. Devices are found on the network (castdiscovery.go) or saved by
// admins; editors of a deck (and the AI presenter, through StartCast) pick
// one of them.

// CastInputError is a request the caller can fix (bad device details, an
// unknown device, a base URL the TV can't reach).
type CastInputError struct{ Msg string }

func (e *CastInputError) Error() string { return e.Msg }

// ErrNotCasting is returned by StopCast when the deck isn't showing anywhere.
var ErrNotCasting = cast.ErrNotCasting

func (s *Service) caster() *cast.Manager {
	s.castOnce.Do(func() {
		if s.Cast == nil {
			s.Cast = cast.NewManager()
		}
	})
	return s.Cast
}

// CastDevices lists the devices people can cast to: the ones admins saved,
// then the ones discovery found on the network that nobody saved or hid
// (Auto set, ids "auto-…"). Saved devices discovery sees carry LastSeen.
func (s *Service) CastDevices(ctx context.Context) ([]store.CastDevice, error) {
	return s.mergedCastDevices(ctx)
}

var hostRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// AddCastDevice registers a device by name and IP address or host name.
// Port 0 means the Cast default, 8009.
func (s *Service) AddCastDevice(ctx context.Context, name, host string, port int) (*store.CastDevice, error) {
	name = strings.TrimSpace(name)
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if name == "" || len(name) > 80 {
		return nil, &CastInputError{"give the device a name (up to 80 characters)"}
	}
	if h, p, err := net.SplitHostPort(host); err == nil && port == 0 { // "172.16.5.5:8009"
		if n, err := strconv.Atoi(p); err == nil {
			host, port = h, n
		}
	}
	if net.ParseIP(host) == nil && !hostRE.MatchString(host) {
		return nil, &CastInputError{"enter the device's IP address or host name, like 192.168.1.50"}
	}
	if port == 0 {
		port = cast.DefaultPort
	}
	if port < 1 || port > 65535 {
		return nil, &CastInputError{"the port must be between 1 and 65535 (Chromecasts use 8009)"}
	}
	d := &store.CastDevice{ID: deck.NewID("cast"), Name: name, Host: host, Port: port}
	if err := s.store.AddCastDevice(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// RemoveCastDevice forgets a device.
func (s *Service) RemoveCastDevice(ctx context.Context, id string) error {
	return s.store.DeleteCastDevice(ctx, id)
}

// DiscoverCastDevices looks for devices now (mDNS plus the subnet scan)
// and lists everything discovery knows, with the run's status.
func (s *Service) DiscoverCastDevices(ctx context.Context) ([]DiscoveredDevice, CastDiscoveryStatus, error) {
	st, err := s.RefreshCastDevices(ctx, true)
	if err != nil {
		return nil, st, err
	}
	found, err := s.FoundCastDevices(ctx)
	return found, st, err
}

func (s *Service) findCastDevice(ctx context.Context, idOrName string) (*store.CastDevice, error) {
	idOrName = strings.TrimSpace(idOrName)
	ds, err := s.CastDevices(ctx)
	if err != nil {
		return nil, err
	}
	for i := range ds {
		if ds[i].ID == idOrName {
			return &ds[i], nil
		}
	}
	for i := range ds {
		if strings.EqualFold(ds[i].Name, idOrName) {
			return &ds[i], nil
		}
	}
	names := make([]string, len(ds))
	for i, d := range ds {
		names[i] = d.Name
	}
	if len(names) == 0 {
		return nil, &CastInputError{"no cast devices were found on the network and none are saved: an admin can add one by IP address under Settings → Cast devices"}
	}
	return nil, &CastInputError{fmt.Sprintf("no cast device called %q (devices: %s)", idOrName, strings.Join(names, ", "))}
}

// CastURL is the page the TV opens: the deck's presenting view in cast mode,
// carrying the view key so a private deck opens without signing in. It uses
// CastBaseURL when set: plain HTTP when the base URL is Slidekick's own local
// HTTPS listener, whose certificate a Chromecast can't be told to trust.
func (s *Service) CastURL(ctx context.Context, deckID string) (string, error) {
	base := s.BaseURL
	if s.CastBaseURL != "" {
		base = s.CastBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", &CastInputError{"SLIDEKICK_BASE_URL isn't set to a valid URL, so the TV can't be told where the deck is"}
	}
	h := u.Hostname()
	if ip := net.ParseIP(h); h == "localhost" || strings.HasSuffix(h, ".localhost") || (ip != nil && ip.IsLoopback()) {
		return "", &CastInputError{fmt.Sprintf("SLIDEKICK_BASE_URL is %s, which a TV can't open: set it to an address the TV can reach on your network, like http://192.168.1.10:7410", base)}
	}
	q := "?"
	if k := s.ViewKey(ctx, deckID); k != "" {
		q += "k=" + url.QueryEscape(k) + "&"
	}
	return base + "/d/" + url.PathEscape(deckID) + "/present" + q + "cast=1", nil
}

func castEvent(state, device, deviceID, actor string) map[string]any {
	return map[string]any{"type": "cast", "state": state, "device": device, "deviceId": deviceID, "actor": actor}
}

// StartCast shows a deck on a cast device (by id, or by name ignoring case)
// and tells the deck's open browsers. A deck shows on one device at a time,
// and a device shows one deck: the deck it was showing is announced as stopped.
func (s *Service) StartCast(ctx context.Context, deckID, deviceIDOrName, actor string) error {
	if _, _, err := s.store.Get(ctx, deckID); err != nil {
		return err
	}
	dev, err := s.findCastDevice(ctx, deviceIDOrName)
	if err != nil {
		return err
	}
	u, err := s.CastURL(ctx, deckID)
	if err != nil {
		return err
	}
	_, displaced, err := s.caster().Start(ctx, deckID, cast.Device{ID: dev.ID, Name: dev.Name, Host: dev.Host, Port: dev.Port}, u, actor)
	if err != nil {
		return fmt.Errorf("couldn't cast to %s: %w", dev.Name, err)
	}
	for _, o := range displaced {
		s.Publish(o.DeckID, castEvent("stopped", o.Name, o.DeviceID, actor))
	}
	s.Publish(deckID, castEvent("started", dev.Name, dev.ID, actor))
	return nil
}

// StopCast takes a deck off the TV. It returns ErrNotCasting when the deck
// wasn't showing anywhere.
func (s *Service) StopCast(ctx context.Context, deckID string) error {
	return s.stopCast(ctx, deckID, "")
}

func (s *Service) stopCast(ctx context.Context, deckID, actor string) error {
	sess, err := s.caster().Stop(ctx, deckID)
	if sess != nil {
		s.Publish(deckID, castEvent("stopped", sess.Name, sess.DeviceID, actor))
	}
	if err != nil && sess != nil {
		return fmt.Errorf("stopped tracking the cast, but %s didn't answer: %w", sess.Name, err)
	}
	return err
}

// StopCastBy is StopCast with the actor recorded in the event.
func (s *Service) StopCastBy(ctx context.Context, deckID, actor string) error {
	return s.stopCast(ctx, deckID, actor)
}

// CastStatus returns where the deck is showing, or nil.
func (s *Service) CastStatus(deckID string) *cast.Session {
	return s.caster().Status(deckID)
}
