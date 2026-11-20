import { useEffect, useState } from "preact/hooks";
import type { Catalog, ColorToken, Deck, Element, LintIssue, PatchOp, Slide } from "../../runtime/types";
import type { Comment } from "./api";
import { describe, elPath, type ElLoc, ELEMENT_DEFAULTS, newElement, remapRegions, setKey, setProp, setSub, slidePath, stripIds } from "./ops";

const TOKENS: ColorToken[] = ["bg", "surface", "text", "muted", "accent", "accent2"];
const SIZES = ["xs", "sm", "md", "lg", "xl", "xxl"];

type Cat = Catalog & { icons: string[]; elements: Record<string, string> };

// ---- small controls that commit on blur / change ----

function Text({ label, value, onCommit, area, rows, mono, placeholder }: { label: string; value: string; onCommit: (v: string) => void; area?: boolean; rows?: number; mono?: boolean; placeholder?: string }) {
  const [v, setV] = useState(value);
  useEffect(() => setV(value), [value]);
  const commit = () => v !== value && onCommit(v);
  return (
    <label class="ed-field">
      <span>{label}</span>
      {area ? (
        <textarea rows={rows ?? 3} class={mono ? "mono" : ""} value={v} placeholder={placeholder} onInput={(e) => setV(e.currentTarget.value)} onBlur={commit} />
      ) : (
        <input value={v} placeholder={placeholder} onInput={(e) => setV(e.currentTarget.value)} onBlur={commit} onKeyDown={(e) => e.key === "Enter" && commit()} />
      )}
    </label>
  );
}

function Num({ label, value, onCommit, step }: { label: string; value: number | undefined; onCommit: (v: number | undefined) => void; step?: number }) {
  const [v, setV] = useState(value === undefined ? "" : String(value));
  useEffect(() => setV(value === undefined ? "" : String(value)), [value]);
  const commit = () => {
    const n = v.trim() === "" ? undefined : Number(v);
    if (n !== value && (n === undefined || Number.isFinite(n))) onCommit(n);
  };
  return (
    <label class="ed-field ed-field-sm">
      <span>{label}</span>
      <input type="number" step={step ?? 1} value={v} onInput={(e) => setV(e.currentTarget.value)} onBlur={commit} onKeyDown={(e) => e.key === "Enter" && commit()} />
    </label>
  );
}

