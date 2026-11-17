import { useEffect, useMemo, useState } from "react";
import { HeartPulse } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { Card, CardTitle } from "../ui";
import { Alert, Button, TextField } from "../form";
import { Sparkline } from "../Sparkline";
import { PageHelp } from "../help/PageHelp";
import { useHelpAnchor } from "../../lib/appnav";

export type HealthLevel = "ok" | "warn" | "crit" | "unknown";

export interface HealthCheck {
  id: string;
  category: string;
  name: string;
  level: HealthLevel;
  summary: string;
  value?: number;
  unit?: string;
  attributes?: { name: string; value?: string; raw?: string; threshold?: number }[];
}

export interface HealthReport {
  overall: HealthLevel;
  summary: string;
  checkedAt: string;
  checks: HealthCheck[] | null;
}

interface Point {
  t: number;
  cpu: number;
  mem: number;
  load: number;
  disk?: Record<string, number>;
  temp?: Record<string, number>;
}

interface Thresholds {
  cpuWarn: number;
  cpuCrit: number;
  memWarn: number;
  memCrit: number;
  loadWarn: number;
  loadCrit: number;
  diskWarn: number;
  diskCrit: number;
  cpuTempWarn: number;
  cpuTempCrit: number;
  diskTempWarn: number;
  diskTempCrit: number;
  gpuTempWarn: number;
  gpuTempCrit: number;
  certWarnDays: number;
  certCritDays: number;
  restartWarn: number;
  restartCrit: number;
  restartWindowMin: number;
}

const LEVEL: Record<HealthLevel, { label: string; cls: string }> = {
  ok: { label: "OK", cls: "bg-ok/15 text-ok" },
  warn: { label: "Warning", cls: "bg-warn/15 text-warn" },
  crit: { label: "Critical", cls: "bg-bad/15 text-bad" },
  unknown: { label: "Unknown", cls: "bg-track text-ink-3" },
};

const GROUPS: { id: string; title: string }[] = [
  { id: "cpu", title: "Processor" },
  { id: "memory", title: "Memory" },
  { id: "load", title: "Load" },
  { id: "space", title: "Storage" },
  { id: "temp", title: "Temperatures" },
  { id: "smart", title: "SMART" },
  { id: "container", title: "Containers" },
  { id: "vm", title: "Virtual machines" },
  { id: "systemd", title: "Services" },
  { id: "cert", title: "Certificates" },
  { id: "tunnel", title: "Tunnels" },
];

const MARKS: { key: keyof Thresholds; label: string; hint: string }[] = [
  { key: "cpuWarn", label: "Processor warning", hint: "percent" },
  { key: "cpuCrit", label: "Processor critical", hint: "percent" },
  { key: "memWarn", label: "Memory warning", hint: "percent" },
  { key: "memCrit", label: "Memory critical", hint: "percent" },
  { key: "loadWarn", label: "Load warning", hint: "per thread" },
  { key: "loadCrit", label: "Load critical", hint: "per thread" },
  { key: "diskWarn", label: "Disk warning", hint: "percent full" },
  { key: "diskCrit", label: "Disk critical", hint: "percent full" },
  { key: "cpuTempWarn", label: "CPU temperature warning", hint: "°C" },
  { key: "cpuTempCrit", label: "CPU temperature critical", hint: "°C" },
  { key: "diskTempWarn", label: "Disk temperature warning", hint: "°C" },
  { key: "diskTempCrit", label: "Disk temperature critical", hint: "°C" },
  { key: "gpuTempWarn", label: "GPU temperature warning", hint: "°C" },
  { key: "gpuTempCrit", label: "GPU temperature critical", hint: "°C" },
  { key: "certWarnDays", label: "Certificate warning", hint: "days left" },
  { key: "certCritDays", label: "Certificate critical", hint: "days left" },
  { key: "restartWarn", label: "Restart warning", hint: "restarts in the window" },
  { key: "restartCrit", label: "Restart critical", hint: "restarts in the window" },
  { key: "restartWindowMin", label: "Restart window", hint: "minutes" },
];

