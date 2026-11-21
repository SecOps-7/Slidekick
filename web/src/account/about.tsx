// About: what this install is. Laid out like MikroDash's about page (identity
// left, system facts right, release notes and dependencies underneath) in
// Slidekick's own logo, type and colours. One fetch of /api/about; anything
// an install doesn't have (a commit in a local build) is left out, not shown empty.
import type { ComponentChildren } from "preact";
import { useEffect, useMemo, useState } from "preact/hooks";
import { markSVG } from "../brand/brand";
import { WEB_DEPS } from "./about-deps.gen";
import "./about.css";

interface Note {
  section: string;
  text: string;
}
interface Release {
  version: string;
  title?: string;
  notes: Note[];
}
interface Dep {
  name: string;
  version: string;
  licence: string;
  url?: string;
  kind: string;
}
interface Info {
  version: string;
  commit?: string;
  built?: string;
  runtime: { go: string; platform: string; memoryMb: number; kernel?: string; uptimeSec: number };
  database: { engine: string; decks: number };
  releases: Release[];
  deps: Dep[];
  licence: string;
}

// Stroke-only 24x24 icons, so they take the text colour in either theme.
const ICONS: Record<string, string> = {
  commit: '<circle cx="12" cy="12" r="3"/><line x1="3" y1="12" x2="9" y2="12"/><line x1="15" y1="12" x2="21" y2="12"/>',
  date: '<rect x="3" y="4" width="18" height="18" rx="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>',
  clock: '<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>',
  server: '<rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/>',
  chip: '<rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><line x1="9" y1="1" x2="9" y2="4"/><line x1="15" y1="1" x2="15" y2="4"/><line x1="9" y1="20" x2="9" y2="23"/><line x1="15" y1="20" x2="15" y2="23"/><line x1="20" y1="9" x2="23" y2="9"/><line x1="20" y1="14" x2="23" y2="14"/><line x1="1" y1="9" x2="4" y2="9"/><line x1="1" y1="14" x2="4" y2="14"/>',
  layers: '<path d="M12 2 2 7l10 5 10-5-10-5z"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/>',
  db: '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>',
  award: '<circle cx="12" cy="8" r="6"/><path d="M15.48 12.89 17 22l-5-3-5 3 1.52-9.11"/>',
  slides: '<rect x="3" y="4" width="18" height="12" rx="2"/><line x1="12" y1="16" x2="12" y2="20"/><line x1="8" y1="20" x2="16" y2="20"/>',
  notes: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/>',
  box: '<path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><polyline points="3.27 6.96 12 12.01 20.73 6.96"/><line x1="12" y1="22.08" x2="12" y2="12"/>',
  bug: '<rect x="8" y="6" width="8" height="14" rx="4"/><path d="M8 11H4"/><path d="M8 16H5"/><path d="M8 7 5.5 4.5"/><path d="M16 11h4"/><path d="M16 16h3"/><path d="m16 7 2.5-2.5"/><path d="M12 6V3"/>',
  coffee: '<path d="M18 8h1a4 4 0 0 1 0 8h-1"/><path d="M2 8h16v9a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4z"/><line x1="6" y1="1" x2="6" y2="4"/><line x1="10" y1="1" x2="10" y2="4"/><line x1="14" y1="1" x2="14" y2="4"/>',
  card: '<rect x="1" y="4" width="22" height="16" rx="2"/><line x1="1" y1="10" x2="23" y2="10"/>',
  ext: '<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/>',
};

function Ico({ name, cls = "ab-ico" }: { name: string; cls?: string }) {
  return <svg viewBox="0 0 24 24" class={cls} aria-hidden="true" dangerouslySetInnerHTML={{ __html: ICONS[name] ?? "" }} />;
}

function Fact({ icon, children }: { icon?: string; children: ComponentChildren }) {
  return (
    <span class="ab-fact">
      {icon && <Ico name={icon} />}
      {children}
    </span>
  );
}

function uptime(sec: number): string {
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return d > 0 ? `${d}d ${h}h` : h > 0 ? `${h}h ${m}m` : `${m}m`;
}

// A note's badge is its change type, the changelog heading it sits under.
// An unknown heading gets the neutral pill rather than a wrong colour.
const KIND_TONE: Record<string, string> = {
  new: "teal",
  added: "teal",
  changed: "sky",
  fixed: "amber",
  security: "rose",
  removed: "rose",
  deprecated: "lime",
  internal: "violet",
};
const tone = (kind: string) => KIND_TONE[kind.trim().toLowerCase()] ?? "neutral";

