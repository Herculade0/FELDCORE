import { useEffect, useState } from "react";
import { Shield } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, CardTitle } from "../ui";
import { Alert, Button, SelectField, TextField, Toggle } from "../form";

interface Tunnel {
  id: string;
  name: string;
  provider: string;
  enabled: boolean;
  address?: string;
  endpoint?: string;
  server?: string;
  country?: string;
  natpmp?: boolean;
  hasKey?: boolean;
  forwards?: { container: string; hostPort: number; proto: string }[];
}

interface VPN {
  enabled: boolean;
  tunnels: Tunnel[];
}

interface Status {
  state: string;
  publicIp?: string;
  handshakeAge?: number;
  forwardedPort?: number;
  killSwitch?: boolean;
  message?: string;
  server?: string;
  country?: string;
}

interface Parsed {
  provider: string;
  detected: string;
  server?: string;
  country?: string;
  natpmp?: boolean;
  ignoredHooks?: boolean;
  address?: string;
  endpoint?: string;
  peerKey?: string;
}

export default function VPNSettings() {
  const [vpn, setVpn] = useState<VPN | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<Record<string, Status>>({});

  const load = () =>
    api<VPN>("/vpn")
      .then((v) => {
        setVpn({ ...v, tunnels: v.tunnels ?? [] });
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)));

  useEffect(() => {
    void load();
  }, []);

  useEffect(() => {
    if (!vpn) return;
    let stop = false;
    const poll = () => {
      vpn.tunnels.forEach((t) => {
        api<Status>(`/vpn/tunnels/${encodeURIComponent(t.id)}/status`)
          .then((s) => {
            if (!stop) setStatus((cur) => ({ ...cur, [t.id]: s }));
          })
          .catch(() => {});
      });
    };
    poll();
    const timer = setInterval(poll, 5000);
    return () => {
      stop = true;
      clearInterval(timer);
    };
  }, [vpn]);

  return (
    <Card>
      <div id="settings-vpn">
        <CardTitle icon={Shield} title="VPN" hint="Off until you turn a tunnel on" />
        <p className="mt-3 text-sm font-medium leading-relaxed text-ink-2">
          WireGuard tunnels, including a Proton VPN config. A container can route through one of them.
          If the tunnel drops, that container has no network.
        </p>
        {error && (
          <div className="mt-3">
            <Alert>{error}</Alert>
          </div>
        )}
        {vpn && (
          <>
            <label className="mt-4 flex items-center justify-between gap-4 text-sm font-semibold text-ink">
              VPN
              <Toggle
                checked={vpn.enabled}
                onChange={(enabled) => {
                  api("/vpn", { method: "PUT", body: { enabled } })
                    .then(load)
                    .catch((e) => setError(errorMessage(e)));
                }}
                label="VPN"
              />
            </label>
            <ul className="mt-4 space-y-3">
              {vpn.tunnels.map((t) => (
                <TunnelRow
                  key={t.id}
                  tunnel={t}
                  status={status[t.id]}
                  onChange={load}
                  onError={setError}
                />
              ))}
              {vpn.tunnels.length === 0 && (
                <li className="rounded-2xl border border-dashed border-line px-4 py-5 text-center text-xs font-medium text-ink-3">
                  No tunnels yet. Import a WireGuard configuration below.
                </li>
              )}
            </ul>
            <AddTunnel onSaved={load} onError={setError} />
          </>
        )}
      </div>
    </Card>
  );
}

function TunnelRow({
  tunnel,
  status,
  onChange,
  onError,
}: {
  tunnel: Tunnel;
  status?: Status;
  onChange: () => void;
  onError: (msg: string) => void;
}) {
  const proton = tunnel.provider === "proton";
  const state = status?.state ?? (tunnel.enabled ? "down" : "off");
  const handshake =
    status?.handshakeAge === undefined || status.handshakeAge < 0
      ? state === "up"
        ? "live, age not reported"
        : "none"
      : state === "off"
        ? "—"
        : `${status.handshakeAge}s ago`;
  return (
    <li className="rounded-2xl border border-line p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-extrabold text-ink">{tunnel.name}</h3>
            <span className="rounded-full bg-accent/12 px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider text-accent">
              {proton ? "Proton" : "WireGuard"}
            </span>
            <span className={`text-xs font-bold ${state === "up" ? "text-ok" : state === "off" ? "text-ink-3" : "text-warn"}`}>
              {state === "up" ? "Up" : state === "off" ? "Off" : "Down"}
            </span>
          </div>
          <p className="mt-1 text-xs font-medium text-ink-2">
            {proton && (tunnel.country || tunnel.server)
              ? [tunnel.country, tunnel.server].filter(Boolean).join(" · ")
              : tunnel.endpoint || "No endpoint"}
          </p>
        </div>
        <Toggle
          checked={tunnel.enabled}
          label={`Enable ${tunnel.name}`}
          onChange={(enabled) => {
            api(`/vpn/tunnels/${encodeURIComponent(tunnel.id)}/enabled`, { method: "POST", body: { enabled } })
              .then(onChange)
              .catch((e) => onError(errorMessage(e)));
          }}
        />
      </div>
      <dl className="mt-3 grid gap-2 text-xs font-semibold sm:grid-cols-3">
        <Stat k="Public IP" v={status?.publicIp || "—"} />
        <Stat k="Handshake" v={handshake} />
        <Stat
          k="Forwarded port"
          v={
            proton && tunnel.natpmp
              ? status?.forwardedPort
                ? String(status.forwardedPort)
                : state === "up"
                  ? "Waiting for NAT-PMP"
                  : "—"
              : "—"
          }
        />
      </dl>
      {status?.message && <p className="mt-2 text-xs font-medium text-ink-3">{status.message}</p>}
      {tunnel.forwards && tunnel.forwards.length > 0 && (
        <p className="mt-2 text-xs font-medium text-ink-2">
          Routed containers: {tunnel.forwards.map((f) => f.container).filter((v, i, a) => a.indexOf(v) === i).join(", ")}. Kill switch on.
        </p>
      )}
      <div className="mt-3">
        <Button
          size="sm"
          variant="danger"
          onClick={() => {
            api(`/vpn/tunnels/${encodeURIComponent(tunnel.id)}`, { method: "DELETE" })
              .then(onChange)
              .catch((e) => onError(errorMessage(e)));
          }}
        >
          Remove
        </Button>
      </div>
    </li>
  );
}

