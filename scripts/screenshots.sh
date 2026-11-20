#!/usr/bin/env bash
# Regenerates docs/img/slides.png: loads the golden decks into a running
# Slidekick (render image), renders chosen slides in several themes through the
# real render_slide MCP tool, and tiles them with ImageMagick.
#
#   SLIDEKICK_URL=http://localhost:7410 SLIDEKICK_TOKEN=... scripts/screenshots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
URL="${SLIDEKICK_URL:-http://localhost:7410}"
OUT=docs/img
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$OUT"

python3 -I - "$URL" "${SLIDEKICK_TOKEN:-}" "$TMP" <<'EOF'
import base64, json, sys, urllib.request
url, token, tmp = sys.argv[1], sys.argv[2], sys.argv[3]
sid = None
def rpc(method, params=None, notify=False):
    global sid
    body = {"jsonrpc": "2.0", "method": method}
    if params is not None: body["params"] = params
    if not notify: body["id"] = 1
    h = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    if token: h["Authorization"] = "Bearer " + token
    if sid: h["Mcp-Session-Id"] = sid
    r = urllib.request.urlopen(urllib.request.Request(url + "/mcp", json.dumps(body).encode(), h), timeout=120)
    sid = r.headers.get("Mcp-Session-Id") or sid
    raw = r.read().decode()
    for line in raw.splitlines():
        if line.startswith("data:"): return json.loads(line[5:])
    return json.loads(raw) if raw.strip() else None
def tool(name, args):
    res = rpc("tools/call", {"name": name, "arguments": args})["result"]
    if res.get("isError"): sys.exit(f"{name}: {res['content'][0]['text']}")
    return res
rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "screenshots", "version": "1"}})
rpc("notifications/initialized", notify=True)
shots = [  # (example, theme, slide index)
    ("launch.json", "aurora", 0), ("showcase.json", "ember", 1), ("launch.json", "paper", 5),
    ("showcase.json", "swiss", 5), ("showcase.json", "meadow", 2), ("showcase.json", "ember", 3),
]
decks = {}
for i, (ex, theme, slide) in enumerate(shots):
    key = (ex, theme)
    if key not in decks:
        d = json.load(open(f"examples/{ex}"))
        decks[key] = tool("create_deck", {"title": d["title"], "theme": theme, "slides": d["slides"]})["structuredContent"]["deckId"]
    res = tool("render_slide", {"deck_id": decks[key], "slide": slide, "width": 960})
    img = next(c for c in res["content"] if c["type"] == "image")
    open(f"{tmp}/{i}.png", "wb").write(base64.b64decode(img["data"]))
    print("rendered", ex, theme, slide)
EOF

montage "$TMP"/{0,1,2,3,4,5}.png -tile 3x2 -geometry 960x540+12+12 -background '#0b0e17' "$TMP/sheet.png"
convert "$TMP/sheet.png" -resize 1800x "$OUT/slides.png"
echo "wrote $OUT/slides.png"
