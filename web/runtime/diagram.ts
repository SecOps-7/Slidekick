// Mermaid diagrams, themed from the deck's tokens. Mermaid is large, so it
// loads lazily the first time a deck uses a diagram. Rendered SVG is cached
// by (source, theme) so live edits elsewhere never cause a diagram to flicker.

let mermaidP: Promise<typeof import("mermaid").default> | null = null;
let configured = "";
const cache = new Map<string, string>();
let seq = 0;
let queue: Promise<string> = Promise.resolve("");

function tokens() {
  const cs = getComputedStyle(document.documentElement);
  const v = (k: string) => cs.getPropertyValue(`--sk-${k}`).trim();
  return {
    key: ["bg", "surface", "text", "muted", "accent", "accent2", "font-body"].map(v).join("|"),
    vars: {
      background: v("bg"),
      primaryColor: v("surface"),
      primaryTextColor: v("text"),
      primaryBorderColor: v("accent"),
      secondaryColor: v("surface"),
      tertiaryColor: v("bg"),
      lineColor: v("muted"),
      textColor: v("text"),
      mainBkg: v("surface"),
      nodeBorder: v("accent"),
      clusterBkg: v("bg"),
      clusterBorder: v("muted"),
      edgeLabelBackground: v("bg"),
      actorBkg: v("surface"),
      actorBorder: v("accent"),
      actorTextColor: v("text"),
      signalColor: v("text"),
      signalTextColor: v("text"),
      noteBkgColor: v("surface"),
      noteTextColor: v("text"),
      fontFamily: v("font-body"),
      fontSize: "28px",
    },
  };
}

async function mermaid() {
  mermaidP ??= import("mermaid").then((m) => m.default);
  return mermaidP;
}

function fit(host: HTMLElement) {
  const svg = host.querySelector("svg");
  if (!svg) return;
  svg.style.maxWidth = "100%";
  svg.style.width = "100%";
  svg.style.height = "100%";
  svg.setAttribute("preserveAspectRatio", "xMidYMid meet");
}

/** Fills host with the rendered diagram; resolves when done (never rejects). */
export async function renderDiagram(host: HTMLElement, source: string): Promise<void> {
  // Theme tokens may land a frame or two after the first render; wait briefly.
  let t = tokens();
  for (let i = 0; i < 60 && !t.vars.background; i++) {
    await new Promise((r) => requestAnimationFrame(r));
    t = tokens();
  }
  const key = `${t.key}\u0000${source}`;
  const hit = cache.get(key);
  if (hit) {
    host.innerHTML = hit;
    fit(host);
    return;
  }
  // Mermaid's render is not safe to run concurrently (shared global state):
  // queue renders one at a time, and re-check the cache once it's our turn.
  const turn = queue.then(async () => {
    const done = cache.get(key);
    if (done) return done;
    const m = await mermaid();
    if (configured !== t.key) {
      // Mermaid 12 defaults to ELK layout and the "neo" look; keep dagre and the
      // classic look so diagrams match the themes (and existing decks don't re-lay out).
      m.initialize({
        startOnLoad: false,
        securityLevel: "strict",
        theme: "base",
        look: "classic",
        layout: "dagre",
        themeVariables: t.vars,
        // SVG text labels, not HTML in foreignObject: HTML labels are measured before
        // the slide's theme font applies and then wrap. Mermaid 12 reads the top-level
        // setting; flowchart.htmlLabels is kept for older diagram types.
        htmlLabels: false,
        // Mermaid 12 wraps flowchart labels at 120px and widens every node to 120px;
        // slide text is large, so wrap later and let small nodes stay small.
        flowchart: { htmlLabels: false, wrappingWidth: 360, minNodeWidth: 0 },
      });
      configured = t.key;
    }
    const { svg } = await m.render(`sk-mmd-${++seq}`, source);
    cache.set(key, svg);
    return svg;
  });
  queue = turn.catch(() => "");
  try {
    host.innerHTML = await turn;
    fit(host);
  } catch (err) {
    host.classList.add("sk-diagram-error");
    host.textContent = `Diagram error: ${err instanceof Error ? err.message.split("\n")[0] : String(err)}`;
  }
}
