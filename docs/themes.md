# Writing a theme

A theme is a folder in `themes/` with two files. Slides only ever reference theme tokens, so a new theme restyles every deck without touching content.

```
themes/
  harbor/
    theme.json   tokens: colours, fonts, sizes, radius, motion
    theme.css    optional flourishes scoped to .sk-theme-harbor
```

## theme.json

```json
{
  "name": "harbor",
  "label": "Harbor",
  "description": "One sentence agents read when picking a theme.",
  "dark": true,
  "tokens": {
    "color": { "bg": "#0f1b24", "surface": "#18293a", "text": "#f1f5f9", "muted": "#94a3b8", "accent": "#38bdf8", "accent2": "#fbbf24" },
    "font": { "heading": "\"Space Grotesk\", sans-serif", "body": "\"Inter\", sans-serif", "mono": "\"JetBrains Mono\", monospace" },
    "fonts": ["space-grotesk", "inter", "jetbrains-mono"],
    "size": { "xs": 22, "sm": 28, "md": 36, "lg": 52, "xl": 76, "xxl": 120 },
    "radius": 12,
    "motion": { "fast": 220, "base": 460, "slow": 800, "easing": "cubic-bezier(0.22, 1, 0.36, 1)" }
  }
}
```

- All six colour tokens are required. Keep `text` and `muted` at 4.5:1 contrast or better against `bg` and `surface`; lint checks overrides against them.
- `size` values are pixels on the 1920×1080 stage.
- `motion` sets the speed and feel of every build and transition. Calm themes use longer durations and ease-in-out; punchy themes use shorter durations and an overshooting curve.
- `motion.bounce` (optional, 0 to 1) turns build entrances into physical springs ([Motion](https://motion.dev)): Ember uses 0.35, Meadow 0.15. Leave it out for a plain easing curve. Transitions keep `easing`.
- `fonts` lists self-hosted families by key. The available keys are in `themes/fonts/faces.json`. To add a family, add it to `web/scripts/gen-fonts.mjs` (any Fontsource package under the SIL Open Font License), run `npm --prefix web run fonts`, and commit `themes/fonts`.

## theme.css

Scope every rule to `.sk-theme-<name>`. Useful hooks:

| Selector | What |
| --- | --- |
| `.sk-slide`, `.sk-slide::before`, `::after` | Slide background and ambient decoration (respect `prefers-reduced-motion`) |
| `.sk-layout-<layout>` | Per-layout tweaks, e.g. left-align the `title` layout |
| `.sk-el-heading h1/h2/h3`, `.sk-el-text`, `.sk-el-quote blockquote`, `.sk-stat-value` | Typography |
| `.sk-card` | The three cards of `grid-3` |
| `.sk-el-code pre` | Code blocks (colours come from tokens) |

Look at `themes/swiss` (hard grid, no decoration) and `themes/aurora` (animated glow) for two ends of the range.

## Check it

1. `go test ./...`: themes are loaded and validated, and the golden decks must lint clean.
2. Run the server and switch a golden deck (`examples/*.json`) to your theme with `set_theme`.
3. `render_slide` every slide, or export a PDF, and look at all eight layouts.
