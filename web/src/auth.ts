// Who is signed in. Browsers use a session cookie (HttpOnly, set by
// /api/login or single sign-on); writes also send the session's CSRF token in
// the X-CSRF-Token header. Agents use bearer tokens instead (see the Account page).

export interface User {
  id: string;
  username: string;
  name: string;
  email: string;
  role: "admin" | "editor" | "viewer";
  hasPassword: boolean;
  sso?: string[];
}

export interface Me {
  signedIn: boolean;
  kind?: string;
  user?: User;
  csrf?: string;
  setupNeeded: boolean;
  openMode: boolean;
  can: Record<string, boolean>;
  /** The public URL an admin set, for links people copy (signed-in callers only). */
  linkBase?: string;
}

try {
  localStorage.removeItem("slidekick-token"); // the old shared-token login is gone
} catch {
  /* storage blocked */
}

let current: Me | null = null;

export async function me(deckId?: string): Promise<Me> {
  const r = await fetch(`/api/me${deckId ? `?deck=${encodeURIComponent(deckId)}` : ""}`, { cache: "no-store" });
  current = (await r.json()) as Me;
  return current;
}

export const currentMe = () => current;

export const authHeaders = (csrf = current?.csrf ?? ""): Record<string, string> => (csrf ? { "X-CSRF-Token": csrf } : {});

/** Sends people who aren't signed in to the sign-in page (or first-run setup), then back here. */
export function goSignIn(): never {
  const next = location.pathname + location.search + location.hash;
  location.href = `${current?.setupNeeded && !current.openMode ? "/setup" : "/login"}?next=${encodeURIComponent(next)}`;
  throw new Error("redirecting to sign-in");
}

/** Makes sure someone is signed in (or the server is in open mode) and returns the CSRF token. */
export async function ensureAuth(_purpose: string, deckId?: string): Promise<string> {
  const m = await me(deckId);
  if (!m.signedIn) goSignIn();
  return m.csrf ?? "";
}

export async function signOut() {
  await fetch("/api/logout", { method: "POST", headers: authHeaders() });
  location.href = "/login";
}

/** Small account widget for page headers: name, Account, Settings (admins), Sign out. */
export function accountMenu(m: Me): HTMLElement {
  const wrap = document.createElement("nav");
  wrap.className = "acct";
  if (!m.user) {
    if (m.openMode) {
      const a = document.createElement("a");
      a.href = "/setup";
      a.textContent = "Set up accounts";
      a.className = "acct-warn";
      a.title = "Anyone who can reach this server can edit everything until an admin account exists.";
      wrap.append(a);
    }
    return wrap;
  }
  const who = document.createElement("span");
  who.className = "acct-who";
  who.textContent = `${m.user.name || m.user.username} · ${m.user.role}`;
  wrap.append(who);
  const link = (href: string, text: string) => {
    const a = document.createElement("a");
    a.href = href;
    a.textContent = text;
    wrap.append(a);
  };
  link("/account", "Account");
  if (m.can["manage-users"]) link("/settings", "Settings");
  const out = document.createElement("button");
  out.type = "button";
  out.textContent = "Sign out";
  out.onclick = () => signOut();
  wrap.append(out);
  return wrap;
}
