// Settings → Voices (admins): text-to-speech providers for narration. Keys are
// write-only: the server says whether one is stored and never sends it back.
// "Browser voices (free)" is built in: the presenter's browser speaks with
// window.speechSynthesis, so it needs no key and the server never calls it.
import { confirmModal } from "../ui/modal";
import { useEffect, useState } from "preact/hooks";
import { authHeaders } from "../auth";
import { addSettingsTab, api, Err } from "./pages";

interface Provider {
  id: string;
  name: string;
  kind: string;
  baseUrl: string;
  hasKey: boolean;
  defaultVoice: string;
  defaultModel: string;
  builtIn: boolean;
  isDefault: boolean;
}

interface Voice {
  id: string;
  name: string;
  lang?: string;
}

interface Default {
  provider: string;
  voice: string;
}

const kinds: Record<string, { label: string; base: string; model: string; hint: string; models?: string }> = {
  fish: { label: "Fish Audio", base: "https://api.fish.audio", model: "s2.1-pro", hint: "API key from fish.audio", models: "s1, s2-pro, s2.1-pro, s2.1-pro-free" },
  elevenlabs: { label: "ElevenLabs", base: "https://api.elevenlabs.io", model: "eleven_multilingual_v2", hint: "API key from elevenlabs.io" },
  openai: { label: "OpenAI", base: "https://api.openai.com/v1", model: "gpt-4o-mini-tts", hint: "API key from platform.openai.com", models: "gpt-4o-mini-tts, tts-1, tts-1-hd" },
  cartesia: { label: "Cartesia", base: "https://api.cartesia.ai", model: "sonic-3.6", hint: "API key from play.cartesia.ai" },
  deepgram: { label: "Deepgram Aura", base: "https://api.deepgram.com", model: "", hint: "API key from console.deepgram.com" },
  "openai-compatible": { label: "OpenAI-compatible (self-hosted)", base: "", model: "tts-1", hint: "Key optional for local servers", models: "Kokoro-FastAPI: kokoro · openedai-speech: tts-1 · LocalAI: your model name" },
};

const kindLabel = (k: string) => (k === "browser" ? "Built into the presenter's browser" : (kinds[k]?.label ?? k));

const blank: Provider = { id: "", name: "", kind: "openai", baseUrl: kinds.openai.base, hasKey: false, defaultVoice: "", defaultModel: "", builtIn: false, isDefault: false };

const browserVoices = (): Voice[] =>
  typeof speechSynthesis === "undefined" ? [] : speechSynthesis.getVoices().map((v) => ({ id: v.name, name: v.name, lang: v.lang }));

const SAMPLE = "Hello! This is how this voice sounds when Slidekick reads your speaker notes.";

let playing: HTMLAudioElement | null = null;

/** Plays a sample of a voice: the browser speaks itself; providers are asked through the server. */
async function playSample(provider: string, voice: string): Promise<void> {
  playing?.pause();
  if (provider === "browser") {
    if (typeof speechSynthesis === "undefined") throw new Error("This browser can't speak.");
    speechSynthesis.cancel();
    const u = new SpeechSynthesisUtterance(SAMPLE);
    const v = speechSynthesis.getVoices().find((x) => x.name === voice);
    if (v) u.voice = v;
    speechSynthesis.speak(u);
    return;
  }
  const r = await fetch(`/api/admin/tts/${encodeURIComponent(provider)}/test`, {
    method: "POST",
    cache: "no-store",
    headers: { ...authHeaders(), "Content-Type": "application/json" },
    body: JSON.stringify({ voice }),
  });
  if (!r.ok) {
    const d = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(d.error ?? `${r.status} ${r.statusText}`);
  }
  const url = URL.createObjectURL(await r.blob());
  playing = new Audio(url);
  playing.addEventListener("ended", () => URL.revokeObjectURL(url), { once: true });
  await playing.play();
}

async function loadVoices(provider: string): Promise<Voice[]> {
  if (provider === "browser") return browserVoices();
  const d = await api<{ voices: Voice[] }>("GET", `/api/tts/voices?provider=${encodeURIComponent(provider)}`);
  return d.voices;
}

function VoicePicker({ provider, value, onChange, voices }: { provider: string; value: string; onChange: (v: string) => void; voices: Voice[] }) {
  const known = voices.some((v) => v.id === value);
  if (voices.length === 0)
    return <input value={value} placeholder={provider === "browser" ? "The browser's default voice" : "Voice ID"} onInput={(e) => onChange(e.currentTarget.value)} />;
  return (
    <select value={value} onChange={(e) => onChange(e.currentTarget.value)}>
      <option value="">{provider === "browser" ? "The browser's default voice" : "The provider's default voice"}</option>
      {!known && value && <option value={value}>{value}</option>}
      {voices.map((v) => (
        <option key={v.id} value={v.id}>
          {v.name}
          {v.lang ? ` (${v.lang})` : ""}
        </option>
      ))}
    </select>
  );
}

