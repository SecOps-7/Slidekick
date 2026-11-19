# Slidekick

Self-hosted slideshow app where **agents author animated decks through MCP** and humans watch them build live in the browser. One Go binary with the web app embedded, shipped as a Docker image. MIT licensed.

> Status: **Phase 0 spike**. It proves the core loop of schema, MCP, lint and live viewer. The editor, presenter view, export and `render_slide` screenshots come in later phases (see `Slidekick Build Plan.md`).

## What works today

- **Structured deck schema** (`schema/deck.schema.json`). Agents compose elements (heading, text, bullets, image, code, stat, quote) inside 8 layouts and style them with theme tokens only. They never write HTML.
- **MCP server** (official Go SDK), over streamable HTTP at `/mcp` or over stdio. It has 12 tools: `create_deck`, `get_deck`, `list_decks`, `add_slide`, `update_slide`, `delete_slide`, `move_slide`, `patch_deck` (RFC 6902), `set_theme`, `list_themes`, `lint_deck` and `get_share_url`. It also serves 5 resources (the schema, the layout, animation and theme catalogues, and a design guide) and 3 prompts.
- **Agent feedback loop.** Every write returns the new version and its lint findings: overflow, density, too many or too-long bullets, missing alt text, contrast, build gaps, and morphs with nothing to match.
- **Optimistic concurrency.** A write that sends a stale `base_version` is rejected with a readable error. Writes are atomic: a patch either validates in full or nothing changes.
- **Live viewer** at `/d/<id>`. Edits stream over WebSocket as JSON Patch. The viewer follows the slide being edited, highlights what changed and keeps a lint panel.
- **Motion.** Build steps with presets (fade, fade-up, scale-in, wipe, typewriter), bullets that build one item at a time, stats that count up, and slide transitions (fade, slide, push, zoom, **morph** via the View Transitions API with a crossfade fallback). It respects `prefers-reduced-motion`.
- **Two themes**: Aurora (dark, drifting glow) and Paper (light, editorial).
- **SQLite storage** (pure Go, no CGO) with the full version history.

## Run it (Docker)

```bash
docker build -t slidekick:local .
docker run -d --name slidekick -p 7410:7410 \
  -e SLIDEKICK_TOKEN=change-me -e SLIDEKICK_BASE_URL=http://<host>:7410 \
  -v slidekick-data:/data slidekick:local
```

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
