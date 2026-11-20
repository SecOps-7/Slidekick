// Syntax highlighting with Shiki, loaded lazily. Uses a CSS-variables theme so
// code colours follow the deck theme's tokens (see runtime.css).
import type { HighlighterCore } from "shiki/core";

const LANGS: Record<string, () => Promise<unknown>> = {
  typescript: () => import("@shikijs/langs/typescript"),
  javascript: () => import("@shikijs/langs/javascript"),
  tsx: () => import("@shikijs/langs/tsx"),
  json: () => import("@shikijs/langs/json"),
  bash: () => import("@shikijs/langs/bash"),
  python: () => import("@shikijs/langs/python"),
  go: () => import("@shikijs/langs/go"),
  rust: () => import("@shikijs/langs/rust"),
  sql: () => import("@shikijs/langs/sql"),
  yaml: () => import("@shikijs/langs/yaml"),
  html: () => import("@shikijs/langs/html"),
  css: () => import("@shikijs/langs/css"),
  java: () => import("@shikijs/langs/java"),
  c: () => import("@shikijs/langs/c"),
  cpp: () => import("@shikijs/langs/cpp"),
  diff: () => import("@shikijs/langs/diff"),
  markdown: () => import("@shikijs/langs/markdown"),
};
const ALIASES: Record<string, string> = { ts: "typescript", js: "javascript", sh: "bash", shell: "bash", shellscript: "bash", py: "python", golang: "go", yml: "yaml", md: "markdown", "c++": "cpp", jsx: "tsx" };

let hlP: Promise<HighlighterCore> | null = null;
const loaded = new Set<string>();
const cache = new Map<string, string>();

async function highlighter() {
  hlP ??= (async () => {
    const [{ createHighlighterCore, createCssVariablesTheme }, { createJavaScriptRegexEngine }] = await Promise.all([
      import("shiki/core"),
      import("shiki/engine/javascript"),
    ]);
    return createHighlighterCore({
      themes: [createCssVariablesTheme({ name: "sk", variablePrefix: "--shiki-", fontStyle: true })],
      langs: [],
      engine: createJavaScriptRegexEngine(),
    });
  })();
  return hlP;
}

export function normalizeLang(lang: string): string | null {
  const l = lang.toLowerCase().trim();
  const name = ALIASES[l] ?? l;
  return name in LANGS ? name : null;
}

/** Replaces the plain <code> inside pre with highlighted HTML. Never rejects. */
export async function highlight(pre: HTMLElement, code: string, lang: string): Promise<void> {
  const name = normalizeLang(lang);
  if (!name) return;
  const key = `${name}\u0000${code}`;
  let html = cache.get(key);
  try {
    if (!html) {
      const hl = await highlighter();
      if (!loaded.has(name)) {
        const mod = (await LANGS[name]()) as { default: Parameters<HighlighterCore["loadLanguage"]>[0] };
        await hl.loadLanguage(mod.default);
        loaded.add(name);
      }
      html = hl.codeToHtml(code, { lang: name, theme: "sk" });
      cache.set(key, html);
    }
    const tmp = document.createElement("div");
    tmp.innerHTML = html;
    const code2 = tmp.querySelector("code");
    if (code2) pre.replaceChildren(...code2.childNodes);
    pre.classList.add("sk-highlighted");
  } catch {
    /* keep plain text */
  }
}
