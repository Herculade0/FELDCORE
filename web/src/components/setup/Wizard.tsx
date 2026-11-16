import { useEffect, useState } from "react";
import { api, errorMessage } from "../../lib/api";
import { Alert, Button, SelectField, TextField, Toggle } from "../form";
import { LogoMark, Wordmark } from "../Logo";

interface Mount {
  path: string;
  device: string;
  free: number;
  total: number;
  os: boolean;
}

interface WizardView {
  done: boolean;
  step: string;
  steps: string[];
  preview?: string[];
  warning?: string;
  hostname: string;
  timezone: string;
  zones?: string[];
  ntp: string;
  iface: string;
  ip: string;
  gateway: string;
  dns?: string[];
  netMode: string;
  backend: string;
  newUrl?: string;
  netToken?: string;
  netWait?: boolean;
  mounts?: Mount[];
  sharesRoot: string;
  onOsDisk: boolean;
  virt: boolean;
  samba: boolean;
  nfs: boolean;
  docker: boolean;
  libvirt: boolean;
  pkgLog?: string;
  pkgRunning?: boolean;
  pkgError?: string;
  shareNames?: string[];
  sshPorts?: number[];
  listenPort: number;
  unattended: boolean;
  firewall: boolean;
  updateCheck: boolean;
  summary?: string[];
}

const LABELS: Record<string, string> = {
  host: "This server",
  network: "Network",
  storage: "Storage",
  packages: "Software",
  shares: "Shares",
  security: "Security",
  updates: "Updates",
  summary: "Summary",
};

const SHARE_HELP: Record<string, string> = {
  appdata: "Docker settings and app data",
  system: "Docker and VM system files",
  domains: "Virtual machine disks",
  isos: "ISO images",
};

function onOs(path: string, mounts: Mount[] | undefined): boolean {
  if (!mounts || !path.startsWith("/")) return false;
  const p = path !== "/" && path.endsWith("/") ? path.slice(0, -1) : path;
  let best = "";
  let os = false;
  for (const m of mounts) {
    const mp = m.path === "/" ? "/" : m.path.replace(/\/$/, "");
    const hit = mp === "/" ? p.startsWith("/") : p === mp || p.startsWith(`${mp}/`);
    if (hit && mp.length >= best.length) {
      best = mp;
      os = m.os;
    }
  }
  return os;
}

function bytes(n: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
}

