// The Slidekick mark and wordmark as inline SVG (so the wordmark text follows
// the page's colour). Each copy gets unique gradient ids, so several logos on
// one page never borrow each other's definitions.
import markSrc from "./mark.svg?raw";
import wordSrc from "./wordmark.svg?raw";

let n = 0;
const unique = (svg: string) => {
  const s = `u${++n}`;
  return svg.replace(/id="([^"]+)"/g, `id="$1-${s}"`).replace(/url\(#([^)]+)\)/g, `url(#$1-${s})`);
};

export const markSVG = () => unique(markSrc);
export const wordmarkSVG = () => unique(wordSrc);

/** An element holding the logo; label it when it's the only text. */
export function brandEl(kind: "mark" | "wordmark", cls: string): HTMLElement {
  const span = document.createElement("span");
  span.className = cls;
  span.innerHTML = kind === "mark" ? markSVG() : wordmarkSVG();
  return span;
}
