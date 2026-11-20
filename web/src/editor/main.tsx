// Slidekick editor: the same deck the agent edits, editable by a human, with
// agent changes streaming in live. Undo and redo walk the shared version
// history, so they cover agent and human edits alike.
import { render } from "preact";
import { useCallback, useEffect, useMemo, useRef, useState } from "preact/hooks";
import { applyPatch } from "../../runtime/patch";
import { applyTheme } from "../../runtime/render";
import type { Catalog, Deck, Element, LintIssue, PatchEvent, PatchOp } from "../../runtime/types";
import { ensureAuth } from "../auth";
import { Live } from "../sync";
import { withKey } from "../key";
import { Api, ApiError, type Access, type Comment } from "./api";
import { Canvas } from "./canvas";
import { ChatPanel } from "./chat";
import { checkPatch } from "./check";
import { elPath, findEl, slidePath, slideTemplate, stripIds } from "./ops";
import { Panel } from "./panel";
import { Sorter } from "./sorter";
import { Timeline } from "./timeline";
import "./editor.css";

type Cat = Catalog & { icons: string[]; elements: Record<string, string> };

function Editor({ api, cat, initial }: { api: Api; cat: Cat; initial: { version: number; deck: Deck; lint: LintIssue[] } }) {
  const [deck, setDeck] = useState(initial.deck);
  const [version, setVersion] = useState(initial.version);
  const [lint, setLint] = useState(initial.lint);
  const [comments, setComments] = useState<Comment[]>([]);
  const [slideId, setSlideId] = useState(() => {
    const m = /^#(\d+)/.exec(location.hash);
    return initial.deck.slides[m ? Number(m[1]) - 1 : 0]?.id ?? initial.deck.slides[0]?.id ?? "";
  });
  const [elId, setElId] = useState<string | null>(null);
  const [live, setLive] = useState<"live" | "offline">("offline");
  const [toast, setToast] = useState("");
  const [flash, setFlash] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [followAgent, setFollowAgent] = useState(true);
  const [access, setAccess] = useState<Access | null>(null);
  const [admin, setAdmin] = useState(false);
  const [canAI, setCanAI] = useState(false);
  const [chat, setChat] = useState(false);
  const followRef = useRef(followAgent);
  followRef.current = followAgent;
  useEffect(() => {
    api.access().then(setAccess, () => {});
    api.me().then((m) => {
      setAdmin(Boolean(m.can["deck:deck-token"]));
      setCanAI(Boolean(m.can.ai));
    }, () => {});
  }, []);
  const key = access?.private ? access.viewKey : "";

  // Refs mirror state for use inside socket callbacks.
  const deckRef = useRef(deck);
  const versionRef = useRef(version);
  deckRef.current = deck;
  versionRef.current = version;
  // Undo walks back through versions; seed with history so agent edits made before opening are undoable too.
  const undo = useRef<number[]>(Array.from({ length: Math.max(0, initial.version - 1) }, (_, i) => i + 1));
  const redo = useRef<number[]>([]);
  const pendingKind = useRef<"undo" | "redo" | null>(null);

  const say = useCallback((msg: string) => {
    setToast(msg);
    window.clearTimeout((say as unknown as { t?: number }).t);
    (say as unknown as { t?: number }).t = window.setTimeout(() => setToast(""), 2800);
  }, []);

  const advance = useCallback((next: Deck, nextVersion: number) => {
    const prev = versionRef.current;
    if (nextVersion <= prev) return;
    const kind = pendingKind.current;
    pendingKind.current = null;
    if (kind === "undo") redo.current.push(prev);
    else if (kind === "redo") undo.current.push(prev);
    else {
      undo.current.push(prev);
      redo.current = [];
    }
    deckRef.current = next;
    versionRef.current = nextVersion;
    setDeck(next);
    setVersion(nextVersion);
  }, []);

  const reload = useCallback(async () => {
    const r = await api.load();
    advance(r.deck, r.version);
    setLint(r.lint);
  }, [api, advance]);

  const loadComments = useCallback(async () => setComments(await api.comments(true)), [api]);

  // Apply theme tokens to the page (slides read --sk-* variables).
  useEffect(() => {
    const t = cat.themes[deck.theme];
    if (t) applyTheme(document.documentElement, t);
  }, [deck.theme]);

  // Keep a valid selection when slides come and go.
  useEffect(() => {
    if (!deck.slides.some((s) => s.id === slideId)) setSlideId(deck.slides[0]?.id ?? "");
    if (elId && !findEl(deck, slideId, elId)) setElId(null);
  }, [deck]);
  useEffect(() => {
    const i = deck.slides.findIndex((s) => s.id === slideId);
    if (i >= 0) history.replaceState(null, "", `#${i + 1}`);
  }, [slideId, deck]);

  // Live updates from the agent (and other tabs).
  useEffect(() => {
    loadComments();
    let lintTimer = 0;
    const live = new Live(
      api.deckId,
      async (m) => {
        if (m.type === "hello" && m.version !== versionRef.current) await reload();
        if (m.type === "comments") loadComments();
        if (m.type === "access") api.access().then(setAccess, () => {});
        if (m.type !== "patch") return;
        const ev = m as unknown as PatchEvent;
        if (ev.version <= versionRef.current) return;
        if (ev.baseVersion !== versionRef.current) return reload();
        try {
          advance(applyPatch(deckRef.current, ev.ops), ev.version);
        } catch {
          return reload();
        }
        if (ev.actor !== "human") {
          // Follow the agent to the slide it is working on.
          if (followRef.current && ev.focusSlide && deckRef.current.slides.some((s) => s.id === ev.focusSlide)) setSlideId(ev.focusSlide);
          setFlash(new Set(ev.changed));
          setTimeout(() => setFlash(new Set()), 1700);
          say(`Agent edit · version ${ev.version}`);
        }
        window.clearTimeout(lintTimer);
        lintTimer = window.setTimeout(async () => setLint((await api.load()).lint), 350);
      },
      setLive,
    );
    return () => live.close();
  }, []);

  /** Sends ops based on the current version; on conflict, reloads and says so. */
  const commit = useCallback(
    async (ops: PatchOp[], label: string) => {
      if (!ops.length) return;
      const problems = checkPatch(deckRef.current, ops);
      if (problems.length) {
        say(`${label} not saved: ${problems[0]}`);
        return;
      }
      setBusy(true);
      try {
        const res = await api.patch(versionRef.current, ops);
        setLint(res.lint);
        // The socket normally delivers the normalised ops first; fall back to a reload.
        await new Promise((r) => setTimeout(r, 250));
        if (versionRef.current < res.version) await reload();
      } catch (err) {
        if (err instanceof ApiError && err.status === 409) {
          await reload();
          say("Someone else just edited this deck; reloaded. Try again.");
        } else if (err instanceof ApiError && err.problems.length) {
          say(`${label} rejected: ${err.problems[0]}`);
        } else {
          say(`${label} failed: ${err instanceof Error ? err.message : String(err)}`);
        }
      } finally {
        setBusy(false);
      }
    },
    [api, reload, say],
  );

  const travel = useCallback(
    async (kind: "undo" | "redo") => {
      const stack = kind === "undo" ? undo.current : redo.current;
      const target = stack.pop();
      if (target === undefined) return;
      pendingKind.current = kind;
      try {
        const res = await api.restore(versionRef.current, target);
        await new Promise((r) => setTimeout(r, 250));
        if (versionRef.current < res.version) {
          const r = await api.load();
          advance(r.deck, r.version);
        }
        setLint(res.lint);
        say(`${kind === "undo" ? "Undid" : "Redid"}: now matches version ${target}`);
      } catch (err) {
        pendingKind.current = null;
        stack.push(target);
        if (err instanceof ApiError && err.status === 409) await reload();
        say(`${kind} failed: ${err instanceof Error ? err.message : String(err)}`);
      }
    },
    [api, advance, reload, say],
  );

  const slideIndex = Math.max(0, deck.slides.findIndex((s) => s.id === slideId));
  const slide = deck.slides[slideIndex];
  const loc = useMemo(() => (elId && slide ? findEl(deck, slide.id, elId) : null), [deck, slide, elId]);

  // Keyboard: undo/redo, delete, escape.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = (e.target as HTMLElement).closest("input, textarea, select, [contenteditable]");
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === "z" && !typing) {
        e.preventDefault();
        travel(e.shiftKey ? "redo" : "undo");
      } else if (mod && e.key.toLowerCase() === "y" && !typing) {
        e.preventDefault();
        travel("redo");
      } else if ((e.key === "Delete" || e.key === "Backspace") && !typing && loc) {
        e.preventDefault();
        commit([{ op: "remove", path: elPath(loc) }], "Delete element");
      } else if (e.key === "Escape" && !typing) setElId(null);
      else if (!typing && (e.key === "ArrowDown" || e.key === "ArrowUp") && !loc) {
        const i = slideIndex + (e.key === "ArrowDown" ? 1 : -1);
        if (deck.slides[i]) setSlideId(deck.slides[i].id);
      }
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  });

  const select = (sid: string, eid: string | null) => {
    setSlideId(sid);
    setElId(eid);
  };

  const onInlineEdit = (el: Element, value: string) => {
    const l = slide && findEl(deck, slide.id, el.id);
    if (!l) return;
    const path = `${elPath(l)}/props`;
    if (el.type === "bullets") commit([{ op: "replace", path: `${path}/items`, value: value.split("\n").map((s) => s.trim()).filter(Boolean) }], "Edit bullets");
    else if (el.type === "code") commit([{ op: "replace", path: `${path}/code`, value }], "Edit code");
    else commit([{ op: "replace", path: `${path}/text`, value: value.trim() }], "Edit text");
  };

  const onStep = (el: Element, step: number) => {
    const l = slide && findEl(deck, slide.id, el.id);
    if (!l) return;
    const p = elPath(l);
    if (step <= 0) commit(el.build ? [{ op: "remove", path: `${p}/build` }] : [], "Build step");
    else commit([{ op: "add", path: `${p}/build`, value: { ...(el.build ?? {}), step } }], "Build step");
  };

  const problems = lint.filter((i) => i.severity !== "info").length;

  return (
    <div class="ed">
      <header class="ed-top">
        <a class="ed-home" href="/" title="All decks">
          ◆
        </a>
        <TitleInput title={deck.title} onCommit={(v) => commit([{ op: "replace", path: "/title", value: v }], "Rename")} />
        <label class="ed-theme">
          <span class="sr">Theme</span>
          <select value={deck.theme} onChange={(e) => commit([{ op: "replace", path: "/theme", value: e.currentTarget.value }], "Theme")}>
            {Object.values(cat.themes).map((t) => (
              <option key={t.name} value={t.name}>
                {t.label}
              </option>
            ))}
          </select>
        </label>
        <div class="ed-group">
          <button type="button" class="ed-btn" title="Undo (Ctrl+Z)" disabled={!undo.current.length} onClick={() => travel("undo")}>
            ↶ Undo
          </button>
          <button type="button" class="ed-btn" title="Redo (Ctrl+Shift+Z)" disabled={!redo.current.length} onClick={() => travel("redo")}>
            ↷ Redo
          </button>
        </div>
        <span class="ed-spacer" />
        <span class={`ed-status ${busy ? "busy" : ""}`}>{busy ? "Saving…" : `v${version}`}</span>
        <span class={`ed-live ${live}`}>{live}</span>
        <span class={`ed-lint-badge ${problems ? "warn" : "ok"}`}>{problems ? `${problems} to fix` : "Lint clean"}</span>
        <label class="ed-follow" title="Jump to the slide the agent is editing">
          <input type="checkbox" checked={followAgent} onChange={(e) => setFollowAgent(e.currentTarget.checked)} /> Follow agent
        </label>
        <a class="ed-btn" href={withKey(`/d/${api.deckId}#${slideIndex + 1}`, key)} target="_blank" rel="noopener">
          View
        </a>
        {canAI && (
          <button type="button" class={`ed-btn${chat ? " on" : ""}`} aria-pressed={chat} onClick={() => setChat(!chat)} title="Ask an AI to change this deck">
            ✦ AI
          </button>
        )}
        <ShareMenu api={api} access={access} admin={admin} onChange={setAccess} say={say} />
        <ExportMenu api={api} say={say} viewKey={key ?? ""} />
        <a class="ed-btn primary" href={`/d/${api.deckId}/present#${slideIndex + 1}`} target="_blank" rel="noopener">
          ▶ Present
        </a>
      </header>
      <Sorter
        deck={deck}
        catalog={cat}
        selected={slide?.id ?? ""}
        flash={flash}
        onSelect={(id) => select(id, null)}
        onMove={(from, to) => commit([{ op: "move", from: slidePath(from), path: slidePath(to) }], "Move slide")}
        onAdd={(layout) => commit([{ op: "add", path: slidePath(slideIndex + 1), value: slideTemplate(layout) }], "Add slide").then(() => setSlideId(deckRef.current.slides[slideIndex + 1]?.id ?? slideId))}
        onDuplicate={(i) => {
          const copy = structuredClone(deck.slides[i]) as Partial<typeof slide>;
          delete copy.id;
          commit([{ op: "add", path: slidePath(i + 1), value: copy }], "Duplicate slide");
        }}
        onDelete={(i) => deck.slides.length > 0 && confirm(`Delete slide ${i + 1}?`) && commit([{ op: "remove", path: slidePath(i) }], "Delete slide")}
      />
      <main class="ed-main">
        {slide ? (
          <>
            <Canvas deck={deck} slide={slide} catalog={cat} selectedEl={elId} flash={flash} onSelectEl={setElId} onInlineEdit={onInlineEdit} />
            <Timeline slide={slide} catalog={cat} selectedEl={elId} onSelectEl={setElId} onStep={onStep} />
          </>
        ) : (
          <div class="ed-empty">
            <p>No slides yet.</p>
            <button type="button" class="ed-btn primary" onClick={() => commit([{ op: "add", path: "/slides/-", value: stripIds(slideTemplate("title")) }], "Add slide")}>
              Add the first slide
            </button>
          </div>
        )}
      </main>
      <Panel
        deck={deck}
        cat={cat}
        slideIndex={slideIndex}
        loc={loc}
        lint={lint}
        comments={comments}
        commit={commit}
        upload={async (f) => (await api.upload(f)).url}
        addComment={async (text) => {
          try {
            await api.addComment(slide?.id ?? "", loc?.el.id ?? "", text);
            loadComments();
            say("Sent to the agent");
          } catch (err) {
            say(`Comment failed: ${err instanceof Error ? err.message : String(err)}`);
          }
        }}
        resolve={async (id) => {
          await api.resolve(id);
          loadComments();
        }}
        select={select}
      />
      {chat && <ChatPanel api={api} version={() => versionRef.current} onClose={() => setChat(false)} say={say} />}
      <div class={`ed-toast${toast ? " show" : ""}`} role="status">
        {toast}
      </div>
    </div>
  );
}

