// Access token for editing and presenting: the server's SLIDEKICK_TOKEN, or a
// deck's own edit token (which only works for that deck). Viewing public decks
// needs neither. The token is kept in this browser only.

const KEY = "slidekick-token";

export function storedToken(): string {
  try {
    return localStorage.getItem(KEY) ?? "";
  } catch {
    return "";
  }
}

function store(token: string) {
  try {
    localStorage.setItem(KEY, token);
  } catch {
    /* private mode: token lives for this page only */
  }
}

export function forgetToken() {
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
}

export const authHeaders = (token: string): Record<string, string> => (token ? { Authorization: `Bearer ${token}` } : {});

async function check(token: string, deckId?: string): Promise<boolean> {
  const r = await fetch("/api/auth", { headers: authHeaders(token), cache: "no-store" });
  if (!r.ok) return false;
  const who = (await r.json().catch(() => ({}))) as { scope?: string; deck?: string };
  return who.scope !== "deck" || (deckId !== undefined && who.deck === deckId);
}

/** Returns a working token ("" when the server needs none), asking the user if necessary. */
export async function ensureAuth(purpose: string, deckId?: string): Promise<string> {
  let token = storedToken();
  if (await check(token, deckId)) return token;
  for (;;) {
    token = await prompt(purpose);
    if (await check(token, deckId)) {
      store(token);
      return token;
    }
  }
}

function prompt(purpose: string): Promise<string> {
  return new Promise((resolve) => {
    const wrap = document.createElement("div");
    wrap.className = "auth-overlay";
    wrap.innerHTML = `<form class="auth-box">
      <h2>Access token needed</h2>
      <p></p>
      <input type="password" autocomplete="current-password" placeholder="Server or deck token" aria-label="Access token" required />
      <button type="submit">Unlock</button>
      <small>Stored in this browser only. Ask whoever runs this server for the token.</small>
    </form>`;
    wrap.querySelector("p")!.textContent = `To ${purpose}, enter the server's access token or this deck's edit token.`;
    const form = wrap.querySelector("form")!;
    const input = wrap.querySelector("input")!;
    if (document.querySelector(".auth-overlay")) wrap.querySelector("h2")!.textContent = "That token didn't work";
    document.querySelectorAll(".auth-overlay").forEach((n) => n.remove());
    form.onsubmit = (e) => {
      e.preventDefault();
      wrap.remove();
      resolve(input.value.trim());
    };
    document.body.append(wrap);
    input.focus();
  });
}
