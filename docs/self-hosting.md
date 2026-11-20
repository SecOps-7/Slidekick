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

- **Viewing is public**: anyone with a deck link (`/d/<id>`, 80-bit random id) can watch it live and download its HTML export. Treat links like unlisted documents.
- **Everything else needs the token**: editing, presenting to remote viewers, uploads, PDF/PNG export, listing decks, MCP.
- **Rendering is sandboxed from your network**: headless Chromium reaches only public internet addresses and Slidekick itself, so deck content can't read internal services through `render_slide`.
- **Uploads** are content-addressed, type-checked and served with a sandboxing Content-Security-Policy.

See [SECURITY.md](../SECURITY.md) to report a vulnerability.

## Backups and upgrades

Stop the container (or not: SQLite runs in WAL mode) and copy `SLIDEKICK_DATA`. Upgrades are pull-and-restart; decks are migrated forward on load. Each deck keeps its last 500 versions.