function NarrationDefault({ list, def, onSaved }: { list: Provider[]; def: Default; onSaved: () => void }) {
  const [provider, setProvider] = useState(def.provider);
  const [voice, setVoice] = useState(def.voice);
  const [voices, setVoices] = useState<Voice[]>([]);
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  useEffect(() => {
    setProvider(def.provider);
    setVoice(def.voice);
  }, [def.provider, def.voice]);
  useEffect(() => {
    setVoices([]);
    setErr("");
    loadVoices(provider)
      .then(setVoices)
      .catch((x) => setErr((x as Error).message));
    if (provider === "browser" && typeof speechSynthesis !== "undefined") {
      // Browsers fill the list asynchronously.
      const again = () => setVoices(browserVoices());
      speechSynthesis.addEventListener("voiceschanged", again);
      return () => speechSynthesis.removeEventListener("voiceschanged", again);
    }
  }, [provider]);
  const save = async () => {
    setErr("");
    try {
      await api("POST", `/api/admin/tts/${encodeURIComponent(provider)}/default`, { voice });
      setNote("Saved. Narration uses this voice unless a deck picks another.");
      onSaved();
    } catch (x) {
      setErr((x as Error).message);
    }
  };
  return (
    <section>
      <h2>Default for narration</h2>
      <Err msg={err} />
      {note && <p class="acc-ok">{note}</p>}
      <div class="acc-form">
        <label>
          Provider
          <select value={provider} onChange={(e) => { setProvider(e.currentTarget.value); setVoice(""); setNote(""); }}>
            {list.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Voice
          <VoicePicker provider={provider} value={voice} onChange={(v) => { setVoice(v); setNote(""); }} voices={voices} />
          {provider === "browser" && <small>Each browser has its own voices. If the presenter's browser lacks this one, it uses its default.</small>}
        </label>
        <div class="acc-inline">
          <button class="acc-primary" onClick={save}>
            Save default
          </button>
          <button type="button" onClick={() => playSample(provider, voice).catch((x) => setErr((x as Error).message))}>
            Play sample
          </button>
        </div>
      </div>
    </section>
  );
}

function Voices() {
  const [list, setList] = useState<Provider[]>([]);
  const [def, setDef] = useState<Default>({ provider: "browser", voice: "" });
  const [edit, setEdit] = useState<Provider | null>(null);
  const [isNew, setIsNew] = useState(false);
  const [key, setKey] = useState<string | undefined>(undefined);
  const [voices, setVoices] = useState<Voice[]>([]);
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  const load = () =>
    api<{ providers: Provider[]; default: Default }>("GET", "/api/admin/tts").then((d) => {
      setList(d.providers);
      setDef(d.default);
    });
  useEffect(() => {
    load();
  }, []);
  const open = (p: Provider, fresh: boolean) => {
    setEdit(p);
    setIsNew(fresh);
    setKey(undefined);
    setVoices([]);
    setErr("");
    setNote("");
  };
  const save = async (e?: Event) => {
    e?.preventDefault();
    setErr("");
    try {
      const p = await api<Provider>("PUT", `/api/admin/tts/${encodeURIComponent(edit!.id)}`, { ...edit!, apiKey: key });
      setKey(undefined);
      setIsNew(false);
      setEdit({ ...edit!, ...p });
      load();
      return true;
    } catch (x) {
      setErr((x as Error).message);
      return false;
    }
  };
  const fetchVoices = async () => {
    if (!(await save())) return;
    setNote("Asking the provider for its voices…");
    try {
      const vs = await loadVoices(edit!.id);
      setVoices(vs);
      setNote(vs.length ? `${vs.length} voices found. Pick the default voice, then Save.` : "The provider listed no voices. Type a voice ID instead.");
    } catch (x) {
      setNote("");
      setErr((x as Error).message);
    }
  };
  const sample = async () => {
    if (!(await save())) return;
    setNote("Playing a sample…");
    try {
      await playSample(edit!.id, edit!.defaultVoice);
      setNote("");
    } catch (x) {
      setNote("");
      setErr((x as Error).message);
    }
  };
  const makeDefault = (p: Provider) => api("POST", `/api/admin/tts/${encodeURIComponent(p.id)}/default`, { voice: p.builtIn ? "" : p.defaultVoice }).then(load);

  if (edit) {
    const k = kinds[edit.kind];
    return (
      <section>
        <h2>{isNew ? "Connect a voice provider" : `Edit ${edit.name}`}</h2>
        <Err msg={err} />
        {note && <p class="acc-ok">{note}</p>}
        <form class="acc-form" onSubmit={save}>
          <label>
            Provider
            <select value={edit.kind} disabled={!isNew} onChange={(e) => setEdit({ ...edit, kind: e.currentTarget.value, baseUrl: kinds[e.currentTarget.value].base, defaultModel: "" })}>
              {Object.entries(kinds).map(([id, v]) => (
                <option key={id} value={id}>
                  {v.label}
                </option>
              ))}
            </select>
          </label>
          <label>
            ID <small>(short slug)</small>
            <input value={edit.id} required pattern="[a-z0-9][a-z0-9\-]{0,31}" disabled={!isNew} onInput={(e) => setEdit({ ...edit, id: e.currentTarget.value })} />
          </label>
          <label>
            Name shown to people
            <input value={edit.name} required onInput={(e) => setEdit({ ...edit, name: e.currentTarget.value })} />
          </label>
          <label>
            Base URL
            <input value={edit.baseUrl} placeholder={k.base || "http://172.16.5.9:8880/v1"} required={!k.base} onInput={(e) => setEdit({ ...edit, baseUrl: e.currentTarget.value })} />
            <small>
              {edit.kind === "openai-compatible"
                ? "The server's OpenAI-style base, ending in /v1 (Kokoro-FastAPI listens on port 8880). "
                : ""}
              Slidekick only ever connects to this host and port. Addresses on your own network are fine: only admins can set them.
            </small>
          </label>
          <label>
            API key
            <input type="password" autocomplete="off" value={key ?? ""} placeholder={edit.hasKey ? "Stored (encrypted). Type to replace." : k.hint} onInput={(e) => setKey(e.currentTarget.value)} />
          </label>
          {edit.kind !== "deepgram" && (
            <label>
              Model <small>(blank for {k.model || "the provider's default"})</small>
              <input value={edit.defaultModel} placeholder={k.model} onInput={(e) => setEdit({ ...edit, defaultModel: e.currentTarget.value })} />
              {k.models && <small>{k.models}</small>}
            </label>
          )}
          <label>
            Default voice {edit.kind === "deepgram" && <small>(the voice is the Aura model, e.g. aura-2-thalia-en)</small>}
            <VoicePicker provider={edit.id} value={edit.defaultVoice} onChange={(v) => setEdit({ ...edit, defaultVoice: v })} voices={voices} />
          </label>
          <div class="acc-inline">
            <button class="acc-primary">Save</button>
            <button type="button" onClick={fetchVoices}>
              Save and load voices
            </button>
            <button type="button" onClick={sample}>
              Play sample
            </button>
            <button type="button" onClick={() => setEdit(null)}>
              Done
            </button>
          </div>
        </form>
      </section>
    );
  }
  return (
    <>
      <section>
        <p class="acc-muted">
          Voices speak the AI presenter's narration when a deck is presented with a voice. Browser voices are free and need nothing set up; a provider gives a more natural voice and is called from this
          server, so its key never reaches anyone's browser.
        </p>
        <table class="acc-table">
          <tbody>
            {list.map((p) => (
              <tr key={p.id}>
                <td>
                  <b>{p.name}</b> {p.isDefault && <span class="acc-tag">Default for narration</span>}
                  <div class="acc-muted">
                    {kindLabel(p.kind)}
                    {p.builtIn ? "" : ` · ${p.baseUrl}`}
                  </div>
                </td>
                <td class="acc-muted">{p.builtIn ? "no key needed" : `${p.defaultVoice ? `voice ${p.defaultVoice}` : "default voice"} · ${p.hasKey ? "key stored" : "no key"}`}</td>
                <td class="acc-actions">
                  {!p.isDefault && (
                    <button class="acc-link" onClick={() => makeDefault(p).catch((x) => setErr((x as Error).message))}>
                      Make default
                    </button>
                  )}
                  {!p.builtIn && (
                    <>
                      <button class="acc-link" onClick={() => open({ ...p }, false)}>
                        Edit
                      </button>
                      <button class="acc-link danger" onClick={async () =>
                          (await confirmModal({
                            title: `Remove ${p.name}?`,
                            message: p.isDefault ? "Its stored key is deleted and narration goes back to browser voices." : "Its stored key is deleted and narration can no longer use it.",
                            confirm: "Remove provider",
                            danger: true,
                          })) && api("DELETE", `/api/admin/tts/${encodeURIComponent(p.id)}`).then(load)
                        }>
                        Remove
                      </button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <Err msg={err} />
        <button class="acc-primary" onClick={() => open({ ...blank }, true)}>
          Connect a provider
        </button>
      </section>
      {list.length > 0 && <NarrationDefault list={list} def={def} onSaved={load} />}
    </>
  );
}

addSettingsTab({ id: "voices", label: "Voices", render: () => <Voices /> });
