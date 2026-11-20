// Private decks: their links carry a view key (?k=...), which every request
// for the deck must pass along. Token holders don't need it.
import { authHeaders, storedToken } from "./auth";

export const viewKey = (): string => new URLSearchParams(location.search).get("k") ?? "";

/** Adds the view key to a path (before any #fragment). */
export function withKey(path: string, key = viewKey()): string {
  if (!key) return path;
  const [p, hash] = path.split("#", 2);
  const out = `${p}${p.includes("?") ? "&" : "?"}k=${encodeURIComponent(key)}`;
  return hash === undefined ? out : `${out}#${hash}`;
}

/** Fetches a deck resource as a viewer: with the key, and with the stored token if any. */
export function viewFetch(path: string): Promise<Response> {
  return fetch(withKey(path), { cache: "no-store", headers: authHeaders(storedToken()) });
}
