// Package server is the HTTP front end: the REST API, the WebSocket live
// sync, the MCP streamable HTTP endpoint and the embedded web app.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/export"
	"github.com/SecOps-7/slidekick/internal/service"
	"github.com/SecOps-7/slidekick/internal/store"
)

type Options struct {
	Token    string // bearer token for /mcp and write API; empty disables auth
	SchemaFS fs.FS
	Web      fs.FS
	Log      *slog.Logger
	Exporter *export.Exporter
}

type server struct {
	svc *service.Service
	opt Options
}

// New returns the root handler.
func New(svc *service.Service, mcpServer *mcp.Server, opt Options) http.Handler {
	s := &server{svc: svc, opt: opt}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /api/decks", s.listDecks)
	mux.HandleFunc("GET /api/decks/{id}", s.getDeck)
	mux.HandleFunc("GET /api/decks/{id}/history", s.history)
	mux.Handle("POST /api/decks/{id}/patch", s.auth(http.HandlerFunc(s.patchDeck)))
	mux.HandleFunc("GET /api/catalog", s.catalog)
	mux.HandleFunc("GET /api/decks/{id}/export.html", s.exportHTML)
	mux.Handle("GET /api/auth", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "required": opt.Token != ""})
	})))
	mux.HandleFunc("GET /api/decks/{id}/comments", s.listComments)
	mux.Handle("POST /api/decks/{id}/comments", s.auth(http.HandlerFunc(s.addComment)))
	mux.Handle("POST /api/decks/{id}/comments/{cid}/resolve", s.auth(http.HandlerFunc(s.resolveComment)))
	mux.Handle("POST /api/decks/{id}/restore", s.auth(http.HandlerFunc(s.restore)))
	mux.Handle("POST /api/files", s.auth(http.HandlerFunc(s.uploadFile)))
	mux.HandleFunc("GET /files/{name}", s.serveFile)
	mux.HandleFunc("GET /ws/decks/{id}", s.ws)
	mux.HandleFunc("GET /schema/deck.schema.json", func(w http.ResponseWriter, r *http.Request) {
		b, _ := fs.ReadFile(opt.SchemaFS, "schema/deck.schema.json")
		w.Header().Set("Content-Type", "application/schema+json")
		w.Write(b)
	})
	mux.HandleFunc("GET /themes/{name}/theme.css", func(w http.ResponseWriter, r *http.Request) {
		theme, ok := svc.Themes.Themes[r.PathValue("name")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		io.WriteString(w, svc.Themes.FontFaceCSS(theme, func(file string) string { return "/fonts/" + file }))
		w.Write(svc.Themes.CSS[theme.Name])
	})
	mux.HandleFunc("GET /fonts/{file}", func(w http.ResponseWriter, r *http.Request) {
		b, ok := svc.Themes.FontFile(r.PathValue("file"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "font/woff2")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Write(b)
	})

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
	mux.Handle("/mcp", s.auth(mcpHandler))
	mux.Handle("/mcp/", s.auth(mcpHandler))

	mux.Handle("/", s.spa())
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *server) auth(next http.Handler) http.Handler {
	if s.opt.Token == "" {
		return next
	}
	want := []byte("Bearer " + s.opt.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="slidekick"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var conflict *service.ConflictError
	var invalid *deck.ValidationError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "deck not found"})
	case errors.As(err, &conflict):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "currentVersion": conflict.Current})
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid deck", "problems": invalid.Problems})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

func (s *server) listDecks(w http.ResponseWriter, r *http.Request) {
	rows, err := s.svc.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"decks": rows})
}

func (s *server) getDeck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, raw, ver, err := s.svc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	issues, _, _ := s.svc.Lint(r.Context(), id)
	writeJSON(w, 200, map[string]any{"version": ver, "deck": json.RawMessage(raw), "lint": issues, "viewers": s.svc.Hub.Count(id)})
}

func (s *server) history(w http.ResponseWriter, r *http.Request) {
	h, err := s.svc.History(r.Context(), r.PathValue("id"), 100)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"versions": h})
}

