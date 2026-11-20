import { useEffect, useRef, useState } from "preact/hooks";
import { renderSlide, STAGE } from "../../runtime/render";
import type { Catalog, Deck, Slide } from "../../runtime/types";

/** Renders one slide into a fixed-size stage, scaled to width w. */
export function SlideThumb({ deck, slide, catalog, w }: { deck: Deck; slide: Slide; catalog: Catalog; w: number }) {
  const host = useRef<HTMLDivElement>(null);
  const [sw, sh] = STAGE[deck.aspect] ?? STAGE["16:9"];
  const key = JSON.stringify(slide) + deck.theme;
  useEffect(() => {
    if (!host.current) return;
    const r = renderSlide(deck, slide, catalog.layouts[slide.layout]?.regions ?? []);
    host.current.replaceChildren(r.el);
  }, [key]);
  const scale = w / sw;
  return (
    <div class="ed-thumb" style={{ width: `${w}px`, height: `${sh * scale}px` }} aria-hidden="true">
      <div ref={host} class="ed-thumb-stage" style={{ width: `${sw}px`, height: `${sh}px`, transform: `scale(${scale})` }} />
    </div>
  );
}

interface Props {
  deck: Deck;
  catalog: Catalog;
  selected: string;
  flash: Set<string>;
  onSelect: (slideId: string) => void;
  onMove: (from: number, to: number) => void;
  onAdd: (layout: string) => void;
  onDuplicate: (i: number) => void;
  onDelete: (i: number) => void;
}

export function Sorter({ deck, catalog, selected, flash, onSelect, onMove, onAdd, onDuplicate, onDelete }: Props) {
  const [drag, setDrag] = useState<number | null>(null);
  const [over, setOver] = useState<number | null>(null);
  const [adding, setAdding] = useState(false);
  return (
    <nav class="ed-sorter" aria-label="Slides">
      <ol>
        {deck.slides.map((s, i) => (
          <li
            key={s.id}
            class={`ed-slide-item${s.id === selected ? " is-selected" : ""}${over === i && drag !== null && drag !== i ? " is-over" : ""}${flash.has(s.id) ? " sk-flash" : ""}`}
            draggable
            onDragStart={(e) => {
              setDrag(i);
              e.dataTransfer?.setData("text/plain", String(i));
            }}
            onDragOver={(e) => {
              e.preventDefault();
              setOver(i);
            }}
            onDragLeave={() => setOver(null)}
            onDrop={(e) => {
              e.preventDefault();
              if (drag !== null && drag !== i) onMove(drag, i);
              setDrag(null);
              setOver(null);
            }}
            onDragEnd={() => {
              setDrag(null);
              setOver(null);
            }}
          >
            <button type="button" class="ed-slide-btn" onClick={() => onSelect(s.id)} aria-current={s.id === selected} aria-label={`Slide ${i + 1}`}>
              <span class="ed-slide-num">{i + 1}</span>
              <SlideThumb deck={deck} slide={s} catalog={catalog} w={176} />
            </button>
            <span class="ed-slide-actions">
              <button type="button" title="Duplicate (keeps element ids, so it morphs)" onClick={() => onDuplicate(i)}>⧉</button>
              <button type="button" title="Delete slide" onClick={() => onDelete(i)}>✕</button>
            </span>
          </li>
        ))}
      </ol>
      <div class="ed-add-slide">
        <button type="button" class="ed-btn ed-btn-wide" onClick={() => setAdding(!adding)} aria-expanded={adding}>
          + Add slide
        </button>
        {adding && (
          <div class="ed-layout-menu" role="menu">
            {Object.entries(catalog.layouts).map(([name, l]) => (
              <button
                type="button"
                role="menuitem"
                key={name}
                title={l.description}
                onClick={() => {
                  setAdding(false);
                  onAdd(name);
                }}
              >
                {name}
              </button>
            ))}
          </div>
        )}
      </div>
    </nav>
  );
}