export default function Wizard({ onDone }: { onDone: () => void }) {
  const [view, setView] = useState<WizardView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [host, setHost] = useState("");
  const [tz, setTz] = useState("UTC");
  const [ntp, setNtp] = useState("systemd-timesyncd");
  const [netMode, setNetMode] = useState("dhcp");
  const [address, setAddress] = useState("");
  const [gateway, setGateway] = useState("");
  const [dns, setDns] = useState("");
  const [root, setRoot] = useState("/srv/feldcore");
  const [samba, setSamba] = useState(true);
  const [nfs, setNfs] = useState(true);
  const [docker, setDocker] = useState(true);
  const [libvirt, setLibvirt] = useState(false);
  const [shares, setShares] = useState<string[]>(["appdata", "system", "domains", "isos"]);
  const [unattended, setUnattended] = useState(true);
  const [firewall, setFirewall] = useState(false);
  const [updates, setUpdates] = useState(false);

  const load = async () => {
    const v = await api<WizardView>("/setup/wizard");
    setView(v);
    if (v.done) onDone();
    return v;
  };

  useEffect(() => {
    void load().catch((err) => setError(errorMessage(err)));
    // The first load fills the form; later polls must not wipe what is typed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!view) return;
    setHost(view.hostname || "");
    setTz(view.timezone || "UTC");
    setNtp(view.ntp || "systemd-timesyncd");
    setNetMode("dhcp");
    setGateway(view.gateway || "");
    setDns((view.dns ?? []).join(" "));
    setRoot(view.sharesRoot || "/srv/feldcore");
    setSamba(view.samba);
    setNfs(view.nfs);
    setDocker(view.docker);
    setLibvirt(view.libvirt && view.virt);
    setShares(view.shareNames ?? ["appdata", "system", "domains", "isos"]);
    setUnattended(view.unattended);
    setFirewall(view.firewall);
    setUpdates(view.updateCheck);
  }, [view?.step]);

  useEffect(() => {
    if (!view?.pkgRunning) return;
    const t = setInterval(() => {
      void load().catch(() => {});
    }, 1000);
    return () => clearInterval(t);
  }, [view?.pkgRunning]);

  const body = () => ({
    step: view?.step,
    hostname: host,
    timezone: tz,
    ntp,
    netMode,
    iface: view?.iface ?? "",
    address,
    gateway,
    dns,
    sharesRoot: root,
    samba,
    nfs,
    docker,
    libvirt: libvirt && Boolean(view?.virt),
    shareNames: shares,
    unattended,
    firewall,
    updateCheck: updates,
  });

  const send = async (extra: { confirm?: boolean; back?: boolean }) => {
    setBusy(true);
    setError(null);
    try {
      const next = await api<WizardView>("/setup/wizard", {
        method: "POST",
        body: { ...body(), ...extra },
      });
      setView(next);
      if (next.done) onDone();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  if (!view) {
    return (
      <div className="flex min-h-screen items-center justify-center text-sm font-semibold text-ink-2">
        {error ?? "Loading setup…"}
      </div>
    );
  }

  const step = view.step;
  const zones = view.zones && view.zones.length > 0 ? view.zones : ["UTC"];
  const zoneOptions = zones.includes(tz) ? zones : [tz, ...zones];

  return (
    <div className="min-h-screen px-4 py-8">
      <div className="mx-auto w-full max-w-3xl">
        <div className="mb-6 flex items-center gap-3">
          <LogoMark size={36} />
          <Wordmark />
        </div>
        <ol className="mb-4 flex flex-wrap gap-2">
          {view.steps.map((id, i) => {
            const current = id === step;
            const past = view.steps.indexOf(step) > i;
            return (
              <li
                key={id}
                className={`rounded-full px-3 py-1 text-xs font-bold ${
                  current ? "accent-gradient text-white" : past ? "bg-accent/15 text-accent" : "bg-surface-2 text-ink-3"
                }`}
              >
                {i + 1}. {LABELS[id] ?? id}
              </li>
            );
          })}
        </ol>

        <div className="glass min-w-0 max-w-full rounded-[2rem] p-6 sm:p-8">
          {step === "host" && (
            <section>
              <h1 className="text-xl font-extrabold text-ink">This server</h1>
              <p className="mt-2 text-sm font-medium leading-relaxed text-ink-2">
                Set the name, the time zone, and which service keeps the clock in sync.
              </p>
              <div className="mt-5 space-y-4">
                <TextField label="Host name" value={host} onChange={(e) => setHost(e.target.value)} required />
                <SelectField label="Time zone" value={tz} onChange={(e) => setTz(e.target.value)}>
                  {zoneOptions.map((z) => (
                    <option key={z} value={z}>
                      {z}
                    </option>
                  ))}
                </SelectField>
                <SelectField label="Network time" value={ntp} onChange={(e) => setNtp(e.target.value)}>
                  <option value="systemd-timesyncd">systemd-timesyncd</option>
                  <option value="chrony">chrony</option>
                </SelectField>
              </div>
            </section>
          )}

          {step === "network" && (
            <section>
              <h1 className="text-xl font-extrabold text-ink">Network</h1>
              <p className="mt-2 text-sm font-medium leading-relaxed text-ink-2 [overflow-wrap:anywhere]">
                {view.iface
                  ? `Using ${view.iface} at ${view.ip || "an unknown address"}${view.gateway ? `, gateway ${view.gateway}` : ""}.`
                  : "No active interface was detected."}{" "}
                DHCP is the default and does not change the address you are using now.
              </p>
              <div className="mt-5 space-y-4">
                <SelectField label="Address" value={netMode} onChange={(e) => setNetMode(e.target.value)}>
                  <option value="dhcp">Keep DHCP</option>
                  <option value="static">Set a static address</option>
                </SelectField>
                {netMode === "static" && (
                  <>
                    <TextField
                      label="Address"
                      value={address}
                      onChange={(e) => setAddress(e.target.value)}
                      placeholder="192.168.1.20/24"
                      hint="IPv4 address and prefix. The new page is shown before anything changes, and the old address comes back if you do not confirm within 90 seconds."
                    />
                    <TextField label="Gateway" value={gateway} onChange={(e) => setGateway(e.target.value)} />
                    <TextField label="DNS" value={dns} onChange={(e) => setDns(e.target.value)} placeholder="1.1.1.1 8.8.8.8" />
                  </>
                )}
                {view.netWait && view.newUrl && (
                  <Alert tone="warn">
                    Open {view.newUrl}
                    {view.netToken ? `?net=${view.netToken}` : ""} within 90 seconds. If that page does not load, wait and this address will be restored.
                  </Alert>
                )}
              </div>
            </section>
          )}

          {step === "storage" && (
            <section>
              <h1 className="text-xl font-extrabold text-ink">Storage</h1>
              <p className="mt-2 text-sm font-medium leading-relaxed text-ink-2">
                Choose the folder shares are created in. FELDCORE does not partition or format anything.
              </p>
              <div className="mt-5 space-y-3">
                {(view.mounts ?? []).map((m) => (
                  <button
                    key={m.path}
                    type="button"
                    onClick={() => setRoot(m.path === "/" ? "/srv/feldcore" : m.path)}
                    className="flex w-full min-w-0 items-center justify-between gap-3 rounded-2xl border border-line bg-surface-2 px-4 py-3 text-left"
                  >
                    <span className="min-w-0">
                      <span className="block text-sm font-bold text-ink [overflow-wrap:anywhere]">{m.path}</span>
                      <span className="block text-xs font-medium text-ink-3 [overflow-wrap:anywhere]">
                        {m.device}
                        {m.os ? " · operating-system disk" : ""}
                      </span>
                    </span>
                    <span className="shrink-0 text-xs font-bold text-ink-2">
                      {bytes(m.free)} free
                    </span>
                  </button>
                ))}
                <TextField
                  label="Shares folder"
                  value={root}
                  onChange={(e) => setRoot(e.target.value)}
                  hint="A path on one of the disks above, for example /mnt/data/feldcore."
                />
                {onOs(root, view.mounts) && (
                  <Alert tone="warn">
                    This folder is on the operating-system disk. Filling it can stop the server from booting. A separate disk is safer.
                  </Alert>
                )}
              </div>
            </section>
          )}

          {step === "packages" && (
            <section>
              <h1 className="text-xl font-extrabold text-ink">Software</h1>
              <p className="mt-2 text-sm font-medium leading-relaxed text-ink-2">
                Install what this server will run. Already-installed packages are left as they are, and a failed install can be started again.
              </p>
              <div className="mt-5 space-y-3">
                <Check label="Samba" hint="File sharing for Windows and macOS" checked={samba} onChange={setSamba} />
                <Check label="NFS server" hint="File sharing for Linux and macOS" checked={nfs} onChange={setNfs} />
                <Check label="Docker" hint="Runs the apps from the App Store" checked={docker} onChange={setDocker} />
                <Check
                  label="libvirt / KVM"
                  hint={view.virt ? "This CPU can run virtual machines." : "This CPU does not report virtualisation, so this stays off."}
                  checked={libvirt && view.virt}
                  onChange={setLibvirt}
                  disabled={!view.virt}
                />
              </div>
              {(view.pkgLog || view.pkgRunning || view.pkgError) && (
                <pre className="mt-4 max-h-48 overflow-auto whitespace-pre-wrap rounded-2xl bg-surface-2 p-4 text-xs font-medium text-ink [overflow-wrap:anywhere]">
                  {view.pkgLog || "Working…"}
                </pre>
              )}
              {view.pkgError && <Alert>{view.pkgError}</Alert>}
            </section>
          )}

          {step === "shares" && (
            <section>
              <h1 className="text-xl font-extrabold text-ink">Default shares</h1>
              <p className="mt-2 text-sm font-medium leading-relaxed text-ink-2">
                These are the four shares Unraid creates. They are created on disk but not exported, so nothing is shared until you turn SMB or NFS on later. Existing folders are kept as they are.
              </p>
              <div className="mt-5 space-y-3">
                {["appdata", "system", "domains", "isos"].map((name) => (
                  <Check
                    key={name}
                    label={name}
                    hint={SHARE_HELP[name]}
                    checked={shares.includes(name)}
                    onChange={(on) =>
                      setShares((cur) => (on ? [...cur, name] : cur.filter((n) => n !== name)))
                    }
                  />
                ))}
              </div>
            </section>
          )}

          {step === "security" && (
            <section>
              <h1 className="text-xl font-extrabold text-ink">Security</h1>
              <p className="mt-2 text-sm font-medium leading-relaxed text-ink-2">
                SSH is left completely alone. A headless server almost certainly depends on it. If you turn the firewall on, ufw allows TCP {view.listenPort} and the SSH
                {view.sshPorts && view.sshPorts.length ? ` port${view.sshPorts.length > 1 ? "s" : ""} ${view.sshPorts.join(", ")}` : " port 22"} that is listening now, before it denies other incoming connections. sshd's configuration is not edited, and SSH is never switched off.
              </p>
              <div className="mt-5 space-y-3">
                <Check
                  label="Install security updates automatically"
                  hint="Uses Debian's unattended-upgrades, limited to security updates."
                  checked={unattended}
                  onChange={setUnattended}
                />
                <Check
                  label="Turn on the ufw firewall"
                  hint="Denies other incoming connections. Outgoing traffic stays allowed. You can leave this off."
                  checked={firewall}
                  onChange={setFirewall}
                />
              </div>
            </section>
          )}

          {step === "updates" && (
            <section>
              <h1 className="text-xl font-extrabold text-ink">FELDCORE updates</h1>
              <p className="mt-2 text-sm font-medium leading-relaxed text-ink-2">
                Optional. When this is on, FELDCORE checks GitHub for a newer release and tells you. It does not download or install anything by itself.
              </p>
              <div className="mt-5">
                <Check label="Check for FELDCORE updates" hint="Off leaves the server as it was installed." checked={updates} onChange={setUpdates} />
              </div>
            </section>
          )}

          {step === "summary" && (
            <section>
              <h1 className="text-xl font-extrabold text-ink">Ready</h1>
              <ul className="mt-4 space-y-2 text-sm font-medium text-ink-2">
                {(view.summary ?? []).map((line) => (
                  <li key={line} className="[overflow-wrap:anywhere]">
                    {line}
                  </li>
                ))}
              </ul>
            </section>
          )}

          {view.warning && (
            <div className="mt-4">
              <Alert tone="warn">{view.warning}</Alert>
            </div>
          )}
          {view.preview && view.preview.length > 0 && (
            <div className="mt-4 rounded-2xl border border-line bg-surface-2 p-4">
              <p className="text-xs font-bold uppercase tracking-wide text-ink-3">This will</p>
              <ul className="mt-2 space-y-1 text-sm font-medium text-ink">
                {view.preview.map((line) => (
                  <li key={line} className="[overflow-wrap:anywhere]">
                    {line}
                  </li>
                ))}
              </ul>
            </div>
          )}
          {error && (
            <div className="mt-4">
              <Alert>{error}</Alert>
            </div>
          )}

          <div className="mt-6 flex flex-wrap gap-2">
            {step !== "host" && (
              <Button disabled={busy || view.pkgRunning} onClick={() => void send({ back: true })}>
                Back
              </Button>
            )}
            <Button disabled={busy || view.pkgRunning} onClick={() => void send({ confirm: false })}>
              Review
            </Button>
            <Button variant="primary" loading={busy || view.pkgRunning} onClick={() => void send({ confirm: true })}>
              {step === "summary" ? "Open the dashboard" : "Apply and continue"}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

function Check({
  label,
  hint,
  checked,
  onChange,
  disabled,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <div className={`flex items-start gap-3 rounded-2xl border border-line bg-surface-2 px-4 py-3 ${disabled ? "opacity-60" : ""}`}>
      <Toggle checked={checked} onChange={onChange} label={label} disabled={disabled} />
      <div className="min-w-0">
        <p className="text-sm font-bold text-ink">{label}</p>
        {hint && <p className="text-xs font-medium leading-relaxed text-ink-3 [overflow-wrap:anywhere]">{hint}</p>}
      </div>
    </div>
  );
}
