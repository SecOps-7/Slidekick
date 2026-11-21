// The deck library. Each card is a live miniature of the deck's first slide,
// drawn by the real renderer in the deck's own theme (fonts, colours,
// backdrop), so the page reads as a light table of your decks. The chrome
// around the covers stays quiet.
import { copyText } from "./ui/clipboard";
import { confirmModal, promptModal } from "./ui/modal";
import { applyTheme, renderSlide, STAGE } from "../runtime/render";
import type { Catalog, Slide, Theme } from "../runtime/types";
import { authHeaders, goSignIn, me, signOut, type Me } from "./auth";
import { wordmarkSVG } from "./brand/brand";
import "./home.css";

interface Summary {
  id: string;
  title: string;
  theme: string;
  version: number;
  slides: number;
  updatedAt: string;
  owner?: string;
  ownerName?: string;
  private?: boolean;
  canEdit?: boolean;
  canDelete?: boolean;
}

interface Cover {
  version: number;
  theme: string;
  aspect: "16:9" | "4:3";
  slide: Slide | null;
}

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
};

function ago(iso: string) {
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 60) return "edited just now";
  if (s < 3600) return `edited ${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `edited ${Math.floor(s / 3600)} h ago`;
  if (s < 86400 * 7) return `edited ${Math.floor(s / 86400)} d ago`;
  return `edited ${new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" })}`;
}

const GLOBE = `<svg viewBox="0 0 16 16" aria-hidden="true"><g fill="none" stroke="currentColor" stroke-width="1.4"><circle cx="8" cy="8" r="6.2"/><path d="M1.8 8h12.4M8 1.8c1.8 1.7 2.7 3.8 2.7 6.2S9.8 12.5 8 14.2C6.2 12.5 5.3 10.4 5.3 8S6.2 3.5 8 1.8Z"/></g></svg>`;
const DOTS = `<svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="3" cy="8" r="1.5" fill="currentColor"/><circle cx="8" cy="8" r="1.5" fill="currentColor"/><circle cx="13" cy="8" r="1.5" fill="currentColor"/></svg>`;

// ---- covers: drawn once per deck version, reused across refreshes ----

const covers = new Map<string, HTMLElement>(); // id@version -> stage element
let catalog: Catalog | null = null;

const coverKey = (d: Summary) => `${d.id}@${d.version}`;

async function drawCover(frame: HTMLElement, d: Summary) {
  const key = coverKey(d);
  let stage = covers.get(key);
  if (!stage) {
    const r = await fetch(`/api/decks/${encodeURIComponent(d.id)}/cover?v=${d.version}`);
    if (!r.ok || !catalog) return;
    const c = (await r.json()) as Cover;
    const theme: Theme | undefined = catalog.themes[c.theme] ?? catalog.themes.aurora;
    const [sw, sh] = STAGE[c.aspect] ?? STAGE["16:9"];
    stage = el("div", "dk-stage");
    stage.style.width = `${sw}px`;
    stage.style.height = `${sh}px`;
    if (theme) applyTheme(stage, theme);
    if (c.slide) {
      const deckish = { schemaVersion: 1, id: d.id, title: d.title, theme: c.theme, aspect: c.aspect, slides: [c.slide] };
      stage.append(renderSlide(deckish, c.slide, catalog.layouts[c.slide.layout]?.regions ?? []).el);
    } else {
      stage.append(el("div", "dk-blank", "No slides yet"));
    }
    covers.set(key, stage);
  }
  frame.replaceChildren(stage);
  fit(frame);
}

// Scale each fixed-size stage to its frame's width.
function fit(frame: HTMLElement) {
  const stage = frame.firstElementChild as HTMLElement | null;
  if (!stage || !stage.style.width) return;
  frame.style.setProperty("--dk-scale", String(frame.clientWidth / parseFloat(stage.style.width)));
}
const ro = new ResizeObserver((entries) => entries.forEach((e) => fit(e.target as HTMLElement)));

// Covers are drawn as they scroll into view.
const io = new IntersectionObserver(
  (entries) => {
    for (const e of entries) {
      if (!e.isIntersecting) continue;
      io.unobserve(e.target);
      const frame = e.target as HTMLElement & { _deck?: Summary };
      if (frame._deck) drawCover(frame, frame._deck);
    }
  },
  { rootMargin: "300px" },
);

// ---- pieces ----

function swatch(theme: Theme | undefined): HTMLElement {
  const s = el("span", "dk-swatch");
  s.setAttribute("aria-hidden", "true");
  const c = theme?.tokens.color;
  for (const k of ["bg", "text", "accent", "accent2"] as const) {
    const dot = el("i");
    dot.style.background = c?.[k] ?? "#888";
    s.append(dot);
  }
  return s;
}

