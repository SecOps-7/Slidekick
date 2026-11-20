// Command slidekick runs the Slidekick server.
//
//	slidekick serve   HTTP server: web app, live sync, MCP at /mcp (default)
//	slidekick stdio   MCP over stdin/stdout for local agents, plus the web app
//	slidekick version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick"
	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/export"
	"github.com/SecOps-7/slidekick/internal/hub"
	"github.com/SecOps-7/slidekick/internal/mcpserver"
	"github.com/SecOps-7/slidekick/internal/render"
	"github.com/SecOps-7/slidekick/internal/server"
	"github.com/SecOps-7/slidekick/internal/service"
	"github.com/SecOps-7/slidekick/internal/store"
)

var version = "0.1.0-dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "slidekick")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "share", "slidekick")
	}
	return "./data"
}

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	// Logs go to stderr: stdout belongs to the MCP stdio transport.
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	fl := flag.NewFlagSet(cmd, flag.ExitOnError)
	defAddr := ":7410"
	if cmd == "stdio" {
		defAddr = "127.0.0.1:7410"
	}
	addr := fl.String("addr", env("SLIDEKICK_ADDR", defAddr), "HTTP listen address")
	data := fl.String("data", env("SLIDEKICK_DATA", defaultDataDir()), "data directory (SQLite database)")
	baseURL := fl.String("base-url", env("SLIDEKICK_BASE_URL", ""), "public base URL used in share links (default http://localhost:<port>)")
	fl.Parse(args)

	switch cmd {
	case "version":
		fmt.Println("slidekick", version)
		return
	case "health":
		// Used by the container HEALTHCHECK (distroless has no curl).
		_, port, _ := net.SplitHostPort(*addr)
		c := http.Client{Timeout: 2 * time.Second}
		r, err := c.Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil || r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	case "serve", "stdio":
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (want serve, stdio or version)\n", cmd)
		os.Exit(2)
	}

	if *baseURL == "" {
		_, port, _ := net.SplitHostPort(*addr)
		*baseURL = "http://localhost:" + port
	}
	token := os.Getenv("SLIDEKICK_TOKEN")
	if token != "" && len(token) < 24 {
		fmt.Fprintln(os.Stderr, "SLIDEKICK_TOKEN is too short: use at least 24 random characters, e.g. `openssl rand -hex 24`")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cmd, *addr, *data, *baseURL, token, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd, addr, data, baseURL, token string, log *slog.Logger) error {
	cat, err := deck.LoadCatalog(slidekick.Schema)
	if err != nil {
		return err
	}
	themes, err := deck.LoadThemes(slidekick.Themes)
	if err != nil {
		return err
	}
	v, err := deck.NewValidator(slidekick.Schema, cat, themes)
	if err != nil {
		return err
	}
	st, err := store.Open(data)
	if err != nil {
		return err
	}
	defer st.Close()

	svc := service.New(st, v, cat, themes, hub.New(), baseURL)
	svc.FilesDir = filepath.Join(data, "files")
	if svc.Blobs, err = openBlobs(ctx, data, log); err != nil {
		return err
	}

	exp := &export.Exporter{Web: slidekick.Web(), Cat: cat, Themes: themes,
		Assets: func(ctx context.Context, name string) (string, []byte, error) {
			a, b, err := svc.ReadAsset(ctx, name)
			if err != nil {
				return "", nil, err
			}
			return a.MIME, b, nil
		}}
	_, port, _ := net.SplitHostPort(addr)
	rnd := render.New("http://127.0.0.1:"+port, log)
	rnd.ViewKey = svc.ViewKey
	defer rnd.Close()
	log.Info("slide rendering", "available", rnd.Available())

	mcpSrv := mcpserver.New(svc, slidekick.Schema, version, mcpserver.Options{Renderer: rnd, Exporter: exp,
		Files: func(ctx context.Context, d *deck.Deck, format string) (*store.Asset, error) {
			return export.MakeFile(ctx, rnd, d, format, svc.SaveGenerated)
		}})
	files := func(ctx context.Context, deckID, format string) (*store.Asset, error) {
		d, _, _, err := svc.Get(ctx, deckID)
		if err != nil {
			return nil, err
		}
		return export.MakeFile(ctx, rnd, d, format, svc.SaveGenerated)
	}
	handler := server.New(svc, mcpSrv, server.Options{Token: token, SchemaFS: slidekick.Schema, Web: slidekick.Web(), Log: log, Exporter: exp, Files: files})

	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute}
	httpErr := make(chan error, 1)
	go func() {
		log.Info("slidekick listening", "addr", addr, "url", baseURL, "data", data, "auth", token != "")
		if token == "" && !isLoopback(addr) {
			log.Warn("SLIDEKICK_TOKEN is not set: /mcp and the write API are open to anyone who can reach this port")
		}
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		httpErr <- err
	}()

	if cmd == "stdio" {
		// The web viewer is a convenience in stdio mode: if the port is busy
		// (another slidekick already serving), keep MCP working without it.
		go func() {
			if err := <-httpErr; err != nil {
				log.Warn("web viewer unavailable; MCP over stdio continues", "err", err)
			}
		}()
		err := mcpSrv.Run(ctx, &mcp.StdioTransport{})
		shutdown(srv)
		return err
	}

	select {
	case <-ctx.Done():
		shutdown(srv)
		return nil
	case err := <-httpErr:
		return err
	}
}

func shutdown(srv *http.Server) {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(c)
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}
