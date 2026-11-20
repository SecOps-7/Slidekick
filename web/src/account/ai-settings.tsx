// Settings → AI providers (admins). Keys are write-only: the server says
// whether one is stored and never sends it back.
import { useEffect, useState } from "preact/hooks";
import { addSettingsTab, api, Err } from "./pages";

interface Provider {
  id: string;
  name: string;
  kind: string;
  baseUrl: string;
  hasKey: boolean;
  models: string[];
  enabled: boolean;
}

const kinds: Record<string, { label: string; base: string; hint: string }> = {
  openai: { label: "OpenAI", base: "https://api.openai.com/v1", hint: "API key from platform.openai.com" },
  anthropic: { label: "Anthropic", base: "https://api.anthropic.com", hint: "API key from console.anthropic.com" },
  google: { label: "Google Gemini", base: "https://generativelanguage.googleapis.com/v1beta", hint: "API key from aistudio.google.com" },
  "openai-compatible": { label: "OpenAI-compatible (Ollama, LM Studio, vLLM, LiteLLM…)", base: "", hint: "Key optional for local servers" },
};

const blank: Provider = { id: "", name: "", kind: "openai", baseUrl: "", hasKey: false, models: [], enabled: true };

function AIProviders() {
  const [list, setList] = useState<Provider[]>([]);
  const [edit, setEdit] = useState<Provider | null>(null);
  const [isNew, setIsNew] = useState(false);
  const [key, setKey] = useState<string | undefined>(undefined);
  const [modelsText, setModelsText] = useState("");
  const [found, setFound] = useState<string[]>([]);
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  const load = () => api<{ providers: Provider[] }>("GET", "/api/admin/ai").then((d) => setList(d.providers));
  useEffect(() => {
    load();
  }, []);
  const open = (p: Provider, fresh: boolean) => {
    setEdit(p);
    setIsNew(fresh);
    setKey(undefined);
    setModelsText(p.models.join("\n"));
    setFound([]);
    setErr("");
    setNote("");
  };
  const models = () =>
    modelsText
      .split(/\n|,/)
      .map((m) => m.trim())
      .filter(Boolean);
  const save = async (e?: Event) => {
    e?.preventDefault();
    setErr("");
    try {
      const p = await api<Provider>("PUT", `/api/admin/ai/${encodeURIComponent(edit!.id)}`, { ...edit!, apiKey: key, models: models() });
      setKey(undefined);
      setIsNew(false);
      setEdit(p);
      load();
      return true;
    } catch (x) {
      setErr((x as Error).message);
      return false;
    }
  };
  const fetchModels = async () => {
    if (!(await save())) return;
    setNote("Asking the provider for its models…");
    try {
      const d = await api<{ models: string[] }>("POST", `/api/admin/ai/${encodeURIComponent(edit!.id)}/models`);
      setFound(d.models);
      setNote(`${d.models.length} models found. Tick the ones people may use, then Save.`);
    } catch (x) {
      setNote("");
      setErr((x as Error).message);
    }
  };
  const chosen = new Set(models());
  const toggle = (m: string) => {
    if (chosen.has(m)) chosen.delete(m);
    else chosen.add(m);
    setModelsText([...chosen].join("\n"));
  };
  if (edit) {
    const k = kinds[edit.kind];
    return (
      <section>
        <h2>{isNew ? "Connect an AI provider" : `Edit ${edit.name}`}</h2>
        <Err msg={err} />
        {note && <p class="acc-ok">{note}</p>}
        <form class="acc-form" onSubmit={save}>
          <label>
            Provider
            <select value={edit.kind} disabled={!isNew} onChange={(e) => setEdit({ ...edit, kind: e.currentTarget.value, baseUrl: kinds[e.currentTarget.value].base })}>
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
            <input value={edit.baseUrl} placeholder={k.base || "http://172.16.5.9:11434/v1"} required={!k.base} onInput={(e) => setEdit({ ...edit, baseUrl: e.currentTarget.value })} />
            <small>Slidekick only ever connects to this host and port. Addresses on your own network are fine: only admins can set them.</small>
          </label>
          <label>
            API key
            <input type="password" autocomplete="off" value={key ?? ""} placeholder={edit.hasKey ? "Stored (encrypted). Type to replace." : k.hint} onInput={(e) => setKey(e.currentTarget.value)} />
          </label>
          <label>
            Models people may use <small>(one per line)</small>
            <textarea rows={4} value={modelsText} onInput={(e) => setModelsText(e.currentTarget.value)} />
          </label>
          {found.length > 0 && (
            <div class="acc-models">
              {found.map((m) => (
                <label class="acc-check" key={m}>
                  <input type="checkbox" checked={chosen.has(m)} onChange={() => toggle(m)} /> {m}
                </label>
              ))}
            </div>
          )}
          <label class="acc-check">
            <input type="checkbox" checked={edit.enabled} onChange={(e) => setEdit({ ...edit, enabled: e.currentTarget.checked })} /> Available in the editor's AI panel
          </label>
          <div class="acc-inline">
            <button class="acc-primary">Save</button>
            <button type="button" onClick={fetchModels}>
              Save and fetch models
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
    <section>
      <p class="acc-muted">
        Editors and admins can ask an AI to build and change decks in the editor. It works with the permissions of the person asking, and every change shows up live and can be
        undone.
      </p>
      <table class="acc-table">
        <tbody>
          {list.map((p) => (
            <tr key={p.id} class={p.enabled ? "" : "off"}>
              <td>
                <b>{p.name}</b>
                <div class="acc-muted">
                  {kinds[p.kind]?.label ?? p.kind} · {p.baseUrl}
                </div>
              </td>
              <td class="acc-muted">
                {p.models.length} model{p.models.length === 1 ? "" : "s"} · {p.hasKey ? "key stored" : "no key"}
              </td>
              <td class="acc-actions">
                <button class="acc-link" onClick={() => open(p, false)}>
                  Edit
                </button>
                <button class="acc-link danger" onClick={() => confirm(`Remove ${p.name}? Its key is deleted.`) && api("DELETE", `/api/admin/ai/${p.id}`).then(load)}>
                  Remove
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {list.length === 0 && <p class="acc-muted">No AI providers yet.</p>}
      <button class="acc-primary" onClick={() => open({ ...blank, baseUrl: kinds.openai.base }, true)}>
        Connect a provider
      </button>
    </section>
  );
}

addSettingsTab({ id: "ai", label: "AI providers", render: () => <AIProviders /> });