type Item = [string, () => void, string?];

function menu(label: string, html: string, items: Item[]): HTMLElement {
  const wrap = el("div", "dk-menu");
  const id = `m-${Math.random().toString(36).slice(2)}`;
  const btn = el("button", "dk-menu-btn");
  btn.type = "button";
  btn.innerHTML = html;
  btn.setAttribute("aria-label", label);
  btn.setAttribute("popovertarget", id);
  const pop = el("div", "dk-pop");
  pop.id = id;
  pop.setAttribute("popover", "");
  pop.setAttribute("role", "menu");
  for (const [text, fn, cls] of items) {
    const b = el("button", cls, text);
    b.type = "button";
    b.setAttribute("role", "menuitem");
    b.onclick = () => {
      (pop as HTMLElement & { hidePopover?: () => void }).hidePopover?.();
      fn();
    };
    pop.append(b);
  }
  // Place the popover under its button, right-aligned, kept inside the window.
  pop.addEventListener("toggle", (e) => {
    if ((e as ToggleEvent).newState !== "open") return;
    const b = btn.getBoundingClientRect();
    const w = pop.offsetWidth;
    const h = pop.offsetHeight;
    pop.style.inset = "auto";
    pop.style.left = `${Math.max(8, Math.min(b.right - w, innerWidth - w - 8))}px`;
    pop.style.top = `${b.bottom + 6 + h > innerHeight - 8 ? Math.max(8, b.top - h - 6) : b.bottom + 6}px`;
  });
  wrap.append(btn, pop);
  return wrap;
}

function card(d: Summary, say: (m: string) => void, onDeleted: () => void): HTMLElement {
  const theme = catalog?.themes[d.theme];
  const art = el("article", "dk-card");
  const view = `/d/${encodeURIComponent(d.id)}`;

  const frame = el("a", "dk-cover") as HTMLAnchorElement & { _deck?: Summary };
  frame.href = view;
  frame.setAttribute("aria-label", `Open ${d.title}`);
  frame._deck = d;
  const stage = covers.get(coverKey(d));
  if (stage) {
    frame.append(stage);
    requestAnimationFrame(() => fit(frame));
  } else io.observe(frame);
  ro.observe(frame);

  const actions = el("div", "dk-actions");
  if (d.canEdit) {
    const edit = el("a", "dk-act primary", "Edit");
    edit.href = `${view}/edit`;
    actions.append(edit);
  }
  const present = el("a", "dk-act", "Present");
  present.href = `${view}/present`;
  actions.append(present);

  const coverWrap = el("div", "dk-cover-wrap");
  coverWrap.append(frame, actions);
  // New decks are private, so mark the exception: a deck anyone with the link can watch.
  if (!d.private) {
    const pub = el("span", "dk-public");
    pub.innerHTML = GLOBE;
    pub.title = "Public: anyone with the link can watch";
    pub.setAttribute("role", "img");
    pub.setAttribute("aria-label", "Public link");
    coverWrap.append(pub);
  }

  const title = el("h3", "dk-title");
  const tl = el("a", undefined, d.title);
  tl.href = view;
  title.append(tl);
  const meta = el("p", "dk-meta");
  const themeName = el("span", "dk-theme");
  themeName.append(swatch(theme), document.createTextNode(theme?.label ?? d.theme));
  const when = el("span", "dk-when", `${d.slides} slide${d.slides === 1 ? "" : "s"}, ${ago(d.updatedAt)}${d.ownerName ? ` by ${d.ownerName}` : ""}`);
  meta.append(themeName, when);

  const items: Item[] = [
    [
      "Copy link",
      async () => {
        // A private deck's plain address only works for people who sign in; copy its private link when we may.
        let link = location.origin + view;
        let note = "";
        if (d.private && d.canEdit) {
          const r = await fetch(`/api/decks/${encodeURIComponent(d.id)}/access`, { headers: authHeaders(), cache: "no-store" });
          if (r.ok) link = ((await r.json()) as { shareUrl?: string }).shareUrl || link;
        } else if (d.private) note = " (people need to sign in to open it)";
        if ((await copyText(link, `Link to "${d.title}"`)) === "copied") say(`Copied the link to "${d.title}"${note}`);
      },
    ],
  ];
  if (d.canDelete)
    items.push([
      "Delete deck…",
      async () => {
        const ok = await confirmModal({
          title: `Delete "${d.title}"?`,
          message: "Its slides, history, comments and links are removed for good and can't be restored. Anyone watching it is told it's gone.",
          confirm: "Delete deck",
          danger: true,
        });
        if (!ok) return;
        const r = await fetch(`/api/decks/${encodeURIComponent(d.id)}`, { method: "DELETE", headers: authHeaders() });
        if (r.ok) {
          say(`Deleted "${d.title}"`);
          onDeleted();
        } else say((await r.json().catch(() => ({}))).error ?? "Couldn't delete the deck");
      },
      "danger",
    ]);
  const row = el("div", "dk-info-row");
  row.append(title, menu(`More actions for ${d.title}`, DOTS, items));
  const info = el("div", "dk-info");
  info.append(row, meta);
  art.append(coverWrap, info);
  return art;
}

