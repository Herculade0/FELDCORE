import { useCallback, useEffect, useState } from "react";
import { HardDrive, Puzzle, RefreshCw } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { Card, CardTitle } from "./ui";
import { Alert, Button, TextField } from "./form";
import { InstallPanel, showInstall, useInstall } from "./install/InstallPanel";
import { PageHelp } from "./help/PageHelp";

interface Slot {
  id: string;
  version?: string;
  booted: boolean;
  triesLeft?: number;
  good: boolean;
  present: boolean;
}

interface Release {
  version: string;
  channel?: string;
  released?: string;
  notes?: string;
  arch?: string;
  kernel?: string;
}

interface Sysext {
  id: string;
  version?: string;
  kernel?: string;
  notes?: string;
  installed: boolean;
  available: boolean;
  source?: string;
  matches: boolean;
  reason?: string;
}

interface OSStatus {
  appliance: boolean;
  version: string;
  daemonVersion: string;
  kernel: string;
  slot?: string;
  secureBoot: string;
  slots: Slot[];
  previous?: Slot;
  nextBoot?: string;
  nextVersion?: string;
  nextTries?: number;
  manifestURL: string;
  urlLocked: boolean;
  pubKey?: string;
  pubKeyFound: boolean;
  available?: Release;
  scheduleAt?: string;
  sysexts: Sysext[];
  warning?: string;
}

