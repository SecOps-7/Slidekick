import { describe, expect, it } from "vitest";
import { ANGLE, BASE_BOTTOM, FONT, GAP, labelWidth, layoutLabels, leftExtra, MAX_BOTTOM, MAX_LEFT_EXTRA, thinned, type LabelLayout } from "./chartlabels";
import { renderChart } from "./chart";
import type { Element } from "./types";

// The chart's geometry (chart.ts): 1000 x 560 viewBox, plot from x 90 to 980.
const H = 560, LEFT = 90, PLOT = 890;
const band = (n: number) => PLOT / n;
const lay = (labels: string[], thin = false) => layoutLabels(labels, band(labels.length), LEFT, H, { thin });

/** No two shown labels overlap, nothing leaves the chart on the left, and angled labels fit the room below. */
function assertClear(l: LabelLayout, n: number) {
  const b = band(n);
  const rad = (l.angle * Math.PI) / 180;
  for (let j = 1; j < l.labels.length; j++) {
    const a = l.labels[j - 1], c = l.labels[j];
    const dist = (c.index - a.index) * b;
    if (l.angle === 0) expect(dist).toBeGreaterThanOrEqual((labelWidth(a.text) + labelWidth(c.text)) / 2);
    else expect(dist * Math.sin(rad)).toBeGreaterThanOrEqual(FONT); // parallel lines of text, a glyph apart at least
  }
  if (l.angle) {
    for (const p of l.labels) {
      const x = LEFT + b * (p.index + 0.5);
      expect(x - labelWidth(p.text) * Math.cos(rad)).toBeGreaterThanOrEqual(0);
    }
    expect(l.bottom).toBeLessThanOrEqual(Math.ceil(H * MAX_BOTTOM));
  }
  if (n > 0) {
    expect(l.labels[0].index).toBe(0);
    expect(l.labels[l.labels.length - 1].index).toBe(n - 1);
  }
}

