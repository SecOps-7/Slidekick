// AI chat panel. The server runs the model and applies its changes through the
// same deck operations agents use, as the signed-in user; changes stream into
// the editor like any agent edit. Stop cancels the run; Undo restores the deck
// to the version from before the run (as a new version, so nothing is lost).
import { confirmModal } from "../ui/modal";
import { useEffect, useRef, useState } from "preact/hooks";
import { authHeaders } from "../auth";
import type { Api } from "./api";

interface Provider {
  id: string;
  name: string;
  models: string[];
}

interface Step {
  tool: string;
  ok?: boolean;
  note?: string;
}

interface Msg {
  role: "user" | "assistant";
  text: string;
  steps?: Step[];
  error?: string;
  run?: { start: number; end: number };
}

const PREF = "slidekick-ai-model";

const toolLabel: Record<string, string> = {
  get_deck: "Read the deck",
  add_slide: "Added a slide",
  update_slide: "Changed a slide",
  delete_slide: "Deleted a slide",
  move_slide: "Moved a slide",
  patch_deck: "Edited the deck",
  set_theme: "Changed the theme",
  list_themes: "Looked at themes",
  lint_deck: "Checked the deck",
  list_comments: "Read comments",
  resolve_comment: "Resolved a comment",
  get_history: "Looked at history",
  create_deck: "Created a deck",
  get_share_url: "Got the link",
};