function Releases({ list }: { list: Release[] }) {
  if (!list.length) return <p class="ab-empty">No release notes are packaged with this build.</p>;
  return (
    <div class="ab-releases">
      {list.map((r, i) => (
        <details class="ab-release" open={i === 0} key={r.version}>
          <summary>
            <span class="ab-rel-v">v{r.version}</span>
            {i === 0 && <span class="ab-pill ab-pill-accent">Latest</span>}
            <span class="ab-count">
              {r.notes.length} {r.notes.length === 1 ? "change" : "changes"}
            </span>
            {r.title && <span class="ab-rel-when">{r.title}</span>}
          </summary>
          <ul class="ab-notes">
            {r.notes.map((n, j) => (
              <li class="ab-note-row" key={j}>
                {n.section && <span class={`ab-pill ab-kind tone-${tone(n.section)}`}>{n.section}</span>}
                <span class="ab-note-text">{n.text}</span>
              </li>
            ))}
          </ul>
        </details>
      ))}
    </div>
  );
}

const KIND_LABEL: Record<string, string> = { go: "Go", web: "Web", font: "Font" };

function Deps({ list }: { list: Dep[] }) {
  const [q, setQ] = useState("");
  const rows = useMemo(() => {
    const s = q.trim().toLowerCase();
    return s ? list.filter((d) => d.name.toLowerCase().includes(s) || d.licence.toLowerCase().includes(s) || d.kind.includes(s)) : list;
  }, [q, list]);
  return (
    <>
      <input type="search" class="ab-search" placeholder="Search packages or licences" aria-label="Search dependencies" value={q} onInput={(e) => setQ(e.currentTarget.value)} />
      <div class="ab-dep ab-dep-head" aria-hidden="true">
        <span>Package</span>
        <span>Version</span>
        <span>Licence</span>
        <span />
      </div>
      <div class="ab-deps" role="list">
        {rows.length === 0 && <p class="ab-empty">Nothing matches that.</p>}
        {rows.map((d) => (
          <div class="ab-dep" role="listitem" key={d.kind + d.name}>
            <span class="ab-dep-name">
              <span class={`ab-kindtag kind-${d.kind}`}>{KIND_LABEL[d.kind] ?? d.kind}</span>
              {d.name}
            </span>
            <span class="ab-dep-ver">{d.version || "-"}</span>
            <span class="ab-dep-lic">{d.licence}</span>
            {d.url ? (
              <a href={d.url} target="_blank" rel="noopener noreferrer" class="ab-dep-link" aria-label={`Open ${d.name}`}>
                <Ico name="ext" cls="ab-ico-sm" />
              </a>
            ) : (
              <span />
            )}
          </div>
        ))}
      </div>
    </>
  );
}

