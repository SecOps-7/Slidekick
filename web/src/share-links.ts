// Shared links: a viewer link watches a deck (no controls), a presenter link
// presents it without signing in. Either can expire; each copy makes a fresh
// link (see service.MintLink).
import { authHeaders } from "./auth";
import { copyText } from "./ui/clipboard";
import { modalButtons, modalHeader, openModal } from "./ui/modal";

export type LinkKind = "view" | "present";

/** The expiry choices (minutes; 0 never expires). The first is the default. */
export const LINK_TTLS: { minutes: number; label: string }[] = [
  { minutes: 0, label: "Don't expire" },
  { minutes: 30, label: "30 minutes" },
  { minutes: 60, label: "1 hour" },
  { minutes: 120, label: "2 hours" },
  { minutes: 240, label: "4 hours" },
  { minutes: 480, label: "8 hours" },
  { minutes: 720, label: "12 hours" },
  { minutes: 1440, label: "24 hours" },
];

export const LINK_KINDS: Record<LinkKind, { name: string; about: string }> = {
  view: { name: "Viewer link", about: "Watches the deck and follows the presenter, with no controls." },
  present: { name: "Presenter link", about: "Presents the deck without signing in: moves the slides for everyone watching, laser pointer, AI voice and casting." },
};

export async function mintLink(deckId: string, kind: LinkKind, minutes: number): Promise<{ url: string; expiresAt?: string }> {
  const r = await fetch(`/api/decks/${encodeURIComponent(deckId)}/links`, {
    method: "POST",
    cache: "no-store",
    headers: { ...authHeaders(), "Content-Type": "application/json" },
    body: JSON.stringify({ kind, minutes }),
  });
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error ?? `${r.status} ${r.statusText}`);
  return data as { url: string; expiresAt?: string };
}

export const expiryText = (expiresAt?: string) =>
  expiresAt ? `expires ${new Date(expiresAt).toLocaleString([], { weekday: "short", hour: "numeric", minute: "2-digit" })}` : "doesn't expire";

/** Makes a link and copies it. Returns what to tell the person ("" when the copy dialog already showed it). */
export async function copyShareLink(deckId: string, title: string, kind: LinkKind, minutes: number): Promise<string> {
  const l = await mintLink(deckId, kind, minutes);
  const { name } = LINK_KINDS[kind];
  const res = await copyText(l.url, `${name} for "${title}"`);
  return res === "copied" ? `${name} copied (${expiryText(l.expiresAt)})` : "";
}

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
};

/** The library's "Share links…" dialog: pick an expiry, copy either kind of link. */
export function shareLinksModal(o: { deckId: string; title: string; isPrivate: boolean; say: (m: string) => void }): Promise<void> {
  return openModal<void>(
    (dlg, id, done) => {
      const form = el("form", "skm-body");
      const field = el("label", "skm-field");
      const select = el("select", "skm-input");
      for (const t of LINK_TTLS) select.append(new Option(t.label, String(t.minutes)));
      field.append(el("span", "skm-label", "Links expire"), select);
      const rows = (Object.keys(LINK_KINDS) as LinkKind[]).map((kind) => {
        const row = el("div", "skm-link");
        const text = el("div");
        const about = kind === "view" && !o.isPrivate ? `${LINK_KINDS.view.about} This deck is public, so its viewer link never expires.` : LINK_KINDS[kind].about;
        text.append(el("b", undefined, LINK_KINDS[kind].name), el("small", undefined, about));
        const btn = el("button", "skm-btn", "Copy");
        btn.type = "button";
        btn.onclick = async () => {
          btn.disabled = true;
          try {
            const msg = await copyShareLink(o.deckId, o.title, kind, Number(select.value));
            if (msg) o.say(msg);
          } catch (err) {
            o.say(`Couldn't make the link: ${err instanceof Error ? err.message : String(err)}`);
          } finally {
            btn.disabled = false;
          }
        };
        row.append(text, btn);
        return row;
      });
      const b = modalButtons(null, "Done");
      form.append(...modalHeader({ title: `Share "${o.title}"` }, id), field, ...rows, b.foot);
      form.addEventListener("submit", (e) => {
        e.preventDefault();
        done();
      });
      dlg.append(form);
      return select;
    },
    () => undefined,
  );
}
