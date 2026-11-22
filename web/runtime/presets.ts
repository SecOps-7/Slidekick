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
  el.animate(frames, { duration: o.reduced ? 0 : o.duration, easing: o.easing, fill: "both", ...extra, ...(o.reduced ? { duration: 0, delay: 0 } : {}) });

/** A simple entrance: from a starting state to the element's natural state. */
const enter = (from: Keyframe, to: Keyframe = {}, scale = 1): Preset => (el, o) => [
  run(el, [from, { opacity: 1, transform: "none", ...to }], o, { duration: o.duration * scale }),
];

/**
 * A shaped entrance whose keyframes carry their own overshoot (pop, drop,
 * bounce): the curve between keyframes is fixed so a theme spring does not
 * add a second bounce, but the duration still follows the theme.
 */
const shaped = (frames: Keyframe[], scale = 1): Preset => (el, o) => [
  run(el, frames, o, { duration: o.duration * scale, easing: "linear" }),
];

const OUT = "cubic-bezier(0.22, 1, 0.36, 1)";
const IN = "cubic-bezier(0.55, 0, 1, 0.45)";

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
  "fade-down": enter({ opacity: 0, transform: "translateY(-36px)" }),
  "fade-left": enter({ opacity: 0, transform: "translateX(48px)" }),
  "fade-right": enter({ opacity: 0, transform: "translateX(-48px)" }),
  "slide-up": enter({ opacity: 0, transform: "translateY(240px)" }, {}, 1.2),
  "slide-down": enter({ opacity: 0, transform: "translateY(-240px)" }, {}, 1.2),
  "slide-left": enter({ opacity: 0, transform: "translateX(320px)" }, {}, 1.2),
  "slide-right": enter({ opacity: 0, transform: "translateX(-320px)" }, {}, 1.2),
  "zoom-in": enter({ opacity: 0, transform: "scale(0.4)" }, {}, 1.2),
  "zoom-out": enter({ opacity: 0, transform: "scale(1.6)" }, {}, 1.2),
  pop: shaped(
    [
      { opacity: 0, transform: "scale(0.5)", easing: OUT },
      { opacity: 1, transform: "scale(1.08)", offset: 0.6, easing: "ease-in-out" },
      { opacity: 1, transform: "none" },
    ],
    1.1,
  ),
  drop: shaped(
    [
      { opacity: 0, transform: "translateY(-160px)", easing: IN },
      { opacity: 1, transform: "translateY(0)", offset: 0.55, easing: OUT },
      { opacity: 1, transform: "translateY(-18px)", offset: 0.75, easing: IN },
      { opacity: 1, transform: "none" },
    ],
    1.6,
  ),
  "bounce-in": shaped(
    [
      { opacity: 0, transform: "scale(0.3)", easing: OUT },
      { opacity: 1, transform: "scale(1.1)", offset: 0.45, easing: "ease-in-out" },
      { opacity: 1, transform: "scale(0.94)", offset: 0.7, easing: "ease-in-out" },
      { opacity: 1, transform: "scale(1.02)", offset: 0.87, easing: "ease-in-out" },
      { opacity: 1, transform: "none" },
    ],
    1.8,
  ),
  "flip-x": enter({ opacity: 0, transform: "perspective(1200px) rotateX(-90deg)" }, {}, 1.3),
  "flip-y": enter({ opacity: 0, transform: "perspective(1200px) rotateY(90deg)" }, {}, 1.3),
  "rotate-in": enter({ opacity: 0, transform: "rotate(-14deg) scale(0.8)" }, {}, 1.2),
  swing: (el, o) => [
    run(
      el,
      [
        { opacity: 0, transformOrigin: "50% 0", transform: "perspective(1200px) rotateX(-75deg)", easing: OUT },
        { opacity: 1, transformOrigin: "50% 0", transform: "perspective(1200px) rotateX(12deg)", offset: 0.55, easing: "ease-in-out" },
        { opacity: 1, transformOrigin: "50% 0", transform: "perspective(1200px) rotateX(-5deg)", offset: 0.8, easing: "ease-in-out" },
        { opacity: 1, transformOrigin: "50% 0", transform: "none" },
      ],
      o,
      { duration: o.duration * 1.8, easing: "linear" },
    ),
  ],
  "blur-in": enter({ opacity: 0, filter: "blur(18px)", transform: "scale(1.04)" }, { filter: "blur(0px)" }, 1.3),
  reveal: (el, o) => [
    run(el, [{ clipPath: "inset(100% 0 0 0)", transform: "translateY(40px)" }, { clipPath: "inset(0 0 0 0)", transform: "none" }], o, { duration: o.duration * 1.3 }),
  ],
  "wipe-down": (el, o) => [run(el, [{ clipPath: "inset(0 0 100% 0)" }, { clipPath: "inset(0 0 0 0)" }], o, { duration: o.duration * 1.4 })],
  iris: (el, o) => [run(el, [{ clipPath: "circle(0% at 50% 50%)" }, { clipPath: "circle(75% at 50% 50%)" }], o, { duration: o.duration * 1.5 })],
  stretch: enter({ opacity: 0, transform: "scaleX(0.2)" }, {}, 1.1),
  highlight: (el, o) => {
    // Fades in, then an accent marker sweeps across and off again, so the
    // element ends exactly as it looks in a static render.
    const marker = "linear-gradient(color-mix(in oklab, var(--sk-accent) 35%, transparent), color-mix(in oklab, var(--sk-accent) 35%, transparent))";
    const bg = { backgroundImage: marker, backgroundRepeat: "no-repeat" };
    return [
      run(
        el,
        [
          { ...bg, backgroundPosition: "0 0", backgroundSize: "0% 100%" },
          { ...bg, backgroundPosition: "0 0", backgroundSize: "100% 100%", offset: 0.5 },
          { ...bg, backgroundPosition: "100% 0", backgroundSize: "100% 100%", offset: 0.65 },
          { ...bg, backgroundPosition: "100% 0", backgroundSize: "0% 100%" },
        ],
        o,
        { duration: o.duration * 2.6, easing: "cubic-bezier(0.65, 0, 0.35, 1)", fill: "none" },
      ),
      run(el, [{ opacity: 0 }, { opacity: 1 }], o, { duration: o.duration * 0.6 }),
    ];
  },
};

