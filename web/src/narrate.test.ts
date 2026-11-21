import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { followTo, isNarration, Narrator, slideIndex } from "./narrate";

const flush = async () => {
  for (let i = 0; i < 5; i++) await Promise.resolve();
};

class FakeAudio {
  static last: FakeAudio | null = null;
  static reject: Error | null = null;
  src = "";
  muted = false;
  paused = true;
  onended: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor() {
    FakeAudio.last = this;
  }
  play() {
    if (FakeAudio.reject) return Promise.reject(FakeAudio.reject);
    this.paused = false;
    return Promise.resolve();
  }
  pause() {
    this.paused = true;
  }
}

let fetchMock: ReturnType<typeof vi.fn>;
beforeEach(() => {
  FakeAudio.last = null;
  FakeAudio.reject = null;
  vi.stubGlobal("Audio", FakeAudio);
  fetchMock = vi.fn(async () => new Response("{}", { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  history.replaceState(null, "", "/d/d_x/present?k=key1");
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  document.body.replaceChildren();
});

const step = (over: Record<string, unknown> = {}) => ({ type: "present", state: "step", seq: 4, slideId: "s2", index: 1, build: 1, text: "Hello room", audio: "/api/tts/audio/abc.mp3", durationMs: 1500, ...over });

function make(o: { ack: boolean; audible: boolean; captions?: boolean }) {
  const host = document.createElement("div");
  document.body.append(host);
  const show = vi.fn();
  const n = new Narrator({ deckId: "d_x", host, show, captions: o.captions ?? true, ack: o.ack, audible: o.audible });
  return { n, show, host };
}

const acks = () => fetchMock.mock.calls.filter((c) => String(c[0]).includes("/present/ack"));

describe("Narrator", () => {
  it("stays silent and leaves the acks to the TV while casting, until unmuted", async () => {
    vi.useFakeTimers();
    const { n, show } = make({ ack: true, audible: true });
    n.setAudible(false);
    n.setAck(false);
    n.handle(step());
    await flush();
    vi.advanceTimersByTime(5000);
    expect(show).toHaveBeenCalled();
    expect(FakeAudio.last?.paused ?? true).toBe(true);
    expect(acks()).toHaveLength(0);
    // Unmuted again: this screen plays the next line and reports it heard.
    n.setAudible(true);
    n.setAck(true);
    n.handle(step({ seq: 5 }));
    await flush();
    expect(FakeAudio.last?.paused).toBe(false);
    FakeAudio.last?.onended?.();
    expect(acks()).toHaveLength(1);
  });

  it("tells AI events from the presenter's position relay", () => {
    expect(isNarration({ type: "present", index: 1, step: 0 })).toBe(false);
    expect(isNarration({ type: "present", state: "step" })).toBe(true);
    expect(isNarration({ type: "focus", state: "x" })).toBe(false);
  });

  it("plays the line on a presenting screen and acks when it ends", async () => {
    const { n, show } = make({ ack: true, audible: true });
    expect(n.handle(step())).toBe(true);
    await flush();
    expect(show).toHaveBeenCalledWith(1, 1, "s2");
    const a = FakeAudio.last!;
    expect(a.src).toBe("/api/tts/audio/abc.mp3");
    expect(a.paused).toBe(false);
    expect(n.caption.textContent).toBe("Hello room");
    expect(n.caption.hidden).toBe(false);
    expect(acks()).toHaveLength(0);
    a.onended!();
    a.onended!(); // acks once
    expect(acks()).toHaveLength(1);
    const [url, init] = acks()[0];
    expect(url).toBe("/api/decks/d_x/present/ack?k=key1");
    expect(JSON.parse((init as RequestInit).body as string)).toEqual({ seq: 4 });
  });

  it("asks for a click when autoplay is blocked and still acks after the line's length", async () => {
    vi.useFakeTimers();
    FakeAudio.reject = new DOMException("blocked", "NotAllowedError");
    const { n } = make({ ack: true, audible: true });
    n.handle(step());
    await flush();
    expect(n.enableBtn.hidden).toBe(false);
    expect(acks()).toHaveLength(0);
    vi.advanceTimersByTime(1600);
    expect(acks()).toHaveLength(1);
    // The click hides the button; later lines play.
    FakeAudio.reject = null;
    n.enableBtn.click();
    expect(n.enableBtn.hidden).toBe(true);
  });

  it("reports a block and its end, and replays the current line on demand", async () => {
    FakeAudio.reject = new DOMException("blocked", "NotAllowedError");
    const host = document.createElement("div");
    document.body.append(host);
    const onBlocked = vi.fn();
    const n = new Narrator({ deckId: "d_x", host, show: vi.fn(), captions: false, ack: false, audible: true, onBlocked });
    n.handle(step());
    await flush();
    expect(onBlocked).toHaveBeenLastCalledWith(true);
    // A second blocked line doesn't report again.
    n.handle(step({ seq: 5 }));
    await flush();
    expect(onBlocked).toHaveBeenCalledTimes(1);
    // The click plays the current line and ends the block.
    FakeAudio.reject = null;
    n.enableBtn.click();
    await flush();
    expect(FakeAudio.last?.paused).toBe(false);
    expect(onBlocked).toHaveBeenLastCalledWith(false);
    expect(onBlocked).toHaveBeenCalledTimes(2);
    // replay() (the presenter view taking over) plays the line still due; a muted screen doesn't.
    FakeAudio.last!.paused = true;
    n.replay();
    expect(FakeAudio.last?.paused).toBe(false);
    FakeAudio.last!.paused = true;
    n.setAudible(false);
    n.replay();
    expect(FakeAudio.last?.paused).toBe(true);
  });

  it("acks a step with nothing to say at once", async () => {
    const { n } = make({ ack: true, audible: true });
    n.handle(step({ text: undefined, audio: undefined, seq: 9 }));
    await flush();
    expect(acks()).toHaveLength(1);
    expect(n.caption.hidden).toBe(true);
  });

  it("follows silently on other screens and never acks", async () => {
    vi.useFakeTimers();
    const { n, show } = make({ ack: false, audible: false });
    n.handle(step());
    await flush();
    vi.advanceTimersByTime(5000);
    expect(show).toHaveBeenCalled();
    expect(FakeAudio.last).toBeNull();
    expect(acks()).toHaveLength(0);
    expect(n.caption.textContent).toBe("Hello room");
    // Unmuted, the viewer plays the next line locally, still without acking.
    n.setAudible(true);
    n.handle(step({ seq: 5 }));
    await flush();
    expect(FakeAudio.last!.paused).toBe(false);
    FakeAudio.last!.onended!();
    expect(acks()).toHaveLength(0);
  });

  it("stops the voice and clears the caption when the run stops", async () => {
    const { n } = make({ ack: true, audible: true });
    n.handle({ type: "present", state: "started" });
    expect(n.running).toBe(true);
    n.handle(step());
    await flush();
    n.handle({ type: "present", state: "stopped" });
    expect(n.running).toBe(false);
    expect(FakeAudio.last!.paused).toBe(true);
    expect(n.caption.hidden).toBe(true);
    FakeAudio.last!.onended?.(); // handlers were detached
    expect(acks()).toHaveLength(0);
  });

  it("hides captions when turned off", async () => {
    const { n } = make({ ack: false, audible: false, captions: false });
    n.handle(step());
    await flush();
    expect(n.caption.hidden).toBe(true);
    n.setCaptions(true);
    expect(n.caption.hidden).toBe(false);
  });
});

describe("followTo", () => {
  const fake = (index: number, step: number, maxStep: number) => ({
    position: { index, step, maxStep },
    next: vi.fn(async () => {}),
    prev: vi.fn(async () => {}),
    goto: vi.fn(async () => {}),
  });
  it("animates single steps and jumps otherwise", async () => {
    let p = fake(1, 0, 2);
    await followTo(p, 1, 1);
    expect(p.next).toHaveBeenCalled();
    p = fake(1, 2, 2);
    await followTo(p, 2, 0);
    expect(p.next).toHaveBeenCalled();
    p = fake(0, 0, 0);
    await followTo(p, 3, 1);
    expect(p.goto).toHaveBeenCalledWith(3, 1, { animate: true });
    p = fake(3, 1, 2);
    await followTo(p, 3, 1);
    expect(p.goto).not.toHaveBeenCalled();
  });
  it("finds slides by id first", () => {
    expect(slideIndex([{ id: "a" }, { id: "b" }], 0, "b")).toBe(1);
    expect(slideIndex([{ id: "a" }], 4, "gone")).toBe(4);
  });
});
