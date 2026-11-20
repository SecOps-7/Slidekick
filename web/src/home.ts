// Deck list plus "connect your agent" instructions.
import { accountMenu, authHeaders, goSignIn, me } from "./auth";
import { wordmarkSVG } from "./brand/brand";

interface Summary {
  id: string;
  title: string;
  theme: string;
  version: number;
  slides: number;
  updatedAt: string;
  ownerName?: string;
  canEdit?: boolean;
  canDelete?: boolean;
}

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
};

function ago(iso: string) {
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return new Date(iso).toLocaleDateString();
}

export async function mountHome(host: HTMLElement) {
  const who = await me();
  if (!who.signedIn) goSignIn();
  document.body.classList.add("home-page");
  document.title = "Slidekick";
  const page = el("main", "home");
  const head = el("header", "home-head");
  const top = el("div", "home-top");
  const h1 = el("h1", "home-logo");
  h1.innerHTML = wordmarkSVG(); // the SVG carries the accessible name "Slidekick"
  top.append(h1, accountMenu(who));
  head.append(top, el("p", undefined, "Agents write the deck. You watch it move."));

  const decks = el("section", "home-decks");
  const decksHead = el("div", "home-decks-head");
  decksHead.append(el("h2", undefined, "Decks"));
  if (who.can.create) {
    const nb = el("button", "new-deck", "New deck");
    nb.type = "button";
    nb.onclick = async () => {
      const title = prompt("Deck title", "Untitled deck");
      if (title === null) return;
      const r = await fetch("/api/decks", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeaders() },
        body: JSON.stringify({ title, slides: [{ layout: "title", regions: { title: [{ type: "heading", props: { text: title || "Untitled deck", level: 1 } }] } }] }),
      });
      const d = await r.json();
      if (r.ok) location.href = `/d/${encodeURIComponent(d.deckId)}/edit`;
      else alert(d.error ?? "Couldn't create the deck");
    };
    decksHead.append(nb);
  }
  decks.append(decksHead);
  const list = el("div", "deck-list");
  decks.append(list);

  const mcpURL = `${location.origin}/mcp`;
  const connect = el("section", "home-connect");
  connect.append(
    el("h2", undefined, "Connect an agent"),
    el("p", undefined, "Slidekick speaks MCP over streamable HTTP. Create a personal agent token on your Account page, then add it to Claude Code:"),
  );
  const cmd = el("pre");
  cmd.textContent = `claude mcp add --transport http slidekick ${mcpURL} \\\n  --header "Authorization: Bearer <your agent token>"`;
  const copy = el("button", "copy", "Copy");
  copy.type = "button";
  copy.onclick = async () => {
    await navigator.clipboard?.writeText(cmd.textContent ?? "");
    copy.textContent = "Copied";
    setTimeout(() => (copy.textContent = "Copy"), 1500);
  };
  const cmdWrap = el("div", "cmd");
  cmdWrap.append(cmd, copy);
  connect.append(
    cmdWrap,
    el("p", "hint", 'Then ask: "Make a 6-slide deck about our Q3 launch with Slidekick." Open the deck here and watch it build.'),
  );

  page.append(head, decks, connect);
  host.append(page);

  const load = async () => {
    try {
      const r = await fetch("/api/decks", { cache: "no-store" });
      if (r.status === 401) goSignIn();
      const { decks: rows } = (await r.json()) as { decks: Summary[] };
      if (!rows.length) {
        list.replaceChildren(el("p", "empty", "No decks yet. Connect an agent and ask it for one; it will show up here."));
        return;
      }
      list.replaceChildren(
        ...rows.map((d) => {
          const a = el("a", `deck-card theme-${d.theme}`);
          a.href = `/d/${encodeURIComponent(d.id)}`;
          a.append(
            el("span", "deck-title", d.title),
            el("span", "deck-meta", `${d.slides} slide${d.slides === 1 ? "" : "s"} · ${d.theme} · ${d.ownerName ? `${d.ownerName} · ` : ""}${ago(d.updatedAt)}`),
          );
          const card = el("div", "deck-card-wrap");
          const links = el("span", "deck-links");
          const actions: [string, string][] = [["Present", `/d/${encodeURIComponent(d.id)}/present`]];
          if (d.canEdit) actions.unshift(["Edit", `/d/${encodeURIComponent(d.id)}/edit`]);
          for (const [label, href] of actions) {
            const l = el("a", undefined, label);
            l.href = href;
            links.append(l);
          }
          if (d.canDelete) {
            const del = el("button", "deck-delete", "Delete");
            del.type = "button";
            del.title = `Delete "${d.title}"`;
            del.onclick = async () => {
              if (!confirm(`Delete "${d.title}" for good?\n\nIts slides, history, comments and links are removed and can't be restored. Anyone watching it is told it's gone.`)) return;
              del.disabled = true;
              const r = await fetch(`/api/decks/${encodeURIComponent(d.id)}`, { method: "DELETE", headers: authHeaders() });
              if (r.ok) card.remove();
              else {
                del.disabled = false;
                alert((await r.json().catch(() => ({}))).error ?? "Couldn't delete the deck");
              }
            };
            links.append(del);
          }
          card.append(a, links);
          return card;
        }),
      );
    } catch {
      list.replaceChildren(el("p", "empty", "Can't reach the server."));
    }
  };
  load();
  setInterval(load, 5000);
}
