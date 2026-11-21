// Shared links: a viewer link carries a view key (?k=...) for a private deck,
// a presenter link a presenter key (?p=...). Every request the page makes for
// the deck passes them along. Signed-in people and token holders don't need them.

export const viewKey = (): string => new URLSearchParams(location.search).get("k") ?? "";
export const presentKey = (): string => new URLSearchParams(location.search).get("p") ?? "";

/** Adds the view and presenter keys to a path (before any #fragment). */
export function withKey(path: string, key = viewKey(), pkey = presentKey()): string {
  if (!key && !pkey) return path;
  const [p, hash] = path.split("#", 2);
  let out = p;
  if (key) out += `${out.includes("?") ? "&" : "?"}k=${encodeURIComponent(key)}`;
  if (pkey) out += `${out.includes("?") ? "&" : "?"}p=${encodeURIComponent(pkey)}`;
  return hash === undefined ? out : `${out}#${hash}`;
}

/** Fetches a deck resource as a viewer: with the key (signed-in users' cookies come along). */
export function viewFetch(path: string): Promise<Response> {
  return fetch(withKey(path), { cache: "no-store" });
}