function connectDialog(): HTMLDialogElement {
  const dlg = el("dialog", "dk-dialog");
  dlg.setAttribute("aria-labelledby", "dk-connect-title");
  const h = el("h2", undefined, "Connect an agent");
  h.id = "dk-connect-title";
  const cmdText = `claude mcp add --transport http slidekick ${location.origin}/mcp \\\n  --header "Authorization: Bearer <your agent token>"`;
  const pre = el("pre", "dk-cmd", cmdText);
  const copy = el("button", "dk-btn", "Copy command");
  copy.type = "button";
  copy.onclick = async () => {
    if ((await copyText(cmdText, "Command to add Slidekick")) === "copied") copy.textContent = "Copied";
  };
  const close = el("button", "dk-btn ghost", "Close");
  close.type = "button";
  close.onclick = () => dlg.close();
  const steps = el("ol", "dk-steps");
  const li1 = el("li");
  li1.append("Create a personal agent token on your ");
  const acc = el("a", undefined, "Account page");
  acc.href = "/account";
  li1.append(acc, ". The agent acts as you, with your permissions.");
  const li2 = el("li");
  li2.append("Add Slidekick to Claude Code (other MCP clients work the same way):", pre);
  const li3 = el("li", undefined, 'Ask: "Make a 6-slide deck about our Q3 launch with Slidekick." Open the deck here and watch it build.');
  steps.append(li1, li2, li3);
  const foot = el("div", "dk-dialog-foot");
  foot.append(copy, close);
  dlg.append(h, steps, foot);
  dlg.addEventListener("click", (e) => e.target === dlg && dlg.close());
  return dlg;
}

