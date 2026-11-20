// Data charts as plain SVG (no chart library): bar, line and pie, coloured
// from theme tokens, with a draw-in animation when the chart appears.
import type { ColorToken, Element } from "./types";

const NS = "http://www.w3.org/2000/svg";
const PALETTE: ColorToken[] = ["accent", "accent2", "text", "muted", "surface"];
const W = 1000;
const H = 560;

interface Series {
  name?: string;
  values: number[];
  color?: ColorToken;
}

const svgEl = <K extends keyof SVGElementTagNameMap>(tag: K, attrs: Record<string, string | number> = {}) => {
  const n = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, String(v));
  return n;
};

const color = (s: Series, i: number) => `var(--sk-${s.color ?? PALETTE[i % PALETTE.length]})`;

function fmt(v: number, unit: string) {
  const n = Math.abs(v) >= 1000 ? v.toLocaleString(undefined, { maximumFractionDigits: 0 }) : v.toLocaleString(undefined, { maximumFractionDigits: 1 });
  return `${n}${unit}`;
}

/** Picks a round tick step (1, 2, 2.5 or 5 x 10^n) giving about four intervals. */
function niceStep(span: number) {
  if (span <= 0) return 1;
  const raw = span / 4;
  const p = Math.pow(10, Math.floor(Math.log10(raw)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= raw) return m * p;
  return 10 * p;
}

export function renderChart(e: Element): HTMLElement {
  const kind = (e.props.kind as string) ?? "bar";
  const labels = (e.props.labels as string[]) ?? [];
  const series = ((e.props.series as Series[]) ?? []).filter((s) => Array.isArray(s.values));
  const unit = (e.props.unit as string) ?? "";
  const showValues = Boolean(e.props.showValues);
  const caption = (e.props.caption as string) ?? "";

  const fig = document.createElement("figure");
  fig.className = `sk-chart sk-chart-${kind}`;
  const svg = svgEl("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", preserveAspectRatio: "xMidYMid meet" });
  svg.setAttribute("aria-label", caption || `${kind} chart`);
  fig.append(svg);

  if (kind === "pie") drawPie(svg, labels, series[0], unit, showValues);
  else drawXY(svg, kind, labels, series, unit, showValues);

  if (series.length > 1 || kind === "pie") {
    const legend = document.createElement("div");
    legend.className = "sk-chart-legend";
    const items = kind === "pie" ? labels.map((l, i) => ({ name: l, c: `var(--sk-${PALETTE[i % PALETTE.length]})` })) : series.map((s, i) => ({ name: s.name ?? `Series ${i + 1}`, c: color(s, i) }));
    for (const it of items) {
      const sp = document.createElement("span");
      const sw = document.createElement("i");
      sw.style.background = it.c;
      sp.append(sw, document.createTextNode(it.name));
      legend.append(sp);
    }
    fig.append(legend);
  }
  if (caption) {
    const fc = document.createElement("figcaption");
    fc.textContent = caption;
    fig.append(fc);
  }
  return fig;
}