describe("chart x-axis labels", () => {
  it("shows short labels as they are, level, with the usual room below", () => {
    for (const labels of [
      ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"],
      ["2019", "2020", "2021", "2022", "2023", "2024", "2025", "2026", "2027", "2028", "2029", "2030"],
      ["Q1", "Q2", "Q3", "Q4"],
      ["Anthropic", "OpenAI", "Google", "Meta", "Others"],
    ]) {
      const l = lay(labels);
      expect(l.mode).toBe("level");
      expect(l.labels.map((p) => p.text)).toEqual(labels);
      expect(l.bottom).toBe(BASE_BOTTOM);
      assertClear(l, labels.length);
    }
  });

  it("thins many long labels on a line chart, keeping the first and the last", () => {
    const times = Array.from({ length: 12 }, (_, i) => `13:${String(19 + i).padStart(2, "0")}:42`);
    const l = lay(times, true);
    expect(l.mode).toBe("thinned");
    expect(l.angle).toBe(0);
    expect(l.labels.length).toBeLessThan(12);
    expect(l.labels.length).toBeGreaterThanOrEqual(Math.ceil(12 / 3));
    assertClear(l, 12);
    expect(l.labels.every((p) => p.text === p.full)).toBe(true);
  });

  it("names every bar: a bar chart angles its labels instead of thinning them", () => {
    const times = Array.from({ length: 12 }, (_, i) => `13:${String(19 + i).padStart(2, "0")}:42`);
    const l = lay(times, false);
    expect(l.mode).toBe("angled");
    expect(l.labels).toHaveLength(12);
    assertClear(l, 12);
  });

  it("angles labels when thinning would hide too much, and makes room below", () => {
    const names = Array.from({ length: 8 }, (_, i) => `Regional office ${i + 1}`);
    const l = lay(names);
    expect(l.mode).toBe("angled");
    expect(l.angle).toBe(ANGLE);
    expect(l.bottom).toBeGreaterThan(BASE_BOTTOM);
    expect(l.labels).toHaveLength(8); // angled, every label still fits
    assertClear(l, 8);
  });

  it("angles two labels that don't fit level (there is nothing to thin)", () => {
    const l = lay(["A very long first category name here", "And an even longer last category name"]);
    expect(l.mode).toBe("angled");
    assertClear(l, 2);
  });

  it("shortens what is still too long, keeping the full text", () => {
    const long = "An extraordinarily long category name that could never fit under any chart at all";
    const l = lay([long, long + " two", long + " three"]);
    expect(l.mode).toBe("angled");
    expect(l.shortened).toBe(true);
    for (const p of l.labels) {
      expect(p.text.endsWith("…")).toBe(true);
      expect(p.full.startsWith("An extraordinarily")).toBe(true);
    }
    assertClear(l, 3);
  });

  it("thins angled labels as far as needed when there are very many", () => {
    const many = Array.from({ length: 60 }, (_, i) => `Item number ${i + 1}`);
    const l = lay(many);
    expect(l.mode).toBe("angled");
    expect(l.step).toBeGreaterThan(1);
    assertClear(l, 60);
  });

  it("never overlaps, for any count and length", () => {
    for (let n = 1; n <= 40; n += 3) {
      for (const len of [1, 4, 8, 14, 30, 70]) {
        const labels = Array.from({ length: n }, (_, i) => `${"W".repeat(Math.max(0, len - String(i).length))}${i}`);
        assertClear(lay(labels, false), n);
        assertClear(lay(labels, true), n);
      }
    }
  });

  it("widens the left margin so the first angled label isn't cut off", () => {
    const labels = ["North America retail", "Europe and Middle East", "Asia Pacific partners", "Latin America direct", "Online marketplace", "Enterprise accounts"];
    const l = lay(labels);
    expect(l.labels[0].text).toMatch(/…$/); // at the usual margin it would be shortened
    const extra = leftExtra(l, labels, band(labels.length), LEFT, H);
    expect(extra).toBeGreaterThan(0);
    expect(extra).toBeLessThanOrEqual(MAX_LEFT_EXTRA);
    const wider = layoutLabels(labels, (PLOT - extra) / labels.length, LEFT + extra, H);
    expect(wider.labels[0].text).toBe(labels[0]);
    expect(leftExtra(lay(["Jan", "Feb"]), ["Jan", "Feb"], band(2), LEFT, H)).toBe(0);
  });

  it("thinning keeps the first and last and never bunches them", () => {
    expect(thinned(12, 2)).toEqual([0, 2, 4, 6, 8, 11]);
    expect(thinned(13, 3)).toEqual([0, 3, 6, 9, 12]);
    expect(thinned(2, 3)).toEqual([0, 1]);
    expect(thinned(0, 2)).toEqual([]);
    expect(GAP).toBeGreaterThan(0);
  });

  it("puts labels in the same places whatever the values", () => {
    const labels = Array.from({ length: 12 }, (_, i) => `13:${String(19 + i).padStart(2, "0")}:42`);
    const chart = (values: number[]) =>
      renderChart({ id: "e_c", type: "chart", props: { kind: "line", labels, series: [{ values }] } } as unknown as Element);
    const spots = (fig: HTMLElement) => [...fig.querySelectorAll(".sk-chart-xlabels text")].map((t) => `${t.getAttribute("x")},${t.getAttribute("y")},${t.textContent}`);
    const a = spots(chart(labels.map((_, i) => i)));
    const b = spots(chart(labels.map((_, i) => 1000 - i * 37)));
    expect(a.length).toBeGreaterThan(0);
    expect(b).toEqual(a);
  });

  it("keeps hidden and shortened labels for screen readers and hover", () => {
    const long = "An extraordinarily long category name that could never fit under any chart at all";
    const fig = renderChart({ id: "e_c", type: "chart", props: { kind: "bar", labels: [long, "B"], series: [{ values: [1, 2] }], caption: "Sales" } } as unknown as Element);
    const svg = fig.querySelector("svg")!;
    expect(svg.getAttribute("aria-label")).toContain(long);
    expect(fig.querySelector(".sk-chart-xlabels title")?.textContent).toBe(long);
    // Labels that fit leave the accessible name as it was.
    const plain = renderChart({ id: "e_d", type: "chart", props: { kind: "bar", labels: ["A", "B"], series: [{ values: [1, 2] }], caption: "Sales" } } as unknown as Element);
    expect(plain.querySelector("svg")!.getAttribute("aria-label")).toBe("Sales");
  });
});
