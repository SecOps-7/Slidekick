// Animation presets. Each preset is a pure function (element, options) =>
// Animation[] built on the Web Animations API, so presets are easy to test
// and to add. Durations and easing come from theme motion tokens.
import { springEasing } from "./motion";
import type { BuildPreset, Duration, TransitionType } from "./types";

export interface MotionOpts {
  duration: number;
  easing: string;
  reduced: boolean;
  direction?: 1 | -1;
}

/** Headless renders and exports can force instant motion with window.__skReduced = true. */
export const prefersReducedMotion = () =>
  (window as { __skReduced?: boolean }).__skReduced === true || matchMedia("(prefers-reduced-motion: reduce)").matches;

/**
 * Theme motion for one duration token. With spring set and a theme bounce
 * above zero, the easing is a Motion spring (as CSS linear()) instead.
 */
export function motionFor(root: HTMLElement, d: Duration = "base", opts: { spring?: boolean } = {}): { duration: number; easing: string } {
  const cs = getComputedStyle(root);
  const ms = parseFloat(cs.getPropertyValue(`--sk-dur-${d}`)) || 500;
  const base = { duration: ms, easing: cs.getPropertyValue("--sk-ease").trim() || "ease" };
  const bounce = parseFloat(cs.getPropertyValue("--sk-bounce"));
  if (opts.spring && bounce > 0) return springEasing(Math.min(1, bounce), ms) ?? base;
  return base;
}

const SVG_STROKES = "path, line, polyline, polygon, circle, ellipse, rect";

/** Length of an SVG shape's outline (0 where the browser can't measure it). */
function strokeLength(s: SVGGeometryElement): number {
  try {
    return s.getTotalLength();
  } catch {
    return 0;
  }
}

/** FLIP: the transform that puts an element laid out at last back where it was at first. scale converts screen px to stage px. */
export function flipDelta(first: DOMRectReadOnly, last: DOMRectReadOnly, scale = 1): string {
  const sx = last.width ? first.width / last.width : 1;
  const sy = last.height ? first.height / last.height : 1;
  const dx = (first.left - last.left) / scale;
  const dy = (first.top - last.top) / scale;
  return `translate(${dx}px, ${dy}px) scale(${sx}, ${sy})`;
}

type Preset = (el: HTMLElement, o: MotionOpts) => Animation[];

const run = (el: HTMLElement, frames: Keyframe[], o: MotionOpts, extra: KeyframeAnimationOptions = {}) =>
  el.animate(frames, { duration: o.reduced ? 0 : o.duration, easing: o.easing, fill: "both", ...extra });

export const builds: Record<BuildPreset, Preset> = {
  fade: (el, o) => [run(el, [{ opacity: 0 }, { opacity: 1 }], o)],
  "fade-up": (el, o) => [run(el, [{ opacity: 0, transform: "translateY(36px)" }, { opacity: 1, transform: "none" }], o)],
  "scale-in": (el, o) => [run(el, [{ opacity: 0, transform: "scale(0.86)" }, { opacity: 1, transform: "none" }], o)],
  wipe: (el, o) => [run(el, [{ clipPath: "inset(0 100% 0 0)" }, { clipPath: "inset(0 0 0 0)" }], { ...o, duration: o.duration * 1.4 })],
  draw: (el, o) => {
    const strokes = [...el.querySelectorAll<SVGGeometryElement>(SVG_STROKES)].filter((s) => strokeLength(s) > 0);
    if (!strokes.length) return builds.wipe(el, o);
    const dur = o.reduced ? 0 : o.duration * 2;
    const anims: Animation[] = [run(el, [{ opacity: 0 }, { opacity: 1 }], o, { duration: o.reduced ? 0 : Math.min(160, o.duration) })];
    strokes.forEach((s, i) => {
      const len = Math.ceil(strokeLength(s)) + 1;
      s.style.strokeDasharray = `${len}`;
      const node = s as unknown as HTMLElement;
      anims.push(
        run(node, [{ strokeDashoffset: `${len}` }, { strokeDashoffset: "0" }], o, {
          duration: dur,
          delay: o.reduced ? 0 : Math.min(i * 60, o.duration),
          easing: "cubic-bezier(0.65, 0, 0.35, 1)",
        }),
      );
      // Filled shapes get their fill once the outline is mostly drawn.
      if (getComputedStyle(s).fill !== "none") {
        anims.push(run(node, [{ fillOpacity: 0 }, { fillOpacity: 1 }], o, { delay: o.reduced ? 0 : dur * 0.6, duration: o.reduced ? 0 : dur * 0.4 }));
      }
    });
    return anims;
  },
  typewriter: (el, o) => {
    const chars = Math.max(1, (el.textContent ?? "").trim().length);
    return [
      run(el, [{ clipPath: "inset(0 100% 0 0)" }, { clipPath: "inset(0 0 0 0)" }], o, {
        duration: o.reduced ? 0 : Math.min(2400, chars * 45),
        easing: `steps(${chars}, end)`,
      }),
    ];
  },
};

/** Counts a stat up from zero. */
export function countUp(node: HTMLElement, o: MotionOpts, format: (v: number) => string) {
  const target = Number(node.dataset.value ?? 0);
  if (o.reduced || !Number.isFinite(target)) {
    node.textContent = format(target);
    return;
  }
  const dur = Math.max(900, o.duration * 2.4);
  const t0 = performance.now();
  const tick = (t: number) => {
    const p = Math.min(1, (t - t0) / dur);
    const eased = 1 - Math.pow(1 - p, 3);
    node.textContent = format(p === 1 ? target : target * eased);
    if (p < 1) requestAnimationFrame(tick);
  };
  node.textContent = format(0);
  requestAnimationFrame(tick);
}

/** Slide transitions between two slide elements stacked in the same stage. */
export const transitions: Record<Exclude<TransitionType, "morph">, (from: HTMLElement | null, to: HTMLElement, o: MotionOpts) => Animation[]> = {
  none: () => [],
  fade: (from, to, o) => [
    run(to, [{ opacity: 0 }, { opacity: 1 }], o),
    ...(from ? [run(from, [{ opacity: 1 }, { opacity: 0 }], o)] : []),
  ],
  slide: (from, to, o) => {
    const d = o.direction ?? 1;
    return [
      run(to, [{ transform: `translateX(${d * 100}%)` }, { transform: "none" }], o),
      ...(from ? [run(from, [{ opacity: 1, transform: "none" }, { opacity: 0.4, transform: `scale(0.96)` }], o)] : []),
    ];
  },
  push: (from, to, o) => {
    const d = o.direction ?? 1;
    return [
      run(to, [{ transform: `translateX(${d * 100}%)` }, { transform: "none" }], o),
      ...(from ? [run(from, [{ transform: "none" }, { transform: `translateX(${-d * 100}%)` }], o)] : []),
    ];
  },
  zoom: (from, to, o) => [
    run(to, [{ opacity: 0, transform: `scale(${o.direction === -1 ? 1.08 : 0.9})` }, { opacity: 1, transform: "none" }], o),
    ...(from ? [run(from, [{ opacity: 1, transform: "none" }, { opacity: 0, transform: `scale(${o.direction === -1 ? 0.9 : 1.08})` }], o)] : []),
  ],
};