/** Counts a stat up from zero. */
export function countUp(node: HTMLElement, o: MotionOpts, format: (v: number) => string, from = 0) {
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
    node.textContent = format(p === 1 ? target : from + (target - from) * eased);
    if (p < 1) requestAnimationFrame(tick);
  };
  node.textContent = format(from);
  requestAnimationFrame(tick);
}

type Transition = (from: HTMLElement | null, to: HTMLElement, o: MotionOpts) => Animation[];

/** Reduced motion turns a transition into the plain crossfade (which is then instant). */
const calm =
  (t: (from: HTMLElement | null, to: HTMLElement, o: MotionOpts, d: 1 | -1) => Animation[]): Transition =>
  (from, to, o) =>
    o.reduced ? transitions.fade(from, to, o) : t(from, to, o, o.direction ?? 1);

/** Slide transitions between two slide elements stacked in the same stage. */
export const transitions: Record<Exclude<TransitionType, "morph">, Transition> = {
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
  // The transitions below share two rules: reduced motion falls back to the
  // (instant) crossfade, and going back plays the mirror image of going forward.
  "zoom-out": calm((from, to, o, d) => [
    run(to, [{ opacity: 0, transform: `scale(${d === 1 ? 1.12 : 0.88})` }, { opacity: 1, transform: "none" }], o),
    ...(from ? [run(from, [{ opacity: 1, transform: "none" }, { opacity: 0, transform: `scale(${d === 1 ? 0.88 : 1.12})` }], o)] : []),
  ]),
  "fade-through": calm((from, to, o) => [
    run(to, [{ opacity: 0 }, { opacity: 1 }], o, { duration: o.duration * 0.6, delay: o.duration * 0.5 }),
    ...(from ? [run(from, [{ opacity: 1 }, { opacity: 0 }], o, { duration: o.duration * 0.5 })] : []),
  ]),
  cover: calm((from, to, o, d) => [
    run(to, [{ transform: `translateX(${d * 100}%)`, boxShadow: "0 0 0 rgba(0,0,0,0)" }, { transform: "none", boxShadow: "0 0 80px rgba(0,0,0,0.45)" }], o),
    ...(from ? [run(from, [{ filter: "brightness(1)" }, { filter: "brightness(0.6)" }], o)] : []),
  ]),
  uncover: calm((from, to, o, d) => [
    run(to, [{ filter: "brightness(0.6)" }, { filter: "brightness(1)" }], o),
    ...(from ? [run(from, [{ zIndex: 2, transform: "none" }, { zIndex: 2, transform: `translateX(${-d * 100}%)` }], o)] : []),
  ]),
  "push-up": calm((from, to, o, d) => [
    run(to, [{ transform: `translateY(${d * 100}%)` }, { transform: "none" }], o),
    ...(from ? [run(from, [{ transform: "none" }, { transform: `translateY(${-d * 100}%)` }], o)] : []),
  ]),
  wipe: calm((_from, to, o, d) => [
    run(to, [{ clipPath: d === 1 ? "inset(0 100% 0 0)" : "inset(0 0 0 100%)" }, { clipPath: "inset(0 0 0 0)" }], o, { duration: o.duration * 1.2 }),
  ]),
  split: calm((from, to, o, d) => {
    if (d === 1 || !from) return [run(to, [{ clipPath: "inset(0 50% 0 50%)" }, { clipPath: "inset(0 0 0 0)" }], o, { duration: o.duration * 1.2 })];
    // Back: the old slide closes into the centre over the new one.
    return [run(from, [{ zIndex: 2, clipPath: "inset(0 0 0 0)" }, { zIndex: 2, clipPath: "inset(0 50% 0 50%)" }], o, { duration: o.duration * 1.2 })];
  }),
  iris: calm((from, to, o, d) => {
    if (d === 1 || !from) return [run(to, [{ clipPath: "circle(0% at 50% 50%)" }, { clipPath: "circle(75% at 50% 50%)" }], o, { duration: o.duration * 1.3 })];
    return [run(from, [{ zIndex: 2, clipPath: "circle(75% at 50% 50%)" }, { zIndex: 2, clipPath: "circle(0% at 50% 50%)" }], o, { duration: o.duration * 1.3 })];
  }),
  blur: calm((from, to, o) => [
    run(to, [{ opacity: 0, filter: "blur(24px)" }, { opacity: 1, filter: "blur(0px)" }], o),
    ...(from ? [run(from, [{ opacity: 1, filter: "blur(0px)" }, { opacity: 0, filter: "blur(24px)" }], o)] : []),
  ]),
  flip: calm((from, to, o, d) => {
    // The old slide turns edge-on, then the new one turns in from the other side.
    const half = o.duration * 0.6;
    return [
      run(to, [{ transform: `perspective(2400px) rotateY(${-d * 90}deg)` }, { transform: "none" }], o, { duration: half, delay: from ? half : 0, easing: OUT }),
      ...(from ? [run(from, [{ transform: "none" }, { transform: `perspective(2400px) rotateY(${d * 90}deg)` }], o, { duration: half, easing: IN })] : []),
    ];
  }),
  cube: calm((from, to, o, d) => [
    // Each slide turns about the edge it shares with the other, like two faces of a cube.
    run(
      to,
      [
        { transformOrigin: d === 1 ? "0 50%" : "100% 50%", transform: `perspective(2400px) translateX(${d * 100}%) rotateY(${d * 90}deg)` },
        { transformOrigin: d === 1 ? "0 50%" : "100% 50%", transform: "perspective(2400px) translateX(0) rotateY(0deg)" },
      ],
      o,
      { duration: o.duration * 1.4 },
    ),
    ...(from
      ? [
          run(
            from,
            [
              { transformOrigin: d === 1 ? "100% 50%" : "0 50%", transform: "perspective(2400px) translateX(0) rotateY(0deg)", filter: "brightness(1)" },
              { transformOrigin: d === 1 ? "100% 50%" : "0 50%", transform: `perspective(2400px) translateX(${-d * 100}%) rotateY(${-d * 90}deg)`, filter: "brightness(0.5)" },
            ],
            o,
            { duration: o.duration * 1.4 },
          ),
        ]
      : []),
  ]),
  swap: calm((from, to, o, d) => [
    run(
      to,
      [
        { opacity: 0, transform: `translateX(${d * 30}%) scale(0.8)` },
        { opacity: 1, transform: `translateX(${d * 12}%) scale(0.9)`, offset: 0.5 },
        { opacity: 1, transform: "none" },
      ],
      o,
      { duration: o.duration * 1.3 },
    ),
    ...(from
      ? [
          run(
            from,
            [
              { opacity: 1, transform: "none" },
              { opacity: 0.6, transform: `translateX(${-d * 12}%) scale(0.9)`, offset: 0.5 },
              { opacity: 0, transform: `translateX(${-d * 30}%) scale(0.8)` },
            ],
            o,
            { duration: o.duration * 1.3 },
          ),
        ]
      : []),
  ]),
};
