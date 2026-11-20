// Renders a slide from deck JSON into DOM. No animation here: the player
// decides what is visible and how it moves.
import { renderChart } from "./chart";
import { highlight } from "./code";
import { renderDiagram } from "./diagram";
import { ICONS } from "./icons.gen";
import { inline } from "./inline";
import type { BuildPreset, Deck, Element, Slide, Theme } from "./types";

export const STAGE = { "16:9": [1920, 1080], "4:3": [1440, 1080] } as const;

export interface BuildUnit {
  node: HTMLElement;
  step: number;
  preset: BuildPreset;
  elementId: string;
}

export interface Rendered {
  el: HTMLElement;
  units: BuildUnit[];
  maxStep: number;
  /** Async work (highlighting, diagrams, image decode) the slide waits on before it is "ready". */
  pending: Promise<unknown>[];
}

const loadedThemes = new Set<string>();
const themeLoads: Promise<unknown>[] = [];

const linkLoaded = (l: HTMLLinkElement) =>
  new Promise<void>((res) => {
    l.addEventListener("load", () => res(), { once: true });
    l.addEventListener("error", () => res(), { once: true });
  });

/** Resolves when theme stylesheets have loaded and the theme's fonts are usable. */
export async function themeReady(theme: Theme | undefined): Promise<void> {
  await Promise.all(themeLoads);
  if (!theme) return;
  const f = theme.tokens.font;
  await Promise.all(
    [`700 64px ${f.heading}`, `400 32px ${f.body}`, `400 24px ${f.mono}`].map((spec) => document.fonts.load(spec).catch(() => [])),
  );
  await document.fonts.ready;
}

/** Applies theme tokens as CSS variables on root and loads the theme's CSS and fonts once. */
export function applyTheme(root: HTMLElement, theme: Theme) {
  const t = theme.tokens;
  for (const [k, v] of Object.entries(t.color)) root.style.setProperty(`--sk-${k}`, v);
  root.style.setProperty("--sk-font-heading", t.font.heading);
  root.style.setProperty("--sk-font-body", t.font.body);
  root.style.setProperty("--sk-font-mono", t.font.mono);
  for (const [k, v] of Object.entries(t.size)) root.style.setProperty(`--sk-size-${k}`, `${v}px`);
  root.style.setProperty("--sk-radius", `${t.radius}px`);
  root.style.setProperty("--sk-ease", t.motion.easing);
  root.style.setProperty("--sk-dur-fast", `${t.motion.fast}ms`);
  root.style.setProperty("--sk-dur-base", `${t.motion.base}ms`);
  root.style.setProperty("--sk-dur-slow", `${t.motion.slow}ms`);
  for (const c of [...root.classList]) if (c.startsWith("sk-theme-")) root.classList.remove(c);
  root.classList.add(`sk-theme-${theme.name}`);
  root.dataset.dark = String(theme.dark);
  const inlined = document.querySelector(`style[data-sk-theme="${CSS.escape(theme.name)}"]`);
  if (!loadedThemes.has(theme.name) && !inlined) {
    loadedThemes.add(theme.name);
    const css = document.createElement("link");
    css.rel = "stylesheet";
    css.href = `/themes/${encodeURIComponent(theme.name)}/theme.css`;
    themeLoads.push(linkLoaded(css));
    document.head.append(css);
  }
}

function h<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, html?: string): HTMLElementTagNameMap[K] {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (html !== undefined) n.innerHTML = html;
  return n;
}

const str = (e: Element, k: string) => (typeof e.props[k] === "string" ? (e.props[k] as string) : "");

export function formatStat(e: Element, value: number): string {
  const d = typeof e.props.decimals === "number" ? (e.props.decimals as number) : 0;
  const n = value.toLocaleString(undefined, { minimumFractionDigits: d, maximumFractionDigits: d });
  return `${str(e, "prefix")}${n}${str(e, "suffix")}`;
}

const SVG_ATTRS = 'viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"';

