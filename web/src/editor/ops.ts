// Turns editor actions into JSON Patch operations against the deck.
import type { Deck, Element, ElementProps, PatchOp, Slide } from "../../runtime/types";

export const esc = (s: string) => s.replace(/~/g, "~0").replace(/\//g, "~1");

export interface ElLoc {
  slideIndex: number;
  region: string;
  index: number;
  el: Element;
}

export function findEl(deck: Deck, slideId: string, elId: string): ElLoc | null {
  const slideIndex = deck.slides.findIndex((s) => s.id === slideId);
  if (slideIndex < 0) return null;
  for (const [region, els] of Object.entries(deck.slides[slideIndex].regions)) {
    const index = els.findIndex((e) => e.id === elId);
    if (index >= 0) return { slideIndex, region, index, el: els[index] };
  }
  return null;
}

export const slidePath = (i: number) => `/slides/${i}`;
export const elPath = (l: ElLoc) => `/slides/${l.slideIndex}/regions/${esc(l.region)}/${l.index}`;

/** Sets (or removes, for undefined/"") one key of an object at path. */
export function setKey(path: string, obj: Record<string, unknown> | undefined, key: string, value: unknown): PatchOp[] {
  const empty = value === undefined || value === "" || value === null;
  if (empty) return obj && key in obj ? [{ op: "remove", path: `${path}/${esc(key)}` }] : [];
  if (!obj) return [{ op: "add", path, value: { [key]: value } }];
  return [{ op: "add", path: `${path}/${esc(key)}`, value }];
}

/** Sets a key in an optional sub-object (style, build, transition, background), dropping it when it becomes empty. */
export function setSub(parentPath: string, parent: Record<string, unknown>, sub: string, key: string, value: unknown): PatchOp[] {
  const cur = parent[sub] as Record<string, unknown> | undefined;
  const next = { ...(cur ?? {}) };
  if (value === undefined || value === "" || value === null) delete next[key];
  else next[key] = value;
  const path = `${parentPath}/${sub}`;
  if (Object.keys(next).length === 0) return cur ? [{ op: "remove", path }] : [];
  return [{ op: "add", path, value: next }];
}

export const setProp = (l: ElLoc, key: string, value: unknown) => setKey(`${elPath(l)}/props`, l.el.props, key, value);

/** A copy without ids (the server assigns fresh ones). */
export function stripIds<T>(v: T): T {
  if (Array.isArray(v)) return v.map(stripIds) as T;
  if (v && typeof v === "object") {
    const o: Record<string, unknown> = {};
    for (const [k, x] of Object.entries(v)) if (k !== "id") o[k] = k === "props" ? structuredClone(x) : stripIds(x);
    return o as T;
  }
  return v;
}

const PLACEHOLDER_IMG =
  "data:image/svg+xml," +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 9"><rect width="16" height="9" fill="#8885"/><path d="M5 6.5l2-2.5 1.5 1.8L10 4l2 2.5z" fill="#8889"/></svg>',
  );

// Typed against the schema's props, so a default that the server would reject fails to compile.
export const ELEMENT_DEFAULTS: { [K in keyof ElementProps]: ElementProps[K] } = {
  heading: { text: "New heading", level: 2 },
  text: { text: "Say one thing clearly." },
  bullets: { items: ["First point", "Second point", "Third point"] },
  image: { src: PLACEHOLDER_IMG, alt: "Placeholder image" },
  code: { code: "const answer = 42;", language: "typescript" },
  stat: { value: 42, suffix: "%", label: "what the number means" },
  quote: { text: "A line worth quoting.", attribution: "Someone" },
  chart: { kind: "bar", labels: ["A", "B", "C"], series: [{ name: "Series", values: [3, 5, 4] }], caption: "What the chart shows" },
  diagram: { source: "flowchart LR\n  A[Start] --> B[Finish]", caption: "What the diagram shows" },
  icon: { name: "star" },
  shape: { kind: "pill", fill: "accent", label: "Label" },
  html: { title: "Custom badge", html: '<div style="display:grid;place-items:center;height:100%"><span style="padding:.4em 1em;border-radius:999px;background:var(--sk-accent);color:var(--sk-bg)">Custom HTML</span></div>' },
  embed: { kind: "iframe", src: "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ", title: "Embedded video" },
};

export function newElement(type: Element["type"]): Omit<Element, "id"> {
  return { type, props: structuredClone(ELEMENT_DEFAULTS[type]) as unknown as Element["props"] };
}

const h = (text: string, level = 2) => ({ type: "heading", props: { text, level } });
const t = (text: string) => ({ type: "text", props: { text } });

/** A sensible starting slide for each layout. */
export function slideTemplate(layout: string): Omit<Slide, "id"> {
  const regions: Record<string, unknown[]> = {
    title: { title: [h("Section title", 1)], subtitle: [t("A short subtitle")] },
    "title-body": { title: [h("Headline that makes a claim")], body: [newElement("bullets")] },
    "two-column": { title: [h("Compare two things")], left: [t("Left side")], right: [t("Right side")] },
    "hero-image": { image: [newElement("image")], title: [h("A big statement", 1)] },
    "big-number": { number: [newElement("stat")], caption: [t("Why this number matters")] },
    quote: { quote: [newElement("quote")] },
    "grid-3": {
      title: [h("Three pillars")],
      a: [{ type: "icon", props: { name: "target" } }, h("One", 3), t("Short detail")],
      b: [{ type: "icon", props: { name: "users" } }, h("Two", 3), t("Short detail")],
      c: [{ type: "icon", props: { name: "bolt" } }, h("Three", 3), t("Short detail")],
    },
    "full-bleed": { content: [h("Anything goes", 1)] },
    live: {
      title: [h("Network right now")],
      figure: [{ type: "stat", props: { value: 0, suffix: " Mbps", label: "Download" } }],
      chart: [{ type: "chart", props: { kind: "line", labels: ["-4m", "-3m", "-2m", "-1m", "now"], series: [{ name: "Mbps", values: [0, 0, 0, 0, 0] }] } }],
      status: [t("Waiting for the first update")],
    },
  }[layout] ?? { title: [h("New slide")] };
  return { layout, regions: regions as Slide["regions"], transition: { type: "fade" } };
}

/** Moves elements into the regions of a new layout, keeping same-named regions. */
export function remapRegions(slide: Slide, newRegions: string[]): Slide["regions"] {
  const out: Slide["regions"] = {};
  const fallback = newRegions.find((r) => r !== "title") ?? newRegions[0];
  for (const [r, els] of Object.entries(slide.regions)) {
    const target = newRegions.includes(r) ? r : fallback;
    out[target] = [...(out[target] ?? []), ...els];
  }
  return out;
}

export function describe(e: Element): string {
  const p = e.props as Record<string, unknown>;
  const s = (p.text as string) ?? (p.label as string) ?? (p.caption as string) ?? (p.name as string) ?? (Array.isArray(p.items) ? (p.items as string[])[0] : "") ?? "";
  const clean = String(s).replace(/\*\*|\*|`/g, "");
  return clean.length > 28 ? clean.slice(0, 27) + "…" : clean || e.type;
}
