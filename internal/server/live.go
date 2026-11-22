package server

import (
	"errors"
	"net/http"

	"github.com/SecOps-7/slidekick/internal/service"
)

// updateLive sets new values on a live slide (the REST twin of the MCP tool
// update_live), for agents and scripts that don't speak MCP. The deck's
// owner, an admin or the deck's edit token may; viewer and presenter links can't.
func (s *server) updateLive(w http.ResponseWriter, r *http.Request) {
	var u service.LiveUpdate
	if !decode(w, r, &u) {
		return
	}
	res, err := s.svc.UpdateLive(r.Context(), r.PathValue("id"), u)
	if err != nil {
		writeLiveErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func writeLiveErr(w http.ResponseWriter, err error) {
	var input *service.LiveInputError
	switch {
	case errors.Is(err, service.ErrLiveOff):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, service.ErrLiveRate):
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
	case errors.As(err, &input):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	default:
		writeErr(w, err)
	}
}
