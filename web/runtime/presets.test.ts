// Unit tests for the animation presets. The Web Animations API is stubbed so
// each preset's keyframes and timing can be checked as plain data.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { springEasing, staggerDelays } from "./motion";
import { builds, flipDelta, motionFor, transitions, type MotionOpts } from "./presets";

interface Call {
  el: Element;
  frames: Keyframe[];
  opts: KeyframeAnimationOptions;
}
let calls: Call[] = [];

beforeEach(() => {
  calls = [];
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (Element.prototype as any).animate = function (frames: Keyframe[], opts: KeyframeAnimationOptions) {
    calls.push({ el: this, frames, opts });
    let timing = { ...opts };
    return {
      finished: Promise.resolve(),
      cancel() {},
      effect: { getTiming: () => timing, updateTiming: (t: object) => (timing = { ...timing, ...t }) },
    } as unknown as Animation;
  };
});
afterEach(() => vi.restoreAllMocks());

const o: MotionOpts = { duration: 400, easing: "ease-out", reduced: false };
const div = (text = "") => {
  const d = document.createElement("div");
  d.textContent = text;
  document.body.append(d);
  return d;
};

describe("build presets", () => {
  it("every preset animates and honours theme timing", () => {
    for (const [name, preset] of Object.entries(builds)) {
      const anims = preset(div("Hello"), o);
      expect(anims.length, name).toBeGreaterThan(0);
      expect(calls.at(-1)!.opts.fill, name).toBe("both");
    }
  });

  it("reduced motion makes every preset instant", () => {
    for (const preset of Object.values(builds)) {
      calls = [];
      preset(div("Hello"), { ...o, reduced: true });
      for (const c of calls) expect(c.opts.duration).toBe(0);
    }
  });

  it("fade-up rises into place", () => {
    builds["fade-up"](div(), o);
    expect(calls[0].frames).toEqual([
      { opacity: 0, transform: "translateY(36px)" },
      { opacity: 1, transform: "none" },
    ]);
    expect(calls[0].opts).toMatchObject({ duration: 400, easing: "ease-out" });
  });

  it("typewriter steps once per character", () => {
    builds.typewriter(div("abcde"), o);
    expect(calls[0].opts.easing).toBe("steps(5, end)");
    expect(calls[0].opts.duration).toBe(5 * 45);
  });

  it("draw traces each SVG stroke", () => {
    const host = div();
    host.innerHTML = `<svg viewBox="0 0 10 10"><path d="M0 0L10 10"/><circle cx="5" cy="5" r="4"/></svg>`;
    for (const s of host.querySelectorAll("path, circle")) Object.defineProperty(s, "getTotalLength", { value: () => 42 });
    builds.draw(host, o);
    const strokes = calls.filter((c) => c.frames.some((f) => "strokeDashoffset" in f));
    expect(strokes).toHaveLength(2);
    expect(strokes[0].frames[0]).toEqual({ strokeDashoffset: "43" });
    expect((host.querySelector("path") as unknown as HTMLElement).style.strokeDasharray).toBe("43");
    expect(strokes[1].opts.delay).toBe(60); // strokes start one after another
  });

  it("draw falls back to a wipe without line art", () => {
    builds.draw(div("text"), o);
    expect(calls[0].frames[0]).toEqual({ clipPath: "inset(0 100% 0 0)" });
  });
});

describe("transitions", () => {
  it("slide comes in from the side it is heading", () => {
    transitions.slide(div(), div(), { ...o, direction: -1 });
    expect(calls[0].frames[0]).toEqual({ transform: "translateX(-100%)" });
  });

  it("push moves both slides", () => {
    transitions.push(div(), div(), o);
    expect(calls).toHaveLength(2);
    expect(calls[1].frames[1]).toEqual({ transform: "translateX(-100%)" });
  });

  it("first slide has no outgoing animation", () => {
    transitions.fade(null, div(), o);
    expect(calls).toHaveLength(1);
  });
});

const REST_TRANSFORM = /^(none|perspective\(\d+px\) translateX\(0\) rotateY\(0deg\))$/;
const REST_CLIP = /^(inset\(0 0 0 0\)|circle\(75% at 50% 50%\))$/;

/** The values an element is left with: every animated property back at its natural state. */
function expectAtRest(frame: Keyframe, label: string) {
  if ("opacity" in frame) expect(frame.opacity, label).toBe(1);
  if ("transform" in frame) expect(String(frame.transform), label).toMatch(REST_TRANSFORM);
  if ("filter" in frame) expect(String(frame.filter), label).toMatch(/^(none|blur\(0px\)|brightness\(1\))$/);
  if ("clipPath" in frame) expect(String(frame.clipPath), label).toMatch(REST_CLIP);
}

