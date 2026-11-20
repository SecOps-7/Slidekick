// Package golden compares screenshots of the example decks with reference
// images in examples/golden, so a change that alters how slides look is
// caught in review. It drives a running Slidekick render image through the
// real MCP tools (create_deck, render_slide), exactly as an agent would.
//
// Skipped unless SLIDEKICK_GOLDEN_URL is set:
//
//	SLIDEKICK_GOLDEN_URL=http://127.0.0.1:7411 go test ./internal/golden
//
// Set SLIDEKICK_GOLDEN_UPDATE=1 to rewrite the references, and
// SLIDEKICK_GOLDEN_OUT=dir to keep actual and diff images of failures.
package golden

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type shot struct {
	example, theme string
	slide          int
}

func (s shot) name() string {
	return fmt.Sprintf("%s-%s-%d", s.example, s.theme, s.slide+1)
}

// shots covers every layout, every theme and every element type at least once.
func shots() []shot {
	var out []shot
	for i := range 7 {
		out = append(out, shot{"launch", "aurora", i}, shot{"showcase", "ember", i})
	}
	for i := range 4 {
		out = append(out, shot{"gallery", "paper", i})
	}
	out = append(out, shot{"launch", "swiss", 0}, shot{"showcase", "meadow", 2}, shot{"showcase", "swiss", 4}, shot{"gallery", "meadow", 1})
	// A sample of the themes added later (each is also contrast-linted in every theme by internal/lint).
	out = append(out, shot{"showcase", "synthwave", 1}, shot{"launch", "magazine", 0}, shot{"gallery", "contrast", 1}, shot{"showcase", "blueprint", 3})
	return out
}

const (
	width = 960
	// A pixel differs when any channel moves by more than channelTol; a shot
	// fails when more than maxDiffFrac of its pixels differ. This absorbs
	// antialiasing noise but catches moved, missing or restyled content.
	channelTol  = 32
	maxDiffFrac = 0.004
)

type bearer struct{ tok string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.tok != "" {
		r.Header.Set("Authorization", "Bearer "+b.tok)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: %s", name, res.Content[0].(*mcp.TextContent).Text)
	}
	return res
}

func TestGoldenScreenshots(t *testing.T) {
	base := os.Getenv("SLIDEKICK_GOLDEN_URL")
	if base == "" {
		t.Skip("set SLIDEKICK_GOLDEN_URL to a running render image to compare screenshots")
	}
	update := os.Getenv("SLIDEKICK_GOLDEN_UPDATE") == "1"
	outDir := os.Getenv("SLIDEKICK_GOLDEN_OUT")
	refDir := filepath.Join("..", "..", "examples", "golden")

	tr := &mcp.StreamableClientTransport{Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearer{os.Getenv("SLIDEKICK_GOLDEN_TOKEN")}}}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "golden"}, nil).Connect(context.Background(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	decks := map[string]string{}
	for _, s := range shots() {
		t.Run(s.name(), func(t *testing.T) {
			key := s.example + "/" + s.theme
			if decks[key] == "" {
				raw, err := os.ReadFile(filepath.Join("..", "..", "examples", s.example+".json"))
				if err != nil {
					t.Fatal(err)
				}
				var d struct {
					Title  string `json:"title"`
					Slides []any  `json:"slides"`
				}
				json.Unmarshal(raw, &d)
				res := call(t, cs, "create_deck", map[string]any{"title": d.Title, "theme": s.theme, "slides": d.Slides})
				var out struct {
					DeckID string `json:"deckId"`
				}
				b, _ := json.Marshal(res.StructuredContent)
				json.Unmarshal(b, &out)
				decks[key] = out.DeckID
			}
			res := call(t, cs, "render_slide", map[string]any{"deck_id": decks[key], "slide": s.slide, "width": width})
			var got []byte
			for _, c := range res.Content {
				if img, ok := c.(*mcp.ImageContent); ok {
					got = img.Data
				}
			}
			if got == nil {
				t.Fatal("render_slide returned no image")
			}
			ref := filepath.Join(refDir, s.name()+".png")
			if update {
				os.MkdirAll(refDir, 0o755)
				if err := os.WriteFile(ref, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(ref)
			if err != nil {
				t.Fatalf("no reference %s (run with SLIDEKICK_GOLDEN_UPDATE=1): %v", ref, err)
			}
			frac, diff, err := compare(want, got)
			if err != nil {
				t.Fatal(err)
			}
			if frac > maxDiffFrac {
				if outDir != "" {
					os.MkdirAll(outDir, 0o755)
					os.WriteFile(filepath.Join(outDir, s.name()+".actual.png"), got, 0o644)
					if diff != nil {
						var buf bytes.Buffer
						png.Encode(&buf, diff)
						os.WriteFile(filepath.Join(outDir, s.name()+".diff.png"), buf.Bytes(), 0o644)
					}
				}
				t.Errorf("%.2f%% of pixels differ from %s (limit %.2f%%)", frac*100, ref, maxDiffFrac*100)
			}
		})
	}
}

// compare returns the fraction of differing pixels and an image marking them.
func compare(a, b []byte) (float64, image.Image, error) {
	ia, err := png.Decode(bytes.NewReader(a))
	if err != nil {
		return 0, nil, fmt.Errorf("reference: %w", err)
	}
	ib, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, nil, fmt.Errorf("actual: %w", err)
	}
	if ia.Bounds() != ib.Bounds() {
		return 1, nil, nil
	}
	r := ia.Bounds()
	diff := image.NewRGBA(r)
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ar, ag, ab, _ := ia.At(x, y).RGBA()
			br, bg, bb, _ := ib.At(x, y).RGBA()
			if far(ar, br) || far(ag, bg) || far(ab, bb) {
				n++
				diff.Set(x, y, color.RGBA{255, 0, 64, 255})
			} else {
				g := uint8((ar>>8 + ag>>8 + ab>>8) / 3 / 3)
				diff.Set(x, y, color.RGBA{g, g, g, 255})
			}
		}
	}
	return float64(n) / float64(r.Dx()*r.Dy()), diff, nil
}

func far(a, b uint32) bool {
	d := int(a>>8) - int(b>>8)
	return d > channelTol || d < -channelTol
}