function accountButton(who: Me): HTMLElement {
  if (!who.user) {
    const wrap = el("div", "dk-account");
    if (who.openMode) {
      const a = el("a", "dk-warn", "Set up accounts");
      a.href = "/setup";
      a.title = "Anyone who can reach this server can edit everything until an admin account exists.";
      wrap.append(a);
    }
    return wrap;
  }
  const u = who.user;
  const initial = (u.name || u.username).trim().charAt(0).toUpperCase().replace(/[<&>"]/g, "");
  const items: Item[] = [["Account", () => location.assign("/account")]];
  if (who.can["manage-users"]) items.push(["Settings", () => location.assign("/settings")]);
  items.push(["Sign out", () => signOut()]);
  const m = menu(`Account menu for ${u.username}`, `<span class="dk-avatar">${initial}</span>`, items);
  m.classList.add("dk-account");
  const head = el("div", "dk-pop-head");
  head.append(el("b", undefined, u.name || u.username), el("span", undefined, u.role));
  m.querySelector(".dk-pop")!.prepend(head);
  return m;
}

// ---- page ----

export async function mountHome(host: HTMLElement) {
  const who = await me();
  if (!who.signedIn) goSignIn();
  document.body.classList.add("home-page");
  document.title = "Your decks · Slidekick";
  catalog = await fetch("/api/catalog").then((r) => r.json() as Promise<Catalog>);

  const page = el("div", "dk");
  const bar = el("header", "dk-bar");
  const logo = el("a", "dk-logo");
  logo.href = "/";
  logo.innerHTML = wordmarkSVG();
  const search = el("input", "dk-search");
  search.type = "search";
  search.placeholder = "Search decks";
  search.setAttribute("aria-label", "Search decks by title (press / to focus)");
  const dlg = connectDialog();
  const connectBtn = el("button", "dk-btn ghost", "Connect an agent");
  connectBtn.type = "button";
  connectBtn.onclick = () => dlg.showModal();
  const tools = el("div", "dk-bar-tools");
  tools.append(search, connectBtn);
  if (who.can.create) {
    const nb = el("button", "dk-btn primary", "New deck");
    nb.type = "button";
    nb.onclick = () => newDeck();
    tools.append(nb);
  }
  tools.append(accountButton(who));
  bar.append(logo, tools);

  const head = el("div", "dk-head");
  const h1 = el("h1", undefined, "Your decks");
  const count = el("span", "dk-count");
  h1.append(" ", count);
  const sort = el("select", "dk-sort");
  sort.setAttribute("aria-label", "Sort decks");
  for (const [v, t] of [
    ["edited", "Last edited"],
    ["title", "Title"],
    ["theme", "Theme"],
  ])
    sort.append(new Option(t, v));
  const filter = el("div", "dk-filter");
  filter.setAttribute("role", "group");
  filter.setAttribute("aria-label", "Show");
  let mineOnly = false;
  const fAll = el("button", "on", "All");
  const fMine = el("button", undefined, "Mine");
  for (const [b, v] of [
    [fAll, false],
    [fMine, true],
  ] as const) {
    b.type = "button";
    b.setAttribute("aria-pressed", String(!v));
    b.onclick = () => {
      mineOnly = v;
      fAll.classList.toggle("on", !v);
      fMine.classList.toggle("on", v);
      fAll.setAttribute("aria-pressed", String(!v));
      fMine.setAttribute("aria-pressed", String(v));
      draw();
    };
  }
  filter.append(fAll, fMine);
  const controls = el("div", "dk-controls");
  if (who.user) controls.append(filter);
  controls.append(sort);
  head.append(h1, controls);

  const grid = el("div", "dk-grid");
  const toast = el("div", "dk-toast");
  toast.setAttribute("role", "status");
  let toastTimer = 0;
  const say = (m: string) => {
    toast.textContent = m;
    toast.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = window.setTimeout(() => toast.classList.remove("show"), 2600);
  };

  page.append(bar, head, grid);
  host.append(page, dlg, toast);

  let rows: Summary[] = [];
  let loaded = false;

  async function newDeck() {
    const title = await promptModal({ title: "New deck", label: "Title", value: "Untitled deck", confirm: "Create deck" });
    if (title === null) return;
    const r = await fetch("/api/decks", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...authHeaders() },
      body: JSON.stringify({ title, slides: [{ layout: "title", regions: { title: [{ type: "heading", props: { text: title || "Untitled deck", level: 1 } }] } }] }),
    });
    const d = await r.json();
    if (r.ok) location.href = `/d/${encodeURIComponent(d.deckId)}/edit`;
    else say(d.error ?? "Couldn't create the deck");
  }

  function emptyState(filtered: boolean): HTMLElement {
    const box = el("div", "dk-empty");
    if (filtered) {
      box.append(el("p", undefined, "No decks match. Try another search, or show all decks."));
      return box;
    }
    box.append(el("h2", undefined, "Make your first deck"), el("p", undefined, "Ask an agent for one, or start from a blank deck and edit it here. Agents write the deck; you watch it move."));
    const row = el("div", "dk-empty-actions");
    if (who.can.create) {
      const nb = el("button", "dk-btn primary", "New deck");
      nb.type = "button";
      nb.onclick = () => newDeck();
      row.append(nb);
    }
    const cb = el("button", "dk-btn ghost", "Connect an agent");
    cb.type = "button";
    cb.onclick = () => dlg.showModal();
    row.append(cb);
    box.append(row);
    return box;
  }

  function draw() {
    const q = search.value.trim().toLowerCase();
    let list = rows.filter((d) => (!q || d.title.toLowerCase().includes(q)) && (!mineOnly || d.owner === who.user?.id));
    if (sort.value === "title") list = [...list].sort((a, b) => a.title.localeCompare(b.title));
    else if (sort.value === "theme") list = [...list].sort((a, b) => a.theme.localeCompare(b.theme) || a.title.localeCompare(b.title));
    count.textContent = String(list.length);
    if (!list.length) {
      grid.replaceChildren(emptyState(rows.length > 0));
      return;
    }
    grid.replaceChildren(
      ...list.map((d) =>
        card(d, say, () => {
          rows = rows.filter((x) => x.id !== d.id);
          draw();
        }),
      ),
    );
  }

  async function load() {
    try {
      const r = await fetch("/api/decks", { cache: "no-store" });
      if (r.status === 401) goSignIn();
      const next = ((await r.json()) as { decks: Summary[] }).decks;
      const sig = (l: Summary[]) => l.map((d) => `${d.id}@${d.version}:${d.title}:${d.private}`).join("|");
      if (loaded && sig(next) === sig(rows)) return; // unchanged: keep the DOM (and focus) as it is
      rows = next;
      loaded = true;
      if (!document.querySelector(".dk-pop:popover-open")) draw();
    } catch {
      if (!loaded) grid.replaceChildren(el("p", "dk-error", "Can't reach the server. Retrying…"));
    }
  }
  search.addEventListener("input", draw);
  sort.addEventListener("change", draw);
  addEventListener("keydown", (e) => {
    if (e.key === "/" && !["INPUT", "TEXTAREA", "SELECT"].includes(document.activeElement?.tagName ?? "")) {
      e.preventDefault();
      search.focus();
    }
  });
  await load();
  setInterval(load, 5000);
}
