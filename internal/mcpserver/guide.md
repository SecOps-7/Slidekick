# Slidekick design guide (for agents)

A slide supports what the speaker says. It is not a document.

## Structure
- **One idea per slide.** If you need "and" in the headline, make two slides.
- **Headlines are claims**, not topics: "Churn fell 40% after onboarding v2", not "Churn".
- Open with a `title` slide. Use `title` again as a section divider in long decks.
- Vary layouts: `title-body` for most slides, `two-column` for comparisons, `big-number` for one figure, `grid-3` for three pillars or three stats, `quote` for a voice, `hero-image` for mood.
- Put detail, sources and the full argument in `notes`.

## Words
- At most 5 bullets, each under about 12 words. Fewer is better.
- No full stops at the end of bullets. Parallel phrasing.
- Use `**bold**` for the one phrase that matters per slide, not more.

## Motion
- Motion should show structure: reveal points in the order you will talk about them (`build.step` 1, 2, 3).
- Use `fade-up` for most builds; `scale-in` for a stat or a payoff; `wipe` for a line or image; `typewriter` sparingly for a single short heading.
- `morph` is the signature move: keep an element's `id` the same on two consecutive slides (for example a heading or a stat) and it glides into its new place and size. Use it to zoom from an overview into a detail.
- One transition style per section. `fade` is a safe default; `push` suits a sequence or timeline.
- Stats count up automatically. Make the number the hero.

## Example element shapes
```json
{"type":"heading","props":{"text":"Revenue doubled","level":1}}
{"type":"text","props":{"text":"Driven by **self-serve** plans."}}
{"type":"bullets","props":{"items":["Faster setup","Lower churn","Higher NPS"],"buildEach":true},"build":{"step":1}}
{"type":"stat","props":{"value":42,"suffix":"%","label":"less churn"},"build":{"step":1,"preset":"scale-in"}}
{"type":"image","props":{"src":"https://...","alt":"Team at the launch event"}}
{"type":"quote","props":{"text":"It just works.","attribution":"Beta customer"}}
{"type":"code","props":{"code":"go run ./cmd/slidekick","language":"bash"}}
```
```json
{"type":"chart","props":{"kind":"bar","labels":["Q1","Q2","Q3"],"series":[{"name":"Revenue","values":[1.2,1.8,2.6]}],"unit":"M","showValues":true,"caption":"Revenue doubled"}}
{"type":"diagram","props":{"source":"flowchart LR\n  A[Idea] --> B[Build] --> C[Ship]","caption":"Our delivery loop"}}
{"type":"icon","props":{"name":"rocket"},"style":{"size":"xl"}}
{"type":"shape","props":{"kind":"pill","fill":"accent","label":"New"}}
{"type":"embed","props":{"kind":"iframe","src":"https://www.youtube-nocookie.com/embed/VIDEO_ID","title":"Product demo"}}
{"type":"html","props":{"title":"Pricing badge","html":"<div style=\"font-size:var(--sk-size-xl);color:var(--sk-accent)\">From $9</div>"}}
```
The `html` element is an escape hatch, not a shortcut: it runs in a sandbox with no scripts, skips most lint checks and always gets a warning. Use it only for something no other element can express, keep it small, and style it with the theme's CSS variables (`var(--sk-accent)`, `var(--sk-font-body)`, `var(--sk-size-lg)`).

Pick a theme by mood: boardroom (corporate), magazine or paper (editorial), noir (premium), synthwave or ember (bold launches), blueprint or terminal (technical), chalkboard (teaching), sorbet or meadow (friendly), ocean, forest or sunset (calm storytelling), aurora or swiss (modern product), brutalist (provocative), contrast (accessibility, large rooms). The animation catalog lists every build and transition: prefer fade-up or reveal for text, pop, iris or draw for icons and stats, and highlight for one key line at most. Keep 3D transitions (flip, cube) for section changes, and one transition style per section.

Work where the user can see it: they usually watch the editor or viewer while you work. Edits move their view to the changed slide; call `focus_slide` before you start on a slide, and whenever the user names one ("slide 4"), so it's on screen before you change it.

Images and videos: call `upload_asset` and use the `/files/...` url it returns.

Style overrides use tokens only: `"style":{"color":"accent","size":"xl","align":"center"}`.

## Data and diagrams
- One chart per slide, and put the takeaway in the headline ("Churn halved"), not "Churn by month".
- Bar for comparing categories, line for change over time, pie only for parts of a whole with 2 to 5 slices.
- Always set `caption`: it is the accessible description.
- Diagrams: keep to about 12 nodes; prefer `flowchart LR` for processes and `sequenceDiagram` for interactions.
- Icons work best as a set: one per card in a `grid-3`, same size, building in with the card text.

## Check your work
- `render_slide` returns a screenshot of a slide exactly as the audience sees it. Look at it: is anything cramped, clipped or empty?
- `export_deck` gives a single HTML file that plays offline.

## Checklist before you finish
- `lint_deck` shows no errors or warnings.
- Every image has `alt` (or `decorative: true`).
- The deck tells a story: problem → insight → proposal → ask.
