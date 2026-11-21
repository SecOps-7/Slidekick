import { afterEach, describe, expect, it, vi } from "vitest";
import { castMenu } from "./cast-menu";

const flush = () => new Promise((r) => setTimeout(r, 0));

function mockFetch(status: number, body: unknown) {
  const f = vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", f);
  return f;
}

afterEach(() => vi.unstubAllGlobals());

describe("castMenu", () => {
  it("stays hidden for people who can't cast the deck", async () => {
    mockFetch(403, { error: "your role (viewer) can't do that" });
    const m = castMenu("d_x", { buttonClass: "ed-btn" });
    await flush();
    await flush();
    expect(m.el.hidden).toBe(true);
  });

  it("shows the casting state from live events", async () => {
    const f = mockFetch(200, { casting: null, devices: [{ id: "cast_1", name: "Living room TV" }] });
    const said: string[] = [];
    const m = castMenu("d_x", { buttonClass: "ed-btn", say: (s) => said.push(s) });
    await flush();
    await flush();
    expect(f).toHaveBeenCalledWith("/api/decks/d_x/cast", expect.objectContaining({ method: "GET" }));
    const btn = m.el.querySelector("button")!;
    expect(m.el.hidden).toBe(false);
    expect(btn.textContent).toBe("Cast");
    m.onEvent({ type: "cast", state: "started", device: "Living room TV", deviceId: "cast_1" });
    expect(btn.textContent).toBe("Casting to Living room TV");
    m.onEvent({ type: "cast", state: "stopped", device: "Living room TV", deviceId: "cast_1" });
    expect(btn.textContent).toBe("Cast");
    expect(said).toEqual(["Stopped casting to Living room TV"]);
    m.onEvent({ type: "patch" });
    expect(btn.textContent).toBe("Cast");
  });

  it("starts casting to the chosen device", async () => {
    const f = mockFetch(200, { casting: null, devices: [{ id: "cast_1", name: "Office" }] });
    const m = castMenu("d_x", { buttonClass: "ed-btn" });
    document.body.append(m.el);
    await flush();
    await flush();
    m.el.querySelector("button")!.click();
    await flush();
    await flush();
    const item = [...m.el.querySelectorAll<HTMLButtonElement>("[role=menuitemradio]")].find((b) => b.textContent?.includes("Office"))!;
    expect(item).toBeTruthy();
    f.mockImplementation(async () => new Response(JSON.stringify({ casting: { deviceId: "cast_1", device: "Office" } }), { status: 200 }));
    item.click();
    await flush();
    await flush();
    expect(f).toHaveBeenLastCalledWith("/api/decks/d_x/cast", expect.objectContaining({ method: "POST", body: JSON.stringify({ deviceId: "cast_1" }) }));
    expect(m.el.querySelector("button")!.textContent).toBe("Casting to Office");
    m.el.remove();
  });

  it("explains an empty list and looks for devices again on refresh", async () => {
    const f = mockFetch(200, { casting: null, devices: [] });
    const m = castMenu("d_x", { buttonClass: "ed-btn" });
    document.body.append(m.el);
    await flush();
    await flush();
    m.el.querySelector("button")!.click();
    await flush();
    await flush();
    expect(m.el.textContent).toContain("No cast devices were found on your network");
    expect(m.el.textContent).toContain("add one by IP address");
    f.mockImplementation(async () => new Response(JSON.stringify({ casting: null, devices: [{ id: "auto-abc", name: "Kitchen", auto: true }] }), { status: 200 }));
    const again = m.el.querySelector<HTMLButtonElement>(".cast-refresh")!;
    expect(again.textContent).toBe("Refresh");
    again.click();
    expect(m.el.querySelector(".cast-refresh")!.textContent).toBe("Looking for devices…");
    await flush();
    await flush();
    expect(f).toHaveBeenLastCalledWith("/api/decks/d_x/cast/refresh", expect.objectContaining({ method: "POST" }));
    const item = [...m.el.querySelectorAll<HTMLButtonElement>("[role=menuitemradio]")].find((b) => b.textContent?.includes("Kitchen"));
    expect(item).toBeTruthy();
    expect(m.el.textContent).not.toContain("No cast devices were found");
    m.el.remove();
  });
});
