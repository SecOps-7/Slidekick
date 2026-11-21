// Sign-in, first-run setup, the account page and admin settings.
import { confirmModal, promptModal } from "../ui/modal";
import { render, type ComponentChildren } from "preact";
import { useEffect, useState } from "preact/hooks";
import { authHeaders, me, signOut, type Me, type User } from "../auth";
import { wordmarkSVG } from "../brand/brand";
import "./account.css";

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(path, {
    method,
    cache: "no-store",
    headers: { ...authHeaders(), ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error((data as { error?: string }).error ?? `${r.status} ${r.statusText}`);
  return data as T;
}

const nextURL = () => {
  const n = new URLSearchParams(location.search).get("next") ?? "/";
  return n.startsWith("/") && !n.startsWith("//") ? n : "/";
};

function Shell({ title, children, wide }: { title: string; children: ComponentChildren; wide?: boolean }) {
  return (
    <main class={`acc ${wide ? "acc-wide" : ""}`}>
      <a class="acc-brand" href="/" aria-label="Slidekick home" dangerouslySetInnerHTML={{ __html: wordmarkSVG() }} />
      <h1>{title}</h1>
      {children}
    </main>
  );
}

export function Err({ msg }: { msg: string }) {
  return msg ? (
    <p class="acc-err" role="alert">
      {msg}
    </p>
  ) : null;
}

// ---- sign in ----

function Login() {
  const [providers, setProviders] = useState<{ id: string; name: string }[]>([]);
  const [err, setErr] = useState(new URLSearchParams(location.search).get("error") ?? "");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    me().then((m) => {
      if (m.signedIn && !m.openMode) location.replace(nextURL());
      else if (m.setupNeeded && !m.openMode) location.replace(`/setup?next=${encodeURIComponent(nextURL())}`);
    });
    fetch("/api/auth/providers")
      .then((r) => r.json())
      .then((d) => setProviders(d.providers ?? []), () => {});
  }, []);
  const submit = async (e: Event) => {
    e.preventDefault();
    const f = new FormData(e.currentTarget as HTMLFormElement);
    setBusy(true);
    setErr("");
    try {
      await api("POST", "/api/login", { username: f.get("username"), password: f.get("password") });
      location.replace(nextURL());
    } catch (x) {
      setErr((x as Error).message);
      setBusy(false);
    }
  };
  return (
    <Shell title="Sign in">
      <Err msg={err} />
      <form class="acc-form" onSubmit={submit}>
        <label>
          Username
          <input name="username" autocomplete="username" required autofocus />
        </label>
        <label>
          Password
          <input name="password" type="password" autocomplete="current-password" required />
        </label>
        <button class="acc-primary" disabled={busy}>
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
      {providers.length > 0 && (
        <div class="acc-sso">
          <p class="acc-or">or</p>
          {providers.map((p) => (
            <a class="acc-sso-btn" key={p.id} href={`/auth/sso/${encodeURIComponent(p.id)}/login?next=${encodeURIComponent(nextURL())}`}>
              Continue with {p.name}
            </a>
          ))}
        </div>
      )}
    </Shell>
  );
}

// ---- first admin ----

function Setup() {
  const [info, setInfo] = useState<{ needed: boolean; acceptsServerToken: boolean } | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    fetch("/api/setup")
      .then((r) => r.json())
      .then(setInfo);
  }, []);
  if (!info) return null;
  if (!info.needed)
    return (
      <Shell title="Already set up">
        <p>
          An admin account exists. <a href="/login">Sign in</a>.
        </p>
      </Shell>
    );
  const submit = async (e: Event) => {
    e.preventDefault();
    const f = new FormData(e.currentTarget as HTMLFormElement);
    if (f.get("password") !== f.get("password2")) return setErr("The passwords don't match.");
    try {
      await api("POST", "/api/setup", { code: f.get("code"), username: f.get("username"), password: f.get("password") });
      location.replace(nextURL() === "/" ? "/settings" : nextURL());
    } catch (x) {
      setErr((x as Error).message);
    }
  };
  return (
    <Shell title="Create the admin account">
      <p class="acc-lead">This is the first account on this Slidekick. It can add other people and connect sign-in and AI providers.</p>
      <Err msg={err} />
      <form class="acc-form" onSubmit={submit}>
        <label>
          Setup code
          <input name="code" required autocomplete="off" />
          <small>
            Printed in the server log at startup (<code>docker logs slidekick</code>)
            {info.acceptsServerToken ? ", or use the server token (SLIDEKICK_TOKEN)" : ""}.
          </small>
        </label>
        <label>
          Username
          <input name="username" autocomplete="username" required />
        </label>
        <label>
          Password
          <input name="password" type="password" autocomplete="new-password" minLength={10} required />
          <small>At least 10 characters.</small>
        </label>
        <label>
          Repeat password
          <input name="password2" type="password" autocomplete="new-password" required />
        </label>
        <button class="acc-primary">Create admin and sign in</button>
      </form>
    </Shell>
  );
}

