import { useCallback, useEffect, useMemo, useState } from "react";
import { HardDrive, Plus, RefreshCw } from "lucide-react";
import { api, errorMessage } from "../../lib/api";
import { bytes } from "../../lib/format";
import { Bar, Card, CardTitle } from "../ui";
import { Alert, Button, Modal, SelectField, TextField } from "../form";
import { PageHelp } from "../help/PageHelp";
import { useHelpAnchor } from "../../lib/appnav";

interface Smart {
  available: boolean;
  healthy: string;
  tempC?: number;
  reallocated?: number;
  pending?: number;
  message?: string;
}

interface Disk {
  id: string;
  name: string;
  path: string;
  byId?: string;
  model?: string;
  serial?: string;
  confirm?: string;
  confirmKind?: string;
  sizeBytes: number;
  type: string;
  tempC?: number;
  smart: Smart;
  usedFor: string;
  fsType?: string;
  pool?: string;
  os: boolean;
  eligible: boolean;
  blocked?: string;
  warnings?: string[];
  rotational: boolean;
}

interface Member {
  id: string;
  serial?: string;
  byId?: string;
  path: string;
  sizeBytes: number;
}

interface Pool {
  name: string;
  fs: string;
  layout: string;
  uuid: string;
  mount: string;
  members: Member[];
  cache: boolean;
  mounted: boolean;
  totalBytes: number;
  usedBytes: number;
  usedPercent: number;
  health: string;
  healthDetail?: string;
  bootMount: boolean;
}

interface LayoutInfo {
  fs: string;
  layout: string;
  label: string;
  min: number;
  max?: number;
  even?: boolean;
  available: boolean;
}

interface Schedule {
  pool: string;
  kind: string;
  enabled: boolean;
  everyHours: number;
  lastResult?: string;
}

interface SpinRule {
  id: string;
  minutes: number;
  applied: boolean;
  note?: string;
}

interface ShareRow {
  name: string;
  pool?: string;
  path: string;
}

interface Job {
  id: string;
  name: string;
  label: string;
  state: "queued" | "running" | "succeeded" | "failed";
  step: string;
  error?: string;
}

interface View {
  privileged: boolean;
  caps: {
    btrfs: boolean;
    xfs: boolean;
    xfsScrub: boolean;
    zfs: boolean;
    smart: boolean;
    hdparm: boolean;
    trim: boolean;
    layouts: LayoutInfo[];
  };
  disks: Disk[];
  pools: Pool[];
  schedules: Schedule[];
  spinDown: SpinRule[];
  shares: ShareRow[];
  cachePool?: string;
  cachePath?: string;
  mountRoot: string;
  jobs: Job[];
}

const SPIN = [
  { value: "0", label: "Stay spinning" },
  { value: "5", label: "5 minutes" },
  { value: "10", label: "10 minutes" },
  { value: "20", label: "20 minutes" },
  { value: "30", label: "30 minutes" },
  { value: "60", label: "1 hour" },
  { value: "120", label: "2 hours" },
];

const KIND_LABEL: Record<string, string> = { scrub: "Scrub", balance: "Balance", trim: "Trim" };

function typeLabel(t: string): string {
  switch (t) {
    case "hdd":
      return "Hard disk";
    case "ssd":
      return "SSD";
    case "nvme":
      return "NVMe";
    case "usb":
      return "USB";
    case "virtual":
      return "Virtual";
    case "loop":
      return "Loop";
    default:
      return t || "Disk";
  }
}

function smartLabel(s: Smart): { text: string; cls: string } {
  if (s.healthy === "passed") return { text: "Passed", cls: "text-ok" };
  if (s.healthy === "failed") return { text: "Failed", cls: "text-bad" };
  return { text: "Unknown", cls: "text-ink-3" };
}

function healthLabel(h: string): { text: string; cls: string } {
  switch (h) {
    case "healthy":
      return { text: "Healthy", cls: "bg-ok/15 text-ok" };
    case "degraded":
      return { text: "Degraded", cls: "bg-warn/15 text-warn" };
    case "faulted":
      return { text: "Faulted", cls: "bg-bad/15 text-bad" };
    case "unmounted":
      return { text: "Unmounted", cls: "bg-track text-ink-3" };
    default:
      return { text: "Unknown", cls: "bg-track text-ink-2" };
  }
}

