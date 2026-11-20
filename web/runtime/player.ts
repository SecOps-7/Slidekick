// The player owns the stage: which slide and build step are showing, how the
// stage scales to the window, and every animation between states.
import { animateChart } from "./chart";
import { applyTheme, formatStat, renderSlide, STAGE, themeReady, type Rendered } from "./render";
import { staggerDelays } from "./motion";
import { builds, countUp, flipDelta, motionFor, prefersReducedMotion, transitions } from "./presets";
import type { Catalog, Deck, Element, TransitionType } from "./types";

export interface Position {
  index: number;
  step: number;
  maxStep: number;
  count: number;
}

type DocWithVT = Document & { startViewTransition?: (cb: () => void) => { finished: Promise<void> } };

export class Player {
  readonly viewport: HTMLElement;
  readonly stage: HTMLElement;
  private deck!: Deck;
  private current: Rendered | null = null;
  private index = 0;
  private step = 0;
  private running: Animation[] = [];
  private busy: Promise<void> = Promise.resolve();
  onChange: (p: Position) => void = () => {};

  constructor(host: HTMLElement, private catalog: Catalog) {
    this.viewport = document.createElement("div");
    this.viewport.className = "sk-viewport";
    this.stage = document.createElement("div");
    this.stage.className = "sk-stage";
    this.stage.setAttribute("role", "region");
    this.stage.setAttribute("aria-live", "polite");
    this.viewport.append(this.stage);
    host.append(this.viewport);
    new ResizeObserver(() => this.fit()).observe(this.viewport);
  }

  get position(): Position {
    return { index: this.index, step: this.step, maxStep: this.current?.maxStep ?? 0, count: this.deck?.slides.length ?? 0 };
  }
  get slideId(): string | undefined {
    return this.deck?.slides[this.index]?.id;
  }

  /** Loads or replaces the deck. Keeps the current slide (by id) when possible. */
  setDeck(deck: Deck, opts: { keepSlideId?: string } = {}) {
    const prevTheme = this.deck?.theme;
    this.deck = deck;
    const theme = this.catalog.themes[deck.theme];
    if (theme && theme.name !== prevTheme) applyTheme(document.documentElement, theme);
    const [w, h] = STAGE[deck.aspect] ?? STAGE["16:9"];
    this.stage.style.width = `${w}px`;
    this.stage.style.height = `${h}px`;
    this.fit();
    if (opts.keepSlideId) {
      const i = deck.slides.findIndex((s) => s.id === opts.keepSlideId);
      if (i >= 0) this.index = i;
    }
    this.index = Math.max(0, Math.min(this.index, deck.slides.length - 1));
  }

  private fit() {
    if (!this.deck) return;
    const [w, h] = STAGE[this.deck.aspect] ?? STAGE["16:9"];
    const s = Math.min(this.viewport.clientWidth / w, this.viewport.clientHeight / h);
    this.stage.style.transform = `translate(-50%, -50%) scale(${s})`;
  }

  private regions(layout: string) {
    return this.catalog.layouts[layout]?.regions ?? [];
  }

  private render(index: number): Rendered | null {
    const slide = this.deck.slides[index];
    return slide ? renderSlide(this.deck, slide, this.regions(slide.layout)) : null;
  }

  private finishRunning() {
    for (const a of this.running) a.finish();
    this.running = [];
  }

  /** Shows units up to step without animation, hides the rest. */
  private settle(r: Rendered, step: number) {
    for (const u of r.units) {
      u.node.getAnimations().forEach((a) => a.cancel());
      u.node.classList.toggle("sk-pending", u.step > step);
    }
  }

  /** Runs "on appear" motion: stats count up, charts draw in. */
  private appear(r: Rendered, filter: (el: HTMLElement) => boolean) {
    const byId = new Map<string, Element>();
    for (const s of this.deck.slides) for (const els of Object.values(s.regions)) for (const e of els) byId.set(e.id, e);
    const m = { ...motionFor(document.documentElement, "slow"), reduced: prefersReducedMotion() };
    r.el.querySelectorAll<HTMLElement>(".sk-el-stat, .sk-el-chart").forEach((el) => {
      if (!filter(el)) return;
      if (el.classList.contains("sk-el-chart")) {
        const fig = el.querySelector<HTMLElement>(".sk-chart");
        if (fig) this.running.push(...animateChart(fig, motionFor(document.documentElement, "base").duration, m.easing, m.reduced));
        return;
      }
      const e = byId.get(el.dataset.elId ?? "");
      const v = el.querySelector<HTMLElement>(".sk-stat-value");
      if (e && v) countUp(v, m, (n) => formatStat(e, n));
    });
  }

