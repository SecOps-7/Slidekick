# Slidekick: Build Plan

Nov 19, 2026 · @Sir

## Vision and scope

Slidekick is a standalone, self-hosted, open-source (MIT) slideshow app where agents author animated HTML decks through MCP, and humans watch and edit the same deck live in the browser. It ships as one Go binary with the editor embedded, packaged as a Docker image.

**The gap.** Existing MCP slide tools (Slidev MCP, revealjs-mcp, Marp MCP, HTML Slide Studio) wrap Markdown frameworks and produce mostly static slides. AI deck generators with MCP (Presenton, Plus AI, Alai) output PPTX. Nobody combines a constrained component model, real motion design and a live shared editor.

**Differentiators**

- Structured JSON deck schema instead of Markdown or freeform HTML, so agents produce valid, good-looking output reliably.
- First-class animation: per-element build steps, morph transitions between slides, motion presets.
- Live editor: agent edits stream into the open browser in real time; humans can edit alongside.
- Agent feedback loop: render screenshots and layout lint results back to the agent so it can self-correct.
- Self-hosted as one Go binary and a Docker image, with no Node or Playwright install for the core. Docker is the primary way to run it.

**Non-goals for v1**

- Full PowerPoint fidelity or PPTX import.
- Multi-user real-time collaboration between humans (agent plus one human is enough).
- A hosted SaaS offering. Keep the door open, but build self-hosted first.

## Architecture overview

Everything runs in one Go process: agents talk MCP, browsers talk HTTP and WebSocket, and both go through the same deck service. The deck service is the only thing that writes state.

&#91;embedded content: system architecture · 2 clients, 5 server components\]

An agent write flows MCP server → deck service (validate, version, lint) → store, then is broadcast as a patch to every open browser. The renderer loads the real viewer page in headless Chromium, so screenshots match what people see.

## Slide schema

The deck is a versioned JSON document validated by JSON Schema, and the schema is the product's core contract. Agents never write raw HTML; they compose components inside layouts, and the theme decides how it looks.

**Model**

- **Deck:** id, title, theme ref, aspect ratio, schema version, slides\[\].
- **Slide:** id, layout, regions{}, transition, notes, background.
- **Layout:** named grid presets (title, title-body, two-column, hero-image, big-number, quote, grid-3, full-bleed). Each layout declares regions such as `title`, `left` and `right`.
- **Element:** id, type, props, style overrides limited to theme tokens, build step.
- **Theme:** design tokens (palette, type scale, spacing, radii, motion durations) plus CSS. Ship 4 to 6 polished themes.

**Element types for v1:** heading, text (rich inline), bullet list, image, code (with syntax highlighting), chart (bar, line, pie, driven by data), diagram (Mermaid source), stat or big number, quote, icon, shape, embed (iframe or video).

**Validation rules**

- Stable element ids so agents can patch precisely and animations can morph by id.
- Style values must reference theme tokens; free hex values only through an explicit escape hatch.
- A layout linter flags text overflow, too many bullets, low contrast and missing alt text. Results are returned to the agent.
- Schema version field plus migrations from day one.

Publish the JSON Schema itself as an MCP resource so any agent can read the contract before writing.

## Animation system

Animation is declarative data in the schema, played by a small TypeScript runtime. Agents pick from named presets; they never write keyframes.

**Three layers**

1. **Slide transitions:** fade, slide, zoom, push and **morph**. Morph uses the View Transitions API, so elements sharing an id across two slides animate smoothly between positions (Keynote Magic Move style). It degrades to a crossfade where the API is not supported.
2. **Builds:** each element gets an optional `build` step number and an entrance preset (fade-up, scale-in, wipe, typewriter, draw for SVG paths). Arrow keys step through builds before changing slides.
3. **Ambient motion:** subtle looping effects such as a slow background gradient drift, number count-up on stat elements and chart series drawing in.

