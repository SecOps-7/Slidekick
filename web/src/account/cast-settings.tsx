// Settings → Cast devices (admins). The server finds Chromecasts on the LAN
// by itself and casts decks to them; admins save (pin, rename) or hide what
// it found, and can still add a device by IP address.
import { confirmModal, promptModal } from "../ui/modal";
import { useEffect, useRef, useState } from "preact/hooks";
import { addSettingsTab, api, Err } from "./pages";

interface Device {
  id: string;
  name: string;
  host: string;
  port: number;
  model?: string;
  lastSeen?: string;
}
interface Found {
  id: string;
  name: string;
  model: string;
  host: string;
  port: number;
  lastSeen: string;
  added: boolean;
  savedName?: string;
  hidden: boolean;
}
interface Discovery {
  enabled: boolean;
  running: boolean;
  done: number;
  total: number;
  lastRun?: string;
  tookSeconds?: number;
  found: number;
  subnets: string[];
  notes?: string[];
  error?: string;
}
interface AdminList {
  devices: Device[];
  found: Found[];
  discovery: Discovery;
}

/** "just now", "5 minutes ago", "3 hours ago", "2 days ago". */
export function ago(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "not seen yet";
  const s = Math.max(0, (now - Date.parse(iso)) / 1000);
  if (s < 60) return "just now";
  const unit = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"} ago`;
  if (s < 3600) return unit(Math.floor(s / 60), "minute");
  if (s < 86400) return unit(Math.floor(s / 3600), "hour");
  return unit(Math.floor(s / 86400), "day");
}

const addr = (d: { host: string; port: number }) => `${d.host}${d.port !== 8009 ? `:${d.port}` : ""}`;

