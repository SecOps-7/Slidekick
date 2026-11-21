// Live connection to a deck: change events, comments, and presenter relay.
// Presenter position and the laser pointer also travel over a same-machine
// BroadcastChannel, so an audience window follows instantly even offline.

import { withKey } from "./key";

export interface PresentMsg {
  type: "present";
  index: number;
  step: number;
  from?: string;
}
export interface PointerMsg {
  type: "pointer";
  x: number;
  y: number;
  on: boolean;
  from?: string;
}
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type LiveMsg = { type: string; [k: string]: any };

export class Live {
  private ws?: WebSocket;
  private retry = 500;
  private closed = false;
  private bc?: BroadcastChannel;
  conn = "";
  authed = false;

  constructor(
    private deckId: string,
    private onMsg: (m: LiveMsg) => void,
    private onState: (s: "live" | "offline") => void = () => {},
    private token?: string,
  ) {
    try {
      this.bc = new BroadcastChannel(`slidekick-present-${deckId}`);
      this.bc.onmessage = (e) => this.onMsg(e.data as LiveMsg);
    } catch {
      /* no BroadcastChannel */
    }
    this.connect();
  }

  private connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    // A private deck opened without its key is unlocked by the auth message.
    const ws = new WebSocket(`${proto}://${location.host}${withKey(`/ws/decks/${encodeURIComponent(this.deckId)}`)}`);
    this.ws = ws;
    ws.onopen = () => {
      this.retry = 500;
      this.onState("live");
      if (this.token !== undefined) ws.send(JSON.stringify({ type: "auth", token: this.token }));
    };
    ws.onmessage = (m) => {
      const msg = JSON.parse(m.data as string) as LiveMsg;
      if (msg.type === "hello") this.conn = msg.conn;
      if (msg.type === "auth") this.authed = Boolean(msg.ok);
      if ((msg.type === "present" || msg.type === "pointer") && msg.from === this.conn) return; // own echo
      this.onMsg(msg);
    };
    ws.onclose = (e) => {
      this.authed = false;
      if (this.closed) return;
      if (e.code === 1008) {
        // Policy violation: the deck went private or its key changed.
        this.onState("offline");
        this.onMsg({ type: "denied" });
        return;
      }
      this.onState("offline");
      setTimeout(() => this.connect(), this.retry);
      this.retry = Math.min(this.retry * 2, 10000);
    };
  }

  /** Sends presenter position or pointer to local windows and remote viewers. */
  broadcast(msg: PresentMsg | PointerMsg) {
    this.bc?.postMessage(msg);
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg));
  }

  /** Same-machine only: the presenter view and its audience window agreeing on who plays the voice. */
  local(msg: LiveMsg) {
    this.bc?.postMessage(msg);
  }

  close() {
    this.closed = true;
    this.ws?.close();
    this.bc?.close();
  }
}
