// Package lint checks decks for layout and accessibility problems and
// returns fix hints written for agents.
package lint

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/SecOps-7/slidekick/internal/deck"
)

// Issue is one lint finding.
type Issue struct {
	Severity  string `json:"severity" jsonschema:"error, warning or info"`
	Code      string `json:"code"`
	Slide     int    `json:"slide" jsonschema:"zero-based slide index"`
	SlideID   string `json:"slideId"`
	ElementID string `json:"elementId,omitempty"`
	Message   string `json:"message"`
	Fix       string `json:"fix,omitempty"`
}

const (
	maxBullets      = 6
	maxBulletChars  = 110
	maxSlideWords   = 90
	maxCodeLines    = 16
	maxDiagramLines = 25
	maxPieSlices    = 6
	maxBarLabels    = 12
	minContrast     = 4.5
	minContrastLarg = 3.0
)

var sizeFactor = map[string]float64{"xs": 0.6, "sm": 0.8, "md": 1, "lg": 1.4, "xl": 2, "xxl": 3}

// Deck lints a whole deck.
func Deck(d *deck.Deck, cat *deck.Catalog, themes *deck.ThemeSet) []Issue {
	issues := []Issue{}
	theme := themes.Themes[d.Theme]
	if len(d.Slides) == 0 {
		issues = append(issues, Issue{Severity: "info", Code: "empty-deck", Slide: -1,
			Message: "The deck has no slides yet.", Fix: "Add slides with add_slide."})
	}
	for i := range d.Slides {
		var prev *deck.Slide
		if i > 0 {
			prev = &d.Slides[i-1]
		}
		issues = append(issues, slide(i, &d.Slides[i], prev, cat, theme)...)
	}
	return issues
}

func slide(i int, s, prev *deck.Slide, cat *deck.Catalog, theme *deck.Theme) []Issue {
	var out []Issue
	add := func(sev, code, el, msg, fix string) {
		out = append(out, Issue{Severity: sev, Code: code, Slide: i, SlideID: s.ID, ElementID: el, Message: msg, Fix: fix})
	}
	els := s.Elements(cat)
	if len(els) == 0 {
		add("warning", "empty-slide", "", "Slide is empty.", "Add content or delete the slide.")
		return out
	}
	layout := cat.Layouts[s.Layout]

	// Title present where the layout has one.
	if _, hasTitle := layout.Regions["title"]; hasTitle && len(s.Regions["title"]) == 0 && s.Layout != "hero-image" {
		add("info", "missing-title", "", fmt.Sprintf("Layout %q has a title region but it is empty.", s.Layout),
			"Add a short heading to the title region so the slide reads at a glance.")
	}

	// Overflow per region against the layout's character budget.
	for _, r := range cat.RegionOrder(s.Layout, s.Regions) {
		budget, ok := layout.Regions[r]
		if !ok {
			continue
		}
		var load float64
		for _, e := range s.Regions[r] {
			load += weight(&e)
		}
		if budget == 0 && load > 0 {
			add("warning", "media-region", "", fmt.Sprintf("Region %q is meant for an image but holds text.", r),
				"Put an image element here and move text to another region.")
		} else if budget > 0 && load > float64(budget) {
			add("warning", "overflow", "", fmt.Sprintf("Region %q likely overflows: about %.0f characters for a budget of %d.", r, load, budget),
				"Shorten the text, use a smaller size, or split the content across two slides.")
		}
	}

	words := 0
	steps := map[int]bool{}
	for _, e := range els {
		e := e
		words += wordCount(&e)
		if e.Build != nil {
			steps[e.Build.Step] = true
		}
		switch e.Type {
		case "bullets":
			items := e.Items()
			if len(items) > maxBullets {
				add("warning", "too-many-bullets", e.ID, fmt.Sprintf("%d bullets; more than %d is hard to read on a slide.", len(items), maxBullets),
					"Keep the strongest 3 to 5 points, or split into two slides.")
			}
			for k, it := range items {
				if n := len([]rune(plain(it))); n > maxBulletChars {
					add("warning", "long-bullet", e.ID, fmt.Sprintf("Bullet %d is %d characters long.", k+1, n),
						"Cut bullets to a short phrase (under ~12 words); move detail to speaker notes.")
				}
			}
		case "image":
			alt := strings.TrimSpace(e.Str("alt"))
			dec, _ := e.Props["decorative"].(bool)
			if alt == "" && !dec {
				add("error", "missing-alt", e.ID, "Image has no alt text.",
					"Set props.alt to a short description, or props.decorative: true if it carries no information.")
			}
		case "chart":
			out = append(out, chart(i, s, &e)...)
		case "diagram":
			if n := strings.Count(strings.TrimSpace(e.Str("source")), "\n") + 1; n > maxDiagramLines {
				add("warning", "dense-diagram", e.ID, fmt.Sprintf("Diagram source has %d lines; it will be too small to read.", n),
					"Keep diagrams to about 12 nodes; split detail across slides.")
			}
			if e.Str("caption") == "" {
				add("info", "diagram-caption", e.ID, "Diagram has no caption, so screen readers get no description.",
					"Set props.caption to one sentence describing what it shows.")
			}
		case "embed":
			if e.Str("kind") == "iframe" && strings.TrimSpace(e.Str("title")) == "" {
				add("error", "embed-title", e.ID, "Iframe has no title.", "Set props.title to describe the embedded content.")
			}
			if auto, _ := e.Props["autoplay"].(bool); auto {
				if muted, ok := e.Props["muted"].(bool); ok && !muted {
					add("warning", "autoplay-sound", e.ID, "Video autoplays with sound; browsers block that.", "Set muted: true or drop autoplay.")
				}
			}
		case "code":
			if n := strings.Count(e.Str("code"), "\n") + 1; n > maxCodeLines {
				add("warning", "long-code", e.ID, fmt.Sprintf("Code block has %d lines; more than %d won't be legible.", n, maxCodeLines),
					"Show only the lines that matter.")
			}
		}
		out = append(out, contrast(i, s, &e, theme)...)
	}
	if words > maxSlideWords {
		add("warning", "dense-slide", "", fmt.Sprintf("Slide has about %d words; aim for under %d.", words, maxSlideWords),
			"Move supporting detail into speaker notes or split the slide.")
	}
	if len(steps) > 0 {
		for k := 1; k <= len(steps); k++ {
			if !steps[k] {
				add("info", "build-gap", "", "Build steps are not consecutive from 1, so some clicks reveal nothing.",
					"Renumber build steps 1, 2, 3, ...")
				break
			}
		}
	}
	if s.Transition != nil && s.Transition.Type == "morph" {
		if prev == nil {
			add("info", "morph-first", "", "Morph on the first slide has nothing to morph from.", "Use fade, or move the slide.")
		} else {
			shared := 0
			ids := map[string]bool{}
			for _, e := range prev.Elements(cat) {
				ids[e.ID] = true
			}
			for _, e := range els {
				if ids[e.ID] {
					shared++
				}
			}
			if shared == 0 {
				add("warning", "morph-no-match", "", "Morph transition, but no element shares an id with the previous slide, so it will only crossfade.",
					"Reuse the ids of elements that should glide between the two slides.")
			}
		}
	}
	if s.Background != nil && s.Background.Image != "" && (s.Background.Dim == nil || *s.Background.Dim < 0.3) && words > 0 {
		add("info", "bg-image-legibility", "", "Text over a background image without much dimming may be hard to read.",
			"Set background.dim to 0.4 or more.")
	}
	return out
}