function drawXY(svg: SVGSVGElement, kind: string, labels: string[], series: Series[], unit: string, showValues: boolean) {
  const padL = 90, padR = 20, padT = 30, padB = 60;
  const iw = W - padL - padR, ih = H - padT - padB;
  const all = series.flatMap((s) => s.values);
  const hi = Math.max(0, ...all);
  const lo = Math.min(0, ...all);
  const stepV = niceStep(hi - lo);
  const max = Math.ceil(hi / stepV) * stepV || stepV;
  const min = Math.floor(lo / stepV) * stepV;
  const ticks = Math.round((max - min) / stepV);
  const y = (v: number) => padT + ih - ((v - min) / (max - min || 1)) * ih;
  const n = labels.length;

  const grid = svgEl("g", { class: "sk-chart-grid" });
  for (let k = 0; k <= ticks; k++) {
    const v = min + stepV * k;
    const yy = y(v);
    grid.append(svgEl("line", { x1: padL, x2: W - padR, y1: yy, y2: yy }));
    const t = svgEl("text", { x: padL - 14, y: yy + 7, "text-anchor": "end", class: "sk-chart-axis" });
    t.textContent = fmt(v, unit);
    grid.append(t);
  }
  svg.append(grid);

  const band = iw / Math.max(1, n);
  labels.forEach((l, i) => {
    const t = svgEl("text", { x: padL + band * (i + 0.5), y: H - padB + 38, "text-anchor": "middle", class: "sk-chart-axis" });
    t.textContent = l;
    svg.append(t);
  });

  if (kind === "line") {
    series.forEach((s, si) => {
      const pts = s.values.slice(0, n).map((v, i) => [padL + band * (i + 0.5), y(v)] as const);
      const path = svgEl("path", {
        d: pts.map((p, i) => `${i ? "L" : "M"}${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(" "),
        class: "sk-chart-line sk-draw",
        stroke: color(s, si),
      });
      svg.append(path);
      pts.forEach((p, i) => {
        svg.append(svgEl("circle", { cx: p[0], cy: p[1], r: 7, fill: color(s, si), class: "sk-chart-dot sk-pop" }));
        if (showValues) {
          const t = svgEl("text", { x: p[0], y: p[1] - 18, "text-anchor": "middle", class: "sk-chart-value sk-pop" });
          t.textContent = fmt(s.values[i], unit);
          svg.append(t);
        }
      });
    });
    return;
  }

  // Grouped bars.
  const groupW = band * 0.72;
  const barW = groupW / Math.max(1, series.length);
  const zero = y(0);
  series.forEach((s, si) => {
    s.values.slice(0, n).forEach((v, i) => {
      const x = padL + band * i + (band - groupW) / 2 + si * barW;
      const top = Math.min(y(v), zero);
      const h = Math.abs(zero - y(v));
      const r = svgEl("rect", { x: x + 3, y: top, width: Math.max(1, barW - 6), height: Math.max(0.5, h), rx: 6, fill: color(s, si), class: "sk-chart-bar sk-grow" });
      r.style.transformOrigin = `0 ${zero}px`;
      svg.append(r);
      if (showValues) {
        const t = svgEl("text", { x: x + barW / 2, y: top - 12, "text-anchor": "middle", class: "sk-chart-value sk-pop" });
        t.textContent = fmt(v, unit);
        svg.append(t);
      }
    });
  });
}

function drawPie(svg: SVGSVGElement, labels: string[], s: Series | undefined, unit: string, showValues: boolean) {
  if (!s) return;
  const vals = s.values.slice(0, labels.length).map((v) => Math.max(0, v));
  const total = vals.reduce((a, b) => a + b, 0) || 1;
  const cx = W / 2, cy = H / 2, r = H / 2 - 20, inner = r * 0.56;
  let a0 = -Math.PI / 2;
  vals.forEach((v, i) => {
    const a1 = a0 + (v / total) * Math.PI * 2;
    const large = a1 - a0 > Math.PI ? 1 : 0;
    const p = (a: number, rr: number) => `${(cx + rr * Math.cos(a)).toFixed(2)},${(cy + rr * Math.sin(a)).toFixed(2)}`;
    const d = vals.length === 1
      ? `M${cx - r},${cy} a${r},${r} 0 1,0 ${2 * r},0 a${r},${r} 0 1,0 ${-2 * r},0 M${cx - inner},${cy} a${inner},${inner} 0 1,1 ${2 * inner},0 a${inner},${inner} 0 1,1 ${-2 * inner},0`
      : `M${p(a0, r)} A${r},${r} 0 ${large} 1 ${p(a1, r)} L${p(a1, inner)} A${inner},${inner} 0 ${large} 0 ${p(a0, inner)} Z`;
    const slice = svgEl("path", { d, fill: `var(--sk-${PALETTE[i % PALETTE.length]})`, class: "sk-chart-slice sk-sweep", "fill-rule": "evenodd" });
    slice.style.transformOrigin = `${cx}px ${cy}px`;
    slice.style.setProperty("--sk-i", String(i));
    svg.append(slice);
    if (showValues && v / total > 0.04) {
      const mid = (a0 + a1) / 2;
      const t = svgEl("text", { x: cx + ((r + inner) / 2) * Math.cos(mid), y: cy + ((r + inner) / 2) * Math.sin(mid) + 8, "text-anchor": "middle", class: "sk-chart-value sk-chart-on-fill sk-pop" });
      t.textContent = unit === "%" ? `${Math.round((v / total) * 100)}%` : fmt(v, unit);
      svg.append(t);
    }
    a0 = a1;
  });
}

/** Animates a chart in: bars grow, lines draw, slices sweep, labels pop. */
export function animateChart(fig: HTMLElement, duration: number, easing: string, reduced: boolean): Animation[] {
  if (reduced) return [];
  const out: Animation[] = [];
  const d = duration * 1.6;
  fig.querySelectorAll<SVGElement>(".sk-grow").forEach((el, i) =>
    out.push(el.animate([{ transform: "scaleY(0)" }, { transform: "scaleY(1)" }], { duration: d, delay: i * 40, easing, fill: "backwards" })),
  );
  fig.querySelectorAll<SVGPathElement>(".sk-draw").forEach((el) => {
    const len = el.getTotalLength();
    out.push(el.animate([{ strokeDasharray: `${len}`, strokeDashoffset: `${len}` }, { strokeDasharray: `${len}`, strokeDashoffset: "0" }], { duration: d * 1.2, easing, fill: "backwards" }));
  });
  fig.querySelectorAll<SVGElement>(".sk-sweep").forEach((el, i) =>
    out.push(el.animate([{ opacity: 0, transform: "rotate(-30deg) scale(0.8)" }, { opacity: 1, transform: "none" }], { duration: d, delay: i * 90, easing, fill: "backwards" })),
  );
  fig.querySelectorAll<SVGElement>(".sk-pop").forEach((el, i) =>
    out.push(el.animate([{ opacity: 0 }, { opacity: 1 }], { duration: duration, delay: d * 0.6 + i * 25, easing, fill: "backwards" })),
  );
  return out;
}
