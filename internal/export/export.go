// Package export turns a deck into one self-contained HTML file: the player
// runtime, theme CSS, deck data and uploaded assets are all inlined, so the
// file plays offline with every animation.
package export

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"regexp"
	"strings"

	"github.com/SecOps-7/slidekick/internal/deck"
)

// AssetReader returns the MIME type and bytes of an uploaded asset ("/files/<name>").
type AssetReader func(ctx context.Context, name string) (mime string, data []byte, err error)

// Exporter builds standalone HTML decks.
type Exporter struct {
	Web    fs.FS // built web app; reads standalone/slidekick*.js and .css
	Cat    *deck.Catalog
	Themes *deck.ThemeSet
	Assets AssetReader
}

// HTML returns the standalone document for a raw deck.
func (x *Exporter) HTML(ctx context.Context, raw []byte) ([]byte, error) {
	d, err := deck.Parse(raw)
	if err != nil {
		return nil, err
	}
	theme, ok := x.Themes.Themes[d.Theme]
	if !ok {
		return nil, fmt.Errorf("unknown theme %q", d.Theme)
	}

	// Inline uploaded files as data: URIs.
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	var inlineErr error
	doc = walkStrings(doc, func(s string) string {
		if !strings.HasPrefix(s, "/files/") || inlineErr != nil {
			return s
		}
		mime, data, err := x.Assets(ctx, strings.TrimPrefix(s, "/files/"))
		if err != nil {
			inlineErr = fmt.Errorf("asset %s: %w", s, err)
			return s
		}
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	})
	if inlineErr != nil {
		return nil, inlineErr
	}

	// Catalog subset the player needs: layouts (with ordered regions) and the deck's theme.
	layouts := map[string]any{}
	for name, l := range x.Cat.Layouts {
		layouts[name] = map[string]any{"description": l.Description, "regions": x.Cat.LayoutRegions(name)}
	}
	data, err := json.Marshal(map[string]any{
		"deck":    doc,
		"catalog": map[string]any{"layouts": layouts, "transitions": x.Cat.Transitions, "builds": x.Cat.Builds, "themes": map[string]any{theme.Name: theme}},
	})
	if err != nil {
		return nil, err
	}
	// json.Marshal escapes <, > and & so the payload cannot close the script tag.

	bundle := "standalone/slidekick-lite.js"
	if usesDiagrams(d) {
		bundle = "standalone/slidekick.js"
	}
	js, err := fs.ReadFile(x.Web, bundle)
	if err != nil {
		return nil, fmt.Errorf("export runtime missing (%s): rebuild the web app", bundle)
	}
	css, err := fs.ReadFile(x.Web, "standalone/slidekick.css")
	if err != nil {
		return nil, fmt.Errorf("export stylesheet missing: rebuild the web app")
	}

	var b bytes.Buffer
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<meta name=\"generator\" content=\"Slidekick\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(d.Title))
	b.WriteString("<style>\n")
	// Theme fonts, inlined so the file needs no network at all.
	b.WriteString(x.Themes.FontFaceCSS(theme, func(file string) string {
		data, _ := x.Themes.FontFile(file)
		return "data:font/woff2;base64," + base64.StdEncoding.EncodeToString(data)
	}))
	b.Write(safeStyle(css))
	b.WriteString("\n</style>\n")
	fmt.Fprintf(&b, "<style data-sk-theme=\"%s\">\n", html.EscapeString(theme.Name))
	b.Write(safeStyle(x.Themes.CSS[theme.Name]))
	b.WriteString("\n</style>\n</head>\n<body>\n<div id=\"app\"></div>\n")
	b.WriteString("<script type=\"application/json\" id=\"sk-data\">")
	b.Write(data)
	b.WriteString("</script>\n<script>\n")
	b.Write(safeScript(js))
	b.WriteString("\n</script>\n</body>\n</html>\n")
	return b.Bytes(), nil
}

func usesDiagrams(d *deck.Deck) bool {
	for _, s := range d.Slides {
		for _, els := range s.Regions {
			for _, e := range els {
				if e.Type == "diagram" {
					return true
				}
			}
		}
	}
	return false
}

func walkStrings(v any, f func(string) string) any {
	switch t := v.(type) {
	case string:
		return f(t)
	case map[string]any:
		for k, c := range t {
			t[k] = walkStrings(c, f)
		}
	case []any:
		for i := range t {
			t[i] = walkStrings(t[i], f)
		}
	}
	return v
}

var (
	closeScript = regexp.MustCompile(`(?i)</(script)`)
	closeStyle  = regexp.MustCompile(`(?i)</(style)`)
)

// safeScript stops inlined JS from ending the <script> element early.
// "<\/script" means the same thing inside JS strings, regexes and comments.
func safeScript(b []byte) []byte { return closeScript.ReplaceAll(b, []byte(`<\/$1`)) }

func safeStyle(b []byte) []byte { return closeStyle.ReplaceAll(b, []byte(`<\/$1`)) }

// Filename suggests a download name for a deck title.
func Filename(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "deck"
	}
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	return s + ".html"
}
