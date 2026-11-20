# Slidekick

Self-hosted slideshow app where **agents author animated decks through MCP** and humans watch them build live in the browser. One Go binary with the web app embedded, shipped as a Docker image. MIT licensed.

> Status: **MVP** (phase 1). Agents can build complete, animated decks with every v1 element type, check their work with screenshots, and export them. The editor and presenter view come next (see `Slidekick Build Plan.md`).

## What works today

- **Structured deck schema** (`schema/deck.schema.json`). Agents compose 12 element types inside 8 layouts and style them with theme tokens only. They never write HTML. The element types are heading, text, bullets, image, code with syntax highlighting, stat, quote, chart (bar, line or pie), Mermaid diagram, icon (99 from Tabler), shape and embed.
- **MCP server** (official Go SDK), over streamable HTTP at `/mcp` or over stdio. It has 15 tools: `create_deck`, `get_deck`, `list_decks`, `add_slide`, `update_slide`, `delete_slide`, `move_slide`, `patch_deck` (RFC 6902), `set_theme`, `list_themes`, `lint_deck`, `render_slide`, `upload_asset`, `export_deck` and `get_share_url`. It also serves 6 resources (the schema, the layout, element and icon, animation and theme catalogues, and a design guide) and 3 prompts.
- **`render_slide`** screenshots any slide at any build step in headless Chromium, using the real viewer page, so the agent sees exactly what the audience sees. It needs the `render` image target; the slim image reports that rendering is unavailable.
- **`export_deck`** produces one self-contained HTML file with the runtime, theme and uploaded images inlined. It plays offline with every animation. Decks without diagrams use a lighter bundle (about 1.9 MB instead of 5.3 MB).
- **`upload_asset`** stores images and videos content-addressed on the data volume. They are served from `/files/...` with a sandboxing CSP.
- **Agent feedback loop.** Every write returns the new version and its lint findings: overflow, density, too many or too-long bullets, missing alt text or captions, contrast, chart data that doesn't line up with its labels, crowded pies, dense diagrams, untitled iframes, build gaps, and morphs with nothing to match.
- **Optimistic concurrency.** A write that sends a stale `base_version` is rejected with a readable error. Writes are atomic: a patch either validates in full or nothing changes.
- **Live viewer** at `/d/<id>`. Edits stream over WebSocket as JSON Patch. The viewer follows the slide being edited, highlights what changed and keeps a lint panel.
- **Motion.** Build steps with presets (fade, fade-up, scale-in, wipe, typewriter), bullets that build one item at a time, stats that count up, charts that draw in, and slide transitions (fade, slide, push, zoom, **morph** via the View Transitions API with a crossfade fallback). It respects `prefers-reduced-motion`.
- **Five themes**: Aurora (dark, drifting glow), Paper (light, editorial), Swiss (white, red signal, hard grid), Ember (warm dark, springy motion) and Meadow (soft sage, rounded, calm). Code colours and diagrams follow each theme's tokens.
- **SQLite storage** (pure Go, no CGO) with the full version history.

## Run it (Docker)

```bash
docker build --target render -t slidekick:local-render .   # with Chromium for render_slide
docker build -t slidekick:local .                           # slim distroless image
docker run -d --name slidekick -p 7410:7410 \
  -e SLIDEKICK_TOKEN=change-me -e SLIDEKICK_BASE_URL=http://<host>:7410 \
  -v slidekick-data:/data slidekick:local
```

The render image needs a writable `/tmp` for Chromium. When you run it with a read-only root filesystem, add `--tmpfs /tmp --shm-size 256m`.

A Compose file is in `deploy/compose.yaml`.

| Variable | Default | Purpose |
| --- | --- | --- |
| `SLIDEKICK_ADDR` | `:7410` | Listen address |
| `SLIDEKICK_DATA` | `/data` in the image | SQLite location |
| `SLIDEKICK_BASE_URL` | `http://localhost:<port>` | Public URL used in share links |
| `SLIDEKICK_TOKEN` | (empty) | Bearer token for `/mcp` and the write API. **Set it** for anything but localhost. |

Viewing decks is open to anyone who can reach the port; writing needs the token.

## Connect an agent

```bash
claude mcp add --transport http slidekick http://<host>:7410/mcp \
  --header "Authorization: Bearer $SLIDEKICK_TOKEN"
```

For local stdio use, run `slidekick stdio`. It also serves the viewer on `127.0.0.1:7410`.

Then ask your agent for a deck, and open the link it returns.

## Develop

```bash
cd web && npm install && npm run build   # builds web/dist (embedded by go:embed)
go test ./...                             # schema, service, lint, MCP end-to-end
go run ./cmd/slidekick serve              # http://localhost:7410
cd web && npm run dev                     # Vite dev server proxying to :7410
```

Layout: `cmd/slidekick` (entry point), `internal/deck` (model, catalogue, validation, ids, migrations), `internal/service` (the single write path), `internal/store` (SQLite), `internal/lint`, `internal/mcpserver`, `internal/server` (HTTP, WebSocket), `internal/hub` (fan-out), `schema/`, `themes/`, `web/runtime` (renderer, presets, player, patch), `web/src` (app pages) and `examples/` (golden decks used in tests).
