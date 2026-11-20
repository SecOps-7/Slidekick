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
| `SLIDEKICK_TOKEN` | empty | Needed for every write: MCP, the editor, presenting to remote viewers, uploads, exports, the deck list. Without it the server is open; only do that on localhost. |
| `SLIDEKICK_BASE_URL` | `http://localhost:<port>` | Public URL used in share and download links. |
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

## TLS and reverse proxies

Slidekick speaks plain HTTP. Put it behind a TLS-terminating proxy (Caddy, Traefik, nginx) for anything beyond a trusted LAN, so the token never crosses the network in clear text. The proxy must pass WebSockets on `/ws/`.

Caddy:

```
slides.example.com {
  reverse_proxy slidekick:7410
}
```

Failed-token lockouts are per client IP. Behind a proxy every client shares the proxy's IP, so a burst of bad tokens locks everyone out for five minutes.

## Security model

- **Viewing is by link**: anyone with a deck link (`/d/<id>`, 80-bit random id) can watch it live and download its HTML export. Treat links like unlisted documents.
- **Private decks** need a key in the link as well (`/d/<id>?k=<key>`, 96-bit). Without it the deck answers 404, so its id can't even be confirmed. Make a deck private from the editor's **Share** menu or with the `set_deck_access` tool; **Make a new link** rotates the key and disconnects anyone watching with the old one. Token holders for the deck can always view it.
- **Everything else needs a token**: editing, presenting to remote viewers, uploads, PDF/PNG export, listing decks, MCP. The server token (`SLIDEKICK_TOKEN`) can do everything. A **deck edit token** (`ske_...`, created in the editor's Share menu by someone holding the server token) can do all of that for one deck only. Only its hash is stored; replacing or revoking it takes effect immediately, and an MCP session can't switch tokens midway.
- **Rendering is sandboxed from your network**: headless Chromium reaches only public internet addresses and Slidekick itself, so deck content can't read internal services through `render_slide`.
- **Uploads** are content-addressed, type-checked and served with a sandboxing Content-Security-Policy. Their URLs are unguessable (80-bit hashes) but not tied to a deck, so a private deck's images are as private as their links.
- **The `html` element** (an escape hatch for agents) renders in a sandboxed frame: no scripts, no same-origin access, no forms, and its own CSP allows only https images and fonts.

See [SECURITY.md](../SECURITY.md) to report a vulnerability.

## Backups and upgrades

Stop the container (or not: SQLite runs in WAL mode) and copy `SLIDEKICK_DATA`. Upgrades are pull-and-restart; decks are migrated forward on load. Each deck keeps its last 500 versions.
