// Live viewer: plays a deck and follows agent edits and the presenter over
// WebSocket. Two modes:
// - Signed in (or an instance without accounts): the full deck page. Arrow
//   keys, clicks and swipes move through it; the bar has Edit and Present (as
//   allowed), lint, follow mode, narration sound and captions, and the library.
// - A viewer link, the presenter's audience window or a kiosk loop: view
//   only. The bar shows the title, position and connection and offers only
//   Fullscreen; the deck moves with the presenter (or by itself with Loop).
import { applyPatch } from "../runtime/patch";
import type { LiveEvent, LiveMap } from "../runtime/live";
import { Player, type Position } from "../runtime/player";
import type { Catalog, Deck, LintIssue, PatchEvent } from "../runtime/types";
import { me } from "./auth";
import { markSVG } from "./brand/brand";
import { Laser } from "./laser";
import { viewFetch } from "./key";
import { Live, type PointerMsg, type PresentMsg } from "./sync";
import { followTo, Narrator, slideIndex } from "./narrate";

interface DeckResponse {
  version: number;
  live?: LiveMap;
  deck: Deck;
  lint: LintIssue[];
  viewers: number;
}

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
};

async function getJSON<T>(url: string): Promise<T> {
  const r = url.startsWith("/api/decks/") ? await viewFetch(url) : await fetch(url, { cache: "no-store" });
  if (!r.ok) throw new Error(`${url}: ${r.status}`);
  return r.json() as Promise<T>;
}

