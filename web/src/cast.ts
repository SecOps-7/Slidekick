// Cast mode (/d/{id}/present?cast=1[&k=key]): what a TV shows when the
// server casts a deck to it. Full-screen slides only: no controls, no cursor,
// nothing to click. It follows whoever drives the deck (the presenter's
// moves, and agents or the AI bringing a slide into view) and keeps its live
// connection coming back, since nobody is there to press reload.
//
// The TV is a presenting screen, so AI narration plays here out loud and each
// line is acknowledged: see onNarrate. Captions are off (the room hears the
// voice); add &captions=1 to the cast URL to show them.
import { applyPatch } from "../runtime/patch";
import type { LiveEvent, LiveMap } from "../runtime/live";
import { Player } from "../runtime/player";
import type { Catalog, Deck, PatchEvent } from "../runtime/types";
import { viewFetch } from "./key";
import { Laser } from "./laser";
import { Live, type LiveMsg, type PointerMsg, type PresentMsg } from "./sync";
import { followTo, isNarration, Narrator, slideIndex } from "./narrate";
import "./cast.css";

let narrator: Narrator | null = null;

/**
 * Called for every AI presentation event on the deck ({"type":"present",
 * "state":...}): the TV plays the line, because it is the screen the room
 * listens to, and acknowledges it so the presentation moves on.
 */
export function onNarrate(event: LiveMsg): void {
  narrator?.handle(event);
}

const note = (host: HTMLElement, text: string) => {
  const n = document.createElement("div");
  n.className = "cast-note";
  n.textContent = text;
  host.replaceChildren(n);
};

export async function mountCast(host: HTMLElement, deckId: string) {
  document.body.classList.add("sk-cast");
  document.title = "Slidekick";
  const deckURL = `/api/decks/${encodeURIComponent(deckId)}`;
  const load = async () => {
    const r = await viewFetch(deckURL);
    if (!r.ok) throw new Error(String(r.status));
    return (await r.json()) as { version: number; deck: Deck; live?: LiveMap };
  };

  // The TV may come up before the network: keep trying rather than give up.
  let catalog: Catalog | null = null;
  let initial: { version: number; deck: Deck; live?: LiveMap } | null = null;
  for (let wait = 1000; !initial; wait = Math.min(wait * 2, 15000)) {
    try {
      catalog ??= (await (await fetch("/api/catalog", { cache: "no-store" })).json()) as Catalog;
      initial = await load();
    } catch (e) {
      note(host, String(e).includes("404") ? "This deck isn't available. Ask for it to be cast again." : "Connecting…");
      await new Promise((r) => setTimeout(r, wait));
    }
  }
  let deck = initial.deck;
  let version = initial.version;
  document.title = deck.title;
  host.replaceChildren();
  const player = new Player(host, catalog!);
  player.setDeck(deck, { live: initial.live });
  const laser = new Laser(player.stage);
  const m = /^#(\d+)(?:\.(\d+))?$/.exec(location.hash);
  await player.goto(m ? Number(m[1]) - 1 : 0, m?.[2] ? Number(m[2]) : 0, { animate: false });
  narrator = new Narrator({
    deckId,
    host,
    ack: true,
    audible: true,
    captions: new URLSearchParams(location.search).has("captions"),
    show: (index, build, slideId) => followTo(player, slideIndex(deck.slides, index, slideId), build),
  });

  const reload = async () => {
    const r = await load().catch(() => null);
    if (!r) return;
    deck = r.deck;
    version = r.version;
    player.setDeck(deck, { keepSlideId: player.slideId, live: r.live ?? {} });
    await player.refresh();
  };

  const onPatch = async (ev: PatchEvent) => {
    if (ev.version <= version) return;
    try {
      // An edit to a slide with live content replaces that content: fetch the deck afresh.
      if (ev.baseVersion !== version || player.touchesLive(ev.changed)) throw new Error("gap");
      deck = applyPatch(deck, ev.ops);
      version = ev.version;
    } catch {
      return reload();
    }
    player.setDeck(deck, { keepSlideId: player.slideId });
    await player.refresh();
  };

  // Same moves as the audience window: animate single steps, jump otherwise.
  const followPresenter = async (pm: PresentMsg) => {
    const p = player.position;
    if (pm.index === p.index && pm.step === p.step + 1) await player.next();
    else if (pm.index === p.index && pm.step === p.step - 1) await player.prev();
    else if (pm.index === p.index + 1 && pm.step === 0 && p.step >= p.maxStep) await player.next();
    else if (pm.index === p.index - 1 && p.step === 0) await player.goto(pm.index, pm.step, { direction: -1 });
    else if (pm.index !== p.index || pm.step !== p.step) await player.goto(pm.index, pm.step, { animate: pm.index !== p.index });
  };

  const onMsg = async (msg: LiveMsg) => {
    switch (msg.type) {
      case "hello":
        if (msg.version !== version) await reload();
        break;
      case "patch":
        await onPatch(msg as unknown as PatchEvent);
        break;
      case "live":
        await player.live(msg as unknown as LiveEvent);
        break;
      case "present":
        if (isNarration(msg)) onNarrate(msg);
        else await followPresenter(msg as unknown as PresentMsg);
        break;
      case "focus": {
        const i = deck.slides.findIndex((s) => s.id === msg.slideId);
        if (i >= 0 && i !== player.position.index) await player.goto(i, Infinity);
        break;
      }
      case "pointer": {
        const pm = msg as unknown as PointerMsg;
        pm.on ? laser.show(pm.x, pm.y) : laser.hide();
        break;
      }
      case "deleted":
        note(host, "This deck was deleted.");
        break;
      case "denied":
        // The deck went private or its key changed. Live stops retrying on
        // that, but the TV has no one to fix it, so keep trying slowly: a
        // new cast reloads the page with the right key anyway.
        setTimeout(connect, 30000);
        break;
    }
  };

  let live: Live | null = null;
  const connect = () => {
    live?.close();
    live = new Live(deckId, (msg) => void onMsg(msg));
  };
  connect();
  // Ask for the deck again when the TV wakes from standby.
  document.addEventListener("visibilitychange", () => document.visibilityState === "visible" && void reload());
}