describe("more build presets", () => {
  it("every preset ends with the element at rest", () => {
    for (const [name, preset] of Object.entries(builds)) {
      calls = [];
      const el = div("Hello");
      preset(el, o);
      for (const c of calls.filter((c) => c.el === el && c.opts.fill === "both")) expectAtRest(c.frames.at(-1)!, name);
    }
  });

  it("durations follow the theme", () => {
    for (const [name, preset] of Object.entries(builds)) {
      if (name === "typewriter") continue; // paced by character count
      calls = [];
      preset(div("Hello"), o);
      const fast = calls.map((c) => Number(c.opts.duration));
      calls = [];
      preset(div("Hello"), { ...o, duration: 800 });
      calls.forEach((c, i) => expect(Number(c.opts.duration), name).toBeCloseTo(fast[i] * 2));
    }
  });

  it("directional entrances come from the side they name", () => {
    const start = (name: keyof typeof builds) => {
      calls = [];
      builds[name](div(), o);
      return String(calls[0].frames[0].transform);
    };
    expect(start("fade-down")).toBe("translateY(-36px)");
    expect(start("fade-left")).toBe("translateX(48px)"); // moves left, so starts to the right
    expect(start("fade-right")).toBe("translateX(-48px)");
    expect(start("slide-up")).toMatch(/^translateY\(\d+px\)$/);
    expect(start("slide-down")).toMatch(/^translateY\(-\d+px\)$/);
    expect(start("slide-left")).toMatch(/^translateX\(\d+px\)$/);
    expect(start("slide-right")).toMatch(/^translateX\(-\d+px\)$/);
    expect(start("drop")).toMatch(/^translateY\(-\d+px\)$/);
  });

  it("pop and bounce-in overshoot with their own curve, not a second theme spring", () => {
    for (const name of ["pop", "bounce-in"] as const) {
      calls = [];
      builds[name](div(), { ...o, easing: "linear(0, 1.2, 1)" });
      const scales = calls[0].frames.map((f) => /scale\(([\d.]+)\)/.exec(String(f.transform))?.[1]).filter(Boolean).map(Number);
      expect(Math.max(...scales), name).toBeGreaterThan(1);
      expect(calls[0].opts.easing, name).toBe("linear");
      const offsets = calls[0].frames.map((f) => f.offset).filter((x) => x !== undefined) as number[];
      expect([...offsets].sort(), name).toEqual(offsets); // keyframes in order
    }
  });

  it("swing hinges on its top edge", () => {
    builds.swing(div(), o);
    for (const f of calls[0].frames) expect(f.transformOrigin).toBe("50% 0");
  });

  it("highlight's marker sweeps away and leaves no trace", () => {
    const el = div("Key line");
    builds.highlight(el, o);
    const marker = calls.find((c) => c.frames.some((f) => "backgroundSize" in f))!;
    expect(marker.opts.fill).toBe("none");
    expect(marker.frames[0].backgroundSize).toBe("0% 100%");
    expect(marker.frames.at(-1)!.backgroundSize).toBe("0% 100%");
    expect(marker.frames.some((f) => f.backgroundSize === "100% 100%")).toBe(true);
  });

  it("clip reveals start fully hidden", () => {
    const first = (name: keyof typeof builds) => {
      calls = [];
      builds[name](div(), o);
      return calls[0].frames[0].clipPath;
    };
    expect(first("reveal")).toBe("inset(100% 0 0 0)");
    expect(first("wipe-down")).toBe("inset(0 0 100% 0)");
    expect(first("iris")).toBe("circle(0% at 50% 50%)");
  });

  it("reduced motion also drops stagger delays", () => {
    for (const preset of Object.values(builds)) {
      calls = [];
      preset(div("Hello"), { ...o, reduced: true });
      for (const c of calls) expect(c.opts.delay ?? 0).toBe(0);
    }
  });
});