function CastDevices() {
  const [list, setList] = useState<AdminList | null>(null);
  const [name, setName] = useState("");
  const [host, setHost] = useState("");
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  const [scanning, setScanning] = useState(false);
  const poll = useRef<number | undefined>(undefined);
  const load = () =>
    api<AdminList>("GET", "/api/admin/cast/devices").then(
      (d) => setList(d),
      (x) => setErr((x as Error).message),
    );
  useEffect(() => {
    load();
    return () => clearInterval(poll.current);
  }, []);

  const fail = (x: unknown) => setErr((x as Error).message);
  const add = async (e: Event) => {
    e.preventDefault();
    setErr("");
    setNote("");
    try {
      const d = await api<Device>("POST", "/api/admin/cast/devices", { name, host });
      setNote(`Added ${d.name}.`);
      setName("");
      setHost("");
      await load();
    } catch (x) {
      fail(x);
    }
  };
  const scan = async () => {
    setErr("");
    setNote("");
    setScanning(true);
    // The scan answers when it is done; meanwhile the list shows progress.
    poll.current = window.setInterval(load, 700);
    try {
      const r = await api<{ discovery: Discovery }>("POST", "/api/admin/cast/discover");
      const d = r.discovery;
      const n = d.found;
      const what = n === 0 ? "no cast devices" : n === 1 ? "1 cast device" : `${n} cast devices`;
      const took = `${(d.tookSeconds ?? 0).toFixed(1)} s`;
      setNote(d.error ? `The scan stopped early: ${d.error}` : d.total ? `Found ${what} (checked ${d.total} addresses in ${took}).` : `Found ${what} in ${took}.`);
    } catch (x) {
      fail(x);
    } finally {
      clearInterval(poll.current);
      setScanning(false);
      await load();
    }
  };
  const save = async (f: Found) => {
    const n = await promptModal({ title: `Save ${f.name}`, message: "Saved devices stay in cast menus even while the TV is off.", label: "Name shown to people", value: f.name, confirm: "Save device", validate: (v) => (v.length > 80 ? "Use up to 80 characters." : null) });
    if (n === null) return;
    setErr("");
    api("POST", `/api/admin/cast/found/${encodeURIComponent(f.id)}/save`, { name: n }).then(() => {
      setNote(`Saved ${n}.`);
      return load();
    }, fail);
  };
  const hide = (f: Found, on: boolean) => api(on ? "POST" : "DELETE", `/api/admin/cast/found/${encodeURIComponent(f.id)}/hide`).then(load, fail);

  const disc = list?.discovery;
  const saved = list?.devices ?? [];
  const found = list?.found ?? [];
  return (
    <section>
      <p class="acc-muted">
        Editors can show a deck on a TV: the server tells the Chromecast to open the deck, and it follows along as someone presents. Casting only works on your own network.
      </p>
      <p class="acc-muted">
        Slidekick finds cast devices by itself, shortly after it starts, every 10 minutes and when you press Scan now. It asks with mDNS and also knocks on port 8009 at every
        address in {disc?.subnets.length ? disc.subnets.join(", ") : "the server's own network"}, which works from Docker's default bridge network. Host networking makes mDNS
        work too, but isn't required.
      </p>
      <Err msg={err} />
      {note && (
        <p class="acc-ok" role="status">
          {note}
        </p>
      )}

      <h2>Found on your network</h2>
      {disc && !disc.enabled && (
        <p class="acc-muted">Automatic discovery is off (SLIDEKICK_CAST_DISCOVERY=off): found devices only reach cast menus once you save them.</p>
      )}
      {disc && !disc.running && !disc.subnets.length && (
        <p class="acc-muted">No network to scan was found, so only mDNS is used. Set SLIDEKICK_CAST_SUBNETS (like 192.168.1.0/24) or SLIDEKICK_BASE_URL to the server's LAN address.</p>
      )}
      {disc?.notes?.map((n) => (
        <p class="acc-muted" key={n}>
          {n}
        </p>
      ))}
      <div class="acc-inline" aria-live="polite">
        <button type="button" onClick={scan} disabled={scanning}>
          {scanning ? "Scanning…" : "Scan now"}
        </button>
        <span class="acc-muted">
          {scanning || disc?.running
            ? disc?.total
              ? `Checked ${disc.done} of ${disc.total} addresses…`
              : "Looking…"
            : disc?.lastRun
              ? `Last looked ${ago(disc.lastRun)}.`
              : "Not looked yet."}
        </span>
      </div>
      {found.length === 0 ? (
        <p class="acc-muted">Nothing found yet. TVs that are off or asleep may not answer; you can still add a device by IP address below.</p>
      ) : (
        <table class="acc-table">
          <tbody>
            {found.map((f) => (
              <tr key={f.id}>
                <td>
                  <b>{f.name}</b>
                  {f.hidden && <span class="acc-tag">Hidden</span>}
                  <div class="acc-muted">
                    {f.model ? `${f.model} · ` : ""}
                    {addr(f)} · seen {ago(f.lastSeen)}
                  </div>
                </td>
                <td class="acc-actions">
                  {f.added ? (
                    <span class="acc-muted">Saved{f.savedName && f.savedName !== f.name ? ` as ${f.savedName}` : ""}</span>
                  ) : (
                    <>
                      <button class="acc-link" onClick={() => save(f)}>
                        Save
                      </button>
                      <button class="acc-link" onClick={() => hide(f, !f.hidden)} title={f.hidden ? "Show it in cast menus again" : "Keep it out of cast menus"}>
                        {f.hidden ? "Show" : "Hide"}
                      </button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <h2>Saved devices</h2>
      <table class="acc-table">
        <tbody>
          {saved.map((d) => (
            <tr key={d.id}>
              <td>
                <b>{d.name}</b>
                <div class="acc-muted">
                  {d.model ? `${d.model} · ` : ""}
                  {addr(d)}
                  {d.lastSeen ? ` · seen ${ago(d.lastSeen)}` : ""}
                </div>
              </td>
              <td class="acc-actions">
                <button
                  class="acc-link danger"
                  onClick={async () =>
                    (await confirmModal({
                      title: `Remove ${d.name}?`,
                      message: "It is no longer saved. If discovery still finds it, it stays in cast menus under its own name unless you hide it. A deck showing on it now keeps showing until it is stopped.",
                      confirm: "Remove device",
                      danger: true,
                    })) && api("DELETE", `/api/admin/cast/devices/${encodeURIComponent(d.id)}`).then(load, fail)
                  }
                >
                  Remove
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {list && saved.length === 0 && <p class="acc-muted">No saved devices. Found devices appear in cast menus without saving; save one to rename it or keep it listed while it is off.</p>}

      <h2>Add a device by IP address</h2>
      <form class="acc-form" onSubmit={add}>
        <label>
          Name shown to people
          <input value={name} required maxLength={80} placeholder="Living room TV" onInput={(e) => setName(e.currentTarget.value)} />
        </label>
        <label>
          IP address or host name
          <input value={host} required placeholder="192.168.1.50" autocomplete="off" spellcheck={false} onInput={(e) => setHost(e.currentTarget.value)} />
          <small>For a device the scan doesn't find (another subnet, say). Find its address in the Google Home app under the device's settings.</small>
        </label>
        <div class="acc-inline">
          <button class="acc-primary">Add device</button>
        </div>
      </form>
    </section>
  );
}

addSettingsTab({ id: "cast", label: "Cast devices", render: () => <CastDevices /> });
