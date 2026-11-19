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
Style overrides use tokens only: `"style":{"color":"accent","size":"xl","align":"center"}`.

## Checklist before you finish
- `lint_deck` shows no errors or warnings.
- Every image has `alt` (or `decorative: true`).
- The deck tells a story: problem → insight → proposal → ask.
