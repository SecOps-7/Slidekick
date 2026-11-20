# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub: **Security → Report a vulnerability** on this repository. Don't open a public issue.

Include what an attacker needs (token or not, deck access), the impact, and steps to reproduce. You'll get an acknowledgement within a week. Fixes for confirmed issues ship in a patch release, with credit if you'd like it.

## Supported versions

The latest minor release receives security fixes.

## Scope

In scope: authentication bypass, signing in without valid credentials (including single sign-on flaws such as state, nonce, issuer or audience checks), an account acting beyond its role (a viewer editing, an editor changing another person's deck or reaching admin settings) through any route, the WebSocket or MCP, session or CSRF weaknesses, reading stored secrets (SSO client secrets, AI keys) from the API or logs, the AI chat doing more than the signed-in user may or reaching hosts other than the configured provider, a new deck being watchable without a sign-in or its private link, forwarding headers (client address, https) believed from a peer that isn't a configured trusted proxy, a deck edit token reaching another deck, viewing a private deck without its key, stored XSS from deck content (decks are written by agents and must be treated as untrusted), escaping the upload sandbox, making `render_slide` or PDF export reach private networks, and denial of service that an unauthenticated client can trigger.

Out of scope: anything that needs the server token (an all-powerful emergency key by design) or the admin role (both are trusted with everything), what a deck token can do to its own deck, and deployments that run without `SLIDEKICK_TOKEN` on a network address.

The security model is described in [docs/self-hosting.md](docs/self-hosting.md#security-model).
