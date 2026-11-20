// Live viewer: plays a deck, follows agent edits over WebSocket, and shows a
// small heads-up bar (position, connection, lint, follow mode).
import { applyPatch } from "../runtime/patch";
import { Player, type Position } from "../runtime/player";
import type { Catalog, Deck, LintIssue, PatchEvent } from "../runtime/types";
import { Laser } from "./laser";
import { Live, type PointerMsg, type PresentMsg } from "./sync";

interface DeckResponse {
  version: number;
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
  const r = await fetch(url, { cache: "no-store" });
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
    host.append(el("div", "app-missing", "Deck not found."));
    return;
  }

  let deck = initial.deck;
  let version = initial.version;
  let follow = true;

  const player = new Player(host, catalog);
  player.setDeck(deck);
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

  // ---- HUD ----
  const hud = el("div", "hud");
  const title = el("span", "hud-title", deck.title);
  const pos = el("span", "hud-pos");
  const live = el("span", "hud-live", "connecting");
  const lint = el("button", "hud-lint");
  lint.type = "button";
  const followBtn = el("button", "hud-btn", "Following edits");
  followBtn.type = "button";
  followBtn.setAttribute("aria-pressed", "true");
  const fsBtn = el("button", "hud-btn", "Fullscreen");
  fsBtn.type = "button";
  const home = el("a", "hud-btn", "All decks");
  home.href = "/";
  const edit = el("a", "hud-btn", "Edit");
  edit.href = `/d/${encodeURIComponent(deckId)}/edit`;
  const present = el("a", "hud-btn", "Present");
  present.href = `/d/${encodeURIComponent(deckId)}/present`;
  hud.append(title, pos, live, lint, followBtn, fsBtn, edit, present, home);
  // Audience window (opened by the presenter): no chrome, always follows.
  const audience = new URLSearchParams(location.search).has("audience");
  if (audience) document.body.classList.add("sk-audience", "hud-hidden");
  const progress = el("div", "hud-progress");
  const bar = el("div");
  progress.append(bar);
  const toast = el("div", "toast");
  toast.setAttribute("role", "status");
  const lintPanel = el("div", "lint-panel");
  lintPanel.hidden = true;
  const help = el("div", "help");
  help.hidden = true;
  help.innerHTML = `<h2>Keys</h2><dl>
    <dt>→ Space PgDn</dt><dd>Next build or slide</dd>
    <dt>← PgUp</dt><dd>Previous</dd>
    <dt>Home / End</dt><dd>First / last slide</dd>
    <dt>F</dt><dd>Fullscreen</dd>
    <dt>L</dt><dd>Follow live edits on or off</dd>
    <dt>H</dt><dd>Hide this bar</dd>
    <dt>?</dt><dd>This help</dd></dl>`;
  document.body.append(hud, progress, toast, lintPanel, help);

  const setFollow = (on: boolean) => {
    follow = on;
    followBtn.textContent = on ? "Following" : "Not following";
    followBtn.setAttribute("aria-pressed", String(on));
  };

  const renderLint = (issues: LintIssue[]) => {
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
  renderLint(initial.lint);
  lint.onclick = () => (lintPanel.hidden = !lintPanel.hidden);

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

  // ---- navigation ----
  addEventListener("keydown", (e) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const k = e.key;
    if (["ArrowRight", "ArrowDown", " ", "PageDown", "Enter"].includes(k)) player.next();
    else if (["ArrowLeft", "ArrowUp", "PageUp", "Backspace"].includes(k)) player.prev();
    else if (k === "Home") player.goto(0);
    else if (k === "End") player.goto(deck.slides.length - 1, Infinity);
    else if (k === "f" || k === "F") toggleFullscreen();
    else if (k === "l" || k === "L") setFollow(!follow);
    else if (k === "h" || k === "H") document.body.classList.toggle("hud-hidden");
    else if (k === "?") help.hidden = !help.hidden;
    else if (k === "Escape") (help.hidden = true), (lintPanel.hidden = true);
    else return;
    e.preventDefault();
  });
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
  const toggleFullscreen = () => (document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen().catch(() => {}));
  fsBtn.onclick = toggleFullscreen;
  followBtn.onclick = () => setFollow(!follow);

  let idle = 0;
  addEventListener("mousemove", () => {
    document.body.classList.add("hud-active");
    clearTimeout(idle);
    idle = window.setTimeout(() => document.body.classList.remove("hud-active"), 2500);
  });

  // ---- live sync ----
  let lintTimer = 0;
  const refreshLint = () => {
    clearTimeout(lintTimer);
    lintTimer = window.setTimeout(async () => {
      const r = await getJSON<DeckResponse>(`/api/decks/${encodeURIComponent(deckId)}`).catch(() => null);
      if (r) renderLint(r.lint);
    }, 400);
  };

  const reload = async () => {
    const r = await getJSON<DeckResponse>(`/api/decks/${encodeURIComponent(deckId)}`);
    deck = r.deck;
    version = r.version;
    player.setDeck(deck, { keepSlideId: player.slideId });
    title.textContent = deck.title;
    renderLint(r.lint);
    await player.refresh();
  };

  const onPatch = async (ev: PatchEvent) => {
    if (ev.baseVersion !== version) {
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
  const followPresenter = async (m: PresentMsg) => {
    if (!follow && !audience) return;
    const p = player.position;
    if (m.index === p.index && m.step === p.step + 1) await player.next();
    else if (m.index === p.index && m.step === p.step - 1) await player.prev();
    else if (m.index === p.index + 1 && m.step === 0 && p.step >= p.maxStep) await player.next();
    else if (m.index === p.index - 1 && p.step === 0) await player.goto(m.index, m.step, { direction: -1 });
    else if (m.index !== p.index || m.step !== p.step) await player.goto(m.index, m.step, { animate: m.index !== p.index });
  };

  new Live(
    deckId,
    async (msg) => {
      if (msg.type === "hello" && msg.version !== version) await reload();
      else if (msg.type === "patch") await onPatch(msg as unknown as PatchEvent);
      else if (msg.type === "present") await followPresenter(msg as unknown as PresentMsg);
      else if (msg.type === "pointer") {
        const pm = msg as unknown as PointerMsg;
        pm.on ? laser.show(pm.x, pm.y) : laser.hide();
      }
    },
    (state) => {
      live.textContent = state;
      live.dataset.state = state === "live" ? "on" : "off";
    },
  );
}