function renderElement(e: Element, units: BuildUnit[], pending: Promise<unknown>[]): HTMLElement {
  const wrap = h("div", `sk-el sk-el-${e.type}`);
  wrap.dataset.elId = e.id;
  wrap.dataset.type = e.type;
  const s = e.style;
  if (s?.size) wrap.style.setProperty("--sk-fs", `var(--sk-size-${s.size})`);
  if (s?.color) wrap.style.color = `var(--sk-${s.color})`;
  if (s?.colorRaw) wrap.style.color = s.colorRaw;
  if (s?.align) wrap.style.textAlign = s.align;
  if (s?.weight) wrap.style.fontWeight = s.weight === "bold" ? "700" : "400";

  switch (e.type) {
    case "heading": {
      const level = [1, 2, 3].includes(e.props.level as number) ? (e.props.level as number) : 1;
      wrap.classList.add(`sk-h${level}`);
      wrap.append(h(`h${level}` as "h1", undefined, inline(str(e, "text"))));
      break;
    }
    case "text":
      wrap.append(h("p", undefined, inline(str(e, "text"))));
      break;
    case "quote": {
      const bq = h("blockquote", undefined, `<p>${inline(str(e, "text"))}</p>`);
      wrap.append(bq);
      const a = str(e, "attribution");
      if (a) {
        const cite = h("cite");
        cite.textContent = a;
        wrap.append(cite);
      }
      break;
    }
    case "bullets": {
      const list = h(e.props.ordered ? "ol" : "ul");
      const items = Array.isArray(e.props.items) ? (e.props.items as string[]) : [];
      items.forEach((it, i) => {
        const li = h("li", undefined, inline(String(it)));
        if (e.props.buildEach && e.build) {
          units.push({ node: li, step: e.build.step + i, preset: e.build.preset ?? "fade-up", elementId: e.id });
        }
        list.append(li);
      });
      wrap.append(list);
      break;
    }
    case "image": {
      const fig = h("figure");
      const img = h("img");
      img.src = str(e, "src");
      img.alt = e.props.decorative ? "" : str(e, "alt");
      img.loading = "eager";
      img.decoding = "async";
      img.style.objectFit = str(e, "fit") || "cover";
      pending.push(img.decode().catch(() => {}));
      fig.append(img);
      const cap = str(e, "caption");
      if (cap) {
        const fc = h("figcaption");
        fc.textContent = cap;
        fig.append(fc);
      }
      wrap.append(fig);
      break;
    }
    case "code": {
      const pre = h("pre");
      const code = h("code");
      code.textContent = str(e, "code");
      const lang = str(e, "language");
      if (lang) pre.dataset.lang = lang;
      pre.append(code);
      wrap.append(pre);
      if (lang) pending.push(highlight(code, str(e, "code"), lang));
      break;
    }
    case "chart":
      wrap.append(renderChart(e));
      break;
    case "diagram": {
      const fig = h("figure", "sk-diagram");
      const host = h("div", "sk-diagram-svg");
      fig.append(host);
      const cap = str(e, "caption");
      if (cap) {
        host.setAttribute("role", "img");
        host.setAttribute("aria-label", cap);
        const fc = h("figcaption");
        fc.textContent = cap;
        fig.append(fc);
      }
      wrap.append(fig);
      pending.push(renderDiagram(host, str(e, "source")));
      break;
    }
    case "icon": {
      const body = Object.hasOwn(ICONS, str(e, "name")) ? ICONS[str(e, "name")] : undefined;
      if (!body) break;
      const label = str(e, "label");
      const svg = `<svg ${SVG_ATTRS} ${label ? `role="img" aria-label="${label.replace(/[&"<>]/g, "")}"` : 'aria-hidden="true"'}>${body}</svg>`;
      wrap.append(h("span", "sk-icon", svg));
      break;
    }
    case "shape": {
      const kind = str(e, "kind") || "rect";
      wrap.classList.add(`sk-shape-${kind}`);
      const fill = str(e, "fill") || "accent";
      wrap.style.setProperty("--sk-shape-fill", `var(--sk-${fill})`);
      if (kind === "line" || kind === "arrow") {
        wrap.innerHTML = `<svg viewBox="0 0 400 24" preserveAspectRatio="none" aria-hidden="true"><line x1="2" y1="12" x2="${kind === "arrow" ? 380 : 398}" y2="12" />${kind === "arrow" ? '<path d="M372 2 L398 12 L372 22 Z" />' : ""}</svg>`;
      } else {
        const label = str(e, "label");
        const box = h("div", "sk-shape", label ? `<span>${inline(label)}</span>` : undefined);
        wrap.append(box);
      }
      break;
    }
    case "embed": {
      const src = str(e, "src");
      const uploaded = /^\/files\/f_[0-9a-f]{20}\.[a-z0-9]+$/.test(src);
      if (!uploaded && !/^https:\/\//.test(src) && !src.startsWith("data:")) break;
      if (str(e, "kind") === "video") {
        const v = h("video");
        v.src = src;
        v.playsInline = true;
        v.controls = !e.props.autoplay;
        v.autoplay = Boolean(e.props.autoplay);
        v.loop = Boolean(e.props.loop);
        v.muted = Boolean(e.props.muted ?? e.props.autoplay);
        const poster = str(e, "poster");
        if (poster) v.poster = poster;
        if (str(e, "title")) v.setAttribute("aria-label", str(e, "title"));
        wrap.append(v);
      } else {
        const f = h("iframe");
        f.src = src;
        f.title = str(e, "title") || "Embedded content";
        f.loading = "eager";
        f.allow = "autoplay; fullscreen; picture-in-picture; encrypted-media";
        f.referrerPolicy = "strict-origin-when-cross-origin";
        // Third-party pages keep their own origin; our own files never get same-origin powers.
        f.setAttribute("sandbox", uploaded ? "allow-presentation" : "allow-scripts allow-same-origin allow-presentation allow-popups");
        wrap.append(f);
      }
      break;
    }
    case "stat": {
      const v = h("div", "sk-stat-value");
      const value = typeof e.props.value === "number" ? (e.props.value as number) : 0;
      v.textContent = formatStat(e, value);
      v.dataset.value = String(value);
      wrap.append(v);
      const label = str(e, "label");
      if (label) {
        const l = h("div", "sk-stat-label");
        l.textContent = label;
        wrap.append(l);
      }
      break;
    }
  }
  const eachItems = e.type === "bullets" && e.props.buildEach;
  if (e.build && !eachItems) {
    units.push({ node: wrap, step: e.build.step, preset: e.build.preset ?? "fade-up", elementId: e.id });
  }
  return wrap;
}

