// Animation presets. Each preset is a pure function (element, options) =>
// Animation[] built on the Web Animations API, so presets are easy to test
// and to add. Durations and easing come from theme motion tokens.
import type { BuildPreset, Duration, TransitionType } from "./types";

export interface MotionOpts {
  duration: number;
  easing: string;
  reduced: boolean;
  direction?: 1 | -1;
}

export const prefersReducedMotion = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

export function motionFor(root: HTMLElement, d: Duration = "base"): { duration: number; easing: string } {
  const cs = getComputedStyle(root);
  const ms = parseFloat(cs.getPropertyValue(`--sk-dur-${d}`)) || 500;
  return { duration: ms, easing: cs.getPropertyValue("--sk-ease").trim() || "ease" };
}

type Preset = (el: HTMLElement, o: MotionOpts) => Animation[];

const run = (el: HTMLElement, frames: Keyframe[], o: MotionOpts, extra: KeyframeAnimationOptions = {}) =>
  el.animate(frames, { duration: o.reduced ? 0 : o.duration, easing: o.easing, fill: "both", ...extra });

export const builds: Record<BuildPreset, Preset> = {
  fade: (el, o) => [run(el, [{ opacity: 0 }, { opacity: 1 }], o)],
  "fade-up": (el, o) => [run(el, [{ opacity: 0, transform: "translateY(36px)" }, { opacity: 1, transform: "none" }], o)],
  "scale-in": (el, o) => [run(el, [{ opacity: 0, transform: "scale(0.86)" }, { opacity: 1, transform: "none" }], o)],
  wipe: (el, o) => [run(el, [{ clipPath: "inset(0 100% 0 0)" }, { clipPath: "inset(0 0 0 0)" }], { ...o, duration: o.duration * 1.4 })],
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