function Select({ label, value, options, onChange, none }: { label: string; value: string; options: string[]; onChange: (v: string) => void; none?: string }) {
  return (
    <label class="ed-field ed-field-sm">
      <span>{label}</span>
      <select value={value} onChange={(e) => onChange(e.currentTarget.value)}>
        {none !== undefined && <option value="">{none}</option>}
        {options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    </label>
  );
}

function Check({ label, value, onChange }: { label: string; value: boolean; onChange: (v: boolean) => void }) {
  return (
    <label class="ed-check">
      <input type="checkbox" checked={value} onChange={(e) => onChange(e.currentTarget.checked)} /> {label}
    </label>
  );
}

function Swatches({ label, value, onChange, allowNone = true }: { label: string; value: string; onChange: (v: string) => void; allowNone?: boolean }) {
  return (
    <div class="ed-field">
      <span>{label}</span>
      <div class="ed-swatches" role="radiogroup" aria-label={label}>
        {allowNone && (
          <button type="button" role="radio" aria-checked={!value} class={`ed-swatch none${!value ? " on" : ""}`} title="Theme default" onClick={() => onChange("")}>
            ∅
          </button>
        )}
        {TOKENS.map((t) => (
          <button type="button" role="radio" aria-checked={value === t} key={t} class={`ed-swatch${value === t ? " on" : ""}`} title={t} style={{ background: `var(--sk-${t})` }} onClick={() => onChange(t)} />
        ))}
      </div>
    </div>
  );
}

// ---- element editor ----

function ElementPanel({ deck, cat, loc, commit, upload }: { deck: Deck; cat: Cat; loc: ElLoc; commit: (ops: PatchOp[], label: string) => void; upload: (f: File) => Promise<string> }) {
  const el = loc.el;
  const p = el.props as Record<string, any>;
  const path = elPath(loc);
  const prop = (k: string, v: unknown) => commit(setProp(loc, k, v), `Edit ${k}`);
  const style = (k: string, v: unknown) => commit(setSub(path, el as unknown as Record<string, unknown>, "style", k, v), `Style ${k}`);
  const regionEls = deck.slides[loc.slideIndex].regions[loc.region];
  const [json, setJson] = useState("");
  useEffect(() => setJson(JSON.stringify(el.props, null, 2)), [JSON.stringify(el.props)]);

  const pickImage = async () => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = "image/png,image/jpeg,image/gif,image/webp,image/avif,image/svg+xml";
    input.onchange = async () => {
      const f = input.files?.[0];
      if (f) prop("src", await upload(f));
    };
    input.click();
  };

  return (
    <div class="ed-panel-body">
      <h3>
        {el.type[0].toUpperCase() + el.type.slice(1)} <small>{el.id}</small>
      </h3>

      {el.type === "heading" && (
        <>
          <Text label="Text" value={p.text ?? ""} onCommit={(v) => prop("text", v)} area rows={2} />
          <Select label="Level" value={String(p.level ?? 1)} options={["1", "2", "3"]} onChange={(v) => prop("level", Number(v))} />
        </>
      )}
      {el.type === "text" && <Text label="Text" value={p.text ?? ""} onCommit={(v) => prop("text", v)} area rows={4} />}
      {el.type === "quote" && (
        <>
          <Text label="Quote" value={p.text ?? ""} onCommit={(v) => prop("text", v)} area rows={3} />
          <Text label="Attribution" value={p.attribution ?? ""} onCommit={(v) => prop("attribution", v)} />
        </>
      )}
      {el.type === "bullets" && (
        <>
          <Text label="Items (one per line)" value={(p.items ?? []).join("\n")} onCommit={(v) => prop("items", v.split("\n").map((s) => s.trim()).filter(Boolean))} area rows={5} />
          <Check label="Numbered" value={Boolean(p.ordered)} onChange={(v) => prop("ordered", v || undefined)} />
          <Check label="Reveal one item per click" value={Boolean(p.buildEach)} onChange={(v) => prop("buildEach", v || undefined)} />
        </>
      )}
      {el.type === "image" && (
        <>
          <div class="ed-row">
            <Text label="Source" value={p.src?.startsWith("data:") ? "(placeholder)" : (p.src ?? "")} onCommit={(v) => prop("src", v)} />
            <button type="button" class="ed-btn" onClick={pickImage}>
              Upload…
            </button>
          </div>
          <Text label="Alt text" value={p.alt ?? ""} onCommit={(v) => prop("alt", v)} placeholder="Describe the image" />
          <Check label="Decorative (no alt needed)" value={Boolean(p.decorative)} onChange={(v) => prop("decorative", v || undefined)} />
          <Select label="Fit" value={p.fit ?? "cover"} options={["cover", "contain"]} onChange={(v) => prop("fit", v)} />
          <Text label="Caption" value={p.caption ?? ""} onCommit={(v) => prop("caption", v)} />
        </>
      )}
      {el.type === "code" && (
        <>
          <Text label="Language" value={p.language ?? ""} onCommit={(v) => prop("language", v)} />
          <Text label="Code" value={p.code ?? ""} onCommit={(v) => prop("code", v)} area rows={8} mono />
        </>
      )}
      {el.type === "stat" && (
        <>
          <div class="ed-row">
            <Num label="Value" value={p.value} step={0.1} onCommit={(v) => prop("value", v ?? 0)} />
            <Num label="Decimals" value={p.decimals} onCommit={(v) => prop("decimals", v)} />
          </div>
          <div class="ed-row">
            <Text label="Prefix" value={p.prefix ?? ""} onCommit={(v) => prop("prefix", v)} />
            <Text label="Suffix" value={p.suffix ?? ""} onCommit={(v) => prop("suffix", v)} />
          </div>
          <Text label="Label" value={p.label ?? ""} onCommit={(v) => prop("label", v)} />
        </>
      )}
      {el.type === "icon" && (
        <>
          <Select label="Icon" value={p.name ?? ""} options={cat.icons} onChange={(v) => prop("name", v)} />
          <Text label="Label (for screen readers)" value={p.label ?? ""} onCommit={(v) => prop("label", v)} />
        </>
      )}
      {el.type === "shape" && (
        <>
          <Select label="Kind" value={p.kind ?? "rect"} options={["rect", "circle", "pill", "line", "arrow"]} onChange={(v) => prop("kind", v)} />
          <Swatches label="Fill" value={p.fill ?? ""} onChange={(v) => prop("fill", v)} />
          <Text label="Label" value={p.label ?? ""} onCommit={(v) => prop("label", v)} />
        </>
      )}
      {el.type === "diagram" && (
        <>
          <Text label="Mermaid source" value={p.source ?? ""} onCommit={(v) => prop("source", v)} area rows={8} mono />
          <Text label="Caption" value={p.caption ?? ""} onCommit={(v) => prop("caption", v)} />
        </>
      )}
      {el.type === "embed" && (
        <>
          <Select label="Kind" value={p.kind ?? "iframe"} options={["iframe", "video"]} onChange={(v) => prop("kind", v)} />
          <Text label="Source (https)" value={p.src ?? ""} onCommit={(v) => prop("src", v)} />
          <Text label="Title" value={p.title ?? ""} onCommit={(v) => prop("title", v)} />
        </>
      )}
      {(el.type === "chart" || el.type === "embed" || el.type === "diagram") && <p class="ed-note">Chart data and other details: edit the JSON below.</p>}

      <h4>Style</h4>
      <Swatches label="Colour" value={el.style?.color ?? ""} onChange={(v) => style("color", v)} />
      <div class="ed-row">
        <Select label="Size" value={el.style?.size ?? ""} options={SIZES} none="default" onChange={(v) => style("size", v)} />
        <Select label="Align" value={el.style?.align ?? ""} options={["start", "center", "end"]} none="default" onChange={(v) => style("align", v)} />
        <Select label="Weight" value={el.style?.weight ?? ""} options={["regular", "bold"]} none="default" onChange={(v) => style("weight", v)} />
      </div>

      <h4>Build</h4>
      <div class="ed-row">
        <Select
          label="Appears"
          value={String(el.build?.step ?? 0)}
          options={Array.from({ length: 11 }, (_, i) => String(i))}
          onChange={(v) =>
            commit(
              Number(v) === 0 ? setKey(path, el as unknown as Record<string, unknown>, "build", undefined) : [{ op: "add", path: `${path}/build`, value: { ...(el.build ?? {}), step: Number(v) } }],
              "Build step",
            )
          }
        />
        <Select
          label="Animation"
          value={el.build?.preset ?? "fade-up"}
          options={Object.keys(cat.builds)}
          onChange={(v) => el.build && commit([{ op: "add", path: `${path}/build`, value: { ...el.build, preset: v } }], "Build preset")}
        />
      </div>
      <p class="ed-note">0 = visible when the slide appears; 1, 2, 3… = revealed on each click.</p>

      <details class="ed-advanced">
        <summary>Advanced: props JSON</summary>
        <textarea class="mono" rows={10} value={json} onInput={(e) => setJson(e.currentTarget.value)} aria-label="Props JSON" />
        <button
          type="button"
          class="ed-btn"
          onClick={() => {
            try {
              commit([{ op: "replace", path: `${path}/props`, value: JSON.parse(json) }], "Edit props");
            } catch {
              alert("That isn't valid JSON.");
            }
          }}
        >
          Apply JSON
        </button>
      </details>

      <h4>Arrange</h4>
      <div class="ed-row ed-actions">
        <button type="button" class="ed-btn" disabled={loc.index === 0} onClick={() => commit([{ op: "move", from: path, path: `${slidePath(loc.slideIndex)}/regions/${loc.region}/${loc.index - 1}` }], "Move up")}>
          ↑ Up
        </button>
        <button type="button" class="ed-btn" disabled={loc.index >= regionEls.length - 1} onClick={() => commit([{ op: "move", from: path, path: `${slidePath(loc.slideIndex)}/regions/${loc.region}/${loc.index + 1}` }], "Move down")}>
          ↓ Down
        </button>
        <button type="button" class="ed-btn" onClick={() => commit([{ op: "add", path: `${slidePath(loc.slideIndex)}/regions/${loc.region}/${loc.index + 1}`, value: stripIds(el) }], "Duplicate element")}>
          Duplicate
        </button>
        <button type="button" class="ed-btn danger" onClick={() => commit([{ op: "remove", path }], "Delete element")}>
          Delete
        </button>
      </div>
    </div>
  );
}

