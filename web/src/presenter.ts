// Presenter view: current slide, next slide, notes, timer and build progress.
// Every move is broadcast so the audience window (same machine, via
// BroadcastChannel) and remote viewers (via WebSocket) follow along.
import { Player, type Position } from "../runtime/player";
import { renderSlide, STAGE } from "../runtime/render";
import type { Catalog, Deck, PatchEvent } from "../runtime/types";
import { applyPatch } from "../runtime/patch";
import { authHeaders, currentMe, ensureAuth } from "./auth";
import { withKey } from "./key";
import { castMenu } from "./cast-menu";
import { Laser } from "./laser";
import { Live, type LiveMsg } from "./sync";
import { followTo, Narrator, slideIndex, type NarrationStep } from "./narrate";
import { alertModal } from "./ui/modal";

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
};

export async function mountPresenter(host: HTMLElement, deckId: string) {
  // Cast mode: the page a TV opens when the server casts the deck (no sign-in, no controls).
  if (new URLSearchParams(location.search).has("cast")) return (await import("./cast")).mountCast(host, deckId);
  document.body.classList.add("pr-page");
  const token = await ensureAuth("present this deck to remote viewers", deckId);
  void token;
  const deckFetch = () => fetch(`/api/decks/${encodeURIComponent(deckId)}`, { cache: "no-store" });
  const [catalog, data] = await Promise.all([
    fetch("/api/catalog").then((r) => r.json() as Promise<Catalog>),
    deckFetch().then((r) => (r.ok ? (r.json() as Promise<{ version: number; deck: Deck }>) : null)),
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
  const cast = castMenu(deckId, { buttonClass: "pr-btn", up: true });
  controls.append(openAud, laserBtn, cast.el, status);
  // AI voice: start/stop a narrated run and see where it is.
  const ai = el("div", "pr-ai");
  const aiBtn = el("button", "pr-btn", "Present with AI voice");
  const capBtn = el("button", "pr-btn", "Captions");
  const aiStatus = el("span", "pr-ai-status");
  aiStatus.setAttribute("role", "status");
  [aiBtn, capBtn].forEach((b) => (b.type = "button"));
  aiBtn.title = "An AI voice reads each slide's speaker notes and moves through the deck";
  capBtn.title = "Show what the voice says over the slide";
  const can = currentMe()?.can ?? {};
  aiBtn.hidden = !(can.narrate && can["deck:share"]);
  ai.append(aiBtn, capBtn, aiStatus);
  side.append(nextBox, meta, notes, ai, controls);
  root.append(current, side);
  host.append(root);

  const player = new Player(current, catalog);
  player.setDeck(deck);
  const laser = new Laser(player.stage);

  // ---- AI narration: this screen plays the voice and reports each line heard ----
  const narrator = new Narrator({
    deckId,
    host: current,
    ack: true,
    audible: true,
    captions: true,
    show: (index, build, slideId) => followTo(player, slideIndex(deck.slides, index, slideId), build),
  });
  const setCaptions = (on: boolean) => {
    narrator.setCaptions(on);
    capBtn.setAttribute("aria-pressed", String(on));
  };
  setCaptions(true);
  capBtn.onclick = () => setCaptions(!narrator.captions);
  const showRun = (running: boolean, text: string, err = false) => {
    aiBtn.textContent = running ? "Stop AI voice" : "Present with AI voice";
    aiBtn.classList.toggle("on", running);
    aiStatus.textContent = text;
    aiStatus.classList.toggle("err", err);
  };
  const onNarration = (m: LiveMsg) => {
    if (m.state === "started") showRun(true, "Starting the AI voice…");
    else if (m.state === "step") {
      const st = m as unknown as NarrationStep;
      showRun(true, `Narrating slide ${slideIndex(deck.slides, st.index, st.slideId) + 1} of ${deck.slides.length}`);
    } else if (m.state === "stopped") showRun(false, "AI voice stopped");
    else if (m.state === "ended") showRun(false, m.error ? `AI voice stopped: ${m.error}` : "AI voice finished", Boolean(m.error));
  };
  const presentURL = `/api/decks/${encodeURIComponent(deckId)}/present`;
  fetch(presentURL, { cache: "no-store" })
    .then((r) => (r.ok ? r.json() : null))
    .then((st: { running: boolean; slide: number; slides: number } | null) => {
      if (st?.running) showRun(true, `Narrating slide ${st.slide || 1} of ${st.slides}`);
    })
    .catch(() => {});
  aiBtn.onclick = async () => {
    const running = aiBtn.classList.contains("on");
    aiBtn.disabled = true;
    try {
      const r = await fetch(presentURL, {
        method: running ? "DELETE" : "POST",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        // From the slide on screen; from the start when that's the last one (a run just ended there).
        body: running ? undefined : JSON.stringify({ from: player.position.index >= deck.slides.length - 1 ? 1 : player.position.index + 1 }),
      });
      const data = await r.json().catch(() => ({}));
      if (r.status === 422 && Array.isArray(data.missingNotes)) {
        const nums: number[] = data.missingNotes;
        const list = nums.length === 1 ? `Slide ${nums[0]} has` : `Slides ${nums.slice(0, -1).join(", ")} and ${nums[nums.length - 1]} have`;
        await alertModal({
          title: "Some slides have no speaker notes",
          message: `${list} no speaker notes, so the voice has nothing to say there.\n\nWrite notes for ${nums.length === 1 ? "it" : "them"} in the editor (or ask the AI to write them), then try again. Blank lines split a slide's notes across its build steps.`,
        });
      } else if (!r.ok) {
        await alertModal({ title: running ? "Couldn't stop the AI voice" : "Couldn't start the AI voice", message: data.error ?? `${r.status} ${r.statusText}` });
      } else if (running) showRun(false, "AI voice stopped");
    } finally {
      aiBtn.disabled = false;
    }
  };

  // ---- live connection: broadcasts our position; follows agent edits ----
  const live = new Live(
    deckId,
    async (m) => {
      if (narrator.handle(m)) return onNarration(m);
      cast.onEvent(m);
      // A TV that just started casting opens on slide 1: tell it where we are once it has loaded.
      if (m.type === "cast" && m.state === "started") [4000, 8000, 15000].forEach((ms) => setTimeout(() => send(player.position), ms));
      if (m.type !== "patch") return;
      const ev = m as unknown as PatchEvent;
      if (ev.version <= version) return;
      try {
        if (ev.baseVersion !== version) throw new Error("gap");
        deck = applyPatch(deck, ev.ops);
      } catch {
        const r = await deckFetch().then((x) => x.json());
        deck = r.deck;
      }
      version = ev.version;
      player.setDeck(deck, { keepSlideId: player.slideId });
      await player.refresh();
    },
    (s) => (status.textContent = s === "live" ? "live · viewers follow you" : "offline: audience windows on this machine still follow"),
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
    window.open(withKey(`/d/${encodeURIComponent(deckId)}?audience=1`) + `#${p.index + 1}${p.step ? `.${p.step}` : ""}`, `slidekick-audience-${deckId}`, "popup,width=1280,height=720");
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
