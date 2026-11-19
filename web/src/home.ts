// Deck list plus "connect your agent" instructions.

interface Summary {
  id: string;
  title: string;
  theme: string;
  version: number;
  slides: number;
  updatedAt: string;
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

export function mountHome(host: HTMLElement) {
  document.body.classList.add("home-page");
  document.title = "Slidekick";
  const page = el("main", "home");
  const head = el("header", "home-head");
  head.append(el("h1", undefined, "Slidekick"), el("p", undefined, "Agents write the deck. You watch it move."));

  const decks = el("section", "home-decks");
  decks.append(el("h2", undefined, "Decks"));
  const list = el("div", "deck-list");
  decks.append(list);

  const mcpURL = `${location.origin}/mcp`;
  const connect = el("section", "home-connect");
  connect.append(
    el("h2", undefined, "Connect an agent"),
    el("p", undefined, "Slidekick speaks MCP over streamable HTTP. Add it to Claude Code with:"),
  );
  const cmd = el("pre");
  cmd.textContent = `claude mcp add --transport http slidekick ${mcpURL} \\\n  --header "Authorization: Bearer $SLIDEKICK_TOKEN"`;
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
            el("span", "deck-meta", `${d.slides} slide${d.slides === 1 ? "" : "s"} · ${d.theme} · v${d.version} · ${ago(d.updatedAt)}`),
          );
          return a;
        }),
      );
    } catch {
      list.replaceChildren(el("p", "empty", "Can't reach the server."));
    }
  };
  load();
  setInterval(load, 5000);
}