// ---- slide editor ----

function SlidePanel({ cat, slide, index, commit }: { cat: Cat; slide: Slide; index: number; commit: (ops: PatchOp[], label: string) => void }) {
  const path = slidePath(index);
  const regions = cat.layouts[slide.layout]?.regions ?? [];
  const [region, setRegion] = useState(regions.find((r) => r !== "title") ?? regions[0]);
  const [type, setType] = useState<Element["type"]>("text");
  useEffect(() => {
    if (!regions.includes(region)) setRegion(regions.find((r) => r !== "title") ?? regions[0]);
  }, [slide.layout]);
  const s = slide as unknown as Record<string, unknown>;
  return (
    <div class="ed-panel-body">
      <h3>
        Slide {index + 1} <small>{slide.id}</small>
      </h3>
      <Select
        label="Layout"
        value={slide.layout}
        options={Object.keys(cat.layouts)}
        onChange={(v) =>
          commit(
            [
              { op: "replace", path: `${path}/layout`, value: v },
              { op: "replace", path: `${path}/regions`, value: remapRegions(slide, cat.layouts[v].regions) },
            ],
            "Change layout",
          )
        }
      />
      <p class="ed-note">{cat.layouts[slide.layout]?.description}</p>
      <div class="ed-row">
        <Select
          label="Transition"
          value={slide.transition?.type ?? "fade"}
          options={Object.keys(cat.transitions)}
          onChange={(v) => commit(setSub("", { t: slide.transition }, "t", "type", v).map((o) => ({ ...o, path: `${path}/transition` })), "Transition")}
        />
        <Select
          label="Speed"
          value={slide.transition?.duration ?? "base"}
          options={["fast", "base", "slow"]}
          onChange={(v) => commit([{ op: "add", path: `${path}/transition`, value: { type: slide.transition?.type ?? "fade", ...slide.transition, duration: v } }], "Transition speed")}
        />
      </div>
      <Swatches label="Background" value={slide.background?.token ?? ""} onChange={(v) => commit(setSub(path, s, "background", "token", v), "Background")} />
      <Text label="Speaker notes" value={slide.notes ?? ""} onCommit={(v) => commit(setKey(path, s, "notes", v), "Notes")} area rows={5} />

      <h4>Add element</h4>
      <div class="ed-row">
        <Select label="Region" value={region} options={regions} onChange={setRegion} />
        <Select label="Type" value={type} options={Object.keys(ELEMENT_DEFAULTS)} onChange={(v) => setType(v as Element["type"])} />
      </div>
      <button
        type="button"
        class="ed-btn ed-btn-wide"
        onClick={() =>
          commit(
            slide.regions[region]
              ? [{ op: "add", path: `${path}/regions/${region}/-`, value: newElement(type) }]
              : [{ op: "add", path: `${path}/regions/${region}`, value: [newElement(type)] }],
            "Add element",
          )
        }
      >
        + Add {type} to {region}
      </button>
      <p class="ed-note">{cat.elements[type]}</p>
    </div>
  );
}

