// Cast button: lists the cast devices an admin added, starts and stops
// casting this deck, and shows "Casting to Living room TV" from the deck's
// live {"type":"cast"} events (so every open editor and presenter agrees).
// Used by the editor toolbar and the presenter view.
import { authHeaders } from "./auth";
import type { LiveMsg } from "./sync";
import "./cast.css";

interface CastDevice {
  id: string;
  name: string;
}
interface Casting {
  deviceId: string;
  device: string;
}

export interface CastMenu {
  el: HTMLElement;
  /** Feed every live message here; cast events update the button. */
  onEvent(m: LiveMsg): void;
}

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
};

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(path, {
    method,
    cache: "no-store",
    headers: { ...authHeaders(), ...(body ? { "Content-Type": "application/json" } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw Object.assign(new Error(data.error ?? `${r.status} ${r.statusText}`), { status: r.status });
  return data as T;
}

/**
 * Makes the cast button. It stays hidden for people who can't cast this deck
 * (the server answers 403 for them).
 */
export function castMenu(deckId: string, opt: { buttonClass: string; up?: boolean; say?: (msg: string) => void }): CastMenu {
  const base = `/api/decks/${encodeURIComponent(deckId)}/cast`;
  const wrap = el("div", "cast-menu");
  wrap.hidden = true;
  const btn = el("button", opt.buttonClass, "Cast");
  btn.type = "button";
  btn.setAttribute("aria-haspopup", "menu");
  btn.setAttribute("aria-expanded", "false");
  btn.title = "Show this deck on a TV on your network";
  const pop = el("div", `cast-pop${opt.up ? " up" : ""}`);
  pop.setAttribute("role", "menu");
  pop.setAttribute("aria-label", "Cast to a TV");
  pop.hidden = true;
  wrap.append(btn, pop);

  let casting: Casting | null = null;
  let devices: CastDevice[] = [];
  let busy = "";
  let error = "";

  const paint = () => {
    btn.textContent = busy ? busy : casting ? `Casting to ${casting.device}` : "Cast";
    btn.classList.toggle("on", Boolean(casting));
    btn.disabled = Boolean(busy);
    if (pop.hidden) return;
    const items: HTMLElement[] = [el("h4", undefined, "Cast to a TV")];
    if (!devices.length) items.push(el("p", undefined, "No cast devices yet. An admin adds them under Settings, Cast devices."));
    for (const d of devices) {
      const on = casting?.deviceId === d.id;
      const b = el("button");
      b.type = "button";
      b.setAttribute("role", "menuitemradio");
      b.setAttribute("aria-checked", String(on));
      b.append(el("span", undefined, d.name), el("small", undefined, on ? "Showing" : ""));
      b.disabled = on || Boolean(busy);
      b.onclick = () => start(d);
      items.push(b);
    }
    if (casting) {
      const stop = el("button", "cast-stop", "Stop casting");
      stop.type = "button";
      stop.setAttribute("role", "menuitem");
      stop.disabled = Boolean(busy);
      stop.onclick = () => stopCast();
      items.push(stop);
    }
    if (error) items.push(el("p", "cast-err", error));
    pop.replaceChildren(...items);
  };

  const refresh = async () => {
    try {
      const r = await call<{ casting: Casting | null; devices: CastDevice[] }>("GET", base);
      casting = r.casting;
      devices = r.devices;
      wrap.hidden = false;
    } catch {
      wrap.hidden = true; // not allowed to cast this deck (or casting is unavailable)
    }
    paint();
  };

  const close = (focus = false) => {
    if (pop.hidden) return;
    pop.hidden = true;
    btn.setAttribute("aria-expanded", "false");
    if (focus) btn.focus();
  };
  const open = async () => {
    error = "";
    pop.hidden = false;
    btn.setAttribute("aria-expanded", "true");
    paint();
    await refresh();
    (pop.querySelector("button:not(:disabled)") as HTMLButtonElement | null)?.focus();
  };

  const start = async (d: CastDevice) => {
    busy = `Starting ${d.name}…`;
    error = "";
    paint();
    try {
      const r = await call<{ casting: Casting | null }>("POST", base, { deviceId: d.id });
      casting = r.casting ?? { deviceId: d.id, device: d.name };
      close(true);
      opt.say?.(`Casting to ${d.name}`);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      busy = "";
      paint();
    }
  };

  const stopCast = async () => {
    busy = "Stopping…";
    error = "";
    paint();
    try {
      await call("DELETE", base);
      casting = null;
      close(true);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      busy = "";
      paint();
    }
  };

  btn.onclick = () => (pop.hidden ? void open() : close());
  document.addEventListener("pointerdown", (e) => !wrap.contains(e.target as Node) && close());
  // Keys pressed in the menu are for the menu, not the page's shortcuts
  // (Enter or Space on a device must not also advance the slides).
  wrap.addEventListener("keydown", (e) => {
    e.stopPropagation();
    if (e.key === "Escape" && !pop.hidden) close(true);
  });
  void refresh();

  return {
    el: wrap,
    onEvent(m) {
      if (m.type !== "cast") return;
      if (m.state === "started") {
        casting = { deviceId: String(m.deviceId ?? ""), device: String(m.device ?? "TV") };
      } else if (m.state === "stopped") {
        if (casting) opt.say?.(`Stopped casting to ${casting.device}`);
        casting = null;
      }
      paint();
    },
  };
}
