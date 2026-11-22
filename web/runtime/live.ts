// Live slides: slides with the "live" layout whose content an agent changes
// while the deck is shown (update_live). The server sends each change as a
// "live" event; screens apply it to their copy of the deck and the player
// changes only that element in place.
import type { Deck, Element, Slide } from "./types";

/** A live slide's current content, as GET /api/decks/<id> returns it. */
export interface LiveEntry {
  regions: Slide["regions"];
  at: string;
}
export type LiveMap = Record<string, LiveEntry>;

export interface LiveEvent {
  type: "live";
  slideId: string;
  op: "set" | "add" | "remove";
  elementId?: string;
  props?: Record<string, unknown>;
  region?: string;
  index?: number;
  element?: Element;
  at: string;
  animate?: boolean;
}

export const isLiveEvent = (m: { type: string }): boolean => m.type === "live";

/** Puts live content in place of the saved regions; returns when each live slide last changed. */
export function applyLive(deck: Deck, live: LiveMap | undefined): Map<string, number> {
  const times = new Map<string, number>();
  for (const [id, entry] of Object.entries(live ?? {})) {
    const slide = deck.slides.find((s) => s.id === id);
    if (!slide || slide.layout !== "live") continue;
    slide.regions = entry.regions;
    times.set(id, Date.parse(entry.at));
  }
  return times;
}

const find = (slide: Slide, id: string): [string, number] | null => {
  for (const [r, els] of Object.entries(slide.regions)) {
    const i = els.findIndex((e) => e.id === id);
    if (i >= 0) return [r, i];
  }
  return null;
};

/** Applies one live event to the deck. Returns the element it set or added (or undefined). */
export function applyLiveEvent(deck: Deck, ev: LiveEvent): Element | undefined {
  const slide = deck.slides.find((s) => s.id === ev.slideId);
  if (!slide || slide.layout !== "live") return undefined;
  if (ev.op === "set" && ev.elementId && ev.props) {
    const at = find(slide, ev.elementId);
    if (!at) return undefined;
    const e = slide.regions[at[0]][at[1]];
    e.props = ev.props;
    return e;
  }
  if (ev.op === "add" && ev.region && ev.element) {
    const list = [...(slide.regions[ev.region] ?? [])];
    list.splice(Math.min(ev.index ?? list.length, list.length), 0, ev.element);
    slide.regions = { ...slide.regions, [ev.region]: list };
    return ev.element;
  }
  if (ev.op === "remove" && ev.elementId) {
    const at = find(slide, ev.elementId);
    if (at) slide.regions = { ...slide.regions, [at[0]]: slide.regions[at[0]].filter((e) => e.id !== ev.elementId) };
  }
  return undefined;
}

export const stampText = (at: number) =>
  `Updated ${new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}`;

/** The small last-updated time in a live slide's corner (null: none to show). */
export function liveStamp(slide: Slide, at: number | undefined): HTMLElement | null {
  if (slide.layout !== "live" || slide.hideUpdated || !at) return null;
  const el = document.createElement("div");
  el.className = "sk-live-stamp";
  el.textContent = stampText(at);
  return el;
}
