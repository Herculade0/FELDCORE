import { useEffect, useState } from "react";
import { Globe } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Alert, Button, SelectField, TextField, Toggle } from "../form";
import { Modal } from "../form";
import type { Container } from "./types";

interface Address {
  container: string;
  suggestion: { hostname: string; port: number; from: string };
  ports: number[];
  lan: { enabled: boolean; hostname: string; port: number; tls: string };
  public: { enabled: boolean; hostname: string; port: number };
  tunnelMode: string;
  apiTokenSet: boolean;
  domain: string;
}

export default function AddressDialog({
  container,
  onClose,
}: {
  container: Container;
  onClose: () => void;
}) {
  const [data, setData] = useState<Address | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [warning, setWarning] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [lanOn, setLanOn] = useState(false);
  const [host, setHost] = useState("");
  const [port, setPort] = useState("");
  const [tls, setTls] = useState("internal");
  const [pubOn, setPubOn] = useState(false);
  const [pubHost, setPubHost] = useState("");
  const [pubPort, setPubPort] = useState("");

  useEffect(() => {
    api<Address>(`/docker/containers/${encodeURIComponent(container.id)}/address`)
      .then((d) => {
        setData(d);
        setLanOn(d.lan.enabled);
        setHost(d.lan.hostname || d.suggestion.hostname);
        setPort(String(d.lan.port || d.suggestion.port || ""));
        setTls(d.lan.tls || "internal");
        setPubOn(d.public.enabled);
        setPubHost(d.public.hostname);
        setPubPort(String(d.public.port || d.lan.port || d.suggestion.port || ""));
      })
      .catch((e) => setError(errorMessage(e)));
  }, [container.id]);

  const save = async () => {
    setBusy(true);
    setError(null);
    setWarning(null);
    try {
      const res = await api<{ warning?: string }>(`/docker/containers/${encodeURIComponent(container.id)}/address`, {
        method: "PUT",
        body: {
          lanEnabled: lanOn,
          lanHostname: host,
          lanPort: Number(port),
          lanTls: tls,
          publicEnabled: pubOn,
          publicHostname: pubHost,
          publicPort: Number(pubPort || port),
        },
      });
      if (res.warning) setWarning(res.warning);
      else onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title={`Web address · ${container.name}`}
      size="lg"
      onClose={onClose}
      onSubmit={() => void save()}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" loading={busy} disabled={!data}>
            Save address
          </Button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      {warning && (
        <div className="mb-4">
          <Alert tone="warn">{warning}</Alert>
        </div>
      )}
      {!data && !error && <p className="text-sm font-semibold text-ink-2">Reading this container…</p>}
      {data && (
        <div className="space-y-6">
          <section>
            <div className="flex items-center gap-3">
              <Toggle checked={lanOn} onChange={setLanOn} label="Name on your home network" />
              <div>
                <p className="text-sm font-bold text-ink">Name on your home network</p>
                <p className="text-xs font-medium text-ink-3">{data.suggestion.from}</p>
              </div>
            </div>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <TextField label="Hostname" value={host} onChange={(e) => setHost(e.target.value)} placeholder={data.suggestion.hostname} disabled={!lanOn} />
              <TextField label="Container port" value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" disabled={!lanOn} hint="The port inside the container, usually the web page." />
            </div>
            {data.ports.length > 0 && (
              <div className="mt-3 flex flex-wrap gap-2">
                {data.ports.map((p) => (
                  <button
                    key={p}
                    type="button"
                    onClick={() => setPort(String(p))}
                    className="rounded-lg bg-surface-2 px-2 py-1 font-mono text-[11px] font-bold text-ink-2"
                  >
                    {p}
                  </button>
                ))}
              </div>
            )}
            <div className="mt-4">
              <SelectField label="How people connect" value={tls} onChange={(e) => setTls(e.target.value)} disabled={!lanOn}>
                <option value="internal">HTTPS with the local certificate</option>
                <option value="off">Plain HTTP</option>
                <option value="acme">HTTPS with Let's Encrypt</option>
              </SelectField>
            </div>
            <p className="mt-3 flex items-start gap-2 text-xs font-medium leading-relaxed text-ink-3">
              <Globe size={14} className="mt-0.5 shrink-0" />
              This is for a web page. Game and UDP ports stay on your home network. Caddy needs ports 80 and 443 on this server; if something else is using them, saving explains which container.
            </p>
          </section>

          <section className="border-t border-line pt-5">
            <div className="flex items-center gap-3">
              <Toggle checked={pubOn} onChange={setPubOn} label="Expose publicly" />
              <div>
                <p className="text-sm font-bold text-ink">Expose publicly</p>
                <p className="text-xs font-medium text-ink-3">
                  Cloudflare Tunnel. No ports are opened on your router.
                  {data.tunnelMode === "off" ? " Remote access is currently off." : ""}
                  {!data.apiTokenSet ? " An API token is needed to publish the name." : ""}
                </p>
              </div>
            </div>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <TextField
                label="Public hostname"
                value={pubHost}
                onChange={(e) => setPubHost(e.target.value)}
                placeholder="app.example.com"
                disabled={!pubOn}
              />
              <TextField label="Container port" value={pubPort} onChange={(e) => setPubPort(e.target.value)} inputMode="numeric" disabled={!pubOn} />
            </div>
          </section>
        </div>
      )}
    </Modal>
  );
}
