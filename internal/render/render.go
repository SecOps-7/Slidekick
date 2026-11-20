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
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(r.exe),
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
}

// Slide renders one slide at a build step (-1 = fully built) and returns a PNG.
func (r *Renderer) Slide(ctx context.Context, deckID string, slide, step, width, height int) ([]byte, error) {
	if !r.Available() {
		return nil, ErrUnavailable
	}
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	bctx, err := r.browserCtx()
	if err != nil {
		return nil, err
	}
	tab, closeTab := chromedp.NewContext(bctx)
	defer closeTab()
	tctx, cancel := context.WithTimeout(tab, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	if step < 0 {
		step = 99 // the player clamps to the last build
	}
	u := fmt.Sprintf("%s/d/%s?render=1#%d.%d", r.base, url.PathEscape(deckID), slide+1, step)
	if err := chromedp.Do(tctx,
		chromedp.EmulateViewport(int64(width), int64(height)),
		chromedp.Navigate(u),
	); err != nil {
		return nil, fmt.Errorf("load viewer: %w", err)
	}
	if _, err := chromedp.Run(tctx, chromedp.Poll[bool]("window.__skReady === true", chromedp.WithPollingTimeout(20*time.Second))); err != nil {
		return nil, fmt.Errorf("slide did not finish rendering: %w", err)
	}
	png, err := chromedp.Run(tctx, chromedp.CaptureScreenshot())
	if err != nil {
		// A dead browser is restarted on the next call.
		r.mu.Lock()
		if r.browser != nil && r.browser.Err() != nil {
			r.browser = nil
		}
		r.mu.Unlock()
		return nil, fmt.Errorf("screenshot: %w", err)
	}
	return png, nil
}
