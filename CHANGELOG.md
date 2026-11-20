# Changelog

## 1.0.0 (unreleased)

First public release.

### Agents (MCP)
- 20 tools over streamable HTTP (`/mcp`, bearer token) or stdio: create and edit decks, JSON Patch, lint, `render_slide` screenshots, uploads, HTML/PDF/PNG export, comments, history and undo.
- Every write returns the new version, lint findings and the number of open human comments.
- Deck edit tokens: an `ske_...` token edits, presents and exports one deck only (MCP and REST), and an MCP session can't be reused under another token.
- Resources (schema, catalogues, design guide, per-deck comments) and prompts (`outline_to_deck`, `polish_deck`, `restyle_deck`).

### Decks
- JSON Schema contract: 8 layouts, 13 element types (heading, text, bullets, image, code, stat, quote, chart, diagram, icon, shape, embed, and a sandboxed `html` escape hatch that lint always flags), theme tokens only.
- Motion: build steps with six presets (including `draw`, which traces icons and other line art), spring entrances and stagger from Motion, per-item bullet builds, count-up stats, drawn-in charts, and fade, slide, push, zoom and morph transitions (View Transitions API, with a FLIP fallback in other browsers).
- Five themes (Aurora, Paper, Swiss, Ember, Meadow) with self-hosted fonts.

### People
- Live viewer: follows agent edits, highlights changes, lint panel.
- Private decks: links carry a view key; rotating it disconnects old viewers.
- Editor: follows the slide the agent is editing (toggle), Share menu (private links, deck tokens), checks every edit against the schema before sending it, slide sorter, inline text editing, property panel, build timeline, undo/redo across agent and human edits, "Send to agent" comments.
- Presenter: notes, next slide, timer, build progress, laser pointer, audience window and remote followers.

### Operations
- One static Go binary with the web app embedded; SQLite storage; distroless and Chromium (`-render`) images for amd64 and arm64.
- Optional S3-compatible storage (AWS S3, MinIO and others) for uploaded and exported files via `SLIDEKICK_S3_*`; files are served with ETag and Range support either way.
- Security: token lockout, egress-filtered rendering, strict CSP, sandboxed uploads, size and connection limits.
- Tests: Go unit and integration tests, Vitest for animation presets and the editor's schema check, and golden screenshots of every example deck compared in CI.
- Shared contract: TypeScript types and the editor's validator are generated from the JSON Schema.
- Build: Docker base images pinned by multi-arch digest, GitHub Actions on Node 24 majors, Dependabot keeps both current weekly.
