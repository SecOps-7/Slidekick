// Entry for exported decks: a single self-contained HTML file that plays the
// deck offline (no server, no live sync). The Go exporter inlines this bundle
// plus a <script type="application/json" id="sk-data"> holding {deck, catalog}.
import "../runtime/runtime.css";
import { Player } from "../runtime/player";
import type { Catalog, Deck } from "../runtime/types";

async function main() {
  const data = JSON.parse(document.getElementById("sk-data")!.textContent!) as { deck: Deck; catalog: Catalog };
  const app = document.getElementById("app")!;
  document.body.style.margin = "0";
  const player = new Player(app, data.catalog);
  player.setDeck(data.deck);

  const bar = document.createElement("div");
  bar.style.cssText = "position:fixed;left:0;bottom:0;height:3px;background:var(--sk-accent);transition:width .4s ease;z-index:5";
  document.body.append(bar);
  player.onChange = (p) => {
    const frac = p.count > 1 ? (p.index + (p.maxStep ? p.step / (p.maxStep + 1) : 0)) / (p.count - 1) : 1;
    bar.style.width = `${Math.min(100, frac * 100)}%`;
    const hash = `#${p.index + 1}${p.step ? `.${p.step}` : ""}`;
    if (location.hash !== hash) history.replaceState(null, "", hash);
  };

  const m = /^#(\d+)(?:\.(\d+))?$/.exec(location.hash);
  await player.goto(m ? Number(m[1]) - 1 : 0, m?.[2] ? Number(m[2]) : 0, { animate: false });

  addEventListener("keydown", (e) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const k = e.key;
    if (["ArrowRight", "ArrowDown", " ", "PageDown", "Enter"].includes(k)) player.next();
    else if (["ArrowLeft", "ArrowUp", "PageUp", "Backspace"].includes(k)) player.prev();
    else if (k === "Home") player.goto(0);
    else if (k === "End") player.goto(data.deck.slides.length - 1, Infinity);
    else if (k === "f" || k === "F") document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen().catch(() => {});
    else return;
    e.preventDefault();
  });
  player.viewport.addEventListener("click", (e) => (e.clientX / innerWidth < 0.3 ? player.prev() : player.next()));
  let tx = 0;
  player.viewport.addEventListener("touchstart", (e) => (tx = e.touches[0].clientX), { passive: true });
  player.viewport.addEventListener("touchend", (e) => {
    const dx = e.changedTouches[0].clientX - tx;
    if (Math.abs(dx) > 40) dx < 0 ? player.next() : player.prev();
  });
}

main();
