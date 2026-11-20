// Package server is the HTTP front end: the REST API, the WebSocket live
// sync, the MCP streamable HTTP endpoint and the embedded web app.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick/internal/accounts"
	"github.com/SecOps-7/slidekick/internal/ai"
	"github.com/SecOps-7/slidekick/internal/authz"
	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/export"
	"github.com/SecOps-7/slidekick/internal/service"
	"github.com/SecOps-7/slidekick/internal/store"
)

type Options struct {
	Token    string // server token (SLIDEKICK_TOKEN): full access for automation and recovery
	BaseURL  string // public URL: Secure cookies when https, SSO redirect URLs, origin checks
	Accounts *accounts.Service
	AI       *ai.Service // nil: no AI chat
	// TrustedProxies are the addresses of a TLS-terminating reverse proxy.
	// Only requests from them may set X-Real-IP / X-Forwarded-For (the client
	// address used for lockouts) and X-Forwarded-Proto (https -> Secure cookies).
	TrustedProxies []netip.Prefix
	SchemaFS       fs.FS
	Web            fs.FS
	Log            *slog.Logger
	Exporter       *export.Exporter
	// Files makes a binary export (pdf or png) and stores it; see export.MakeFile.
	Files func(ctx context.Context, deckID, format string) (*store.Asset, error)
}

type server struct {
	svc     *service.Service
	acc     *accounts.Service
	opt     Options
	fails   *failLimiter
	conns   *connLimiter
	exports *exportCache
}

