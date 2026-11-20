# Contributing

Thanks for helping. Issues and pull requests are welcome.

## Setup

You need Go (version in `go.mod`), Node 24 and, for screenshots and PDF export, Chromium.

```bash
npm --prefix web ci && npm --prefix web run build   # web app, embedded into the binary
go test ./...
go run ./cmd/slidekick serve                         # http://localhost:7410
npm --prefix web run dev                             # live-reloading web app proxied to :7410
```

Or build the container: `docker build --target render -t slidekick:dev .`

## Where things live

| Path | |
| --- | --- |
| `schema/deck.schema.json` | The deck contract. Change it with care; add a migration in `internal/deck/migrate.go` for breaking changes. |
| `schema/catalog.json` | Layouts, regions, presets, element docs, icon list |
| `internal/service` | The only code that writes decks (validate, version, lint, broadcast) |
| `internal/mcpserver` | MCP tools, resources and prompts; `guide.md` is the agent design guide |
| `internal/lint` | Layout and accessibility checks |
| `internal/render`, `internal/export` | Chromium screenshots and PDF; HTML/PNG export |
| `web/runtime` | Renderer, animation presets, player (shared by viewer, editor, presenter, export) |
| `web/src` | Viewer, editor (Preact), presenter, home |
| `themes` | Themes and self-hosted fonts; see `docs/themes.md` |
| `examples` | Golden decks; tests require them to validate and lint clean |

## Pull requests

- Keep `go test ./...` and `npm --prefix web run build` green; CI runs both plus `gofmt`.
- New element types or props: update the schema, `catalog.json` (`elements`), the renderer, lint, `ops.ts` defaults in the editor, and a golden example.
- New lint rules need a test in `internal/lint/lint_test.go` and must not flag the golden decks.
- Visual changes: attach before/after screenshots (`render_slide` or the PDF export).
- Dependencies must be MIT, BSD, Apache-2.0, ISC or OFL (fonts).

By contributing you agree your work is released under the MIT License.
