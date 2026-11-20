import type { Catalog, Element, Slide } from "../../runtime/types";
import { describe } from "./ops";

interface Props {
  slide: Slide;
  catalog: Catalog;
  selectedEl: string | null;
  onSelectEl: (id: string) => void;
  onStep: (el: Element, step: number) => void;
}

/** The slide's build order: what appears on entry, then on each click. */
export function Timeline({ slide, catalog, selectedEl, onSelectEl, onStep }: Props) {
  const order = catalog.layouts[slide.layout]?.regions ?? [];
  const els = [...order, ...Object.keys(slide.regions).filter((r) => !order.includes(r))].flatMap((r) => slide.regions[r] ?? []);
  const maxStep = els.reduce((m, e) => Math.max(m, e.build?.step ?? 0), 0);
  const groups = Array.from({ length: maxStep + 1 }, (_, s) => els.filter((e) => (e.build?.step ?? 0) === s));
  return (
    <section class="ed-timeline" aria-label="Build order">
      <div class="ed-tl-head">
        <strong>Build order</strong>
        <span>{slide.transition ? `enters with ${slide.transition.type}` : "enters with fade"}</span>
      </div>
      <div class="ed-tl-track">
        {groups.map((g, step) => (
          <div class="ed-tl-group" key={step}>
            <div class="ed-tl-label">{step === 0 ? "On entry" : `Click ${step}`}</div>
            {g.length === 0 && <div class="ed-tl-empty">nothing</div>}
            {g.map((e) => (
              <div class={`ed-tl-chip${e.id === selectedEl ? " is-selected" : ""}`} key={e.id}>
                <button type="button" class="ed-tl-step" title="Earlier" disabled={step === 0} onClick={() => onStep(e, step - 1)}>
                  ‹
                </button>
                <button type="button" class="ed-tl-name" onClick={() => onSelectEl(e.id)}>
                  <b>{e.type}</b> {describe(e)}
                  {e.build && <i>{e.build.preset ?? "fade-up"}</i>}
                </button>
                <button type="button" class="ed-tl-step" title="Later" disabled={step >= 20} onClick={() => onStep(e, step + 1)}>
                  ›
                </button>
              </div>
            ))}
          </div>
        ))}
      </div>
    </section>
  );
}
