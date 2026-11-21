// AI narration playback, shared by the presenter view, the cast TV, the
// viewer and the editor.
//
// While an AI presents a deck the server sends {"type":"present","state":
// "step", seq, slideId, index, build, text?, audio?, browserVoice?, voice?,
// durationMs?} for every step, and {"type":"present","state":"started" |
// "stopped" | "ended"} around a run. (The presenter's own position relay is
// also "present", but has no state.)
//
// Presenting screens (the presenter view and a TV) play the line and then
// acknowledge it, which tells the server to go on: that's what paces the
// presentation. Other screens follow the slides silently; the viewer may
// unmute to hear it too, without acknowledging.
import { withKey } from "./key";
import type { LiveMsg } from "./sync";
import "./narrate.css";

export interface NarrationStep {
  type: "present";
  state: "step";
  seq: number;
  slideId: string;
  index: number;
  build: number;
  text?: string;
  audio?: string;
  browserVoice?: boolean;
  voice?: string;
  durationMs?: number;
  actor?: string;
}

/** True for AI presentation events (as opposed to the presenter's position relay). */
export const isNarration = (m: LiveMsg): boolean => m.type === "present" && typeof m.state === "string";

export interface NarratorOptions {
  deckId: string;
  /** Moves this screen to a slide (by index; slideId to double-check) at a build step. */
  show: (index: number, build: number, slideId: string) => Promise<void> | void;
  /** Presenting screens acknowledge each line once it has been heard. */
  ack: boolean;
  /** Whether lines are played out loud here. */
  audible: boolean;
  /** Where the caption and the "Click to enable sound" button go. */
  host: HTMLElement;
  captions: boolean;
}

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
};

export class Narrator {
  /** Set while an AI presentation is running (from started/step until stopped/ended). */
  running = false;
  /** The last step seen. */
  step: NarrationStep | null = null;
  readonly caption: HTMLDivElement;
  readonly enableBtn: HTMLButtonElement;
  private audio: HTMLAudioElement | null = null;
  private acked = new Set<number>();
  private fallback = 0;
  private current = 0; // seq of the line playing now (0: none)
  private o: NarratorOptions;

  constructor(o: NarratorOptions) {
    this.o = o;
    this.caption = el("div", "nr-caption");
    this.caption.setAttribute("aria-live", "polite");
    this.caption.hidden = true;
    this.enableBtn = el("button", "nr-enable", "Click to enable sound");
    this.enableBtn.type = "button";
    this.enableBtn.hidden = true;
    this.enableBtn.onclick = (e) => {
      e.stopPropagation();
      this.unlock();
    };
    o.host.append(this.caption, this.enableBtn);
  }

  get audible() {
    return this.o.audible;
  }
  get captions() {
    return this.o.captions;
  }

  setAudible(on: boolean) {
    this.o.audible = on;
    if (!on) this.silence();
  }

  setCaptions(on: boolean) {
    this.o.captions = on;
    this.caption.hidden = !on || !this.caption.textContent;
  }

  /** Feeds a live message in. Returns true when it was a narration event (and was handled). */
  handle(m: LiveMsg): boolean {
    if (!isNarration(m)) return false;
    if (m.state === "step") void this.play(m as unknown as NarrationStep);
    else if (m.state === "started") this.running = true;
    else if (m.state === "stopped" || m.state === "ended") {
      this.running = false;
      this.silence();
      this.showCaption("");
    }
    return true;
  }

  private showCaption(text: string) {
    this.caption.textContent = text;
    this.caption.hidden = !this.o.captions || !text;
  }

  /** Stops whatever is being said here. */
  silence() {
    clearTimeout(this.fallback);
    this.current = 0;
    if (this.audio) {
      this.audio.onended = this.audio.onerror = null;
      this.audio.pause();
    }
    if (typeof speechSynthesis !== "undefined") speechSynthesis.cancel();
  }