/** Renders one slide. regionOrder lists the layout's regions in display order. */
export function renderSlide(_deck: Deck, slide: Slide, regionOrder: string[]): Rendered {
  const el = h("section", `sk-slide sk-layout-${slide.layout}`);
  el.dataset.slideId = slide.id;
  el.setAttribute("aria-roledescription", "slide");
  const bg = slide.background;
  if (bg?.token) el.style.background = `var(--sk-${bg.token})`;
  if (bg?.image) {
    el.dataset.bgImage = "";
    const dim = bg.dim ?? 0;
    el.style.backgroundImage = `linear-gradient(rgba(0,0,0,${dim}), rgba(0,0,0,${dim})), url(${JSON.stringify(bg.image)})`;
    el.style.backgroundSize = "cover";
    el.style.backgroundPosition = "center";
  }
  const units: BuildUnit[] = [];
  const pending: Promise<unknown>[] = [];
  const names = [...regionOrder, ...Object.keys(slide.regions).filter((r) => !regionOrder.includes(r)).sort()];
  for (const r of names) {
    const items = slide.regions[r];
    if (!items) continue;
    const region = h("div", `sk-region sk-region-${r}`);
    if (slide.layout === "grid-3" && ["a", "b", "c"].includes(r)) region.classList.add("sk-card");
    region.style.gridArea = r;
    for (const e of items) region.append(renderElement(e, units, pending));
    el.append(region);
  }
  const maxStep = units.reduce((m, u) => Math.max(m, u.step), 0);
  return { el, units, maxStep, pending };
}