func (s *server) patchDeck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseVersion *int      `json:"baseVersion"`
		Ops         []deck.Op `json:"ops"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		writeErr(w, err)
		return
	}
	res, err := s.svc.Patch(r.Context(), r.PathValue("id"), body.BaseVersion, body.Ops, "human")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *server) catalog(w http.ResponseWriter, r *http.Request) {
	layouts := map[string]any{}
	for name, l := range s.svc.Cat.Layouts {
		layouts[name] = map[string]any{"description": l.Description, "regions": s.svc.Cat.LayoutRegions(name)}
	}
	writeJSON(w, 200, map[string]any{
		"layouts":     layouts,
		"transitions": s.svc.Cat.Transitions,
		"builds":      s.svc.Cat.Builds,
		"elements":    s.svc.Cat.Elements,
		"icons":       s.svc.Cat.Icons,
		"themes":      s.svc.Themes.Themes,
	})
}

// ws streams deck change events. The first message is a hello carrying the
// current version so the client can detect a gap and refetch.
//
// Clients may also send: {"type":"auth","token":...} to unlock presenting,
// then {"type":"present","index":n,"step":k} and {"type":"pointer","x":0..1,
// "y":0..1,"on":bool}, which are relayed to everyone watching the deck.
func (s *server) ws(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, _, ver, err := s.svc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(8 << 10)
	ch, cancel := s.svc.Hub.Subscribe(id)
	defer cancel()
	ctx, stop := context.WithCancel(r.Context())
	defer stop()
	conn := deck.NewID("w")

	hello, _ := json.Marshal(map[string]any{"type": "hello", "version": ver, "conn": conn})
	if err := c.Write(ctx, websocket.MessageText, hello); err != nil {
		return
	}

	go func() {
		defer stop()
		authed := s.opt.Token == ""
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var m struct {
				Type  string  `json:"type"`
				Token string  `json:"token"`
				Index int     `json:"index"`
				Step  int     `json:"step"`
				X     float64 `json:"x"`
				Y     float64 `json:"y"`
				On    bool    `json:"on"`
			}
			if json.Unmarshal(data, &m) != nil {
				continue
			}
			switch m.Type {
			case "auth":
				authed = s.opt.Token == "" || subtle.ConstantTimeCompare([]byte(m.Token), []byte(s.opt.Token)) == 1
				reply, _ := json.Marshal(map[string]any{"type": "auth", "ok": authed})
				c.Write(ctx, websocket.MessageText, reply)
			case "present":
				if authed && m.Index >= 0 && m.Step >= 0 {
					s.svc.Publish(id, map[string]any{"type": "present", "index": m.Index, "step": m.Step, "from": conn})
				}
			case "pointer":
				if authed {
					s.svc.Publish(id, map[string]any{"type": "pointer", "x": clamp01(m.X), "y": clamp01(m.Y), "on": m.On, "from": conn})
				}
			}
		}
	}()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				c.Close(websocket.StatusTryAgainLater, "too slow; reconnect")
				return
			}
			wctx, done := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageText, msg)
			done()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, done := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			done()
			if err != nil {
				return
			}
		}
	}
}

func clamp01(v float64) float64 { return max(0, min(1, v)) }

func (s *server) listComments(w http.ResponseWriter, r *http.Request) {
	cs, err := s.svc.Comments(r.Context(), r.PathValue("id"), r.URL.Query().Get("all") != "")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"comments": cs})
}

func (s *server) addComment(w http.ResponseWriter, r *http.Request) {
	var body struct{ SlideID, ElementID, Text string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeErr(w, err)
		return
	}
	c, err := s.svc.AddComment(r.Context(), r.PathValue("id"), body.SlideID, body.ElementID, body.Text, "human")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *server) resolveComment(w http.ResponseWriter, r *http.Request) {
	var body struct{ Reply string }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body)
	if err := s.svc.ResolveComment(r.Context(), r.PathValue("id"), r.PathValue("cid"), body.Reply); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) restore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseVersion *int `json:"baseVersion"`
		Version     int  `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, err)
		return
	}
	res, err := s.svc.Restore(r.Context(), r.PathValue("id"), body.BaseVersion, body.Version, "human")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

// spa serves the built web app, falling back to index.html for client routes.
func (s *server) spa() http.Handler {
	files := http.FileServerFS(s.opt.Web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := s.opt.Web.Open(p); err == nil {
				st, _ := f.Stat()
				f.Close()
				if st != nil && !st.IsDir() {
					if strings.HasPrefix(p, "assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					files.ServeHTTP(w, r)
					return
				}
			}
		}
		idx, err := fs.ReadFile(s.opt.Web, "index.html")
		if err != nil {
			http.Error(w, "web app not built (run: npm --prefix web run build)", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(idx)
	})
}

// serveFile serves an uploaded asset. Uploaded SVGs could carry script, so
// every file is served with a locked-down CSP sandbox.
func (s *server) serveFile(w http.ResponseWriter, r *http.Request) {
	a, path, err := s.svc.OpenAsset(r.Context(), r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", a.MIME)
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; media-src 'self'; sandbox")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, a.ID+a.Ext, a.CreatedAt, f)
}

func (s *server) uploadFile(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, service.MaxAssetBytes+1))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "file too large or unreadable"})
		return
	}
	name := r.URL.Query().Get("name")
	a, err := s.svc.UploadAsset(r.Context(), name, r.Header.Get("Content-Type"), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": a.ID, "url": a.URL(), "mime": a.MIME, "size": a.Size})
}

func (s *server) exportHTML(w http.ResponseWriter, r *http.Request) {
	if s.opt.Exporter == nil {
		http.Error(w, "export not configured", http.StatusNotImplemented)
		return
	}
	d, raw, _, err := s.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := s.opt.Exporter.HTML(r.Context(), raw)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+export.Filename(d.Title)+`"`)
	}
	w.Write(out)
}
