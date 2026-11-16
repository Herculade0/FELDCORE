import { useEffect, useState } from "react";
import { Cpu, MemoryStick, HardDrive, Activity, Network, Clock } from "lucide-react";
import type { Info } from "../types";
import { useStatsStream } from "../lib/useStream";
import { bytes, bytesPerSec, duration, pct } from "../lib/format";
import { Card, Meter, StatTile } from "./ui";

export default function Dashboard() {
  const { stats, status } = useStatsStream();
  const [info, setInfo] = useState<Info | null>(null);

  useEffect(() => {
    fetch("/api/v1/system/info")
      .then((r) => (r.ok ? r.json() : null))
      .then(setInfo)
      .catch(() => setInfo(null));
  }, []);

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-slate-100">
            {info?.hostname ?? "Dashboard"}
          </h1>
          <p className="text-sm text-slate-400">
            {info
              ? `${info.platform} ${info.platformVersion} · ${info.kernel} · ${info.arch}`
              : "Loading host info…"}
          </p>
        </div>
        <StatusPill status={status} />
      </header>

      <section className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile
          label="CPU"
          value={pct(stats?.cpuPercent ?? 0)}
          sub={info?.cpuModel ? truncate(info.cpuModel, 32) : `${info?.cpuCores ?? "–"} cores`}
          percent={stats?.cpuPercent ?? 0}
          icon={<Cpu size={16} />}
        />
        <StatTile
          label="Memory"
          value={pct(stats?.memPercent ?? 0)}
          sub={
            stats
              ? `${bytes(stats.memUsed)} / ${bytes(stats.memTotal)}`
              : "–"
          }
          percent={stats?.memPercent ?? 0}
          icon={<MemoryStick size={16} />}
        />
        <StatTile
          label="Load (1m)"
          value={(stats?.load1 ?? 0).toFixed(2)}
          sub={
            stats ? `5m ${stats.load5.toFixed(2)} · 15m ${stats.load15.toFixed(2)}` : "–"
          }
          icon={<Activity size={16} />}
        />
        <StatTile
          label="Uptime"
          value={stats ? duration(stats.uptimeSeconds) : "–"}
          sub={info ? `since boot` : ""}
          icon={<Clock size={16} />}
        />
      </section>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card title="Storage" icon={<HardDrive size={16} />}>
          <div className="space-y-4">
            {stats?.disks?.length ? (
              stats.disks.map((d) => (
                <div key={d.mountpoint}>
                  <div className="mb-1 flex items-center justify-between text-sm">
                    <span className="font-medium text-slate-200">{d.mountpoint}</span>
                    <span className="text-slate-400">
                      {bytes(d.usedBytes)} / {bytes(d.totalBytes)}
                    </span>
                  </div>
                  <Meter percent={d.usedPercent} />
                </div>
              ))
            ) : (
              <Empty label="No mounted filesystems reported" />
            )}
          </div>
        </Card>

        <Card title="Network" icon={<Network size={16} />}>
          <div className="space-y-3">
            {stats?.net?.length ? (
              stats.net.map((n) => (
                <div
                  key={n.name}
                  className="flex items-center justify-between text-sm"
                >
                  <span className="font-medium text-slate-200">{n.name}</span>
                  <span className="text-slate-400">
                    <span className="text-emerald-400">↓ {bytesPerSec(n.rxPerSec)}</span>
                    {"  "}
                    <span className="text-sky-400">↑ {bytesPerSec(n.txPerSec)}</span>
                  </span>
                </div>
              ))
            ) : (
              <Empty label="Measuring throughput…" />
            )}
          </div>
        </Card>
      </div>

      {stats?.perCore?.length ? (
        <Card title={`CPU cores (${stats.perCore.length})`} icon={<Cpu size={16} />}>
          <div className="grid grid-cols-2 gap-x-6 gap-y-2 sm:grid-cols-4 xl:grid-cols-8">
            {stats.perCore.map((c, i) => (
              <div key={i}>
                <div className="mb-1 flex justify-between text-xs text-slate-400">
                  <span>#{i}</span>
                  <span>{pct(c)}</span>
                </div>
                <Meter percent={c} />
              </div>
            ))}
          </div>
        </Card>
      ) : null}
    </div>
  );
}

function StatusPill({ status }: { status: string }) {
  const map: Record<string, string> = {
    live: "bg-emerald-500/15 text-emerald-300 ring-emerald-500/30",
    connecting: "bg-amber-500/15 text-amber-300 ring-amber-500/30",
    offline: "bg-rose-500/15 text-rose-300 ring-rose-500/30",
  };
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium ring-1 ${map[status]}`}
    >
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {status}
    </span>
  );
}

function Empty({ label }: { label: string }) {
  return <div className="py-6 text-center text-sm text-slate-500">{label}</div>;
}

function truncate(s: string, n: number): string {
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}
