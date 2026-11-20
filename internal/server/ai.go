package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SecOps-7/slidekick/internal/ai"
	"github.com/SecOps-7/slidekick/internal/authz"
	"github.com/SecOps-7/slidekick/internal/store"
)

// aiRoutes: admins connect providers; editors and admins chat. Keys never
// leave the server: the admin list says only whether a key is stored.
func (s *server) aiRoutes(mux *http.ServeMux) {
	if s.opt.AI == nil {
		return
	}
	mux.Handle("GET /api/admin/ai", s.require(authz.ManageSettings, false, s.listAI))
	mux.Handle("PUT /api/admin/ai/{pid}", s.require(authz.ManageSettings, false, s.putAI))
	mux.Handle("DELETE /api/admin/ai/{pid}", s.require(authz.ManageSettings, false, s.deleteAI))
	mux.Handle("POST /api/admin/ai/{pid}/models", s.require(authz.ManageSettings, false, s.fetchModels))
	mux.Handle("GET /api/ai/providers", s.require(authz.UseAI, false, s.aiProviders))
	mux.Handle("POST /api/decks/{id}/ai/chat", s.require(authz.UseAI, true, s.aiChat))
}

func writeAIErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ai.ErrValidation):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": strings.TrimPrefix(err.Error(), "invalid: ")})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
}

func (s *server) listAI(w http.ResponseWriter, r *http.Request) {
	ps, err := s.opt.AI.Store.AIProviders(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"providers": ps})
}

func (s *server) putAI(w http.ResponseWriter, r *http.Request) {
	var in ai.ProviderInput
	if !decode(w, r, &in) {
		return
	}
	in.ID = r.PathValue("pid")
	p, err := s.opt.AI.SaveProvider(r.Context(), in)
	if err != nil {
		writeAIErr(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *server) deleteAI(w http.ResponseWriter, r *http.Request) {
	if err := s.opt.AI.Store.DeleteAIProvider(r.Context(), r.PathValue("pid")); err != nil {
		writeAIErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) fetchModels(w http.ResponseWriter, r *http.Request) {
	ms, err := s.opt.AI.FetchModels(r.Context(), r.PathValue("pid"))
	if err != nil {
		writeAIErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"models": ms})
}

func (s *server) aiProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := s.opt.AI.Available(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"providers": ps})
}

// chatSlots limits each user to two chats running at once.
var chatSlots = struct {
	sync.Mutex
	n map[string]int
}{n: map[string]int{}}

func takeChatSlot(user string) bool {
	chatSlots.Lock()
	defer chatSlots.Unlock()
	if chatSlots.n[user] >= 2 {
		return false
	}
	chatSlots.n[user]++
	return true
}

func freeChatSlot(user string) {
	chatSlots.Lock()
	chatSlots.n[user]--
	if chatSlots.n[user] <= 0 {
		delete(chatSlots.n, user)
	}
	chatSlots.Unlock()
}

// aiChat streams one chat turn as server-sent events. Closing the request
// (the Stop button) cancels the model call and any further tool calls.
func (s *server) aiChat(w http.ResponseWriter, r *http.Request) {
	u := authFrom(r.Context()).user
	if u == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "the AI chat needs a signed-in account"})
		return
	}
	var req ai.ChatRequest
	if !decode(w, r, &req) {
		return
	}
	if !takeChatSlot(u.ID) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "you already have two AI chats running"})
		return
	}
	defer freeChatSlot(u.ID)
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	var mu sync.Mutex
	send := func(ev ai.Event) {
		b, _ := json.Marshal(ev)
		mu.Lock()
		w.Write([]byte("data: "))
		w.Write(b)
		w.Write([]byte("\n\n"))
		fl.Flush()
		mu.Unlock()
	}
	err := s.opt.AI.Chat(ctx, u, r.PathValue("id"), req, send)
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		send(ai.Event{Type: "error", Text: "Stopped."})
	case errors.Is(err, context.DeadlineExceeded):
		send(ai.Event{Type: "error", Text: "The AI took too long and was stopped."})
	default:
		if s.opt.Log != nil {
			s.opt.Log.Warn("ai chat failed", "user", u.Username, "err", err) // errors are key-redacted by internal/llm
		}
		send(ai.Event{Type: "error", Text: strings.TrimPrefix(err.Error(), "invalid: ")})
	}
}
