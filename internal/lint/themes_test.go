package lint_test

import (
	"encoding/json"
	"testing"

	"github.com/SecOps-7/slidekick/internal/lint"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

// TestEveryThemeIsReadable lints the golden decks in every theme, so a theme
// whose colours make the examples hard to read fails the build.
func TestEveryThemeIsReadable(t *testing.T) {
	env := testenv.New(t)
	if len(env.Themes.Themes) < 2 {
		t.Fatalf("expected the bundled themes, got %v", env.Themes.Names())
	}
	for _, name := range []string{"launch.json", "showcase.json"} {
		var doc map[string]any
		if err := json.Unmarshal(testenv.Example(t, name), &doc); err != nil {
			t.Fatal(err)
		}
		for _, theme := range env.Themes.Names() {
			doc["theme"] = theme
			raw, _ := json.Marshal(doc)
			d, err := env.Validator.Validate(raw)
			if err != nil {
				t.Fatalf("%s in %s: %v", name, theme, err)
			}
			for _, i := range lint.Deck(d, env.Cat, env.Themes) {
				if i.Severity != "info" {
					t.Errorf("%s in %s: %+v", name, theme, i)
				}
			}
		}
	}
}

// TestThemeTokenContrast holds every theme's colour tokens to WCAG contrast:
// body text, muted text and the accent (links, bold, stats) at 4.5:1 on both
// backgrounds, and the secondary accent (chart series, large marks) at 3:1.
// The high-contrast theme must reach AAA (7:1) for all text tokens.
func TestThemeTokenContrast(t *testing.T) {
	env := testenv.New(t)
	need := map[string]float64{"text": 4.5, "muted": 4.5, "accent": 4.5, "accent2": 3}
	for _, name := range env.Themes.Names() {
		c := env.Themes.Themes[name].Tokens.Color
		for fg, min := range need {
			if name == "contrast" {
				min = 7
			}
			for _, bg := range []string{"bg", "surface"} {
				r, ok := lint.Contrast(c[fg], c[bg])
				if !ok {
					t.Errorf("%s: %s or %s is not a #rrggbb colour", name, fg, bg)
					continue
				}
				if r < min {
					t.Errorf("%s: %s on %s is %.2f:1, needs %.1f:1", name, fg, bg, r, min)
				}
			}
		}
	}
}
