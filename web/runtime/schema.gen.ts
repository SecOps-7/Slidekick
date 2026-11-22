/* Generated from schema/deck.schema.json by scripts/gen-types.mjs. Do not edit; run `npm run types`. */

/**
 * Stable id. Keep it when editing so patches stay precise and morph transitions can match elements across slides.
 *
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "id".
 */
export type Id = string;
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "element".
 */
export type Element = {
  id: Id;
  type: "heading" | "text" | "bullets" | "image" | "code" | "stat" | "quote" | "chart" | "diagram" | "icon" | "shape" | "embed" | "html";
  props: {};
  style?: Style;
  build?: Build;
};
/**
 * Theme colour token.
 *
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "colorToken".
 */
export type ColorToken = "bg" | "surface" | "text" | "muted" | "accent" | "accent2";
/**
 * Rich inline text: **bold**, *italic*, `code` and [link](https://...) are supported.
 *
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "inline".
 */
export type Inline = string;

/**
 * A Slidekick deck. Agents compose elements inside named layout regions; the theme decides how everything looks. Never write raw HTML.
 */
export interface SlidekickDeck {
  /**
   * Schema version. Older decks are migrated on load.
   */
  schemaVersion: 1;
  id: Id;
  title: string;
  /**
   * Theme name; see the theme catalogue resource.
   */
  theme: string;
  aspect: "16:9" | "4:3";
  /**
   * @maxItems 300
   */
  slides: Slide[];
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "slide".
 */
export interface Slide {
  id: Id;
  /**
   * Layout preset; see the layout catalogue resource for each layout's region names.
   */
  layout: "title" | "title-body" | "two-column" | "hero-image" | "big-number" | "quote" | "grid-3" | "full-bleed" | "live";
  /**
   * Map of region name to the elements placed in it, in order.
   */
  regions: {
    /**
     * @maxItems 12
     */
    [k: string]: Element[];
  };
  transition?: Transition;
  /**
   * Speaker notes.
   */
  notes?: string;
  background?: Background;
  /**
   * Live slides only: hide the small last-updated time in the corner.
   */
  hideUpdated?: boolean;
}
/**
 * Overrides limited to theme tokens.
 *
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "style".
 */
export interface Style {
  color?: ColorToken;
  size?: "xs" | "sm" | "md" | "lg" | "xl" | "xxl";
  align?: "start" | "center" | "end";
  weight?: "regular" | "bold";
  /**
   * Escape hatch: a raw hex colour. Prefer 'color'; lint warns when this is used.
   */
  colorRaw?: string;
}
/**
 * Reveal this element on a build step. Elements without a build are visible when the slide appears.
 *
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "build".
 */
export interface Build {
  step: number;
  preset?:
    | "fade"
    | "fade-up"
    | "scale-in"
    | "wipe"
    | "typewriter"
    | "draw"
    | "fade-down"
    | "fade-left"
    | "fade-right"
    | "slide-up"
    | "slide-down"
    | "slide-left"
    | "slide-right"
    | "zoom-in"
    | "zoom-out"
    | "pop"
    | "drop"
    | "bounce-in"
    | "flip-x"
    | "flip-y"
    | "rotate-in"
    | "swing"
    | "blur-in"
    | "reveal"
    | "wipe-down"
    | "iris"
    | "stretch"
    | "highlight";
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "transition".
 */
export interface Transition {
  /**
   * How this slide enters. 'morph' animates elements that share an id with the previous slide.
   */
  type:
    | "none"
    | "fade"
    | "slide"
    | "zoom"
    | "push"
    | "zoom-out"
    | "fade-through"
    | "cover"
    | "uncover"
    | "push-up"
    | "wipe"
    | "split"
    | "iris"
    | "blur"
    | "flip"
    | "cube"
    | "swap"
    | "morph";
  duration?: "fast" | "base" | "slow";
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "background".
 */
export interface Background {
  token?: ColorToken;
  /**
   * Image URL: https, an uploaded file (/files/...) or a data:image URI.
   */
  image?: string;
  /**
   * Darken an image background for legibility.
   */
  dim?: number;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-heading".
 */
export interface PropsHeading {
  /**
   * Rich inline text: **bold**, *italic*, `code` and [link](https://...) are supported.
   */
  text: string;
  level?: 1 | 2 | 3;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-text".
 */
export interface PropsText {
  /**
   * Rich inline text: **bold**, *italic*, `code` and [link](https://...) are supported.
   */
  text: string;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-bullets".
 */
export interface PropsBullets {
  /**
   * @minItems 1
   * @maxItems 12
   *
   * Items: Rich inline text: **bold**, *italic*, `code` and [link](https://...) are supported.
   */
  items: [string, ...string[]];
  ordered?: boolean;
  /**
   * Reveal items one build step at a time, starting at this element's build step.
   */
  buildEach?: boolean;
  minItems?: 0;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-image".
 */
export interface PropsImage {
  /**
   * https URL, an uploaded file (/files/... from upload_asset) or a data:image URI.
   */
  src: string;
  /**
   * Required for accessibility unless purely decorative (then set decorative: true).
   */
  alt?: string;
  decorative?: boolean;
  fit?: "cover" | "contain";
  caption?: string;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-code".
 */
export interface PropsCode {
  code: string;
  language?: string;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-stat".
 */
export interface PropsStat {
  /**
   * Counts up from zero when the element appears.
   */
  value: number;
  decimals?: number;
  prefix?: string;
  suffix?: string;
  label?: string;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-quote".
 */
export interface PropsQuote {
  /**
   * Rich inline text: **bold**, *italic*, `code` and [link](https://...) are supported.
   */
  text: string;
  attribution?: string;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-chart".
 */
export interface PropsChart {
  /**
   * bar and line plot every series against labels; pie uses the first series only.
   */
  kind: "bar" | "line" | "pie";
  /**
   * @minItems 1
   * @maxItems 24
   */
  labels: [string, ...string[]];
  /**
   * @minItems 1
   * @maxItems 6
   */
  series: [
    {
      name?: string;
      /**
       * @minItems 1
       * @maxItems 24
       */
      values: [number, ...number[]];
      color?: ColorToken;
    },
    ...{
      name?: string;
      /**
       * @minItems 1
       * @maxItems 24
       */
      values: [number, ...number[]];
      color?: ColorToken;
    }[]
  ];
  /**
   * Suffix for values, e.g. '%' or 'k'.
   */
  unit?: string;
  /**
   * Print each value on the chart.
   */
  showValues?: boolean;
  /**
   * Short description, also used as the accessible label.
   */
  caption?: string;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-diagram".
 */
export interface PropsDiagram {
  /**
   * Mermaid source, e.g. 'flowchart LR\n  A[Agent] --> B[Deck]'.
   */
  source: string;
  /**
   * Accessible description of what the diagram shows.
   */
  caption?: string;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-icon".
 */
export interface PropsIcon {
  /**
   * Icon name from the icon catalogue (slidekick://catalog/elements).
   */
  name: string;
  /**
   * Accessible label. Omit for a decorative icon.
   */
  label?: string;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-shape".
 */
export interface PropsShape {
  kind: "rect" | "circle" | "pill" | "line" | "arrow";
  fill?: ColorToken;
  /**
   * Optional text inside the shape.
   */
  label?: string;
}
/**
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-embed".
 */
export interface PropsEmbed {
  kind: "iframe" | "video";
  /**
   * https URL, or an uploaded file (/files/...) for video.
   */
  src: string;
  /**
   * Required for iframes (accessibility).
   */
  title?: string;
  poster?: string;
  autoplay?: boolean;
  loop?: boolean;
  muted?: boolean;
}
/**
 * Escape hatch: raw HTML and CSS for something the other elements can't express. Rendered in a sandboxed frame with no scripts, forms or network access except https images and fonts. Theme tokens are available as CSS variables (--sk-text, --sk-accent, --sk-font-body, --sk-size-md...). Lint warns every time it is used.
 *
 * This interface was referenced by `SlidekickDeck`'s JSON-Schema
 * via the `definition` "props-html".
 */
export interface PropsHtml {
  /**
   * HTML fragment (may include a <style> block). Scripts never run.
   */
  html: string;
  /**
   * Describes the content for screen readers.
   */
  title?: string;
}