// ---- account: password and personal API tokens ----

interface Token {
  id: string;
  name: string;
  createdAt: string;
  lastUsed?: string;
}

function Account({ m }: { m: Me }) {
  const u = m.user!;
  const [tokens, setTokens] = useState<Token[]>([]);
  const [fresh, setFresh] = useState("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const load = () => api<{ tokens: Token[] }>("GET", "/api/me/tokens").then((d) => setTokens(d.tokens));
  useEffect(() => {
    load();
  }, []);
  const changePw = async (e: Event) => {
    e.preventDefault();
    const form = e.currentTarget as HTMLFormElement;
    const f = new FormData(form);
    setErr("");
    try {
      await api("POST", "/api/me/password", { current: f.get("current") ?? "", new: f.get("new") });
      form.reset();
      setMsg("Password changed. Your other sessions were signed out.");
    } catch (x) {
      setErr((x as Error).message);
    }
  };
  const newToken = async (e: Event) => {
    e.preventDefault();
    const form = e.currentTarget as HTMLFormElement;
    try {
      const d = await api<{ token: string }>("POST", "/api/me/tokens", { name: new FormData(form).get("name") });
      setFresh(d.token);
      form.reset();
      load();
    } catch (x) {
      setErr((x as Error).message);
    }
  };
  return (
    <Shell title="Your account" wide>
      <p class="acc-lead">
        Signed in as <b>{u.username}</b> ({u.role}).{" "}
        <button class="acc-link" onClick={() => signOut()}>
          Sign out
        </button>
      </p>
      <Err msg={err} />
      {msg && <p class="acc-ok">{msg}</p>}
      <section>
        <h2>Agent tokens</h2>
        <p>A personal token lets an agent use Slidekick over MCP as you, with your role: it can do what you can, nothing more. Decks it creates are yours.</p>
        {fresh && (
          <div class="acc-fresh">
            <p>Copy this token now; it won't be shown again.</p>
            <code>{fresh}</code>
            <pre>{`claude mcp add --transport http slidekick ${location.origin}/mcp \\\n  --header "Authorization: Bearer ${fresh}"`}</pre>
          </div>
        )}
        <table class="acc-table">
          <tbody>
            {tokens.map((t) => (
              <tr key={t.id}>
                <td>{t.name}</td>
                <td class="acc-muted">created {new Date(t.createdAt).toLocaleDateString()}</td>
                <td class="acc-muted">{t.lastUsed ? `used ${new Date(t.lastUsed).toLocaleString()}` : "never used"}</td>
                <td>
                  <button class="acc-link danger" onClick={async () => (await confirmModal({ title: `Revoke "${t.name}"?`, message: "Agents using this token lose access at once.", confirm: "Revoke token", danger: true })) && api("DELETE", `/api/me/tokens/${t.id}`).then(load)}>
                    Revoke
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <form class="acc-inline" onSubmit={newToken}>
          <input name="name" placeholder="Token name, e.g. Claude Code on my laptop" required />
          <button>Create token</button>
        </form>
      </section>
      <section>
        <h2>Password</h2>
        <form class="acc-form" onSubmit={changePw}>
          {u.hasPassword && (
            <label>
              Current password
              <input name="current" type="password" autocomplete="current-password" required />
            </label>
          )}
          <label>
            New password
            <input name="new" type="password" autocomplete="new-password" minLength={10} required />
          </label>
          <button>Change password</button>
        </form>
      </section>
    </Shell>
  );
}

// ---- admin settings ----

const baseTabs = [
  { id: "users", label: "Users" },
  { id: "sso", label: "Sign-in providers" },
  { id: "general", label: "General" },
];

let extraTabs: { id: string; label: string; render: () => ComponentChildren }[] = [];
/** Lets other features (AI providers) add a settings tab. */
export function addSettingsTab(t: { id: string; label: string; render: () => ComponentChildren }) {
  extraTabs = [...extraTabs.filter((x) => x.id !== t.id), t];
}

function Settings({ m }: { m: Me }) {
  const [tab, setTab] = useState<string>(location.hash.slice(1) || "users");
  useEffect(() => {
    history.replaceState(null, "", `#${tab}`);
  }, [tab]);
  const tabs = [...baseTabs, ...extraTabs.map(({ id, label }) => ({ id, label }))];
  const extra = extraTabs.find((t) => t.id === tab);
  return (
    <Shell title="Settings" wide>
      <nav class="acc-tabs" role="tablist">
        {tabs.map((t) => (
          <button key={t.id} role="tab" aria-selected={tab === t.id} class={tab === t.id ? "on" : ""} onClick={() => setTab(t.id)}>
            {t.label}
          </button>
        ))}
      </nav>
      {tab === "users" && <Users self={m.user?.id ?? ""} />}
      {tab === "sso" && <SSO />}
      {tab === "general" && <General />}
      {extra && extra.render()}
    </Shell>
  );
}

export const ROLES = ["admin", "editor", "viewer"] as const;
const roleHelp: Record<string, string> = {
  admin: "everything, including users and settings",
  editor: "creates decks; edits, shares and exports their own; uses AI",
  viewer: "views, presents and comments",
};

type AdminUser = User & { disabled?: boolean; lastLogin?: string };

function Users({ self }: { self: string }) {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [err, setErr] = useState("");
  const load = () => api<{ users: AdminUser[] }>("GET", "/api/admin/users").then((d) => setUsers(d.users));
  useEffect(() => {
    load();
  }, []);
  const run = async (f: () => Promise<unknown>) => {
    setErr("");
    try {
      await f();
    } catch (x) {
      setErr((x as Error).message);
    }
    await load();
  };
  const add = (e: Event) => {
    e.preventDefault();
    const form = e.currentTarget as HTMLFormElement;
    const f = new FormData(form);
    run(async () => {
      await api("POST", "/api/admin/users", {
        username: f.get("username"),
        name: f.get("name"),
        email: f.get("email"),
        role: f.get("role"),
        password: f.get("password"),
      });
      form.reset();
    });
  };
  return (
    <section>
      <Err msg={err} />
      <table class="acc-table">
        <thead>
          <tr>
            <th>User</th>
            <th>Role</th>
            <th>Sign-in</th>
            <th>Last sign-in</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id} class={u.disabled ? "off" : ""}>
              <td>
                <b>{u.username}</b>
                {u.disabled && <span class="acc-tag">disabled</span>}
                <div class="acc-muted">{[u.name, u.email].filter(Boolean).join(" · ")}</div>
              </td>
              <td>
                <select value={u.role} aria-label={`Role of ${u.username}`} onChange={(e) => run(() => api("PATCH", `/api/admin/users/${u.id}`, { role: e.currentTarget.value }))}>
                  {ROLES.map((r) => (
                    <option key={r}>{r}</option>
                  ))}
                </select>
              </td>
              <td class="acc-muted">{[u.hasPassword ? "password" : "", ...(u.sso ?? [])].filter(Boolean).join(", ") || "none"}</td>
              <td class="acc-muted">{u.lastLogin ? new Date(u.lastLogin).toLocaleString() : "never"}</td>
              <td class="acc-actions">
                <button
                  class="acc-link"
                  onClick={async () => {
                    const pw = await promptModal({
                      title: `Set a password for ${u.username}`,
                      message: "Their other sessions are signed out. Tell them the new password in person or over a private channel.",
                      label: "New password",
                      type: "password",
                      minLength: 10,
                      confirm: "Set password",
                    });
                    if (pw) run(() => api("POST", `/api/admin/users/${u.id}/password`, { password: pw }));
                  }}
                >
                  Set password
                </button>
                {u.id !== self && (
                  <>
                    <button class="acc-link" onClick={() => run(() => api("PATCH", `/api/admin/users/${u.id}`, { disabled: !u.disabled }))}>
                      {u.disabled ? "Enable" : "Disable"}
                    </button>
                    <button
                      class="acc-link danger"
                      onClick={async () =>
                        (await confirmModal({ title: `Delete ${u.username}?`, message: "Their account and agent tokens are removed. Their decks move to you.", confirm: "Delete user", danger: true })) &&
                        run(() => api("DELETE", `/api/admin/users/${u.id}?transferTo=${encodeURIComponent(self)}`))
                      }
                    >
                      Delete
                    </button>
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <h2>Add a user</h2>
      <form class="acc-grid" onSubmit={add}>
        <input name="username" placeholder="Username" aria-label="Username" required />
        <input name="name" placeholder="Name (optional)" aria-label="Name" />
        <input name="email" type="email" placeholder="Email (optional; links single sign-on)" aria-label="Email" />
        <select name="role" aria-label="Role" defaultValue="editor">
          {ROLES.map((r) => (
            <option key={r} value={r}>
              {r}: {roleHelp[r]}
            </option>
          ))}
        </select>
        <input name="password" type="password" placeholder="Password (empty: single sign-on only)" aria-label="Password" minLength={10} autocomplete="new-password" />
        <button class="acc-primary">Add user</button>
      </form>
    </section>
  );
}

interface Provider {
  id: string;
  name: string;
  issuer: string;
  clientId: string;
  hasSecret: boolean;
  scopes: string;
  groupsClaim: string;
  trustEmail: boolean;
  autoCreate: boolean;
  defaultRole: string;
  roleRules: { claim: string; value: string; role: string }[];
  enabled: boolean;
  redirectUrl: string;
}

const blankProvider: Provider = {
  id: "",
  name: "",
  issuer: "",
  clientId: "",
  hasSecret: false,
  scopes: "",
  groupsClaim: "groups",
  trustEmail: false,
  autoCreate: false,
  defaultRole: "viewer",
  roleRules: [],
  enabled: true,
  redirectUrl: "",
};

const presets: Record<string, Partial<Provider>> = {
  "Microsoft Entra": { id: "entra", name: "Microsoft", issuer: "https://login.microsoftonline.com/<tenant-id>/v2.0", groupsClaim: "groups" },
  Google: { id: "google", name: "Google", issuer: "https://accounts.google.com" },
  Authentik: { id: "authentik", name: "Authentik", issuer: "https://authentik.example.lan/application/o/slidekick/" },
  Keycloak: { id: "keycloak", name: "Keycloak", issuer: "https://keycloak.example.lan/realms/<realm>" },
};

function SSO() {
  const [list, setList] = useState<Provider[]>([]);
  const [base, setBase] = useState("");
  const [edit, setEdit] = useState<Provider | null>(null);
  const [isNew, setIsNew] = useState(false);
  const [secret, setSecret] = useState<string | undefined>(undefined);
  const [err, setErr] = useState("");
  const [check, setCheck] = useState("");
  const load = () =>
    api<{ providers: Provider[]; redirectBase: string }>("GET", "/api/admin/sso").then((d) => {
      setList(d.providers);
      setBase(d.redirectBase);
    });
  useEffect(() => {
    load();
  }, []);
  const save = async (e: Event) => {
    e.preventDefault();
    if (!edit) return;
    setErr("");
    try {
      await api("PUT", `/api/admin/sso/${encodeURIComponent(edit.id)}`, { ...edit, clientSecret: secret });
      setEdit(null);
      setSecret(undefined);
      load();
    } catch (x) {
      setErr((x as Error).message);
    }
  };
  const set = (k: keyof Provider, v: unknown) => setEdit({ ...edit!, [k]: v });
  const setRule = (i: number, k: string, v: string) => set("roleRules", edit!.roleRules.map((x, j) => (j === i ? { ...x, [k]: v } : x)));
  if (edit)
    return (
      <section>
        <h2>{isNew ? "Add a sign-in provider" : `Edit ${edit.name}`}</h2>
        <Err msg={err} />
        <form class="acc-form" onSubmit={save}>
          {isNew && (
            <p class="acc-presets">
              Start from:{" "}
              {Object.entries(presets).map(([label, p]) => (
                <button type="button" class="acc-chip" key={label} onClick={() => setEdit({ ...edit, ...p })}>
                  {label}
                </button>
              ))}
            </p>
          )}
          <label>
            ID <small>(short, used in the redirect URL)</small>
            <input value={edit.id} required pattern="[a-z0-9][a-z0-9\-]{0,31}" disabled={!isNew} onInput={(e) => set("id", e.currentTarget.value)} />
          </label>
          <p class="acc-muted">
            Redirect URL to register at the provider: <code>{base + (edit.id || "<id>") + "/callback"}</code>
          </p>
          <label>
            Button label
            <input value={edit.name} required onInput={(e) => set("name", e.currentTarget.value)} />
          </label>
          <label>
            Issuer URL
            <input value={edit.issuer} required placeholder="https://..." onInput={(e) => set("issuer", e.currentTarget.value)} />
            <small>With or without a trailing slash; Slidekick matches what the provider publishes.</small>
          </label>
          <label>
            Client ID
            <input value={edit.clientId} required onInput={(e) => set("clientId", e.currentTarget.value)} />
          </label>
          <label>
            Client secret
            <input type="password" autocomplete="off" placeholder={edit.hasSecret ? "Stored (encrypted). Type to replace." : ""} onInput={(e) => setSecret(e.currentTarget.value)} />
          </label>
          <label>
            Extra scopes <small>(openid email profile are always requested)</small>
            <input value={edit.scopes} onInput={(e) => set("scopes", e.currentTarget.value)} />
          </label>
          <label>
            Groups claim
            <input value={edit.groupsClaim} onInput={(e) => set("groupsClaim", e.currentTarget.value)} />
          </label>
          <label class="acc-check">
            <input type="checkbox" checked={edit.autoCreate} onChange={(e) => set("autoCreate", e.currentTarget.checked)} /> Create accounts for new people automatically
          </label>
          <label>
            Role for new accounts
            <select value={edit.defaultRole} onChange={(e) => set("defaultRole", e.currentTarget.value)}>
              {ROLES.map((r) => (
                <option key={r}>{r}</option>
              ))}
            </select>
          </label>
          <label class="acc-check">
            <input type="checkbox" checked={edit.trustEmail} onChange={(e) => set("trustEmail", e.currentTarget.checked)} /> Trust this provider's email addresses even without
            email_verified (e.g. Microsoft Entra work accounts). Only for providers you control: a trusted email can sign in to an existing account with that email.
          </label>
          <fieldset>
            <legend>Role rules (first match wins; a match also updates the role at each sign-in)</legend>
            {edit.roleRules.map((r, i) => (
              <div class="acc-rule" key={i}>
                <select value={r.claim} aria-label="Claim" onChange={(e) => setRule(i, "claim", e.currentTarget.value)}>
                  <option value="group">group is</option>
                  <option value="email">email is</option>
                  <option value="domain">email domain is</option>
                </select>
                <input value={r.value} aria-label="Value" required onInput={(e) => setRule(i, "value", e.currentTarget.value)} />
                <select value={r.role} aria-label="Role" onChange={(e) => setRule(i, "role", e.currentTarget.value)}>
                  {ROLES.map((x) => (
                    <option key={x}>{x}</option>
                  ))}
                </select>
                <button type="button" class="acc-link danger" onClick={() => set("roleRules", edit.roleRules.filter((_, j) => j !== i))}>
                  Remove
                </button>
              </div>
            ))}
            <button type="button" class="acc-link" onClick={() => set("roleRules", [...edit.roleRules, { claim: "group", value: "", role: "editor" }])}>
              + Add rule
            </button>
          </fieldset>
          <label class="acc-check">
            <input type="checkbox" checked={edit.enabled} onChange={(e) => set("enabled", e.currentTarget.checked)} /> Show on the sign-in page
          </label>
          <div class="acc-inline">
            <button class="acc-primary">Save</button>
            <button type="button" onClick={() => setEdit(null)}>
              Cancel
            </button>
          </div>
        </form>
      </section>
    );
  return (
    <section>
      <Err msg={err} />
      {check && <p class="acc-ok">{check}</p>}
      {list.length === 0 && <p class="acc-muted">No single sign-on providers yet. People sign in with a username and password.</p>}
      <table class="acc-table">
        <tbody>
          {list.map((p) => (
            <tr key={p.id} class={p.enabled ? "" : "off"}>
              <td>
                <b>{p.name}</b>
                <div class="acc-muted">{p.issuer}</div>
              </td>
              <td class="acc-muted">
                {p.autoCreate ? `new people join as ${p.defaultRole}` : "existing accounts only"}
                {p.roleRules.length ? ` · ${p.roleRules.length} role rule${p.roleRules.length > 1 ? "s" : ""}` : ""}
              </td>
              <td class="acc-actions">
                <button
                  class="acc-link"
                  onClick={async () => {
                    setCheck("");
                    setErr("");
                    const r = await api<{ ok: boolean; issuer?: string; error?: string }>("POST", `/api/admin/sso/${p.id}/check`);
                    if (r.ok) setCheck(`${p.name}: discovery works (issuer ${r.issuer}).`);
                    else setErr(`${p.name}: ${r.error}`);
                  }}
                >
                  Test
                </button>
                <button class="acc-link" onClick={() => (setEdit(p), setIsNew(false), setSecret(undefined))}>
                  Edit
                </button>
                <button class="acc-link danger" onClick={async () =>
                    (await confirmModal({ title: `Remove ${p.name}?`, message: "People can no longer sign in with it. Their accounts stay.", confirm: "Remove provider", danger: true })) &&
                    api("DELETE", `/api/admin/sso/${p.id}`).then(load)
                  }>
                  Remove
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <button class="acc-primary" onClick={() => (setEdit({ ...blankProvider }), setIsNew(true), setSecret(undefined))}>
        Add provider
      </button>
    </section>
  );
}

function General() {
  const [s, setS] = useState<{ editorsEditAll: boolean } | null>(null);
  useEffect(() => {
    api<{ editorsEditAll: boolean }>("GET", "/api/admin/settings").then(setS);
  }, []);
  if (!s) return null;
  return (
    <section>
      <label class="acc-check">
        <input type="checkbox" checked={s.editorsEditAll} onChange={async (e) => setS(await api("PUT", "/api/admin/settings", { ...s, editorsEditAll: e.currentTarget.checked }))} /> Editors
        can edit every deck, not only their own
      </label>
      <p class="acc-muted">Everyone signed in can always view, present and comment on every deck. Changing a deck needs its owner, an admin, or this setting.</p>
    </section>
  );
}

export async function mountAccountPage(host: HTMLElement, page: "login" | "setup" | "account" | "settings" | "about") {
  document.body.classList.add("acc-page");
  document.title = { login: "Sign in", setup: "Set up", account: "Account", settings: "Settings", about: "About" }[page] + " · Slidekick";
  if (page === "login") return render(<Login />, host);
  if (page === "setup") return render(<Setup />, host);
  const m = await me();
  if (page === "about") {
    // Anyone signed in may see it, and everyone while no accounts exist yet.
    if (!m.user && !m.openMode) {
      location.href = `/login?next=${encodeURIComponent(location.pathname)}`;
      return;
    }
    const { About } = await import("./about");
    return render(<About />, host);
  }
  if (!m.user) {
    location.href = `/login?next=${encodeURIComponent(location.pathname + location.hash)}`;
    return;
  }
  if (page === "settings" && !m.can["manage-users"]) {
    render(
      <Shell title="Settings">
        <p>Only admins can change settings.</p>
      </Shell>,
      host,
    );
    return;
  }
  if (page === "settings") await import("./ai-settings");
  if (page === "settings") await import("./cast-settings");
  if (page === "settings") await import("./tts-settings");
  render(page === "account" ? <Account m={m} /> : <Settings m={m} />, host);
}