function when(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function localInput(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function slotLabel(id?: string): string {
  return id ? id.toUpperCase() : "—";
}

export default function UpdatesPage() {
  const [status, setStatus] = useState<OSStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [checking, setChecking] = useState(false);
  const [source, setSource] = useState("");
  const [whenAt, setWhenAt] = useState("");
  const [saving, setSaving] = useState(false);
  const [rebooting, setRebooting] = useState(false);

  const load = useCallback(() => {
    setLoading(true);
    api<OSStatus>("/os")
      .then((next) => {
        setStatus(next);
        setSource(next.manifestURL);
        setWhenAt(localInput(next.scheduleAt));
        setError(null);
      })
      .catch((e) => setError(errorMessage(e)))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const update = useInstall("os-update", () => void load());
  const rollback = useInstall("os-rollback", () => void load());

  const check = async () => {
    setChecking(true);
    setError(null);
    try {
      await api<Release>("/os/check", { method: "POST" });
      load();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setChecking(false);
    }
  };

  const saveSource = async () => {
    setSaving(true);
    setError(null);
    try {
      const next = await api<OSStatus>("/os/source", { method: "PUT", body: { url: source.trim() } });
      setStatus(next);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const saveSchedule = async (at: string) => {
    setSaving(true);
    setError(null);
    try {
      const next = await api<OSStatus>("/os/schedule", { method: "PUT", body: { at } });
      setStatus(next);
      setWhenAt(localInput(next.scheduleAt));
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const reboot = async () => {
    setRebooting(true);
    setError(null);
    try {
      await api("/os/reboot", { method: "POST" });
    } catch (e) {
      setError(errorMessage(e));
      setRebooting(false);
    }
  };

  const appliance = status?.appliance ?? false;
  const pending =
    !!status?.nextBoot && !!status.slot && status.nextBoot !== status.slot;
  const sameRelease =
    !!status?.available && status.available.version === status.version;

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">Updates</h1>
          <p className="mt-2 max-w-xl text-sm font-semibold text-ink-2">
            FELDCORE OS replaces a whole disk slot. Nothing is installed with apt onto the running system. A new image gets three boots, then the previous image is selected automatically.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <PageHelp topic="updates-rollback" />
          <Button variant="secondary" size="sm" loading={loading} onClick={load}>
            <RefreshCw size={14} />
            Refresh
          </Button>
        </div>
      </header>

      {error && <Alert>{error}</Alert>}
      {status && !appliance && (
        <Alert tone="warn">
          This machine is not running the FELDCORE OS image. You can check a signed release here. Staging, rollback, and extensions run on the appliance.
        </Alert>
      )}
      {status?.warning && <Alert tone="warn">{status.warning}</Alert>}

      <Card>
        <CardTitle icon={HardDrive} title="This image" hint={status ? `Secure Boot ${status.secureBoot}` : undefined} />
        <dl className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Fact label="Running" value={status ? `${status.version} · slot ${slotLabel(status.slot)}` : "—"} />
          <Fact label="Next boot" value={status?.nextVersion ? `${status.nextVersion} · slot ${slotLabel(status.nextBoot)}` : "—"} />
          <Fact
            label="Tries left"
            value={status?.nextTries === undefined ? "Good" : String(status.nextTries)}
          />
          <Fact label="Kernel" value={status?.kernel || "—"} />
        </dl>
        {status && status.slots.length > 0 && (
          <ul className="mt-4 divide-y divide-line overflow-hidden rounded-2xl border border-line">
            {status.slots.map((slot) => (
              <li key={slot.id} className="flex flex-wrap items-center justify-between gap-2 px-4 py-3 text-sm">
                <span className="font-extrabold text-ink">Slot {slotLabel(slot.id)}</span>
                <span className="font-semibold text-ink-2">
                  {slot.present ? slot.version || "Empty" : "Not on this disk"}
                  {slot.booted ? " · booted" : ""}
                  {slot.triesLeft !== undefined ? ` · ${slot.triesLeft} tries` : ""}
                </span>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card>
        <CardTitle icon={RefreshCw} title="Release" hint={status?.available?.channel} />
        <div className="mt-4 space-y-4">
          {status?.available ? (
            <div>
              <p className="text-lg font-extrabold text-ink">{status.available.version}</p>
              {status.available.released && (
                <p className="text-xs font-semibold text-ink-3">{when(status.available.released)}</p>
              )}
              {status.available.notes && (
                <pre className="mt-3 max-h-48 overflow-auto whitespace-pre-wrap rounded-2xl bg-surface-2 px-4 py-3 text-sm font-medium text-ink-2">
                  {status.available.notes}
                </pre>
              )}
            </div>
          ) : (
            <p className="text-sm font-semibold text-ink-2">No signed release has been checked yet.</p>
          )}

          <div className="flex flex-wrap gap-2">
            <Button variant="secondary" loading={checking} onClick={() => void check()}>
              Check for updates
            </Button>
            <Button
              variant="primary"
              loading={update.starting}
              disabled={!appliance || update.active || sameRelease || !status?.available}
              onClick={() => void update.start().catch(() => {})}
            >
              Update now
            </Button>
            <Button
              variant="secondary"
              loading={rollback.starting}
              disabled={!appliance || !status?.previous || rollback.active}
              onClick={() => void rollback.start().catch(() => {})}
            >
              {status?.previous?.version
                ? `Roll back to ${status.previous.version}`
                : "Roll back"}
            </Button>
            {appliance && pending && (
              <Button variant="danger" loading={rebooting} onClick={() => void reboot()}>
                Restart into slot {slotLabel(status?.nextBoot)}
              </Button>
            )}
          </div>
          {update.error && <Alert>{update.error}</Alert>}
          {rollback.error && <Alert>{rollback.error}</Alert>}
          {showInstall(update.job) && update.job && (
            <InstallPanel
              job={update.job}
              title="Update"
              doneLabel="Staged"
              success={`Version ${status?.available?.version ?? ""} is staged. Restart to boot it. It has three tries.`}
              busy={update.starting}
              onRetry={() => void update.start().catch(() => {})}
            />
          )}
          {showInstall(rollback.job) && rollback.job && (
            <InstallPanel
              job={rollback.job}
              title="Rollback"
              doneLabel="Ready"
              success="The previous image is selected for the next boot. Restart to use it."
              busy={rollback.starting}
              onRetry={() => void rollback.start().catch(() => {})}
            />
          )}

          <div className="grid gap-3 border-t border-line pt-4 sm:grid-cols-[1fr_auto] sm:items-end">
            <TextField
              label="Install at"
              type="datetime-local"
              value={whenAt}
              onChange={(e) => setWhenAt(e.target.value)}
              hint={status?.scheduleAt ? `Scheduled for ${when(status.scheduleAt)}.` : "One time. The schedule clears after it starts."}
            />
            <div className="flex gap-2">
              <Button
                variant="secondary"
                loading={saving}
                disabled={!whenAt}
                onClick={() => {
                  const d = new Date(whenAt);
                  if (Number.isNaN(d.getTime())) return;
                  void saveSchedule(d.toISOString());
                }}
              >
                Schedule
              </Button>
              <Button variant="ghost" disabled={!status?.scheduleAt} onClick={() => void saveSchedule("")}>
                Clear
              </Button>
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end">
            <TextField
              label="Release address"
              value={source}
              disabled={status?.urlLocked}
              onChange={(e) => setSource(e.target.value)}
              hint={
                status?.urlLocked
                  ? "Set by the system."
                  : status?.pubKeyFound
                    ? `Signed with ${status.pubKey}`
                    : "No public key found yet."
              }
            />
            {!status?.urlLocked && (
              <Button variant="secondary" loading={saving} onClick={() => void saveSource()}>
                Save address
              </Button>
            )}
          </div>
        </div>
      </Card>

      <Card>
        <CardTitle icon={Puzzle} title="Extensions" hint="systemd-sysext" />
        <p className="mt-3 text-sm font-semibold text-ink-2">
          Drivers and extras live in signed images on the state partition. NVIDIA, the container toolkit, and libvirt are extensions when a release offers them. They are not installed with apt.
        </p>
        <ul className="mt-4 space-y-3">
          {(status?.sysexts ?? []).length === 0 && (
            <li className="text-sm font-semibold text-ink-3">No extensions are installed or offered.</li>
          )}
          {(status?.sysexts ?? []).map((sx) => (
            <SysextRow key={sx.id} sx={sx} appliance={appliance} onDone={() => void load()} />
          ))}
        </ul>
      </Card>
    </div>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-2xl bg-surface-2 px-4 py-3">
      <dt className="text-[11px] font-bold uppercase tracking-wider text-ink-3">{label}</dt>
      <dd className="mt-1 truncate text-sm font-extrabold text-ink">{value}</dd>
    </div>
  );
}

function SysextRow({
  sx,
  appliance,
  onDone,
}: {
  sx: Sysext;
  appliance: boolean;
  onDone: () => void;
}) {
  const install = useInstall(`os-sysext-${sx.id}`, onDone);
  const remove = useInstall(`os-sysext-remove-${sx.id}`, onDone);
  const busy = install.active || remove.active || install.starting || remove.starting;
  return (
    <li className="rounded-2xl border border-line px-4 py-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <p className="font-extrabold text-ink">{sx.id}</p>
          <p className="text-xs font-semibold text-ink-3">
            {sx.version || "No version"}
            {sx.kernel ? ` · kernel ${sx.kernel}` : " · any kernel"}
            {sx.installed ? ` · ${sx.source === "image" ? "in the image" : "on the state partition"}` : ""}
            {sx.available && !sx.installed ? " · available" : ""}
          </p>
          {sx.notes && <p className="mt-1 text-sm font-medium text-ink-2">{sx.notes}</p>}
          {!sx.matches && sx.reason && <p className="mt-1 text-sm font-semibold text-warn">{sx.reason}</p>}
        </div>
        <div className="flex gap-2">
          {sx.available && (
            <Button
              size="sm"
              variant="primary"
              loading={install.starting}
              disabled={!appliance || !sx.matches || busy}
              onClick={() => void install.start({ path: `/os/sysexts/${sx.id}` }).catch(() => {})}
            >
              {sx.installed ? "Reinstall" : "Install"}
            </Button>
          )}
          {sx.installed && sx.source === "state" && (
            <Button
              size="sm"
              variant="danger"
              loading={remove.starting}
              disabled={!appliance || busy}
              onClick={() => void remove.start({ path: `/os/sysexts/${sx.id}`, method: "DELETE" }).catch(() => {})}
            >
              Remove
            </Button>
          )}
        </div>
      </div>
      {install.error && (
        <div className="mt-3">
          <Alert>{install.error}</Alert>
        </div>
      )}
      {remove.error && (
        <div className="mt-3">
          <Alert>{remove.error}</Alert>
        </div>
      )}
      {showInstall(install.job) && install.job && (
        <div className="mt-3">
          <InstallPanel
            job={install.job}
            title="Extension"
            doneLabel="Installed"
            success={`${sx.id} is installed.`}
            busy={install.starting}
            onRetry={() => void install.start({ path: `/os/sysexts/${sx.id}` }).catch(() => {})}
          />
        </div>
      )}
      {showInstall(remove.job) && remove.job && (
        <div className="mt-3">
          <InstallPanel
            job={remove.job}
            title="Extension"
            doneLabel="Removed"
            success={`${sx.id} was removed.`}
            busy={remove.starting}
          />
        </div>
      )}
    </li>
  );
}
