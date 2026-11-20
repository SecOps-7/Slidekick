package deck

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Layout describes a layout preset and its regions. Regions maps region
// name to a rough character budget used by the overflow lint.
type Layout struct {
	Description string         `json:"description"`
	Regions     map[string]int `json:"regions"`
}

// Catalog is the layout and animation preset catalogue (schema/catalog.json).
type Catalog struct {
	Layouts     map[string]Layout `json:"layouts"`
	Transitions map[string]string `json:"transitions"`
	Builds      map[string]string `json:"builds"`
	Elements    map[string]string `json:"elements"`
	Icons       []string          `json:"icons"`
	iconSet     map[string]bool
	// regionOrder keeps each layout's regions in file order.
	regionOrder map[string][]string
}

// LoadCatalog reads schema/catalog.json from the embedded schema FS.
func LoadCatalog(fsys fs.FS) (*Catalog, error) {
	raw, err := fs.ReadFile(fsys, "schema/catalog.json")
	if err != nil {
		return nil, err
	}
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("catalog.json: %w", err)
	}
	// Recover declaration order, which encoding/json maps lose.
	var ordered struct {
		Layouts map[string]struct {
			Regions json.RawMessage `json:"regions"`
		} `json:"layouts"`
	}
	if err := json.Unmarshal(raw, &ordered); err != nil {
		return nil, err
	}
	c.iconSet = map[string]bool{}
	for _, i := range c.Icons {
		c.iconSet[i] = true
	}
	c.regionOrder = map[string][]string{}
	for name, l := range ordered.Layouts {
		keys, err := objectKeys(l.Regions)
		if err != nil {
			return nil, err
		}
		c.regionOrder[name] = keys
	}
	return &c, nil
}

func objectKeys(raw json.RawMessage) ([]string, error) {
	dec := json.NewDecoder(bytesReader(raw))
	if _, err := dec.Token(); err != nil { // {
		return nil, err
	}
	var keys []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		keys = append(keys, t.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// HasIcon reports whether name is in the icon catalogue.
func (c *Catalog) HasIcon(name string) bool { return c.iconSet[name] }

// LayoutRegions returns a layout's region names in declaration order.
func (c *Catalog) LayoutRegions(layout string) []string { return c.regionOrder[layout] }

// RegionOrder returns region names for a slide: the layout's own order first,
// then any extra regions present on the slide, sorted.
func (c *Catalog) RegionOrder(layout string, present map[string][]Element) []string {
	known := c.regionOrder[layout]
	seen := map[string]bool{}
	out := make([]string, 0, len(present))
	for _, r := range known {
		seen[r] = true
		if _, ok := present[r]; ok {
			out = append(out, r)
		}
	}
	var extra []string
	for r := range present {
		if !seen[r] {
			extra = append(extra, r)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// Theme is a theme's design tokens (themes/<name>/theme.json).
type Theme struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Dark        bool   `json:"dark"`
	Tokens      struct {
		Color  map[string]string `json:"color"`
		Font   map[string]string `json:"font"`
		Fonts  []string          `json:"fonts" jsonschema:"self-hosted font families (keys of themes/fonts/faces.json)"`
		Size   map[string]int    `json:"size"`
		Radius int               `json:"radius"`
		Motion struct {
			Fast   int    `json:"fast"`
			Base   int    `json:"base"`
			Slow   int    `json:"slow"`
			Easing string `json:"easing"`
		} `json:"motion"`
	} `json:"tokens"`
}

// ThemeSet holds every theme, keyed by name, with its CSS and fonts.
type ThemeSet struct {
	Themes map[string]*Theme
	CSS    map[string][]byte
	// Faces maps a font key to its @font-face entries (themes/fonts/faces.json).
	Faces map[string][]FontFace
	fsys  fs.FS
}

// FontFace is one self-hosted font file and its @font-face descriptors.
type FontFace struct {
	Family       string `json:"family"`
	File         string `json:"file"`
	Style        string `json:"style"`
	Weight       string `json:"weight"`
	Stretch      string `json:"stretch"`
	UnicodeRange string `json:"unicodeRange"`
}

// FontFile returns a self-hosted font file's bytes; ok is false for unknown files.
func (t *ThemeSet) FontFile(file string) ([]byte, bool) {
	for _, faces := range t.Faces {
		for _, f := range faces {
			if f.File == file {
				b, err := fs.ReadFile(t.fsys, path.Join("themes", "fonts", file))
				return b, err == nil
			}
		}
	}
	return nil, false
}

// FontFaceCSS returns @font-face rules for a theme's fonts; src(file) gives each file's URL.
func (t *ThemeSet) FontFaceCSS(theme *Theme, src func(file string) string) string {
	var b strings.Builder
	for _, key := range theme.Tokens.Fonts {
		for _, f := range t.Faces[key] {
			fmt.Fprintf(&b, "@font-face{font-family:%q;font-style:%s;font-weight:%s;font-display:swap;", f.Family, f.Style, f.Weight)
			if f.Stretch != "" {
				fmt.Fprintf(&b, "font-stretch:%s;", f.Stretch)
			}
			fmt.Fprintf(&b, "src:url(%q) format(\"woff2\");", src(f.File))
			if f.UnicodeRange != "" {
				fmt.Fprintf(&b, "unicode-range:%s;", f.UnicodeRange)
			}
			b.WriteString("}\n")
		}
	}
	return b.String()
}

// Names returns theme names sorted.
func (t *ThemeSet) Names() []string {
	out := make([]string, 0, len(t.Themes))
	for n := range t.Themes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// LoadThemes reads every themes/<name>/theme.json (+ optional theme.css).
func LoadThemes(fsys fs.FS) (*ThemeSet, error) {
	entries, err := fs.ReadDir(fsys, "themes")
	if err != nil {
		return nil, err
	}
	ts := &ThemeSet{Themes: map[string]*Theme{}, CSS: map[string][]byte{}, Faces: map[string][]FontFace{}, fsys: fsys}
	if raw, err := fs.ReadFile(fsys, "themes/fonts/faces.json"); err == nil {
		if err := json.Unmarshal(raw, &ts.Faces); err != nil {
			return nil, fmt.Errorf("themes/fonts/faces.json: %w", err)
		}
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "fonts" {
			continue
		}
		dir := path.Join("themes", e.Name())
		raw, err := fs.ReadFile(fsys, path.Join(dir, "theme.json"))
		if err != nil {
			return nil, fmt.Errorf("theme %s: %w", e.Name(), err)
		}
		var t Theme
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("theme %s: %w", e.Name(), err)
		}
		if t.Name != e.Name() {
			return nil, fmt.Errorf("theme folder %q declares name %q", e.Name(), t.Name)
		}
		for _, tok := range ColorTokens {
			if _, ok := t.Tokens.Color[tok]; !ok {
				return nil, fmt.Errorf("theme %s: missing colour token %q", t.Name, tok)
			}
		}
		for _, key := range t.Tokens.Fonts {
			if len(ts.Faces[key]) == 0 {
				return nil, fmt.Errorf("theme %s: font %q is not in themes/fonts (run npm run fonts)", t.Name, key)
			}
		}
		ts.Themes[t.Name] = &t
		if css, err := fs.ReadFile(fsys, path.Join(dir, "theme.css")); err == nil {
			ts.CSS[t.Name] = css
		}
	}
	if len(ts.Themes) == 0 {
		return nil, fmt.Errorf("no themes found")
	}
	return ts, nil
}

// ColorTokens are the colour token names every theme must define.
var ColorTokens = []string{"bg", "surface", "text", "muted", "accent", "accent2"}
