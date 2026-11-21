# Decisions

Settled questions about how Slidekick works, newest first. Each records what was decided, when, and what it changed. The open questions they answer have been removed from the rest of the docs.

## 2026-11-20: TLS on the home LAN only

**Decision.** Slidekick is served over https on the home LAN through the existing reverse proxy (Nginx Proxy Manager) with an internal hostname. Nothing is exposed to the internet: no public DNS record, no tunnel, no port forward, no public link.

**What changed.**

- `SLIDEKICK_TRUSTED_PROXIES` names the proxy's address. Only that peer may set `X-Real-IP` / `X-Forwarded-For` (the client address used for lockouts) and `X-Forwarded-Proto`. Headers from anyone else are ignored, and Slidekick logs the first untrusted peer that sends them so the proxy's address is easy to find.
- Session and sign-in cookies are marked `Secure` whenever the request arrived over https: directly over TLS, or through the trusted proxy. Plain http on the LAN still works, without the flag (browsers drop `Secure` cookies on http).
- The setup is in [self-hosting: TLS on the LAN](self-hosting.md#tls-on-the-lan). The proxy host, its access list and the LAN DNS record are set up by whoever runs the deployment, in the proxy and the LAN DNS; Slidekick only needs `SLIDEKICK_TRUSTED_PROXIES` and an https `SLIDEKICK_BASE_URL` afterwards.

## 2026-11-20: Keep the server token as a backstop

**Decision.** `SLIDEKICK_TOKEN` stays configured as an all-powerful emergency key. Personal agent tokens (`skp_...`) remain the normal way to connect agents.

**What changed.** The docs now describe the server token as an emergency key and say where it lives (the deployment's owner-only env file), and no longer suggest removing it. Its value is never printed by Slidekick, its docs or its tooling.

## 2026-11-20: New decks are private by default

**Decision.** A new deck needs a sign-in or its private link (with the view key) to watch. Decks that existed before this change, and their links, stay exactly as they were.

**What changed.**

- Every way of creating a deck (the editor, `create_deck` over MCP, the AI chat) makes it private with a fresh view key. The link agents and the editor hand out includes the key.
- Decks created earlier keep no access setting and remain public by link; edits don't change that. Owners and admins can still make any deck public or private in the editor's **Share** menu or with `set_deck_access`.
- Tests: `TestNewDecksArePrivate` and `TestExistingPublicDeckUnchanged` in `internal/server`.

## 2026-11-20: Deck ownership stays as built

**Decision.** One owner per deck. Every signed-in user can view every deck; only the owner or an admin edits. The **Settings → General → Editors can edit every deck** switch stays. There are no per-deck collaborators.

**What changed.** Nothing in the code: this confirms the model shipped with accounts. The docs state it as decided.
