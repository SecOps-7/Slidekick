# Contributing

Thanks for helping. Issues and pull requests are welcome.

## Setup

You need Go (version in `go.mod`), Node 24 and, for screenshots and PDF export, Chromium.

```bash
npm --prefix web ci && npm --prefix web run build   # web app, embedded into the binary
go test ./...
go run ./cmd/slidekick serve                         # http://localhost:7410
npm --prefix web run dev                             # live-reloading web app proxied to :7410
npm --prefix web test                                # animation presets and the editor's schema check
```

The TypeScript types (`web/runtime/schema.gen.ts`) are generated from `schema/deck.schema.json` and committed, and CI checks they're current. The editor's Ajv validator (`web/src/editor/validate.gen.js`) and the icon table (`web/runtime/icons.gen.ts`) are build output and not committed. `npm --prefix web run gen` writes all three; `npm run build`, `npm test`, `npm run typecheck` and `npm run dev` run it first.

### Golden screenshots

`internal/golden` renders every example deck through a running Slidekick container and compares it with `examples/golden/*.png` (CI does this on every push). Run it locally against a throwaway container:

```bash
docker build -t slidekick:dev .
docker run -d --rm --name sk-golden -p 127.0.0.1:7411:7410 --tmpfs /data:uid=65532 --tmpfs /tmp --shm-size 256m slidekick:dev serve
SLIDEKICK_GOLDEN_URL=http://127.0.0.1:7411 go test ./internal/golden
```

When a visual change is intended, add `SLIDEKICK_GOLDEN_UPDATE=1` to rewrite the references and commit them with the change, so the review shows the before and after. `SLIDEKICK_GOLDEN_OUT=dir` keeps actual and diff images of failures (CI uploads them as an artifact). Chromium comes from Debian at build time, so a Chromium update can shift antialiasing; the comparison tolerates that, but refresh the references if a new Chromium moves text.

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
| `examples` | Golden decks; tests require `launch` and `showcase` to lint clean. `gallery` covers the remaining layouts and the `html` element. `examples/golden` holds their reference screenshots. |

## Pull requests

- Keep `go test ./...`, `npm --prefix web test` and `npm --prefix web run build` green; CI runs them plus `gofmt` and the golden screenshots.
- New element types or props: update the schema, `catalog.json` (`elements`), the renderer, lint, `ops.ts` defaults in the editor (they're type-checked against the schema), and a golden example.
- New lint rules need a test in `internal/lint/lint_test.go` and must not flag the golden decks.
- Visual changes: attach before/after screenshots (`render_slide` or the PDF export).
- Dependencies must be MIT, BSD, Apache-2.0, ISC or OFL (fonts).

By contributing you agree your work is released under the MIT License.