// New returns the root handler.
func New(svc *service.Service, mcpServer *mcp.Server, opt Options) http.Handler {
	s := &server{svc: svc, acc: opt.Accounts, opt: opt, fails: newFailLimiter(), conns: newConnLimiter(50, 500), exports: newExportCache(2, 8)}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") })
	// Every route states the action it needs; authz.Policy decides (see access.go).
	// Deck ids are share links: listing needs a signed-in user (a deck token sees only its deck).
	mux.Handle("GET /api/decks", s.require(authz.ListDecks, false, s.listDecks))
	mux.Handle("POST /api/decks", s.require(authz.Create, false, s.createDeck))
	mux.HandleFunc("GET /api/decks/{id}", s.viewGate(s.getDeck))
	mux.HandleFunc("GET /api/decks/{id}/history", s.viewGate(s.history))
	mux.Handle("POST /api/decks/{id}/patch", s.require(authz.Edit, true, s.patchDeck))
	mux.HandleFunc("GET /api/catalog", s.catalog)
	mux.HandleFunc("GET /api/decks/{id}/export.html", s.viewGate(s.exportHTML))
	mux.Handle("POST /api/decks/{id}/export", s.require(authz.Export, true, s.exportFile))
	mux.Handle("GET /api/auth", s.require(anyAuthenticated, false, s.authInfo))
	mux.HandleFunc("GET /api/decks/{id}/comments", s.viewGate(s.listComments))
	mux.Handle("POST /api/decks/{id}/comments", s.require(authz.Comment, true, s.addComment))
	mux.Handle("POST /api/decks/{id}/comments/{cid}/resolve", s.require(authz.Edit, true, s.resolveComment))
	mux.Handle("POST /api/decks/{id}/restore", s.require(authz.Edit, true, s.restore))
	mux.Handle("GET /api/decks/{id}/access", s.require(authz.Share, true, s.getAccess))
	mux.Handle("POST /api/decks/{id}/access", s.require(authz.Share, true, s.setAccess))
	mux.Handle("POST /api/decks/{id}/edit-token", s.require(authz.MintDeckToken, true, s.issueEditToken))
	mux.Handle("DELETE /api/decks/{id}/edit-token", s.require(authz.MintDeckToken, true, s.revokeEditToken))
	mux.Handle("PUT /api/decks/{id}/owner", s.require(authz.ManageUsers, true, s.setDeckOwner))
	mux.Handle("DELETE /api/decks/{id}", s.require(authz.Delete, true, s.deleteDeck))
	mux.Handle("POST /api/files", s.require(authz.Upload, false, s.uploadFile))
	s.accountRoutes(mux)
	s.aiRoutes(mux)
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
	mcpCapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 24<<20) // upload_asset carries up to 15 MB as base64
		mcpHandler.ServeHTTP(w, r)
	})
	mux.Handle("/mcp", s.mcpAuth(mcpCapped))
	mux.Handle("/mcp/", s.mcpAuth(mcpCapped))

	mux.Handle("/", s.spa())
	return s.proxyHeaders(securityHeaders(mux))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
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
	p := authFrom(r.Context()).p
	if p.Kind == authz.KindDeck {
		mine := rows[:0]
		for _, row := range rows {
			if row.ID == p.Deck {
				mine = append(mine, row)
			}
		}
		rows = mine
	}
	// Tell the UI what this caller may do with each deck (the server checks again on use).
	type listed struct {
		store.Summary
		CanEdit   bool `json:"canEdit"`
		CanDelete bool `json:"canDelete"`
	}
	pol := s.policy()
	out := make([]listed, 0, len(rows))
	for _, row := range rows {
		d := &authz.Deck{ID: row.ID, Owner: row.Owner}
		out = append(out, listed{row, pol.Can(p, authz.Edit, d), pol.Can(p, authz.Delete, d)})
	}
	writeJSON(w, 200, map[string]any{"decks": out})
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
	key := r.URL.Query().Get("k")
	// A private deck without its key needs an auth message (deck or server
	// token) before anything is sent: browsers can't set headers on WebSockets.
	keyOK := s.svc.CanView(r.Context(), id, key)
	ip := clientIP(r)
	if !s.conns.acquire(ip, id) {
		http.Error(w, "too many live connections", http.StatusServiceUnavailable)
		return
	}
	defer s.conns.release(ip, id)
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(8 << 10)
	// Signed-in browsers authenticate with their session cookie (the Origin
	// check in websocket.Accept stops other sites from using it); token
	// holders send {"type":"auth","token":...}.
	cookieOK := false
	if a, _ := s.identify(r); a.p.Authenticated() {
		if d, err := s.deckRef(r.Context(), id); err == nil {
			cookieOK = s.policy().Can(a.p, authz.Present, d)
		}
	}
	authed := cookieOK
	tokenAuth := func(tok string) bool { return tok != "" && s.wsAuth(r.Context(), ip, tok, id) }
	if !keyOK && !authed {
		actx, done := context.WithTimeout(r.Context(), 10*time.Second)
		_, data, err := c.Read(actx)
		done()
		var m struct{ Type, Token string }
		if err != nil || json.Unmarshal(data, &m) != nil || m.Type != "auth" || !tokenAuth(m.Token) {
			c.Close(websocket.StatusPolicyViolation, "deck not found")
			return
		}
		authed = true
		reply, _ := json.Marshal(map[string]any{"type": "auth", "ok": true})
		c.Write(r.Context(), websocket.MessageText, reply)
	}
	ch, cancel := s.svc.Hub.Subscribe(id)
	defer cancel()
	ctx, stop := context.WithCancel(r.Context())
	defer stop()
	conn := deck.NewID("w")

	hello, _ := json.Marshal(map[string]any{"type": "hello", "version": ver, "conn": conn})
	if err := c.Write(ctx, websocket.MessageText, hello); err != nil {
		return
	}

	var authMu sync.Mutex
	isAuthed := func() bool { authMu.Lock(); defer authMu.Unlock(); return authed }
	go func() {
		defer stop()
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
				ok := cookieOK || tokenAuth(m.Token)
				authMu.Lock()
				authed = ok || (authed && !keyOK) // a key-less private viewer keeps its earlier proof
				authMu.Unlock()
				reply, _ := json.Marshal(map[string]any{"type": "auth", "ok": ok})
				c.Write(ctx, websocket.MessageText, reply)
			case "present":
				if isAuthed() && m.Index >= 0 && m.Step >= 0 {
					s.svc.Publish(id, map[string]any{"type": "present", "index": m.Index, "step": m.Step, "from": conn})
				}
			case "pointer":
				if isAuthed() {
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
			if string(msg) == accessChanged && !isAuthed() && !s.svc.CanView(ctx, id, key) {
				c.Close(websocket.StatusPolicyViolation, "deck not found") // made private or key rotated
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

// accessChanged is what service.SetPrivate publishes.
const accessChanged = `{"type":"access"}`

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

// appCSP locks the app page down: scripts only from this server, images and
// media from here, data: or https, embeds only over https.
const appCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; " +
	"media-src 'self' blob: https:; font-src 'self' data:; connect-src 'self' ws: wss:; frame-src https:; " +
	"object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

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
		w.Header().Set("Content-Security-Policy", appCSP)
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
	a, f, err := s.svc.OpenAsset(r.Context(), r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, "file storage unavailable", http.StatusBadGateway)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", a.MIME)
	if a.SHA256 != "" {
		h.Set("ETag", `"`+a.SHA256+`"`)
	}
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; media-src 'self'; sandbox")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	if a.MIME == "application/pdf" || a.MIME == "application/zip" {
		h.Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(a.Name, `"`, "")+`"`)
	}
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
	d, raw, ver, err := s.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	key := fmt.Sprintf("%s@%d", d.ID, ver)
	out, ok := s.exports.get(key)
	if !ok {
		select {
		case s.exports.sem <- struct{}{}:
		case <-r.Context().Done():
			return
		}
		out, err = s.opt.Exporter.HTML(r.Context(), raw)
		<-s.exports.sem
		if err != nil {
			writeErr(w, err)
			return
		}
		s.exports.put(key, out)
	}
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+export.Filename(d.Title)+`"`)
	}
	w.Write(out)
}

// exportFile makes a PDF or PNG zip (headless Chromium), stores it as an
// asset and returns its download URL. Needs the token: rendering is costly.
func (s *server) exportFile(w http.ResponseWriter, r *http.Request) {
	if s.opt.Files == nil {
		http.Error(w, "export not configured", http.StatusNotImplemented)
		return
	}
	a, err := s.opt.Files(r.Context(), r.PathValue("id"), r.URL.Query().Get("format"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"url": a.URL(), "name": a.Name, "bytes": a.Size})
}
