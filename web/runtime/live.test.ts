import { describe, expect, it } from "vitest";
import { applyLive, applyLiveEvent, liveStamp } from "./live";
import type { Deck } from "./types";

const deck = (): Deck =>
  ({
    schemaVersion: 1,
    id: "d_x",
    title: "Live",
    theme: "aurora",
    aspect: "16:9",
    slides: [
      { id: "s_plain", layout: "title", regions: { title: [{ id: "e_t", type: "heading", props: { text: "Hi" } }] } },
      {
        id: "s_live",
        layout: "live",
        regions: { figure: [{ id: "e_n", type: "stat", props: { value: 1 } }], status: [{ id: "e_s", type: "text", props: { text: "ok" } }] },
      },
    ],
  }) as unknown as Deck;

describe("live slides", () => {
  it("puts stored live content in place, only on live slides", () => {
    const d = deck();
    const at = "2026-11-22T10:00:00Z";
    const times = applyLive(d, {
      s_live: { regions: { figure: [{ id: "e_n", type: "stat", props: { value: 9 } }] }, at },
      s_plain: { regions: {}, at },
    });
    expect(d.slides[1].regions.figure[0].props.value).toBe(9);
    expect(d.slides[0].regions.title).toHaveLength(1);
    expect(times.get("s_live")).toBe(Date.parse(at));
    expect(times.has("s_plain")).toBe(false);
  });

  it("sets, adds and removes elements", () => {
    const d = deck();
    const at = "2026-11-22T10:00:01Z";
    applyLiveEvent(d, { type: "live", slideId: "s_live", op: "set", elementId: "e_n", props: { value: 5 }, at });
    expect(d.slides[1].regions.figure[0].props.value).toBe(5);
    const added = applyLiveEvent(d, { type: "live", slideId: "s_live", op: "add", region: "status", index: 0, element: { id: "e_new", type: "text", props: { text: "new" } }, at });
    expect(added?.id).toBe("e_new");
    expect(d.slides[1].regions.status.map((e) => e.id)).toEqual(["e_new", "e_s"]);
    applyLiveEvent(d, { type: "live", slideId: "s_live", op: "remove", elementId: "e_s", at });
    expect(d.slides[1].regions.status.map((e) => e.id)).toEqual(["e_new"]);
    // Not a live slide: nothing changes.
    applyLiveEvent(d, { type: "live", slideId: "s_plain", op: "remove", elementId: "e_t", at });
    expect(d.slides[0].regions.title).toHaveLength(1);
  });

  it("shows the last-updated time unless hidden", () => {
    const d = deck();
    expect(liveStamp(d.slides[1], Date.now())?.textContent).toMatch(/^Updated /);
    expect(liveStamp(d.slides[1], undefined)).toBeNull();
    expect(liveStamp({ ...d.slides[1], hideUpdated: true }, Date.now())).toBeNull();
    expect(liveStamp(d.slides[0], Date.now())).toBeNull();
  });
});