// ---- comments ----

function Comments({ deck, comments, sel, onAdd, onResolve, onSelect }: {
  deck: Deck;
  comments: Comment[];
  sel: { slideId: string; elId: string | null };
  onAdd: (text: string) => void;
  onResolve: (id: string) => void;
  onSelect: (slideId: string, elId: string | null) => void;
}) {
  const [text, setText] = useState("");
  const slideNo = (id?: string) => (id ? deck.slides.findIndex((s) => s.id === id) + 1 : 0);
  return (
    <div class="ed-panel-body">
      <h3>Send to agent</h3>
      <p class="ed-note">Pin a request to the {sel.elId ? "selected element" : `slide ${slideNo(sel.slideId)}`}. Your agent sees it in every write result and with list_comments.</p>
      <textarea rows={3} value={text} placeholder="e.g. Make this headline punchier" onInput={(e) => setText(e.currentTarget.value)} aria-label="Comment" />
      <button
        type="button"
        class="ed-btn ed-btn-wide"
        disabled={!text.trim()}
        onClick={() => {
          onAdd(text.trim());
          setText("");
        }}
      >
        Send to agent
      </button>
      <h4>Open ({comments.filter((c) => c.status === "open").length})</h4>
      {comments.length === 0 && <p class="ed-note">No comments yet.</p>}
      <ul class="ed-comments">
        {comments.map((c) => (
          <li key={c.id} class={c.status}>
            <button type="button" class="ed-link" onClick={() => c.slideId && onSelect(c.slideId, c.elementId || null)}>
              {c.slideId ? `Slide ${slideNo(c.slideId)}${c.elementId ? " · element" : ""}` : "Deck"}
            </button>
            <p>{c.text}</p>
            {c.reply && <p class="reply">↳ {c.reply}</p>}
            {c.status === "open" && (
              <button type="button" class="ed-btn small" onClick={() => onResolve(c.id)}>
                Resolve
              </button>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function Lint({ deck, lint, onSelect }: { deck: Deck; lint: LintIssue[]; onSelect: (slideId: string, elId: string | null) => void }) {
  return (
    <div class="ed-panel-body">
      <h3>Lint</h3>
      {lint.length === 0 && <p class="ed-note">No problems found.</p>}
      <ul class="ed-lint">
        {lint.map((i, k) => (
          <li key={k} class={i.severity}>
            <button type="button" onClick={() => i.slideId && onSelect(i.slideId, i.elementId ?? null)}>
              <b>{i.slide >= 0 ? `Slide ${i.slide + 1}` : deck.title}</b> {i.message}
              {i.fix && <small>{i.fix}</small>}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

export interface PanelProps {
  deck: Deck;
  cat: Cat;
  slideIndex: number;
  loc: ElLoc | null;
  lint: LintIssue[];
  comments: Comment[];
  commit: (ops: PatchOp[], label: string) => void;
  upload: (f: File) => Promise<string>;
  addComment: (text: string) => void;
  resolve: (id: string) => void;
  select: (slideId: string, elId: string | null) => void;
}

export function Panel(props: PanelProps) {
  const [tab, setTab] = useState<"props" | "comments" | "lint">("props");
  const { deck, cat, slideIndex, loc, lint } = props;
  const slide = deck.slides[slideIndex];
  const open = props.comments.filter((c) => c.status === "open").length;
  const problems = lint.filter((i) => i.severity !== "info").length;
  return (
    <aside class="ed-panel">
      <div class="ed-tabs" role="tablist">
        <button role="tab" type="button" aria-selected={tab === "props"} onClick={() => setTab("props")}>
          {loc ? describe(loc.el) : "Slide"}
        </button>
        <button role="tab" type="button" aria-selected={tab === "comments"} onClick={() => setTab("comments")}>
          Agent{open ? ` (${open})` : ""}
        </button>
        <button role="tab" type="button" aria-selected={tab === "lint"} onClick={() => setTab("lint")} class={problems ? "warn" : ""}>
          Lint{lint.length ? ` (${lint.length})` : ""}
        </button>
      </div>
      {tab === "props" && slide && (loc ? <ElementPanel deck={deck} cat={cat} loc={loc} commit={props.commit} upload={props.upload} /> : <SlidePanel cat={cat} slide={slide} index={slideIndex} commit={props.commit} />)}
      {tab === "comments" && slide && (
        <Comments deck={deck} comments={props.comments} sel={{ slideId: slide.id, elId: loc?.el.id ?? null }} onAdd={props.addComment} onResolve={props.resolve} onSelect={props.select} />
      )}
      {tab === "lint" && <Lint deck={deck} lint={lint} onSelect={props.select} />}
    </aside>
  );
}
