// X-axis label layout for bar and line charts. Labels sit centred under
// their slot, `band` units apart, in the chart's 1000-unit-wide viewBox (the
// whole chart scales together, so this doesn't depend on the slide's size or
// aspect). Clearest option that fits, in order:
//
//   1. every label, level;
//   2. line charts only: every 2nd or 3rd label (FIT_MAX_STEP), always
//      keeping the first and the last, level. A line's points are a sequence
//      (times, dates), so the hidden labels are implied by their neighbours;
//      a bar chart's bars are separate categories and each keeps its name;
//   3. labels angled at ANGLE degrees, thinned only as far as rotated
//      neighbours need, with the plot raised to make room below it (up to
//      MAX_BOTTOM of the chart's height);
//   4. a label still too long for that room is shortened with an ellipsis;
//      the full text stays in a <title> (hover) and the chart's accessible
//      label.
//
// Widths are estimated, not measured: the layout must be the same in the
// editor, every screen and every export, whenever the fonts happen to load,
// and must depend only on the labels (so changing values never moves them).
// The estimate is generous on purpose: every character counts as at least a
// monospace character (two themes use a monospace body font), and capitals
// and wide letters count wider, so labels never overlap in any theme.

export const FONT = 26; // .sk-chart-axis font size, in viewBox units
export const GAP = 0.35 * FONT; // least clear space between level labels
export const FIT_MAX_STEP = 3; // thin to every 3rd label at most before angling
export const ANGLE = 35; // degrees
export const LINE = 1.25 * FONT; // least distance between parallel angled labels
export const BASE_BOTTOM = 60; // room under the plot for level labels
export const MAX_BOTTOM = 0.45; // of the chart's height, for angled labels
const BELOW = 22; // from the axis down to an angled label's anchor
export const MAX_LEFT_EXTRA = 170; // extra left margin for the first angled label

const em = (c: string): number => {
  if ("MWmw@%&".includes(c)) return 0.92;
  if (c >= "A" && c <= "Z") return 0.72;
  return 0.6; // a monospace character: the floor for everything else
};

/** Estimated width of a label, in viewBox units. */
export function labelWidth(s: string, font = FONT): number {
  let w = 0;
  for (const c of s) w += em(c);
  return w * font;
}

/** Shortens s with an ellipsis to at most max units wide (null: nothing would fit). */
export function shorten(s: string, max: number, font = FONT): string | null {
  if (labelWidth(s, font) <= max) return s;
  const chars = [...s];
  for (let k = chars.length - 1; k > 0; k--) {
    const t = chars.slice(0, k).join("").trimEnd() + "…";
    if (labelWidth(t, font) <= max) return t;
  }
  return null;
}

export interface PlacedLabel {
  index: number;
  text: string; // what is drawn (may be shortened)
  full: string; // the label as given
}

export interface LabelLayout {
  mode: "level" | "thinned" | "angled";
  angle: number; // 0 or ANGLE
  step: number; // 1: every label
  labels: PlacedLabel[];
  bottom: number; // room under the plot, viewBox units
  shortened: boolean;
}

/** Indices shown with a step: 0, step, 2·step…, always the last; the one before the last goes when they'd be closer than step. */
export function thinned(n: number, step: number): number[] {
  if (n <= 0) return [];
  if (step <= 1 || n <= 2) return [...Array(n).keys()];
  const out: number[] = [];
  for (let i = 0; i < n; i += step) out.push(i);
  if (out[out.length - 1] !== n - 1) {
    if (n - 1 - out[out.length - 1] < step && out.length > 1) out.pop();
    out.push(n - 1);
  }
  return out;
}

/** Whether level labels at these indices clear each other. */
function levelFits(widths: number[], idx: number[], band: number): boolean {
  for (let j = 1; j < idx.length; j++) {
    const a = idx[j - 1], b = idx[j];
    if ((b - a) * band < (widths[a] + widths[b]) / 2 + GAP) return false;
  }
  return true;
}

/**
 * How much wider the chart's left margin should be so the first label, angled,
 * isn't cut off by the chart's edge (it hangs down and to the left of its
 * slot), up to MAX_LEFT_EXTRA. 0 when the labels aren't angled.
 */
export function leftExtra(layout: LabelLayout, labels: string[], band: number, left: number, height: number): number {
  if (!layout.angle || !labels.length) return 0;
  const rad = (ANGLE * Math.PI) / 180;
  const sin = Math.sin(rad), cos = Math.cos(rad);
  const maxLen = (height * MAX_BOTTOM - BELOW - FONT * cos) / sin;
  const need = Math.min(labelWidth(labels[0]), maxLen) * cos - (left + band / 2) + 4;
  // Widening the margin by e also narrows every slot by e/n, moving the first
  // slot's centre by only e·(1 - 1/2n): solve for that.
  const n = labels.length;
  return Math.max(0, Math.min(MAX_LEFT_EXTRA, Math.ceil(need / (1 - 1 / (2 * n)))));
}

/**
 * Lays out x-axis labels: band is the slot width, left the x of the plot's
 * left edge, height the chart's height (all viewBox units).
 */
export function layoutLabels(labels: string[], band: number, left: number, height: number, opts: { thin?: boolean } = {}): LabelLayout {
  const n = labels.length;
  const widths = labels.map((l) => labelWidth(l));
  const place = (idx: number[]): PlacedLabel[] => idx.map((i) => ({ index: i, text: labels[i], full: labels[i] }));

  // 1 and 2: level, every label or thinned (first and last always kept).
  for (let step = 1; step <= (opts.thin ? FIT_MAX_STEP : 1); step++) {
    const idx = thinned(n, step);
    if (step > 1 && idx.length === n) break; // too few labels to thin
    if (levelFits(widths, idx, band)) {
      return { mode: step === 1 ? "level" : "thinned", angle: 0, step, labels: place(idx), bottom: BASE_BOTTOM, shortened: false };
    }
  }

  // 3: angled. Thin only as far as parallel labels need.
  const rad = (ANGLE * Math.PI) / 180;
  const sin = Math.sin(rad), cos = Math.cos(rad);
  const step = Math.max(1, Math.ceil(LINE / (band * sin)));
  const idx = thinned(n, step);
  // Room below: the longest label's drop, capped; beyond that, 4: shorten.
  const maxBottom = height * MAX_BOTTOM;
  const maxLen = (maxBottom - BELOW - FONT * cos) / sin;
  let shortened = false;
  const placed = idx.map((i) => {
    const x = left + band * (i + 0.5);
    // A label hangs down and to the left of its slot: keep it on the chart.
    const room = Math.min(maxLen, (x - 4) / cos);
    const text = shorten(labels[i], room) ?? "…";
    if (text !== labels[i]) shortened = true;
    return { index: i, text, full: labels[i] };
  });
  const longest = Math.max(0, ...placed.map((p) => labelWidth(p.text)));
  const bottom = Math.max(BASE_BOTTOM, Math.ceil(BELOW + longest * sin + FONT * cos + 6));
  return { mode: "angled", angle: ANGLE, step, labels: placed, bottom: Math.min(bottom, Math.ceil(maxBottom)), shortened };
}
