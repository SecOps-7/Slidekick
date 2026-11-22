// Runtime types. The enums and element props come from schema.gen.ts, which
// is generated from schema/deck.schema.json (npm run types), so the schema
// stays the single contract. Props stay loose at runtime (decks may come
// from older schema versions); ElementProps types them where it matters.
import type * as S from "./schema.gen";

export type ColorToken = S.ColorToken;
export type Size = NonNullable<S.Style["size"]>;
export type TransitionType = S.Transition["type"];
export type BuildPreset = NonNullable<S.Build["preset"]>;
export type Duration = NonNullable<S.Transition["duration"]>;
export type Layout = S.Slide["layout"];
export type ElementType = S.Element["type"];

/** Props of each element type, as the schema defines them. */
export interface ElementProps {
  heading: S.PropsHeading;
  text: S.PropsText;
  bullets: S.PropsBullets;
  image: S.PropsImage;
  code: S.PropsCode;
  stat: S.PropsStat;
  quote: S.PropsQuote;
  chart: S.PropsChart;
  diagram: S.PropsDiagram;
  icon: S.PropsIcon;
  shape: S.PropsShape;
  embed: S.PropsEmbed;
  html: S.PropsHtml;
}
// Fails to compile if the schema gains an element type ElementProps lacks (or the reverse).
type Same<A, B> = [A] extends [B] ? ([B] extends [A] ? true : false) : false;
export const elementPropsCoverSchema: Same<keyof ElementProps, ElementType> = true;

export interface Deck {
  schemaVersion: number;
  id: string;
  title: string;
  theme: string;
  aspect: S.SlidekickDeck["aspect"];
  slides: Slide[];
}

export interface Slide {
  id: string;
  layout: string;
  regions: Record<string, Element[]>;
  transition?: S.Transition;
  notes?: string;
  background?: S.Background;
  /** Live slides only: hide the small last-updated time. */
  hideUpdated?: boolean;
}

export interface Element {
  id: string;
  type: ElementType;
  props: Record<string, unknown>;
  style?: S.Style;
  build?: S.Build;
}

export interface Theme {
  name: string;
  label: string;
  description: string;
  dark: boolean;
  tokens: {
    color: Record<ColorToken, string>;
    font: { heading: string; body: string; mono: string };
    fonts: string[];
    size: Record<Size, number>;
    radius: number;
    motion: { fast: number; base: number; slow: number; easing: string; bounce?: number };
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
