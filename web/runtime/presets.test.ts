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