describe("more transitions", () => {
  const MOVING = ["zoom-out", "cover", "uncover", "push-up", "wipe", "split", "iris", "flip", "cube", "swap"] as const;
  const ALL = [...MOVING, "fade-through", "blur"] as const;

  const record = (name: keyof typeof transitions, from: HTMLElement | null, to: HTMLElement, opts: MotionOpts) => {
    calls = [];
    transitions[name](from, to, opts);
    return calls.map((c) => ({ who: c.el === to ? "to" : "from", frames: c.frames, opts: c.opts }));
  };

  it("every transition animates and leaves the new slide at rest", () => {
    for (const name of ALL) {
      for (const d of [1, -1] as const) {
        const to = div();
        const anims = record(name, div(), to, { ...o, direction: d });
        expect(anims.length, name).toBeGreaterThan(0);
        for (const a of anims.filter((a) => a.who === "to")) expectAtRest(a.frames.at(-1)!, `${name} ${d}`);
        for (const a of anims) expect(Number(a.opts.duration), name).toBeGreaterThan(0);
      }
    }
  });

  it("going back plays the mirror image", () => {
    for (const name of MOVING) {
      const fwd = JSON.stringify(record(name, div(), div(), { ...o, direction: 1 }));
      const back = JSON.stringify(record(name, div(), div(), { ...o, direction: -1 }));
      expect(back, name).not.toBe(fwd);
    }
    expect(record("cover", div(), div(), { ...o, direction: -1 })[0].frames[0].transform).toBe("translateX(-100%)");
    expect(record("push-up", div(), div(), { ...o, direction: 1 })[0].frames[0].transform).toBe("translateY(100%)");
    expect(record("wipe", div(), div(), { ...o, direction: -1 })[0].frames[0].clipPath).toBe("inset(0 0 0 100%)");
  });

  it("uncover and the backward iris and split keep the old slide on top", () => {
    for (const [name, d] of [["uncover", 1], ["iris", -1], ["split", -1]] as const) {
      const from = record(name, div(), div(), { ...o, direction: d }).filter((a) => a.who === "from");
      expect(from.length, name).toBe(1);
      for (const f of from[0].frames) expect(f.zIndex, name).toBe(2);
    }
  });

  it("flip turns the new slide in only after the old one is edge-on", () => {
    const [to, from] = record("flip", div(), div(), o);
    expect(from.frames.at(-1)!.transform).toBe("perspective(2400px) rotateY(90deg)");
    expect(to.frames[0].transform).toBe("perspective(2400px) rotateY(-90deg)");
    expect(to.opts.delay).toBe(from.opts.duration);
  });

  it("reduced motion falls back to an instant crossfade", () => {
    for (const name of ALL) {
      const anims = record(name, div(), div(), { ...o, reduced: true, direction: -1 });
      for (const a of anims) {
        expect(a.opts.duration, name).toBe(0);
        for (const f of a.frames) expect(Object.keys(f), name).toEqual(["opacity"]);
      }
    }
  });

  it("the first slide gets no outgoing animation", () => {
    for (const name of ALL) {
      const anims = record(name, null, div(), { ...o, direction: -1 });
      expect(anims.every((a) => a.who === "to"), name).toBe(true);
      expect(anims.length, name).toBeGreaterThan(0);
    }
  });
});

describe("morph fallback (FLIP)", () => {
  it("maps the new box back onto the old one", () => {
    const first = new DOMRect(100, 50, 200, 100);
    const last = new DOMRect(300, 150, 400, 50);
    expect(flipDelta(first, last)).toBe("translate(-200px, -100px) scale(0.5, 2)");
  });

  it("converts screen pixels to stage pixels", () => {
    expect(flipDelta(new DOMRect(0, 0, 10, 10), new DOMRect(50, 100, 10, 10), 0.5)).toBe("translate(-100px, -200px) scale(1, 1)");
  });
});

describe("springs and stagger (Motion)", () => {
  it("staggers units sharing a build step", () => {
    expect(staggerDelays(3)).toEqual([0, 90, 180]);
    expect(staggerDelays(1)).toEqual([0]);
  });

  it("turns a bouncy spring into a linear() easing that overshoots", () => {
    vi.stubGlobal("CSS", { supports: () => true, escape: (s: string) => s });
    const s = springEasing(0.4, 500)!;
    expect(s.easing.startsWith("linear(")).toBe(true);
    expect(s.duration).toBeGreaterThan(0);
    const points = s.easing.slice(7, -1).split(",").map((p) => parseFloat(p));
    expect(points[0]).toBe(0);
    expect(Math.max(...points)).toBeGreaterThan(1);
    vi.unstubAllGlobals();
  });

  it("uses the theme spring only where asked", () => {
    vi.stubGlobal("CSS", { supports: () => true, escape: (s: string) => s });
    const root = document.documentElement;
    root.style.setProperty("--sk-dur-base", "500ms");
    root.style.setProperty("--sk-ease", "ease");
    root.style.setProperty("--sk-bounce", "0.3");
    expect(motionFor(root, "base").easing).toBe("ease");
    expect(motionFor(root, "base", { spring: true }).easing.startsWith("linear(")).toBe(true);
    root.style.setProperty("--sk-bounce", "0");
    expect(motionFor(root, "base", { spring: true }).easing).toBe("ease");
    vi.unstubAllGlobals();
  });
});
