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
- `fonts` lists self-hosted families by key. The available keys are in `themes/fonts/faces.json`. Add `playfair-display-italic` alongside `playfair-display` for real italics. To add a family, add it to `web/scripts/gen-fonts.mjs` (any Fontsource package under the SIL Open Font License), run `npm --prefix web run fonts`, and commit `themes/fonts`.

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

## Bundled themes

Pick one with `set_theme` (or `theme` in `create_deck`). Every theme passes the same contrast checks (see [Check it](#check-it)).

| Theme | Mode | Fonts | Character |
| --- | --- | --- | --- |
| `aurora` (Aurora) | dark | Space Grotesk / Inter | Dark, cinematic. Deep navy with a slowly drifting teal-violet glow. Good for keynotes and product launches. |
| `paper` (Paper) | light | Fraunces / Source Sans 3 | Light, editorial. Warm off-white, serif headlines, a single vermilion accent. Good for strategy, research and reports. |
| `swiss` (Swiss) | light | Inter Tight | International style. Pure white, black type, one signal red, hard edges and left-aligned grids. Precise, fast motion. Good for product, design and engineering reviews. |
| `ember` (Ember) | dark | Archivo / Inter | Warm and bold. Charcoal with glowing orange and amber, chunky display type and springy motion. Good for launches, pitches and town halls. |
| `meadow` (Meadow) | light | DM Serif Display / Nunito Sans | Soft and friendly. Pale sage, deep green and terracotta, a warm serif with rounded cards and calm, slow motion. Good for education, health and community updates. |
| `boardroom` (Boardroom) | light | Inter Tight / Source Sans 3 | Light and corporate. Cool white, deep navy type, a confident blue and a touch of gold, with a navy header rule. Good for board updates, quarterly reviews and client proposals. |
| `noir` (Noir) | dark | Playfair Display / Inter | Midnight luxury. Near-black with ivory type, a gold hairline frame and high-contrast Playfair serif headlines. Slow, cinematic motion. Good for brand stories, premium launches and award decks. |
| `synthwave` (Synthwave) | dark | Unbounded / Space Grotesk | Neon retro. Deep violet night, hot magenta and cyan glow, a wide display face and a glowing grid horizon. Punchy, springy motion. Good for gaming, music, hackathons and bold product reveals. |
| `blueprint` (Blueprint) | dark | Oswald / Source Sans 3 | Technical drawing. Blueprint blue with a fine white grid, condensed uppercase headings, dashed outlines and pencil-yellow highlights. Precise motion. Good for architecture, engineering and system design. |
| `terminal` (Terminal) | dark | JetBrains Mono | Hacker console. Black screen, phosphor-green monospace everywhere, scanlines, prompt markers and a blinking cursor. Quick, clipped motion. Good for developer talks, security briefings and CLI demos. |
| `chalkboard` (Chalkboard) | dark | Caveat / Nunito Sans | Classroom slate. Green-black board, chalk-white text, handwritten Caveat headings, chalk-dust smudges and dashed chalk boxes. Calm motion. Good for teaching, workshops and explainers. |
| `sorbet` (Sorbet) | light | Fredoka / Nunito Sans | Pastel and playful. Cream with raspberry, teal and lavender, rounded Fredoka headings, floating confetti and bubbly cards. Bouncy motion. Good for kick-offs, celebrations, kids and community events. |
| `sunset` (Sunset) | dark | DM Serif Display / Inter | Warm dusk gradient. Plum sky melting into a coral and gold horizon glow, elegant DM Serif headlines and soft, unhurried motion. Good for vision talks, retrospectives and storytelling. |
| `ocean` (Ocean) | light | Space Grotesk / Nunito Sans | Fresh and coastal. Pale sea-glass background, deep-sea navy type, teal and coral accents and slow rolling waves along the bottom edge. Good for sustainability, travel, health and calm explainers. |
| `forest` (Forest) | dark | Fraunces / Source Sans 3 | Deep woodland. Pine-dark green, moss and amber accents, a soft Fraunces serif and layered tree silhouettes in the mist. Slow, grounded motion. Good for nature, sustainability and reflective keynotes. |
| `magazine` (Magazine) | light | Playfair Display / Source Sans 3 | Editorial magazine. Crisp white page, black Playfair display headlines, italic pull quotes, double masthead rules and a crimson accent. Good for thought leadership, research and long-form storytelling. |
| `brutalist` (Brutalist) | light | Oswald / JetBrains Mono | Raw and loud. Concrete off-white, heavy black borders, hard offset shadows, condensed uppercase Oswald headlines, monospace body and electric blue. Abrupt motion. Good for manifestos, design critiques and provocative pitches. |
| `contrast` (High Contrast) | dark | Atkinson Hyperlegible Next | Accessibility first. Pure black with white text, yellow and cyan accents at AAA contrast, Atkinson Hyperlegible type at larger sizes, clear outlines and no ambient motion. Good for accessible presentations, large rooms and low-vision audiences. |

## Builds and transitions

Builds reveal an element on a step (`build.preset`); transitions bring a slide in (`transition.type`). Both take their speed and curve from the theme's `motion` tokens, and both respect `prefers-reduced-motion`: builds appear instantly and transitions become an instant crossfade. Going back through a deck plays a slide's transition mirrored (a push from the right becomes a push from the left).

### Build presets

| Preset | Effect |
| --- | --- |
| `fade` | Fades in. |
| `fade-up` | Fades in while rising slightly. The default. |
| `scale-in` | Grows in from slightly smaller. |
| `wipe` | Revealed by a left-to-right wipe. |
| `typewriter` | Text types in character by character (headings, text and quotes). |
| `draw` | Line art draws itself stroke by stroke (icons, diagrams and other SVG); anything else wipes in. |
| `fade-down` | Fades in while dropping slightly from above. |
| `fade-left` | Fades in while gliding left (enters from the right). |
| `fade-right` | Fades in while gliding right (enters from the left). |
| `slide-up` | Travels up from well below the slot. Bolder than fade-up. |
| `slide-down` | Travels down from well above the slot. |
| `slide-left` | Travels in leftwards from the right. |
| `slide-right` | Travels in rightwards from the left. |
| `zoom-in` | Zooms up from small. Bolder than scale-in. |
| `zoom-out` | Shrinks into place from oversized, as if landing on the slide. |
| `pop` | Pops in with a quick overshoot. Good for icons, stats and badges. |
| `drop` | Drops in from above and settles with a small bounce. |
| `bounce-in` | Springs in from small with a few bounces. Playful. |
| `flip-x` | Flips down into place around its horizontal axis (3D). |
| `flip-y` | Flips into place around its vertical axis, like turning a card (3D). |
| `rotate-in` | Spins in a little while growing to size. |
| `swing` | Swings down from a hinge on its top edge and settles (3D). |
| `blur-in` | Sharpens into focus from a blur. |
| `reveal` | Rises out of an invisible mask at its bottom edge. Elegant for headings and short text. |
| `wipe-down` | Revealed by a top-to-bottom wipe. |
| `iris` | Revealed from the centre by a growing circle. Good for images and icons. |
| `stretch` | Stretches open horizontally from its centre. |
| `highlight` | Fades in, then an accent marker sweeps across it and away. Draws the eye to one key line. |

### Transitions

| Type | Effect |
| --- | --- |
| `none` | Instant cut. |
| `fade` | Crossfade. |
| `slide` | New slide slides in from the right. |
| `zoom` | New slide scales up from slightly smaller. |
| `push` | New slide pushes the old one off to the left. |
| `zoom-out` | New slide settles down from slightly larger while the old one shrinks away, like stepping back for the big picture. |
| `fade-through` | Old slide fades out completely, then the new one fades in. A calm pause between sections. |
| `cover` | New slide slides in over the old one, which stays put and dims. |
| `uncover` | Old slide slides away to reveal the new one waiting underneath. |
| `push-up` | New slide comes up from below and pushes the old one off the top (vertical push). |
| `wipe` | New slide is revealed by a straight wipe from left to right. |
| `split` | New slide opens out from a vertical line in the centre, like curtains parting. |
| `iris` | New slide opens out from a growing circle in the centre. |
| `blur` | Old slide blurs out of focus as the new one sharpens into view. |
| `flip` | The slide flips over like a card to show the new one on its back (3D). |
| `cube` | Slides turn like the faces of a rotating cube (3D). |
| `swap` | Old and new slides trade places, one sliding behind the other. |
| `morph` | Elements that share an id with the previous slide glide to their new position and size; the rest crossfade. |

Adding one: put the name in the enum in `schema/deck.schema.json`, describe it in `schema/catalog.json` (agents and the editor read it there) and write it as a pure function in `web/runtime/presets.ts` with a test in `presets.test.ts`. A preset must end with the element in its natural state, so a static render or PDF looks the same as the end of the animation.

## Check it

1. `go test ./...`: themes are loaded and validated, the golden decks must lint clean in every theme, and every theme's `text`, `muted` and `accent` must reach 4.5:1 (`accent2` 3:1) on both `bg` and `surface`.
2. Run the server and switch a golden deck (`examples/*.json`) to your theme with `set_theme`.
3. `render_slide` every slide, or export a PDF, and look at all eight layouts.
