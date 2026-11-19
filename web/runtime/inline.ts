// Rich inline text: **bold**, *italic*, `code`, [link](url). Everything else is
// escaped, so deck text can never inject markup.

const esc = (s: string) =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

function safeURL(u: string): string | null {
  try {
    const url = new URL(u, location.href);
    return ["http:", "https:", "mailto:"].includes(url.protocol) ? url.href : null;
  } catch {
    return null;
  }
}

export function inline(src: string): string {
  // Pull code spans out first so their contents are not formatted.
  const codes: string[] = [];
  let s = src.replace(/`([^`]+)`/g, (_, c: string) => {
    codes.push(c);
    return `\u0000${codes.length - 1}\u0000`;
  });
  s = esc(s);
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (m, text: string, href: string) => {
    const u = safeURL(href.replace(/&amp;/g, "&"));
    return u ? `<a href="${esc(u)}" target="_blank" rel="noopener noreferrer">${text}</a>` : m;
  });
  s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/(^|[^*])\*([^*]+)\*/g, "$1<em>$2</em>");
  return s.replace(/\u0000(\d+)\u0000/g, (_, i: string) => `<code>${esc(codes[Number(i)])}</code>`);
}

export function plain(src: string): string {
  return src.replace(/\*\*|\*|`/g, "").replace(/\[([^\]]+)\]\([^)]*\)/g, "$1");
}