// chart checks data shape and readability.
func chart(i int, s *deck.Slide, e *deck.Element) []Issue {
	var out []Issue
	add := func(sev, code, msg, fix string) {
		out = append(out, Issue{Severity: sev, Code: code, Slide: i, SlideID: s.ID, ElementID: e.ID, Message: msg, Fix: fix})
	}
	labels, _ := e.Props["labels"].([]any)
	series, _ := e.Props["series"].([]any)
	kind := e.Str("kind")
	for k, raw := range series {
		sm, _ := raw.(map[string]any)
		vals, _ := sm["values"].([]any)
		if len(vals) != len(labels) {
			add("error", "chart-mismatch", fmt.Sprintf("Series %d has %d values but there are %d labels.", k+1, len(vals), len(labels)),
				"Give every series exactly one value per label.")
		}
		if kind == "pie" {
			for _, v := range vals {
				if f, _ := v.(float64); f < 0 {
					add("error", "pie-negative", "Pie charts can't show negative values.", "Use a bar chart.")
					break
				}
			}
		}
	}
	if kind == "pie" && len(series) > 1 {
		add("info", "pie-series", "Pie charts only draw the first series.", "Remove the extra series or use a bar chart.")
	}
	if kind == "pie" && len(labels) > maxPieSlices {
		add("warning", "pie-slices", fmt.Sprintf("%d pie slices; more than %d are hard to compare.", len(labels), maxPieSlices),
			"Group small slices into 'Other', or use a bar chart.")
	}
	if kind != "pie" && len(labels) > maxBarLabels {
		add("warning", "chart-crowded", fmt.Sprintf("%d categories; labels will collide.", len(labels)), "Show the top items, or use a line chart.")
	}
	if show, _ := e.Props["showValues"].(bool); show && kind != "pie" && len(series) >= 3 {
		add("warning", "chart-labels-overlap", fmt.Sprintf("Value labels on %d series will overlap.", len(series)),
			"Turn off showValues, or keep value labels to two series.")
	}
	if e.Str("caption") == "" {
		add("info", "chart-caption", "Chart has no caption, so screen readers get no description.",
			"Set props.caption to the takeaway, e.g. 'Revenue doubled since 2024'.")
	}
	return out
}

