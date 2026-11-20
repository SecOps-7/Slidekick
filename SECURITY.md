# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub: **Security → Report a vulnerability** on this repository. Don't open a public issue.

Include what an attacker needs (token or not, deck access), the impact, and steps to reproduce. You'll get an acknowledgement within a week. Fixes for confirmed issues ship in a patch release, with credit if you'd like it.

## Supported versions

The latest minor release receives security fixes.

## Scope

In scope: authentication bypass, stored XSS from deck content (decks are written by agents and must be treated as untrusted), escaping the upload sandbox, making `render_slide` or PDF export reach private networks, and denial of service that an unauthenticated client can trigger.

Out of scope: anything that needs the server token (token holders are trusted to edit everything), and deployments that run without `SLIDEKICK_TOKEN` on a network address.

The security model is described in [docs/self-hosting.md](docs/self-hosting.md#security-model).
