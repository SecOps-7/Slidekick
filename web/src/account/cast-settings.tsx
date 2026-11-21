// Settings → Cast devices (admins). The server casts decks to these
// Chromecasts over the LAN; editors pick from this list in the editor.
import { confirmModal } from "../ui/modal";
import { useEffect, useState } from "preact/hooks";
import { addSettingsTab, api, Err } from "./pages";

interface Device {
  id: string;
  name: string;
  host: string;
  port: number;
}
interface Found {
  id: string;
  name: string;
  model: string;
  host: string;
  port: number;
  added: boolean;
}

function CastDevices() {
  const [list, setList] = useState<Device[]>([]);
  const [name, setName] = useState("");
  const [host, setHost] = useState("");
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  const [found, setFound] = useState<Found[] | null>(null);
  const [searching, setSearching] = useState(false);
  const load = () => api<{ devices: Device[] }>("GET", "/api/admin/cast/devices").then((d) => setList(d.devices), (x) => setErr((x as Error).message));
  useEffect(() => {
    load();
  }, []);

  const add = async (body: { name: string; host: string; port?: number }) => {
    setErr("");
    setNote("");
    try {
      const d = await api<Device>("POST", "/api/admin/cast/devices", body);
      setNote(`Added ${d.name}.`);
      await load();
      return true;
    } catch (x) {
      setErr((x as Error).message);
      return false;
    }
  };
  const submit = async (e: Event) => {
    e.preventDefault();
    if (await add({ name, host })) {
      setName("");
      setHost("");
    }
  };
  const discover = async () => {
    setErr("");
    setNote("");
    setSearching(true);
    try {
      const d = await api<{ devices: Found[] }>("POST", "/api/admin/cast/discover");
      setFound(d.devices);
    } catch (x) {
      setErr((x as Error).message);
    } finally {
      setSearching(false);
    }
  };

  return (
    <section>
      <p class="acc-muted">
        Editors can show a deck on these TVs: the server tells the Chromecast to open the deck, and it follows along as someone presents. Casting only works on your own
        network.
      </p>
      <Err msg={err} />
      {note && (
        <p class="acc-ok" role="status">
          {note}
        </p>
      )}
      <table class="acc-table">
        <tbody>
          {list.map((d) => (
            <tr key={d.id}>
              <td>
                <b>{d.name}</b>
                <div class="acc-muted">
                  {d.host}
                  {d.port !== 8009 ? `:${d.port}` : ""}
                </div>
              </td>
              <td class="acc-actions">
                <button
                  class="acc-link danger"
                  onClick={async () =>
                    (await confirmModal({ title: `Remove ${d.name}?`, message: "Editors can no longer cast to it. A deck showing on it now keeps showing until it is stopped.", confirm: "Remove device", danger: true })) &&
                    api("DELETE", `/api/admin/cast/devices/${encodeURIComponent(d.id)}`).then(load, (x) => setErr((x as Error).message))
                  }
                >
                  Remove
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {list.length === 0 && <p class="acc-muted">No cast devices yet.</p>}

      <h2>Add a device</h2>
      <form class="acc-form" onSubmit={submit}>
        <label>
          Name shown to people
          <input value={name} required maxLength={80} placeholder="Living room TV" onInput={(e) => setName(e.currentTarget.value)} />
        </label>
        <label>
          IP address or host name
          <input value={host} required placeholder="192.168.1.50" autocomplete="off" spellcheck={false} onInput={(e) => setHost(e.currentTarget.value)} />
          <small>Find it in the Google Home app under the device's settings. Give the TV a fixed address in your router so it doesn't change.</small>
        </label>
        <div class="acc-inline">
          <button class="acc-primary">Add device</button>
          <button type="button" onClick={discover} disabled={searching}>
            {searching ? "Looking…" : "Find devices"}
          </button>
        </div>
      </form>

      {found && (
        <div aria-live="polite">
          <h2>Found on your network</h2>
          {found.length === 0 ? (
            <p class="acc-muted">
              No devices answered. If Slidekick runs in Docker with the default bridge network, it can't see them: add them by IP address above, or run the container with host
              networking.
            </p>
          ) : (
            <table class="acc-table">
              <tbody>
                {found.map((f) => (
                  <tr key={f.id || f.host}>
                    <td>
                      <b>{f.name}</b>
                      <div class="acc-muted">
                        {f.model ? `${f.model} · ` : ""}
                        {f.host}
                      </div>
                    </td>
                    <td class="acc-actions">
                      {f.added ? (
                        <span class="acc-muted">Added</span>
                      ) : (
                        <button class="acc-link" onClick={async () => (await add({ name: f.name, host: f.host, port: f.port })) && setFound(found.map((x) => (x === f ? { ...x, added: true } : x)))}>
                          Add
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </section>
  );
}

addSettingsTab({ id: "cast", label: "Cast devices", render: () => <CastDevices /> });
