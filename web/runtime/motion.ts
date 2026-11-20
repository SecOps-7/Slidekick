// Springs and stagger from Motion (motion.dev, MIT). Springs are turned into
// CSS linear() easings so every preset still runs on the Web Animations API
// and stays a pure (element, options) => Animation[] function.
import { spring, stagger } from "motion";

const cache = new Map<string, { easing: string; duration: number } | null>();

let linearOK: boolean | undefined;
function supportsLinear(): boolean {
  if (linearOK === undefined) {
    try {
      linearOK = typeof CSS !== "undefined" && CSS.supports("animation-timing-function", "linear(0, 0.5, 1)");
    } catch {
      linearOK = false;
    }
  }
  return linearOK;
}

/**
 * A spring as a WAAPI easing. visualMs is how long the spring should appear
 * to take; the returned duration includes its settle. Returns null where
 * linear() easing isn't supported, so callers keep the theme's easing.
 */
export function springEasing(bounce: number, visualMs: number): { easing: string; duration: number } | null {
  if (!supportsLinear()) return null;
  const key = `${bounce}:${visualMs}`;
  if (cache.has(key)) return cache.get(key)!;
  const css = String(spring({ keyframes: [0, 1], bounce, visualDuration: visualMs / 1000 }));
  const m = /^\s*([\d.]+)ms\s+(linear\(.+\))\s*$/s.exec(css);
  const out = m ? { duration: Number(m[1]), easing: m[2] } : null;
  cache.set(key, out);
  return out;
}

/** Start offsets (ms) for n units revealed together. */
export function staggerDelays(n: number, gapMs = 90): number[] {
  const f = stagger(gapMs / 1000);
  return Array.from({ length: n }, (_, i) => f(i, n) * 1000);
}
