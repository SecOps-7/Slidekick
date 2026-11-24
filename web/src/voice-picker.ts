// The voice picker shown before "Present with AI voice" starts: every voice
// from every provider an admin connected (by the names given in Settings >
// Voices), plus the presenting browser's own voices. It opens on the voice
// used last on this device, else the instance's default.
import { withKey } from "./key";
import { modalButtons, modalHeader, openModal } from "./ui/modal";

export interface VoiceChoice {
  provider: string; // "browser" for the presenting browser's own voices
  voice: string; // "" for the provider's (or browser's) default voice
}

interface PublicProvider {
  id: string;
  name: string;
  kind: string;
  defaultVoice: string;
  voices: { id: string; name: string }[];
  isDefault: boolean;
}

const LAST = "slidekick-narration-voice";
const SEP = "\u0000"; // between provider and voice in an option value

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
};

/** The browser's voices; browsers fill the list asynchronously, so wait briefly for it. */
function browserVoices(): Promise<SpeechSynthesisVoice[]> {
  if (typeof speechSynthesis === "undefined") return Promise.resolve([]);
  const now = speechSynthesis.getVoices();
  if (now.length) return Promise.resolve(now);
  return new Promise((resolve) => {
    const done = () => resolve(speechSynthesis.getVoices());
    speechSynthesis.addEventListener("voiceschanged", done, { once: true });
    setTimeout(done, 1000);
  });
}

function lastChoice(): VoiceChoice | null {
  try {
    const v = JSON.parse(localStorage.getItem(LAST) ?? "null") as VoiceChoice | null;
    return v && typeof v.provider === "string" && typeof v.voice === "string" ? v : null;
  } catch {
    return null;
  }
}

function remember(c: VoiceChoice) {
  try {
    localStorage.setItem(LAST, JSON.stringify(c));
  } catch {
    /* storage blocked: next time opens on the default */
  }
}

/** Asks which voice to present with. Resolves null when cancelled. */
export async function pickVoice(deckId: string): Promise<VoiceChoice | null> {
  const r = await fetch(withKey(`/api/decks/${encodeURIComponent(deckId)}/voices`), { cache: "no-store" });
  const data = r.ok ? ((await r.json()) as { providers: PublicProvider[]; default: VoiceChoice }) : null;
  const providers = data?.providers ?? [{ id: "browser", name: "Browser voices (free)", kind: "browser", defaultVoice: "", voices: [], isDefault: true }];
  const def = data?.default ?? { provider: "browser", voice: "" };
  const local = await browserVoices();

  // One option per voice, grouped by provider.
  const select = el("select", "skm-input");
  const values = new Set<string>();
  const add = (group: HTMLElement, p: PublicProvider, voice: string, label: string) => {
    const isDef = p.id === def.provider && (def.voice === voice || (!def.voice && voice === (p.kind === "browser" ? "" : p.defaultVoice)));
    const o = new Option(isDef ? `${label} (default)` : label, p.id + SEP + voice);
    values.add(o.value);
    group.append(o);
  };
  for (const p of providers) {
    const group = el("optgroup");
    group.label = p.name;
    if (p.kind === "browser") {
      add(group, p, "", "This browser's default voice");
      for (const v of local) add(group, p, v.name, `${v.name} (${v.lang})`);
    } else if (p.voices.length === 0) {
      add(group, p, "", `${p.name}: default voice`);
    } else {
      for (const v of p.voices) add(group, p, v.id, v.name);
    }
    select.append(group);
  }
  // Open on the voice used last here, else the default.
  const last = lastChoice();
  const want = [last && last.provider + SEP + last.voice, def.provider + SEP + def.voice, def.provider + SEP + (providers.find((p) => p.id === def.provider)?.defaultVoice ?? "")];
  select.value = want.find((v) => v && values.has(v)) ?? select.options[0]?.value ?? "";

  return openModal<VoiceChoice | null>(
    (dlg, id, done) => {
      const form = el("form", "skm-body");
      const field = el("label", "skm-field");
      field.append(el("span", "skm-label", "Voice"), select);
      const note = el("p", "skm-msg", "Browser voices speak on the screens presenting this deck (this one, the audience window or a cast TV), so each may sound a little different.");
      const b = modalButtons("Cancel", "Start presenting");
      form.append(...modalHeader({ title: "Present with AI voice", message: "The AI presenter talks through each slide in this voice." }, id), field, note, b.foot);
      form.addEventListener("submit", (e) => {
        e.preventDefault();
        const [provider, voice = ""] = select.value.split(SEP);
        const c = { provider, voice };
        remember(c);
        done(c);
      });
      b.cancel!.onclick = () => done(null);
      dlg.append(form);
      return select;
    },
    () => null,
  );
}

/** The AI sparkle: a large four-point star with two small ones. */
export function sparkleIcon(): SVGSVGElement {
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("class", "pr-sparkle");
  for (const d of [
    "M10 3.5c.5 3.6 2.9 6 6.5 6.5-3.6.5-6 2.9-6.5 6.5-.5-3.6-2.9-6-6.5-6.5 3.6-.5 6-2.9 6.5-6.5Z",
    "M18.5 14c.25 1.8 1.45 3 3.25 3.25-1.8.25-3 1.45-3.25 3.25-.25-1.8-1.45-3-3.25-3.25 1.8-.25 3-1.45 3.25-3.25Z",
    "M18 2.5c.17 1.2.97 2 2.17 2.17-1.2.17-2 .97-2.17 2.17-.17-1.2-.97-2-2.17-2.17 1.2-.17 2-.97 2.17-2.17Z",
  ]) {
    const p = document.createElementNS(ns, "path");
    p.setAttribute("d", d);
    p.setAttribute("fill", "currentColor");
    svg.append(p);
  }
  return svg;
}