**Engine choice:** use the Web Animations API directly, plus [Motion](https://motion.dev) (MIT) only for springs and stagger. Avoid GSAP so the whole stack stays MIT-friendly.

**Rules**

- Durations and easings come from theme motion tokens, so a theme can feel calm or punchy.
- Respect `prefers-reduced-motion`: builds become instant and morphs become fades.
- Each preset is a pure function `(element, options) => Animation[]`. That makes presets testable and lets the community add more.

## MCP server

The MCP server runs inside the same Go binary on the official Go SDK, over stdio for local clients and streamable HTTP at `/mcp` for remote ones. Its standout feature is the feedback loop: every write returns lint results, and the agent can ask for a screenshot of what it just made.

**Tools**

| Tool | Purpose |
| --- | --- |
| `create_deck` | New deck from title, theme and optional outline |
| `get_deck` / `list_decks` | Read the full deck JSON or list decks |
| `add_slide` / `update_slide` / `delete_slide` / `move_slide` | Slide-level edits |
| `patch_deck` | JSON Patch (RFC 6902) for precise, batched element edits |
| `set_theme` / `list_themes` | Switch or inspect themes and their tokens |
| `lint_deck` | Overflow, density, contrast and alt-text checks with fix hints |
| `render_slide` | PNG screenshot of one slide (or one build step) for agent self-review |
| `upload_asset` | Store an image or file and get back an asset URL |
| `export_deck` | Standalone HTML or PDF |
| `get_share_url` | Link to the live viewer or presenter |

**Resources:** the deck JSON Schema, theme catalogue, layout catalogue with region names, animation preset list and a design guide (good slide practice, written for agents).

**Prompts:** `outline_to_deck`, `polish_deck` (lint, screenshot, fix loop) and `restyle_deck`.

**Agent loop design**

- Every mutating tool returns the new deck version number plus lint warnings, so the agent fixes problems without an extra call.
- Writes use optimistic concurrency (`base_version`), so agent and human edits never silently clobber each other.
- `render_slide` uses headless Chromium through chromedp, as an optional dependency. If Chromium is missing, the tool reports it and everything else keeps working.
- Auth for HTTP mode: a bearer token per deck or workspace from day one, with OAuth later.

## Live editor and presenter

One browser app has three modes (Edit, Present and View) that share the same renderer, so what the agent sees in screenshots is exactly what the audience sees.

**Live sync**

- The server is the source of truth. Each change is a JSON Patch with a version number, broadcast over WebSocket to every open client.
- Agent edits animate in: changed elements briefly highlight and the editor follows the slide being edited, so you can watch the agent work.
- Conflicts: last write wins per element, with base-version checks on agent writes. Upgrade to CRDTs (Yjs or Automerge) only if human multi-user editing becomes a goal.

**Editor (v1)**

- Slide sorter sidebar with drag to reorder.
- Click to edit text inline; a property panel for layout, theme tokens and the build step.
- An animation timeline strip per slide showing build order.
- Undo and redo across both agent and human changes, backed by the version history.
- A "Send to agent" comment box: pin a note to an element, which the agent can read through an MCP resource.

**Presenter mode**

- Speaker view with notes, next slide, timer and build progress.
- Audience window synced over BroadcastChannel (same machine) or WebSocket (remote).
- Keyboard, clicker and touch navigation; fullscreen; laser pointer.

## Export and sharing

Standalone HTML is the main export: one file with the runtime, theme and assets inlined, playable offline with all animations.

| Format | How | When |
| --- | --- | --- |
| Standalone HTML | Go templates inline the runtime bundle, theme CSS and base64 assets | v1 |
| Live link | Read-only viewer URL served by the binary, optional token | v1 |
| PDF | Headless Chromium prints each slide at its final build state | v1.1 |
| PNG per slide | Same renderer as `render_slide` | v1.1 |
| Video (MP4) | Chromium screencast stepping through builds, encoded with ffmpeg | Later |
| PPTX | Lossy mapping of layouts and text; no animations | Later, if users ask |

## Tech stack and repo layout

Use a Go backend and a TypeScript frontend in one monorepo, with the frontend built by Vite and embedded in the binary via `go:embed`. Every dependency is MIT, BSD or Apache licensed.

| Layer | Choice |
| --- | --- |
| HTTP and WebSocket | Go stdlib `net/http` plus `coder/websocket` |
| MCP | Official Go SDK (`modelcontextprotocol/go-sdk`) |
| Storage | SQLite via `modernc.org/sqlite` (pure Go, no CGO); assets on disk or S3-compatible |
| Schema validation | `santhosh-tekuri/jsonschema` (Go) and Ajv (TS), both compiled from the same schema file |
| Screenshots and PDF | `chromedp`, optional at runtime |
| Runtime and editor | TypeScript, Vite, Preact or Solid (small and fast), Web Animations API, Motion |
| Code highlighting and diagrams | Shiki, Mermaid |
| Shared types | Generate TS types from the JSON Schema so Go, TS and agents share one contract |
| Packaging | Primary: a distroless Docker image, multi-arch (amd64/arm64) via buildx, plus a Compose file. Also GoReleaser for binaries; Helm chart later |

**Repo layout**

```
/cmd/server          main entry, flags, stdio vs http
/internal/deck       model, JSON Patch, versioning, migrations
/internal/mcp        tool, resource and prompt handlers
/internal/store      SQLite repo, assets
/internal/render     chromedp screenshots, PDF, HTML export
/internal/lint       layout and accessibility checks
/schema              deck.schema.json (single source of truth)
/web/runtime         renderer, layouts, animation presets
/web/editor          editor and presenter UI
/themes              token files and CSS per theme
/examples            sample decks used as golden tests
```

## Deployment (Docker)

Docker is required: Slidekick runs as a container. The binary also runs bare for local stdio MCP use, but the supported deployment is Docker Compose.

**Image**

- Multi-stage Dockerfile. Stage 1 is `node`: Vite builds `web/` into `web/dist`. Stage 2 is `golang`: a static `CGO_ENABLED=0` build that embeds `web/dist` via `go:embed`. The final stage is `gcr.io/distroless/static-debian12:nonroot`.
- Exposes port 7410. `/healthz` serves as the health check. SQLite data lives on a `/data` volume.
- A separate `-render` image tag adds headless Chromium for `render_slide` and PDF export, keeping the base image small.

**Configuration** is through environment variables:

| Variable | Purpose |
| --- | --- |
| `SLIDEKICK_ADDR` | Listen address |
| `SLIDEKICK_DATA` | Data directory (SQLite and assets) |
| `SLIDEKICK_BASE_URL` | Public URL used in share links |
| `SLIDEKICK_TOKEN` | Bearer token for `/mcp` and the write API. It is a secret. |

**Host layout on the operator's server**

| What | Where |
| --- | --- |
| Compose file | `/DATA/Stacks/slidekick/compose.yaml`, using absolute paths. A `.env` beside it may hold only non-secret values, because that folder is world-readable. Creating the folder needs `sudo install -d -o secops7 -g secops7 /DATA/Stacks/slidekick`, which the operator runs. |
| App data and secrets | `/DATA/AppData/Slidekick/` (folder mode 700). Holds `data/` (the SQLite volume) and a `.env` with `SLIDEKICK_TOKEN` at mode 600. |
| Code and build context | `/DATA/Backup/Projects/Slidekick`. This is an NFS share that ignores file permissions, so nothing the running app writes and no secrets go there. |

**Connecting agents:** over streamable HTTP at `http://<host>:7410/mcp` with `Authorization: Bearer <token>`. Local stdio mode is `docker run -i ... slidekick stdio`.

## Milestones

There are four phases, and each one ends at a gate you can demo. Phase 0 exists to prove the schema-plus-MCP idea before you invest in an editor. Every phase gate is demoed from the Docker Compose stack, not a bare binary.

&#91;embedded content: roadmap · 4 phases, 4 gates\]

Rough solo, part-time estimate: about 2 weeks for the spike, 4 to 6 for the MVP, 4 for the editor loop and 3 for launch, so roughly 4 months to v1.0. **After v1.0:** video export, PPTX, multi-user CRDT editing and hosted multi-tenant mode, prioritised by user demand.

## Risks and open questions

The biggest risk is design quality: if the default themes and layouts don't look great, the agent angle won't save it.

| Risk | Mitigation |
| --- | --- |
| Decks look generic or "AI-made" | Invest early in 4 to 6 opinionated themes; use golden-deck screenshots as the quality bar |
| Schema too rigid for creative slides | Add a sandboxed `html` element as an escape hatch, with lint warnings |
| View Transitions support varies by browser | Feature-detect and fall back to a FLIP-based morph or crossfade |
| Chromium dependency bloats the image | Keep it optional; publish a separate `-render` Docker tag |
| Agents produce overloaded slides | Density lint plus a design-guide resource; `polish_deck` loop |
| Container can't reach host or LAN agents / share links point at the container hostname | `SLIDEKICK_BASE_URL` sets the public URL used in share links; publish 7410 on the host |
| Crowded MCP slides space | Lead with live editing and motion in demos, not "MCP slides" |

**Open questions**

- [ ] Name decided: Slidekick. Still to secure: domain, GitHub org, Go module path.
- [ ] Preact or Solid for the editor UI.
- [ ] Decided: standalone MIT project, not a SaaS feature.
- [ ] Should the escape-hatch `html` element ship in v1 or wait for demand?
- [ ] Should it support hosted multi-tenant mode, or stay strictly self-hosted?
