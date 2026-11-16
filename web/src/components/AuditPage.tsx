import { useEffect, useState } from "react";
import { RefreshCw, ScrollText } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { Card, CardTitle } from "./ui";
import { Alert, Button } from "./form";

interface Event {
  id: string;
  time: string;
  tokenName: string;
  action: string;
  target?: string;
  result: string;
  ip?: string;
  host?: string;
  detail?: Record<string, string>;
}

interface Log {
  events: Event[];
  retentionDays: number;
  maxEvents: number;
}

function when(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

const RESULT: Record<string, { label: string; cls: string }> = {
  ok: { label: "OK", cls: "bg-ok/12 text-ok" },
  denied: { label: "Denied", cls: "bg-warn/15 text-warn" },
  error: { label: "Error", cls: "bg-bad/12 text-bad" },
};

export default function AuditPage() {
  const [log, setLog] = useState<Log | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = () => {
    setLoading(true);
    api<Log>("/audit")
      .then((next) => {
        setLog(next);
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    void load();
  }, []);

  const events = log?.events ?? [];

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">Audit</h1>
          <p className="mt-2 max-w-xl text-sm font-semibold text-ink-2">
            Every API and MCP call made with an agent token, and every published request the protection layer blocked.
            {log
              ? ` The newest ${log.maxEvents.toLocaleString()} are kept, for ${log.retentionDays} days.`
              : ""}
          </p>
        </div>
        <Button variant="secondary" size="sm" loading={loading} onClick={load}>
          <RefreshCw size={14} />
          Refresh
        </Button>
      </header>

      {error && <Alert>{error}</Alert>}

      <Card>
        <CardTitle icon={ScrollText} title="Agent calls" hint={events.length ? `${events.length} shown` : undefined} />
        {loading && events.length === 0 ? (
          <p className="mt-5 text-sm font-medium text-ink-3">Loading…</p>
        ) : events.length === 0 ? (
          <p className="mt-5 text-sm font-medium leading-relaxed text-ink-3">
            No agent has called FELDCORE yet. Calls made with a token show up here. Signing in from the browser does not.
          </p>
        ) : (
          <ul className="mt-4 divide-y divide-line">
            {events.map((ev) => {
              const tone = RESULT[ev.result] ?? { label: ev.result, cls: "bg-track text-ink-2" };
              return (
                <li key={ev.id} className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:gap-4">
                  <time className="w-36 shrink-0 text-xs font-semibold text-ink-3" dateTime={ev.time}>
                    {when(ev.time)}
                  </time>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-bold text-ink" title={ev.action}>
                      {ev.action}
                    </div>
                    <div className="truncate text-xs font-medium text-ink-3">
                      {ev.tokenName}
                      {ev.target ? ` · ${ev.target}` : ""}
                      {ev.detail?.reason ? ` · ${ev.detail.reason}` : ""}
                      {ev.ip ? ` · ${ev.ip}` : ""}
                      {ev.host ? ` · ${ev.host}` : ""}
                    </div>
                  </div>
                  <span className={`w-fit rounded-full px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider ${tone.cls}`}>
                    {tone.label}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </Card>
    </div>
  );
}
