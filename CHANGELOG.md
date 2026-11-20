# Changelog

## 1.0.0 (unreleased)

First public release.

### Agents (MCP)
- 19 tools over streamable HTTP (`/mcp`, bearer token) or stdio: create and edit decks, JSON Patch, lint, `render_slide` screenshots, uploads, HTML/PDF/PNG export, comments, history and undo.
- Every write returns the new version, lint findings and the number of open human comments.
- Resources (schema, catalogues, design guide, per-deck comments) and prompts (`outline_to_deck`, `polish_deck`, `restyle_deck`).

### Decks
- JSON Schema contract: 8 layouts, 12 element types (heading, text, bullets, image, code, stat, quote, chart, diagram, icon, shape, embed), theme tokens only.
- Motion: build steps with five presets, per-item bullet builds, count-up stats, drawn-in charts, and fade, slide, push, zoom and morph transitions (View Transitions API).
- Five themes (Aurora, Paper, Swiss, Ember, Meadow) with self-hosted fonts.

### People
- Live viewer: follows agent edits, highlights changes, lint panel.
- Editor: slide sorter, inline text editing, property panel, build timeline, undo/redo across agent and human edits, "Send to agent" comments.
- Presenter: notes, next slide, timer, build progress, laser pointer, audience window and remote followers.

### Operations
- One static Go binary with the web app embedded; SQLite storage; distroless and Chromium (`-render`) images for amd64 and arm64.
- Security: token lockout, egress-filtered rendering, strict CSP, sandboxed uploads, size and connection limits.