  private sendAck(seq: number) {
    if (!this.o.ack || this.acked.has(seq)) return;
    this.acked.add(seq);
    if (this.current === seq) this.current = 0;
    void fetch(withKey(`/api/decks/${encodeURIComponent(this.o.deckId)}/present/ack`), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ seq }),
    }).catch(() => {});
  }

  /** Acks after the line's length, for when it can't be heard here. */
  private ackLater(st: NarrationStep) {
    clearTimeout(this.fallback);
    this.fallback = window.setTimeout(() => this.sendAck(st.seq), Math.max(0, st.durationMs ?? 0));
  }

  private async play(st: NarrationStep) {
    this.silence();
    this.running = true;
    this.step = st;
    this.current = st.seq;
    try {
      await this.o.show(st.index, st.build, st.slideId);
    } catch {
      /* the move failing must not stall the narration */
    }
    if (this.current !== st.seq) return; // a newer step arrived meanwhile
    const text = st.text?.trim() ?? "";
    this.showCaption(text);
    if (!text) return this.sendAck(st.seq);
    if (!this.o.audible) return this.o.ack ? this.ackLater(st) : undefined;
    if (st.audio) return this.playAudio(st);
    if (st.browserVoice) return this.speak(st, text);
    this.ackLater(st);
  }

  private playAudio(st: NarrationStep) {
    this.audio ??= new Audio();
    const a = this.audio;
    a.onended = () => this.sendAck(st.seq);
    a.onerror = () => this.sendAck(st.seq); // a broken file: move on
    a.src = st.audio!;
    a.play().then(
      () => (this.enableBtn.hidden = true),
      (err: unknown) => {
        if (this.current !== st.seq) return;
        // Autoplay blocked (or the element was interrupted): ask for a click,
        // and don't hold up the presentation meanwhile.
        if (err instanceof DOMException && err.name === "AbortError") return;
        this.enableBtn.hidden = false;
        this.ackLater(st);
      },
    );
  }

  private speak(st: NarrationStep, text: string) {
    if (typeof speechSynthesis === "undefined") return this.ackLater(st);
    const u = new SpeechSynthesisUtterance(text);
    if (st.voice) {
      const v = speechSynthesis.getVoices().find((x) => x.name === st.voice || x.voiceURI === st.voice);
      if (v) u.voice = v;
    }
    u.onend = () => this.sendAck(st.seq);
    u.onerror = (e) => {
      if (this.current !== st.seq || e.error === "interrupted" || e.error === "canceled") return;
      if (e.error === "not-allowed") {
        this.enableBtn.hidden = false;
        this.ackLater(st);
      } else this.sendAck(st.seq);
    };
    speechSynthesis.speak(u);
    // Some browsers never fire onend: the estimate (plus a margin, so a real
    // onend usually wins) is the backstop.
    clearTimeout(this.fallback);
    this.fallback = window.setTimeout(() => this.sendAck(st.seq), (st.durationMs ?? 0) + 4000);
  }

  /** The "Click to enable sound" button: plays the current line from a user gesture. */
  private unlock() {
    this.enableBtn.hidden = true;
    const st = this.step;
    // The click itself lets this page play sound from now on; replay the
    // current line if it's still due, otherwise the next one will play.
    if (!st || !st.text || this.current !== st.seq) return;
    if (st.audio) {
      clearTimeout(this.fallback);
      this.playAudio(st);
    } else if (st.browserVoice) {
      clearTimeout(this.fallback);
      this.speak(st, st.text);
    }
  }
}

interface Stepper {
  readonly position: { index: number; step: number; maxStep: number };
  next(): Promise<void>;
  prev(): Promise<void>;
  goto(index: number, step?: number, opts?: { animate?: boolean; direction?: 1 | -1 }): Promise<void>;
}

/** Moves a player the way an audience window follows the presenter: single steps animate, jumps cut. */
export async function followTo(player: Stepper, index: number, step: number): Promise<void> {
  const p = player.position;
  if (index === p.index && step === p.step + 1) await player.next();
  else if (index === p.index && step === p.step - 1) await player.prev();
  else if (index === p.index + 1 && step === 0 && p.step >= p.maxStep) await player.next();
  else if (index === p.index - 1 && p.step === 0 && step === Infinity) await player.prev();
  else if (index !== p.index || step !== p.step) await player.goto(index, step, { animate: index !== p.index });
}

/** The slide's index now (the deck may have changed since the event was sent). */
export function slideIndex(slides: { id: string }[], index: number, slideId: string): number {
  const i = slides.findIndex((s) => s.id === slideId);
  return i >= 0 ? i : index;
}