export function LevelPill({ level }: { level: HealthLevel }) {
  const meta = LEVEL[level] ?? LEVEL.unknown;
  return (
    <span className={`inline-flex rounded-full px-2.5 py-1 text-[10px] font-bold uppercase tracking-wider ${meta.cls}`}>
      {meta.label}
    </span>
  );
}

export function HealthStrip({
  report,
  onOpen,
}: {
  report: HealthReport | null;
  onOpen: () => void;
}) {
  const level = report?.overall ?? "unknown";
  const checks = (report?.checks ?? []).filter((c) => c.level === "crit" || c.level === "warn").slice(0, 3);
  return (
    <button onClick={onOpen} className="glass w-full rounded-3xl p-5 text-left transition-transform duration-200 hover:-translate-y-0.5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <CardTitle icon={HeartPulse} title="Health" hint={report?.checkedAt ? when(report.checkedAt) : "Waiting for the first check"} />
        <LevelPill level={level} />
      </div>
      <p className="mt-3 text-sm font-semibold text-ink-2">
        {report?.summary || "FELDCORE is reading disks, temperatures, and services."}
      </p>
      {checks.length > 0 && (
        <ul className="mt-3 space-y-1.5">
          {checks.map((c) => (
            <li key={c.id} className="flex items-center gap-2 text-xs font-semibold text-ink">
              <LevelPill level={c.level} />
              <span className="truncate">{c.summary}</span>
            </li>
          ))}
        </ul>
      )}
    </button>
  );
}

