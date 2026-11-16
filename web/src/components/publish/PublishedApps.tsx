import { useEffect, useState } from "react";
import { Globe } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, CardTitle } from "../ui";
import { Alert } from "../form";

export interface PublishedItem {
  container: string;
  hostname: string;
  target: string;
  access: string;
  accessLabel: string;
  groups?: string[];
  cert: string;
  certStatus: string;
  tunnel: string;
  tunnelDetail: string;
}

interface Overview {
  domain: string;
  apps: PublishedItem[];
}

const CERT: Record<string, string> = {
  letsencrypt: "Let's Encrypt",
  local: "Local certificate",
  http: "Plain HTTP",
  missing: "Not routed",
};

const CERT_STATUS: Record<string, string> = {
  ready: "Ready",
  pending: "Waiting",
  down: "Proxy down",
  blocked: "Ports 80/443 busy",
  off: "Off",
  missing: "Not routed",
};

const TUNNEL: Record<string, string> = {
  connected: "Connected",
  pending: "Waiting",
  skipped: "Home network only",
  off: "Off",
  down: "Down",
};

export function certLabel(item: PublishedItem): string {
  const kind = CERT[item.cert] ?? item.cert;
  const status = CERT_STATUS[item.certStatus] ?? item.certStatus;
  if (item.certStatus === "ready" || item.certStatus === "off") return `${kind} · ${status}`;
  return `${kind} · ${status}`;
}

export function tunnelLabel(item: PublishedItem): string {
  return TUNNEL[item.tunnel] ?? item.tunnel;
}

export function PublishedTable({ apps }: { apps: PublishedItem[] }) {
  if (apps.length === 0) {
    return (
      <p className="mt-5 text-sm font-medium leading-relaxed text-ink-3">
        Nothing is published. Open a container and turn on Publish. Nothing is exposed until you do.
      </p>
    );
  }
  return (
    <div className="mt-4 overflow-x-auto">
      <table className="w-full min-w-[40rem] text-left">
        <thead>
          <tr className="text-[10px] font-bold uppercase tracking-[0.14em] text-ink-3">
            <th className="pb-2 pr-4 font-bold">Hostname</th>
            <th className="pb-2 pr-4 font-bold">Target</th>
            <th className="pb-2 pr-4 font-bold">Citizen ID</th>
            <th className="pb-2 pr-4 font-bold">Certificate</th>
            <th className="pb-2 font-bold">Tunnel</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {apps.map((app) => (
            <tr key={app.hostname + app.container}>
              <td className="py-3 pr-4 align-top">
                <div className="text-sm font-bold text-ink">{app.hostname}</div>
                {app.groups && app.groups.length > 0 && (
                  <div className="text-xs font-medium text-ink-3">{app.groups.join(", ")}</div>
                )}
              </td>
              <td className="py-3 pr-4 align-top font-mono text-xs font-semibold text-ink-2">{app.target}</td>
              <td className="py-3 pr-4 align-top text-sm font-semibold text-ink">{app.accessLabel}</td>
              <td className="py-3 pr-4 align-top text-sm font-semibold text-ink">{certLabel(app)}</td>
              <td className="py-3 align-top">
                <div className="text-sm font-semibold text-ink">{tunnelLabel(app)}</div>
                {app.tunnelDetail && <div className="text-xs font-medium text-ink-3">{app.tunnelDetail}</div>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Published apps overview. Used on Docker and on Settings → Publishing. */
export default function PublishedApps() {
  const [data, setData] = useState<Overview | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api<Overview>("/publish")
      .then((d) => {
        setData({ ...d, apps: d.apps ?? [] });
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)));
  }, []);

  return (
    <div id="published-apps">
      <Card>
        <CardTitle
          icon={Globe}
          title="Published apps"
          hint={data ? `New names use ${data.domain}` : undefined}
        />
        {error && (
          <div className="mt-4">
            <Alert>{error}</Alert>
          </div>
        )}
        {!data && !error && <p className="mt-5 text-sm font-medium text-ink-3">Loading…</p>}
        {data && <PublishedTable apps={data.apps} />}
      </Card>
    </div>
  );
}