export async function mountViewer(host: HTMLElement, deckId: string) {
  document.body.classList.add("sk-viewer-page");
  const [catalog, initial] = await Promise.all([
    getJSON<Catalog>("/api/catalog"),
    getJSON<DeckResponse>(`/api/decks/${encodeURIComponent(deckId)}`).catch(() => null),
  ]);
  if (!initial) {
    host.append(el("div", "app-missing", "Deck not found. If it's private, ask for a link that includes its key."));
    return;
  }

  let deck = initial.deck;
  let version = initial.version;
  let follow = true;
  // Kiosk loop (?loop=<seconds per slide>, from a viewer link made with Loop):
  // plays the deck by itself, over and over, instead of following a presenter,
  // and stays silent.
  const loopParam = new URLSearchParams(location.search).get("loop");
  const looping = loopParam !== null;
  const loopSecs = Number(loopParam) >= 3 ? Math.min(600, Number(loopParam)) : 10;
  if (looping) follow = false;

  const player = new Player(host, catalog);
  player.setDeck(deck, { live: initial.live });
  document.title = `${deck.title} · Slidekick`;

  // Headless render mode (used by render_slide): no chrome, no live sync,
  // instant motion; signals window.__skReady when the slide is fully drawn.
  if (new URLSearchParams(location.search).has("render")) {
    const w = window as { __skReduced?: boolean; __skReady?: boolean };
    w.__skReduced = true;
    document.body.classList.add("sk-render");
    const rm = /^#(\d+)(?:\.(\d+))?$/.exec(location.hash);
    await player.goto(rm ? Number(rm[1]) - 1 : 0, rm?.[2] ? Number(rm[2]) : 0, { animate: false });
    await player.ready();
    w.__skReady = true;
    return;
  }

  // Audience window (opened by the presenter): no chrome, always follows.
  const audience = new URLSearchParams(location.search).has("audience");
  // The full deck page is for signed-in people; viewer links stay view only.
  const who = audience || looping ? null : await me(deckId).catch(() => null);
  const full = Boolean(who && (who.signedIn || who.openMode));
  const can = who?.can ?? {};

  // ---- HUD ----
  const hud = el("div", "hud");
  const title = el("span", "hud-title", deck.title);
  const pos = el("span", "hud-pos");
  const live = el("span", "hud-live", "connecting");
  const fsBtn = el("button", "hud-btn", "Fullscreen");
  fsBtn.type = "button";
  const lint = el("button", "hud-lint");
  lint.type = "button";
  const followBtn = el("button", "hud-btn");
  followBtn.type = "button";
  // AI narration: shown while the AI voice speaks. Viewers hear it by default.
  const soundBtn = el("button", "hud-btn nr-toggle", "Mute narration");
  const capBtn = el("button", "hud-btn nr-toggle", "Captions");
  [soundBtn, capBtn].forEach((b) => ((b.type = "button"), (b.hidden = true)));
  const lintPanel = el("div", "lint-panel");
  lintPanel.hidden = true;
  const help = el("div", "help");
  help.hidden = true;
  if (full) {
    const home = el("a", "hud-btn hud-home");
    home.href = "/";
    home.title = "All decks";
    home.setAttribute("aria-label", "All decks");
    home.innerHTML = markSVG();
    const links: HTMLElement[] = [];
    if (can["deck:edit"]) {
      const edit = el("a", "hud-btn", "Edit");
      edit.href = `/d/${encodeURIComponent(deckId)}/edit`;
      links.push(edit);
    }
    if (can["deck:present"]) {
      const present = el("a", "hud-btn", "Present");
      present.href = `/d/${encodeURIComponent(deckId)}/present`;
      links.push(present);
    }
    hud.append(title, pos, live, lint, followBtn, soundBtn, capBtn, fsBtn, ...links, home);
    help.innerHTML = `<h2>Keys</h2><dl>
    <dt>→ Space PgDn</dt><dd>Next build or slide</dd>
    <dt>← PgUp</dt><dd>Previous</dd>
    <dt>Home / End</dt><dd>First / last slide</dd>
    <dt>F</dt><dd>Fullscreen</dd>
    <dt>L</dt><dd>Follow the presenter and live edits on or off</dd>
    <dt>H</dt><dd>Hide this bar</dd>
    <dt>?</dt><dd>This help</dd></dl>`;
  } else hud.append(title, pos, live, fsBtn);
  if (audience) document.body.classList.add("sk-audience", "hud-hidden");
  const progress = el("div", "hud-progress");
  const bar = el("div");
  progress.append(bar);
  const toast = el("div", "toast");
  toast.setAttribute("role", "status");
  document.body.append(hud, progress, toast, lintPanel, help);

  const setFollow = (on: boolean) => {
    follow = on;
    followBtn.textContent = on ? "Following" : "Not following";
    followBtn.title = on ? "Moves with the presenter and with live edits: click to look around on your own" : "Click to follow the presenter and live edits again";
    followBtn.setAttribute("aria-pressed", String(on));
  };
  setFollow(follow);
  const renderLint = (issues: LintIssue[]) => {
    if (!full) return;
    const errs = issues.filter((i) => i.severity === "error").length;
    const warns = issues.filter((i) => i.severity === "warning").length;
    lint.textContent = errs + warns === 0 ? "Lint clean" : `${errs} errors · ${warns} warnings`;
    lint.dataset.state = errs ? "error" : warns ? "warning" : "ok";
    lintPanel.replaceChildren(
      el("h2", undefined, "Lint"),
      ...(issues.length
        ? issues.map((i) => {
            const row = el("button", `lint-row lint-${i.severity}`);
            row.type = "button";
            row.append(el("b", undefined, `Slide ${i.slide + 1}`), el("span", undefined, i.message));
            if (i.fix) row.append(el("small", undefined, i.fix));
            row.onclick = () => i.slide >= 0 && player.goto(i.slide, Infinity);
            return row;
          })
        : [el("p", undefined, "No problems found.")]),
    );
  };
  renderLint(initial.lint ?? []);
  lint.onclick = () => (lintPanel.hidden = !lintPanel.hidden);
  followBtn.onclick = () => setFollow(!follow);

  let toastTimer = 0;
  const say = (msg: string) => {
    toast.textContent = msg;
    toast.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = window.setTimeout(() => toast.classList.remove("show"), 2600);
  };

  player.onChange = (p: Position) => {
    pos.textContent = p.count ? `${p.index + 1} / ${p.count}${p.maxStep ? ` · build ${p.step}/${p.maxStep}` : ""}` : "empty";
    const frac = p.count ? (p.index + (p.maxStep ? p.step / (p.maxStep + 1) : 0)) / Math.max(1, p.count - 1 || 1) : 0;
    bar.style.width = `${Math.min(100, frac * 100)}%`;
    const hash = `#${p.index + 1}${p.step ? `.${p.step}` : ""}`;
    if (location.hash !== hash) history.replaceState(null, "", hash);
  };

  // ---- initial position from the URL hash (#slide or #slide.build) ----
  const m = /^#(\d+)(?:\.(\d+))?$/.exec(location.hash);
  await player.goto(m ? Number(m[1]) - 1 : 0, m?.[2] ? Number(m[2]) : 0, { animate: false });

  // ---- kiosk loop: builds every 1.5 s, each slide held for loopSecs, then from the top ----
  if (looping) {
    const BUILD_GAP = 1500;
    const wait = () => {
      const p = player.position;
      return p.step < p.maxStep ? BUILD_GAP : loopSecs * 1000;
    };
    const step = async () => {
      const p = player.position;
      try {
        if (p.step < p.maxStep || p.index < p.count - 1) await player.next();
        else await player.goto(0, 0);
      } finally {
        window.setTimeout(step, wait());
      }
    };
    window.setTimeout(step, wait());
  }

  // ---- navigation: the full page moves with keys, clicks and swipes. View
  // only (links, audience window, loop): the deck moves with the presenter or
  // by itself, and F toggles fullscreen.
  addEventListener("keydown", (e) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const k = e.key;
    if (k === "f" || k === "F") toggleFullscreen();
    else if (!full) return;
    else if (["ArrowRight", "ArrowDown", " ", "PageDown", "Enter"].includes(k)) player.next();
    else if (["ArrowLeft", "ArrowUp", "PageUp", "Backspace"].includes(k)) player.prev();
    else if (k === "Home") player.goto(0);
    else if (k === "End") player.goto(deck.slides.length - 1, Infinity);
    else if (k === "l" || k === "L") setFollow(!follow);
    else if (k === "h" || k === "H") document.body.classList.toggle("hud-hidden");
    else if (k === "?") help.hidden = !help.hidden;
    else if (k === "Escape") (help.hidden = true), (lintPanel.hidden = true);
    else return;
    e.preventDefault();
  });
  if (full) {
    player.viewport.addEventListener("click", (e) => {
      const x = e.clientX / innerWidth;
      if (x < 0.3) player.prev();
      else player.next();
    });
    let touchX = 0;
    player.viewport.addEventListener("touchstart", (e) => (touchX = e.touches[0].clientX), { passive: true });
    player.viewport.addEventListener("touchend", (e) => {
      const dx = e.changedTouches[0].clientX - touchX;
      if (Math.abs(dx) > 40) dx < 0 ? player.next() : player.prev();
    });
  }
  const toggleFullscreen = () => (document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen().catch(() => {}));
  fsBtn.onclick = toggleFullscreen;
  // The label follows the state however it changes (this button, F, or Esc).
  const showFullscreen = () => {
    const on = Boolean(document.fullscreenElement);
    fsBtn.textContent = on ? "Exit Fullscreen" : "Fullscreen";
    fsBtn.setAttribute("aria-pressed", String(on));
  };
  document.addEventListener("fullscreenchange", showFullscreen);
  showFullscreen();

  let idle = 0;
  addEventListener("mousemove", () => {
    document.body.classList.add("hud-active");
    clearTimeout(idle);
    idle = window.setTimeout(() => document.body.classList.remove("hud-active"), 2500);
  });

  // ---- live sync ----
  const reload = async () => {
    const r = await getJSON<DeckResponse>(`/api/decks/${encodeURIComponent(deckId)}`);
    deck = r.deck;
    version = r.version;
    player.setDeck(deck, { keepSlideId: player.slideId, live: r.live ?? {} });
    title.textContent = deck.title;
    renderLint(r.lint ?? []);
    await player.refresh();
  };
  // Lint after edits, a moment after the last one.
  let lintTimer = 0;
  const refreshLint = () => {
    if (!full) return;
    clearTimeout(lintTimer);
    lintTimer = window.setTimeout(async () => {
      const r = await getJSON<DeckResponse>(`/api/decks/${encodeURIComponent(deckId)}`).catch(() => null);
      if (r) renderLint(r.lint ?? []);
    }, 400);
  };

  const onPatch = async (ev: PatchEvent) => {
    // An edit to a slide with live content replaces that content: fetch the deck afresh.
    if (ev.baseVersion !== version || player.touchesLive(ev.changed)) {
      await reload();
      return;
    }
    try {
      deck = applyPatch(deck, ev.ops);
      version = ev.version;
    } catch {
      await reload();
      return;
    }
    const keep = player.slideId;
    player.setDeck(deck, { keepSlideId: keep });
    title.textContent = deck.title;
    document.title = `${deck.title} · Slidekick`;
    const target = ev.focusSlide ? deck.slides.findIndex((s) => s.id === ev.focusSlide) : -1;
    if (follow && target >= 0 && target !== player.position.index) {
      await player.goto(target, Infinity);
    } else {
      await player.refresh();
    }
    player.flash(ev.changed);
    say(`${ev.actor === "agent" ? "Agent" : "Edit"} · version ${ev.version}`);
    refreshLint();
  };

  // Presenter: jump (with the right animation) to wherever the presenter is.
  const laser = new Laser(player.stage);
  let audBlocked = false; // the audience window's sound is blocked until a click
  const narrator = new Narrator({
    deckId,
    host: document.body,
    // The audience window is the room's speaker (the presenter view goes quiet
    // while it is open), so it reports lines as heard; remote viewers don't.
    ack: audience,
    audible: !looping,
    captions: false,
    // A browser that blocks sound in the audience window until someone clicks
    // in it hands the voice back to the presenter view meanwhile.
    onBlocked: (blocked) => {
      if (!audience) return;
      audBlocked = blocked;
      narrator.setAck(!blocked && narrator.audible);
      conn.local({ type: "audience", state: blocked ? "blocked" : "open" });
    },
    show: (index, build, slideId) => {
      if (!follow && !audience) return;
      return followTo(player, slideIndex(deck.slides, index, slideId), build);
    },
  });
  // Viewers hear the voice, without captions. The audience window does what
  // the presenter view on this machine says (quiet while a TV casts it).
  const setSound = (on: boolean) => {
    narrator.setAudible(on);
    if (audience) narrator.setAck(on && !audBlocked);
    soundBtn.textContent = on ? "Mute narration" : "Unmute narration";
    soundBtn.setAttribute("aria-pressed", String(on));
  };
  // The full page's narration buttons show while the AI voice speaks.
  const setCaptions = (on: boolean) => {
    narrator.setCaptions(on);
    capBtn.setAttribute("aria-pressed", String(on));
  };
  soundBtn.onclick = () => setSound(!narrator.audible);
  capBtn.onclick = () => setCaptions(!narrator.captions);
  setCaptions(false);
  const narrationActive = (on: boolean) => {
    if (full) soundBtn.hidden = capBtn.hidden = !on;
  };
  if (full)
    viewFetch(`/api/decks/${encodeURIComponent(deckId)}/present`)
      .then((r) => (r.ok ? r.json() : null))
      .then((st: { running?: boolean } | null) => narrationActive(Boolean(st?.running)))
      .catch(() => {});
  const followPresenter = async (m: PresentMsg) => {
    if (!follow && !audience) return;
    const p = player.position;
    if (m.index === p.index && m.step === p.step + 1) await player.next();
    else if (m.index === p.index && m.step === p.step - 1) await player.prev();
    else if (m.index === p.index + 1 && m.step === 0 && p.step >= p.maxStep) await player.next();
    else if (m.index === p.index - 1 && p.step === 0) await player.goto(m.index, m.step, { direction: -1 });
    else if (m.index !== p.index || m.step !== p.step) await player.goto(m.index, m.step, { animate: m.index !== p.index });
  };

  const conn = new Live(
    deckId,
    async (msg) => {
      // The presenter view on this machine says whether the audience window
      // plays the voice (not while a TV is casting it in the same room).
      if (audience && msg.type === "audience-sound") setSound(Boolean(msg.on));
      else if (audience && msg.type === "audience-ping") conn.local({ type: "audience", state: "open" });
      else if (msg.type === "hello" && msg.version !== version) await reload();
      else if (msg.type === "patch") await onPatch(msg as unknown as PatchEvent);
      else if (msg.type === "live") await player.live(msg as unknown as LiveEvent);
      else if (narrator.handle(msg)) narrationActive(msg.state !== "stopped" && msg.state !== "ended");
      else if (msg.type === "present") await followPresenter(msg as unknown as PresentMsg);
      else if (msg.type === "denied") say("This link has expired or was replaced: ask for a new one");
      else if (msg.type === "deleted") {
        say("This deck was deleted");
        host.replaceChildren(el("div", "app-missing", "This deck was deleted."));
      } else if (msg.type === "focus" && follow && !audience) {
        // An agent is working on this slide: show it (unless following a presenter).
        const i = deck.slides.findIndex((s) => s.id === msg.slideId);
        if (i >= 0 && i !== player.position.index) await player.goto(i, Infinity);
      }
      else if (msg.type === "pointer") {
        const pm = msg as unknown as PointerMsg;
        pm.on ? laser.show(pm.x, pm.y) : laser.hide();
      }
    },
    (state) => {
      live.textContent = state;
      live.dataset.state = state === "live" ? "on" : "off";
    },
    undefined,
    "viewer",
  );
  if (audience) {
    conn.local({ type: "audience", state: "open" });
    addEventListener("pagehide", () => conn.local({ type: "audience", state: "closed" }));
  }
}
