// Package render takes PNG screenshots of slides with headless Chromium
// (chromedp). It loads the real viewer page in render mode, so screenshots
// match what people see. Chromium is optional: without it, Available reports
// false and callers explain how to enable rendering.
package render

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// ErrUnavailable means no Chromium binary was found.
var ErrUnavailable = errors.New("slide rendering needs Chromium, which is not installed in this build; run the -render image (or set SLIDEKICK_CHROME to a Chromium binary)")

// Renderer owns one long-lived headless browser and opens a tab per render.
type Renderer struct {
	base string // internal base URL of this server, e.g. http://127.0.0.1:7410
	exe  string
	log  *slog.Logger

	mu      sync.Mutex
	browser context.Context
	stop    context.CancelFunc
	sem     chan struct{}
	proxy   *egressProxy
}

// New finds Chromium (SLIDEKICK_CHROME, then common names on PATH).
func New(internalBase string, log *slog.Logger) *Renderer {
	r := &Renderer{base: internalBase, log: log, sem: make(chan struct{}, 2)}
	candidates := []string{os.Getenv("SLIDEKICK_CHROME"), "chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "headless-shell"}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			r.exe = p
			break
		}
	}
	return r
}

// Available reports whether Chromium was found.
func (r *Renderer) Available() bool { return r.exe != "" }

func (r *Renderer) browserCtx() (context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.browser != nil && r.browser.Err() == nil {
		return r.browser, nil
	}
	if r.proxy == nil {
		u, err := url.Parse(r.base)
		if err != nil {
			return nil, err
		}
		p, err := startProxy(u.Host)
		if err != nil {
			return nil, fmt.Errorf("start egress proxy: %w", err)
		}
		r.proxy = p
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(r.exe),
		// All traffic, loopback included, goes through the egress filter.
		chromedp.Flag("proxy-server", "http://"+r.proxy.addr()),
		chromedp.Flag("proxy-bypass-list", "<-loopback>"),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("force-webrtc-ip-handling-policy", "disable_non_proxied_udp"),
		chromedp.NoSandbox, // containers run without user namespaces
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("autoplay-policy", "user-gesture-required"),
		chromedp.WindowSize(1920, 1080),
	)
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	bctx, cancelB := chromedp.NewContext(alloc)
	// The first Run starts the browser; its context must not carry a timeout.
	if err := chromedp.Do(bctx); err != nil {
		cancelB()
		cancelAlloc()
		return nil, fmt.Errorf("start chromium: %w", err)
	}
	r.browser = bctx
	r.stop = func() { cancelB(); cancelAlloc() }
	r.log.Info("render: chromium started", "exe", r.exe)
	return bctx, nil
}

// Close stops the browser.
func (r *Renderer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stop != nil {
		r.stop()
		r.browser, r.stop = nil, nil
	}
	if r.proxy != nil {
		r.proxy.close()
		r.proxy = nil
	}
}

// PDF prints the deck's print view (every slide, one per page).
func (r *Renderer) PDF(ctx context.Context, deckID string) ([]byte, error) {
	var out []byte
	err := r.withPage(ctx, fmt.Sprintf("%s/d/%s/print", r.base, url.PathEscape(deckID)), 1920, 1080, 90*time.Second, func(tctx context.Context) error {
		b, err := chromedp.Run(tctx, chromedp.PrintToPDF(chromedp.PDFPreferCSSPageSize(), chromedp.PDFPrintBackground(), chromedp.PDFMargin(0)))
		out = b
		return err
	})
	return out, err
}

// Slide renders one slide at a build step (-1 = fully built) and returns a PNG.
func (r *Renderer) Slide(ctx context.Context, deckID string, slide, step, width, height int) ([]byte, error) {
	if step < 0 {
		step = 99 // the player clamps to the last build
	}
	u := fmt.Sprintf("%s/d/%s?render=1#%d.%d", r.base, url.PathEscape(deckID), slide+1, step)
	var out []byte
	err := r.withPage(ctx, u, width, height, 30*time.Second, func(tctx context.Context) error {
		b, err := chromedp.Run(tctx, chromedp.CaptureScreenshot())
		out = b
		return err
	})
	return out, err
}

// withPage opens u in a new tab at the given viewport, waits for the page to
// set window.__skReady, then runs fn.
func (r *Renderer) withPage(ctx context.Context, u string, width, height int, timeout time.Duration, fn func(context.Context) error) error {
	if !r.Available() {
		return ErrUnavailable
	}
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	bctx, err := r.browserCtx()
	if err != nil {
		return err
	}
	tab, closeTab := chromedp.NewContext(bctx)
	defer closeTab()
	tctx, cancel := context.WithTimeout(tab, timeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	if err := chromedp.Do(tctx, chromedp.EmulateViewport(int64(width), int64(height)), chromedp.Navigate(u)); err != nil {
		r.resetIfDead()
		return fmt.Errorf("load viewer: %w", err)
	}
	if _, err := chromedp.Run(tctx, chromedp.Poll[bool]("window.__skReady === true", chromedp.WithPollingTimeout(timeout-5*time.Second))); err != nil {
		return fmt.Errorf("page did not finish rendering: %w", err)
	}
	if err := fn(tctx); err != nil {
		r.resetIfDead()
		return err
	}
	return nil
}

// resetIfDead drops a crashed browser so the next call starts a new one.
func (r *Renderer) resetIfDead() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.browser != nil && r.browser.Err() != nil {
		r.browser = nil
	}
}
