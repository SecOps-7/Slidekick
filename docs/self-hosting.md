# Self-hosting

Slidekick is one container. Pick an image:

| Image | Size | What you get |
| --- | --- | --- |
| `ghcr.io/secops-7/slidekick:<version>-render` | ~890 MB | Everything, including `render_slide` and PDF/PNG export (headless Chromium) |
| `ghcr.io/secops-7/slidekick:<version>` | ~32 MB | Everything except screenshots and PDF/PNG export |

Both are multi-arch (amd64, arm64) and run as a non-root user.

## Docker Compose

```yaml
services:
  slidekick:
    image: ghcr.io/secops-7/slidekick:1-render
    restart: unless-stopped
    user: "1000:1000"
    ports: ["7410:7410"]
    env_file: [./slidekick.env]          # SLIDEKICK_TOKEN=... (chmod 600)
    environment:
      SLIDEKICK_BASE_URL: https://slides.example.com
    volumes: ["./data:/data"]
    read_only: true
    tmpfs: ["/tmp:size=512m"]           # Chromium scratch space (render image)
    shm_size: 256m
    security_opt: ["no-new-privileges:true"]
    cap_drop: [ALL]
```

Create the token with `openssl rand -hex 24`. Slidekick refuses to start with a token shorter than 24 characters.

## Configuration

