import { useEffect, useState } from "react";
import { Cloud, ShieldAlert } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, CardTitle } from "../ui";
import { Alert, Button, TextField, Toggle } from "../form";
import { InstallPanel, showInstall, useInstall } from "../install/InstallPanel";

interface Route {
  container: string;
  hostname: string;
  port: number;
  enabled: boolean;
}

interface RemoteView {
  mode: string;
  tunnelId?: string;
  tunnelName?: string;
  accountId?: string;
  apiTokenSet: boolean;
  tunnelTokenSet: boolean;
  exposeAdmin: boolean;
  adminHostname?: string;
  adminWarning?: string;
  runsOnHost?: boolean;
  routes: Route[];
  status: { state: string; summary: string; connections: number; connector: string; detail?: string };
}

const MODES: { id: string; title: string; body: string }[] = [
  {
    id: "off",
    title: "Off",
    body: "Only this network can reach FELDCORE. This is the default, and nothing is published.",
  },
  {
    id: "token",
    title: "Tunnel token",
    body: "Paste a token from the Cloudflare dashboard. FELDCORE runs the connector for you.",
  },
  {
    id: "api",
    title: "API token",
    body: "FELDCORE creates the tunnel and the DNS names. You only paste an API token.",
  },
];

export default function RemoteAccess() {
  const [view, setView] = useState<RemoteView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [mode, setMode] = useState("off");
  const [tunnelToken, setTunnelToken] = useState("");
  const [apiToken, setApiToken] = useState("");
  const [accountId, setAccountId] = useState("");
  const [tunnelName, setTunnelName] = useState("feldcore");
  const [expose, setExpose] = useState(false);
  const [adminHost, setAdminHost] = useState("");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);

  const load = () =>
    api<RemoteView>("/remote")
      .then((v) => {
        setView(v);
        setMode(v.mode || "off");
        setAccountId(v.accountId || "");
        setTunnelName(v.tunnelName || "feldcore");
        setExpose(v.exposeAdmin);
        setAdminHost(v.adminHostname || "");
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)));

  const cloudflared = useInstall("cloudflared", () => void load());

  useEffect(() => {
    void load();
  }, []);

  const save = async () => {
    setBusy(true);
    setSaved(false);
    setError(null);
    try {
      const next = await api<RemoteView>("/remote", {
        method: "PUT",
        body: {
          mode,
          tunnelToken,
          apiToken,
          accountId,
          tunnelName,
          exposeAdmin: expose,
          adminHostname: adminHost,
        },
      });
      setView(next);
      setTunnelToken("");
      setApiToken("");
      setSaved(true);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const status = view?.status;
  const tone = status?.state === "healthy" || status?.state === "up" ? "text-ok" : status?.state === "off" ? "text-ink-3" : status?.state === "starting" ? "text-warn" : "text-bad";

  return (
    <div id="settings-remote" className="space-y-6">
      <div>
        <h2 className="text-2xl font-extrabold tracking-tight text-ink">Remote access</h2>
        <p className="mt-2 max-w-2xl text-sm font-medium leading-relaxed text-ink-2">
          Reach this server from the internet without opening ports on your router. A Cloudflare Tunnel
          makes the connection outwards. Nothing is published until you turn it on here, or on a container.
        </p>
      </div>

      {error && <Alert>{error}</Alert>}

      <Card>
        <CardTitle icon={Cloud} title="Cloudflare Tunnel" hint={connectorHint(status?.connector)} />
        <div className="mt-4 rounded-2xl border border-line bg-surface-2 px-4 py-3">
          <p className={`text-sm font-bold ${tone}`}>{labelFor(status?.state)}</p>
          <p className="mt-1 text-sm font-medium leading-relaxed text-ink-2">{status?.summary || "Checking the tunnel…"}</p>
          {status && status.connections > 0 && (
            <p className="mt-1 text-xs font-semibold text-ink-3">
              {status.connections} connection{status.connections === 1 ? "" : "s"} to Cloudflare
              {view?.tunnelId ? ` · tunnel ${view.tunnelId.slice(0, 8)}` : ""}
            </p>
          )}
          {status?.detail && <p className="mt-1 text-xs font-medium text-ink-3">{status.detail}</p>}
        </div>

        <div className="mt-5 grid gap-3 lg:grid-cols-3">
          {MODES.map((m) => {
            const on = mode === m.id;
            return (
              <button
                key={m.id}
                type="button"
                onClick={() => setMode(m.id)}
                aria-pressed={on}
                className={`rounded-2xl border px-4 py-3 text-left transition-colors ${
                  on ? "border-accent bg-accent/8" : "border-line hover:border-accent/40"
                }`}
              >
                <p className="text-sm font-bold text-ink">{m.title}</p>
                <p className="mt-1 text-xs font-medium leading-relaxed text-ink-2">{m.body}</p>
              </button>
            );
          })}
        </div>

        {mode === "token" && (
          <div className="mt-5 grid gap-4 sm:grid-cols-2">
            <TextField
              label="Tunnel token"
              type="password"
              autoComplete="off"
              value={tunnelToken}
              onChange={(e) => setTunnelToken(e.target.value)}
              placeholder={view?.tunnelTokenSet ? "Saved — paste a new one to replace it" : "Paste from the Cloudflare dashboard"}
              hint="Zero Trust → Networks → Tunnels → your tunnel → Install connector. The token is stored encrypted and is never shown again."
            />
            <TextField
              label="API token (optional)"
              type="password"
              autoComplete="off"
              value={apiToken}
              onChange={(e) => setApiToken(e.target.value)}
              placeholder={view?.apiTokenSet ? "Saved — paste a new one to replace it" : "Needed to publish hostnames"}
              hint="A tunnel token only connects this server. Publishing a hostname also needs an API token that can edit tunnels and DNS."
            />
          </div>
        )}

        {mode === "api" && (
          <div className="mt-5 grid gap-4 sm:grid-cols-2">
            <TextField
              className="sm:col-span-2"
              label="API token"
              type="password"
              autoComplete="off"
              value={apiToken}
              onChange={(e) => setApiToken(e.target.value)}
              placeholder={view?.apiTokenSet ? "Saved — paste a new one to replace it" : "Cloudflare API token"}
              hint="Create one at dash.cloudflare.com/profile/api-tokens. Grant Account / Cloudflare Tunnel / Edit, and Zone / DNS / Edit."
            />
            <TextField label="Account ID" value={accountId} onChange={(e) => setAccountId(e.target.value)} hint="Leave blank if the token can see only one account." />
            <TextField label="Tunnel name" value={tunnelName} onChange={(e) => setTunnelName(e.target.value)} hint="Shown in the Cloudflare dashboard." />
          </div>
        )}

        <div className="mt-4 flex flex-wrap items-center gap-3">
          <Button variant="primary" loading={busy} onClick={() => void save()}>
            {mode === "off" ? "Turn remote access off" : "Save and connect"}
          </Button>
          {mode !== "off" && !view?.runsOnHost && (
            <Button
              onClick={() => void cloudflared.start().catch(() => {})}
              disabled={busy}
              loading={cloudflared.active || cloudflared.starting}
            >
              {cloudflared.active ? "Installing cloudflared…" : "Install cloudflared on the host"}
            </Button>
          )}
          {saved && <p className="text-xs font-semibold text-ok">Saved.</p>}
        </div>
        <p className="mt-3 text-xs font-medium leading-relaxed text-ink-3">
          {view?.runsOnHost
            ? "FELDCORE OS runs the connector from the cloudflared program shipped with the image (2026.11.0 at /usr/bin/cloudflared). It is not a container, and it starts even while Docker is still coming up. The token is read from a mode 0600 file. It is not put on the command line, and it is not logged."
            : "When Docker is running, FELDCORE runs cloudflared as a container. If Docker is not running, it can run the cloudflared program instead. The token file is mode 0600. It is not written into the command, and it is not logged."}
        </p>
        {cloudflared.error && (
          <div className="mt-3">
            <Alert>{cloudflared.error}</Alert>
          </div>
        )}
        {showInstall(cloudflared.job) && cloudflared.job && (
          <div className="mt-4">
            <InstallPanel
              job={cloudflared.job}
              busy={cloudflared.starting}
              onRetry={() => void cloudflared.start().catch(() => {})}
            />
          </div>
        )}
      </Card>

      <Card>
        <CardTitle icon={ShieldAlert} title="This admin page" />
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          Leave this off unless you really want to sign in from outside your house. Containers you expose later are separate, and they stay off until you ask.
        </p>
        <div className="mt-4 flex items-center gap-3">
          <Toggle checked={expose} onChange={setExpose} label="Expose the FELDCORE page" />
          <span className="text-sm font-bold text-ink">Expose the FELDCORE page</span>
        </div>
        {expose && (
          <div className="mt-4 space-y-4">
            <TextField
              label="Hostname"
              value={adminHost}
              onChange={(e) => setAdminHost(e.target.value)}
              placeholder="feldcore.example.com"
              hint="A domain Cloudflare already hosts. home.arpa names cannot be published."
            />
            <Alert tone="warn">
              {view?.adminWarning ||
                "This publishes the FELDCORE admin page on the internet. Anyone who can open that hostname can try to sign in. Put Cloudflare Access in front of it (Zero Trust → Access → Applications) so visitors pass an identity check before they reach the login page."}
            </Alert>
          </div>
        )}
        <div className="mt-4">
          <Button variant="primary" loading={busy} onClick={() => void save()}>
            Save admin access
          </Button>
        </div>
      </Card>

      <Card>
        <CardTitle icon={Cloud} title="Published containers" hint="Opt in per container" />
        {view && view.routes.length > 0 ? (
          <ul className="mt-4 space-y-2">
            {view.routes.map((r) => (
              <li key={r.container} className="flex flex-wrap items-baseline justify-between gap-2 rounded-2xl border border-line bg-surface-2 px-4 py-3">
                <span className="text-sm font-bold text-ink">{r.container}</span>
                <span className="font-mono text-xs text-ink-2">
                  {r.hostname} → port {r.port}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="mt-4 text-sm font-medium leading-relaxed text-ink-2">
            No container is exposed. Open Docker, choose a container, and turn on <span className="font-bold text-ink">Expose publicly</span>. Pick a hostname such as plex.example.com and the container's web port. FELDCORE writes the tunnel route and the DNS record.
          </p>
        )}
      </Card>
    </div>
  );
}

function connectorHint(connector?: string) {
  switch (connector) {
    case "binary":
      return "Host program";
    case "container":
      return "Container";
    default:
      return "Not connected";
  }
}

function labelFor(state?: string) {
  switch (state) {
    case "healthy":
      return "Connected";
    case "up":
      return "Connector running";
    case "starting":
      return "Waiting for Cloudflare";
    case "down":
      return "Not running";
    default:
      return "Off";
  }
}
