import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import type { Deck } from "../../runtime/types";
import { checkPatch } from "./check";

const deck = JSON.parse(readFileSync(resolve(__dirname, "../../../examples/launch.json"), "utf8")) as Deck;

describe("client-side schema check", () => {
  it("accepts valid edits, including new elements without ids", () => {
    expect(checkPatch(deck, [{ op: "replace", path: "/title", value: "Renamed" }])).toEqual([]);
    expect(checkPatch(deck, [{ op: "add", path: "/slides/-", value: { layout: "title", regions: { title: [{ type: "heading", props: { text: "Hi" } }] } } }])).toEqual([]);
  });

  it("explains invalid edits", () => {
    const problems = checkPatch(deck, [{ op: "add", path: "/slides/0/regions/title/0/props/level", value: 9 }]);
    expect(problems.length).toBeGreaterThan(0);
    expect(problems[0]).toMatch(/^slide 1, title element 1 props level: must be/);
  });

  it("rejects unknown element types and layouts", () => {
    expect(checkPatch(deck, [{ op: "replace", path: "/slides/0/layout", value: "nope" }]).length).toBeGreaterThan(0);
    expect(checkPatch(deck, [{ op: "replace", path: "/slides/0/regions/title/0/type", value: "marquee" }]).length).toBeGreaterThan(0);
  });
});
