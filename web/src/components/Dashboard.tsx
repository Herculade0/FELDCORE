import { useEffect, useMemo, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  Cpu,
  HardDrive,
  LayoutGrid,
  MemoryStick,
  Network,
  Server,
  type LucideIcon,
} from "lucide-react";
import type { Info, Stats } from "../types";
import { NAV, type NavKey } from "../nav";
import { api } from "../lib/api";
import { HISTORY_SIZE, useStatsStream } from "../lib/useStream";
import { bytes, bytesPerSec, duration, greeting, shortCpu } from "../lib/format";
import { Bar, Card, CardTitle, IconBadge, StatusPill } from "./ui";
import { TerminalButton, useTerminal } from "./Terminal";
import { Gauge } from "./Gauge";
import { Sparkline } from "./Sparkline";

export default function Dashboard({ onNavigate }: { onNavigate: (k: NavKey) => void }) {
  const { stats, history, status } = useStatsStream();
  const [info, setInfo] = useState<Info | null>(null);
  const { openHost } = useTerminal();

  useEffect(() => {
    api<Info>("/system/info")
      .then(setInfo)
      .catch(() => setInfo(null));
  }, []);

  const series = useMemo(
    () => ({
      cpu: history.map((s) => s.cpuPercent),
      mem: history.map((s) => s.memPercent),
      rx: history.map((s) => total(s, "rxPerSec")),
      tx: history.map((s) => total(s, "txPerSec")),
    }),
    [history],
  );

  const disks = stats?.disks ?? [];
  const ifaces = stats?.net ?? [];
  const cores = stats?.perCore ?? [];
  const rx = stats ? total(stats, "rxPerSec") : 0;
  const tx = stats ? total(stats, "txPerSec") : 0;
  const netMax = Math.max(...series.rx, ...series.tx, 1024) * 1.2;
  const state = health(stats);

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="text-sm font-semibold text-ink-3">{greeting()}</p>
          <h1 className="mt-1 truncate text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">
            {info?.hostname ?? "FELDCORE"}
          </h1>
          <p className={`mt-2 text-sm font-semibold ${state.ok ? "text-ink-2" : "text-warn"}`}>
            {state.text}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <Clock />
          <TerminalButton label="Host terminal" onClick={openHost} />
          <StatusPill status={status} />
        </div>
      </header>

      {/* Vital signs */}
      <section className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <Card className="flex flex-col overflow-hidden">
          <CardTitle
            icon={Cpu}
            title="Processor"
            hint={info ? `${info.cpuCores} threads` : undefined}
          />
          <div className="mt-4 flex justify-center">
            <Gauge percent={stats?.cpuPercent ?? 0} label="Usage" />
          </div>
          <p className="mt-3 truncate text-center text-xs font-semibold text-ink-2">
            {info?.cpuModel ? shortCpu(info.cpuModel) : " "}
          </p>
          <div className="-mx-5 -mb-5 mt-auto pt-3">
            <Sparkline data={series.cpu} capacity={HISTORY_SIZE} max={100} />
          </div>
        </Card>

        <Card className="flex flex-col overflow-hidden">
          <CardTitle
            icon={MemoryStick}
            title="Memory"
            hint={stats ? bytes(stats.memTotal) : undefined}
          />
          <div className="mt-4 flex justify-center">
            <Gauge percent={stats?.memPercent ?? 0} label="In use" />
          </div>
          <p className="mt-3 truncate text-center text-xs font-semibold tabular-nums text-ink-2">
            {stats ? `${bytes(stats.memUsed)} of ${bytes(stats.memTotal)}` : " "}
          </p>
          <div className="-mx-5 -mb-5 mt-auto pt-3">
            <Sparkline
              data={series.mem}
              capacity={HISTORY_SIZE}
              max={100}
              color="var(--accent-2)"
            />
          </div>
        </Card>

        <Card className="flex flex-col overflow-hidden">
          <CardTitle
            icon={Network}
            title="Network"
            hint={ifaces.map((n) => n.name).join(", ") || undefined}
          />
          <div className="mt-6 space-y-5">
            <Rate icon={ArrowDown} label="Download" value={bytesPerSec(rx)} tone="text-accent" />
            <Rate icon={ArrowUp} label="Upload" value={bytesPerSec(tx)} tone="text-accent-2" />
          </div>
          <div className="relative -mx-5 -mb-5 mt-auto pt-4">
            <Sparkline data={series.rx} capacity={HISTORY_SIZE} max={netMax} />
            <div className="absolute inset-x-0 bottom-0">
              <Sparkline
                data={series.tx}
                capacity={HISTORY_SIZE}
                max={netMax}
                color="var(--accent-2)"
              />
            </div>
          </div>
        </Card>

        <Card className="flex flex-col">
          <CardTitle icon={Server} title="System" hint={info?.arch} />
          <div className="mt-6">
            <div className="text-[10px] font-bold uppercase tracking-[0.18em] text-ink-3">
              Uptime
            </div>
            <div className="mt-1 text-3xl font-extrabold tabular-nums tracking-tight text-ink">
              {stats ? duration(stats.uptimeSeconds) : "–"}
            </div>
          </div>
          <dl className="mt-6 space-y-2.5 text-xs">
            <Row k="OS" v={info ? `${cap(info.platform)} ${info.platformVersion}` : "–"} />
            <Row k="Kernel" v={info?.kernel ?? "–"} />
            <Row
              k="Load"
              v={
                stats
                  ? `${stats.load1.toFixed(2)} · ${stats.load5.toFixed(2)} · ${stats.load15.toFixed(2)}`
                  : "–"
              }
            />
          </dl>
        </Card>
      </section>

      <section className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Card>
          <CardTitle
            icon={HardDrive}
            title="Storage"
            hint={disks.length ? `${disks.length} volume${disks.length > 1 ? "s" : ""}` : undefined}
          />
          {disks.length ? (
            <ul className="mt-5 space-y-5">
              {disks.map((d) => (
                <li key={d.mountpoint}>
                  <div className="mb-2 flex items-baseline justify-between gap-3">
                    <div className="min-w-0 truncate">
                      <span className="text-sm font-bold text-ink">{d.mountpoint}</span>
                      <span className="ml-2 text-xs font-medium text-ink-3">
                        {d.device} · {d.fstype}
                      </span>
                    </div>
                    <span className="shrink-0 text-xs font-bold tabular-nums text-ink-2">
                      {bytes(d.totalBytes - d.usedBytes)} free
                    </span>
                  </div>
                  <Bar percent={d.usedPercent} />
                  <div className="mt-1.5 text-[11px] font-medium tabular-nums text-ink-3">
                    {bytes(d.usedBytes)} of {bytes(d.totalBytes)} used ·{" "}
                    {d.usedPercent.toFixed(0)}%
                  </div>
                </li>
              ))}
            </ul>
          ) : (
            <Empty label="No volumes reported yet" />
          )}
        </Card>

        <Card>
          <CardTitle icon={LayoutGrid} title="Threads" hint="Load per thread" />
          {cores.length ? (
            <div
              className="mt-5 grid gap-2"
              style={{
                gridTemplateColumns: `repeat(${Math.min(8, cores.length)}, minmax(0, 1fr))`,
              }}
            >
              {cores.map((c, i) => (
                <div
                  key={i}
                  title={`Thread ${i}: ${c.toFixed(0)}%`}
                  className="flex aspect-square items-center justify-center rounded-xl text-[11px] font-bold tabular-nums transition-colors duration-500"
                  style={{
                    background: `color-mix(in oklab, var(--accent) ${Math.round(
                      6 + c * 0.94,
                    )}%, var(--track))`,
                    color: c > 50 ? "#fff" : "var(--ink-2)",
                  }}
                >
                  {Math.round(c)}
                </div>
              ))}
            </div>
          ) : (
            <Empty label="Measuring…" />
          )}
        </Card>
      </section>

      <section>
        <h2 className="mb-3 px-1 text-[11px] font-bold uppercase tracking-[0.2em] text-ink-3">
          Modules
        </h2>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {NAV.filter((n) => n.key !== "dashboard").map((item) => (
            <button
              key={item.key}
              onClick={() => onNavigate(item.key)}
              className="glass flex items-center gap-4 rounded-3xl p-4 text-left transition-transform duration-200 hover:-translate-y-0.5"
            >
              <IconBadge icon={item.icon} />
              <span className="min-w-0">
                <span className="block text-sm font-bold text-ink">{item.label}</span>
                <span className="block truncate text-xs font-medium text-ink-3">
                  {item.ready ? item.desc : "Coming soon"}
                </span>
              </span>
            </button>
          ))}
        </div>
      </section>
    </div>
  );
}

