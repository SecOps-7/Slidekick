// Types mirroring schema/deck.schema.json. Keep in step with the schema.

export type ColorToken = "bg" | "surface" | "text" | "muted" | "accent" | "accent2";
export type Size = "xs" | "sm" | "md" | "lg" | "xl" | "xxl";
export type TransitionType = "none" | "fade" | "slide" | "zoom" | "push" | "morph";
export type BuildPreset = "fade" | "fade-up" | "scale-in" | "wipe" | "typewriter";
export type Duration = "fast" | "base" | "slow";

export interface Deck {
  schemaVersion: number;
  id: string;
  title: string;
  theme: string;
  aspect: "16:9" | "4:3";
  slides: Slide[];
}

export interface Slide {
  id: string;
  layout: string;
  regions: Record<string, Element[]>;
  transition?: { type: TransitionType; duration?: Duration };
  notes?: string;
  background?: { token?: ColorToken; image?: string; dim?: number };
}

export interface Element {
  id: string;
  type: "heading" | "text" | "bullets" | "image" | "code" | "stat" | "quote";
  props: Record<string, unknown>;
  style?: { color?: ColorToken; size?: Size; align?: "start" | "center" | "end"; weight?: "regular" | "bold"; colorRaw?: string };
  build?: { step: number; preset?: BuildPreset };
}

export interface Theme {
  name: string;
  label: string;
  description: string;
  dark: boolean;
  tokens: {
    color: Record<ColorToken, string>;
    font: { heading: string; body: string; mono: string };
    fontImport?: string;
    size: Record<Size, number>;
    radius: number;
    motion: { fast: number; base: number; slow: number; easing: string };
  };
}

export interface Catalog {
  layouts: Record<string, { description: string; regions: string[] }>;
  transitions: Record<string, string>;
  builds: Record<string, string>;
  themes: Record<string, Theme>;
}

export interface LintIssue {
  severity: "error" | "warning" | "info";
  code: string;
  slide: number;
  slideId: string;
  elementId?: string;
  message: string;
  fix?: string;
}

export interface PatchOp {
  op: "add" | "remove" | "replace" | "move" | "copy" | "test";
  path: string;
  from?: string;
  value?: unknown;
}

export interface PatchEvent {
  type: "patch";
  deckId: string;
  version: number;
  baseVersion: number;
  actor: string;
  ops: PatchOp[];
  changed: string[];
  focusSlide?: string;
}