| Variable | Default | |
| --- | --- | --- |
| `SLIDEKICK_TOKEN` | empty | The server token: an all-powerful emergency key (see [The server token](#the-server-token-emergency-key)). Also accepted as the setup code for the first admin. At least 24 characters. |
| `SLIDEKICK_TRUSTED_PROXIES` | empty | Addresses (or CIDR ranges) of your TLS reverse proxy, comma-separated. Only these peers may set the client address and `X-Forwarded-Proto`. See [TLS on the LAN](#tls-on-the-lan). |
| `SLIDEKICK_ADMIN_USERNAME` | empty | With `SLIDEKICK_ADMIN_PASSWORD`, creates this admin on first run when no account exists yet. Ignored afterwards. |
| `SLIDEKICK_ADMIN_PASSWORD` | empty | Password for that first admin (at least 10 characters). A secret: keep it in the `env_file`. |
| `SLIDEKICK_SECRET_KEY` | key file | 32 bytes (`openssl rand -hex 32`) that encrypt stored secrets: SSO client secrets and AI provider keys. Without it Slidekick creates `SLIDEKICK_DATA/secret.key` (mode 600) on first run; back it up with the data, because the stored secrets can't be decrypted without it. |
| `SLIDEKICK_BASE_URL` | `http://localhost:<port>` | The URL people use, in share and download links and SSO redirect URLs: `https://<internal hostname>` once TLS is set up. |
| `SLIDEKICK_ADDR` | `:7410` | Listen address. |
| `SLIDEKICK_DATA` | `/data` | SQLite database and uploaded files. Back this folder up. |
| `SLIDEKICK_CHROME` | auto | Path to Chromium if it isn't on `PATH`. |
| `SLIDEKICK_S3_BUCKET` | empty | Store uploaded and exported files in this S3-compatible bucket instead of `SLIDEKICK_DATA/files`. Setting it turns S3 on; the bucket must already exist. |
| `SLIDEKICK_S3_ENDPOINT` | empty | `host:port` of the S3 API, without a scheme (for example `s3.eu-central-1.amazonaws.com` or `minio:9000`). Required with a bucket. |
| `SLIDEKICK_S3_REGION` | empty | Bucket region, if your provider needs one. |
| `SLIDEKICK_S3_ACCESS_KEY` | empty | Access key. Required with a bucket. |
| `SLIDEKICK_S3_SECRET_KEY` | empty | Secret key. Required with a bucket. Keep it in the `env_file`, not in the compose file; it is never logged. |
| `SLIDEKICK_S3_PREFIX` | empty | Optional key prefix, such as `slidekick/`, to share a bucket. |
| `SLIDEKICK_S3_INSECURE` | `0` | `1` talks plain HTTP to the endpoint, for a local MinIO. Leave it off for anything across a network. |

With S3 enabled, Slidekick checks the bucket at startup and refuses to start if it can't reach it. Deck data and file metadata stay in SQLite under `SLIDEKICK_DATA`, so you still back that folder up; files already on disk are not moved to the bucket for you (copy `SLIDEKICK_DATA/files/*` to the bucket under the same names and prefix).

## Accounts, roles and sign-in

Everyone signs in with a username and password or through single sign-on. Roles:

| Role | Can |
| --- | --- |
| **admin** | Everything: every deck, users, sign-in providers, AI providers and instance settings. |
| **editor** | Create decks; edit, share (private links, deck tokens) and export **their own** decks; upload files; use AI. View, present and comment on every deck. |
| **viewer** | View, present and comment on every deck. |

**Decks have one owner** (decided 2026-11-20, see [decisions](decisions.md)). The person (or the personal agent token) that creates a deck owns it. Signed-in people see every deck on the instance, like a shared team library, but only the owner and admins can change one. There are no per-deck collaborators; turn on **Settings → General → Editors can edit every deck** for a team that edits everything together. Admins can hand a deck to someone else (`PUT /api/decks/<id>/owner`). Deleting a user moves their decks to the admin who deletes them.

**New decks are private** (decided 2026-11-20): watching one needs a sign-in or its private link. Decks created before that change stay public by link.

These rules are enforced by the server on every request (REST, the live WebSocket and MCP); the web app only hides what you can't use.

![Settings: users and roles](img/settings-users.png)

### The first admin

- **New install:** set `SLIDEKICK_ADMIN_USERNAME` and `SLIDEKICK_ADMIN_PASSWORD` for the first run, or open `/setup` and enter the one-time setup code from the log (`docker logs slidekick | grep setup_code`). Until the first admin exists and no server token is set, the server is open to anyone who can reach it, as before; set it up straight away.
- **Upgrading an install that used `SLIDEKICK_TOKEN`:** nothing stops working. The token keeps full access, decks keep their links, and `/setup` accepts the token as the setup code. The first admin becomes the owner of every existing deck.
- **Locked out?** Reset or create an admin from the host: `docker exec -i slidekick slidekick admin <username>` and type the new password. It works while the server runs.

Passwords are hashed with argon2id (64 MB, 3 passes). Sessions are 14-day `HttpOnly`, `SameSite=Lax` cookies, marked `Secure` whenever the request arrived over https (directly or through the trusted proxy); only their SHA-256 is stored. Every write from a browser also needs the session's CSRF token and a same-origin request. Ten failed sign-ins from one address, or against one username, within a minute lock it out for five minutes. Changing a password signs out the account's other sessions; disabling a user ends theirs immediately.

### Agents

Agents authenticate MCP with a bearer token:

- **Personal agent tokens** (`skp_...`, from the **Account** page) act as you, with your role: an agent can never do more than you. Decks it creates are yours. This is the recommended way.
- **Deck tokens** (`ske_...`, editor **Share** menu) edit one deck only. Its owner or an admin can create one.
- **The server token** (`SLIDEKICK_TOKEN`) also works, with full access; keep it for emergencies (below). Moving agents to personal tokens: create a token on the Account page, swap it into `claude mcp add ... --header "Authorization: Bearer skp_..."`, and decks the agent creates get an owner.

### The server token (emergency key)

`SLIDEKICK_TOKEN` is an all-powerful emergency key: whoever holds it can read, change and delete every deck, manage every user and setting, and act over MCP without any account. It stays configured as a backstop (decided 2026-11-20) for when nobody can sign in, and as the setup code for the first admin. Day-to-day agents use personal tokens instead.

- **Where it lives:** only in the deployment's owner-only env file, the one named by `env_file:` in the compose file (in the reference deployment `/opt/slidekick/slidekick.env`, mode 600, folder 700). Never in the compose file, the stack's `.env`, the repository, shell history or chat.
- **Using it without showing it:** read it from that file in the same command, e.g. ``T=$(sed -n 's/^SLIDEKICK_TOKEN=//p' /opt/slidekick/slidekick.env); curl -H "Authorization: Bearer $T" ...; unset T``. Slidekick never logs or displays it.
- **Rotating it:** put a new value (`openssl rand -hex 24`) in that file and recreate the container; the old one stops working at once.

### Single sign-on (OpenID Connect)

Admins add providers under **Settings → Sign-in providers**: Microsoft Entra, Google, Authentik, Keycloak or any OpenID Connect provider, several at once. Each needs the issuer URL, a client ID and secret (stored encrypted, never shown again), and this redirect URL registered at the provider:

```
<SLIDEKICK_BASE_URL>/auth/sso/<provider id>/callback
```

![Adding a sign-in provider](img/settings-sso.png)

Slidekick uses discovery, the authorization code flow with PKCE, and checks state (bound to the browser by a cookie), nonce, issuer, audience, expiry and signature. The issuer may be typed with or without a trailing slash: Slidekick accepts a single-slash difference from what discovery publishes and then verifies tokens against the exact published issuer. Use **Test** to check discovery.

- **Accounts:** a sign-in finds the account already linked to that provider. Otherwise it links to an existing account with the same email, but only if the provider marks the email verified (or you ticked **Trust this provider's email addresses**, for Entra work accounts, which send no `email_verified`). Otherwise, if **Create accounts for new people automatically** is on, a new account gets the default role; if it's off, the person is told to ask an admin.
- **Roles:** rules map a group (from the groups claim) or an email or email domain to a role. The first matching rule wins and is applied at every sign-in, so removing someone from the group in your directory demotes them at their next sign-in. Group claims need the provider configured to send them (Entra: token configuration → groups claim; Keycloak: a group mapper; Authentik: the default `groups` scope).
- Provider notes: Entra issuer `https://login.microsoftonline.com/<tenant id>/v2.0` (multi-tenant `common` isn't supported); Google `https://accounts.google.com`; Authentik `https://<host>/application/o/<slug>/`; Keycloak `https://<host>/realms/<realm>`. Providers on your LAN work: only an admin can add them.

## AI providers and the chat panel

Admins connect AI providers under **Settings → AI providers**: OpenAI, Anthropic, Google Gemini, or any OpenAI-compatible endpoint (Ollama, LM Studio, vLLM, LiteLLM and other local or self-hosted models). Each has a base URL, an API key (optional for local servers) and the list of models people may use; **Save and fetch models** asks the provider for its list.

Editors and admins then get an **✦ AI** button in the editor. They pick a provider and model and ask for changes ("add a slide with our 42% growth as one big number", "restyle this for a board meeting"). Replies stream in, each step is listed (✓ added a slide, ✗ refused...), the editor follows along as the changes land, **Stop** cancels the run, and **Undo these changes** restores the deck to the version from before the run (as a new version, so history keeps everything). Viewers can't use AI.

![The AI assistant in the editor](img/ai-chat.png)

How it stays safe:

- **The AI acts as the person asking.** The server runs the model and applies its changes through Slidekick's own MCP tools with a token that acts as the signed-in user, lives only for that request and is revoked when it ends. Every call goes through the same role and ownership checks as any agent, so an editor's AI can't change someone else's deck, and nobody's AI can change sharing, upload files, export or reach settings (those tools aren't offered to the chat at all).
- **Its output is untrusted.** Changes go through schema validation, sanitising and lint like any agent's: invalid elements are refused and the model is told why. The `html` element stays sandboxed. The model is told to treat deck content as data, never as instructions.
- **Keys stay on the server.** They're stored encrypted (AES-256-GCM, see `SLIDEKICK_SECRET_KEY`), never sent to the browser, and removed from any provider error before it's shown or logged. Google's key goes in a header, never in a URL.
- **Outbound calls go only to the configured provider.** Each provider's HTTP client can connect only to the host and port of its base URL (checked after DNS), ignores proxy settings and refuses redirects. A provider on your home network works because an admin set it; nobody else can add a URL. Nothing is exposed to the internet: Slidekick only makes outgoing requests.
- **Limits:** two chats per person at once, 24 model turns and 10 minutes per message, 8,000 characters per message.

Providers bill by usage; the chat sends the design guide, the layout list and the tool definitions with each request.

## TLS on the LAN

Slidekick speaks plain HTTP. Serve it over https through a TLS-terminating reverse proxy on your home network (decided 2026-11-20 for the reference deployment: the existing Nginx Proxy Manager, an internal hostname, **nothing exposed to the internet**: no public DNS record, no tunnel, no port forward).

1. **LAN DNS:** an internal record for the hostname (e.g. `slides.<your domain>`) pointing at the proxy's LAN address, in your LAN's DNS server only. A wildcard certificate the proxy already has covers it without any new public record.
2. **Proxy host:** forward `https://<hostname>` to `http://<slidekick host>:7410` with WebSockets enabled, force https, and attach an access list that allows only private address ranges and denies everything else, so the name answers on the LAN only even if the proxy also serves the internet.
3. **Slidekick:** set `SLIDEKICK_TRUSTED_PROXIES` to the proxy's LAN address (the server log names it: "ignoring forwarding headers from an untrusted peer") and `SLIDEKICK_BASE_URL` to `https://<hostname>`, then recreate the container. Single sign-on providers need their redirect URLs updated to the new base URL.

What Slidekick does behind the proxy:

- Cookies are marked `Secure` on requests that came in over https.
- Lockouts follow the real client address the proxy reports in `X-Real-IP` (NPM sends it by default), not the proxy's own.
- Forwarding headers from any other peer are ignored, so nobody on the LAN can fake an address or claim https.

Direct `http://<host>:7410` keeps working on the LAN, without `Secure` cookies; once https works, prefer the hostname, and optionally firewall port 7410 so only the proxy reaches it.

Without `SLIDEKICK_TRUSTED_PROXIES`, every client behind a proxy shares the proxy's address, so a burst of bad sign-ins locks everyone out for five minutes.

## Security model

- **Accounts and roles** decide who can list, edit, share, export and administer (see above). Signed-in people of any role can view every deck.
- **New decks are private** (decided 2026-11-20): only signed-in people, the deck's token holders, and anyone with its private link can watch it.
- **Public links** still exist for decks created before that, and for any deck an owner makes public: anyone with the link (`/d/<id>`, 80-bit random id) can watch it live and download its HTML export, signed in or not. Treat them like unlisted documents.
- **Private links** carry a key (`/d/<id>?k=<key>`, 96-bit). Without it the deck answers 404, so its id can't even be confirmed. Make a deck private from the editor's **Share** menu or with the `set_deck_access` tool; **Make a new link** rotates the key and disconnects anyone watching with the old one. Signed-in people and token holders for the deck can always view it.
- **Everything else needs a signed-in account or a token**: editing, presenting to remote viewers, uploads, PDF/PNG export, listing decks, MCP, settings. Tokens are stored as hashes; revoking one, or disabling its user, takes effect on the next request, and an MCP session can't switch to another principal midway.
- **Stored secrets** (SSO client secrets, AI provider keys) are encrypted with AES-256-GCM under `SLIDEKICK_SECRET_KEY` or the key file, are never sent to the browser and never logged.
- **Rendering is sandboxed from your network**: headless Chromium reaches only public internet addresses and Slidekick itself, so deck content can't read internal services through `render_slide`.
- **Uploads** are content-addressed, type-checked and served with a sandboxing Content-Security-Policy. Their URLs are unguessable (80-bit hashes) but not tied to a deck, so a private deck's images are as private as their links.
- **The `html` element** (an escape hatch for agents) renders in a sandboxed frame: no scripts, no same-origin access, no forms, and its own CSP allows only https images and fonts.

See [SECURITY.md](../SECURITY.md) to report a vulnerability.

## Backups and upgrades

Stop the container (or not: SQLite runs in WAL mode) and copy `SLIDEKICK_DATA`. Upgrades are pull-and-restart; decks are migrated forward on load. Each deck keeps its last 500 versions.