var noContrastCheck = map[string]bool{"image": true, "chart": true, "diagram": true, "embed": true, "shape": true}

func contrast(i int, s *deck.Slide, e *deck.Element, theme *deck.Theme) []Issue {
	if theme == nil || noContrastCheck[e.Type] {
		return nil
	}
	bgTok := "bg"
	if s.Background != nil && s.Background.Token != "" {
		bgTok = s.Background.Token
	}
	if s.Background != nil && s.Background.Image != "" {
		return nil // can't judge contrast against an image
	}
	var out []Issue
	fg := ""
	label := ""
	if e.Style != nil && e.Style.ColorRaw != "" {
		fg, label = e.Style.ColorRaw, e.Style.ColorRaw
		out = append(out, Issue{Severity: "warning", Code: "raw-color", Slide: i, SlideID: s.ID, ElementID: e.ID,
			Message: "Element uses a raw hex colour (escape hatch).", Fix: "Prefer style.color with a theme token so restyling works."})
	} else if e.Style != nil && e.Style.Color != "" {
		fg, label = theme.Tokens.Color[e.Style.Color], "token "+e.Style.Color
	} else if bgTok != "bg" && bgTok != "surface" {
		fg, label = theme.Tokens.Color["text"], "token text"
	}
	if fg == "" {
		return out
	}
	ratio, ok := Contrast(fg, theme.Tokens.Color[bgTok])
	if !ok {
		return out
	}
	need := minContrast
	if e.Type == "heading" || e.Type == "stat" || e.Type == "icon" || (e.Style != nil && (e.Style.Size == "lg" || e.Style.Size == "xl" || e.Style.Size == "xxl")) {
		need = minContrastLarg
	}
	if ratio < need {
		out = append(out, Issue{Severity: "warning", Code: "low-contrast", Slide: i, SlideID: s.ID, ElementID: e.ID,
			Message: fmt.Sprintf("Contrast of %s on background %s is %.1f:1 (needs %.1f:1).", label, bgTok, ratio, need),
			Fix:     "Use the text colour token, or a different background token."})
	}
	return out
}

// Contrast returns the WCAG contrast ratio between two #rrggbb colours.
func Contrast(a, b string) (float64, bool) {
	la, ok1 := luminance(a)
	lb, ok2 := luminance(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05), true
}

func luminance(hex string) (float64, bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, false
	}
	var c [3]float64
	for k := 0; k < 3; k++ {
		v, err := strconv.ParseUint(hex[k*2:k*2+2], 16, 8)
		if err != nil {
			return 0, false
		}
		x := float64(v) / 255
		if x <= 0.03928 {
			c[k] = x / 12.92
		} else {
			c[k] = math.Pow((x+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2], true
}

var inlineMarks = regexp.MustCompile("\\*\\*|\\*|`|\\[([^\\]]*)\\]\\([^)]*\\)")

// plain strips inline markup, keeping link text.
func plain(s string) string {
	return inlineMarks.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "[") {
			return m[1:strings.Index(m, "]")]
		}
		return ""
	})
}

func text(e *deck.Element) string {
	switch e.Type {
	case "heading", "text", "quote":
		return plain(e.Str("text")) + " " + e.Str("attribution")
	case "bullets":
		return plain(strings.Join(e.Items(), " "))
	case "stat":
		return e.Str("label")
	case "image", "chart", "diagram":
		return e.Str("caption")
	case "shape":
		return plain(e.Str("label"))
	}
	return ""
}

func wordCount(e *deck.Element) int { return len(strings.Fields(text(e))) }

// weight estimates how much room an element takes, in body-text characters.
func weight(e *deck.Element) float64 {
	f := 1.0
	if e.Style != nil {
		if v, ok := sizeFactor[e.Style.Size]; ok {
			f = v
		}
	}
	switch e.Type {
	case "image", "chart", "diagram", "embed", "icon":
		return 0
	case "code":
		// Code height is driven by line count; ~24 body characters per line.
		return float64(strings.Count(e.Str("code"), "\n")+1) * 24 * f
	case "stat":
		return 20 * f
	case "bullets":
		n := 0.0
		for _, it := range e.Items() {
			n += float64(len([]rune(plain(it)))) + 25 // per-line overhead
		}
		return n * f
	}
	return float64(len([]rune(strings.TrimSpace(text(e))))) * f
}
