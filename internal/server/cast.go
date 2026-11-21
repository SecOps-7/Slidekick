package server

import (
	"errors"
	"net/http"

	"github.com/SecOps-7/slidekick/internal/authz"
	"github.com/SecOps-7/slidekick/internal/service"
	"github.com/SecOps-7/slidekick/internal/store"
)

// castRoutes: the server finds Chromecasts on the network and admins save
// (or hide) them; whoever may share a deck (its owner, admins, its edit
// token) may cast it. Listing devices for the cast menu needs an editor or
// admin; editors see names only, never addresses.
func (s *server) castRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/admin/cast/devices", s.require(authz.ManageSettings, false, s.listCastDevicesAdmin))
	mux.Handle("POST /api/admin/cast/devices", s.require(authz.ManageSettings, false, s.addCastDevice))
	mux.Handle("DELETE /api/admin/cast/devices/{did}", s.require(authz.ManageSettings, false, s.removeCastDevice))
	mux.Handle("POST /api/admin/cast/discover", s.require(authz.ManageSettings, false, s.discoverCast))
	mux.Handle("POST /api/admin/cast/found/{fid}/save", s.require(authz.ManageSettings, false, s.saveFoundCast))
	mux.Handle("POST /api/admin/cast/found/{fid}/hide", s.require(authz.ManageSettings, false, s.hideFoundCast(true)))
	mux.Handle("DELETE /api/admin/cast/found/{fid}/hide", s.require(authz.ManageSettings, false, s.hideFoundCast(false)))
	mux.Handle("GET /api/cast/devices", s.require(authz.Create, false, s.listCastDevices))
	mux.Handle("GET /api/decks/{id}/cast", s.require(authz.Drive, true, s.castStatus))
	mux.Handle("POST /api/decks/{id}/cast", s.require(authz.Drive, true, s.startCast))
	mux.Handle("DELETE /api/decks/{id}/cast", s.require(authz.Drive, true, s.stopCast))
	mux.Handle("POST /api/decks/{id}/cast/refresh", s.require(authz.Drive, true, s.refreshCast))
}

func writeCastErr(w http.ResponseWriter, err error) {
	var in *service.CastInputError
	switch {
	case errors.As(err, &in):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": in.Msg})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
}

type castDeviceName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Auto bool   `json:"auto,omitempty"` // found on the network, not saved
}

func (s *server) deviceNames(r *http.Request) ([]castDeviceName, error) {
	ds, err := s.svc.CastDevices(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]castDeviceName, len(ds))
	for i, d := range ds {
		out[i] = castDeviceName{d.ID, d.Name, d.Auto}
	}
	return out, nil
}

// listCastDevicesAdmin: saved devices, everything discovery found (hidden
// ones included, flagged) and how discovery is doing.
func (s *server) listCastDevicesAdmin(w http.ResponseWriter, r *http.Request) {
	ds, err := s.svc.CastDevices(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	saved := []store.CastDevice{}
	for _, d := range ds {
		if !d.Auto {
			saved = append(saved, d)
		}
	}
	found, err := s.svc.FoundCastDevices(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"devices": saved, "found": found, "discovery": s.svc.CastDiscoveryStatus()})
}

func (s *server) addCastDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if !decode(w, r, &in) {
		return
	}
	d, err := s.svc.AddCastDevice(r.Context(), in.Name, in.Host, in.Port)
	if err != nil {
		writeCastErr(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *server) removeCastDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RemoveCastDevice(r.Context(), r.PathValue("did")); err != nil {
		writeCastErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// discoverCast runs discovery now and answers when it is done (a few
// seconds); the settings page polls the admin list meanwhile for progress.
func (s *server) discoverCast(w http.ResponseWriter, r *http.Request) {
	found, st, err := s.svc.DiscoverCastDevices(r.Context())
	if err != nil {
		writeCastErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"devices": found, "discovery": st})
}

func (s *server) saveFoundCast(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	d, err := s.svc.SaveFoundCastDevice(r.Context(), r.PathValue("fid"), in.Name)
	if err != nil {
		writeCastErr(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *server) hideFoundCast(hide bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.svc.HideFoundCastDevice(r.Context(), r.PathValue("fid"), hide); err != nil {
			writeCastErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}

// refreshCast looks for devices again (at most every few seconds, and not
// at all when discovery is off) and answers like castStatus.
func (s *server) refreshCast(w http.ResponseWriter, r *http.Request) {
	if _, err := s.svc.RefreshCastDevices(r.Context(), false); err != nil {
		writeCastErr(w, err)
		return
	}
	s.castStatus(w, r)
}

func (s *server) listCastDevices(w http.ResponseWriter, r *http.Request) {
	ds, err := s.deviceNames(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"devices": ds})
}

// castStatus says where the deck is showing (casting is null when nowhere)
// and lists the devices, so the cast menu needs one request.
func (s *server) castStatus(w http.ResponseWriter, r *http.Request) {
	ds, err := s.deviceNames(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"casting": s.svc.CastStatus(r.PathValue("id")), "devices": ds})
}

func (s *server) startCast(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DeviceID string `json:"deviceId"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if err := s.svc.StartCast(r.Context(), id, in.DeviceID, authFrom(r.Context()).p.Actor()); err != nil {
		writeCastErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"casting": s.svc.CastStatus(id)})
}

func (s *server) stopCast(w http.ResponseWriter, r *http.Request) {
	err := s.svc.StopCastBy(r.Context(), r.PathValue("id"), authFrom(r.Context()).p.Actor())
	if err != nil && !errors.Is(err, service.ErrNotCasting) {
		writeCastErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"casting": nil})
}
