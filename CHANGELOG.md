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

### Brand, deletion and working in view
- 13 more themes (18 in all): boardroom, noir, synthwave, blueprint, terminal, chalkboard, sorbet, sunset, ocean, forest, magazine, brutalist and contrast (High Contrast), with 6 more self-hosted OFL font families (Playfair Display with italic, Fredoka, Oswald, Caveat, Atkinson Hyperlegible Next, Unbounded). Tests lint the example decks in every theme and enforce token contrast.
- 22 more build presets (fade-down/left/right, slide-up/down/left/right, zoom-in, zoom-out, pop, drop, bounce-in, flip-x, flip-y, rotate-in, swing, blur-in, reveal, wipe-down, iris, stretch, highlight) and 12 more transitions (zoom-out, fade-through, cover, uncover, push-up, wipe, split, iris, blur, flip, cube, swap), all reduced-motion safe.
- A logo (a slide kicked into motion) and a wordmark, used as the favicon, Apple touch icon, in the app header, editor, sign-in pages, viewer and standalone exports.
- Delete decks from the home page: owners and admins only, with confirmation; watchers are told the deck is gone.
- `focus_slide` MCP tool: agents bring the slide they're working on into the user's view; edits already move the view to the changed slide.
- The AI chat works only on the open deck (no creating decks out of view, every call pinned to it) and brings slides into view as it works; naming a slide in a message ("slide 4") focuses it at once.

### Decisions of 2026-11-20 (see docs/decisions.md)
- New decks are private by default: watching needs a sign-in or the private link. Existing decks and links are unchanged.
- TLS on the LAN through a reverse proxy: `SLIDEKICK_TRUSTED_PROXIES` names the proxy; cookies are `Secure` on https requests; lockouts use the real client address; forwarding headers from other peers are ignored and the first such peer is logged.
- The server token is documented as an all-powerful emergency key that stays configured as a backstop.
- Deck ownership confirmed: one owner, everyone signed in views, owner or admin edits, no per-deck collaborators.

### Accounts and sign-in
- User accounts with admin, editor and viewer roles, enforced on every REST route, the live WebSocket and MCP. Decks have owners; everyone signed in can view every deck, and only owners and admins change one (or every editor, with an instance setting).
- Sign-in with username and password (argon2id), 14-day session cookies with CSRF protection, per-address and per-username lockout, sign-out. First admin from `SLIDEKICK_ADMIN_*` or `/setup` with a one-time code; `slidekick admin` resets access from the host.
- Single sign-on with OpenID Connect, several providers at once (Entra, Google, Authentik, Keycloak...): discovery, code flow with PKCE, state, nonce, issuer and audience checks, trailing-slash tolerant issuers, group and email role rules, optional account auto-creation.
- Personal agent tokens (`skp_...`) let agents act as a user with that user's role. The server token and deck tokens keep working; existing decks keep their links and pass to the first admin.
- Stored secrets are encrypted with AES-256-GCM (`SLIDEKICK_SECRET_KEY` or a generated key file).

### AI
- Admins connect AI providers (OpenAI, Anthropic, Google, any OpenAI-compatible endpoint including local models) with base URL, encrypted key and allowed models.
- Editor chat panel for editors and admins: streamed replies, each step shown, the editor follows the changes, Stop cancels, Undo restores the deck to before the run.
- The chat edits through Slidekick's own MCP tools as the signed-in user (short-lived token), so roles, ownership, validation, lint and sanitising all apply; sharing, uploads, exports and settings aren't available to it.
- Outbound calls only reach the provider's configured host and port; keys never reach the browser or logs.

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
- Mermaid 12 for diagrams, pinned to its classic look and dagre layout so existing diagrams keep their shape; the full standalone export grows to about 7 MB because Mermaid now bundles the ELK layout engine (the lite export, used for decks without diagrams, is unchanged).
- Build: Docker base images pinned by multi-arch digest, GitHub Actions on Node 24 majors, Dependabot keeps both current weekly.
