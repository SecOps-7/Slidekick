// Presenter view: current slide, next slide, notes, timer and build progress.
// Every move is broadcast so the audience window (same machine, via
// BroadcastChannel) and remote viewers (via WebSocket) follow along.
import { Player, type Position } from "../runtime/player";
import { renderSlide, STAGE } from "../runtime/render";
import type { Catalog, Deck, PatchEvent } from "../runtime/types";
import { applyPatch } from "../runtime/patch";
import { ensureAuth } from "./auth";
import { Laser } from "./laser";
import { Live } from "./sync";

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
};

export async function mountPresenter(host: HTMLElement, deckId: string) {
  document.body.classList.add("pr-page");
  const token = await ensureAuth("present this deck to remote viewers");
  const [catalog, data] = await Promise.all([
    fetch("/api/catalog").then((r) => r.json() as Promise<Catalog>),
    fetch(`/api/decks/${encodeURIComponent(deckId)}`, { cache: "no-store" }).then((r) => (r.ok ? (r.json() as Promise<{ version: number; deck: Deck }>) : null)),
  ]);
  if (!data) {
    host.textContent = "Deck not found.";
    return;
  }
  let deck = data.deck;
  let version = data.version;
  document.title = `Presenting · ${deck.title}`;

  // ---- layout ----
  const root = el("div", "pr");
  const current = el("section", "pr-current");
  current.setAttribute("aria-label", "Current slide");
  const side = el("aside", "pr-side");
  const nextBox = el("div", "pr-next");
  const nextLabel = el("div", "pr-label", "Next");
  const nextStage = el("div", "pr-next-stage");
  nextBox.append(nextLabel, nextStage);
  const meta = el("div", "pr-meta");
  const pos = el("div", "pr-pos");
  const builds = el("div", "pr-builds");
  const timer = el("button", "pr-timer");
  timer.type = "button";
  timer.title = "Click to pause; double-click to reset (T resets)";
  const clock = el("div", "pr-clock");
  meta.append(pos, builds, timer, clock);
  const notes = el("div", "pr-notes");
  notes.setAttribute("aria-label", "Speaker notes");
  const controls = el("div", "pr-controls");
  const openAud = el("button", "pr-btn primary", "Open audience window");
  const laserBtn = el("button", "pr-btn", "Laser (L)");
  const status = el("span", "pr-status", "connecting");
  [openAud, laserBtn].forEach((b) => (b.type = "button"));
  controls.append(openAud, laserBtn, status);
  side.append(nextBox, meta, notes, controls);
  root.append(current, side);
  host.append(root);

  const player = new Player(current, catalog);
  player.setDeck(deck);
  const laser = new Laser(player.stage);

  // ---- live connection: broadcasts our position; follows agent edits ----
  const live = new Live(
    deckId,
    async (m) => {
      if (m.type !== "patch") return;
      const ev = m as unknown as PatchEvent;
      if (ev.version <= version) return;
      try {
        if (ev.baseVersion !== version) throw new Error("gap");
        deck = applyPatch(deck, ev.ops);
      } catch {
        const r = await fetch(`/api/decks/${encodeURIComponent(deckId)}`, { cache: "no-store" }).then((x) => x.json());
        deck = r.deck;
      }
      version = ev.version;
      player.setDeck(deck, { keepSlideId: player.slideId });
      await player.refresh();
    },
    (s) => (status.textContent = s === "live" ? "live · viewers follow you" : "offline: audience windows on this machine still follow"),
    token,
  );

  const send = (p: Position) => live.broadcast({ type: "present", index: p.index, step: p.step });

  // ---- next slide preview ----
  let nextKey = "";
  const showNext = (p: Position) => {
    const s = deck.slides[p.index + 1];
    const key = s ? JSON.stringify(s) + deck.theme : "end";
    if (key === nextKey) return;
    nextKey = key;
    if (!s) {
      nextStage.replaceChildren(el("div", "pr-end", "End of deck"));
      return;
    }
    const [w, h] = STAGE[deck.aspect] ?? STAGE["16:9"];
    const box = el("div", "pr-next-inner");
    box.style.width = `${w}px`;
    box.style.height = `${h}px`;
    box.append(renderSlide(deck, s, catalog.layouts[s.layout]?.regions ?? []).el);
    nextStage.replaceChildren(box);
    const fit = () => (box.style.transform = `scale(${nextStage.clientWidth / w})`);
    fit();
    requestAnimationFrame(fit);
  };

  player.onChange = (p) => {
    pos.textContent = `Slide ${p.index + 1} of ${p.count}`;
    builds.replaceChildren(
      ...Array.from({ length: p.maxStep }, (_, i) => {
        const d = el("i", i < p.step ? "on" : "");
        d.title = `Build ${i + 1}`;
        return d;
      }),
    );
    if (!p.maxStep) builds.textContent = "no builds";
    nextLabel.textContent = p.step < p.maxStep ? `Next: build ${p.step + 1} of ${p.maxStep}, then` : "Next";
    const s = deck.slides[p.index];
    notes.replaceChildren(...(s?.notes ? s.notes.split(/\n{2,}/).map((para) => el("p", undefined, para)) : [el("p", "pr-none", "No notes for this slide.")]));
    showNext(p);
    send(p);
    history.replaceState(null, "", `#${p.index + 1}${p.step ? `.${p.step}` : ""}`);
  };

  // ---- timer ----
  let start = performance.now();
  let paused = 0;
  let pausedAt: number | null = null;
  const fmt = (ms: number) => {
    const s = Math.floor(ms / 1000);
    return `${String(Math.floor(s / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
  };
  setInterval(() => {
    const now = performance.now();
    timer.textContent = `${pausedAt ? "⏸ " : ""}${fmt((pausedAt ?? now) - start - paused)}`;
    clock.textContent = new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }, 250);
  const resetTimer = () => {
    start = performance.now();
    paused = 0;
    pausedAt = null;
  };
  timer.onclick = () => {
    if (pausedAt) {
      paused += performance.now() - pausedAt;
      pausedAt = null;
    } else pausedAt = performance.now();
  };
  timer.ondblclick = resetTimer;

  // ---- audience window ----
  openAud.onclick = () => {
    const p = player.position;
    window.open(`/d/${encodeURIComponent(deckId)}?audience=1#${p.index + 1}${p.step ? `.${p.step}` : ""}`, `slidekick-audience-${deckId}`, "popup,width=1280,height=720");
    setTimeout(() => send(player.position), 1200);
  };

  // ---- laser pointer ----
  let laserOn = false;
  const setLaser = (on: boolean) => {
    laserOn = on;
    laserBtn.classList.toggle("on", on);
    current.classList.toggle("pr-laser", on);
    if (!on) {
      laser.hide();
      live.broadcast({ type: "pointer", x: 0, y: 0, on: false });
    }
  };
  laserBtn.onclick = () => setLaser(!laserOn);
  let last = 0;
  current.addEventListener("pointermove", (e) => {
    if (!laserOn) return;
    const r = player.stage.getBoundingClientRect();
    const x = (e.clientX - r.left) / r.width;
    const y = (e.clientY - r.top) / r.height;
    const on = x >= 0 && x <= 1 && y >= 0 && y <= 1;
    on ? laser.show(x, y) : laser.hide();
    const now = performance.now();
    if (now - last > 30) {
      last = now;
      live.broadcast({ type: "pointer", x, y, on });
    }
  });

  // ---- navigation ----
  const m = /^#(\d+)(?:\.(\d+))?$/.exec(location.hash);
  await player.goto(m ? Number(m[1]) - 1 : 0, m?.[2] ? Number(m[2]) : 0, { animate: false });
  addEventListener("keydown", (e) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const k = e.key;
    if (["ArrowRight", "ArrowDown", " ", "PageDown", "Enter"].includes(k)) player.next();
    else if (["ArrowLeft", "ArrowUp", "PageUp", "Backspace"].includes(k)) player.prev();
    else if (k === "Home") player.goto(0);
    else if (k === "End") player.goto(deck.slides.length - 1, Infinity);
    else if (k === "l" || k === "L") setLaser(!laserOn);
    else if (k === "t" || k === "T") resetTimer();
    else if (k === "f" || k === "F") document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen().catch(() => {});
    else return;
    e.preventDefault();
  });
  current.addEventListener("click", (e) => {
    if (laserOn) return;
    const r = current.getBoundingClientRect();
    (e.clientX - r.left) / r.width < 0.3 ? player.prev() : player.next();
  });
}
