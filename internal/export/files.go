package export

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/render"
	"github.com/SecOps-7/slidekick/internal/store"
)

// PDF prints every slide, fully built, one per page at the deck's own size.
func PDF(ctx context.Context, r *render.Renderer, deckID string) ([]byte, error) {
	if r == nil || !r.Available() {
		return nil, render.ErrUnavailable
	}
	return r.PDF(ctx, deckID)
}

// PNGZip renders every slide, fully built, and returns a zip of PNGs
// named 01-<slide id>.png, 02-... at the given width.
func PNGZip(ctx context.Context, r *render.Renderer, d *deck.Deck, width int) ([]byte, error) {
	if r == nil || !r.Available() {
		return nil, render.ErrUnavailable
	}
	if len(d.Slides) == 0 {
		return nil, fmt.Errorf("the deck has no slides")
	}
	height := width * 9 / 16
	if d.Aspect == "4:3" {
		height = width * 3 / 4
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, s := range d.Slides {
		png, err := r.Slide(ctx, d.ID, i, -1, width, height)
		if err != nil {
			return nil, fmt.Errorf("slide %d: %w", i+1, err)
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("%02d-%s.png", i+1, s.ID), Method: zip.Store, Modified: time.Now()})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(png); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BaseName is Filename without the .html extension.
func BaseName(title string) string { return strings.TrimSuffix(Filename(title), ".html") }

// Saver stores a generated file and returns its asset.
type Saver func(ctx context.Context, name, mime string, data []byte) (*store.Asset, error)

// MakeFile builds a PDF or PNG-zip export of a deck and stores it.
func MakeFile(ctx context.Context, r *render.Renderer, d *deck.Deck, format string, save Saver) (*store.Asset, error) {
	switch format {
	case "pdf":
		b, err := PDF(ctx, r, d.ID)
		if err != nil {
			return nil, err
		}
		return save(ctx, BaseName(d.Title)+".pdf", "application/pdf", b)
	case "png":
		b, err := PNGZip(ctx, r, d, 1920)
		if err != nil {
			return nil, err
		}
		return save(ctx, BaseName(d.Title)+"-slides.zip", "application/zip", b)
	}
	return nil, fmt.Errorf("format %q is not supported (use html, pdf or png)", format)
}