export function ChatPanel({ api, version, onClose, say }: { api: Api; version: () => number; onClose: () => void; say: (m: string) => void }) {
  const [providers, setProviders] = useState<Provider[] | null>(null);
  const [choice, setChoice] = useState(() => {
    try {
      return localStorage.getItem(PREF) ?? "";
    } catch {
      return "";
    }
  });
  const [msgs, setMsgs] = useState<Msg[]>([]);
  const [input, setInput] = useState("");
  const [running, setRunning] = useState(false);
  const abort = useRef<AbortController | null>(null);
  const list = useRef<HTMLDivElement>(null);

  useEffect(() => {
    fetch("/api/ai/providers", { cache: "no-store" })
      .then((r) => (r.ok ? r.json() : { providers: [] }))
      .then((d: { providers: Provider[] }) => {
        setProviders(d.providers);
        const all = d.providers.flatMap((p) => p.models.map((m) => `${p.id}/${m}`));
        if (!all.includes(choice) && all.length) setChoice(all[0]);
      });
  }, []);
  useEffect(() => {
    list.current?.scrollTo({ top: list.current.scrollHeight });
  }, [msgs]);
  useEffect(() => {
    try {
      if (choice) localStorage.setItem(PREF, choice);
    } catch {
      /* storage blocked */
    }
  }, [choice]);

  const update = (f: (m: Msg) => Msg) => setMsgs((ms) => [...ms.slice(0, -1), f(ms[ms.length - 1])]);

  const send = async (e?: Event) => {
    e?.preventDefault();
    const text = input.trim();
    if (!text || running || !choice) return;
    const [provider, ...rest] = choice.split("/");
    const model = rest.join("/");
    const history = msgs.filter((m) => m.text).map((m) => ({ role: m.role, text: m.text }));
    setInput("");
    setMsgs((ms) => [...ms, { role: "user", text }, { role: "assistant", text: "", steps: [] }]);
    setRunning(true);
    const ctl = new AbortController();
    abort.current = ctl;
    let start = 0;
    try {
      const r = await fetch(`/api/decks/${encodeURIComponent(api.deckId)}/ai/chat`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders() },
        body: JSON.stringify({ provider, model, history, message: text }),
        signal: ctl.signal,
      });
      if (!r.ok || !r.body) {
        const d = await r.json().catch(() => ({}));
        throw new Error(d.error ?? `${r.status} ${r.statusText}`);
      }
      const reader = r.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        let i;
        while ((i = buf.indexOf("\n\n")) >= 0) {
          const chunk = buf.slice(0, i);
          buf = buf.slice(i + 2);
          if (!chunk.startsWith("data: ")) continue;
          const ev = JSON.parse(chunk.slice(6)) as { type: string; text?: string; tool?: string; ok?: boolean; version?: number; startVersion?: number };
          if (ev.type === "start") start = ev.startVersion ?? 0;
          else if (ev.type === "text") update((m) => ({ ...m, text: m.text + (ev.text ?? "") }));
          else if (ev.type === "tool") update((m) => ({ ...m, steps: [...(m.steps ?? []), { tool: ev.tool ?? "" }] }));
          else if (ev.type === "tool_result")
            update((m) => {
              const steps = [...(m.steps ?? [])];
              steps[steps.length - 1] = { tool: ev.tool ?? "", ok: ev.ok, note: ev.text };
              return { ...m, steps };
            });
          else if (ev.type === "done") update((m) => ({ ...m, text: m.text || ev.text || "", run: (ev.version ?? 0) > start ? { start, end: ev.version! } : undefined }));
          else if (ev.type === "error") update((m) => ({ ...m, error: ev.text }));
        }
      }
    } catch (x) {
      const stopped = (x as Error).name === "AbortError";
      update((m) => ({ ...m, error: stopped ? "Stopped." : (x as Error).message }));
    } finally {
      // A stopped run may still have changed the deck before it stopped.
      if (start && version() > start) update((m) => (m.run ? m : { ...m, run: { start, end: version() } }));
      setRunning(false);
      abort.current = null;
    }
  };

  const undo = async (m: Msg) => {
    if (!m.run) return;
    if (
      version() !== m.run.end &&
      !(await confirmModal({
        title: "Undo past later changes?",
        message: "The deck changed after this AI run. Undo restores it to how it was before the run, which also drops those later changes. They stay in the history.",
        confirm: "Undo anyway",
        danger: true,
      }))
    )
      return;
    try {
      await api.restore(version(), m.run.start);
      setMsgs((ms) => ms.map((x) => (x === m ? { ...x, run: undefined, text: `${x.text}\n\n(Undone.)` } : x)));
      say(`Restored version ${m.run.start}`);
    } catch (x) {
      say(`Undo failed: ${(x as Error).message}`);
    }
  };

  const options = (providers ?? []).flatMap((p) => p.models.map((m) => ({ value: `${p.id}/${m}`, label: `${p.name} · ${m}` })));

  return (
    <aside class="ed-chat" aria-label="AI assistant">
      <header>
        <b>AI assistant</b>
        {options.length > 0 && (
          <select value={choice} aria-label="Model" onChange={(e) => setChoice(e.currentTarget.value)} disabled={running}>
            {options.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        )}
        <button type="button" class="ed-chat-x" aria-label="Close" onClick={onClose}>
          ×
        </button>
      </header>
      <div class="ed-chat-list" ref={list} aria-live="polite">
        {providers && options.length === 0 && <p class="ed-chat-empty">No AI provider is set up yet. An admin can connect one under Settings → AI providers.</p>}
        {options.length > 0 && msgs.length === 0 && <p class="ed-chat-empty">Ask for a new slide, a restyle, tighter wording or a whole deck. Changes appear live; Undo puts the deck back.</p>}
        {msgs.map((m, i) => (
          <div key={i} class={`ed-chat-msg ${m.role}`}>
            {m.steps && m.steps.length > 0 && (
              <ul class="ed-chat-steps">
                {m.steps.map((s, j) => (
                  <li key={j} class={s.ok === false ? "bad" : s.ok ? "ok" : "busy"} title={s.note}>
                    {s.ok === false ? "✗" : s.ok ? "✓" : "…"} {toolLabel[s.tool] ?? s.tool}
                    {s.ok === false && s.note ? `: ${s.note}` : ""}
                  </li>
                ))}
              </ul>
            )}
            {m.text && <p>{m.text}</p>}
            {m.role === "assistant" && !m.text && !m.error && running && i === msgs.length - 1 && <p class="ed-chat-dots">Thinking…</p>}
            {m.error && <p class="ed-chat-err">{m.error}</p>}
            {m.run && !running && (
              <button type="button" class="ed-btn" onClick={() => undo(m)}>
                ↶ Undo these changes (back to v{m.run.start})
              </button>
            )}
          </div>
        ))}
      </div>
      <form class="ed-chat-input" onSubmit={send}>
        <textarea
          value={input}
          rows={3}
          placeholder={options.length ? "Ask the AI to change this deck…" : ""}
          disabled={!options.length}
          aria-label="Message to the AI"
          onInput={(e) => setInput(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) send(e);
          }}
        />
        {running ? (
          <button type="button" class="ed-btn" onClick={() => abort.current?.abort()}>
            ■ Stop
          </button>
        ) : (
          <button class="ed-btn primary" disabled={!input.trim() || !options.length}>
            Send
          </button>
        )}
      </form>
    </aside>
  );
}
