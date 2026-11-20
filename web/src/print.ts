// Print view used for PDF export: every slide, fully built and static, one
// per page at the deck's native size. Signals window.__skReady when drawn.
import { applyTheme, renderSlide, STAGE, themeReady } from "../runtime/render";
import type { Catalog, Deck } from "../runtime/types";

export async function mountPrint(host: HTMLElement, deckId: string) {
  const w = window as { __skReduced?: boolean; __skReady?: boolean };
  w.__skReduced = true;
  const [catalog, data] = await Promise.all([
    fetch("/api/catalog").then((r) => r.json() as Promise<Catalog>),
    fetch(`/api/decks/${encodeURIComponent(deckId)}`, { cache: "no-store" }).then((r) => r.json() as Promise<{ deck: Deck }>),
  ]);
  const deck = data.deck;
  const theme = catalog.themes[deck.theme];
  applyTheme(document.documentElement, theme);
  const [sw, sh] = STAGE[deck.aspect] ?? STAGE["16:9"];
  document.title = deck.title;

  const style = document.createElement("style");
  style.textContent = `
    @page { size: ${sw}px ${sh}px; margin: 0; }
    html, body { margin: 0; padding: 0; background: var(--sk-bg); }
    .sk-print-page { position: relative; width: ${sw}px; height: ${sh}px; overflow: hidden; break-after: page; page-break-after: always; }
    .sk-print-page:last-child { break-after: auto; page-break-after: auto; }
    .sk-print-page .sk-slide::before, .sk-print-page .sk-slide::after { animation: none !important; }
  `;
  document.head.append(style);
  document.body.classList.add("sk-print");

  const pending: Promise<unknown>[] = [];
  for (const slide of deck.slides) {
    const page = document.createElement("section");
    page.className = "sk-print-page";
    const r = renderSlide(deck, slide, catalog.layouts[slide.layout]?.regions ?? []);
    pending.push(...r.pending);
    page.append(r.el);
    host.append(page);
  }
  await themeReady(theme);
  await Promise.all(pending);
  // Shrink code blocks that are wider than their box (as the player does).
  document.querySelectorAll<HTMLElement>(".sk-el-code pre").forEach((pre) => {
    let size = parseFloat(getComputedStyle(pre).fontSize);
    for (let i = 0; i < 12 && pre.scrollWidth > pre.clientWidth + 1 && size > 16; i++) {
      size -= 2;
      pre.style.fontSize = `${size}px`;
    }
  });
  await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
  w.__skReady = true;
}