  /** Resolves when the current slide's fonts, images, code and diagrams are done. */
  async ready(): Promise<void> {
    await this.busy;
    await themeReady(this.catalog.themes[this.deck.theme]);
    await Promise.all(this.current?.pending ?? []);
    if (this.current) fitCodeNow(this.current.el);
    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
  }

  private emit() {
    this.onChange(this.position);
  }

  /** Serialises navigation so rapid key presses never interleave transitions. */
  private queue(fn: () => Promise<void>): Promise<void> {
    this.finishRunning();
    this.busy = this.busy.then(fn, fn);
    return this.busy;
  }

  goto(index: number, step = 0, opts: { animate?: boolean; direction?: 1 | -1 } = {}): Promise<void> {
    return this.queue(async () => {
      if (!this.deck.slides.length) {
        this.stage.replaceChildren(emptyState());
        this.current = null;
        this.emit();
        return;
      }
      index = Math.max(0, Math.min(index, this.deck.slides.length - 1));
      const next = this.render(index)!;
      step = Math.max(0, Math.min(step, next.maxStep));
      this.settle(next, step);
      const from = this.current;
      const dir = opts.direction ?? (index >= this.index ? 1 : -1);
      const animate = opts.animate !== false && from !== null && index !== this.index;
      // Forward: the entering slide's transition. Back: the leaving slide's, reversed.
      const tSlide = dir === 1 ? this.deck.slides[index] : this.deck.slides[this.index] ?? this.deck.slides[index];
      const type: TransitionType = animate ? tSlide.transition?.type ?? "fade" : "none";
      const m = { ...motionFor(document.documentElement, tSlide.transition?.duration ?? "base"), reduced: prefersReducedMotion(), direction: dir };

      this.index = index;
      this.step = step;
      this.current = next;

      if (type === "morph" && from) {
        await this.morph(from, next, m);
      } else {
        const t = type === "morph" ? "fade" : type;
        this.stage.append(next.el);
        const anims = t === "none" || !from ? [] : transitions[t](from.el, next.el, m);
        this.running = anims;
        this.emit();
        await Promise.all(anims.map((a) => a.finished.catch(() => {})));
        from?.el.remove();
        for (const a of anims) a.cancel();
      }
      fitCode(next);
      // Remove any stray slides (e.g. after rapid navigation).
      for (const c of [...this.stage.children]) if (c !== next.el) c.remove();
      if (dir === 1) this.appear(next, (el) => !el.classList.contains("sk-pending"));
      this.emit();
    });
  }

  private async morph(from: Rendered, to: Rendered, m: { duration: number; easing: string; reduced: boolean }) {
    const doc = document as DocWithVT;
    if (!doc.startViewTransition && !m.reduced) {
      await this.flipMorph(from, to, m);
      return;
    }
    if (!doc.startViewTransition || m.reduced) {
      // Reduced motion: crossfade.
      this.stage.append(to.el);
      const anims = transitions.fade(from.el, to.el, m);
      this.emit();
      await Promise.all(anims.map((a) => a.finished.catch(() => {})));
      from.el.remove();
      anims.forEach((a) => a.cancel());
      return;
    }
    const name = (el: HTMLElement) => `sk-${el.dataset.elId}`;
    const tag = (r: Rendered) => r.el.querySelectorAll<HTMLElement>("[data-el-id]").forEach((el) => (el.style.viewTransitionName = name(el)));
    const untag = (r: Rendered) => r.el.querySelectorAll<HTMLElement>("[data-el-id]").forEach((el) => (el.style.viewTransitionName = ""));
    document.documentElement.style.setProperty("--sk-vt-dur", `${m.duration}ms`);
    tag(from);
    const vt = doc.startViewTransition(() => {
      from.el.remove();
      tag(to);
      this.stage.append(to.el);
      this.emit();
    });
    await vt.finished.catch(() => {});
    untag(to);
  }