function jobLabel(state: Job["state"]): { text: string; cls: string } {
  switch (state) {
    case "queued":
      return { text: "Queued", cls: "bg-track text-ink-2" };
    case "running":
      return { text: "Running", cls: "bg-accent/15 text-accent" };
    case "succeeded":
      return { text: "Finished", cls: "bg-ok/15 text-ok" };
    default:
      return { text: "Failed", cls: "bg-bad/12 text-bad" };
  }
}

function diskTitle(d: Disk): string {
  return d.model || typeLabel(d.type);
}

function confirmHint(d: Disk): string {
  if (d.confirmKind === "by-id") {
    return `This disk has no hardware serial. Type its by-id name, ${d.confirm}.`;
  }
  return `Type the serial ${d.confirm}.`;
}

export default function StoragePage() {
  useHelpAnchor("storage");
  const [view, setView] = useState<View | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [creating, setCreating] = useState(false);
  const [acting, setActing] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setView(await api<View>("/storage"));
      setLoadError(null);
    } catch (e) {
      setLoadError(errorMessage(e));
    }
  }, []);

  const running = view?.jobs.some((j) => j.state === "queued" || j.state === "running") ?? false;

  useEffect(() => {
    void load();
    const t = setInterval(() => void load(), running ? 3000 : 8000);
    return () => clearInterval(t);
  }, [load, running]);

  const run = async (label: string, fn: () => Promise<void>) => {
    setBusy(true);
    setNotice(null);
    try {
      await fn();
      setNotice(label);
      await load();
    } catch (e) {
      setLoadError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">Storage</h1>
            <PageHelp topic="disk-management" />
          </div>
          <p className="mt-2 max-w-2xl text-sm font-semibold text-ink-2">
            Disks on this server, and the pools they belong to. Nothing is formatted until you type each disk's serial.
          </p>
        </div>
        <div className="flex gap-2">
          <Button onClick={() => void load()} disabled={busy}>
            <RefreshCw size={15} />
            Refresh
          </Button>
          <Button variant="primary" onClick={() => setCreating(true)} disabled={!view || view.caps.layouts.every((l) => !l.available)}>
            <Plus size={16} strokeWidth={2.75} />
            New pool
          </Button>
        </div>
      </header>

      {loadError && <Alert tone="error">{loadError}</Alert>}
      {notice && <Alert tone="info">{notice}</Alert>}
      {view && !view.privileged && (
        <Alert tone="warn">FELDCORE is not running as root, so it can list disks but cannot format, mount or spin them down.</Alert>
      )}
      {view && (
        <p className="text-xs font-semibold text-ink-3">
          Tools: btrfs {view.caps.btrfs ? "ready" : "not installed"}, XFS {view.caps.xfs ? "ready" : "not installed"}, ZFS{" "}
          {view.caps.zfs ? "ready" : "not available"}, SMART {view.caps.smart ? "ready" : "not installed"}. Pools mount under {view.mountRoot}.
        </p>
      )}

      <Card>
        <CardTitle icon={HardDrive} title="Disks" hint={view ? `${view.disks.length} found` : "Looking"} />
        {!view ? (
          <p className="mt-4 text-sm font-medium text-ink-3">Reading the disk list…</p>
        ) : view.disks.length === 0 ? (
          <p className="mt-4 text-sm font-medium text-ink-3">No whole disks were found.</p>
        ) : (
          <div className="mt-4 overflow-x-auto">
            <table className="w-full min-w-[52rem] text-left text-sm">
              <thead className="text-[11px] font-bold uppercase tracking-wider text-ink-3">
                <tr>
                  <th className="pb-2 pr-3 font-bold">Disk</th>
                  <th className="pb-2 pr-3 font-bold">Serial</th>
                  <th className="pb-2 pr-3 font-bold">Size</th>
                  <th className="pb-2 pr-3 font-bold">Type</th>
                  <th className="pb-2 pr-3 font-bold">Temp</th>
                  <th className="pb-2 pr-3 font-bold">SMART</th>
                  <th className="pb-2 pr-3 font-bold">By-id</th>
                  <th className="pb-2 font-bold">Used for</th>
                </tr>
              </thead>
              <tbody>
                {view.disks.map((d) => {
                  const sm = smartLabel(d.smart);
                  return (
                    <tr key={d.id} className="border-t border-line align-top">
                      <td className="py-3 pr-3">
                        <div className="font-bold text-ink">{diskTitle(d)}</div>
                        <div className="text-xs font-medium text-ink-3">{d.path}</div>
                      </td>
                      <td className="py-3 pr-3 font-medium text-ink-2">{d.serial || "—"}</td>
                      <td className="py-3 pr-3 font-medium text-ink">{bytes(d.sizeBytes)}</td>
                      <td className="py-3 pr-3 font-medium text-ink-2">{typeLabel(d.type)}</td>
                      <td className="py-3 pr-3 font-medium text-ink-2">{d.tempC != null ? `${d.tempC}°C` : "—"}</td>
                      <td className="py-3 pr-3">
                        <div className={`font-bold ${sm.cls}`}>{sm.text}</div>
                        {d.smart.message && <div className="max-w-[14rem] text-xs font-medium text-ink-3">{d.smart.message}</div>}
                        {d.smart.reallocated != null && d.smart.reallocated > 0 && (
                          <div className="text-xs font-medium text-warn">{d.smart.reallocated} reallocated</div>
                        )}
                      </td>
                      <td className="max-w-[12rem] py-3 pr-3 break-all text-xs font-medium text-ink-3">{d.byId || "—"}</td>
                      <td className="py-3 font-semibold text-ink">{d.usedFor}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <section className="space-y-4">
        <h2 id="storage-pools" className="text-lg font-extrabold text-ink">Pools</h2>
        {view && view.pools.length === 0 && (
          <Card>
            <p className="text-sm font-medium text-ink-2">
              No pools yet. A pool is a filesystem on one or more disks. Creating one erases those disks. FELDCORE waits until you type each serial.
            </p>
          </Card>
        )}
        {view?.pools.map((p) => (
          <PoolCard
            key={p.name}
            pool={p}
            view={view}
            busy={busy}
            acting={acting}
            onAct={(key, label, fn) => {
              setActing(key);
              void run(label, fn).finally(() => setActing(null));
            }}
          />
        ))}
      </section>

      {view && <SharesCard view={view} busy={busy} onChange={(label, fn) => void run(label, fn)} />}
      {view && <SpinCard view={view} busy={busy} onChange={(label, fn) => void run(label, fn)} />}
      {view && view.jobs.length > 0 && <JobsCard jobs={view.jobs} />}

      {creating && view && (
        <CreateDialog
          view={view}
          busy={busy}
          onClose={() => setCreating(false)}
          onCreate={(body) =>
            run("The pool was created and mounted by its UUID.", async () => {
              await api("/storage/pools", { method: "POST", body });
              setCreating(false);
            })
          }
        />
      )}
    </div>
  );
}

function PoolCard({
  pool,
  view,
  busy,
  acting,
  onAct,
}: {
  pool: Pool;
  view: View;
  busy: boolean;
  acting: string | null;
  onAct: (key: string, label: string, fn: () => Promise<void>) => void;
}) {
  const [mode, setMode] = useState<"add" | "replace" | "forget" | "schedule" | null>(null);
  const health = healthLabel(pool.health);
  const canBalance = pool.fs === "btrfs";
  const canScrub = pool.fs !== "xfs" || view.caps.xfsScrub;
  const key = (action: string) => `${pool.name}:${action}`;

  return (
    <Card>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-lg font-extrabold text-ink">{pool.name}</h3>
            <span className={`rounded-full px-2.5 py-0.5 text-[11px] font-bold ${health.cls}`}>{health.text}</span>
            {pool.cache && <span className="rounded-full bg-accent/15 px-2.5 py-0.5 text-[11px] font-bold text-accent">Cache</span>}
          </div>
          <p className="mt-1 text-xs font-semibold text-ink-3">
            {pool.fs} {pool.layout} · {pool.mounted ? pool.mount : "not mounted"} · UUID {pool.uuid || "—"}
            {pool.bootMount ? " · mounts on boot" : ""}
          </p>
          {pool.healthDetail && <p className="mt-1 text-xs font-medium text-ink-2">{pool.healthDetail}</p>}
        </div>
        <div className="text-right">
          <div className="text-sm font-bold text-ink">
            {pool.mounted ? `${bytes(pool.usedBytes)} of ${bytes(pool.totalBytes)}` : "—"}
          </div>
          <div className="text-xs font-medium text-ink-3">{pool.mounted ? `${Math.round(pool.usedPercent)}% used` : "Unmounted"}</div>
        </div>
      </div>
      {pool.mounted && (
        <div className="mt-3">
          <Bar percent={pool.usedPercent} />
        </div>
      )}
      <ul className="mt-3 space-y-1 text-xs font-medium text-ink-2">
        {pool.members.map((m) => (
          <li key={m.id}>
            {m.path}
            {m.serial ? ` · ${m.serial}` : ""}
          </li>
        ))}
      </ul>
      <div className="mt-4 flex flex-wrap gap-2">
        {pool.mounted ? (
          <Button size="sm" disabled={busy} loading={acting === key("unmount")} onClick={() => onAct(key("unmount"), `${pool.name} was unmounted.`, () => api(`/storage/pools/${pool.name}/unmount`, { method: "POST" }))}>
            Unmount
          </Button>
        ) : (
          <Button size="sm" disabled={busy} loading={acting === key("mount")} onClick={() => onAct(key("mount"), `${pool.name} was mounted by its UUID.`, () => api(`/storage/pools/${pool.name}/mount`, { method: "POST" }))}>
            Mount
          </Button>
        )}
        {canScrub && (
          <Button size="sm" disabled={busy || !pool.mounted} onClick={() => onAct(key("scrub"), `Scrub of ${pool.name} was queued.`, () => api(`/storage/pools/${pool.name}/jobs`, { method: "POST", body: { kind: "scrub" } }))}>
            Scrub
          </Button>
        )}
        {canBalance && (
          <Button size="sm" disabled={busy || !pool.mounted} onClick={() => onAct(key("balance"), `Balance of ${pool.name} was queued.`, () => api(`/storage/pools/${pool.name}/jobs`, { method: "POST", body: { kind: "balance" } }))}>
            Balance
          </Button>
        )}
        {view.caps.trim && (
          <Button size="sm" disabled={busy || !pool.mounted} onClick={() => onAct(key("trim"), `Trim of ${pool.name} was queued.`, () => api(`/storage/pools/${pool.name}/jobs`, { method: "POST", body: { kind: "trim" } }))}>
            Trim
          </Button>
        )}
        <Button size="sm" disabled={busy || pool.fs === "xfs"} onClick={() => setMode("add")}>
          Add disk
        </Button>
        <Button size="sm" disabled={busy || pool.fs === "xfs"} onClick={() => setMode("replace")}>
          Replace disk
        </Button>
        <Button size="sm" disabled={busy} onClick={() => setMode("schedule")}>
          Schedule
        </Button>
        {pool.cache ? (
          <Button size="sm" disabled={busy} onClick={() => onAct(key("cache"), "The cache pool was cleared. Files were left where they are.", () => api("/storage/cache", { method: "POST", body: { pool: "" } }))}>
            Clear cache
          </Button>
        ) : (
          <Button size="sm" disabled={busy || !pool.mounted} onClick={() => onAct(key("cache"), `${pool.name} now holds appdata. Existing files were not moved.`, () => api("/storage/cache", { method: "POST", body: { pool: pool.name } }))}>
            Use as cache
          </Button>
        )}
        <Button size="sm" variant="danger" disabled={busy} onClick={() => setMode("forget")}>
          Forget
        </Button>
      </div>

      {mode === "add" && (
        <DiskConfirm
          title={`Add a disk to ${pool.name}`}
          warning="Adding a disk erases that disk. The pool's existing files stay. A btrfs RAID pool then starts a balance so the new disk is used."
          submit="Erase and add"
          disks={view.disks.filter((d) => d.eligible)}
          busy={busy}
          onClose={() => setMode(null)}
          onSubmit={(disk, confirm) =>
            onAct(key("add"), `A disk was added to ${pool.name}.`, async () => {
              await api(`/storage/pools/${pool.name}/disks`, { method: "POST", body: { disk, confirm } });
              setMode(null);
            })
          }
        />
      )}
      {mode === "replace" && (
        <ReplaceDialog
          pool={pool}
          disks={view.disks.filter((d) => d.eligible)}
          busy={busy}
          onClose={() => setMode(null)}
          onSubmit={(oldId, disk, confirm) =>
            onAct(key("replace"), `Replacing a disk in ${pool.name}.`, async () => {
              await api(`/storage/pools/${pool.name}/replace`, { method: "POST", body: { old: oldId, disk, confirm } });
              setMode(null);
            })
          }
        />
      )}
      {mode === "forget" && (
        <ForgetDialog
          pool={pool}
          busy={busy}
          onClose={() => setMode(null)}
          onSubmit={(confirm) =>
            onAct(key("forget"), `${pool.name} was forgotten. The filesystem was left on the disks.`, async () => {
              await api(`/storage/pools/${pool.name}/destroy`, { method: "POST", body: { confirm } });
              setMode(null);
            })
          }
        />
      )}
      {mode === "schedule" && (
        <ScheduleDialog
          pool={pool}
          view={view}
          busy={busy}
          onClose={() => setMode(null)}
          onSubmit={(kind, enabled, everyHours) =>
            onAct(key("schedule"), `The ${kind} schedule for ${pool.name} was saved.`, async () => {
              await api(`/storage/pools/${pool.name}/schedule`, { method: "PUT", body: { kind, enabled, everyHours } });
              setMode(null);
            })
          }
        />
      )}
    </Card>
  );
}

function CreateDialog({
  view,
  busy,
  onClose,
  onCreate,
}: {
  view: View;
  busy: boolean;
  onClose: () => void;
  onCreate: (body: { name: string; fs: string; layout: string; disks: string[]; confirm: string[] }) => void;
}) {
  const layouts = view.caps.layouts.filter((l) => l.available);
  const [choice, setChoice] = useState(layouts[0] ? `${layouts[0].fs}:${layouts[0].layout}` : "");
  const spec = layouts.find((l) => `${l.fs}:${l.layout}` === choice);
  const [name, setName] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [typed, setTyped] = useState<Record<string, string>>({});
  const chosen = picked.map((id) => view.disks.find((d) => d.id === id)).filter((d): d is Disk => !!d);
  const countOk = !!spec && picked.length >= spec.min && (spec.max ? picked.length <= spec.max : true) && (!spec.even || picked.length % 2 === 0);
  const confirmsOk = chosen.every((d) => (typed[d.id] ?? "").trim() !== "");

  return (
    <Modal
      title="Create a pool"
      size="lg"
      onClose={onClose}
      onSubmit={() => {
        if (!spec || !countOk || !confirmsOk) return;
        const [fs, layout] = choice.split(":");
        onCreate({
          name: name.trim(),
          fs,
          layout,
          disks: chosen.map((d) => d.id),
          confirm: chosen.map((d) => typed[d.id].trim()),
        });
      }}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="danger" loading={busy} disabled={!spec || !name.trim() || !countOk || !confirmsOk}>
            Erase and create
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Alert tone="error">
          This erases every selected disk. The files on them are destroyed and FELDCORE cannot bring them back. Nothing is formatted until the serial you type matches that disk. FELDCORE never formats a disk by itself.
        </Alert>
        <TextField label="Pool name" value={name} onChange={(e) => setName(e.target.value)} placeholder="data" autoComplete="off" hint="A lower-case letter, then letters, numbers or hyphens." />
        <SelectField label="Filesystem and layout" value={choice} onChange={(e) => setChoice(e.target.value)} hint={spec ? layoutHint(spec) : "No filesystem tools are installed."}>
          {layouts.map((l) => (
            <option key={`${l.fs}:${l.layout}`} value={`${l.fs}:${l.layout}`}>
              {l.fs} {l.label}
            </option>
          ))}
        </SelectField>
        <fieldset className="space-y-2">
          <legend className="mb-2 text-xs font-bold text-ink-2">Disks</legend>
          {view.disks.map((d) => (
            <label key={d.id} className={`flex items-start gap-3 rounded-2xl border border-line px-3 py-2.5 ${d.eligible ? "" : "opacity-60"}`}>
              <input
                type="checkbox"
                className="mt-1"
                disabled={!d.eligible}
                checked={picked.includes(d.id)}
                onChange={(e) => {
                  setPicked((cur) => (e.target.checked ? [...cur, d.id] : cur.filter((id) => id !== d.id)));
                  if (!e.target.checked) {
                    setTyped((cur) => {
                      const next = { ...cur };
                      delete next[d.id];
                      return next;
                    });
                  }
                }}
              />
              <span className="min-w-0">
                <span className="block text-sm font-bold text-ink">
                  {diskTitle(d)} · {bytes(d.sizeBytes)} · {d.path}
                </span>
                <span className="block text-xs font-medium text-ink-3">
                  {d.blocked || d.usedFor}
                  {d.warnings?.length ? ` · ${d.warnings.join(" ")}` : ""}
                </span>
              </span>
            </label>
          ))}
        </fieldset>
        {chosen.map((d) => (
          <TextField
            key={d.id}
            label={d.confirmKind === "by-id" ? `By-id name for ${diskTitle(d)}` : `Serial for ${diskTitle(d)}`}
            hint={confirmHint(d)}
            value={typed[d.id] ?? ""}
            onChange={(e) => setTyped((cur) => ({ ...cur, [d.id]: e.target.value }))}
            autoComplete="off"
            autoCorrect="off"
            spellCheck={false}
            placeholder="Type it here"
          />
        ))}
      </div>
    </Modal>
  );
}

function layoutHint(spec: LayoutInfo): string {
  const bits = [`At least ${spec.min} disk${spec.min === 1 ? "" : "s"}.`];
  if (spec.max) bits.push(`At most ${spec.max}.`);
  if (spec.even) bits.push("An even number.");
  return bits.join(" ");
}

function DiskConfirm({
  title,
  warning,
  submit,
  disks,
  busy,
  onClose,
  onSubmit,
}: {
  title: string;
  warning: string;
  submit: string;
  disks: Disk[];
  busy: boolean;
  onClose: () => void;
  onSubmit: (disk: string, confirm: string) => void;
}) {
  const [id, setId] = useState(disks[0]?.id ?? "");
  const [confirm, setConfirm] = useState("");
  const disk = disks.find((d) => d.id === id);
  return (
    <Modal
      title={title}
      size="lg"
      onClose={onClose}
      onSubmit={() => disk && onSubmit(disk.id, confirm.trim())}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="danger" loading={busy} disabled={!disk || confirm.trim() === ""}>
            {submit}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Alert tone="error">{warning}</Alert>
        {disks.length === 0 ? (
          <p className="text-sm font-medium text-ink-2">No free disk can be used. A disk in use, or one with no serial and no by-id name, is left alone.</p>
        ) : (
          <>
            <SelectField label="Disk" value={id} onChange={(e) => { setId(e.target.value); setConfirm(""); }}>
              {disks.map((d) => (
                <option key={d.id} value={d.id}>
                  {diskTitle(d)} · {bytes(d.sizeBytes)} · {d.path}
                </option>
              ))}
            </SelectField>
            {disk && (
              <TextField
                label={disk.confirmKind === "by-id" ? "By-id name" : "Serial"}
                hint={confirmHint(disk)}
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                autoComplete="off"
                spellCheck={false}
                placeholder="Type it here"
              />
            )}
          </>
        )}
      </div>
    </Modal>
  );
}

function ReplaceDialog({
  pool,
  disks,
  busy,
  onClose,
  onSubmit,
}: {
  pool: Pool;
  disks: Disk[];
  busy: boolean;
  onClose: () => void;
  onSubmit: (oldId: string, disk: string, confirm: string) => void;
}) {
  const [oldId, setOldId] = useState(pool.members[0]?.id ?? "");
  const [id, setId] = useState(disks[0]?.id ?? "");
  const [confirm, setConfirm] = useState("");
  const disk = disks.find((d) => d.id === id);
  return (
    <Modal
      title={`Replace a disk in ${pool.name}`}
      size="lg"
      onClose={onClose}
      onSubmit={() => disk && onSubmit(oldId, disk.id, confirm.trim())}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="danger" loading={busy} disabled={!disk || !oldId || confirm.trim() === ""}>
            Erase and replace
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Alert tone="error">
          The new disk is erased. The pool copies the old disk onto it. This can take a long time. The old disk is left with its filesystem until the copy finishes, and FELDCORE does not wipe it afterwards.
        </Alert>
        <SelectField label="Disk to replace" value={oldId} onChange={(e) => setOldId(e.target.value)}>
          {pool.members.map((m) => (
            <option key={m.id} value={m.id}>
              {m.path}
              {m.serial ? ` · ${m.serial}` : ""}
            </option>
          ))}
        </SelectField>
        {disks.length === 0 ? (
          <p className="text-sm font-medium text-ink-2">No free disk is available to replace it with.</p>
        ) : (
          <>
            <SelectField label="New disk" value={id} onChange={(e) => { setId(e.target.value); setConfirm(""); }}>
              {disks.map((d) => (
                <option key={d.id} value={d.id}>
                  {diskTitle(d)} · {bytes(d.sizeBytes)} · {d.path}
                </option>
              ))}
            </SelectField>
            {disk && (
              <TextField label={disk.confirmKind === "by-id" ? "By-id name" : "Serial"} hint={confirmHint(disk)} value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="off" spellCheck={false} placeholder="Type it here" />
            )}
          </>
        )}
      </div>
    </Modal>
  );
}

function ForgetDialog({
  pool,
  busy,
  onClose,
  onSubmit,
}: {
  pool: Pool;
  busy: boolean;
  onClose: () => void;
  onSubmit: (confirm: string) => void;
}) {
  const [confirm, setConfirm] = useState("");
  return (
    <Modal
      title={`Forget ${pool.name}`}
      onClose={onClose}
      onSubmit={() => onSubmit(confirm.trim())}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="danger" loading={busy} disabled={confirm.trim() !== pool.name}>
            Forget the pool
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Alert tone="warn">
          FELDCORE unmounts {pool.name} and forgets it. The filesystem stays on the disks. It is not wiped. Shares placed on it, and a cache flag, have to be cleared first.
        </Alert>
        <TextField label={`Type ${pool.name} to confirm`} value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="off" spellCheck={false} placeholder={pool.name} />
      </div>
    </Modal>
  );
}

function ScheduleDialog({
  pool,
  view,
  busy,
  onClose,
  onSubmit,
}: {
  pool: Pool;
  view: View;
  busy: boolean;
  onClose: () => void;
  onSubmit: (kind: string, enabled: boolean, everyHours: number) => void;
}) {
  const kinds = useMemo(() => {
    const out = ["scrub"];
    if (pool.fs === "btrfs") out.push("balance");
    if (view.caps.trim) out.push("trim");
    return out;
  }, [pool.fs, view.caps.trim]);
  const [kind, setKind] = useState(kinds[0]);
  const existing = view.schedules.find((s) => s.pool === pool.name && s.kind === kind);
  const [enabled, setEnabled] = useState(existing?.enabled ?? false);
  const [hours, setHours] = useState(String(existing?.everyHours || 168));
  useEffect(() => {
    const cur = view.schedules.find((s) => s.pool === pool.name && s.kind === kind);
    setEnabled(cur?.enabled ?? false);
    setHours(String(cur?.everyHours || 168));
  }, [kind, pool.name, view.schedules]);
  return (
    <Modal
      title={`Schedule maintenance for ${pool.name}`}
      onClose={onClose}
      onSubmit={() => onSubmit(kind, enabled, Number(hours))}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" loading={busy}>
            Save schedule
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <p className="text-sm font-medium text-ink-2">Scrub, balance and trim run on the install job runner. They do not format disks.</p>
        <SelectField label="Job" value={kind} onChange={(e) => setKind(e.target.value)}>
          {kinds.map((k) => (
            <option key={k} value={k}>
              {KIND_LABEL[k] || k}
            </option>
          ))}
        </SelectField>
        <SelectField label="Run every" value={hours} onChange={(e) => setHours(e.target.value)}>
          <option value="24">Day</option>
          <option value="168">Week</option>
          <option value="720">30 days</option>
        </SelectField>
        <label className="flex items-center gap-2 text-sm font-bold text-ink">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          Enabled
        </label>
        {pool.fs === "xfs" && !view.caps.xfsScrub && <p className="text-xs font-medium text-ink-3">XFS scrub needs xfs_scrub, and it is not installed.</p>}
      </div>
    </Modal>
  );
}

function SharesCard({
  view,
  busy,
  onChange,
}: {
  view: View;
  busy: boolean;
  onChange: (label: string, fn: () => Promise<void>) => void;
}) {
  return (
    <Card>
      <CardTitle icon={HardDrive} title="Shares" hint="A placement does not move files" />
      {view.cachePool && (
        <p className="mt-3 text-sm font-medium text-ink-2">
          Cache pool <span className="font-bold text-ink">{view.cachePool}</span> holds appdata at {view.cachePath}. Existing files were not moved.
        </p>
      )}
      {view.shares.length === 0 ? (
        <p className="mt-3 text-sm font-medium text-ink-3">No shares yet. Create them on the Shares page, then place them on a pool here.</p>
      ) : (
        <div className="mt-4 space-y-3">
          {view.shares.map((s) => (
            <label key={s.name} className="flex flex-wrap items-center justify-between gap-3">
              <span>
                <span className="block text-sm font-bold text-ink">{s.name}</span>
                <span className="block text-xs font-medium text-ink-3">{s.path}</span>
              </span>
              <select
                className="rounded-xl border border-line bg-surface-2 px-3 py-2 text-sm font-semibold text-ink"
                disabled={busy}
                value={s.pool || ""}
                aria-label={`Pool for ${s.name}`}
                onChange={(e) => {
                  const pool = e.target.value;
                  onChange(
                    pool ? `${s.name} now points at ${pool}. Files were not moved.` : `${s.name} is back on the main disk. Files on the pool were left there.`,
                    () => api(`/storage/shares/${encodeURIComponent(s.name)}`, { method: "PUT", body: { pool } }),
                  );
                }}
              >
                <option value="">Main disk</option>
                {view.pools.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name}
                    {p.cache ? " (cache)" : ""}
                  </option>
                ))}
              </select>
            </label>
          ))}
        </div>
      )}
    </Card>
  );
}

function SpinCard({
  view,
  busy,
  onChange,
}: {
  view: View;
  busy: boolean;
  onChange: (label: string, fn: () => Promise<void>) => void;
}) {
  const disks = view.disks.filter((d) => d.type === "hdd");
  return (
    <Card>
      <CardTitle icon={HardDrive} title="Spin-down" hint={view.caps.hdparm ? "Hard disks only" : "hdparm is not installed"} />
      {disks.length === 0 ? (
        <p className="mt-3 text-sm font-medium text-ink-3">No spinning hard disks were found. Solid-state, NVMe and virtual disks are left running.</p>
      ) : (
        <div className="mt-4 space-y-3">
          {disks.map((d) => {
            const rule = view.spinDown.find((s) => s.id === d.id);
            return (
              <div key={d.id} className="flex flex-wrap items-center justify-between gap-3">
                <span>
                  <span className="block text-sm font-bold text-ink">{diskTitle(d)}</span>
                  <span className="block text-xs font-medium text-ink-3">{d.serial || d.path}</span>
                </span>
                <div className="flex gap-2">
                  <select
                    className="rounded-xl border border-line bg-surface-2 px-3 py-2 text-sm font-semibold text-ink"
                    disabled={busy || !view.caps.hdparm}
                    aria-label={`Spin-down for ${diskTitle(d)}`}
                    value={String(rule?.minutes ?? 0)}
                    onChange={(e) =>
                      onChange(`Spin-down for ${diskTitle(d)} was saved.`, () =>
                        api("/storage/spindown", { method: "PUT", body: { id: d.id, minutes: Number(e.target.value) } }),
                      )
                    }
                  >
                    {SPIN.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </select>
                  <Button
                    size="sm"
                    disabled={busy || !view.caps.hdparm}
                    onClick={() => onChange(`${diskTitle(d)} was spun down.`, () => api("/storage/spindown", { method: "POST", body: { id: d.id } }))}
                  >
                    Spin down now
                  </Button>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </Card>
  );
}

function JobsCard({ jobs }: { jobs: Job[] }) {
  return (
    <Card>
      <CardTitle icon={HardDrive} title="Maintenance" hint="Scrub, balance, trim and replace" />
      <ul className="mt-4 space-y-3">
        {jobs.map((j) => {
          const st = jobLabel(j.state);
          return (
            <li key={j.id} className="flex items-start justify-between gap-3">
              <span>
                <span className="block text-sm font-bold text-ink">{j.label || j.name}</span>
                <span className="block text-xs font-medium text-ink-3">{j.error || j.step || j.name}</span>
              </span>
              <span className={`rounded-full px-2.5 py-0.5 text-[11px] font-bold ${st.cls}`}>{st.text}</span>
            </li>
          );
        })}
      </ul>
    </Card>
  );
}
