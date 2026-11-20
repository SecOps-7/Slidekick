import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { renderSlide, STAGE } from "../../runtime/render";
import type { Catalog, Deck, Element, Slide } from "../../runtime/types";

const TEXT_TYPES = new Set(["heading", "text", "quote", "bullets", "code"]);

interface Props {
  deck: Deck;
  slide: Slide;
  catalog: Catalog;
  selectedEl: string | null;
  flash: Set<string>;
  onSelectEl: (id: string | null) => void;
  /** Commit inline text for an element: text, items (bullets) or code. */
  onInlineEdit: (el: Element, value: string) => void;
}

export function Canvas({ deck, slide, catalog, selectedEl, flash, onSelectEl, onInlineEdit }: Props) {
  const wrap = useRef<HTMLDivElement>(null);
  const stage = useRef<HTMLDivElement>(null);
  const [scale, setScale] = useState(0.5);
  const [editing, setEditing] = useState<string | null>(null);
  const [sw, sh] = STAGE[deck.aspect] ?? STAGE["16:9"];

  useLayoutEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setScale(Math.min((el.clientWidth - 48) / sw, (el.clientHeight - 48) / sh)));
    ro.observe(el);
    return () => ro.disconnect();
  }, [sw, sh]);

  const key = JSON.stringify(slide) + deck.theme;
  useEffect(() => {
    if (!stage.current) return;
    const r = renderSlide(deck, slide, catalog.layouts[slide.layout]?.regions ?? []);
    stage.current.replaceChildren(r.el);
    setEditing(null);
  }, [key]);

  // Selection outline and flashes after each render.
  useEffect(() => {
    const root = stage.current;
    if (!root) return;
    root.querySelectorAll(".ed-selected").forEach((n) => n.classList.remove("ed-selected"));
    if (selectedEl) root.querySelector(`[data-el-id="${CSS.escape(selectedEl)}"]`)?.classList.add("ed-selected");
    for (const id of flash) {
      const n = root.querySelector<HTMLElement>(`[data-el-id="${CSS.escape(id)}"]`);
      if (n) {
        n.classList.remove("sk-flash");
        void n.offsetWidth;
        n.classList.add("sk-flash");
      }
    }
  });

  // Inline text editing: swap the element's content for a textarea holding the raw source.
  useEffect(() => {
    const root = stage.current;
    if (!root || !editing) return;
    const node = root.querySelector<HTMLElement>(`[data-el-id="${CSS.escape(editing)}"]`);
    const el = Object.values(slide.regions).flat().find((e) => e.id === editing);
    if (!node || !el) return;
    const p = el.props as Record<string, unknown>;
    const raw = el.type === "bullets" ? ((p.items as string[]) ?? []).join("\n") : el.type === "code" ? String(p.code ?? "") : String(p.text ?? "");
    const ta = document.createElement("textarea");
    ta.className = `ed-inline ed-inline-${el.type}`;
    ta.value = raw;
    ta.setAttribute("aria-label", `Edit ${el.type}`);
    const rect = node.getBoundingClientRect();
    ta.style.minHeight = `${Math.max(rect.height / scale, 60)}px`;
    node.replaceChildren(ta);
    ta.focus();
    ta.select();
    let done = false;
    const finish = (save: boolean) => {
      if (done) return;
      done = true;
      setEditing(null);
      if (save && ta.value !== raw) onInlineEdit(el, ta.value);
      else {
        // restore the rendered element
        const r = renderSlide(deck, slide, catalog.layouts[slide.layout]?.regions ?? []);
        root.replaceChildren(r.el);
      }
    };
    ta.addEventListener("blur", () => finish(true));
    ta.addEventListener("keydown", (e) => {
      e.stopPropagation();
      if (e.key === "Escape") finish(false);
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) finish(true);
      if (e.key === "Enter" && !e.shiftKey && (el.type === "heading" || el.type === "text" || el.type === "quote")) {
        e.preventDefault();
        finish(true);
      }
    });
  }, [editing]);

  const target = (e: MouseEvent) => (e.target as HTMLElement).closest<HTMLElement>("[data-el-id]");

  return (
    <div
      ref={wrap}
      class="ed-canvas"
      onClick={(e) => {
        if (editing) return;
        onSelectEl(target(e)?.dataset.elId ?? null);
      }}
      onDblClick={(e) => {
        const n = target(e);
        if (n && TEXT_TYPES.has(n.dataset.type ?? "")) {
          onSelectEl(n.dataset.elId!);
          setEditing(n.dataset.elId!);
        }
      }}
    >
      <div class="ed-stage-box" style={{ width: `${sw * scale}px`, height: `${sh * scale}px` }}>
        <div ref={stage} class="ed-stage" style={{ width: `${sw}px`, height: `${sh}px`, transform: `scale(${scale})` }} />
      </div>
      <p class="ed-hint">Click to select · double-click text to edit in place · Enter saves (Shift+Enter for a new line) · Esc cancels</p>
    </div>
  );
}
