# Changelog

Each release lists its changes by type: New, Changed, Fixed and Internal (also Security, Removed and Deprecated when a release has them). The about page badges every note with its type.

## 1.1.0 (unreleased)

### New
- Local HTTPS: Slidekick can serve HTTPS itself on the local network (Settings > General, off by default), on `SLIDEKICK_HTTPS_ADDR` (`:7443`) next to plain HTTP, with your own certificate or one from its own name-constrained certificate authority, which you download and trust on your devices. Generated certificates renew themselves; private keys are stored encrypted. Plain HTTP stays available for proxies and cast TVs, and casting uses it.
- Live slides: a ninth layout, Live (a headline figure, a chart and a status line), whose content an agent changes while the deck is shown, for network or home stats. `update_live` (or `POST /api/decks/<id>/live`) sets new values, adds and removes elements; each change is checked like an edit, kept as the slide's current content without adding versions, limited to 5 a second per slide, and applied in place on the viewer, audience window, presenter view and cast TV: no redraw, the presenter keeps their place, values just change (or animate with `animate`), added and removed elements ease in and out, and a small last-updated time shows unless the slide hides it. Off by default (Settings > General); only the deck's owner, an admin or the deck's edit token can send updates.
- Sharing: viewer links can be embedded in other sites an admin lists (Settings > General, off by default). Only viewer link pages relax framing, for exactly those sites; everything else stays unframeable.

## 1.0.0 (2026-11-21)

First public release.

