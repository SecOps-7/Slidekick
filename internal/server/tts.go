package server

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SecOps-7/slidekick/internal/authz"
	"github.com/SecOps-7/slidekick/internal/narration"
	"github.com/SecOps-7/slidekick/internal/store"
	"github.com/SecOps-7/slidekick/internal/tts"
)

// ttsRoutes: admins connect text-to-speech providers and pick the narration
// default; editors and admins list voices. Keys never leave the server: the
// admin list says only whether a key is stored.
//
// Synthesized audio is served from /api/tts/audio/{hash}.mp3 without
// credentials, so a cast receiver or an <audio> element can fetch it: the
// hash is a keyed HMAC nobody can derive from the text, and the entry
// disappears from memory after narration.CacheTTL unused.
func (s *server) ttsRoutes(mux *http.ServeMux) {
	if s.opt.TTS == nil {
		return
	}
	mux.Handle("GET /api/admin/tts", s.require(authz.ManageSettings, false, s.listTTS))
	mux.Handle("PUT /api/admin/tts/{pid}", s.require(authz.ManageSettings, false, s.putTTS))
	mux.Handle("DELETE /api/admin/tts/{pid}", s.require(authz.ManageSettings, false, s.deleteTTS))
	mux.Handle("POST /api/admin/tts/{pid}/default", s.require(authz.ManageSettings, false, s.defaultTTS))
	mux.Handle("POST /api/admin/tts/{pid}/test", s.require(authz.ManageSettings, false, s.testTTS))
	mux.Handle("GET /api/tts/providers", s.require(authz.Narrate, false, s.ttsProviders))
	mux.Handle("GET /api/tts/voices", s.require(authz.Narrate, false, s.ttsVoices))
	// The presenter view's voice picker, also for presenter links.
	mux.Handle("GET /api/decks/{id}/voices", s.require(authz.Narrate, true, s.ttsProviders))
	mux.HandleFunc("GET /api/tts/audio/{file}", s.ttsAudio)
}

func (s *server) writeTTSErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, narration.ErrValidation), errors.Is(err, tts.ErrText):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": strings.TrimPrefix(strings.TrimPrefix(err.Error(), "invalid: "), "tts: ")})
	case errors.Is(err, tts.ErrBrowserVoice):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "browser voices are spoken by the browser", "browser": true})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		if s.opt.Log != nil {
			s.opt.Log.Warn("text-to-speech failed", "err", err) // errors are key-redacted by internal/tts
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
}

func (s *server) listTTS(w http.ResponseWriter, r *http.Request) {
	ps, d, err := s.opt.TTS.Providers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"providers": ps, "default": d})
}

func (s *server) putTTS(w http.ResponseWriter, r *http.Request) {
	var in narration.ProviderInput
	if !decode(w, r, &in) {
		return
	}
	in.ID = r.PathValue("pid")
	p, err := s.opt.TTS.SaveProvider(r.Context(), in)
	if err != nil {
		s.writeTTSErr(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *server) deleteTTS(w http.ResponseWriter, r *http.Request) {
	if err := s.opt.TTS.DeleteProvider(r.Context(), r.PathValue("pid")); err != nil {
		s.writeTTSErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) defaultTTS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Voice string `json:"voice"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.opt.TTS.SetDefault(r.Context(), r.PathValue("pid"), in.Voice); err != nil {
		s.writeTTSErr(w, err)
		return
	}
	writeJSON(w, 200, s.opt.TTS.Default(r.Context()))
}

// testTTS speaks a short sample and returns the MP3 itself.
func (s *server) testTTS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Voice string `json:"voice"`
	}
	if !decode(w, r, &in) {
		return
	}
	a, err := s.opt.TTS.Sample(r.Context(), r.PathValue("pid"), in.Voice)
	if err != nil {
		s.writeTTSErr(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.MIME)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Audio-Duration-Ms", strconv.FormatInt(a.Duration.Milliseconds(), 10))
	w.Write(a.Data)
}

// ttsProviders is what editors and presenters see: names and voices, no
// URL, no key.
func (s *server) ttsProviders(w http.ResponseWriter, r *http.Request) {
	ps, d, err := s.opt.TTS.Providers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	type public struct {
		ID           string           `json:"id"`
		Name         string           `json:"name"`
		Kind         string           `json:"kind"`
		DefaultVoice string           `json:"defaultVoice"`
		Voices       []store.TTSVoice `json:"voices"`
		IsDefault    bool             `json:"isDefault"`
	}
	out := make([]public, 0, len(ps))
	for _, p := range ps {
		vs := p.Voices
		if vs == nil {
			vs = []store.TTSVoice{} // the browser's voices are listed by the browser
		}
		out = append(out, public{p.ID, p.Name, p.Kind, p.DefaultVoice, vs, p.IsDefault})
	}
	writeJSON(w, 200, map[string]any{"providers": out, "default": d})
}

func (s *server) ttsVoices(w http.ResponseWriter, r *http.Request) {
	pid := r.URL.Query().Get("provider")
	vs, err := s.opt.TTS.Voices(r.Context(), pid)
	if err != nil {
		s.writeTTSErr(w, err)
		return
	}
	if pid == "" {
		pid = s.opt.TTS.Default(r.Context()).Provider
	}
	writeJSON(w, 200, map[string]any{"voices": vs, "browser": pid == narration.BrowserID})
}

func (s *server) ttsAudio(w http.ResponseWriter, r *http.Request) {
	hash, ok := strings.CutSuffix(r.PathValue("file"), ".mp3")
	if !ok || len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		http.NotFound(w, r)
		return
	}
	a, ok := s.opt.TTS.Cached(hash)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.MIME)
	h.Set("Cache-Control", "private, max-age="+strconv.Itoa(int(narration.CacheTTL/time.Second)))
	if a.Duration > 0 {
		h.Set("X-Audio-Duration-Ms", strconv.FormatInt(a.Duration.Milliseconds(), 10))
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(a.Data))
}