/** The about page; `embedded` drops the page title and back link (Settings > About). */
export function About({ embedded = false }: { embedded?: boolean }) {
  const [info, setInfo] = useState<Info | null>(null);
  const [failed, setFailed] = useState(false);
  const [tab, setTab] = useState<"notes" | "deps">("notes");
  useEffect(() => {
    fetch("/api/about", { cache: "no-store" })
      .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
      .then(setInfo, () => setFailed(true));
  }, []);
  const deps = useMemo(() => [...(info?.deps ?? []), ...(WEB_DEPS as Dep[])], [info]);
  const rt = info?.runtime;

  return (
    <div class={`ab ${embedded ? "ab-embedded" : ""}`}>
      {!embedded && (
        <>
          <a href="/" class="ab-back">
            ← Your decks
          </a>
          <h1 class="ab-h1">About</h1>
        </>
      )}
      {failed && <p class="ab-empty">Couldn't load the details for this install. Reload to try again.</p>}

      <div class="ab-top">
        <section class="ab-card ab-id" aria-label="Slidekick">
          <span class="ab-logo" dangerouslySetInnerHTML={{ __html: markSVG() }} />
          <div class="ab-name">
            Slide<span>kick</span>
          </div>
          <div class="ab-slogan">Agents write the deck. You watch it move.</div>
          <div class="ab-version">
            <span class="ab-pill ab-pill-accent ab-pill-lg">v{info?.version ?? "…"}</span>
          </div>
          <div class="ab-build">
            {info?.commit && <Fact icon="commit">{info.commit.slice(0, 7)}</Fact>}
            {info?.built && <Fact icon="date">Built {info.built}</Fact>}
            {rt && <Fact icon="clock">Up {uptime(rt.uptimeSec)}</Fact>}
          </div>
          <p class="ab-blurb">A self-hosted slide app for people and AI agents: agents build decks over MCP, you watch every change land live, then present, narrate and cast them.</p>
        </section>

        <section class="ab-card ab-sys" aria-labelledby="ab-sys-h">
          <h2 class="ab-card-h" id="ab-sys-h">
            <Ico name="chip" cls="ab-hdr-ico" />
            System information
          </h2>
          <div class="ab-group">
            <div class="ab-group-label">
              <Ico name="layers" cls="ab-grp-ico" />
              Runtime
            </div>
            <div class="ab-facts">
              {rt && <span class="ab-pill">{rt.go}</span>}
              {rt && <Fact icon="server">{rt.platform}</Fact>}
              {rt?.memoryMb ? <Fact icon="chip">{rt.memoryMb} MB</Fact> : null}
              {rt?.kernel && <Fact icon="layers">Kernel {rt.kernel}</Fact>}
            </div>
          </div>
          <div class="ab-group">
            <div class="ab-group-label">
              <Ico name="db" cls="ab-grp-ico" />
              Database
            </div>
            <div class="ab-facts">
              {info && <span class="ab-pill">{info.database.engine}</span>}
              {info && (
                <Fact icon="slides">
                  {info.database.decks} {info.database.decks === 1 ? "deck" : "decks"}
                </Fact>
              )}
            </div>
          </div>
          <div class="ab-group">
            <div class="ab-group-label">
              <Ico name="award" cls="ab-grp-ico" />
              Licence
            </div>
            <div class="ab-facts">
              <span class="ab-pill ab-pill-accent">MIT</span>
              <button type="button" class="ab-inline-link" onClick={() => setTab("deps")}>
                Third-party licences
              </button>
            </div>
            {info?.licence && (
              <details class="ab-licence">
                <summary>Show the licence</summary>
                <pre>{info.licence}</pre>
              </details>
            )}
          </div>
          <div class="ab-actions">
            <a href="https://github.com/SecOps-7/Slidekick/issues" target="_blank" rel="noopener noreferrer" class="ab-inline-link">
              <Ico name="bug" cls="ab-act-ico" />
              Report a bug or idea
            </a>
            <a href="https://buymeacoffee.com/SecOps7" target="_blank" rel="noopener noreferrer" class="ab-inline-link ab-coffee">
              <Ico name="coffee" cls="ab-act-ico" />
              Buy me a coffee
            </a>
            <a href="https://paypal.me/SecOps7App" target="_blank" rel="noopener noreferrer" class="ab-inline-link">
              <Ico name="card" cls="ab-act-ico" />
              PayPal
            </a>
          </div>
          <p class="ab-note">Slidekick is free and open source, and always will be. If it saves you time, fuelling the developer with caffeine is welcome and never expected.</p>
          <p class="ab-note ab-disclaimer">
            Slidekick is independent and not affiliated with the AI, voice or casting services it connects to. Chromecast is a trademark of Google LLC; other names are trademarks of their owners.
          </p>
        </section>
      </div>

      <section class="ab-card ab-wide">
        <div class="ab-tabs" role="tablist" aria-label="About this build">
          <button type="button" role="tab" aria-selected={tab === "notes"} class={tab === "notes" ? "on" : ""} onClick={() => setTab("notes")}>
            <Ico name="notes" cls="ab-tab-ico" />
            Release notes <span class="ab-count">{info?.releases.reduce((n, r) => n + r.notes.length, 0) ?? 0}</span>
          </button>
          <button type="button" role="tab" aria-selected={tab === "deps"} class={tab === "deps" ? "on" : ""} onClick={() => setTab("deps")}>
            <Ico name="box" cls="ab-tab-ico" />
            Dependencies <span class="ab-count">{deps.length}</span>
          </button>
        </div>
        <div role="tabpanel">{tab === "notes" ? <Releases list={info?.releases ?? []} /> : <Deps list={deps} />}</div>
      </section>
    </div>
  );
}