function ShareMenu({ api, access, admin, onChange, say }: { api: Api; access: Access | null; admin: boolean; onChange: (a: Access) => void; say: (m: string) => void }) {
  const [open, setOpen] = useState(false);
  const [token, setToken] = useState("");
  if (!access) return null;
  const run = async (f: () => Promise<void>) => {
    try {
      await f();
    } catch (err) {
      say(`Sharing change failed: ${err instanceof Error ? err.message : String(err)}`);
    }
  };
  const copy = (text: string, what: string) =>
    navigator.clipboard?.writeText(text).then(
      () => say(`${what} copied`),
      () => say(`Couldn't copy; select it by hand`),
    );
  const origin = location.origin;
  return (
    <div class="ed-export">
      <button type="button" class="ed-btn" aria-expanded={open} onClick={() => setOpen(!open)}>
        {access.private ? "🔒 Private ▾" : "Share ▾"}
      </button>
      {open && (
        <div class="ed-export-menu ed-share" role="dialog" aria-label="Sharing">
          <label class="ed-share-row">
            <input type="checkbox" checked={access.private} onChange={(e) => run(async () => onChange(await api.setAccess(e.currentTarget.checked)))} />
            <span>
              Private <small>only people with the link below can watch</small>
            </span>
          </label>
          <div class="ed-share-link">
            <input readOnly value={access.shareUrl} aria-label="Viewer link" onFocus={(e) => e.currentTarget.select()} />
            <button type="button" class="ed-btn" onClick={() => copy(access.shareUrl, "Link")}>
              Copy
            </button>
          </div>
          {access.private && (
            <button type="button" class="ed-link" onClick={() => confirm("Make a new link? Links shared so far stop working.") && run(async () => onChange(await api.setAccess(true, true)))}>
              Make a new link (old links stop working)
            </button>
          )}
          {admin && (
            <div class="ed-share-token">
              <h4>Deck edit token</h4>
              <p>
                <small>Lets an agent or person edit, present and export this deck only.</small>
              </p>
              {token ? (
                <>
                  <div class="ed-share-link">
                    <input readOnly value={token} aria-label="Edit token" onFocus={(e) => e.currentTarget.select()} />
                    <button type="button" class="ed-btn" onClick={() => copy(token, "Token")}>
                      Copy
                    </button>
                  </div>
                  <p>
                    <small>Shown once. Connect an agent with:</small>
                  </p>
                  <code class="ed-share-cmd">{`claude mcp add --transport http slidekick-${api.deckId} ${origin}/mcp --header "Authorization: Bearer ${token}"`}</code>
                </>
              ) : (
                <button
                  type="button"
                  class="ed-btn"
                  onClick={() =>
                    (!access.hasEditToken || confirm("Replace the existing edit token? The old one stops working.")) &&
                    run(async () => {
                      setToken((await api.issueEditToken()).token);
                      onChange({ ...access, hasEditToken: true });
                    })
                  }
                >
                  {access.hasEditToken ? "Replace token" : "Create token"}
                </button>
              )}
              {access.hasEditToken && (
                <button
                  type="button"
                  class="ed-link"
                  onClick={() =>
                    confirm("Revoke the edit token?") &&
                    run(async () => {
                      await api.revokeEditToken();
                      setToken("");
                      onChange({ ...access, hasEditToken: false });
                    })
                  }
                >
                  Revoke token
                </button>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function ExportMenu({ api, say, viewKey }: { api: Api; say: (m: string) => void; viewKey: string }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState("");
  const make = async (format: "pdf" | "png") => {
    setOpen(false);
    setBusy(format);
    try {
      const { url } = await api.exportFile(format);
      location.href = url;
    } catch (err) {
      say(`Export failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setBusy("");
    }
  };
  return (
    <div class="ed-export">
      <button type="button" class="ed-btn" aria-expanded={open} onClick={() => setOpen(!open)} disabled={Boolean(busy)}>
        {busy ? `Exporting ${busy.toUpperCase()}…` : "Export ▾"}
      </button>
      {open && (
        <div class="ed-export-menu" role="menu">
          <a role="menuitem" href={withKey(`/api/decks/${api.deckId}/export.html?download=1`, viewKey)} onClick={() => setOpen(false)}>
            HTML <small>plays offline, with animations</small>
          </a>
          <button type="button" role="menuitem" onClick={() => make("pdf")}>
            PDF <small>one page per slide</small>
          </button>
          <button type="button" role="menuitem" onClick={() => make("png")}>
            PNG <small>zip of slide images</small>
          </button>
        </div>
      )}
    </div>
  );
}

function TitleInput({ title, onCommit }: { title: string; onCommit: (v: string) => void }) {
  const [v, setV] = useState(title);
  useEffect(() => setV(title), [title]);
  const commit = () => v.trim() && v.trim() !== title && onCommit(v.trim());
  return <input class="ed-title" aria-label="Deck title" value={v} onInput={(e) => setV(e.currentTarget.value)} onBlur={commit} onKeyDown={(e) => e.key === "Enter" && (e.currentTarget as HTMLInputElement).blur()} />;
}

export async function mountEditor(host: HTMLElement, deckId: string) {
  document.body.classList.add("ed-page");
  document.title = "Edit · Slidekick";
  const token = await ensureAuth("edit this deck", deckId);
  const can = (await import("../auth")).currentMe()?.can ?? {};
  if (!can["deck:edit"]) {
    // Viewers (and editors on someone else's deck) watch instead; the server would refuse their edits anyway.
    location.replace(`/d/${encodeURIComponent(deckId)}${location.hash}`);
    return;
  }
  const api = new Api(deckId, token);
  const [cat, initial] = await Promise.all([api.catalog(), api.load().catch(() => null)]);
  if (!initial) {
    host.textContent = "Deck not found.";
    return;
  }
  document.title = `Edit · ${initial.deck.title}`;
  const theme = cat.themes[initial.deck.theme];
  if (theme) applyTheme(document.documentElement, theme); // before the first render: diagrams read the tokens
  render(<Editor api={api} cat={cat} initial={initial} />, host);
}
