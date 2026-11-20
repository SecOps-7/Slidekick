// REST calls for the editor. Every write sends the version it was based on.
import type { Catalog, Deck, LintIssue, PatchOp } from "../../runtime/types";
import { authHeaders } from "../auth";

export interface DeckState {
  version: number;
  deck: Deck;
  lint: LintIssue[];
}

export interface Comment {
  id: string;
  slideId?: string;
  elementId?: string;
  text: string;
  author: string;
  status: "open" | "resolved";
  reply?: string;
  createdAt: string;
}

export interface Access {
  private: boolean;
  viewKey?: string;
  hasEditToken: boolean;
  shareUrl: string;
}

export interface WriteResult {
  version: number;
  changed?: string[];
  lint: LintIssue[];
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public problems: string[] = [],
  ) {
    super(message);
  }
}

export class Api {
  constructor(
    readonly deckId: string,
    readonly token: string,
  ) {}

  private async req<T>(method: string, path: string, body?: unknown, headers: Record<string, string> = {}): Promise<T> {
    const isRaw = body instanceof Blob;
    const r = await fetch(path, {
      method,
      cache: "no-store",
      headers: { ...authHeaders(this.token), ...(body && !isRaw ? { "Content-Type": "application/json" } : {}), ...headers },
      body: body === undefined ? undefined : isRaw ? body : JSON.stringify(body),
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) throw new ApiError(r.status, data.error ?? `${r.status} ${r.statusText}`, data.problems ?? []);
    return data as T;
  }

  private get base() {
    return `/api/decks/${encodeURIComponent(this.deckId)}`;
  }

  catalog = () => this.req<Catalog & { icons: string[]; elements: Record<string, string> }>("GET", "/api/catalog");
  load = () => this.req<DeckState>("GET", this.base);
  patch = (baseVersion: number, ops: PatchOp[]) => this.req<WriteResult>("POST", `${this.base}/patch`, { baseVersion, ops });
  restore = (baseVersion: number, version: number) => this.req<WriteResult>("POST", `${this.base}/restore`, { baseVersion, version });
  comments = async (all = false) => (await this.req<{ comments: Comment[] }>("GET", `${this.base}/comments${all ? "?all=1" : ""}`)).comments;
  addComment = (slideId: string, elementId: string, text: string) => this.req<Comment>("POST", `${this.base}/comments`, { slideId, elementId, text });
  resolve = (id: string, reply = "") => this.req<{ ok: boolean }>("POST", `${this.base}/comments/${encodeURIComponent(id)}/resolve`, { reply });
  exportFile = (format: "pdf" | "png") => this.req<{ url: string; name: string; bytes: number }>("POST", `${this.base}/export?format=${format}`);
  whoami = () => this.req<{ scope: "admin" | "deck"; required: boolean }>("GET", "/api/auth");
  access = () => this.req<Access>("GET", `${this.base}/access`);
  setAccess = (priv: boolean, rotate = false) => this.req<Access>("POST", `${this.base}/access`, { private: priv, rotate });
  issueEditToken = () => this.req<{ token: string }>("POST", `${this.base}/edit-token`);
  revokeEditToken = () => this.req<{ ok: boolean }>("DELETE", `${this.base}/edit-token`);
  upload = (file: File) =>
    this.req<{ url: string }>("POST", `/api/files?name=${encodeURIComponent(file.name)}`, file, { "Content-Type": file.type || "application/octet-stream" });
}
