package server

import (
	"errors"
	"io"
	"net/http"

	"github.com/SecOps-7/slidekick/internal/authz"
	"github.com/SecOps-7/slidekick/internal/localtls"
)

// tlsRoutes are the local HTTPS settings (admins) and the generated CA
// certificate, which anyone may download while it's in use: it's a public
// certificate that devices on the LAN need in order to trust Slidekick.
func (s *server) tlsRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/admin/tls", s.require(authz.ManageSettings, false, s.tlsStatus))
	mux.Handle("PUT /api/admin/tls", s.require(authz.ManageSettings, false, s.tlsSave))
	mux.Handle("POST /api/admin/tls/ca", s.require(authz.ManageSettings, false, s.tlsRotateCA))
	mux.HandleFunc("GET /tls/ca.crt", s.tlsCA)
}

func (s *server) tlsStatus(w http.ResponseWriter, r *http.Request) {
	if s.opt.LocalTLS == nil {
		writeJSON(w, 200, map[string]any{"available": false})
		return
	}
	writeJSON(w, 200, s.opt.LocalTLS.Status(r.Context()))
}

func (s *server) tlsReply(w http.ResponseWriter, st localtls.Status, err error) {
	switch {
	case err == nil:
		writeJSON(w, 200, st)
	case errors.Is(err, localtls.ErrInput):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "status": st})
	default:
		if s.opt.Log != nil {
			s.opt.Log.Error("local HTTPS settings", "err", err)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "couldn't save the local HTTPS settings", "status": st})
	}
}

func (s *server) tlsSave(w http.ResponseWriter, r *http.Request) {
	if s.opt.LocalTLS == nil {
		http.NotFound(w, r)
		return
	}
	var u localtls.Update
	if !decode(w, r, &u) {
		return
	}
	st, err := s.opt.LocalTLS.Save(r.Context(), u)
	s.tlsReply(w, st, err)
}

func (s *server) tlsRotateCA(w http.ResponseWriter, r *http.Request) {
	if s.opt.LocalTLS == nil {
		http.NotFound(w, r)
		return
	}
	st, err := s.opt.LocalTLS.RotateCA(r.Context())
	s.tlsReply(w, st, err)
}

func (s *server) tlsCA(w http.ResponseWriter, r *http.Request) {
	if s.opt.LocalTLS == nil {
		http.NotFound(w, r)
		return
	}
	ca, ok := s.opt.LocalTLS.CACert(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="slidekick-local-ca.crt"`)
	w.Header().Set("Cache-Control", "no-cache")
	io.WriteString(w, ca)
}