### New
- Presenting: the built-in AI and MCP agents present a deck on their own. `start_presentation` runs the whole deck, `present_slide` drives it step by step, and each step waits until the line has been heard before moving on.
- Presenting: the voice plays on the cast TV or the audience window (the presenter view starts muted, with a Mute voice / Unmute voice button) and for remote viewers on the deck's link. Captions show only in the presenter view. The editor shows a status chip with Stop while a presentation runs.
- Voices: text-to-speech through Fish Audio, ElevenLabs, OpenAI, Cartesia, Deepgram Aura or any OpenAI-compatible server (Kokoro, Piper, LocalAI), plus free browser voices. Keys are stored encrypted and managed under Settings > Voices with a sample player and a default narration voice.
- Casting: cast a deck to a Chromecast from the editor, the presenter or an agent (`cast_deck`). The TV shows a full-screen, sign-in-free view that follows the presenter.
- Casting: cast devices on the server's network are found automatically and appear in the Cast menu; others can be added by address, saved, renamed or hidden under Settings > Cast devices.
- Sharing: two kinds of link. A viewer link is view-only (Fullscreen is its one control; clicks, swipes and keys don't move it), or with Loop plays it by itself on repeat for kiosk screens; a presenter link presents it without signing in: it moves the slides for everyone watching, uses the laser pointer and audience window, runs the AI voice and casts. Both can expire after 30 minutes to 24 hours (or never, the default); Make new links revokes every link at once.
- About page (Settings > About): the build, the system it runs on, these release notes, and every Go module, web library and font in the build with its licence.
- Agents (MCP): 28 tools over streamable HTTP (`/mcp`, bearer token) or stdio: create and edit decks, JSON Patch, lint, `render_slide` screenshots, uploads, HTML/PDF/PNG export, comments, history and undo.
- Agents (MCP): every write returns the new version, lint findings and the number of open human comments.
- Agents (MCP): deck edit tokens. An `ske_...` token edits, presents and exports one deck only (MCP and REST), and an MCP session can't be reused under another token.
- Agents (MCP): resources (schema, catalogues, design guide, per-deck comments) and prompts (`outline_to_deck`, `polish_deck`, `restyle_deck`).
- Agents (MCP): `focus_slide` brings the slide an agent is working on into the user's view; edits move the view to the changed slide on their own.
- Decks: a JSON Schema contract with 8 layouts and 13 element types (heading, text, bullets, image, code, stat, quote, chart, diagram, icon, shape, embed, and a sandboxed `html` escape hatch that lint always flags), styled with theme tokens only.
- Decks: 18 themes with self-hosted fonts (Aurora, Paper, Swiss, Ember, Meadow, Boardroom, Noir, Synthwave, Blueprint, Terminal, Chalkboard, Sorbet, Sunset, Ocean, Forest, Magazine, Brutalist and High Contrast). Tests lint the example decks in every theme and enforce token contrast.
- Decks: 28 build presets (including `draw`, which traces icons and line art) with spring entrances and stagger from Motion, per-item bullet builds, count-up stats and drawn-in charts; 18 transitions through the View Transitions API with a FLIP fallback. All respect reduced motion.
- Decks: delete a deck from the library (owners and admins, with confirmation); anyone watching is told it's gone.
- Library: every deck is a live miniature of its first slide in its own theme, with search, sort, an All/Mine filter and a menu for copying links and deleting.
- Brand: a logo (a slide kicked into motion) and a wordmark, used as the favicon, Apple touch icon, and in the app header, editor, sign-in pages, viewer and standalone exports.
- Accounts: admin, editor and viewer roles, enforced on every REST route, the live WebSocket and MCP. Decks have owners; everyone signed in can view every deck, and only owners and admins change one (or every editor, with an instance setting).
- Accounts: sign-in with username and password (argon2id), 14-day session cookies with CSRF protection, per-address and per-username lockout. The first admin comes from `SLIDEKICK_ADMIN_*` or `/setup` with a one-time code; `slidekick admin` resets access from the host.
- Accounts: single sign-on with OpenID Connect, several providers at once (Entra, Google, Authentik, Keycloak...), with PKCE, state, nonce, issuer and audience checks, group and email role rules and optional account creation.
- Accounts: personal agent tokens (`skp_...`) let agents act as a user with that user's role.
- AI: admins connect AI providers (OpenAI, Anthropic, Google, any OpenAI-compatible endpoint including local models) with a base URL, an encrypted key and allowed models.
- AI: a chat panel in the editor with streamed replies and each step shown; the editor follows the changes, Stop cancels and Undo restores the deck to before the run. It edits through Slidekick's own MCP tools as the signed-in user, so roles, ownership, validation and lint all apply.
- People: a live viewer for shared links, on another computer or someone else's laptop: it follows the presenter and agent edits, highlights changes, and offers only Fullscreen (no editing or presenting controls).
- People: private decks, whose links carry a view key; rotating it disconnects old viewers.
- People: an editor that follows the slide the agent is editing, with a Share menu (viewer and presenter links, deck tokens), a schema check on every edit and a slide sorter.
- People: a presenter view with notes, next slide, timer, build progress, laser pointer, an audience window and remote followers.
- Operations: one static Go binary with the web app embedded, SQLite storage, and one image for amd64 and arm64 with headless Chromium built in for screenshots and PDF/PNG export.
- Operations: optional S3-compatible storage (AWS S3, MinIO and others) for uploaded and exported files via `SLIDEKICK_S3_*`; files are served with ETag and Range support either way.
- Operations: TLS on the LAN through a reverse proxy. `SLIDEKICK_TRUSTED_PROXIES` names the proxy, cookies are `Secure` on https requests, and lockouts use the real client address.

### Changed
- New decks are private by default: watching one needs a sign-in or its private link. Existing decks and links are unchanged (decision of 2026-11-20, see docs/decisions.md).
- Deck ownership stays simple: one owner, everyone signed in views, the owner or an admin edits, no per-deck collaborators (decision of 2026-11-20).
- The server token is documented as an all-powerful emergency key that stays configured as a backstop (decision of 2026-11-20).
- The AI presenter talks about each slide's content and data, using the speaker notes as talking points rather than reading them out, and no longer needs notes to start.
- The AI chat works only on the open deck: it can't create decks out of view, every call is pinned to the open deck, and naming a slide in a message ("slide 4") brings it into view.
- The sign-in and first-run setup pages lead with the brand: a dark stage with the logo and wordmark at full size and a deck kicked into motion, beside the form (stacked on phones).
- Slidekick's own dialogs replace the browser's pop-ups everywhere, with a clear action on the button and Cancel focused for anything destructive.
- Pages fill the window and scale up on large screens.
- Diagrams use Mermaid 12, pinned to its classic look and dagre layout so existing diagrams keep their shape; the full standalone export grows to about 7 MB as a result.

### Fixed
- PDF export: gradient headings and big numbers (Aurora, Ember, Sunset and Ocean) could show as an empty bar in some PDF viewers, which drop the clip that shapes the gradient into letters. PDFs and printouts now use the theme's solid colours for that text; the screen and PNG screenshots keep the gradient.
- Copy link did nothing on plain-HTTP network addresses, and copied a private deck's bare address, which only works for people who sign in. It now copies the private link and works everywhere.
- The AI presenter read stage directions aloud ("the first point builds in now"); a filter now removes talk about slides, builds and transitions before anything is spoken.
- Cast discovery mistook the container's own Docker network for the LAN; it now searches only the network the server is on.

### Internal
- Security: token lockout, egress-filtered rendering, a strict CSP, sandboxed uploads, size and connection limits, and secrets encrypted with AES-256-GCM (`SLIDEKICK_SECRET_KEY` or a generated key file).
- Tests: Go unit and integration tests, Vitest for animation presets and the editor's schema check, and golden screenshots of every example deck compared in CI.
- The TypeScript types and the editor's validator are generated from the JSON Schema, so the browser and the server share one contract.
- Build: Docker base images pinned by multi-arch digest, GitHub Actions on Node 24 majors, and Dependabot keeping both current weekly.
