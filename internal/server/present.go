package server

import (
	"errors"
	"net/http"

	"github.com/SecOps-7/slidekick/internal/authz"
	"github.com/SecOps-7/slidekick/internal/presenting"
)

// presentRoutes: AI-narrated presenting. Whoever may share a deck and use the
// voices (its owner or an admin, with the narrate permission) starts and stops
// an automatic run. Presenting screens report that a line finished playing
// through the ack route, which only needs view access (a cast TV carries the
// deck's view key), as does the status.
func (s *server) presentRoutes(mux *http.ServeMux) {
	if s.opt.Presenter == nil {
		return
	}
	mux.HandleFunc("GET /api/decks/{id}/present", s.viewGate(s.presentStatus))
	mux.HandleFunc("POST /api/decks/{id}/present/ack", s.viewGate(s.presentAck))
	mux.Handle("POST /api/decks/{id}/present", s.require(authz.Share, true, s.mayNarrate(s.startPresent)))
	mux.Handle("DELETE /api/decks/{id}/present", s.require(authz.Share, true, s.mayNarrate(s.stopPresent)))
}

// mayNarrate adds the narrate permission to a route that already checked the deck.
func (s *server) mayNarrate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p := authFrom(r.Context()).p; !s.policy().Can(p, authz.Narrate, nil) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": forbiddenMsg(p, authz.Narrate)})
			return
		}
		next(w, r)
	}
}

func (s *server) presentStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.opt.Presenter.Status(r.PathValue("id")))
}

func (s *server) presentAck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Seq int `json:"seq"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.opt.Presenter.Ack(r.PathValue("id"), in.Seq)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) startPresent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From     int    `json:"from"`
		To       int    `json:"to"`
		Provider string `json:"provider"`
		Voice    string `json:"voice"`
	}
	if !decode(w, r, &in) {
		return
	}
	st, err := s.opt.Presenter.Start(r.PathValue("id"), presenting.RunOptions{From: in.From, To: in.To, Provider: in.Provider, Voice: in.Voice}, authFrom(r.Context()).p.Actor())
	var missing *presenting.ErrMissingNotes
	switch {
	case err == nil:
		writeJSON(w, 200, st)
	case errors.As(err, &missing):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "missingNotes": missing.Slides})
	case errors.Is(err, presenting.ErrBusy):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a presentation is already running on this deck"})
	default:
		writeErr(w, err)
	}
}

func (s *server) stopPresent(w http.ResponseWriter, r *http.Request) {
	stopped := s.opt.Presenter.Stop(r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"stopped": stopped})
}