export default function HealthPage() {
  useHelpAnchor("health");
  const [report, setReport] = useState<HealthReport | null>(null);
  const [points, setPoints] = useState<Point[]>([]);
  const [marks, setMarks] = useState<Thresholds | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState("");

  const load = () => {
    api<HealthReport>("/health/status")
      .then(setReport)
      .catch((e) => setError(errorMessage(e)));
    api<{ points: Point[] }>("/health/history?hours=24")
      .then((h) => setPoints(h.points ?? []))
      .catch(() => undefined);
  };

  useEffect(() => {
    load();
    api<{ thresholds: Thresholds }>("/health/thresholds")
      .then((t) => setMarks(t.thresholds))
      .catch((e) => setError(errorMessage(e)));
    const timer = setInterval(load, 30_000);
    return () => clearInterval(timer);
  }, []);

  const groups = useMemo(() => {
    const checks = report?.checks ?? [];
    const known = new Set(GROUPS.map((g) => g.id));
    const rows = GROUPS.map((g) => ({ ...g, checks: checks.filter((c) => c.category === g.id) })).filter(
      (g) => g.checks.length > 0,
    );
    const other = checks.filter((c) => !known.has(c.category));
    if (other.length) rows.push({ id: "other", title: "Other", checks: other });
    return rows;
  }, [report]);

  const charts = useMemo(() => {
    const diskKey = fullest(points, (p) => p.disk);
    const tempKey = fullest(points, (p) => p.temp);
    return [
      { title: "Processor", data: points.map((p) => p.cpu), max: 100, color: "var(--accent)" },
      { title: "Memory", data: points.map((p) => p.mem), max: 100, color: "var(--accent-2)" },
      { title: "Load per thread", data: points.map((p) => p.load), color: "var(--ok)" },
      diskKey
        ? { title: `Disk ${diskKey}`, data: points.map((p) => p.disk?.[diskKey] ?? 0), max: 100, color: "var(--warn)" }
        : null,
      tempKey
        ? { title: tempKey, data: points.map((p) => p.temp?.[tempKey] ?? 0), color: "var(--bad)" }
        : null,
    ].filter((c): c is { title: string; data: number[]; max?: number; color: string } => c !== null);
  }, [points]);

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">Health</h1>
            <PageHelp topic="notifications-health" />
          </div>
          <p className="mt-2 max-w-2xl text-sm font-semibold text-ink-2">
            Disks, heat, space, load, containers, virtual machines, services, certificates, and tunnels. A missing
            tool is unknown, not a failure.
          </p>
        </div>
        <div className="flex items-center gap-3">
          {report && <LevelPill level={report.overall || "unknown"} />}
          <Button
            variant="primary"
            loading={busy === "check"}
            onClick={() => {
              setBusy("check");
              setError(null);
              api<HealthReport>("/health/check", { method: "POST" })
                .then((r) => {
                  setReport(r);
                  setNotice("Checked.");
                  load();
                })
                .catch((e) => setError(errorMessage(e)))
                .finally(() => setBusy(""));
            }}
          >
            Check now
          </Button>
        </div>
      </header>

      {error && <Alert>{error}</Alert>}
      {notice && <Alert tone="info">{notice}</Alert>}

      <Card>
        <CardTitle
          icon={HeartPulse}
          title={report?.summary || "No health check yet."}
          hint={report?.checkedAt ? when(report.checkedAt) : undefined}
        />
        <div className="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {charts.map((c) => (
            <div key={c.title} className="rounded-2xl border border-line p-3">
              <div className="mb-2 text-xs font-bold text-ink-2">{c.title}</div>
              {c.data.filter((n) => n !== undefined).length < 2 ? (
                <p className="py-4 text-xs font-medium text-ink-3">A graph appears after the next sample.</p>
              ) : (
                <Sparkline data={c.data} capacity={Math.max(c.data.length, 2)} max={c.max} color={c.color} height={64} />
              )}
            </div>
          ))}
        </div>
      </Card>

      {groups.map((g) => (
        <Card key={g.id}>
          <CardTitle icon={HeartPulse} title={g.title} hint={`${g.checks.length}`} />
          <ul className="mt-4 divide-y divide-line">
            {g.checks.map((c) => (
              <li key={c.id} className="flex flex-wrap items-start justify-between gap-3 py-3">
                <div className="min-w-0">
                  <div className="text-sm font-bold text-ink">{c.name}</div>
                  <p className="mt-1 text-xs font-medium leading-relaxed text-ink-2">{c.summary}</p>
                  {c.attributes && c.attributes.length > 0 && (
                    <details className="mt-2">
                      <summary className="cursor-pointer text-xs font-bold text-ink-3">SMART attributes</summary>
                      <ul className="mt-2 grid gap-1 sm:grid-cols-2">
                        {c.attributes.map((a) => (
                          <li key={a.name} className="text-[11px] font-medium text-ink-2">
                            {a.name}
                            {a.raw ? `: ${a.raw}` : a.value ? `: ${a.value}` : ""}
                          </li>
                        ))}
                      </ul>
                    </details>
                  )}
                </div>
                <LevelPill level={c.level} />
              </li>
            ))}
          </ul>
        </Card>
      ))}

      <Card>
        <div id="health-thresholds">
        <CardTitle icon={HeartPulse} title="Thresholds" hint="Warning, then critical" />
        <p className="mt-3 text-sm font-medium text-ink-2">
          A warning mark has to sit below its critical mark. Certificate warning is a larger number of days than
          the critical window. These are saved on this server.
        </p>
        {marks && (
          <>
            <div className="mt-4 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {MARKS.map((m) => (
                <TextField
                  key={m.key}
                  label={m.label}
                  hint={m.hint}
                  type="number"
                  min={0}
                  step={m.key.startsWith("load") ? "0.1" : "1"}
                  value={marks[m.key]}
                  onChange={(e) => setMarks({ ...marks, [m.key]: Number(e.target.value) })}
                />
              ))}
            </div>
            <div className="mt-4">
              <Button
                variant="primary"
                loading={busy === "save"}
                onClick={() => {
                  setBusy("save");
                  setError(null);
                  api<{ thresholds: Thresholds }>("/health/thresholds", { method: "PUT", body: marks })
                    .then((t) => {
                      setMarks(t.thresholds);
                      setNotice("Thresholds saved.");
                    })
                    .catch((e) => setError(errorMessage(e)))
                    .finally(() => setBusy(""));
                }}
              >
                Save thresholds
              </Button>
            </div>
          </>
        )}
        </div>
      </Card>
    </div>
  );
}

function when(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function fullest(points: Point[], pick: (p: Point) => Record<string, number> | undefined): string {
  const last = [...points].reverse().find((p) => pick(p) && Object.keys(pick(p)!).length > 0);
  if (!last) return "";
  const map = pick(last)!;
  return Object.keys(map).sort((a, b) => (map[b] ?? 0) - (map[a] ?? 0))[0] ?? "";
}