function total(s: Stats, key: "rxPerSec" | "txPerSec"): number {
  return (s.net ?? []).reduce((sum, n) => sum + n[key], 0);
}

function cap(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
}

/** One-line plain-language summary of machine health. */
function health(s: Stats | null): { ok: boolean; text: string } {
  if (!s) return { ok: true, text: "Establishing link…" };
  const issues: string[] = [];
  if (s.cpuPercent >= 90) issues.push("processor under heavy load");
  if (s.memPercent >= 90) issues.push("memory nearly full");
  const full = (s.disks ?? []).find((d) => d.usedPercent >= 90);
  if (full) issues.push(`${full.mountpoint} almost out of space`);
  if (!issues.length) return { ok: true, text: "All systems nominal." };
  return { ok: false, text: `Attention: ${issues.join(", ")}.` };
}

function Clock() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(t);
  }, []);
  return (
    <div className="hidden text-right sm:block">
      <div className="text-lg font-extrabold leading-none tabular-nums text-ink">
        {now.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
      </div>
      <div className="mt-1 text-[11px] font-semibold text-ink-3">
        {now.toLocaleDateString([], { weekday: "short", day: "numeric", month: "short" })}
      </div>
    </div>
  );
}

function Rate({
  icon: Icon,
  label,
  value,
  tone,
}: {
  icon: LucideIcon;
  label: string;
  value: string;
  tone: string;
}) {
  return (
    <div className="flex items-center gap-3">
      <span className={`flex h-9 w-9 items-center justify-center rounded-xl bg-track ${tone}`}>
        <Icon size={17} strokeWidth={2.5} />
      </span>
      <div>
        <div className="text-[10px] font-bold uppercase tracking-[0.18em] text-ink-3">{label}</div>
        <div className="text-lg font-extrabold leading-tight tabular-nums text-ink">{value}</div>
      </div>
    </div>
  );
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <dt className="shrink-0 font-semibold text-ink-3">{k}</dt>
      <dd className="truncate font-bold tabular-nums text-ink" title={v}>
        {v}
      </dd>
    </div>
  );
}

function Empty({ label }: { label: string }) {
  return <div className="py-8 text-center text-sm font-medium text-ink-3">{label}</div>;
}