  /**
   * Morph without View Transitions (FLIP): elements sharing an id animate from
   * their old box to their new one; the rest crossfade.
   */
  private async flipMorph(from: Rendered, to: Rendered, m: { duration: number; easing: string; reduced: boolean }) {
    const first = new Map<string, DOMRect>();
    from.el.querySelectorAll<HTMLElement>("[data-el-id]").forEach((el) => first.set(el.dataset.elId!, el.getBoundingClientRect()));
    if (from.el.parentNode === this.stage) this.stage.insertBefore(to.el, from.el); // old slide fades out on top, revealing the new one
    else this.stage.append(to.el);
    const scale = to.el.offsetWidth ? to.el.getBoundingClientRect().width / to.el.offsetWidth : 1;
    const opts: KeyframeAnimationOptions = { duration: m.duration, easing: m.easing, fill: "both" };
    const anims: Animation[] = [];
    to.el.querySelectorAll<HTMLElement>("[data-el-id]").forEach((el) => {
      const f = first.get(el.dataset.elId!);
      if (f) {
        anims.push(el.animate([{ transformOrigin: "0 0", transform: flipDelta(f, el.getBoundingClientRect(), scale) }, { transformOrigin: "0 0", transform: "none" }], opts));
        from.el.querySelector<HTMLElement>(`[data-el-id="${CSS.escape(el.dataset.elId!)}"]`)?.style.setProperty("visibility", "hidden");
      } else {
        anims.push(el.animate([{ opacity: 0 }, { opacity: 1 }], opts));
      }
    });
    anims.push(from.el.animate([{ opacity: 1 }, { opacity: 0 }], { ...opts, duration: m.duration * 0.6 }));
    this.emit();
    await Promise.all(anims.map((a) => a.finished.catch(() => {})));
    from.el.remove();
    anims.forEach((a) => a.cancel());
  }

  next(): Promise<void> {
    const r = this.current;
    if (r && this.step < r.maxStep) {
      this.step++;
      this.reveal(r, this.step);
      this.emit();
      return Promise.resolve();
    }
    if (this.index < this.deck.slides.length - 1) return this.goto(this.index + 1, 0, { direction: 1 });
    return Promise.resolve();
  }

  prev(): Promise<void> {
    const r = this.current;
    if (r && this.step > 0) {
      this.step--;
      this.settle(r, this.step);
      this.emit();
      return Promise.resolve();
    }
    if (this.index > 0) return this.goto(this.index - 1, Infinity, { direction: -1 });
    return Promise.resolve();
  }

  private reveal(r: Rendered, step: number) {
    this.finishRunning();
    const m = { ...motionFor(document.documentElement, "base", { spring: true }), reduced: prefersReducedMotion() };
    const units = r.units.filter((u) => u.step === step);
    const delays = m.reduced ? [] : staggerDelays(units.length); // units sharing a step enter one after another
    units.forEach((u, i) => {
      u.node.classList.remove("sk-pending");
      const anims = builds[u.preset](u.node, m);
      if (delays[i]) {
        for (const a of anims) {
          const t = a.effect?.getTiming();
          a.effect?.updateTiming({ delay: Number(t?.delay ?? 0) + delays[i] });
        }
      }
      this.running.push(...anims);
    });
    this.appear(r, (el) => units.some((u) => u.node === el));
  }

  /** Re-renders the current slide in place (after a live edit), keeping the build step. */
  refresh(): Promise<void> {
    return this.queue(async () => {
      if (!this.deck.slides.length) {
        this.stage.replaceChildren(emptyState());
        this.current = null;
        this.emit();
        return;
      }
      this.index = Math.max(0, Math.min(this.index, this.deck.slides.length - 1));
      const r = this.render(this.index)!;
      this.step = Math.min(this.step, r.maxStep);
      this.settle(r, this.step);
      this.stage.replaceChildren(r.el);
      this.current = r;
      fitCode(r);
      this.emit();
    });
  }

  /** Briefly highlights elements or slides that an edit touched. */
  flash(ids: string[]) {
    for (const id of ids) {
      this.stage.querySelectorAll<HTMLElement>(`[data-el-id="${CSS.escape(id)}"], [data-slide-id="${CSS.escape(id)}"]`).forEach((el) => {
        el.classList.remove("sk-flash");
        void el.offsetWidth;
        el.classList.add("sk-flash");
        el.addEventListener("animationend", () => el.classList.remove("sk-flash"), { once: true });
      });
    }
  }
}

function emptyState() {
  const d = document.createElement("div");
  d.className = "sk-empty";
  d.innerHTML = "<p>This deck has no slides yet.</p><p>Ask your agent to add some; they will appear here live.</p>";
  return d;
}

/** Shrinks code blocks whose longest line is wider than the block, now and after highlighting. */
function fitCode(r: Rendered) {
  fitCodeNow(r.el);
  Promise.all(r.pending).then(() => requestAnimationFrame(() => fitCodeNow(r.el)));
}

function fitCodeNow(root: HTMLElement) {
  root.querySelectorAll<HTMLElement>(".sk-el-code pre").forEach((pre) => {
    pre.style.fontSize = "";
    let size = parseFloat(getComputedStyle(pre).fontSize);
    for (let i = 0; i < 12 && pre.scrollWidth > pre.clientWidth + 1 && size > 16; i++) {
      size -= 2;
      pre.style.fontSize = `${size}px`;
    }
  });
}
