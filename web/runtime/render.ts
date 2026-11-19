// Renders a slide from deck JSON into DOM. No animation here: the player
// decides what is visible and how it moves.
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
}

const loadedThemes = new Set<string>();

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
  if (!loadedThemes.has(theme.name)) {
    loadedThemes.add(theme.name);
    const css = document.createElement("link");
    css.rel = "stylesheet";
    css.href = `/themes/${encodeURIComponent(theme.name)}/theme.css`;
    document.head.append(css);
    if (t.fontImport) {
      const f = document.createElement("link");
      f.rel = "stylesheet";
      f.href = t.fontImport;
      document.head.append(f);
    }
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

function renderElement(e: Element, units: BuildUnit[]): HTMLElement {
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
  const names = [...regionOrder, ...Object.keys(slide.regions).filter((r) => !regionOrder.includes(r)).sort()];
  for (const r of names) {
    const items = slide.regions[r];
    if (!items) continue;
    const region = h("div", `sk-region sk-region-${r}`);
    if (slide.layout === "grid-3" && ["a", "b", "c"].includes(r)) region.classList.add("sk-card");
    region.style.gridArea = r;
    for (const e of items) region.append(renderElement(e, units));
    el.append(region);
  }
  const maxStep = units.reduce((m, u) => Math.max(m, u.step), 0);
  return { el, units, maxStep };
}