function Stat({ k, v }: { k: string; v: string }) {
  return (
    <div>
      <dt className="text-[10px] font-bold uppercase tracking-wider text-ink-3">{k}</dt>
      <dd className="mt-0.5 text-ink">{v}</dd>
    </div>
  );
}

function AddTunnel({ onSaved, onError }: { onSaved: () => void; onError: (msg: string) => void }) {
  const [preset, setPreset] = useState("auto");
  const [conf, setConf] = useState("");
  const [parsed, setParsed] = useState<Parsed | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);

  const readFile = (file: File) => {
    file.text().then(setConf).catch(() => onError("Could not read that file."));
  };

  return (
    <div className="mt-5 rounded-2xl border border-line p-4">
      <h3 className="text-sm font-extrabold text-ink">Add a tunnel</h3>
      <p className="mt-1 text-xs font-medium text-ink-3">
        Import a .conf or paste it. PostUp and PostDown lines are ignored. The private key is stored encrypted.
      </p>
      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <SelectField label="Preset" value={preset} onChange={(e) => setPreset(e.target.value)}>
          <option value="auto">Recognise automatically</option>
          <option value="wireguard">Standard WireGuard</option>
          <option value="proton">Proton VPN</option>
        </SelectField>
        <div>
          <label className="mb-1.5 block text-xs font-bold text-ink-2">WireGuard file</label>
          <input
            type="file"
            accept=".conf,text/plain"
            className="block w-full text-xs font-medium text-ink-2"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) readFile(file);
            }}
          />
        </div>
      </div>
      <label className="mb-1.5 mt-4 block text-xs font-bold text-ink-2">Configuration</label>
      <textarea
        value={conf}
        onChange={(e) => setConf(e.target.value)}
        rows={6}
        spellCheck={false}
        placeholder={"[Interface]\nPrivateKey = …\nAddress = 10.2.0.2/32\n\n[Peer]\nPublicKey = …\nEndpoint = 1.2.3.4:51820"}
        className="w-full rounded-xl border border-line bg-surface-2 px-3.5 py-2.5 font-mono text-xs text-ink"
      />
      {parsed && (
        <div className="mt-3">
          <Alert tone="info">
            {parsed.provider === "proton" ? "Recognised as Proton VPN." : "Standard WireGuard."}{" "}
            {[parsed.country, parsed.server].filter(Boolean).join(" · ")}
            {parsed.natpmp ? " NAT-PMP port forwarding is enabled in this config." : ""}
            {parsed.ignoredHooks ? " Hook commands were ignored." : ""}
          </Alert>
        </div>
      )}
      <div className="mt-3 max-w-sm">
        <TextField label="Name (optional)" value={name} onChange={(e) => setName(e.target.value)} placeholder="Proton Netherlands" />
      </div>
      <div className="mt-4 flex flex-wrap gap-2">
        <Button
          loading={busy}
          onClick={() => {
            setBusy(true);
            api<Parsed>("/vpn/parse", { method: "POST", body: { conf, preset } })
              .then((p) => {
                setParsed(p);
                if (!name && p.provider === "proton" && p.server) setName(`Proton ${p.server}`);
              })
              .catch((e) => onError(errorMessage(e)))
              .finally(() => setBusy(false));
          }}
        >
          Check
        </Button>
        <Button
          variant="primary"
          loading={busy}
          onClick={() => {
            setBusy(true);
            api("/vpn/tunnels", { method: "POST", body: { conf, preset, name } })
              .then(() => {
                setConf("");
                setParsed(null);
                setName("");
                onSaved();
              })
              .catch((e) => onError(errorMessage(e)))
              .finally(() => setBusy(false));
          }}
        >
          Save tunnel
        </Button>
      </div>
    </div>
  );
}
